package distribution

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ttab/distconf"
	dist "github.com/ttab/elephant-public-api/distribution"
)

// The distribution service stores the compiled rendition configuration,
// with the per-kind defaults resolved. If we sent the raw configuration
// the active generation would never match the desired one, and every apply
// would report the same diff.
func TestRenditionConfigToRPCResolvesDefaults(t *testing.T) {
	entry := renditionConfigToRPC(RenditionsConfig{
		Kind: "image",
		Sources: []RenditionSourceConfig{
			{
				Name:       "tt-archive",
				Namespace:  "mm",
				LinkTypes:  []string{"tt/picture"},
				URIPattern: "^https?://tt\\.se/media/image/sdl([A-Za-z0-9._-]+)$",
			},
			{
				Name:       "repo",
				Namespace:  "repo",
				BlockTypes: []string{"core/graphic"},
				LinkRel:    "graphic",
				URIPattern: "^repo://([A-Za-z0-9._-]+)$",
			},
		},
	})

	defaulted := entry.Sources[0]

	if !slices.Equal(defaulted.BlockTypes, []string{"core/image"}) {
		t.Errorf("got block types %v, want [core/image]",
			defaulted.BlockTypes)
	}

	if defaulted.LinkRel != "image" {
		t.Errorf("got link rel %q, want image", defaulted.LinkRel)
	}

	explicit := entry.Sources[1]

	if !slices.Equal(explicit.BlockTypes, []string{"core/graphic"}) {
		t.Errorf("got block types %v, want [core/graphic]",
			explicit.BlockTypes)
	}

	if explicit.LinkRel != "graphic" {
		t.Errorf("got link rel %q, want graphic", explicit.LinkRel)
	}
}

// Kinds without built-in defaults are left as declared; the server rejects
// the registration if they are incomplete, and inventing values here would
// hide that.
func TestRenditionConfigToRPCUnknownKind(t *testing.T) {
	entry := renditionConfigToRPC(RenditionsConfig{
		Kind: "audio",
		Sources: []RenditionSourceConfig{
			{
				Name:       "repo",
				Namespace:  "repo",
				URIPattern: "^repo://([A-Za-z0-9._-]+)$",
			},
		},
	})

	src := entry.Sources[0]

	if len(src.BlockTypes) != 0 || src.LinkRel != "" {
		t.Errorf("got block types %v and link rel %q, want both empty",
			src.BlockTypes, src.LinkRel)
	}
}

// A delivery field name is one field per type, so the declarations are a
// set and their order in the HCL is not information. If the conversion did
// not put them in a canonical order, reordering two blocks would print a
// diff line and register a whole new configuration generation - on every
// apply, forever, since the server would keep storing the order it was
// last sent.
func TestDeliveryFieldsToRPC(t *testing.T) {
	if got := deliveryFieldsToRPC(nil); got != nil {
		t.Errorf("got %v for an empty set, want nil", got)
	}

	fields := []DeliveryFieldConfig{
		{
			Name:        "section",
			Kind:        KindKeyword,
			Expression:  ".links(rel='section')@{uuid}",
			Description: "The section.",
		},
		{
			Name:       "headline",
			Kind:       KindText,
			Expression: "@{title}",
		},
		{
			Name:       "newsvalue",
			Kind:       KindNumber,
			Expression: ".meta(type='core/newsvalue')@{value}",
		},
	}

	converted := deliveryFieldsToRPC(fields)

	names := make([]string, len(converted))
	for i, f := range converted {
		names[i] = f.GetName()
	}

	if !slices.Equal(names, []string{"headline", "newsvalue", "section"}) {
		t.Errorf("got the order %v, want it sorted by name", names)
	}

	section := converted[2]

	if section.GetKind() != KindKeyword ||
		section.GetExpression() != ".links(rel='section')@{uuid}" ||
		section.GetDescription() != "The section." {
		t.Errorf("field not converted whole: %+v", section)
	}

	reordered := deliveryFieldsToRPC([]DeliveryFieldConfig{
		fields[2], fields[0], fields[1],
	})

	if !deliveryFieldsEqual(converted, reordered) {
		t.Error("reordering the blocks is not a no-op")
	}

	// The description is what an editor shows beside the name, so a
	// change to it is a change an apply has to carry.
	described := slices.Clone(fields)
	described[1].Description = "The headline."

	if deliveryFieldsEqual(converted, deliveryFieldsToRPC(described)) {
		t.Error("a description-only change compares equal")
	}
}

