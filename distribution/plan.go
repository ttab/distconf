package distribution

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/akedrou/textdiff"
	"github.com/ttab/distconf"
	dist "github.com/ttab/elephant-public-api/distribution"
)

// Clients provides access to the distribution Twirp API.
type Clients interface {
	GetConfiguration() dist.Configuration
}

// Interface guard.
var _ Clients = &StaticClients{}

// StaticClients is a concrete implementation of Clients.
type StaticClients struct {
	Configuration dist.Configuration
}

// GetConfiguration implements Clients.
func (c *StaticClients) GetConfiguration() dist.Configuration {
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

	// DesiredSchemas, DesiredTypes, and DesiredRenditions form the full
	// payload that will be sent to RegisterConfigGeneration.
	DesiredSchemas    []*dist.ConfigGenerationSchema
	DesiredTypes      []*dist.ConfigGenerationType
	DesiredRenditions []*dist.RenditionConfiguration

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
	err := validateConfigTypes(conf, schemas)
	if err != nil {
		return nil, err
	}

	config := clients.GetConfiguration()

	active, err := config.GetActiveConfigGeneration(ctx,
		&dist.GetActiveConfigGenerationRequest{})
	if err != nil {
		return nil, fmt.Errorf("read active generation: %w", err)
	}

	var (
		activeSchemas    []distconf.SchemaRef
		activeTypes      map[string]*dist.ConfigGenerationType
		activeRenditions map[string]*dist.RenditionConfiguration
		currentID        int64
	)

	if active.Generation != nil {
		currentID = active.Generation.Id

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

		activeTypes = make(
			map[string]*dist.ConfigGenerationType,
			len(active.Generation.Types))
		for _, t := range active.Generation.Types {
			activeTypes[t.Type] = t
		}

		activeRenditions = make(
			map[string]*dist.RenditionConfiguration,
			len(active.Generation.Renditions))
		for _, r := range active.Generation.Renditions {
			activeRenditions[r.Kind] = r
		}
	}

	renditionTypes, err := renditionLinkTypes(schemas)
	if err != nil {
		return nil, err
	}

	desiredRefs, schemaChanges := distconf.PlanSchemas(schemas, activeSchemas)

	desiredSchemas := make(
		[]*dist.ConfigGenerationSchema, len(desiredRefs))
	for i, ref := range desiredRefs {
		desiredSchemas[i] = &dist.ConfigGenerationSchema{
			Name:    ref.Name,
			Version: ref.Version,
			Spec:    ref.Spec,
		}
	}

	desiredTypes, typeChanges := planTypes(conf, activeTypes)

	desiredRenditions, renditionChanges, err := planRenditions(
		conf, activeRenditions, renditionTypes)
	if err != nil {
		return nil, err
	}

	changes := slices.Concat(schemaChanges, typeChanges, renditionChanges)

	plan := Plan{
		CurrentGenerationID: currentID,
		DesiredSchemas:      desiredSchemas,
		DesiredTypes:        desiredTypes,
		DesiredRenditions:   desiredRenditions,
		Description:         description,
		Changes:             changes,
	}

	return &plan, nil
}

// Execute registers the desired generation and activates it. Returns the
// newly activated generation.
func (p *Plan) Execute(
	ctx context.Context, clients Clients,
) (*dist.ConfigGeneration, error) {
	config := clients.GetConfiguration()

	res, err := config.RegisterConfigGeneration(ctx,
		&dist.RegisterConfigGenerationRequest{
			Description: p.Description,
			Schemas:     p.DesiredSchemas,
			Types:       p.DesiredTypes,
			Renditions:  p.DesiredRenditions,
			Activate:    true,
		})
	if err != nil {
		return nil, fmt.Errorf("register generation: %w", err)
	}

	return res.Generation, nil
}

