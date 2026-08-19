package distribution

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

// Renderer kinds. Both kinds behave identically apart from where the code
// runs, so the kind decides which of the two sources - a script file or an
// endpoint - the renderer must declare, and nothing else.
const (
	// RendererKindJS is a script we run in-process, once per document it
	// is invoked for.
	RendererKindJS = "js"
	// RendererKindRemote is an HTTP endpoint we POST the document to,
	// once per document the renderer is invoked for. Operator-configured,
	// so a private in-cluster address is a legitimate endpoint, but the
	// channel trust is the shared secret plus TLS - which is why https is
	// required unless the block asks for otherwise.
	RendererKindRemote = "remote"
)

// Sanitizer policy presets. A renderer's HTML is sanitized before it
// reaches a consumer, and the presets are the shared vocabulary for what
// "strict" and "rich text" mean - the service exports the same two names,
// so a preset is the way to stay in step with them instead of restating a
// policy that then drifts.
const (
	// PolicyPresetStrict is the narrowest vocabulary that is still
	// HTML: inline emphasis and links, plus the paragraph and line
	// break a block of prose needs. It is NOT "strip everything" - a
	// renderer whose policy admits no markup cannot render anything,
	// which is why the service's preset of the same name is not
	// bluemonday's strict policy either.
	PolicyPresetStrict = "strict"
	// PolicyPresetRichText allows the inline and block markup the
	// built-in renderers themselves emit.
	PolicyPresetRichText = "rich-text"
)

// DefaultImageVariant is the rendition variant the images of a rendered
// fragment link to when the html_rendering block does not name one.
const DefaultImageVariant = "preview"

// Circuit breaker defaults. They are resolved here rather than left to the
// service for the same reason the rendition defaults are: the service
// stores the compiled configuration, so a block that leaves them out comes
// back from the server with them filled in, and a desired configuration
// that left them out would diff against it on every apply.
const (
	// DefaultRendererTimeout bounds one call to one renderer. It is the
	// only circuit breaker setting a js renderer has - the rest of them
	// describe a remote endpoint's failure behaviour.
	DefaultRendererTimeout = "1s"
	// DefaultRendererFailureThreshold is how many consecutive failures
	// open the breaker.
	DefaultRendererFailureThreshold = 5
	// DefaultRendererOpenDuration is how long the breaker stays open
	// before it admits one probe.
	DefaultRendererOpenDuration = "30s"
	// DefaultRendererMaxInFlight bounds concurrent calls to one remote
	// renderer.
	DefaultRendererMaxInFlight = 4
)

// rendererNamePattern is what a renderer may be called. A remote
// renderer's shared secret is resolved from the environment variable
// REMOTE_SECRET_<NAME>, so the name has to survive uppercasing into one -
// the same reason the delivery key ids are spelled this way.
var rendererNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// HTMLRenderingConfig configures the delivery-time HTML rendering of
// documents: the rendition variant the fragment's images link to, and
// which document types render HTML at all.
//
// At most one html_rendering block may be declared across a configuration
// directory. Leaving the block out entirely renders NO HTML at all: the type
// list is the opt-in, so a deployment that has never thought about HTML
// delivers none rather than all of it.
type HTMLRenderingConfig struct {
	// ImageVariant is the rendition variant the fragment's images link
	// to. It is service-level by construction - a request never picks
	// one, since the fragment is cached and one render serves every
	// caller - and defaults to DefaultImageVariant.
	ImageVariant string `hcl:"image_variant,optional"`
	// DocumentTypes are the document types that render HTML. The list is
	// the opt-in, not a narrowing: none at all means NO type renders
	// HTML, which is the opposite of the empty-means-everything
	// convention a trigger's block types follow. A deployment that wants
	// HTML for its articles names them, and its planning items stay out
	// of it - a fragment nobody asked for is what the scoping exists to
	// avoid.
	DocumentTypes []string `hcl:"document_types,optional"`
}

