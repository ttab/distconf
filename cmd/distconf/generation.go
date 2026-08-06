package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	dist "github.com/ttab/elephant-public-api/distribution"
	"github.com/urfave/cli/v3"
)

// defaultActivationLagGate mirrors the distribution service's
// INDEX_ACTIVATION_LAG_GATE default. It is only the CLI's idea of when a
// generation is ready to wait for - the service applies its own gate on
// activation, and a deployment that has moved the gate has to pass
// --max-lag to match.
const defaultActivationLagGate = 10

// defaultWaitInterval is how often "generation wait" polls. A rebuild is
// measured in hours, so there is nothing to be gained by asking often.
const defaultWaitInterval = 30 * time.Second

// generationCommand builds the "generation" command group for managing
// index generations: the unit a full index rebuild is done in, and
// therefore what a recovery from a lost cluster goes through.
func generationCommand(authFlags []cli.Flag) *cli.Command {
	return &cli.Command{
		Name:        "generation",
		Description: "Manage index generations",
		Commands: []*cli.Command{
			{
				Name:        "list",
				Description: "List index generations and how far each has caught up",
				Flags: append([]cli.Flag{
					&cli.BoolFlag{
						Name:  flagJSON,
						Usage: "print the generations as JSON",
					},
				}, authFlags...),
				Action: listGenerationsAction,
			},
			{
				Name: "create",
				Description: "Register a new index generation and start" +
					" building it",
				Flags: append([]cli.Flag{
					&cli.StringFlag{
						Name: "name",
						Usage: "name of the generation, a readable" +
							" codename is generated when empty",
					},
					&cli.StringFlag{
						Name:  "cluster",
						Value: "default",
						Usage: "cluster to build on, as the deployment declares it",
					},
					&cli.StringFlag{
						Name:  "prefix",
						Usage: "index name prefix, defaults to the generation name",
					},
					&cli.BoolFlag{
						Name:  flagJSON,
						Usage: "print the created generation as JSON",
					},
				}, authFlags...),
				Action: createGenerationAction,
			},
			{
				Name: "wait",
				Description: "Wait for a generation to catch up with the" +
					" eventlog",
				ArgsUsage: argUsageName,
				Flags: append([]cli.Flag{
					&cli.IntFlag{
						Name:  "max-lag",
						Value: defaultActivationLagGate,
						Usage: "lag to wait for, matching the service's" +
							" activation gate",
					},
					&cli.DurationFlag{
						Name:  "interval",
						Value: defaultWaitInterval,
						Usage: "how often to poll",
					},
					&cli.DurationFlag{
						Name:  "timeout",
						Usage: "give up after this long, no timeout when unset",
					},
				}, authFlags...),
				Action: waitGenerationAction,
			},
			{
				Name: "activate",
				Description: "Make a generation the one search and" +
					" subscriptions read",
				ArgsUsage: argUsageName,
				Flags: append([]cli.Flag{
					&cli.BoolFlag{
						Name: "force",
						Usage: "activate a generation that lags further" +
							" than the gate allows",
					},
					&cli.BoolFlag{
						Name:  flagYes,
						Usage: "activate without asking for confirmation",
					},
				}, authFlags...),
				Action: activateGenerationAction,
			},
			{
				Name:        "delete",
				Description: "Delete an inactive generation and its indexes",
				ArgsUsage:   argUsageName,
				Flags: append([]cli.Flag{
					&cli.BoolFlag{
						Name:  flagYes,
						Usage: "delete without asking for confirmation",
					},
				}, authFlags...),
				Action: deleteGenerationAction,
			},
		},
	}
}

// generationEntry is the JSON shape of a generation in "list" and "create".
// It flattens the status into the generation, as the two are only ever read
// together.
type generationEntry struct {
	Name        string `json:"name"`
	Cluster     string `json:"cluster"`
	Prefix      string `json:"prefix"`
	Enabled     bool   `json:"enabled"`
	Active      bool   `json:"active"`
	CreatedAt   string `json:"created_at"`
	ActivatedAt string `json:"activated_at,omitempty"`
	// Position and Lag are always emitted: zero is what a caught-up
	// generation reports, and a script reading a missing lag as null
	// would have to tell "no lag" from "not there".
	Position int64 `json:"position"`
	Lag      int64 `json:"lag"`
}

