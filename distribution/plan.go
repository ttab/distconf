package distribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	// DesiredSchemas, DesiredTypes, DesiredRenditions and
	// DesiredRenderers form the full payload that will be sent to
	// RegisterConfigGeneration.
	DesiredSchemas    []*dist.ConfigGenerationSchema
	DesiredTypes      []*dist.ConfigGenerationType
	DesiredRenditions []*dist.RenditionConfiguration

	// DesiredRenderers is the HTML rendering configuration the
	// generation should carry, or nil when the configuration declares
	// none - which is how a generation is left rendering no HTML.
	DesiredRenderers *HTMLRenderingSpec

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
		activeRenderers  *HTMLRenderingSpec
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

		activeRenderers = htmlRenderingFromRPC(
			active.Generation.Renderers)
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

	desiredRenderers, rendererChanges, err := planRenderers(
		conf, activeRenderers)
	if err != nil {
		return nil, err
	}

	changes := slices.Concat(
		schemaChanges, typeChanges, renditionChanges, rendererChanges)

	plan := Plan{
		CurrentGenerationID: currentID,
		DesiredSchemas:      desiredSchemas,
		DesiredTypes:        desiredTypes,
		DesiredRenditions:   desiredRenditions,
		DesiredRenderers:    desiredRenderers,
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
			Renderers:   htmlRenderingToRPC(p.DesiredRenderers),
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
				FacetExpressions:  facetsToRPC(doc.Facets),
				DeliveryFields:    deliveryFieldsToRPC(doc.DeliveryFields),
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

// HTMLRenderingSpec is the desired HTML rendering configuration: the
// html_rendering settings and the renderers, with the defaults resolved,
// in the shape the generation stores them.
//
// It is a type of our own rather than the API message because the diff is
// computed over it: the display shape hashes the script and carries the
// declaration position, neither of which is a field of the message.
// htmlRenderingToRPC and htmlRenderingFromRPC are the only two places that
// know about the message, and between them they have to be each other's
// inverse - an active generation that reads back as something other than
// what was sent is a diff no apply can settle.
type HTMLRenderingSpec struct {
	ImageVariant  string         `json:"image_variant,omitempty"`
	DocumentTypes []string       `json:"document_types,omitempty"`
	Renderers     []RendererSpec `json:"renderers,omitempty"`
}

// RendererSpec is one renderer as the generation stores it. The script is
// the content of the configuration's script_file: a generation carries what
// it will run, not a path into somebody's checkout.
type RendererSpec struct {
	Name           string                `json:"name"`
	Kind           string                `json:"kind"`
	Revision       int64                 `json:"revision"`
	Script         string                `json:"script,omitempty"`
	URL            string                `json:"url,omitempty"`
	AllowInsecure  bool                  `json:"allow_insecure,omitempty"`
	Triggers       []RendererTriggerSpec `json:"triggers,omitempty"`
	DocumentTypes  []string              `json:"document_types,omitempty"`
	Policy         *RendererPolicySpec   `json:"policy,omitempty"`
	PolicyPreset   string                `json:"policy_preset,omitempty"`
	CircuitBreaker *CircuitBreakerSpec   `json:"circuit_breaker,omitempty"`
}

// RendererTriggerSpec is one condition on the document's top-level blocks
// that invokes the renderer.
type RendererTriggerSpec struct {
	BlockTypes []string `json:"block_types,omitempty"`
	Roles      []string `json:"roles,omitempty"`
}

// RendererPolicySpec is a renderer's sanitizer policy.
type RendererPolicySpec struct {
	Elements   []string            `json:"elements,omitempty"`
	Attributes map[string][]string `json:"attributes,omitempty"`
	URLSchemes []string            `json:"url_schemes,omitempty"`
}

// CircuitBreakerSpec bounds what a failing renderer costs. Durations are
// Go duration strings.
type CircuitBreakerSpec struct {
	Timeout          string `json:"timeout,omitempty"`
	FailureThreshold int32  `json:"failure_threshold,omitempty"`
	OpenDuration     string `json:"open_duration,omitempty"`
	MaxInFlight      int32  `json:"max_in_flight,omitempty"`
}

// htmlRenderingSpec builds the desired HTML rendering configuration, or
// nil when the configuration declares neither an html_rendering block nor
// a renderer - a deployment that never asked for HTML rendering must send
// nothing rather than a block of defaults.
//
// The renderers keep their declaration order, and that is not cosmetic
// either: every invoked renderer answers for whichever top-level blocks it
// likes, and where two of them answer for one block the first in this order
// wins, so reordering two renderer blocks changes what the output is. It is
// the opposite of a set of delivery fields, which are sorted precisely
// because their order means nothing.
func htmlRenderingSpec(conf *Config) *HTMLRenderingSpec {
	html := conf.htmlRendering()

	if html == nil && len(conf.Renderers) == 0 {
		return nil
	}

	spec := HTMLRenderingSpec{
		ImageVariant: DefaultImageVariant,
	}

	if html != nil {
		spec.ImageVariant = html.ImageVariant
		spec.DocumentTypes = html.DocumentTypes
	}

	for _, r := range conf.Renderers {
		spec.Renderers = append(spec.Renderers, rendererSpec(r))
	}

	return &spec
}

func rendererSpec(r RendererConfig) RendererSpec {
	spec := RendererSpec{
		Name:          r.Name,
		Kind:          r.Kind,
		Revision:      r.Revision,
		Script:        r.Script,
		URL:           r.URL,
		AllowInsecure: r.AllowInsecure,
		DocumentTypes: r.DocumentTypes,
		PolicyPreset:  r.PolicyPreset,
	}

	for _, t := range r.Triggers {
		spec.Triggers = append(
			spec.Triggers, RendererTriggerSpec(t))
	}

	if r.Policy != nil {
		spec.Policy = &RendererPolicySpec{
			Elements:   r.Policy.Elements,
			Attributes: policyAttributes(r.Policy.Attributes),
			URLSchemes: r.Policy.URLSchemes,
		}
	}

	if r.CircuitBreaker != nil {
		spec.CircuitBreaker = &CircuitBreakerSpec{
			Timeout:          r.CircuitBreaker.Timeout,
			FailureThreshold: r.CircuitBreaker.FailureThreshold,
			OpenDuration:     r.CircuitBreaker.OpenDuration,
			MaxInFlight:      r.CircuitBreaker.MaxInFlight,
		}
	}

	return spec
}

// policyAttributes copies an attribute allowlist into a canonical shape:
// no map at all when it is empty, and a nil list for an element that
// allows no attributes.
//
// The normalization is here because the API carries the lists in a message
// per element, and a declared empty list comes back from the server as a
// message with no entries. Read as an empty list on one side and a nil one
// on the other, the two render as different JSON and the renderer diffs
// against itself for ever. They mean the same thing - an element with no
// entry keeps no attributes - so they have to compare the same too.
func policyAttributes(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string][]string, len(in))

	for element, attributes := range in {
		if len(attributes) == 0 {
			out[element] = nil

			continue
		}

		out[element] = attributes
	}

	return out
}

