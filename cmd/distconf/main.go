package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/ttab/clitools"
	"github.com/ttab/distconf"
	"github.com/ttab/elephantine"
	"github.com/urfave/cli/v3"
)

const appName = "distconf"

var version = "dev"

func main() {
	err := clitools.LoadEnv(appName)
	if err != nil {
		slog.Error("exiting",
			elephantine.LogKeyError, err)
		os.Exit(1)
	}

	versionCmd := cli.Command{
		Name: "version",
		Action: func(_ context.Context, _ *cli.Command) error {
			fmt.Fprintln(os.Stdout, version)

			return nil
		},
	}

	updateCmd := cli.Command{
		Name:        "update",
		Description: "Refresh schema lockfile",
		Action:      updateAction,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "dir",
				Usage: "Configuration directory",
				Value: ".",
			},
		},
	}

	authFlags := []cli.Flag{
		&cli.StringFlag{
			Name:    "env",
			Sources: cli.EnvVars("ENV"),
		},
		&cli.StringFlag{
			Name:    "client-id",
			Usage:   "Client ID",
			Sources: cli.EnvVars("CLIENT_ID"),
		},
		&cli.StringFlag{
			Name:    "client-secret",
			Usage:   "Client secret",
			Sources: cli.EnvVars("CLIENT_SECRET"),
		},
	}

	applyCmd := cli.Command{
		Name:        "apply",
		Description: "Register and activate a new config generation",
		Action:      applyAction,
		Flags: append([]cli.Flag{
			&cli.StringFlag{
				Name:  "dir",
				Usage: "Configuration directory",
				Value: ".",
			},
			&cli.StringFlag{
				Name:  "description",
				Usage: "Human-readable label for the new generation",
			},
		}, authFlags...),
	}

	configureCmd := clitools.ConfigureCliCommands(
		appName, clitools.DefaultApplicationID)

	cmd := cli.Command{
		Name:  "distconf",
		Usage: "Distribution configuration tool",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "env",
				Sources: cli.EnvVars("ENV"),
			},
		},
		Commands: []*cli.Command{
			&versionCmd,
			&updateCmd,
			&applyCmd,
			syncCommand(authFlags),
			configureCmd,
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func updateAction(ctx context.Context, c *cli.Command) error {
	dir := c.String("dir")

	conf, err := distconf.ReadConfigFromDirectory(dir)
	if err != nil {
		return fmt.Errorf("read configuration: %w", err)
	}

	lock, err := distconf.LoadLockFile(distconf.LockFilePath(dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load lock file: %w", err)
	}

	var schemas []distconf.LoadedSchema

	for _, set := range conf.SchemaSets {
		loaded, err := distconf.LoadSchemaSet(ctx, set, lock, true)
		if err != nil {
			return fmt.Errorf(
				"load schema set %q: %w", set.Name, err)
		}

		schemas = append(schemas, loaded...)
	}

	lock = distconf.NewSchemaLockFile(schemas)

	err = lock.Save(distconf.LockFilePath(dir))
	if err != nil {
		return fmt.Errorf("save lock file: %w", err)
	}

	return nil
}

func applyAction(ctx context.Context, c *cli.Command) error {
	dir := c.String("dir")
	description := c.String("description")

	conf, err := distconf.ReadConfigFromDirectory(dir)
	if err != nil {
		return fmt.Errorf("read configuration: %w", err)
	}

	lock, err := distconf.LoadLockFile(distconf.LockFilePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("missing lock file, run distconf update")
	} else if err != nil {
		return fmt.Errorf("load lock file: %w", err)
	}

	var schemas []distconf.LoadedSchema

	for _, set := range conf.SchemaSets {
		loaded, err := distconf.LoadSchemaSet(ctx, set, lock, false)
		if err != nil {
			return fmt.Errorf(
				"load schema set %q: %w", set.Name, err)
		}

		schemas = append(schemas, loaded...)
	}

	clients, err := getClients(ctx, c)
	if err != nil {
		return fmt.Errorf("get API clients: %w", err)
	}

	plan, err := distconf.BuildPlan(ctx, clients, conf, schemas, description)
	if err != nil {
		return fmt.Errorf("build plan: %w", err)
	}

	printChanges(plan.Changes)

	fmt.Fprintln(os.Stdout)

	if plan.CurrentGenerationID != 0 {
		fmt.Fprintf(os.Stdout,
			"Current active generation: %d\n",
			plan.CurrentGenerationID)
	} else {
		fmt.Fprintln(os.Stdout, "No generation is currently active.")
	}

	if !plan.HasChanges() && plan.CurrentGenerationID != 0 {
		fmt.Fprintln(os.Stdout, "No changes needed")

		return nil
	}

	if !askForConfirmation(
		"Register a new generation and activate it?") {
		return errors.New("aborted by user")
	}

	gen, err := plan.Execute(ctx, clients)
	if err != nil {
		return fmt.Errorf("apply generation: %w", err)
	}

	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout,
		"Activated generation %d (%d schemas, %d type configurations)\n",
		gen.Id, len(gen.Schemas), len(gen.Types))

	return nil
}

func printChanges(changes []distconf.ConfigurationChange) {
	if len(changes) == 0 {
		fmt.Fprintln(os.Stdout, "No changes since the active generation.")

		return
	}

	for _, change := range changes {
		op, info := change.Describe()

		col := color.New()

		switch op {
		case distconf.OpAdd:
			col.Add(color.FgGreen)
		case distconf.OpUpdate:
			col.Add(color.FgYellow)
		case distconf.OpRemove:
			col.Add(color.FgRed)
		}

		_, _ = col.Printf("%s ", op)

		fmt.Fprintln(os.Stdout, info)

		for _, msg := range change.Warnings() {
			warnCol := color.New(color.FgWhite, color.BgRed)

			_, _ = warnCol.Print(" Warning: ")

			fmt.Fprintf(os.Stdout, " %s\n", msg)
		}
	}
}

func askForConfirmation(s string) bool {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Fprintf(os.Stdout, "%s [y/n]: ", s)

		response, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintln(os.Stderr, err)

			return false
		}

		response = strings.ToLower(strings.TrimSpace(response))

		switch response {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
	}
}