// resolveHTMLRendering verifies that at most one html_rendering block was
// declared and fills in the image variant default. HCL only rejects
// duplicate blocks within one file, so a second block in a second file has
// to be caught after the merge, the way a duplicate renditions kind is.
func resolveHTMLRendering(conf *Config) error {
	if len(conf.HTMLRendering) > 1 {
		return fmt.Errorf(
			"html_rendering declared %d times, only one block may be declared",
			len(conf.HTMLRendering))
	}

	html := conf.htmlRendering()
	if html == nil {
		return nil
	}

	if html.ImageVariant == "" {
		html.ImageVariant = DefaultImageVariant
	}

	return nil
}

// htmlRendering is the declared html_rendering block, or nil when the
// configuration declares none. Only valid after resolveHTMLRendering has
// run: it is the check that there is at most one.
func (c *Config) htmlRendering() *HTMLRenderingConfig {
	if len(c.HTMLRendering) == 0 {
		return nil
	}

	return &c.HTMLRendering[0]
}

// RendererConfig registers one HTML renderer extension. The label names
// it, and the name is not decoration: it labels the renderer's metrics and
// log lines, and a remote renderer's shared secret is looked up under it.
//
// A trigger is an invocation condition, not a claim on blocks: a renderer
// is called when the document is one of its document types and some
// top-level block matches one of its triggers, and it is then handed the
// whole document and answers for whichever top-level blocks it likes. Two
// renderers may answer for the same block, which is why the declaration
// order is still information, unlike a set of delivery fields: the first
// renderer in configuration order wins a replacement collision, and
// insertions are applied in that order too. A renderer that fails, for any
// reason at all, contributes nothing: the blocks it would have answered
// for fall back to the built-in renderers and the delivery goes out
// regardless.
type RendererConfig struct {
	Name string `hcl:"name,label"`
	// Kind is RendererKindJS or RendererKindRemote.
	Kind string `hcl:"kind"`
	// Revision busts the render cache. The cache is keyed on the
	// configuration that affects output, and a remote renderer's output
	// can change without its configuration changing at all - so a
	// deployment that changes what the endpoint returns has to bump
	// this. Defaults to 1.
	Revision int64 `hcl:"revision,optional"`
	// ScriptFile is the js renderer's script, relative to the
	// configuration directory. Mutually exclusive with URL.
	ScriptFile string `hcl:"script_file,optional"`
	// URL is the remote renderer's endpoint. Mutually exclusive with
	// ScriptFile.
	URL string `hcl:"url,optional"`
	// AllowInsecure permits a plain http endpoint. Renderer URLs are
	// operator-configured, so there is no forbidden-address predicate
	// here - a private in-cluster endpoint is the expected deployment -
	// but dropping TLS leaves the shared secret as the only thing
	// between the endpoint and the network.
	AllowInsecure bool `hcl:"allow_insecure,optional"`
	// Triggers decide when the renderer is invoked: it is called for a
	// document in which some top-level block matches one of them. None at
	// all means it is always invoked for its document types.
	Triggers []RendererTriggerConfig `hcl:"trigger,block"`
	// DocumentTypes limits the renderer to the listed document types.
	// None at all means every type.
	DocumentTypes []string `hcl:"document_types,optional"`
	// Policy is the sanitizer the renderer's output is passed through.
	// Exactly one of Policy and PolicyPreset must be declared.
	Policy *RendererPolicyConfig `hcl:"policy,block"`
	// PolicyPreset names a shared policy instead of spelling one out:
	// PolicyPresetStrict or PolicyPresetRichText.
	PolicyPreset string `hcl:"policy_preset,optional"`
	// CircuitBreaker bounds what a slow or failing renderer costs. The
	// timeout applies to both kinds; the rest describe a remote
	// endpoint. Omitted means the defaults.
	CircuitBreaker *CircuitBreakerConfig `hcl:"circuit_breaker,block"`

	// Script is the content of ScriptFile, resolved by
	// ReadConfigFromDirectory. It is not an HCL attribute: a renderer
	// script is a file, the way a transform script can be, and there is
	// no reason to inline one in the configuration.
	Script string
}

// RendererTriggerConfig is one condition on the document's top-level
// blocks: the block types, optionally narrowed to the roles. A trigger has
// to select something - an empty block matches every document, which is
// what leaving the triggers out says, so it is refused rather than read as
// a synonym.
type RendererTriggerConfig struct {
	BlockTypes []string `hcl:"block_types,optional"`
	Roles      []string `hcl:"roles,optional"`
}