// htmlRenderingToRPC converts the desired HTML rendering configuration
// into the API message, in the arrangement renditions use: the defaults are
// resolved before this point, so what is sent is what the generation
// stores and what the next plan reads back.
//
// Nil for a configuration that declares neither block. An absent message is
// what says "render no HTML at all", so a deployment that never asked for
// it must not have an empty one with a filled-in image variant registered
// on its behalf.
func htmlRenderingToRPC(
	spec *HTMLRenderingSpec,
) *dist.HTMLRenderingConfiguration {
	if spec == nil {
		return nil
	}

	msg := dist.HTMLRenderingConfiguration{
		ImageVariant:  spec.ImageVariant,
		DocumentTypes: spec.DocumentTypes,
	}

	for _, r := range spec.Renderers {
		msg.Renderers = append(msg.Renderers, rendererToRPC(r))
	}

	return &msg
}

// rendererToRPC, and rendererFromRPC with it, deliberately never touch
// RendererConfiguration.full_document (field 6). Every invoked renderer is
// handed the whole document, so there is nothing left for the flag to say.
// It is only still on the wire because elephant-public-api v0.0.12 shipped
// with it, and v0.0.13 reserves the field number. Setting it would send a
// value the service does not read, and reading it would put a field in the
// plan that no configuration can express - a diff no apply can settle.
func rendererToRPC(spec RendererSpec) *dist.RendererConfiguration {
	msg := dist.RendererConfiguration{
		Name:          spec.Name,
		Kind:          spec.Kind,
		Revision:      spec.Revision,
		Script:        spec.Script,
		Url:           spec.URL,
		AllowInsecure: spec.AllowInsecure,
		DocumentTypes: spec.DocumentTypes,
		PolicyPreset:  spec.PolicyPreset,
	}

	for _, t := range spec.Triggers {
		msg.Triggers = append(msg.Triggers, &dist.RendererTrigger{
			BlockTypes: t.BlockTypes,
			Roles:      t.Roles,
		})
	}

	if spec.Policy != nil {
		policy := dist.RendererPolicy{
			Elements:   spec.Policy.Elements,
			UrlSchemes: spec.Policy.URLSchemes,
		}

		if len(spec.Policy.Attributes) > 0 {
			policy.Attributes = make(
				map[string]*dist.RendererAttributes,
				len(spec.Policy.Attributes))

			for element, attributes := range spec.Policy.Attributes {
				policy.Attributes[element] = &dist.RendererAttributes{
					Attributes: attributes,
				}
			}
		}

		msg.Policy = &policy
	}

	if spec.CircuitBreaker != nil {
		msg.CircuitBreaker = &dist.RendererCircuitBreaker{
			Timeout:          spec.CircuitBreaker.Timeout,
			FailureThreshold: spec.CircuitBreaker.FailureThreshold,
			OpenDuration:     spec.CircuitBreaker.OpenDuration,
			MaxInFlight:      spec.CircuitBreaker.MaxInFlight,
		}
	}

	return &msg
}

