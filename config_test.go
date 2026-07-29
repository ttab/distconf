package distconf_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ttab/distconf"
	"github.com/ttab/eleconf"
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

// TestReadDirectoryInfo verifies that the service-independent parts of a
// configuration directory are read while service-specific blocks are
// ignored.
func TestReadDirectoryInfo(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"main.hcl": `
configuration {
  service = "distribution"
  version = 1
}
`,
		"schemas.hcl": `
schema_set "public" {
  version    = "v0.0.4"
  repository = "https://example.com/schemas.git"
  schemas    = ["se.ecms.dist"]
}
`,
		"documents.hcl": `
document "core/article" {
  transform_file = "article.ts"
}
`,
	})

	info, err := distconf.ReadDirectoryInfo(dir)
	if err != nil {
		t.Fatalf("read directory info: %v", err)
	}

	if info.Configuration.Service != "distribution" {
		t.Errorf("got service %q, want distribution",
			info.Configuration.Service)
	}

	if info.Configuration.Version != 1 {
		t.Errorf("got version %d, want 1", info.Configuration.Version)
	}

	if len(info.SchemaSets) != 1 {
		t.Errorf("got %d schema sets, want 1", len(info.SchemaSets))
	}
}

func TestReadDirectoryInfoMissingConfiguration(t *testing.T) {
	dir := writeConfigDir(t, map[string]string{
		"schemas.hcl": "",
	})

	_, err := distconf.ReadDirectoryInfo(dir)
	if err == nil {
		t.Fatal("expected an error for a missing configuration block")
	}

	if !strings.Contains(err.Error(), "no configuration block") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadDirectoryInfoDuplicateConfiguration(t *testing.T) {
	confBlock := `
configuration {
  service = "distribution"
  version = 1
}
`

	dir := writeConfigDir(t, map[string]string{
		"one.hcl": confBlock,
		"two.hcl": confBlock,
	})

	_, err := distconf.ReadDirectoryInfo(dir)
	if err == nil {
		t.Fatal("expected an error for duplicate configuration blocks")
	}

	if !strings.Contains(err.Error(), "only one may be declared") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPlanSchemas(t *testing.T) {
	want := []distconf.LoadedSchema{
		{
			Lock: eleconf.SchemaLock{
				Name:    "se.ecms.dist",
				Version: "v0.0.5",
			},
			Data: []byte(`{}`),
		},
		{
			Lock: eleconf.SchemaLock{
				Name:    "se.ecms.live",
				Version: "v0.0.5",
			},
			Data: []byte(`{}`),
		},
	}

	active := []distconf.SchemaRef{
		{Name: "se.ecms.dist", Version: "v0.0.4", Spec: "{}"},
		{Name: "se.tt.dist", Version: "v0.0.4", Spec: "{}"},
	}

	desired, changes := distconf.PlanSchemas(want, active)

	if len(desired) != 2 {
		t.Fatalf("got %d desired schemas, want 2", len(desired))
	}

	// One upgrade, one add, one remove.
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3", len(changes))
	}

	var ops []string

	for _, change := range changes {
		op, msg := change.Describe()

		ops = append(ops, string(op)+" "+msg)
	}

	joined := strings.Join(ops, "\n")

	for _, wanted := range []string{
		"~ upgrade schema se.ecms.dist v0.0.4 => v0.0.5",
		"+ add schema se.ecms.live@v0.0.5",
		"- remove schema se.tt.dist@v0.0.4",
	} {
		if !strings.Contains(joined, wanted) {
			t.Errorf("missing change %q in:\n%s", wanted, joined)
		}
	}
}