// validateConfigTypes verifies that every document type configured in the
// HCL files is declared by one of the loaded schemas.
func validateConfigTypes(conf *Config, schemas []distconf.LoadedSchema) error {
	types := make([]string, len(conf.Documents))
	for i, doc := range conf.Documents {
		types[i] = doc.Type
	}

	return distconf.ValidateDeclaredTypes(types, schemas)
}

func planTypes(
	conf *Config,
	active map[string]*dist.ConfigGenerationType,
) ([]*dist.ConfigGenerationType, []distconf.ConfigurationChange) {
	desired := make([]*dist.ConfigGenerationType, 0, len(conf.Documents))
	seen := make(map[string]bool, len(conf.Documents))

	var changes []distconf.ConfigurationChange

	for _, doc := range conf.Documents {
		seen[doc.Type] = true

		entry := &dist.ConfigGenerationType{
			Type: doc.Type,
			Configuration: &dist.TypeConfiguration{
				TransformScript:   doc.TransformScript,
				BoundedCollection: doc.BoundedCollection,
				Variants:          doc.Variants,
				Embeddings:        doc.Embeddings,
				Anchor:            doc.Anchor,
				TimeExpressions:   timeExpressionsToRPC(doc.TimeExpressions),
			},
		}

		desired = append(desired, entry)

		curr, ok := active[doc.Type]
		switch {
		case !ok:
			changes = append(changes, &typePlanChange{
				docType: doc.Type,
				wanted:  doc.TransformScript,
				op:      distconf.OpAdd,
			})
		default:
			currConf := curr.Configuration
			if currConf == nil {
				currConf = &dist.TypeConfiguration{}
			}

			settingsDiff := typeSettingsDiff(currConf, doc)

			if currConf.TransformScript != doc.TransformScript ||
				settingsDiff != "" {
				changes = append(changes, &typePlanChange{
					docType:      doc.Type,
					current:      currConf.TransformScript,
					wanted:       doc.TransformScript,
					settingsDiff: settingsDiff,
					op:           distconf.OpUpdate,
				})
			}
		}
	}

	for typ, curr := range active {
		if seen[typ] {
			continue
		}

		current := ""
		if curr.Configuration != nil {
			current = curr.Configuration.TransformScript
		}

		changes = append(changes, &typePlanChange{
			docType: typ,
			current: current,
			op:      distconf.OpRemove,
		})
	}

	return desired, changes
}

// planRenditions builds the desired rendition payload and the diff
// against the active generation. renditionTypes is the set of block
// types for which some desired schema declares a rel "rendition" link;
// sources matching other block types get a warning, since their
// delivered documents would fail consumer-side validation.
func planRenditions(
	conf *Config,
	active map[string]*dist.RenditionConfiguration,
	renditionTypes map[string]bool,
) ([]*dist.RenditionConfiguration, []distconf.ConfigurationChange, error) {
	desired := make([]*dist.RenditionConfiguration, 0, len(conf.Renditions))
	seen := make(map[string]bool, len(conf.Renditions))

	var changes []distconf.ConfigurationChange

	for _, rc := range conf.Renditions {
		seen[rc.Kind] = true

		entry := renditionConfigToRPC(rc)
		desired = append(desired, entry)

		wanted, err := renditionSpecJSON(entry)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"render %q rendition spec: %w", rc.Kind, err)
		}

		warnings := renditionSchemaWarnings(entry, renditionTypes)

		curr, ok := active[rc.Kind]
		if !ok {
			changes = append(changes, &renditionPlanChange{
				kind:     rc.Kind,
				wanted:   wanted,
				op:       distconf.OpAdd,
				warnings: warnings,
			})

			continue
		}

		current, err := renditionSpecJSON(curr)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"render active %q rendition spec: %w", rc.Kind, err)
		}

		if current != wanted {
			changes = append(changes, &renditionPlanChange{
				kind:     rc.Kind,
				current:  current,
				wanted:   wanted,
				op:       distconf.OpUpdate,
				warnings: warnings,
			})
		}
	}

	for kind := range active {
		if seen[kind] {
			continue
		}

		changes = append(changes, &renditionPlanChange{
			kind: kind,
			op:   distconf.OpRemove,
		})
	}

	return desired, changes, nil
}