// htmlRenderingFromRPC reads the active generation's HTML rendering
// configuration. It is htmlRenderingToRPC's inverse, and the two
// normalizations in it are what make it one:
//
// An entirely empty message reads as no configuration at all. Empty
// document types render no type and no renderer is registered, which is
// what an absent message says, and reading it as a configuration would
// diff against a desired nil - a removal an apply carries out by sending
// nothing, which reads back the same way again on the next run.
//
// An empty image variant is the default, the same as an explicit
// "preview": the service documents it that way, so a generation that
// stored it compiled and one that stored it as sent have to plan alike.
func htmlRenderingFromRPC(
	msg *dist.HTMLRenderingConfiguration,
) *HTMLRenderingSpec {
	if msg == nil {
		return nil
	}

	if msg.GetImageVariant() == "" && len(msg.GetDocumentTypes()) == 0 &&
		len(msg.GetRenderers()) == 0 {
		return nil
	}

	spec := HTMLRenderingSpec{
		ImageVariant:  msg.GetImageVariant(),
		DocumentTypes: msg.GetDocumentTypes(),
	}

	if spec.ImageVariant == "" {
		spec.ImageVariant = DefaultImageVariant
	}

	for _, r := range msg.GetRenderers() {
		spec.Renderers = append(spec.Renderers, rendererFromRPC(r))
	}

	return &spec
}