// rendererTestConfig is a configuration with one script and one remote
// renderer, defaults resolved the way ReadConfigFromDirectory resolves
// them.
func rendererTestConfig() *Config {
	return &Config{
		Documents: []DocumentConfig{
			{Type: "core/article"},
		},
		HTMLRendering: []HTMLRenderingConfig{
			{
				ImageVariant:  DefaultImageVariant,
				DocumentTypes: []string{"core/article"},
			},
		},
		Renderers: []RendererConfig{
			{
				Name:         "factbox",
				Kind:         RendererKindJS,
				Revision:     1,
				ScriptFile:   "factbox.js",
				Script:       "export function render() {}",
				PolicyPreset: PolicyPresetStrict,
				CircuitBreaker: &CircuitBreakerConfig{
					Timeout: DefaultRendererTimeout,
				},
			},
			{
				Name:         "chart",
				Kind:         RendererKindRemote,
				Revision:     2,
				URL:          "https://renderers.example.com/chart",
				PolicyPreset: PolicyPresetRichText,
				CircuitBreaker: &CircuitBreakerConfig{
					Timeout:          DefaultRendererTimeout,
					FailureThreshold: DefaultRendererFailureThreshold,
					OpenDuration:     DefaultRendererOpenDuration,
					MaxInFlight:      DefaultRendererMaxInFlight,
				},
			},
		},
	}
}

// fullRendererTestConfig is rendererTestConfig with everything that has to
// survive a trip through the API messages: triggers, a spelled-out policy,
// an element that allows no attributes, and the settings that only mean
// something for a remote endpoint.
func fullRendererTestConfig() *Config {
	conf := rendererTestConfig()

	conf.Renderers[0].PolicyPreset = ""
	conf.Renderers[0].Policy = &RendererPolicyConfig{
		Elements: []string{"aside", "p", "a"},
		Attributes: map[string][]string{
			"a": {"href"},
			// An element that allows the element and no
			// attributes on it.
			"aside": {},
		},
		URLSchemes: []string{"https"},
	}
	conf.Renderers[0].Triggers = []RendererTriggerConfig{
		{BlockTypes: []string{"core/factbox"}},
		{Roles: []string{"sidebar"}},
	}
	conf.Renderers[0].DocumentTypes = []string{"core/article"}

	conf.Renderers[1].Triggers = []RendererTriggerConfig{
		{BlockTypes: []string{"tt/chart"}, Roles: []string{"body"}},
	}

	return conf
}

// The two conversions have to be each other's inverse: the active
// generation is read back through one what the apply sent through the
// other, so anything the pair does not preserve is a diff that every apply
// reports and no apply settles.
func TestRenderersRoundTripThroughRPC(t *testing.T) {
	conf := fullRendererTestConfig()

	desired, changes, err := planRenderers(conf, nil)
	if err != nil {
		t.Fatalf("plan renderers: %v", err)
	}

	if len(changes) == 0 {
		t.Fatal("no changes against an empty deployment")
	}

	active := htmlRenderingFromRPC(htmlRenderingToRPC(desired))

	if !reflect.DeepEqual(desired, active) {
		t.Errorf("the round trip is not the identity:\n got %+v\nwant %+v",
			active, desired)
	}

	// The same thing said as a plan, which is what an operator sees: a
	// plan run against the generation the previous apply registered has
	// nothing to report.
	_, changes, err = planRenderers(conf, active)
	if err != nil {
		t.Fatalf("plan against the round-tripped configuration: %v", err)
	}

	if len(changes) != 0 {
		t.Errorf("got %d changes after a round trip: %v",
			len(changes), describeChanges(changes))
	}
}

// There is deliberately no test here for RendererConfiguration.full_document,
// the field an earlier draft used to ask for the whole document beside the
// blocks a renderer claimed. Every invoked renderer is handed the whole
// document now, so nothing can set the flag and nothing may read it -
// rendererToRPC/rendererFromRPC say why - and elephant-public-api v0.0.13
// *reserves* field 6, which is what makes the guarantee the contract's
// rather than a test's. A test could only state it by naming a generated
// field that the very next dependency bump removes, so it would break the
// build in exactly the upgrade window it reasoned about. What an operator
// can still do wrong is keep the attribute in their HCL, and that is
// refused, pinned by config_test.go's "the full_document flag of the
// block-claiming draft".