// Built-in per-kind defaults for rendition sources. These mirror the
// defaults the distribution service applies when it compiles a rendition
// configuration at registration: it stores the compiled configuration, so
// a source that leaves block types or link rel out comes back from the
// server with them filled in. We resolve them here, before the payload is
// built and diffed, so that what we send is what the server stores.
// Otherwise the defaulting shows up as a diff that no number of applies
// can settle.
const (
	renditionKindImage = "image"
	imageBlockType     = "core/image"
	imageLinkRel       = "image"
)

// renditionKindDefaults returns the built-in default block types and link
// rel for an asset kind. Kinds without built-in defaults must declare both
// explicitly in every source; the server rejects the registration if they
// don't.
func renditionKindDefaults(kind string) ([]string, string) {
	switch kind {
	case renditionKindImage:
		return []string{imageBlockType}, imageLinkRel
	default:
		return nil, ""
	}
}

func renditionConfigToRPC(rc RenditionsConfig) *dist.RenditionConfiguration {
	entry := dist.RenditionConfiguration{
		Kind:             rc.Kind,
		DefaultVariants:  rc.DefaultVariants,
		DefaultExtension: rc.DefaultExtension,
		Sources: make(
			[]*dist.RenditionSource, len(rc.Sources)),
	}

	defBlocks, defRel := renditionKindDefaults(rc.Kind)

	for i, src := range rc.Sources {
		blockTypes := src.BlockTypes
		if len(blockTypes) == 0 {
			blockTypes = defBlocks
		}

		linkRel := src.LinkRel
		if linkRel == "" {
			linkRel = defRel
		}

		entry.Sources[i] = &dist.RenditionSource{
			Name:       src.Name,
			Namespace:  src.Namespace,
			BlockTypes: blockTypes,
			LinkRel:    linkRel,
			LinkTypes:  src.LinkTypes,
			UriPattern: src.URIPattern,
		}
	}

	return &entry
}

// renditionKindSpec is the canonical JSON shape used to compare and
// display rendition configurations in plans.
type renditionKindSpec struct {
	DefaultVariants  []string              `json:"default_variants,omitempty"`
	DefaultExtension string                `json:"default_extension,omitempty"`
	Sources          []renditionSourceSpec `json:"sources"`
}

type renditionSourceSpec struct {
	Name       string   `json:"name"`
	Namespace  string   `json:"namespace"`
	BlockTypes []string `json:"block_types,omitempty"`
	LinkRel    string   `json:"link_rel,omitempty"`
	LinkTypes  []string `json:"link_types,omitempty"`
	URIPattern string   `json:"uri_pattern"`
}

func renditionSpecJSON(rc *dist.RenditionConfiguration) (string, error) {
	spec := renditionKindSpec{
		DefaultVariants:  rc.DefaultVariants,
		DefaultExtension: rc.DefaultExtension,
		Sources: make(
			[]renditionSourceSpec, len(rc.Sources)),
	}

	for i, src := range rc.Sources {
		if src == nil {
			continue
		}

		spec.Sources[i] = renditionSourceSpec{
			Name:       src.Name,
			Namespace:  src.Namespace,
			BlockTypes: src.BlockTypes,
			LinkRel:    src.LinkRel,
			LinkTypes:  src.LinkTypes,
			URIPattern: src.UriPattern,
		}
	}

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal spec: %w", err)
	}

	return string(data), nil
}

// renditionSchemaWarnings warns about sources whose block types have no
// rel "rendition" link declared by any desired schema.
func renditionSchemaWarnings(
	rc *dist.RenditionConfiguration, renditionTypes map[string]bool,
) []string {
	var warnings []string

	for _, src := range rc.Sources {
		for _, bt := range src.BlockTypes {
			if renditionTypes[bt] {
				continue
			}

			warnings = append(warnings, fmt.Sprintf(
				"source %q matches block type %q, but no schema "+
					"in the generation declares a rel \"rendition\" "+
					"link for it; delivered rendition links will "+
					"fail consumer-side validation",
				src.Name, bt))
		}
	}

	return warnings
}