func rendererFromRPC(msg *dist.RendererConfiguration) RendererSpec {
	spec := RendererSpec{
		Name:          msg.GetName(),
		Kind:          msg.GetKind(),
		Revision:      msg.GetRevision(),
		Script:        msg.GetScript(),
		URL:           msg.GetUrl(),
		AllowInsecure: msg.GetAllowInsecure(),
		DocumentTypes: msg.GetDocumentTypes(),
		PolicyPreset:  msg.GetPolicyPreset(),
	}

	for _, t := range msg.GetTriggers() {
		spec.Triggers = append(spec.Triggers, RendererTriggerSpec{
			BlockTypes: t.GetBlockTypes(),
			Roles:      t.GetRoles(),
		})
	}

	if policy := msg.GetPolicy(); policy != nil {
		attributes := make(
			map[string][]string, len(policy.GetAttributes()))

		for element, a := range policy.GetAttributes() {
			attributes[element] = a.GetAttributes()
		}

		spec.Policy = &RendererPolicySpec{
			Elements:   policy.GetElements(),
			Attributes: policyAttributes(attributes),
			URLSchemes: policy.GetUrlSchemes(),
		}
	}

	if breaker := msg.GetCircuitBreaker(); breaker != nil {
		read := CircuitBreakerSpec{
			Timeout: breaker.GetTimeout(),
		}

		// A js renderer reads the timeout and nothing else, which is why
		// the configuration refuses the other three on one and this
		// never sends them. A generation that answers with them filled
		// in - the service stores the configuration compiled - would
		// otherwise diff for ever against a configuration that has no
		// way to say them.
		if spec.Kind != RendererKindJS {
			read.FailureThreshold = breaker.GetFailureThreshold()
			read.OpenDuration = breaker.GetOpenDuration()
			read.MaxInFlight = breaker.GetMaxInFlight()
		}

		spec.CircuitBreaker = &read
	}

	return spec
}

// planRenderers builds the desired HTML rendering payload and the diff
// against the active generation's.
func planRenderers(
	conf *Config, active *HTMLRenderingSpec,
) (*HTMLRenderingSpec, []distconf.ConfigurationChange, error) {
	desired := htmlRenderingSpec(conf)

	configured := make(map[string]bool, len(conf.Documents))
	for _, doc := range conf.Documents {
		configured[doc.Type] = true
	}

	changes, err := planHTMLSettings(desired, active, configured)
	if err != nil {
		return nil, nil, err
	}

	rendererChanges, err := planRendererBlocks(desired, active, configured)
	if err != nil {
		return nil, nil, err
	}

	return desired, append(changes, rendererChanges...), nil
}

// planHTMLSettings diffs the html_rendering settings themselves - the
// image variant and the document types - separately from the renderers, so
// that a changed image variant does not read as every renderer changing.
func planHTMLSettings(
	desired *HTMLRenderingSpec,
	active *HTMLRenderingSpec,
	configured map[string]bool,
) ([]distconf.ConfigurationChange, error) {
	if desired == nil && active == nil {
		return nil, nil
	}

	const subject = "HTML rendering"

	if desired == nil {
		return []distconf.ConfigurationChange{
			&rendererPlanChange{
				subject: subject,
				op:      distconf.OpRemove,
			},
		}, nil
	}

	wanted, err := htmlSettingsJSON(desired)
	if err != nil {
		return nil, fmt.Errorf("render HTML rendering settings: %w", err)
	}

	warnings := documentTypeWarnings(
		"html_rendering", desired.DocumentTypes, configured)

	if active == nil {
		return []distconf.ConfigurationChange{
			&rendererPlanChange{
				subject:  subject,
				wanted:   wanted,
				op:       distconf.OpAdd,
				warnings: warnings,
			},
		}, nil
	}

	current, err := htmlSettingsJSON(active)
	if err != nil {
		return nil, fmt.Errorf(
			"render active HTML rendering settings: %w", err)
	}

	if current == wanted {
		return nil, nil
	}

	return []distconf.ConfigurationChange{
		&rendererPlanChange{
			subject:  subject,
			current:  current,
			wanted:   wanted,
			op:       distconf.OpUpdate,
			warnings: warnings,
		},
	}, nil
}

