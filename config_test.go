package distconf_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttab/distconf"
)

func writeConfigDir(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

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

	conf, err := distconf.ReadConfigFromDirectory(dir)
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

	conf, err := distconf.ReadConfigFromDirectory(dir)
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

	_, err := distconf.ReadConfigFromDirectory(dir)
	if err == nil {
		t.Fatal("expected an error for a kind declared in two files")
	}

	if !strings.Contains(err.Error(), "declared more than once") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestReadConfigExample parses the example directory shipped with the
// repository. The README points operators at it, so it has to stay
// loadable as the configuration format evolves.
func TestReadConfigExample(t *testing.T) {
	dir := filepath.Join("testdata", "config-example")

	conf, err := distconf.ReadConfigFromDirectory(dir)
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
}

func TestReadConfigDocumentSettings(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"doc.hcl": `
document "core/article" {
  transform_script   = "function transform(doc) { return doc }"
  bounded_collection = true
  variants           = ["web", "print"]
}
`,
	})

	conf, err := distconf.ReadConfigFromDirectory(dir)
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
}