// generationListing is what "list --json" prints. The eventlog head is
// included because a lag only means something against it.
type generationListing struct {
	EventlogHead int64             `json:"eventlog_head"`
	Generations  []generationEntry `json:"generations"`
}

func entryFromStatus(status *dist.IndexGenerationStatus) generationEntry {
	gen := status.Generation

	return generationEntry{
		Name:        gen.Name,
		Cluster:     gen.Cluster,
		Prefix:      gen.Prefix,
		Enabled:     gen.Enabled,
		Active:      gen.Active,
		CreatedAt:   gen.CreatedAt,
		ActivatedAt: gen.ActivatedAt,
		Position:    status.Position,
		Lag:         status.Lag,
	}
}

func entryFromGeneration(gen *dist.IndexGeneration) generationEntry {
	return generationEntry{
		Name:        gen.Name,
		Cluster:     gen.Cluster,
		Prefix:      gen.Prefix,
		Enabled:     gen.Enabled,
		Active:      gen.Active,
		CreatedAt:   gen.CreatedAt,
		ActivatedAt: gen.ActivatedAt,
	}
}

// listGenerationsAction prints the generations with the position each
// one's indexer has reached, which is the view a rebuild is followed
// through.
func listGenerationsAction(ctx context.Context, c *cli.Command) error {
	clients, err := getDistributionClients(ctx, c)
	if err != nil {
		return fmt.Errorf("get API clients: %w", err)
	}

	res, err := clients.Configuration.ListIndexGenerations(ctx,
		&dist.ListIndexGenerationsRequest{})
	if err != nil {
		return fmt.Errorf("list index generations: %w", err)
	}

	listing := generationListing{
		EventlogHead: res.EventlogHead,
	}

	for _, status := range res.Generations {
		listing.Generations = append(listing.Generations,
			entryFromStatus(status))
	}

	if c.Bool(flagJSON) {
		data, err := json.Marshal(listing)
		if err != nil {
			return fmt.Errorf("marshal the generations: %w", err)
		}

		fmt.Fprintf(os.Stdout, "%s\n", data)

		return nil
	}

	fmt.Fprintf(os.Stdout, "Eventlog head: %d\n\n", res.EventlogHead)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "NAME\tCLUSTER\tPREFIX\tENABLED\tACTIVE\tPOSITION\tLAG")

	for _, gen := range listing.Generations {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%t\t%d\t%d\n",
			gen.Name, gen.Cluster, gen.Prefix,
			gen.Enabled, gen.Active, gen.Position, gen.Lag)
	}

	err = w.Flush()
	if err != nil {
		return fmt.Errorf("write the generation table: %w", err)
	}

	return nil
}

// createGenerationAction registers a generation. It is additive: the
// generation starts building while the active one keeps serving, so
// nothing about the running system changes until it is activated.
func createGenerationAction(ctx context.Context, c *cli.Command) error {
	clients, err := getDistributionClients(ctx, c)
	if err != nil {
		return fmt.Errorf("get API clients: %w", err)
	}

	res, err := clients.Configuration.CreateIndexGeneration(ctx,
		&dist.CreateIndexGenerationRequest{
			Name:    c.String("name"),
			Cluster: c.String("cluster"),
			Prefix:  c.String("prefix"),
		})
	if err != nil {
		return fmt.Errorf("create index generation: %w", err)
	}

	if c.Bool(flagJSON) {
		data, err := json.Marshal(entryFromGeneration(res.Generation))
		if err != nil {
			return fmt.Errorf("marshal the generation: %w", err)
		}

		fmt.Fprintf(os.Stdout, "%s\n", data)

		return nil
	}

	fmt.Fprintf(os.Stdout,
		"Created generation %q on cluster %q with prefix %q.\n",
		res.Generation.Name, res.Generation.Cluster,
		res.Generation.Prefix)
	fmt.Fprintln(os.Stdout,
		"It builds in the background while the active generation keeps"+
			" serving.")
	fmt.Fprintf(os.Stdout,
		"Follow it with: distconf distribution generation wait %s\n",
		res.Generation.Name)

	return nil
}