func planRendererBlocks(
	desired *HTMLRenderingSpec,
	active *HTMLRenderingSpec,
	configured map[string]bool,
) ([]distconf.ConfigurationChange, error) {
	var (
		changes []distconf.ConfigurationChange
		wantedR []RendererSpec
		activeR []RendererSpec
	)

	if desired != nil {
		wantedR = desired.Renderers
	}

	if active != nil {
		activeR = active.Renderers
	}

	activeByName := make(map[string]int, len(activeR))
	for i, r := range activeR {
		activeByName[r.Name] = i
	}

	seen := make(map[string]bool, len(wantedR))

	for i, r := range wantedR {
		seen[r.Name] = true

		wanted, err := rendererSpecJSON(i, r)
		if err != nil {
			return nil, fmt.Errorf(
				"render renderer %q: %w", r.Name, err)
		}

		warnings := rendererWarnings(r, configured)

		pos, ok := activeByName[r.Name]
		if !ok {
			changes = append(changes, &rendererPlanChange{
				subject:  rendererSubject(r.Name),
				wanted:   wanted,
				op:       distconf.OpAdd,
				warnings: warnings,
			})

			continue
		}

		curr := activeR[pos]

		current, err := rendererSpecJSON(pos, curr)
		if err != nil {
			return nil, fmt.Errorf(
				"render active renderer %q: %w", r.Name, err)
		}

		if current == wanted {
			continue
		}

		changes = append(changes, &rendererPlanChange{
			subject:    rendererSubject(r.Name),
			current:    current,
			wanted:     wanted,
			scriptDiff: rendererScriptDiff(curr, r),
			op:         distconf.OpUpdate,
			warnings:   warnings,
		})
	}

	for _, r := range activeR {
		if seen[r.Name] {
			continue
		}

		changes = append(changes, &rendererPlanChange{
			subject: rendererSubject(r.Name),
			op:      distconf.OpRemove,
		})
	}

	return changes, nil
}

func rendererSubject(name string) string {
	return fmt.Sprintf("renderer %q", name)
}

// rendererScriptDiff is the script change spelled out, since the spec
// diff only carries the hash of it. A script that appears or disappears is
// already visible there, so this is the both-sides case only.
func rendererScriptDiff(current RendererSpec, wanted RendererSpec) string {
	if current.Script == "" || wanted.Script == "" ||
		current.Script == wanted.Script {
		return ""
	}

	diff := textdiff.Unified(
		"current script", "wanted script",
		current.Script, wanted.Script)

	return strings.TrimRight(diff, "\n")
}

// htmlSettingsDisplay is the canonical JSON shape of the html_rendering
// settings for a plan line.
type htmlSettingsDisplay struct {
	ImageVariant  string   `json:"image_variant,omitempty"`
	DocumentTypes []string `json:"document_types,omitempty"`
}

// rendererDisplay is the canonical JSON shape of one renderer for a plan
// line. The script is a hash rather than the script: an escaped script
// reads as one unreadable line, and a change to it gets its own diff.
//
// The position is in there because a renderer's place in the declaration
// order decides which of two renderers answering for one block wins, so
// moving one is a change even when nothing about the renderer itself moved
// with it.
type rendererDisplay struct {
	Position       int                   `json:"position"`
	Kind           string                `json:"kind"`
	Revision       int64                 `json:"revision"`
	ScriptSHA256   string                `json:"script_sha256,omitempty"`
	URL            string                `json:"url,omitempty"`
	AllowInsecure  bool                  `json:"allow_insecure,omitempty"`
	Triggers       []RendererTriggerSpec `json:"triggers,omitempty"`
	DocumentTypes  []string              `json:"document_types,omitempty"`
	Policy         *RendererPolicySpec   `json:"policy,omitempty"`
	PolicyPreset   string                `json:"policy_preset,omitempty"`
	CircuitBreaker *CircuitBreakerSpec   `json:"circuit_breaker,omitempty"`
}