// renditionLinkTypes collects the block types for which some schema
// declares a rel "rendition" link.
func renditionLinkTypes(
	schemas []distconf.LoadedSchema,
) (map[string]bool, error) {
	types := make(map[string]bool)

	for _, sc := range schemas {
		var spec any

		err := json.Unmarshal(sc.Data, &spec)
		if err != nil {
			return nil, fmt.Errorf(
				"unmarshal schema %q: %w", sc.Lock.Name, err)
		}

		collectRenditionLinkTypes(spec, types)
	}

	return types, nil
}

func collectRenditionLinkTypes(node any, out map[string]bool) {
	switch n := node.(type) {
	case map[string]any:
		blockType := declaredBlockType(n)
		if blockType != "" && hasRenditionLink(n) {
			out[blockType] = true
		}

		for _, v := range n {
			collectRenditionLinkTypes(v, out)
		}
	case []any:
		for _, e := range n {
			collectRenditionLinkTypes(e, out)
		}
	}
}

func declaredBlockType(block map[string]any) string {
	decl, ok := block["declares"].(map[string]any)
	if !ok {
		return ""
	}

	t, _ := decl["type"].(string)

	return t
}

func hasRenditionLink(block map[string]any) bool {
	links, ok := block["links"].([]any)
	if !ok {
		return false
	}

	for _, l := range links {
		lm, ok := l.(map[string]any)
		if !ok {
			continue
		}

		if relIsRendition(lm["rel"]) {
			return true
		}

		if decl, ok := lm["declares"].(map[string]any); ok &&
			relIsRendition(decl["rel"]) {
			return true
		}
	}

	return false
}

func relIsRendition(rel any) bool {
	switch r := rel.(type) {
	case string:
		return r == "rendition"
	case []any:
		for _, e := range r {
			if s, ok := e.(string); ok && s == "rendition" {
				return true
			}
		}
	}

	return false
}

type renditionPlanChange struct {
	kind     string
	current  string
	wanted   string
	op       distconf.ChangeOp
	warnings []string
}

func (c *renditionPlanChange) Describe() (distconf.ChangeOp, string) {
	switch c.op {
	case distconf.OpAdd:
		return distconf.OpAdd, fmt.Sprintf(
			"configure %q renditions:\n%s", c.kind, c.wanted)
	case distconf.OpRemove:
		return distconf.OpRemove, fmt.Sprintf(
			"remove %q rendition configuration", c.kind)
	case distconf.OpUpdate:
	}

	diff := textdiff.Unified("current", "wanted", c.current, c.wanted)

	return distconf.OpUpdate, fmt.Sprintf(
		"update %q rendition configuration:\n%s",
		c.kind, strings.TrimRight(diff, "\n"))
}

func (c *renditionPlanChange) Warnings() []string {
	return c.warnings
}

// typeSettingsDiff describes changes to the non-script type settings,
// or returns an empty string when they are unchanged.
func typeSettingsDiff(
	curr *dist.TypeConfiguration, doc DocumentConfig,
) string {
	var lines []string

	if curr.BoundedCollection != doc.BoundedCollection {
		lines = append(lines, fmt.Sprintf(
			"bounded_collection: %t => %t",
			curr.BoundedCollection, doc.BoundedCollection))
	}

	if !slices.Equal(curr.Variants, doc.Variants) {
		lines = append(lines, fmt.Sprintf(
			"variants: %v => %v", curr.Variants, doc.Variants))
	}

	if curr.Embeddings != doc.Embeddings {
		lines = append(lines, fmt.Sprintf(
			"embeddings: %t => %t", curr.Embeddings, doc.Embeddings))
	}

	if curr.Anchor != doc.Anchor {
		lines = append(lines, fmt.Sprintf(
			"anchor: %s => %s",
			anchorName(curr.Anchor), anchorName(doc.Anchor)))
	}

	wanted := timeExpressionsToRPC(doc.TimeExpressions)

	if !timeExpressionsEqual(curr.TimeExpressions, wanted) {
		lines = append(lines, fmt.Sprintf(
			"time_expressions: %s => %s",
			timeExpressionSummary(curr.TimeExpressions),
			timeExpressionSummary(wanted)))
	}

	return strings.Join(lines, "\n")
}

