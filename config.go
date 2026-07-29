package distconf

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/ttab/eleconf"
)

// Config is the root configuration for distconf.
type Config struct {
	SchemaSets []eleconf.SchemaSet `hcl:"schema_set,block"`
	Documents  []DocumentConfig    `hcl:"document,block"`
	Renditions []RenditionsConfig  `hcl:"renditions,block"`

	// Schemas is populated by ReadConfigFromDirectory when WithSchemasDir
	// is used. It contains all schemas loaded from the local directory.
	Schemas []LoadedSchema
}

// LoadedSchema is a re-export of eleconf.LoadedSchema for convenience.
type LoadedSchema = eleconf.LoadedSchema

// DocumentConfig defines the distribution configuration for a document type.
type DocumentConfig struct {
	Type              string   `hcl:"type,label"`
	TransformScript   string   `hcl:"transform_script,optional"`
	TransformFile     string   `hcl:"transform_file,optional"`
	BoundedCollection bool     `hcl:"bounded_collection,optional"`
	Variants          []string `hcl:"variants,optional"`
}

// RenditionsConfig defines delivery-time rendition generation for one
// asset kind.
type RenditionsConfig struct {
	Kind             string                  `hcl:"kind,label"`
	DefaultVariants  []string                `hcl:"default_variants,optional"`
	DefaultExtension string                  `hcl:"default_extension,optional"`
	Sources          []RenditionSourceConfig `hcl:"source,block"`
}

// RenditionSourceConfig matches asset references in documents and maps
// them to an asset CDN namespace. Sources are evaluated in order; the
// first match wins.
type RenditionSourceConfig struct {
	Name       string   `hcl:"name,label"`
	Namespace  string   `hcl:"namespace"`
	BlockTypes []string `hcl:"block_types,optional"`
	LinkRel    string   `hcl:"link_rel,optional"`
	LinkTypes  []string `hcl:"link_types,optional"`
	URIPattern string   `hcl:"uri_pattern"`
}

// ReadOption configures ReadConfigFromDirectory.
type ReadOption func(*readOptions)

type readOptions struct {
	schemasDir string
}

// WithSchemasDir loads all schema sets from a local directory instead of
// using the configured repository or URL template. Each schema is read as
// {name}.json from the given directory. This is useful for testing without
// network access.
func WithSchemasDir(dir string) ReadOption {
	return func(o *readOptions) {
		o.schemasDir = dir
	}
}

// ReadConfigFromDirectory reads and merges all .hcl files in the given
// directory. When WithSchemasDir is provided the schemas referenced by
// each schema_set block are loaded from that local directory and stored
// in Config.Schemas.
func ReadConfigFromDirectory(path string, opts ...ReadOption) (*Config, error) {
	var o readOptions

	for _, opt := range opts {
		opt(&o)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("list directory contents: %w", err)
	}

	var tutti Config

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".hcl") {
			continue
		}

		c, err := parseFile(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", entry.Name(), err)
		}

		tutti.SchemaSets = append(tutti.SchemaSets, c.SchemaSets...)
		tutti.Documents = append(tutti.Documents, c.Documents...)
		tutti.Renditions = append(tutti.Renditions, c.Renditions...)
	}

	err = resolveScriptFiles(&tutti, path)
	if err != nil {
		return nil, err
	}

	// HCL only rejects duplicate labels within one file, so kinds
	// declared in two files have to be caught after the merge.
	seenKinds := make(map[string]bool, len(tutti.Renditions))

	for _, r := range tutti.Renditions {
		if seenKinds[r.Kind] {
			return nil, fmt.Errorf(
				"renditions %q declared more than once", r.Kind)
		}

		seenKinds[r.Kind] = true
	}

	if o.schemasDir != "" {
		for _, set := range tutti.SchemaSets {
			schemas, err := LoadSchemasFromDir(o.schemasDir, set)
			if err != nil {
				return nil, fmt.Errorf(
					"load schema set %q from directory: %w",
					set.Name, err)
			}

			tutti.Schemas = append(tutti.Schemas, schemas...)
		}
	}

	return &tutti, nil
}

// LoadSchemasFromDir loads the schemas listed in a SchemaSet from a local
// directory. Each schema is read from {dir}/{name}.json. The version from
// the schema set is recorded in the lock metadata.
func LoadSchemasFromDir(
	dir string, set eleconf.SchemaSet,
) ([]LoadedSchema, error) {
	var schemas []LoadedSchema

	for _, name := range set.Schemas {
		path := filepath.Join(dir, name+".json")

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read schema %q: %w", name, err)
		}

		hash := sha256.Sum256(data)

		schemas = append(schemas, LoadedSchema{
			Lock: eleconf.SchemaLock{
				Name:    name,
				Version: set.Version,
				Hash:    fmt.Sprintf("%x", hash),
			},
			Data: data,
		})
	}

	return schemas, nil
}

func parseFile(path string) (*Config, error) {
	var c Config

	err := hclsimple.DecodeFile(path, nil, &c)
	if err != nil {
		return nil, fmt.Errorf("decode file: %w", err)
	}

	return &c, nil
}

func resolveScriptFiles(conf *Config, dir string) error {
	for i := range conf.Documents {
		doc := &conf.Documents[i]

		if doc.TransformFile == "" {
			continue
		}

		if doc.TransformScript != "" {
			return fmt.Errorf(
				"document %q: transform_script and transform_file are mutually exclusive",
				doc.Type,
			)
		}

		path := doc.TransformFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf(
				"read transform script file for %q: %w",
				doc.Type, err,
			)
		}

		doc.TransformScript = string(data)
	}

	return nil
}