func htmlSettingsJSON(spec *HTMLRenderingSpec) (string, error) {
	data, err := json.MarshalIndent(htmlSettingsDisplay{
		ImageVariant:  spec.ImageVariant,
		DocumentTypes: spec.DocumentTypes,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal spec: %w", err)
	}

	return string(data), nil
}

func rendererSpecJSON(position int, spec RendererSpec) (string, error) {
	display := rendererDisplay{
		Position:       position,
		Kind:           spec.Kind,
		Revision:       spec.Revision,
		URL:            spec.URL,
		AllowInsecure:  spec.AllowInsecure,
		Triggers:       spec.Triggers,
		DocumentTypes:  spec.DocumentTypes,
		Policy:         spec.Policy,
		PolicyPreset:   spec.PolicyPreset,
		CircuitBreaker: spec.CircuitBreaker,
	}

	if spec.Script != "" {
		hash := sha256.Sum256([]byte(spec.Script))
		display.ScriptSHA256 = hex.EncodeToString(hash[:])
	}

	data, err := json.MarshalIndent(display, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal spec: %w", err)
	}

	return string(data), nil
}

// rendererWarnings covers the renderer configurations that are valid and
// probably not what somebody meant.
func rendererWarnings(
	spec RendererSpec, configured map[string]bool,
) []string {
	warnings := documentTypeWarnings(
		"renderer "+spec.Name, spec.DocumentTypes, configured)

	if spec.AllowInsecure {
		warnings = append(warnings, fmt.Sprintf(
			"renderer %q talks to %s without TLS, so the shared "+
				"secret is the only thing protecting the channel",
			spec.Name, spec.URL))
	}

	if spec.Policy == nil {
		return warnings
	}

	allowed := make(map[string]bool, len(spec.Policy.Elements))
	for _, e := range spec.Policy.Elements {
		allowed[e] = true
	}

	for element := range spec.Policy.Attributes {
		if allowed[element] {
			continue
		}

		warnings = append(warnings, fmt.Sprintf(
			"renderer %q allows attributes on %q, but the policy "+
				"does not allow the element itself, so the "+
				"attributes are read by nothing",
			spec.Name, element))
	}

	slices.Sort(warnings)

	return warnings
}

// documentTypeWarnings warns about a scope that names a document type the
// generation does not configure. It is not an error - a type can be added
// in the same apply as the scope that names it, but not in a different
// one - and a renderer scoped to a type nothing else configures never runs.
func documentTypeWarnings(
	subject string, types []string, configured map[string]bool,
) []string {
	var warnings []string

	for _, t := range types {
		if configured[t] {
			continue
		}

		warnings = append(warnings, fmt.Sprintf(
			"%s is scoped to the document type %q, which no "+
				"document block configures, so it never runs",
			subject, t))
	}

	return warnings
}

// rendererPlanChange is a change to the HTML rendering configuration:
// either one named renderer, or the html_rendering settings themselves.
type rendererPlanChange struct {
	subject    string
	current    string
	wanted     string
	scriptDiff string
	op         distconf.ChangeOp
	warnings   []string
}

func (c *rendererPlanChange) Describe() (distconf.ChangeOp, string) {
	switch c.op {
	case distconf.OpAdd:
		return distconf.OpAdd, fmt.Sprintf(
			"configure %s:\n%s", c.subject, c.wanted)
	case distconf.OpRemove:
		return distconf.OpRemove, fmt.Sprintf(
			"remove %s configuration", c.subject)
	case distconf.OpUpdate:
	}

	diff := textdiff.Unified("current", "wanted", c.current, c.wanted)

	message := fmt.Sprintf("update %s configuration:\n%s",
		c.subject, strings.TrimRight(diff, "\n"))

	if c.scriptDiff != "" {
		message += "\n" + c.scriptDiff
	}

	return distconf.OpUpdate, message
}

func (c *rendererPlanChange) Warnings() []string {
	return c.warnings
}

// summaryNone is how an empty list of blocks reads on a plan line, on
// both sides of the arrow.
const summaryNone = "none"

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

	wantedFacets := facetsToRPC(doc.Facets)

	if !facetsEqual(curr.FacetExpressions, wantedFacets) {
		lines = append(lines, fmt.Sprintf(
			"facets: %s => %s",
			facetSummary(curr.FacetExpressions),
			facetSummary(wantedFacets)))
	}

	wantedDelivery := deliveryFieldsToRPC(doc.DeliveryFields)

	if !deliveryFieldsEqual(curr.DeliveryFields, wantedDelivery) {
		lines = append(lines, fmt.Sprintf(
			"delivery_fields: %s => %s",
			deliveryFieldSummary(curr.DeliveryFields),
			deliveryFieldSummary(wantedDelivery)))
	}

	return strings.Join(lines, "\n")
}

