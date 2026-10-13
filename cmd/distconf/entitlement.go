package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/fatih/color"
	distributionv1 "github.com/ttab/elephant-public-api/elephant/distribution/v1"
	"github.com/urfave/cli/v3"
)

// Flag names of the entitlement commands.
const (
	flagOrg         = "org"
	flagEntitlement = "entitlement"
)

const argUsageGrant = "<org> <entitlement>"

// entitlementCommand builds the "entitlement" command group for managing
// which organisations hold which entitlements. What an entitlement hides is
// configuration - the entitlement blocks, applied with "distconf apply" -
// while who holds it is a grant, made here and in effect immediately.
func entitlementCommand(authFlags []cli.Flag) *cli.Command {
	return &cli.Command{
		Name:        "entitlement",
		Description: "Grant, revoke and list content entitlements",
		Commands: []*cli.Command{
			{
				Name: "grant",
				Description: "Grant an organisation an entitlement," +
					" in effect immediately on every surface",
				ArgsUsage: argUsageGrant,
				Flags: append([]cli.Flag{
					&cli.BoolFlag{
						Name:  flagJSON,
						Usage: "print the grant as JSON",
					},
				}, authFlags...),
				Action: grantEntitlementAction,
			},
			{
				Name: "revoke",
				Description: "Revoke an organisation's entitlement," +
					" in effect immediately on every surface",
				ArgsUsage: argUsageGrant,
				Flags: append([]cli.Flag{
					&cli.BoolFlag{
						Name:  flagYes,
						Usage: "revoke without asking for confirmation",
					},
					&cli.BoolFlag{
						Name:  flagJSON,
						Usage: "print the revoked grant as JSON",
					},
				}, authFlags...),
				Action: revokeEntitlementAction,
			},
			{
				Name:        "list",
				Description: "List the entitlement grants",
				Flags: append([]cli.Flag{
					&cli.StringFlag{
						Name:  flagOrg,
						Usage: "only the grants of this organisation",
					},
					&cli.StringFlag{
						Name:  flagEntitlement,
						Usage: "only the grants of this entitlement",
					},
					&cli.BoolFlag{
						Name:  flagJSON,
						Usage: "print the grants as JSON",
					},
				}, authFlags...),
				Action: listEntitlementGrantsAction,
			},
		},
	}
}

// grantEntry is the JSON shape of a grant.
type grantEntry struct {
	Org         string `json:"org"`
	Entitlement string `json:"entitlement"`
	GrantedAt   string `json:"granted_at"`
	GrantedBy   string `json:"granted_by"`
	// Defined is always emitted: false is the case worth seeing, a grant
	// of an entitlement the active generation does not define.
	Defined bool `json:"defined"`
}

func entryFromGrant(g *distributionv1.EntitlementGrant) grantEntry {
	return grantEntry{
		Org:         g.GetOrg(),
		Entitlement: g.GetEntitlement(),
		GrantedAt:   g.GetGrantedAt(),
		GrantedBy:   g.GetGrantedBy(),
		Defined:     g.GetDefined(),
	}
}

// grantArgs reads the organisation and the entitlement from the arguments.
func grantArgs(c *cli.Command) (string, string, error) {
	if c.Args().Len() != 2 {
		return "", "", fmt.Errorf(
			"expected the arguments %s", argUsageGrant)
	}

	org, entitlement := c.Args().Get(0), c.Args().Get(1)

	if org == "" || entitlement == "" {
		return "", "", fmt.Errorf(
			"expected the arguments %s, neither of them empty",
			argUsageGrant)
	}

	return org, entitlement, nil
}

func printJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal the output: %w", err)
	}

	fmt.Fprintf(os.Stdout, "%s\n", data)

	return nil
}

// undefinedNote is what a grant of an entitlement the active generation
// does not define is told. It is not an error - grants can be put in place
// before the gate that needs them - but a misspelled name looks exactly
// like it, so it is said out loud.
func undefinedNote(entitlement string) string {
	return fmt.Sprintf(
		"The active configuration does not define %q, so the grant"+
			" does nothing until a generation that defines it is"+
			" activated. Check the spelling if that is not the plan.",
		entitlement)
}

// grantEntitlementAction grants an entitlement. Granting is idempotent, so
// it needs no confirmation: a repeated grant returns the existing one.
func grantEntitlementAction(ctx context.Context, c *cli.Command) error {
	org, entitlement, err := grantArgs(c)
	if err != nil {
		return err
	}

	clients, err := getDistributionClients(ctx, c)
	if err != nil {
		return fmt.Errorf("get API clients: %w", err)
	}

	resp, err := clients.Configuration.GrantEntitlement(ctx,
		connect.NewRequest(&distributionv1.GrantEntitlementRequest{
			Org:         org,
			Entitlement: entitlement,
		}))
	if err != nil {
		return fmt.Errorf("grant entitlement: %w", err)
	}

	grant := entryFromGrant(resp.Msg.GetGrant())

	if c.Bool(flagJSON) {
		return printJSON(grant)
	}

	fmt.Fprintf(os.Stdout,
		"Organisation %q holds %q, granted at %s by %s.\n",
		grant.Org, grant.Entitlement, grant.GrantedAt, grant.GrantedBy)

	if !grant.Defined {
		fmt.Fprintln(os.Stdout, undefinedNote(grant.Entitlement))
	}

	return nil
}

