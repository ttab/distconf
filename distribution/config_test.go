package distribution_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ttab/distconf/distribution"
)

const mainHCL = `
configuration {
  service = "distribution"
  version = 1
}
`

func writeConfigDir(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	if _, ok := files["main.hcl"]; !ok {
		files["main.hcl"] = mainHCL
	}

	for name, content := range files {
		err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
		if err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	return dir
}

const imageRenditionsHCL = `
renditions "image" {
  default_variants  = ["thumbnail", "preview", "hires"]
  default_extension = "jpg"

  source "tt-archive" {
    namespace   = "mm"
    link_types  = ["tt/picture", "tt/graphic"]
    uri_pattern = "^https?://tt\\.se/media/image/sdl([A-Za-z0-9._-]+)$"
  }

  source "repo" {
    namespace   = "repo"
    block_types = ["core/image"]
    link_rel    = "image"
    uri_pattern = "^repo://([A-Za-z0-9._-]+)$"
  }
}
`

func TestReadConfigRenditions(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"renditions.hcl": imageRenditionsHCL,
	})

	conf, err := distribution.ReadConfigFromDirectory(dir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if len(conf.Renditions) != 1 {
		t.Fatalf("got %d renditions blocks, want 1", len(conf.Renditions))
	}

	r := conf.Renditions[0]

	if r.Kind != "image" {
		t.Errorf("got kind %q, want image", r.Kind)
	}

	if r.DefaultExtension != "jpg" {
		t.Errorf("got extension %q, want jpg", r.DefaultExtension)
	}

	if len(r.DefaultVariants) != 3 {
		t.Errorf("got variants %v, want 3", r.DefaultVariants)
	}

	if len(r.Sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(r.Sources))
	}

	// Source order carries meaning (first match wins), so the
	// declaration order must be preserved.
	if r.Sources[0].Name != "tt-archive" || r.Sources[1].Name != "repo" {
		t.Errorf("source order not preserved: %q, %q",
			r.Sources[0].Name, r.Sources[1].Name)
	}

	if r.Sources[1].LinkRel != "image" ||
		len(r.Sources[1].BlockTypes) != 1 {
		t.Errorf("source fields not decoded: %+v", r.Sources[1])
	}
}

func TestReadConfigRenditionsMultiFileMerge(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"image.hcl": imageRenditionsHCL,
		"audio.hcl": `
renditions "audio" {
  default_variants = ["clip"]

  source "repo" {
    namespace   = "repo"
    block_types = ["core/audio"]
    link_rel    = "audio"
    uri_pattern = "^repo://([A-Za-z0-9._-]+)$"
  }
}
`,
	})

	conf, err := distribution.ReadConfigFromDirectory(dir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if len(conf.Renditions) != 2 {
		t.Fatalf("got %d renditions blocks, want 2", len(conf.Renditions))
	}
}