// deliveryFieldsToRPC converts the configured delivery fields into the
// API representation. Nil for an empty set, so that a type without them
// round-trips to what it started as - an empty slice would make the
// server see a diff and register a generation on every apply.
//
// Sorted by name, and that is not cosmetic. A name is one field per type
// (the server refuses a duplicate), so the list is a set: reordering two
// delivery_field blocks in the HCL means nothing, and without a canonical
// order it would still print a diff line and register a whole new
// configuration generation on apply - the outcome "activated:false,
// changes:0" exists to make visible and avoid. Facets are deliberately not
// changed to match: several facet blocks may share a name and their values
// are unioned, so their order is arguably information. This one is not.
func deliveryFieldsToRPC(
	fields []DeliveryFieldConfig,
) []*dist.TypeDeliveryField {
	if len(fields) == 0 {
		return nil
	}

	out := make([]*dist.TypeDeliveryField, len(fields))

	for i, f := range fields {
		out[i] = &dist.TypeDeliveryField{
			Name:        f.Name,
			Kind:        f.Kind,
			Expression:  f.Expression,
			Description: f.Description,
		}
	}

	slices.SortFunc(out, func(x, y *dist.TypeDeliveryField) int {
		return strings.Compare(x.GetName(), y.GetName())
	})

	return out
}

// deliveryFieldsEqual compares two sets. Both sides come out of
// deliveryFieldsToRPC or out of the server, which stores what that sent,
// so both are already name-sorted and a positional comparison is a set
// comparison. Sorting a defensive copy of the current side first costs
// nothing and makes that independent of the server having preserved order.
func deliveryFieldsEqual(a, b []*dist.TypeDeliveryField) bool {
	as := slices.Clone(a)
	bs := slices.Clone(b)

	byName := func(x, y *dist.TypeDeliveryField) int {
		return strings.Compare(x.GetName(), y.GetName())
	}

	slices.SortFunc(as, byName)
	slices.SortFunc(bs, byName)

	return slices.EqualFunc(as, bs,
		func(x, y *dist.TypeDeliveryField) bool {
			return x.GetName() == y.GetName() &&
				x.GetKind() == y.GetKind() &&
				x.GetExpression() == y.GetExpression() &&
				x.GetDescription() == y.GetDescription()
		})
}

// deliveryFieldSummary renders delivery fields for a plan line.
func deliveryFieldSummary(fields []*dist.TypeDeliveryField) string {
	if len(fields) == 0 {
		return summaryNone
	}

	parts := make([]string, len(fields))

	for i, f := range fields {
		parts[i] = f.GetName() + ":" + f.GetKind() +
			"=" + f.GetExpression()
	}

	return strings.Join(parts, ", ")
}

// facetsToRPC converts the configured facet expressions into the API
// representation.
func facetsToRPC(facets []FacetConfig) []*dist.TypeFacetExpression {
	if len(facets) == 0 {
		return nil
	}

	out := make([]*dist.TypeFacetExpression, len(facets))

	for i, f := range facets {
		out[i] = &dist.TypeFacetExpression{
			Name:       f.Name,
			Expression: f.Expression,
		}
	}

	return out
}

// facetsEqual compares two sets of facet expressions. Order is part of
// the value, as it is for time expressions: the stored configuration is a
// list and is replaced whole.
func facetsEqual(a, b []*dist.TypeFacetExpression) bool {
	return slices.EqualFunc(a, b,
		func(x, y *dist.TypeFacetExpression) bool {
			return x.GetName() == y.GetName() &&
				x.GetExpression() == y.GetExpression()
		})
}

// facetSummary renders facet expressions for a plan line.
func facetSummary(facets []*dist.TypeFacetExpression) string {
	if len(facets) == 0 {
		return summaryNone
	}

	parts := make([]string, len(facets))

	for i, f := range facets {
		parts[i] = f.GetName() + "=" + f.GetExpression()
	}

	return strings.Join(parts, ", ")
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
		return summaryNone
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
