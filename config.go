package distconf

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/ttab/eleconf"
)

// ConfigurationBlock identifies the system that a configuration directory
// targets, and the version of the configuration format used. Exactly one
// configuration block must be declared across the .hcl files in a
// directory.
type ConfigurationBlock struct {
	Service string `hcl:"service"`
	Version int64  `hcl:"version"`
}

// Expect verifies that the configuration block targets the given service
// and configuration format version.
func (c ConfigurationBlock) Expect(service string, version int64) error {
	if c.Service != service {
		return fmt.Errorf(
			"the configuration targets the service %q, not %q",
			c.Service, service)
	}

	if c.Version != version {
		return fmt.Errorf(
			"unsupported %s configuration version %d, supported version: %d",
			service, c.Version, version)
	}

	return nil
}

// ResolveConfiguration verifies that exactly one configuration block was
// declared in a configuration directory and returns it.
func ResolveConfiguration(blocks []ConfigurationBlock) (*ConfigurationBlock, error) {
	switch len(blocks) {
	case 0:
		return nil, fmt.Errorf(
			"no configuration block found; declare one like this:\n\n%s",
			exampleConfigurationBlock)
	case 1:
		return &blocks[0], nil
	default:
		return nil, fmt.Errorf(
			"found %d configuration blocks, only one may be declared",
			len(blocks))
	}
}

const exampleConfigurationBlock = `configuration {
  service = "distribution"
  version = 1
}`

// DirectoryInfo is the service-independent part of a configuration
// directory: the configuration block that identifies the target system,
// and the schema sets.
type DirectoryInfo struct {
	Configuration ConfigurationBlock
	SchemaSets    []eleconf.SchemaSet
}

// partialFile decodes the service-independent blocks of a configuration
// file, ignoring everything else.
type partialFile struct {
	Configuration *ConfigurationBlock `hcl:"configuration,block"`
	SchemaSets    []eleconf.SchemaSet `hcl:"schema_set,block"`
	Remain        hcl.Body            `hcl:",remain"`
}

// ReadDirectoryInfo reads the service-independent parts of a configuration
// directory: the configuration block and the schema sets. Unknown blocks
// are ignored; use the service-specific config loaders for a strict parse.
func ReadDirectoryInfo(path string) (*DirectoryInfo, error) {
	var (
		info   DirectoryInfo
		blocks []ConfigurationBlock
	)

	err := ParseDirectory(path, func(_ string, file *partialFile) error {
		if file.Configuration != nil {
			blocks = append(blocks, *file.Configuration)
		}

		info.SchemaSets = append(info.SchemaSets, file.SchemaSets...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	conf, err := ResolveConfiguration(blocks)
	if err != nil {
		return nil, err
	}

	info.Configuration = *conf

	return &info, nil
}

// ParseDirectory decodes every .hcl file in the given directory into a
// fresh T and passes it to merge together with the file name. Files are
// visited in lexical order.
func ParseDirectory[T any](
	path string, merge func(fileName string, file *T) error,
) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("list directory contents: %w", err)
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".hcl") {
			continue
		}

		var file T

		err := hclsimple.DecodeFile(
			filepath.Join(path, entry.Name()), nil, &file)
		if err != nil {
			return fmt.Errorf("parse %q: %w", entry.Name(), err)
		}

		err = merge(entry.Name(), &file)
		if err != nil {
			return fmt.Errorf("process %q: %w", entry.Name(), err)
		}
	}

	return nil
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

// LoadSchemaSetsFromDir loads the schemas of all the given schema sets
// from a local directory using LoadSchemasFromDir.
func LoadSchemaSetsFromDir(
	dir string, sets []eleconf.SchemaSet,
) ([]LoadedSchema, error) {
	var schemas []LoadedSchema

	for _, set := range sets {
		loaded, err := LoadSchemasFromDir(dir, set)
		if err != nil {
			return nil, fmt.Errorf(
				"load schema set %q from directory: %w",
				set.Name, err)
		}

		schemas = append(schemas, loaded...)
	}

	return schemas, nil
}
