package live

import (
	"context"
	"fmt"
	"slices"

	"github.com/ttab/distconf"
	liveapi "github.com/ttab/elephant-public-api/live"
)

// Clients provides access to the live Twirp API.
type Clients interface {
	GetConfiguration() liveapi.Configuration
}

// Interface guard.
var _ Clients = &StaticClients{}

// StaticClients is a concrete implementation of Clients.
type StaticClients struct {
	Configuration liveapi.Configuration
}

// GetConfiguration implements Clients.
func (c *StaticClients) GetConfiguration() liveapi.Configuration {
	return c.Configuration
}

// Plan is the result of comparing the desired configuration to the active
// generation. It carries both the diff (for display) and the desired
// generation payload (for execution).
type Plan struct {
	// CurrentGenerationID is the active generation ID on the server, or 0
	// when none is active.
	CurrentGenerationID int64

	// Changes describes the diff between the active generation and the
	// desired one, for display purposes.
	Changes []distconf.ConfigurationChange

	// DesiredSchemas and DesiredPostTypes form the full payload that
	// will be sent to RegisterConfigGeneration.
	DesiredSchemas   []*liveapi.ConfigGenerationSchema
	DesiredPostTypes []string

	// Description is the human-readable label attached to the new
	// generation.
	Description string
}

// HasChanges reports whether the plan would create a meaningful new
// generation.
func (p Plan) HasChanges() bool {
	return len(p.Changes) > 0
}

// BuildPlan compares the desired configuration against the server's active
// generation and produces a Plan with the diff and the payload that would
// register a matching generation.
func BuildPlan(
	ctx context.Context,
	clients Clients,
	conf *Config,
	schemas []distconf.LoadedSchema,
	description string,
) (*Plan, error) {
	postTypes := make([]string, len(conf.PostTypes))
	for i, pt := range conf.PostTypes {
		postTypes[i] = pt.Type
	}

	err := distconf.ValidateDeclaredTypes(postTypes, schemas)
	if err != nil {
		return nil, err
	}

	config := clients.GetConfiguration()

	// A known_id that can never match an actual generation makes the
	// server respond immediately instead of long-polling for a change
	// when no generation is active yet.
	active, err := config.GetActiveConfigGeneration(ctx,
		&liveapi.GetActiveConfigGenerationRequest{
			KnownId: -1,
		})
	if err != nil {
		return nil, fmt.Errorf("read active generation: %w", err)
	}

	var (
		activeSchemas   []distconf.SchemaRef
		activePostTypes []string
		currentID       int64
	)

	if active.Generation != nil {
		currentID = active.Generation.Id
		activePostTypes = active.Generation.PostTypes

		activeSchemas = make(
			[]distconf.SchemaRef,
			0, len(active.Generation.Schemas))
		for _, sc := range active.Generation.Schemas {
			activeSchemas = append(activeSchemas, distconf.SchemaRef{
				Name:    sc.Name,
				Version: sc.Version,
				Spec:    sc.Spec,
			})
		}
	}

	desiredRefs, schemaChanges := distconf.PlanSchemas(schemas, activeSchemas)

	desiredSchemas := make(
		[]*liveapi.ConfigGenerationSchema, len(desiredRefs))
	for i, ref := range desiredRefs {
		desiredSchemas[i] = &liveapi.ConfigGenerationSchema{
			Name:    ref.Name,
			Version: ref.Version,
			Spec:    ref.Spec,
		}
	}

	postTypeChanges := planPostTypes(postTypes, activePostTypes)

	plan := Plan{
		CurrentGenerationID: currentID,
		DesiredSchemas:      desiredSchemas,
		DesiredPostTypes:    postTypes,
		Description:         description,
		Changes:             slices.Concat(schemaChanges, postTypeChanges),
	}

	return &plan, nil
}

// Execute registers the desired generation and activates it. Returns the
// newly activated generation.
func (p *Plan) Execute(
	ctx context.Context, clients Clients,
) (*liveapi.ConfigGeneration, error) {
	config := clients.GetConfiguration()

	res, err := config.RegisterConfigGeneration(ctx,
		&liveapi.RegisterConfigGenerationRequest{
			Description: p.Description,
			Schemas:     p.DesiredSchemas,
			PostTypes:   p.DesiredPostTypes,
			Activate:    true,
		})
	if err != nil {
		return nil, fmt.Errorf("register generation: %w", err)
	}

	return res.Generation, nil
}

// planPostTypes diffs the desired post types against the active
// generation's.
func planPostTypes(
	want []string, active []string,
) []distconf.ConfigurationChange {
	var changes []distconf.ConfigurationChange

	for _, t := range want {
		if slices.Contains(active, t) {
			continue
		}

		changes = append(changes, &postTypePlanChange{
			postType: t,
			op:       distconf.OpAdd,
		})
	}

	for _, t := range active {
		if slices.Contains(want, t) {
			continue
		}

		changes = append(changes, &postTypePlanChange{
			postType: t,
			op:       distconf.OpRemove,
		})
	}

	return changes
}

type postTypePlanChange struct {
	postType string
	op       distconf.ChangeOp
}

func (c *postTypePlanChange) Describe() (distconf.ChangeOp, string) {
	if c.op == distconf.OpRemove {
		return distconf.OpRemove, fmt.Sprintf(
			"stop accepting %q posts", c.postType)
	}

	return distconf.OpAdd, fmt.Sprintf(
		"accept %q posts", c.postType)
}

func (c *postTypePlanChange) Warnings() []string {
	return nil
}
