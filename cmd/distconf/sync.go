package main

import (
	"context"
	"fmt"
	"os"

	dist "github.com/ttab/elephant-public-api/distribution"
	"github.com/urfave/cli/v3"
)

// Desired sync worker states, matching the distribution service.
const (
	syncStateRunning = "running"
	syncStatePaused  = "paused"
)

// syncCommand builds the "sync" command group for controlling the
// distribution sync worker. The auth flags are shared with the other
// server-facing commands.
func syncCommand(authFlags []cli.Flag) *cli.Command {
	return &cli.Command{
		Name:        "sync",
		Description: "Control the distribution sync worker",
		Commands: []*cli.Command{
			{
				Name:        "start",
				Description: "Resume the sync worker",
				Flags:       authFlags,
				Action:      setSyncStateAction(syncStateRunning),
			},
			{
				Name:        "stop",
				Description: "Pause the sync worker",
				Flags:       authFlags,
				Action:      setSyncStateAction(syncStatePaused),
			},
			{
				Name:        "status",
				Description: "Show the sync worker status",
				Flags:       authFlags,
				Action:      syncStatusAction,
			},
		},
	}
}

// setSyncStateAction returns an action that sets the desired sync worker
// state.
func setSyncStateAction(desired string) cli.ActionFunc {
	return func(ctx context.Context, c *cli.Command) error {
		clients, err := getDistributionClients(ctx, c)
		if err != nil {
			return fmt.Errorf("get API clients: %w", err)
		}

		_, err = clients.Configuration.SetSyncState(ctx,
			&dist.SetSyncStateRequest{Desired: desired})
		if err != nil {
			return fmt.Errorf("set sync state to %q: %w", desired, err)
		}

		fmt.Fprintf(os.Stdout, "Sync worker set to %q.\n", desired)

		return nil
	}
}

// syncStatusAction prints the desired and current sync worker state and
// its position in the repository eventlog.
func syncStatusAction(ctx context.Context, c *cli.Command) error {
	clients, err := getDistributionClients(ctx, c)
	if err != nil {
		return fmt.Errorf("get API clients: %w", err)
	}

	status, err := clients.Configuration.GetSyncStatus(ctx,
		&dist.GetSyncStatusRequest{})
	if err != nil {
		return fmt.Errorf("get sync status: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Desired:  %s\n", status.Desired)
	fmt.Fprintf(os.Stdout, "Current:  %s\n", status.Current)
	fmt.Fprintf(os.Stdout, "Position: %d\n", status.Position)
	fmt.Fprintf(os.Stdout, "CaughtUp: %t\n", status.CaughtUp)

	return nil
}