// An absent message and an empty one say the same thing - no type renders
// HTML and no renderer is registered - so they have to read the same way.
// An empty one read as a configuration would diff against a desired nil on
// every run, and the apply that carries the removal out sends nothing,
// which is what comes back as the empty message again.
func TestHTMLRenderingFromRPCEmpty(t *testing.T) {
	if spec := htmlRenderingFromRPC(nil); spec != nil {
		t.Errorf("got %+v for an absent message, want nil", spec)
	}

	spec := htmlRenderingFromRPC(&dist.HTMLRenderingConfiguration{})
	if spec != nil {
		t.Errorf("got %+v for an empty message, want nil", spec)
	}

	if msg := htmlRenderingToRPC(nil); msg != nil {
		t.Errorf("got %+v for an absent spec, want nil", msg)
	}
}

// An empty image variant is the default, so a generation that stored it
// compiled and one that stored it as sent have to plan alike.
func TestHTMLRenderingFromRPCDefaultsTheImageVariant(t *testing.T) {
	spec := htmlRenderingFromRPC(&dist.HTMLRenderingConfiguration{
		DocumentTypes: []string{"core/article"},
	})

	if spec == nil {
		t.Fatal("no spec for a message with document types")
	}

	if spec.ImageVariant != DefaultImageVariant {
		t.Errorf("got the image variant %q, want the default %q",
			spec.ImageVariant, DefaultImageVariant)
	}
}

// A js renderer reads the circuit breaker timeout and nothing else, and the
// configuration refuses the other three settings on one - so a generation
// that answers with them filled in, as it would if the service materialized
// the defaults it compiles, must not read as a change nothing can express.
func TestRendererFromRPCIgnoresRemoteBreakerSettingsOnAScript(t *testing.T) {
	spec := rendererFromRPC(&dist.RendererConfiguration{
		Name:     "factbox",
		Kind:     RendererKindJS,
		Revision: 1,
		Script:   "export function render() {}",
		CircuitBreaker: &dist.RendererCircuitBreaker{
			Timeout:          DefaultRendererTimeout,
			FailureThreshold: DefaultRendererFailureThreshold,
			OpenDuration:     DefaultRendererOpenDuration,
			MaxInFlight:      DefaultRendererMaxInFlight,
		},
	})

	breaker := spec.CircuitBreaker

	if breaker == nil {
		t.Fatal("the breaker was dropped whole")
	}

	if breaker.Timeout != DefaultRendererTimeout {
		t.Errorf("got the timeout %q, want %q",
			breaker.Timeout, DefaultRendererTimeout)
	}

	if breaker.FailureThreshold != 0 || breaker.OpenDuration != "" ||
		breaker.MaxInFlight != 0 {
		t.Errorf("remote-only breaker settings read on a script renderer: %+v",
			breaker)
	}
}

// stubConfiguration answers the two RPCs a plan makes, and stores what it
// was registered with the way the service does - so a second plan against
// it is the apply-then-plan an operator runs.
//
// The embedded interface is nil on purpose: a plan reaches nothing else on
// the service, and a call that does panics here rather than being answered
// with something plausible.
type stubConfiguration struct {
	dist.Configuration

	generation *dist.ConfigGeneration
	registered *dist.RegisterConfigGenerationRequest
}

func (s *stubConfiguration) GetActiveConfigGeneration(
	_ context.Context, _ *dist.GetActiveConfigGenerationRequest,
) (*dist.GetActiveConfigGenerationResponse, error) {
	return &dist.GetActiveConfigGenerationResponse{
		Generation: s.generation,
	}, nil
}

func (s *stubConfiguration) RegisterConfigGeneration(
	_ context.Context, req *dist.RegisterConfigGenerationRequest,
) (*dist.RegisterConfigGenerationResponse, error) {
	s.registered = req

	s.generation = &dist.ConfigGeneration{
		Id:         s.generation.GetId() + 1,
		Types:      req.GetTypes(),
		Renditions: req.GetRenditions(),
		Renderers:  req.GetRenderers(),
	}

	return &dist.RegisterConfigGenerationResponse{
		Generation: s.generation,
	}, nil
}