// RendererPolicyConfig is the sanitizer a renderer's output is passed
// through. Everything a renderer returns is sanitized: the code runs
// outside the service, and its output is embedded in what a consumer
// renders.
type RendererPolicyConfig struct {
	// Elements are the element names the policy allows. Everything else
	// is stripped.
	Elements []string `hcl:"elements,optional"`
	// Attributes are the allowed attributes per element name.
	Attributes map[string][]string `hcl:"attributes,optional"`
	// URLSchemes are the schemes a URL-carrying attribute may use.
	URLSchemes []string `hcl:"url_schemes,optional"`
}

// CircuitBreakerConfig bounds what a failing renderer costs. Durations are
// Go duration strings ("1s", "500ms").
//
// The two counters are int32 because that is what the API messages carry.
// They are small numbers either way, and matching the wire type means a
// value that does not fit is refused where it is written - by the HCL
// decoder, naming the file and the line - rather than narrowed on its way
// out to something the plan never showed anybody.
type CircuitBreakerConfig struct {
	// Timeout bounds one call. It is the only field a js renderer reads.
	Timeout string `hcl:"timeout,optional"`
	// FailureThreshold is the number of consecutive failures that opens
	// the breaker.
	FailureThreshold int32 `hcl:"failure_threshold,optional"`
	// OpenDuration is how long the breaker stays open before it admits a
	// probe.
	OpenDuration string `hcl:"open_duration,optional"`
	// MaxInFlight bounds concurrent calls. A call that cannot get a slot
	// within the timeout is a failure like any other.
	MaxInFlight int32 `hcl:"max_in_flight,optional"`
}

// resolveRenderers validates the renderer blocks and fills in the
// defaults, so that what is planned and sent is what the service stores.
//
// The mutual exclusion of script_file and url is checked while the script
// is read, in resolveScriptFiles, the way it is for a transform script.
func resolveRenderers(renderers []RendererConfig) error {
	seen := make(map[string]bool, len(renderers))

	for i := range renderers {
		r := &renderers[i]

		if !rendererNamePattern.MatchString(r.Name) {
			return fmt.Errorf(
				"renderer %q: a renderer name must match %s, since a remote renderer's secret is read from REMOTE_SECRET_<NAME>",
				r.Name, rendererNamePattern)
		}

		if seen[r.Name] {
			return fmt.Errorf(
				"renderer %q declared more than once", r.Name)
		}

		seen[r.Name] = true

		err := resolveRenderer(r)
		if err != nil {
			return fmt.Errorf("renderer %q: %w", r.Name, err)
		}
	}

	return nil
}

func resolveRenderer(r *RendererConfig) error {
	switch r.Kind {
	case RendererKindJS:
		err := checkJSRenderer(r)
		if err != nil {
			return err
		}
	case RendererKindRemote:
		err := checkRemoteRenderer(r)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"unknown kind %q, expected %q or %q",
			r.Kind, RendererKindJS, RendererKindRemote)
	}

	if r.Revision < 0 {
		return fmt.Errorf(
			"revision %d is negative; it is a counter a deployment bumps when the renderer's output changes",
			r.Revision)
	}

	if r.Revision == 0 {
		r.Revision = 1
	}

	for i, t := range r.Triggers {
		if len(t.BlockTypes) == 0 && len(t.Roles) == 0 {
			return fmt.Errorf(
				"trigger %d selects nothing: give it block_types or"+
					" roles, or leave the triggers out entirely to be"+
					" invoked for every document",
				i)
		}
	}

	err := checkRendererPolicy(r)
	if err != nil {
		return err
	}

	return resolveCircuitBreaker(r)
}

// checkJSRenderer checks that the renderer has a script, and that it does
// not carry the settings that only mean something for a remote endpoint.
// Those decode cleanly on a js renderer and are read by nothing at all
// there, which is the same reason a time expression under the wrong anchor
// is refused.
func checkJSRenderer(r *RendererConfig) error {
	if r.ScriptFile == "" {
		return fmt.Errorf(
			"the %q kind needs a script_file", RendererKindJS)
	}

	if r.Script == "" {
		return fmt.Errorf("the script file %q is empty", r.ScriptFile)
	}

	if r.AllowInsecure {
		return errors.New(
			"allow_insecure describes an endpoint, and this renderer is a script")
	}

	breaker := r.CircuitBreaker
	if breaker == nil {
		return nil
	}

	if breaker.FailureThreshold != 0 || breaker.OpenDuration != "" ||
		breaker.MaxInFlight != 0 {
		return errors.New(
			"a script renderer reads the circuit_breaker timeout and" +
				" nothing else: failure_threshold, open_duration and" +
				" max_in_flight describe a remote endpoint")
	}

	return nil
}