// revokeEntitlementAction revokes an entitlement. It asks first: the
// revocation takes effect immediately, reads are refused from the next
// request and queued pushes to the organisation's destinations are dropped.
func revokeEntitlementAction(ctx context.Context, c *cli.Command) error {
	org, entitlement, err := grantArgs(c)
	if err != nil {
		return err
	}

	clients, err := getDistributionClients(ctx, c)
	if err != nil {
		return fmt.Errorf("get API clients: %w", err)
	}

	if !c.Bool(flagYes) && !askForConfirmation(fmt.Sprintf(
		"Revoke %q from %q? It takes effect immediately on every"+
			" surface, queued deliveries included.",
		entitlement, org)) {
		return errors.New("aborted by user")
	}

	resp, err := clients.Configuration.RevokeEntitlement(ctx,
		connect.NewRequest(&distributionv1.RevokeEntitlementRequest{
			Org:         org,
			Entitlement: entitlement,
		}))
	if err != nil {
		return fmt.Errorf("revoke entitlement: %w", err)
	}

	revoked := resp.Msg.GetGrant()

	if c.Bool(flagJSON) {
		// null when there was nothing to revoke, which is not an
		// error on the service either.
		var out *grantEntry

		if revoked != nil {
			entry := entryFromGrant(revoked)
			out = &entry
		}

		return printJSON(out)
	}

	if revoked == nil {
		fmt.Fprintf(os.Stdout,
			"Organisation %q did not hold %q, nothing was revoked.\n",
			org, entitlement)

		return nil
	}

	fmt.Fprintf(os.Stdout,
		"Revoked %q from %q (granted at %s by %s).\n",
		revoked.GetEntitlement(), revoked.GetOrg(),
		revoked.GetGrantedAt(), revoked.GetGrantedBy())

	return nil
}

// listEntitlementGrantsAction prints the grants, narrowed to one
// organisation or one entitlement when asked.
func listEntitlementGrantsAction(ctx context.Context, c *cli.Command) error {
	clients, err := getDistributionClients(ctx, c)
	if err != nil {
		return fmt.Errorf("get API clients: %w", err)
	}

	resp, err := clients.Configuration.ListEntitlementGrants(ctx,
		connect.NewRequest(&distributionv1.ListEntitlementGrantsRequest{
			Org:         c.String(flagOrg),
			Entitlement: c.String(flagEntitlement),
		}))
	if err != nil {
		return fmt.Errorf("list entitlement grants: %w", err)
	}

	// An empty list rather than null, so that a script can iterate it
	// without a special case.
	grants := make([]grantEntry, 0, len(resp.Msg.GetGrants()))

	for _, g := range resp.Msg.GetGrants() {
		grants = append(grants, entryFromGrant(g))
	}

	if c.Bool(flagJSON) {
		return printJSON(grants)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "ORG\tENTITLEMENT\tDEFINED\tGRANTED AT\tGRANTED BY")

	for _, g := range grants {
		fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%s\n",
			g.Org, g.Entitlement, g.Defined, g.GrantedAt, g.GrantedBy)
	}

	err = w.Flush()
	if err != nil {
		return fmt.Errorf("write the grant table: %w", err)
	}

	return nil
}

// gateCoverage is the JSON shape of one field gate search cannot yet
// enforce on all of the indexed content.
type gateCoverage struct {
	Entitlement     string `json:"entitlement"`
	DocumentType    string `json:"document_type"`
	DeliveryField   string `json:"delivery_field"`
	IndexGeneration string `json:"index_generation"`
}

func reindexNeededFromRPC(
	coverage []*distributionv1.GateCoverage,
) []gateCoverage {
	if len(coverage) == 0 {
		return nil
	}

	out := make([]gateCoverage, len(coverage))

	for i, g := range coverage {
		out[i] = gateCoverage{
			Entitlement:     g.GetEntitlement(),
			DocumentType:    g.GetDocumentType(),
			DeliveryField:   g.GetDeliveryField(),
			IndexGeneration: g.GetIndexGeneration(),
		}
	}

	return out
}

// printReindexNeeded prints the coverage report of a registration. It is
// a report and not a failure - the generation is active either way, and
// bodies are refused on every surface - but search keeps matching the
// gated content indexed before the field was, so the operator has to know
// that a rebuild is due.
func printReindexNeeded(coverage []gateCoverage) {
	if len(coverage) == 0 {
		return
	}

	fmt.Fprintln(os.Stdout)

	warnCol := color.New(color.FgWhite, color.BgRed)

	_, _ = warnCol.Print(" Re-index needed: ")

	fmt.Fprintf(os.Stdout,
		" the active index generation was not built with the delivery"+
			" field of %d field gate(s):\n", len(coverage))

	for _, g := range coverage {
		fmt.Fprintf(os.Stdout,
			"  entitlement %q gates %q by %q, not in index generation %q\n",
			g.Entitlement, g.DocumentType, g.DeliveryField,
			g.IndexGeneration)
	}

	fmt.Fprintln(os.Stdout,
		"Until the index is rebuilt, gated documents indexed before the"+
			" field was declared still match in search. They are dropped"+
			" from the hits after the query, so nothing of them is"+
			" returned, but a page can come back short. Their bodies are"+
			" refused on every surface, and the eventlog and the daily"+
			" views go on listing them.")
	fmt.Fprintln(os.Stdout,
		"To rebuild: \"distconf distribution generation create\", wait"+
			" for it, activate it, and re-materialize the archived"+
			" partitions.")
}