// waitGenerationAction polls until the generation is within max-lag of the
// eventlog head. A rebuild drains the archive before it stores a position,
// so a generation can sit at zero for a long time without anything being
// wrong - the lag is reported against the head throughout.
func waitGenerationAction(ctx context.Context, c *cli.Command) error {
	name := c.Args().First()
	if name == "" {
		return errors.New("no generation name given")
	}

	clients, err := getDistributionClients(ctx, c)
	if err != nil {
		return fmt.Errorf("get API clients: %w", err)
	}

	maxLag := c.Int("max-lag")

	if timeout := c.Duration("timeout"); timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	ticker := time.NewTicker(c.Duration("interval"))
	defer ticker.Stop()

	for {
		res, err := clients.Configuration.ListIndexGenerations(ctx,
			&dist.ListIndexGenerationsRequest{})
		if err != nil {
			return fmt.Errorf("list index generations: %w", err)
		}

		status, err := findGeneration(res.Generations, name)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stdout, "%s position %d of %d, lag %d\n",
			time.Now().Format(time.TimeOnly),
			status.Position, res.EventlogHead, status.Lag)

		if status.Lag <= int64(maxLag) {
			fmt.Fprintf(os.Stdout,
				"Generation %q is within %d of the head.\n",
				name, maxLag)

			return nil
		}

		// A disabled generation runs no indexer, so its lag is not
		// going to come down however long we wait for it.
		if !status.Generation.Enabled {
			return fmt.Errorf(
				"generation %q is disabled and is not catching up",
				name)
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("wait for generation %q: %w",
				name, ctx.Err())
		}
	}
}

func findGeneration(
	generations []*dist.IndexGenerationStatus, name string,
) (*dist.IndexGenerationStatus, error) {
	for _, status := range generations {
		if status.Generation.Name == name {
			return status, nil
		}
	}

	return nil, fmt.Errorf("no generation named %q", name)
}

// activateGenerationAction switches search, calibration and subscription
// matching over to the generation.
func activateGenerationAction(ctx context.Context, c *cli.Command) error {
	name := c.Args().First()
	if name == "" {
		return errors.New("no generation name given")
	}

	clients, err := getDistributionClients(ctx, c)
	if err != nil {
		return fmt.Errorf("get API clients: %w", err)
	}

	if !c.Bool(flagYes) && !askForConfirmation(fmt.Sprintf(
		"Make %q the generation search and subscriptions read?", name)) {
		return errors.New("aborted by user")
	}

	res, err := clients.Configuration.ActivateIndexGeneration(ctx,
		&dist.ActivateIndexGenerationRequest{
			Name:  name,
			Force: c.Bool("force"),
		})
	if err != nil {
		return fmt.Errorf("activate index generation: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Activated generation %q.\n",
		res.Generation.Name)

	// Worth printing rather than hiding: it is the lower bound of the
	// window in which a subscription can see the same document version
	// delivered twice, which is what a consumer that cares has to
	// deduplicate over.
	fmt.Fprintf(os.Stdout,
		"Subscription matching handed over at eventlog position %d.\n",
		res.HandoverPosition)

	return nil
}

// deleteGenerationAction deletes an inactive generation. The cleanup of
// its indexes and snapshots runs asynchronously and is scoped to the
// generation's prefix.
func deleteGenerationAction(ctx context.Context, c *cli.Command) error {
	name := c.Args().First()
	if name == "" {
		return errors.New("no generation name given")
	}

	clients, err := getDistributionClients(ctx, c)
	if err != nil {
		return fmt.Errorf("get API clients: %w", err)
	}

	if !c.Bool(flagYes) && !askForConfirmation(fmt.Sprintf(
		"Delete generation %q, its indexes and its snapshots?", name)) {
		return errors.New("aborted by user")
	}

	res, err := clients.Configuration.DeleteIndexGeneration(ctx,
		&dist.DeleteIndexGenerationRequest{Name: name})
	if err != nil {
		return fmt.Errorf("delete index generation: %w", err)
	}

	fmt.Fprintf(os.Stdout,
		"Deleted generation %q. The cleanup of its indexes and"+
			" snapshots runs in the background.\n",
		res.Generation.Name)

	return nil
}
