package live_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttab/distconf/live"
)

const mainHCL = `
configuration {
  service = "live"
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

func TestReadConfigPostTypes(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"posts.hcl": `
post_type "core/live-post" {}
post_type "core/flash" {}
`,
	})

	conf, err := live.ReadConfigFromDirectory(dir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if len(conf.PostTypes) != 2 {
		t.Fatalf("got %d post types, want 2", len(conf.PostTypes))
	}

	if conf.PostTypes[0].Type != "core/live-post" {
		t.Errorf("got type %q, want core/live-post",
			conf.PostTypes[0].Type)
	}
}

func TestReadConfigDuplicatePostType(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"one.hcl": `post_type "core/live-post" {}`,
		"two.hcl": `post_type "core/live-post" {}`,
	})

	_, err := live.ReadConfigFromDirectory(dir)
	if err == nil {
		t.Fatal("expected an error for a type declared in two files")
	}

	if !strings.Contains(err.Error(), "declared more than once") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadConfigWrongService(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"main.hcl": `
configuration {
  service = "distribution"
  version = 1
}
`,
	})

	_, err := live.ReadConfigFromDirectory(dir)
	if err == nil {
		t.Fatal("expected an error for a distribution configuration")
	}

	if !strings.Contains(err.Error(), "not \"live\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestReadConfigExample parses the example directory shipped with the
// repository. The README points operators at it, so it has to stay
// loadable as the configuration format evolves.
func TestReadConfigExample(t *testing.T) {
	dir := filepath.Join("testdata", "config-example")

	conf, err := live.ReadConfigFromDirectory(dir)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if len(conf.SchemaSets) == 0 {
		t.Error("no schema sets in the example configuration")
	}

	if len(conf.PostTypes) == 0 {
		t.Error("no post types in the example configuration")
	}
}
