// Package distribution implements distconf configuration handling for the
// Elephant distribution service.
package distribution

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ttab/distconf"
	"github.com/ttab/eleconf"
)

// ServiceName is the service identifier used in the configuration block
// of distribution configuration directories.
const ServiceName = "distribution"

// ConfigVersion is the configuration format version this package
// supports.
const ConfigVersion = 1

// Config is the root configuration for the distribution service.
type Config struct {
	Configuration *distconf.ConfigurationBlock `hcl:"configuration,block"`
	SchemaSets    []eleconf.SchemaSet          `hcl:"schema_set,block"`
	Documents     []DocumentConfig             `hcl:"document,block"`
	Renditions    []RenditionsConfig           `hcl:"renditions,block"`

	// Schemas is populated by ReadConfigFromDirectory when WithSchemasDir
	// is used. It contains all schemas loaded from the local directory.
	Schemas []distconf.LoadedSchema
}

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

	var (
		tutti  Config
		blocks []distconf.ConfigurationBlock
	)

	err := distconf.ParseDirectory(path, func(_ string, c *Config) error {
		if c.Configuration != nil {
			blocks = append(blocks, *c.Configuration)
		}

		tutti.SchemaSets = append(tutti.SchemaSets, c.SchemaSets...)
		tutti.Documents = append(tutti.Documents, c.Documents...)
		tutti.Renditions = append(tutti.Renditions, c.Renditions...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	confBlock, err := distconf.ResolveConfiguration(blocks)
	if err != nil {
		return nil, err
	}

	err = confBlock.Expect(ServiceName, ConfigVersion)
	if err != nil {
		return nil, err
	}

	tutti.Configuration = confBlock

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
		schemas, err := distconf.LoadSchemaSetsFromDir(
			o.schemasDir, tutti.SchemaSets)
		if err != nil {
			return nil, err
		}

		tutti.Schemas = schemas
	}

	return &tutti, nil
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