// timeExpressionsToRPC converts the configured time expressions into the
// API representation.
func timeExpressionsToRPC(
	expressions []TimeExpressionConfig,
) []*dist.TypeTimeExpression {
	if len(expressions) == 0 {
		return nil
	}

	out := make([]*dist.TypeTimeExpression, len(expressions))

	for i, e := range expressions {
		out[i] = &dist.TypeTimeExpression{
			Expression: e.Expression,
			Layout:     e.Layout,
			Timezone:   e.Timezone,
		}
	}

	return out
}

// timeExpressionsEqual compares two sets of time expressions. Order is
// part of the value: the extractor merges the spans it gets, but the
// stored configuration is a list and is replaced whole.
func timeExpressionsEqual(a, b []*dist.TypeTimeExpression) bool {
	return slices.EqualFunc(a, b,
		func(x, y *dist.TypeTimeExpression) bool {
			return x.GetExpression() == y.GetExpression() &&
				x.GetLayout() == y.GetLayout() &&
				x.GetTimezone() == y.GetTimezone()
		})
}

// timeExpressionSummary renders time expressions for a plan line.
func timeExpressionSummary(expressions []*dist.TypeTimeExpression) string {
	if len(expressions) == 0 {
		return "none"
	}

	parts := make([]string, len(expressions))

	for i, e := range expressions {
		parts[i] = e.GetExpression()

		if e.GetLayout() != "" {
			parts[i] += " layout=" + e.GetLayout()
		}

		if e.GetTimezone() != "" {
			parts[i] += " tz=" + e.GetTimezone()
		}
	}

	return strings.Join(parts, ", ")
}

type typePlanChange struct {
	docType      string
	current      string
	wanted       string
	settingsDiff string
	op           distconf.ChangeOp
}

func (c *typePlanChange) Describe() (distconf.ChangeOp, string) {
	switch c.op {
	case distconf.OpAdd:
		if c.wanted != "" {
			return distconf.OpAdd, fmt.Sprintf(
				"configure type %q with transform script", c.docType)
		}

		return distconf.OpAdd, fmt.Sprintf("configure type %q", c.docType)
	case distconf.OpRemove:
		return distconf.OpRemove, fmt.Sprintf(
			"remove type configuration for %q", c.docType)
	case distconf.OpUpdate:
	}

	if c.current == c.wanted {
		return distconf.OpUpdate, fmt.Sprintf(
			"update settings for %q:\n%s",
			c.docType, c.settingsDiff)
	}

	var op distconf.ChangeOp

	var message string

	switch {
	case c.current == "" && c.wanted != "":
		op = distconf.OpAdd
		message = fmt.Sprintf("set transform script for %q", c.docType)
	case c.wanted == "":
		op = distconf.OpRemove
		message = fmt.Sprintf("remove transform script for %q", c.docType)
	default:
		diff := textdiff.Unified("current", "wanted",
			c.current, c.wanted)

		op = distconf.OpUpdate
		message = fmt.Sprintf(
			"update transform script for %q:\n%s",
			c.docType, strings.TrimRight(diff, "\n"))
	}

	if c.settingsDiff != "" {
		message += "\n" + c.settingsDiff
	}

	return op, message
}

func (c *typePlanChange) Warnings() []string {
	return nil
}
