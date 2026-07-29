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
	"github.com/ttab/distconf/distribution"
	"github.com/ttab/distconf/live"
	"github.com/ttab/eleconf"
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

	distributionCmd := cli.Command{
		Name:        "distribution",
		Description: "Commands specific to the distribution service",
		Commands: []*cli.Command{
			syncCommand(authFlags),
		},
	}

	configureCmd := clitools.ConfigureCliCommands(
		appName, clitools.DefaultApplicationID)

	cmd := cli.Command{
		Name:  "distconf",
		Usage: "Elephant configuration tool",
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
			&distributionCmd,
			configureCmd,
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// serviceConfig is a configuration directory parsed with the loader for
// the service its configuration block targets. Exactly one of the
// service fields is set.
type serviceConfig struct {
	SchemaSets   []eleconf.SchemaSet
	Distribution *distribution.Config
	Live         *live.Config
}

// loadServiceConfig identifies the service a configuration directory
// targets and parses it with the corresponding config loader.
func loadServiceConfig(dir string) (*serviceConfig, error) {
	info, err := distconf.ReadDirectoryInfo(dir)
	if err != nil {
		return nil, fmt.Errorf("read configuration: %w", err)
	}

	switch info.Configuration.Service {
	case distribution.ServiceName:
		conf, err := distribution.ReadConfigFromDirectory(dir)
		if err != nil {
			return nil, fmt.Errorf("read configuration: %w", err)
		}

		return &serviceConfig{
			SchemaSets:   conf.SchemaSets,
			Distribution: conf,
		}, nil
	case live.ServiceName:
		conf, err := live.ReadConfigFromDirectory(dir)
		if err != nil {
			return nil, fmt.Errorf("read configuration: %w", err)
		}

		return &serviceConfig{
			SchemaSets: conf.SchemaSets,
			Live:       conf,
		}, nil
	default:
		return nil, fmt.Errorf(
			"the configuration targets the unknown service %q",
			info.Configuration.Service)
	}
}

// loadSchemas loads the schemas of all schema sets, validating against
// the lockfile unless init is set.
func loadSchemas(
	ctx context.Context,
	sets []eleconf.SchemaSet,
	lock *eleconf.SchemaLockfile,
	init bool,
) ([]distconf.LoadedSchema, error) {
	var schemas []distconf.LoadedSchema

	for _, set := range sets {
		loaded, err := distconf.LoadSchemaSet(ctx, set, lock, init)
		if err != nil {
			return nil, fmt.Errorf(
				"load schema set %q: %w", set.Name, err)
		}

		schemas = append(schemas, loaded...)
	}

	return schemas, nil
}

func updateAction(ctx context.Context, c *cli.Command) error {
	dir := c.String("dir")

	conf, err := loadServiceConfig(dir)
	if err != nil {
		return err
	}

	lock, err := distconf.LoadLockFile(distconf.LockFilePath(dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load lock file: %w", err)
	}

	schemas, err := loadSchemas(ctx, conf.SchemaSets, lock, true)
	if err != nil {
		return err
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

	conf, err := loadServiceConfig(dir)
	if err != nil {
		return err
	}

	lock, err := distconf.LoadLockFile(distconf.LockFilePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("missing lock file, run distconf update")
	} else if err != nil {
		return fmt.Errorf("load lock file: %w", err)
	}

	schemas, err := loadSchemas(ctx, conf.SchemaSets, lock, false)
	if err != nil {
		return err
	}

	switch {
	case conf.Distribution != nil:
		clients, err := getDistributionClients(ctx, c)
		if err != nil {
			return fmt.Errorf("get API clients: %w", err)
		}

		plan, err := distribution.BuildPlan(
			ctx, clients, conf.Distribution, schemas, description)
		if err != nil {
			return fmt.Errorf("build plan: %w", err)
		}

		return executePlan(plan.Changes, plan.CurrentGenerationID,
			func() (string, error) {
				gen, err := plan.Execute(ctx, clients)
				if err != nil {
					return "", err
				}

				return fmt.Sprintf(
					"Activated generation %d (%d schemas, %d type configurations)",
					gen.Id, len(gen.Schemas), len(gen.Types)), nil
			})
	case conf.Live != nil:
		clients, err := getLiveClients(ctx, c)
		if err != nil {
			return fmt.Errorf("get API clients: %w", err)
		}

		plan, err := live.BuildPlan(
			ctx, clients, conf.Live, schemas, description)
		if err != nil {
			return fmt.Errorf("build plan: %w", err)
		}

		return executePlan(plan.Changes, plan.CurrentGenerationID,
			func() (string, error) {
				gen, err := plan.Execute(ctx, clients)
				if err != nil {
					return "", err
				}

				return fmt.Sprintf(
					"Activated generation %d (%d schemas, %d post types)",
					gen.Id, len(gen.Schemas), len(gen.PostTypes)), nil
			})
	}

	return errors.New("no service configuration loaded")
}

// executePlan prints the plan diff and current state, asks for
// confirmation if the plan would change anything, and runs execute. The
// summary returned by execute is printed on success.
func executePlan(
	changes []distconf.ConfigurationChange,
	currentGenerationID int64,
	execute func() (string, error),
) error {
	printChanges(changes)

	fmt.Fprintln(os.Stdout)

	if currentGenerationID != 0 {
		fmt.Fprintf(os.Stdout,
			"Current active generation: %d\n",
			currentGenerationID)
	} else {
		fmt.Fprintln(os.Stdout, "No generation is currently active.")
	}

	if len(changes) == 0 && currentGenerationID != 0 {
		fmt.Fprintln(os.Stdout, "No changes needed")

		return nil
	}

	if !askForConfirmation(
		"Register a new generation and activate it?") {
		return errors.New("aborted by user")
	}

	summary, err := execute()
	if err != nil {
		return fmt.Errorf("apply generation: %w", err)
	}

	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, summary)

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