func TestReadConfigRenditionsDuplicateKind(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"one.hcl": imageRenditionsHCL,
		"two.hcl": imageRenditionsHCL,
	})

	_, err := distribution.ReadConfigFromDirectory(dir)
	if err == nil {
		t.Fatal("expected an error for a kind declared in two files")
	}

	if !strings.Contains(err.Error(), "declared more than once") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadConfigMissingConfigurationBlock(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"main.hcl":       "",
		"renditions.hcl": imageRenditionsHCL,
	})

	_, err := distribution.ReadConfigFromDirectory(dir)
	if err == nil {
		t.Fatal("expected an error for a missing configuration block")
	}

	if !strings.Contains(err.Error(), "no configuration block") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadConfigWrongService(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"main.hcl": `
configuration {
  service = "live"
  version = 1
}
`,
	})

	_, err := distribution.ReadConfigFromDirectory(dir)
	if err == nil {
		t.Fatal("expected an error for a live configuration")
	}

	if !strings.Contains(err.Error(), "not \"distribution\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadConfigUnsupportedVersion(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"main.hcl": `
configuration {
  service = "distribution"
  version = 2
}
`,
	})

	_, err := distribution.ReadConfigFromDirectory(dir)
	if err == nil {
		t.Fatal("expected an error for an unsupported version")
	}

	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestReadConfigExample parses the example directory shipped with the
// repository. The README points operators at it, so it has to stay
// loadable as the configuration format evolves.
func TestReadConfigExample(t *testing.T) {
	dir := filepath.Join("testdata", "config-example")

	conf, err := distribution.ReadConfigFromDirectory(dir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if len(conf.SchemaSets) == 0 {
		t.Error("no schema sets in the example configuration")
	}

	if len(conf.Renditions) == 0 {
		t.Error("no renditions blocks in the example configuration")
	}

	if len(conf.HTMLRendering) != 1 {
		t.Errorf("got %d html_rendering blocks in the example, want 1",
			len(conf.HTMLRendering))
	}

	if len(conf.Renderers) == 0 {
		t.Error("no renderers in the example configuration")
	}

	// script_file references have to be resolved relative to the
	// configuration directory too, and a renderer script is the one thing
	// in the example that only that resolution can produce.
	for _, r := range conf.Renderers {
		if r.Kind == distribution.RendererKindJS && r.Script == "" {
			t.Errorf("renderer %q resolved no script", r.Name)
		}
	}

	var withScript int

	for _, doc := range conf.Documents {
		if doc.TransformScript != "" {
			withScript++
		}
	}

	// transform_file references have to be resolved relative to the
	// configuration directory.
	if withScript == 0 {
		t.Error("no document in the example resolved a transform script")
	}

	var embedded int

	for _, doc := range conf.Documents {
		if doc.Embeddings {
			embedded++
		}
	}

	if embedded == 0 {
		t.Error("no document in the example enabled embeddings")
	}

	var forwardAnchored, backwardAnchored, bounded int

	for _, doc := range conf.Documents {
		switch doc.Anchor {
		case distribution.AnchorTimeExpressions:
			if len(doc.TimeExpressions) > 0 {
				forwardAnchored++
			}
		case distribution.AnchorFirstPublished:
			backwardAnchored++
		}

		if doc.BoundedCollection {
			bounded++
		}
	}

	if forwardAnchored == 0 {
		t.Error("no document in the example is forward-anchored")
	}

	if backwardAnchored == 0 {
		t.Error("no document in the example is backward-anchored")
	}

	if bounded == 0 {
		t.Error("no document in the example is a bounded collection")
	}

	// The example is what operators crib from, so the delivery fields it
	// declares are checked by name and kind. "place" is on planning items
	// only, because only a planning item carries the link - it is the
	// partially-declared field an editor has to warn about, and it is one
	// because of what the types are, not because the example withheld a
	// declaration.
	wantDelivery := map[string]map[string]string{
		"core/article": {
			"section":   distribution.KindKeyword,
			"newsvalue": distribution.KindNumber,
			"headline":  distribution.KindText,
		},
		"core/planning-item": {
			"section":   distribution.KindKeyword,
			"newsvalue": distribution.KindNumber,
			"headline":  distribution.KindText,
			"place":     distribution.KindKeyword,
		},
	}

	for _, doc := range conf.Documents {
		want, ok := wantDelivery[doc.Type]
		if !ok {
			continue
		}

		delete(wantDelivery, doc.Type)

		if len(doc.DeliveryFields) != len(want) {
			t.Errorf("document %q has %d delivery fields, want %d",
				doc.Type, len(doc.DeliveryFields), len(want))
		}

		for _, f := range doc.DeliveryFields {
			kind, ok := want[f.Name]
			if !ok {
				t.Errorf("document %q declares the unexpected delivery field %q",
					doc.Type, f.Name)

				continue
			}

			if f.Kind != kind {
				t.Errorf("document %q declares %q as %q, want %q",
					doc.Type, f.Name, f.Kind, kind)
			}

			if f.Expression == "" {
				t.Errorf("document %q declares %q with no expression",
					doc.Type, f.Name)
			}

			if f.Description == "" {
				t.Errorf("document %q declares %q with no description",
					doc.Type, f.Name)
			}
		}
	}

	for docType := range wantDelivery {
		t.Errorf("the example has no %q document", docType)
	}
}

// TestReadConfigAnchors covers the anchor settings and the two ways the
// anchor and the time expressions can disagree. Neither of them fails at
// apply time, which is why they are refused here.
func TestReadConfigAnchors(t *testing.T) {
	t.Run("a forward anchor decodes its expressions", func(t *testing.T) {
		dir := writeConfigDir(t, map[string]string{
			"doc.hcl": `
document "core/planning-item" {
  anchor = "time_expressions"

  time_expression {
    expression = ".meta(type='core/event').data{start}"
    layout     = "2006-01-02 15:04"
    timezone   = "Europe/Stockholm"
  }
}
`,
		})

		conf, err := distribution.ReadConfigFromDirectory(dir)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}

		doc := conf.Documents[0]

		if doc.Anchor != distribution.AnchorTimeExpressions {
			t.Errorf("anchor not decoded: %q", doc.Anchor)
		}

		if len(doc.TimeExpressions) != 1 {
			t.Fatalf("got %d time expressions, want 1",
				len(doc.TimeExpressions))
		}

		if doc.TimeExpressions[0].Layout != "2006-01-02 15:04" {
			t.Errorf("layout not decoded: %q",
				doc.TimeExpressions[0].Layout)
		}

		if doc.TimeExpressions[0].Timezone != "Europe/Stockholm" {
			t.Errorf("timezone not decoded: %q",
				doc.TimeExpressions[0].Timezone)
		}
	})

	cases := map[string]struct {
		config  string
		errPart string
	}{
		"forward anchor without expressions": {
			config: `
document "core/planning-item" {
  anchor = "time_expressions"
}
`,
			errPart: "at least one time_expression block",
		},
		"expressions without the anchor": {
			config: `
document "core/planning-item" {
  time_expression {
    expression = ".meta(type='core/planning-item').data{start_date:date}"
  }
}
`,
			errPart: "nothing reads them",
		},
		"expressions under a backward anchor": {
			config: `
document "core/article" {
  anchor = "first_published"

  time_expression {
    expression = ".meta(type='core/planning-item').data{start_date:date}"
  }
}
`,
			errPart: "nothing reads them",
		},
		"unknown anchor": {
			config: `
document "core/article" {
  anchor = "whenever"
}
`,
			errPart: "unknown anchor",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeConfigDir(t, map[string]string{
				"doc.hcl": tc.config,
			})

			_, err := distribution.ReadConfigFromDirectory(dir)
			if err == nil {
				t.Fatal("expected an error")
			}

			if !strings.Contains(err.Error(), tc.errPart) {
				t.Errorf("expected an error mentioning %q, got %v",
					tc.errPart, err)
			}
		})
	}
}

func TestReadConfigDocumentSettings(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"doc.hcl": `
document "core/article" {
  transform_script   = "function transform(doc) { return doc }"
  bounded_collection = true
  variants           = ["web", "print"]
  embeddings         = true

  delivery_field "section" {
    kind        = "keyword"
    expression  = ".links(rel='section')@{uuid}"
    description = "The section the content was published in."
  }
}
`,
	})

	conf, err := distribution.ReadConfigFromDirectory(dir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if len(conf.Documents) != 1 {
		t.Fatalf("got %d documents, want 1", len(conf.Documents))
	}

	doc := conf.Documents[0]

	if !doc.BoundedCollection {
		t.Error("bounded_collection not decoded")
	}

	if len(doc.Variants) != 2 {
		t.Errorf("variants not decoded: %v", doc.Variants)
	}

	if !doc.Embeddings {
		t.Error("embeddings not decoded")
	}

	if len(doc.DeliveryFields) != 1 {
		t.Fatalf("got %d delivery fields, want 1", len(doc.DeliveryFields))
	}

	df := doc.DeliveryFields[0]

	if df.Name != "section" {
		t.Errorf("delivery field name not decoded: %q", df.Name)
	}

	if df.Kind != distribution.KindKeyword {
		t.Errorf("delivery field kind not decoded: %q", df.Kind)
	}

	if df.Expression != ".links(rel='section')@{uuid}" {
		t.Errorf("delivery field expression not decoded: %q", df.Expression)
	}

	// The description is what an editor shows beside the field name, so a
	// silently dropped one is a field nobody can tell apart from the next.
	if df.Description != "The section the content was published in." {
		t.Errorf("delivery field description not decoded: %q",
			df.Description)
	}
}

// TestReadConfigDeliveryFieldKinds covers the delivery field declarations
// that decode cleanly and mean nothing. Neither of them fails at apply
// time in a way an operator sees, which is why they are refused here.
func TestReadConfigDeliveryFieldKinds(t *testing.T) {
	cases := map[string]struct {
		config  string
		errPart string
	}{
		// kind is a required attribute rather than an optional one, so
		// omitting it entirely is HCL's error rather than ours. Both
		// are covered: the kind has no usable default, and a
		// declaration that forgot it must not be read as a keyword.
		"no kind at all": {
			config: `
document "core/article" {
  delivery_field "section" {
    expression = ".links(rel='section')@{uuid}"
  }
}
`,
			errPart: `The argument "kind" is required`,
		},
		"an empty kind": {
			config: `
document "core/article" {
  delivery_field "section" {
    kind       = ""
    expression = ".links(rel='section')@{uuid}"
  }
}
`,
			errPart: "unknown kind",
		},
		"a misspelled kind": {
			config: `
document "core/article" {
  delivery_field "section" {
    kind       = "keywrod"
    expression = ".links(rel='section')@{uuid}"
  }
}
`,
			errPart: "unknown kind",
		},
		"no expression": {
			config: `
document "core/article" {
  delivery_field "section" {
    kind       = "keyword"
    expression = ""
  }
}
`,
			errPart: "has no expression",
		},
		"one name, two kinds": {
			config: `
document "core/article" {
  delivery_field "section" {
    kind       = "keyword"
    expression = ".links(rel='section')@{uuid}"
  }

  delivery_field "section" {
    kind       = "text"
    expression = ".links(rel='section')@{title}"
  }
}
`,
			errPart: "declared as both",
		},
		"one name, two descriptions": {
			config: `
document "core/article" {
  delivery_field "section" {
    kind        = "keyword"
    expression  = ".links(rel='section')@{uuid}"
    description = "The section."
  }

  delivery_field "section" {
    kind        = "keyword"
    expression  = ".links(rel='subsection')@{uuid}"
    description = "The subsection."
  }
}
`,
			errPart: "two different descriptions",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeConfigDir(t, map[string]string{
				"doc.hcl": tc.config,
			})

			_, err := distribution.ReadConfigFromDirectory(dir)
			if err == nil {
				t.Fatal("expected an error")
			}

			if !strings.Contains(err.Error(), tc.errPart) {
				t.Errorf("expected an error mentioning %q, got %v",
					tc.errPart, err)
			}
		})
	}
}

const factboxRendererJS = `
export function render(req) {
  return { blocks: [] }
}
`

const rendererHCL = `
html_rendering {
  image_variant  = "hires"
  document_types = ["core/article"]
}

renderer "factbox-special" {
  kind        = "js"
  revision    = 3
  script_file = "factbox.js"

  trigger {
    block_types = ["core/factbox"]
    roles       = ["sidebar"]
  }

  document_types = ["core/article"]

  policy {
    elements    = ["p", "em", "aside"]
    attributes  = { p = ["class"] }
    url_schemes = ["https"]
  }
}

renderer "chart" {
  kind          = "remote"
  url           = "https://renderers.example.com/chart"
  policy_preset = "rich-text"

  circuit_breaker {
    timeout           = "2s"
    failure_threshold = 3
    open_duration     = "1m"
    max_in_flight     = 8
  }
}
`

// TestReadConfigRenderers covers both new blocks: that they decode whole,
// and that the renderers keep their declaration order - where two of them
// answer for one block the first one wins, so the order in the
// configuration decides what the output is.
func TestReadConfigRenderers(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"renderers.hcl": rendererHCL,
		"factbox.js":    factboxRendererJS,
	})

	conf, err := distribution.ReadConfigFromDirectory(dir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if len(conf.HTMLRendering) != 1 {
		t.Fatalf("got %d html_rendering blocks, want 1",
			len(conf.HTMLRendering))
	}

	html := conf.HTMLRendering[0]

	if html.ImageVariant != "hires" {
		t.Errorf("image variant not decoded: %q", html.ImageVariant)
	}

	if !slices.Equal(html.DocumentTypes, []string{"core/article"}) {
		t.Errorf("document types not decoded: %v", html.DocumentTypes)
	}

	if len(conf.Renderers) != 2 {
		t.Fatalf("got %d renderers, want 2", len(conf.Renderers))
	}

	if conf.Renderers[0].Name != "factbox-special" ||
		conf.Renderers[1].Name != "chart" {
		t.Fatalf("renderer order not preserved: %q, %q",
			conf.Renderers[0].Name, conf.Renderers[1].Name)
	}

	js := conf.Renderers[0]

	if js.Kind != distribution.RendererKindJS {
		t.Errorf("kind not decoded: %q", js.Kind)
	}

	if js.Revision != 3 {
		t.Errorf("got revision %d, want 3", js.Revision)
	}

	// The script is a file reference in the configuration and script
	// content in the generation: a generation carries what it will run.
	if js.Script != factboxRendererJS {
		t.Errorf("script file not resolved: %q", js.Script)
	}

	if len(js.Triggers) != 1 {
		t.Fatalf("got %d triggers, want 1", len(js.Triggers))
	}

	if !slices.Equal(js.Triggers[0].BlockTypes, []string{"core/factbox"}) ||
		!slices.Equal(js.Triggers[0].Roles, []string{"sidebar"}) {
		t.Errorf("trigger not decoded: %+v", js.Triggers[0])
	}

	if js.Policy == nil {
		t.Fatal("policy not decoded")
	}

	if !slices.Equal(js.Policy.Elements, []string{"p", "em", "aside"}) {
		t.Errorf("policy elements not decoded: %v", js.Policy.Elements)
	}

	if !slices.Equal(js.Policy.Attributes["p"], []string{"class"}) {
		t.Errorf("policy attributes not decoded: %v",
			js.Policy.Attributes)
	}

	if !slices.Equal(js.Policy.URLSchemes, []string{"https"}) {
		t.Errorf("policy url schemes not decoded: %v",
			js.Policy.URLSchemes)
	}

	remote := conf.Renderers[1]

	if remote.URL != "https://renderers.example.com/chart" {
		t.Errorf("url not decoded: %q", remote.URL)
	}

	if remote.PolicyPreset != distribution.PolicyPresetRichText {
		t.Errorf("policy preset not decoded: %q", remote.PolicyPreset)
	}

	breaker := remote.CircuitBreaker

	if breaker == nil {
		t.Fatal("circuit breaker not decoded")
	}

	if breaker.Timeout != "2s" || breaker.FailureThreshold != 3 ||
		breaker.OpenDuration != "1m0s" || breaker.MaxInFlight != 8 {
		t.Errorf("circuit breaker not decoded: %+v", breaker)
	}
}

// TestReadConfigRendererDefaults covers the defaults we resolve rather than
// leave to the service. The service stores the compiled configuration, so a
// default it fills in and we don't is a diff every apply reports and no
// apply settles.
func TestReadConfigRendererDefaults(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"renderers.hcl": `
html_rendering {}

renderer "chart" {
  kind          = "remote"
  url           = "https://renderers.example.com/chart"
  policy_preset = "strict"
}

renderer "factbox" {
  kind          = "js"
  script_file   = "factbox.js"
  policy_preset = "strict"
}
`,
		"factbox.js": factboxRendererJS,
	})

	conf, err := distribution.ReadConfigFromDirectory(dir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if conf.HTMLRendering[0].ImageVariant != distribution.DefaultImageVariant {
		t.Errorf("image variant default not filled in: %q",
			conf.HTMLRendering[0].ImageVariant)
	}

	remote := conf.Renderers[0]

	if remote.Revision != 1 {
		t.Errorf("got revision %d, want the default 1", remote.Revision)
	}

	breaker := remote.CircuitBreaker

	if breaker == nil {
		t.Fatal("no circuit breaker defaults filled in")
	}

	if breaker.Timeout != distribution.DefaultRendererTimeout ||
		breaker.OpenDuration != distribution.DefaultRendererOpenDuration ||
		breaker.FailureThreshold !=
			distribution.DefaultRendererFailureThreshold ||
		breaker.MaxInFlight != distribution.DefaultRendererMaxInFlight {
		t.Errorf("circuit breaker defaults not filled in: %+v", breaker)
	}

	// A script renderer reads the timeout and nothing else, so the
	// settings that describe a remote endpoint are left unset rather than
	// sent as values the service would read for nothing.
	script := conf.Renderers[1].CircuitBreaker

	if script == nil {
		t.Fatal("no circuit breaker timeout filled in")
	}

	if script.Timeout != distribution.DefaultRendererTimeout {
		t.Errorf("timeout default not filled in: %q", script.Timeout)
	}

	if script.FailureThreshold != 0 || script.OpenDuration != "" ||
		script.MaxInFlight != 0 {
		t.Errorf("remote-only breaker settings filled in for a script renderer: %+v",
			script)
	}
}

// TestReadConfigRendererDuplicateName covers a name declared in two files.
// HCL only rejects duplicate labels within one file, and the name is what a
// remote renderer's secret is looked up under, so two of them is two
// renderers sharing one secret and one set of metrics.
func TestReadConfigRendererDuplicateName(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"one.hcl": rendererHCL,
		"two.hcl": `
renderer "chart" {
  kind          = "remote"
  url           = "https://elsewhere.example.com/chart"
  policy_preset = "strict"
}
`,
		"factbox.js": factboxRendererJS,
	})

	_, err := distribution.ReadConfigFromDirectory(dir)
	if err == nil {
		t.Fatal("expected an error for a renderer declared in two files")
	}

	if !strings.Contains(err.Error(), "declared more than once") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadConfigHTMLRenderingDuplicateBlock(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"one.hcl": "html_rendering {\n  image_variant = \"preview\"\n}\n",
		"two.hcl": "html_rendering {\n  image_variant = \"hires\"\n}\n",
	})

	_, err := distribution.ReadConfigFromDirectory(dir)
	if err == nil {
		t.Fatal("expected an error for two html_rendering blocks")
	}

	if !strings.Contains(err.Error(), "only one block") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestReadConfigRendererRefusals covers the renderer declarations that
// decode cleanly and cannot work, or work as something other than what they
// say. Nothing here fails in a way an operator sees at apply time.
func TestReadConfigRendererRefusals(t *testing.T) {
	cases := map[string]struct {
		config  string
		errPart string
	}{
		"a script and an endpoint": {
			config: `
renderer "factbox" {
  kind          = "js"
  script_file   = "factbox.js"
  url           = "https://renderers.example.com/factbox"
  policy_preset = "strict"
}
`,
			errPart: "mutually exclusive",
		},
		"a script renderer with no script": {
			config: `
renderer "factbox" {
  kind          = "js"
  policy_preset = "strict"
}
`,
			errPart: "needs a script_file",
		},
		"a remote renderer with no url": {
			config: `
renderer "chart" {
  kind          = "remote"
  policy_preset = "strict"
}
`,
			errPart: "needs a url",
		},
		"an unknown kind": {
			config: `
renderer "chart" {
  kind          = "grpc"
  url           = "https://renderers.example.com/chart"
  policy_preset = "strict"
}
`,
			errPart: "unknown kind",
		},
		"a name the secret lookup cannot spell": {
			config: `
renderer "Factbox Special" {
  kind          = "js"
  script_file   = "factbox.js"
  policy_preset = "strict"
}
`,
			errPart: "REMOTE_SECRET_<NAME>",
		},
		"an insecure url that does not say so": {
			config: `
renderer "chart" {
  kind          = "remote"
  url           = "http://renderers.example.com/chart"
  policy_preset = "strict"
}
`,
			errPart: "allow_insecure",
		},
		"an unknown url scheme": {
			config: `
renderer "chart" {
  kind          = "remote"
  url           = "ftp://renderers.example.com/chart"
  policy_preset = "strict"
}
`,
			errPart: "expected https or http",
		},
		"a policy and a preset": {
			config: `
renderer "factbox" {
  kind          = "js"
  script_file   = "factbox.js"
  policy_preset = "strict"

  policy {
    elements = ["p"]
  }
}
`,
			errPart: "policy and policy_preset are mutually exclusive",
		},
		"neither a policy nor a preset": {
			config: `
renderer "factbox" {
  kind        = "js"
  script_file = "factbox.js"
}
`,
			errPart: "declare a policy block or a policy_preset",
		},
		"an unknown preset": {
			config: `
renderer "factbox" {
  kind          = "js"
  script_file   = "factbox.js"
  policy_preset = "permissive"
}
`,
			errPart: "unknown policy_preset",
		},
		"a policy that allows nothing": {
			config: `
renderer "factbox" {
  kind        = "js"
  script_file = "factbox.js"

  policy {
    attributes = { p = ["class"] }
  }
}
`,
			errPart: "allows no elements",
		},
		"an empty trigger": {
			config: `
renderer "factbox" {
  kind          = "js"
  script_file   = "factbox.js"
  policy_preset = "strict"

  trigger {}
}
`,
			errPart: "selects nothing",
		},
		"a breaker setting a script renderer does not read": {
			config: `
renderer "factbox" {
  kind          = "js"
  script_file   = "factbox.js"
  policy_preset = "strict"

  circuit_breaker {
    timeout           = "2s"
    failure_threshold = 3
  }
}
`,
			errPart: "describe a remote endpoint",
		},
		"an endpoint setting a script renderer does not read": {
			config: `
renderer "factbox" {
  kind           = "js"
  script_file    = "factbox.js"
  policy_preset  = "strict"
  allow_insecure = true
}
`,
			errPart: "this renderer is a script",
		},
		"a timeout that is not a duration": {
			config: `
renderer "chart" {
  kind          = "remote"
  url           = "https://renderers.example.com/chart"
  policy_preset = "strict"

  circuit_breaker {
    timeout = "one second"
  }
}
`,
			errPart: "parse timeout",
		},
		// full_document was how an earlier draft asked for the whole
		// document beside the blocks a renderer claimed. Every invoked
		// renderer gets the whole document now, so the flag is gone -
		// and it has to be refused rather than ignored, since a
		// configuration that still sets it was written against
		// semantics the service no longer has.
		"the full_document flag of the block-claiming draft": {
			config: `
renderer "chart" {
  kind          = "remote"
  url           = "https://renderers.example.com/chart"
  policy_preset = "strict"
  full_document = true
}
`,
			errPart: "full_document",
		},
		"a negative revision": {
			config: `
renderer "chart" {
  kind          = "remote"
  revision      = -1
  url           = "https://renderers.example.com/chart"
  policy_preset = "strict"
}
`,
			errPart: "is negative",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeConfigDir(t, map[string]string{
				"renderer.hcl": tc.config,
				"factbox.js":   factboxRendererJS,
			})

			_, err := distribution.ReadConfigFromDirectory(dir)
			if err == nil {
				t.Fatal("expected an error")
			}

			if !strings.Contains(err.Error(), tc.errPart) {
				t.Errorf("expected an error mentioning %q, got %v",
					tc.errPart, err)
			}
		})
	}
}

// TestReadConfigRendererInsecureEndpoint covers the endpoint that says so:
// renderer URLs are operator-configured, and an in-cluster renderer on a
// plain address is a legitimate deployment.
func TestReadConfigRendererInsecureEndpoint(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"renderer.hcl": `
renderer "chart" {
  kind           = "remote"
  url            = "http://chart-renderer.internal/render"
  allow_insecure = true
  policy_preset  = "strict"
}
`,
	})

	conf, err := distribution.ReadConfigFromDirectory(dir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if !conf.Renderers[0].AllowInsecure {
		t.Error("allow_insecure not decoded")
	}
}