// The whole point of resolving the defaults before the payload is built:
// an apply followed by a plan reports nothing. The renderer diff is in
// Plan.Changes, so a renderer configuration that read as changed here
// would register a new generation on every run.
func TestApplyThenPlanIsAFixpoint(t *testing.T) {
	ctx := context.Background()

	// No document blocks: the types would have to be declared by a loaded
	// schema, and this is about the renderers.
	conf := fullRendererTestConfig()
	conf.Documents = nil

	stub := stubConfiguration{}
	clients := StaticClients{Configuration: &stub}

	plan, err := BuildPlan(ctx, &clients, conf, nil, "first")
	if err != nil {
		t.Fatalf("build the first plan: %v", err)
	}

	if !plan.HasChanges() {
		t.Fatal("the first plan reports no changes")
	}

	_, err = plan.Execute(ctx, &clients)
	if err != nil {
		t.Fatalf("execute the plan: %v", err)
	}

	sent := stub.registered.GetRenderers()

	if sent == nil {
		t.Fatal("the registration carried no HTML rendering configuration")
	}

	if len(sent.GetRenderers()) != 2 {
		t.Fatalf("got %d renderers in the payload, want 2",
			len(sent.GetRenderers()))
	}

	// The script content travels with the generation, not the path.
	if sent.GetRenderers()[0].GetScript() != "export function render() {}" {
		t.Errorf("the payload carries the script as %q",
			sent.GetRenderers()[0].GetScript())
	}

	next, err := BuildPlan(ctx, &clients, conf, nil, "second")
	if err != nil {
		t.Fatalf("build the second plan: %v", err)
	}

	if next.HasChanges() {
		t.Errorf("the plan after the apply reports %d changes: %v",
			len(next.Changes), describeChanges(next.Changes))
	}
}

// describeChanges renders the changes for a failure message.
func describeChanges(changes []distconf.ConfigurationChange) []string {
	out := make([]string, len(changes))

	for i, c := range changes {
		op, message := c.Describe()
		out[i] = string(op) + " " + message
	}

	return out
}

// A configuration that declares neither block sends nothing at all: a
// deployment that never asked for HTML rendering must not have a block of
// defaults registered on its behalf.
func TestPlanRenderersEmpty(t *testing.T) {
	desired, changes, err := planRenderers(&Config{}, nil)
	if err != nil {
		t.Fatalf("plan renderers: %v", err)
	}

	if desired != nil {
		t.Errorf("got the spec %+v for an empty configuration, want nil",
			desired)
	}

	if len(changes) != 0 {
		t.Errorf("got %d changes, want none", len(changes))
	}
}

func TestPlanRenderersSpec(t *testing.T) {
	desired, changes, err := planRenderers(rendererTestConfig(), nil)
	if err != nil {
		t.Fatalf("plan renderers: %v", err)
	}

	if desired == nil {
		t.Fatal("no desired spec")
	}

	if desired.ImageVariant != DefaultImageVariant {
		t.Errorf("image variant not carried: %q", desired.ImageVariant)
	}

	// Declaration order is information: where two renderers answer for one
	// block, the first one in this order wins.
	names := make([]string, len(desired.Renderers))
	for i, r := range desired.Renderers {
		names[i] = r.Name
	}

	if !slices.Equal(names, []string{"factbox", "chart"}) {
		t.Errorf("got the order %v, want the declaration order", names)
	}

	// The script content travels with the generation, not the path.
	if desired.Renderers[0].Script != "export function render() {}" {
		t.Errorf("script not carried: %q", desired.Renderers[0].Script)
	}

	// The settings and both renderers, against an empty deployment.
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3: %v", len(changes),
			describeChanges(changes))
	}

	for _, c := range changes {
		op, message := c.Describe()
		if op != distconf.OpAdd {
			t.Errorf("got the op %q for %q, want an addition",
				op, message)
		}
	}
}

