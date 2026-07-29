// Package live implements distconf configuration handling for the
// Elephant live (liveblogging) service.
package live

import (
	"fmt"

	"github.com/ttab/distconf"
	"github.com/ttab/eleconf"
)

// ServiceName is the service identifier used in the configuration block
// of live configuration directories.
const ServiceName = "live"

// ConfigVersion is the configuration format version this package
// supports.
const ConfigVersion = 1

// Config is the root configuration for the live service.
type Config struct {
	Configuration *distconf.ConfigurationBlock `hcl:"configuration,block"`
	SchemaSets    []eleconf.SchemaSet          `hcl:"schema_set,block"`
	PostTypes     []PostTypeConfig             `hcl:"post_type,block"`

	// Schemas is populated by ReadConfigFromDirectory when WithSchemasDir
	// is used. It contains all schemas loaded from the local directory.
	Schemas []distconf.LoadedSchema
}

// PostTypeConfig declares a document type that the live service accepts
// as post content.
type PostTypeConfig struct {
	Type string `hcl:"type,label"`
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
		tutti.PostTypes = append(tutti.PostTypes, c.PostTypes...)

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

	// HCL only rejects duplicate labels within one file, so types
	// declared in two files have to be caught after the merge.
	seenTypes := make(map[string]bool, len(tutti.PostTypes))

	for _, pt := range tutti.PostTypes {
		if seenTypes[pt.Type] {
			return nil, fmt.Errorf(
				"post_type %q declared more than once", pt.Type)
		}

		seenTypes[pt.Type] = true
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
