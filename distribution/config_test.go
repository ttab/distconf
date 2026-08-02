package distribution_test

import (
	"os"
	"path/filepath"
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
}