// The plan line carries a hash of the script rather than the script: an
// escaped script reads as one unreadable line, and the change to it gets
// its own diff.
func TestPlanRenderersScriptDiff(t *testing.T) {
	active, _, err := planRenderers(rendererTestConfig(), nil)
	if err != nil {
		t.Fatalf("plan the active side: %v", err)
	}

	conf := rendererTestConfig()
	conf.Renderers[0].Script = "export function render() { return null }"

	_, changes, err := planRenderers(conf, active)
	if err != nil {
		t.Fatalf("plan renderers: %v", err)
	}

	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %v",
			len(changes), describeChanges(changes))
	}

	op, message := changes[0].Describe()

	if op != distconf.OpUpdate {
		t.Errorf("got the op %q, want an update", op)
	}

	if !strings.Contains(message, "script_sha256") {
		t.Errorf("the change does not name the script hash: %s", message)
	}

	if strings.Contains(message, `\n`) {
		t.Errorf("the change carries an escaped script: %s", message)
	}

	if !strings.Contains(message, "return null") {
		t.Errorf("the change does not diff the script: %s", message)
	}
}

// Moving a renderer is a change even when nothing about the renderer moved
// with it: the order decides which of two renderers answering for one block
// wins.
func TestPlanRenderersReorder(t *testing.T) {
	active, _, err := planRenderers(rendererTestConfig(), nil)
	if err != nil {
		t.Fatalf("plan the active side: %v", err)
	}

	conf := rendererTestConfig()
	conf.Renderers[0], conf.Renderers[1] = conf.Renderers[1], conf.Renderers[0]

	_, changes, err := planRenderers(conf, active)
	if err != nil {
		t.Fatalf("plan renderers: %v", err)
	}

	if len(changes) != 2 {
		t.Fatalf("got %d changes for a reorder, want 2: %v",
			len(changes), describeChanges(changes))
	}
}

// An unchanged configuration is no change at all. It is the property the
// defaults are resolved for: a diff no apply can settle registers a new
// generation on every run.
func TestPlanRenderersUnchanged(t *testing.T) {
	active, _, err := planRenderers(rendererTestConfig(), nil)
	if err != nil {
		t.Fatalf("plan the active side: %v", err)
	}

	_, changes, err := planRenderers(rendererTestConfig(), active)
	if err != nil {
		t.Fatalf("plan renderers: %v", err)
	}

	if len(changes) != 0 {
		t.Errorf("got %d changes for an unchanged configuration: %v",
			len(changes), describeChanges(changes))
	}
}

func TestPlanRenderersRemoval(t *testing.T) {
	active, _, err := planRenderers(rendererTestConfig(), nil)
	if err != nil {
		t.Fatalf("plan the active side: %v", err)
	}

	conf := rendererTestConfig()
	conf.Renderers = conf.Renderers[:1]

	_, changes, err := planRenderers(conf, active)
	if err != nil {
		t.Fatalf("plan renderers: %v", err)
	}

	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1: %v",
			len(changes), describeChanges(changes))
	}

	op, message := changes[0].Describe()

	if op != distconf.OpRemove || !strings.Contains(message, `renderer "chart"`) {
		t.Errorf("unexpected change: %s %s", op, message)
	}
}

// The warnings are the configurations that are valid and probably not what
// somebody meant. A renderer scoped to a type nothing configures never
// runs, and a policy that allows attributes on an element it strips reads
// as permitting something it does not.
func TestPlanRenderersWarnings(t *testing.T) {
	conf := rendererTestConfig()
	conf.Renderers[0].DocumentTypes = []string{"core/planning-item"}
	conf.Renderers[0].PolicyPreset = ""
	conf.Renderers[0].Policy = &RendererPolicyConfig{
		Elements:   []string{"p"},
		Attributes: map[string][]string{"aside": {"class"}},
	}
	conf.Renderers[1].AllowInsecure = true

	_, changes, err := planRenderers(conf, nil)
	if err != nil {
		t.Fatalf("plan renderers: %v", err)
	}

	var warnings []string

	for _, c := range changes {
		warnings = append(warnings, c.Warnings()...)
	}

	want := []string{
		"core/planning-item",
		"does not allow the element itself",
		"without TLS",
	}

	for _, part := range want {
		var found bool

		for _, w := range warnings {
			if strings.Contains(w, part) {
				found = true
			}
		}

		if !found {
			t.Errorf("no warning mentions %q: %v", part, warnings)
		}
	}
}