func checkRemoteRenderer(r *RendererConfig) error {
	if r.URL == "" {
		return fmt.Errorf(
			"the %q kind needs a url", RendererKindRemote)
	}

	u, err := url.Parse(r.URL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}

	if u.Host == "" {
		return fmt.Errorf("the url %q names no host", r.URL)
	}

	switch u.Scheme {
	case "https":
	case "http":
		if !r.AllowInsecure {
			return fmt.Errorf(
				"the url %q is not https; set allow_insecure to accept the shared secret as the only channel protection",
				r.URL)
		}
	default:
		return fmt.Errorf(
			"the url %q has the scheme %q, expected https or http",
			r.URL, u.Scheme)
	}

	return nil
}

func checkRendererPolicy(r *RendererConfig) error {
	switch {
	case r.Policy != nil && r.PolicyPreset != "":
		return errors.New(
			"policy and policy_preset are mutually exclusive")
	case r.Policy == nil && r.PolicyPreset == "":
		return fmt.Errorf(
			"declare a policy block or a policy_preset (%q or %q):"+
				" everything a renderer returns is sanitized, and a"+
				" renderer with no policy would deliver nothing",
			PolicyPresetStrict, PolicyPresetRichText)
	case r.PolicyPreset != "":
		switch r.PolicyPreset {
		case PolicyPresetStrict, PolicyPresetRichText:
			return nil
		default:
			return fmt.Errorf(
				"unknown policy_preset %q, expected %q or %q",
				r.PolicyPreset, PolicyPresetStrict,
				PolicyPresetRichText)
		}
	}

	if len(r.Policy.Elements) == 0 {
		return fmt.Errorf(
			"the policy allows no elements, which strips every"+
				" renderer output whole; use policy_preset = %q to"+
				" say that on purpose",
			PolicyPresetStrict)
	}

	return nil
}

// resolveCircuitBreaker fills in the defaults and normalizes the
// durations, so that "1000ms" and "1s" are the same configuration rather
// than a diff. A js renderer only gets the timeout: the rest of the fields
// describe a remote endpoint, and filling them in would send the service
// settings it reads for nothing.
func resolveCircuitBreaker(r *RendererConfig) error {
	if r.CircuitBreaker == nil {
		r.CircuitBreaker = &CircuitBreakerConfig{}
	}

	breaker := r.CircuitBreaker

	timeout, err := resolveDuration(
		breaker.Timeout, DefaultRendererTimeout, "timeout")
	if err != nil {
		return err
	}

	breaker.Timeout = timeout

	if r.Kind == RendererKindJS {
		return nil
	}

	open, err := resolveDuration(
		breaker.OpenDuration, DefaultRendererOpenDuration,
		"open_duration")
	if err != nil {
		return err
	}

	breaker.OpenDuration = open

	if breaker.FailureThreshold < 0 {
		return fmt.Errorf(
			"failure_threshold %d is negative",
			breaker.FailureThreshold)
	}

	if breaker.FailureThreshold == 0 {
		breaker.FailureThreshold = DefaultRendererFailureThreshold
	}

	if breaker.MaxInFlight < 0 {
		return fmt.Errorf(
			"max_in_flight %d is negative", breaker.MaxInFlight)
	}

	if breaker.MaxInFlight == 0 {
		breaker.MaxInFlight = DefaultRendererMaxInFlight
	}

	return nil
}

func resolveDuration(value string, def string, name string) (string, error) {
	if value == "" {
		value = def
	}

	d, err := time.ParseDuration(value)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}

	if d <= 0 {
		return "", fmt.Errorf(
			"the %s %s is not a positive duration", name, value)
	}

	return d.String(), nil
}
