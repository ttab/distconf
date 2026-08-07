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

// Anchor modes. The anchor decides how the type is partitioned across the
// search indexes, and nothing else - it says nothing about whether the type
// is embedded, which is the separate `embeddings` setting.
const (
	// AnchorNone is the default: a non-temporal type. One unpartitioned
	// index per language that is never archived. Entity registries and
	// other small, slowly changing collections.
	AnchorNone = ""
	// AnchorFirstPublished partitions by the time of the document's
	// first distribution event, which is immutable, so a document never
	// moves between partitions. The mode for news content.
	AnchorFirstPublished = "first_published"
	// AnchorTimeExpressions partitions by the dates the type's
	// time_expression blocks extract from the document, with everything
	// from the current quarter onwards sharing one index. The mode for
	// content that is about a date rather than published on one.
	AnchorTimeExpressions = "time_expressions"
)

// DocumentConfig defines the distribution configuration for a document type.
type DocumentConfig struct {
	Type              string   `hcl:"type,label"`
	TransformScript   string   `hcl:"transform_script,optional"`
	TransformFile     string   `hcl:"transform_file,optional"`
	BoundedCollection bool     `hcl:"bounded_collection,optional"`
	Variants          []string `hcl:"variants,optional"`
	// Anchor is how the type is partitioned across the search indexes:
	// one of AnchorNone, AnchorFirstPublished or AnchorTimeExpressions.
	// It is fixed for the indexes that already exist, so changing it for
	// a type that is already indexed takes a new index generation.
	Anchor string `hcl:"anchor,optional"`
	// TimeExpressions extract the dates a forward-anchored type is
	// partitioned by. Required by, and only meaningful for, the
	// time_expressions anchor.
	TimeExpressions []TimeExpressionConfig `hcl:"time_expression,block"`

	// Embeddings turns on semantic indexing for the type: its documents
	// are chunked and embedded, and it can be searched and subscribed to
	// by vector. It needs a deployment with an embedding sidecar, and it
	// only takes effect for indexes created after it is applied - the
	// vector field cannot be added to an index that already exists, so
	// switching it on for a type that is already indexed takes a new
	// index generation.
	Embeddings bool `hcl:"embeddings,optional"`

	// Facets extract the values the daily views of
	// Content.ListPublishedVersions and Content.ListPlannedVersions can
	// narrow on. They are evaluated when a version is stored, so a facet
	// only narrows what was published after it was applied - adding one
	// to a type that already has content means backfilling the rest.
	Facets []FacetConfig `hcl:"facet,block"`

	// DeliveryFields declare the type's contribution to the delivery-field
	// vocabulary: the stable public names a delivery rule may reference,
	// and how each one is read out of a document of this type.
	//
	// They are extracted when a version is stored, so a field only reaches
	// content published after it was applied. There is no backfill, and
	// unlike a facet that is not a gap to fill in later: a delivery rule
	// runs at the head of the log, and the one thing that runs rules over
	// history resolves the boundary itself.
	DeliveryFields []DeliveryFieldConfig `hcl:"delivery_field,block"`
}

// FacetConfig extracts one facet's values from a document. The label is
// the facet name a request narrows by, and several blocks may share it -
// their values are unioned.
//
// The expression should yield document UUIDs rather than labels: a facet
// filter matches exactly, with no analysis or case folding, so
// "section = sport" matches nothing at all. A section is a core/section
// document, and the client resolves display names itself.
//
// Extraction is configuration rather than code because a facet is not in
// the same place in every type - a flash, an article and a planning item
// all reference their section differently.
type FacetConfig struct {
	Name       string `hcl:"name,label"`
	Expression string `hcl:"expression"`
}

// Delivery field kinds. The kind decides which kind of condition may name
// the field, and it is fixed for the name across every type that declares
// it.
const (
	// KindKeyword is an exact value: matched against a set, with no
	// analysis and no case folding. Document UUIDs and codes.
	KindKeyword = "keyword"
	// KindNumber is a decimal number, matched by range. A value that
	// does not read as a number is dropped rather than stored as text.
	KindNumber = "number"
	// KindText is human-readable text, matched by substring, phrase or
	// prefix. It is a bounded extract, not the document body - the
	// delivery matcher never reads bodies.
	KindText = "text"
	// KindGeo is a "latitude,longitude" pair in decimal degrees, matched
	// against a circle.
	KindGeo = "geo"
)

// DeliveryFieldConfig declares one delivery field on a document type. The
// label is the name a delivery rule references, and it is a public name:
// several types declare it, each with the expression that finds it in that
// type, and a rule that names it works across all of them.
//
// The kind and description must agree wherever the name is declared. They
// are properties of the name rather than of the type, and two types
// disagreeing about them means the name means two things - which the
// service refuses when the generation is registered.
//
// Unlike a facet, the value is not necessarily a UUID: what it should be
// depends on the kind, and what a rule can do with it depends on the kind
// too.
type DeliveryFieldConfig struct {
	Name        string `hcl:"name,label"`
	Kind        string `hcl:"kind"`
	Expression  string `hcl:"expression"`
	Description string `hcl:"description,optional"`
}

// TimeExpressionConfig extracts a date or a timespan from a document. It
// is a newsdoc value-extractor expression, optionally with the layout the
// value is parsed with and the timezone a value without one of its own is
// read in.
//
// The timezone is for wall-clock timestamps that carry no offset - a
// "2006-01-02 15:04" value means the time somebody meant locally. Leave it
// off for a date-only value (`:date`): partitions are cut in UTC, so
// reading a bare date as UTC is what keeps it on the day it says, and
// reading it in a zone east of UTC moves it back a day - across a quarter
// boundary, back a whole quarter.
type TimeExpressionConfig struct {
	Expression string `hcl:"expression"`
	Layout     string `hcl:"layout,optional"`
	Timezone   string `hcl:"timezone,optional"`
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

	err = validateDocuments(tutti.Documents)
	if err != nil {
		return nil, err
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

// validateDocuments checks the parts of a document block that can be
// checked without the API: the anchor mode, and that it agrees with the
// time expressions. The expressions themselves are compiled by the
// service when the generation is registered, the way transform scripts
// are.
//
// The mismatches are worth catching here because neither of them fails:
// a forward-anchored type with no expressions silently falls back to
// anchoring on first-published, and expressions under any other anchor
// are read by nothing at all.
func validateDocuments(docs []DocumentConfig) error {
	for _, doc := range docs {
		switch doc.Anchor {
		case AnchorNone, AnchorFirstPublished:
			if len(doc.TimeExpressions) > 0 {
				return fmt.Errorf(
					"document %q: time_expression blocks need the %q anchor, nothing reads them under %q",
					doc.Type, AnchorTimeExpressions,
					anchorName(doc.Anchor))
			}
		case AnchorTimeExpressions:
			if len(doc.TimeExpressions) == 0 {
				return fmt.Errorf(
					"document %q: the %q anchor needs at least one time_expression block to partition on",
					doc.Type, AnchorTimeExpressions)
			}
		default:
			return fmt.Errorf(
				"document %q: unknown anchor %q, expected %q or %q, or no anchor at all",
				doc.Type, doc.Anchor,
				AnchorFirstPublished, AnchorTimeExpressions)
		}

		for i, e := range doc.TimeExpressions {
			if e.Expression == "" {
				return fmt.Errorf(
					"document %q: time_expression %d has no expression",
					doc.Type, i)
			}
		}

		for _, f := range doc.Facets {
			if f.Expression == "" {
				return fmt.Errorf(
					"document %q: facet %q has no expression",
					doc.Type, f.Name)
			}
		}

		err := validateDeliveryFields(doc)
		if err != nil {
			return err
		}
	}

	return nil
}

// validateDeliveryFields checks what a delivery field declaration can be
// checked for without the API: that it has an expression, that its kind is
// one of the four, and that the type does not declare the same name twice
// meaning two different things.
//
// An unknown or missing kind is worth catching here for the same reason an
// unknown anchor is: it decodes cleanly and is a semantic error, and the
// service is the only other thing that would ever notice.
//
// Cross-type agreement is deliberately not checked here. The registry the
// store path and GetDeliveryFields read is the authority, and a check that
// only exists in the CLI is a check a direct RPC caller skips.
func validateDeliveryFields(doc DocumentConfig) error {
	declared := make(map[string]DeliveryFieldConfig, len(doc.DeliveryFields))

	for _, f := range doc.DeliveryFields {
		if f.Expression == "" {
			return fmt.Errorf(
				"document %q: delivery_field %q has no expression",
				doc.Type, f.Name)
		}

		switch f.Kind {
		case KindKeyword, KindNumber, KindText, KindGeo:
		default:
			return fmt.Errorf(
				"document %q: delivery_field %q has the unknown kind %q, expected %q, %q, %q or %q",
				doc.Type, f.Name, f.Kind,
				KindKeyword, KindNumber, KindText, KindGeo)
		}

		prev, ok := declared[f.Name]
		if ok && prev.Kind != f.Kind {
			return fmt.Errorf(
				"document %q: delivery_field %q is declared as both %q and %q",
				doc.Type, f.Name, prev.Kind, f.Kind)
		}

		if ok && prev.Description != f.Description {
			return fmt.Errorf(
				"document %q: delivery_field %q is declared with two different descriptions",
				doc.Type, f.Name)
		}

		declared[f.Name] = f
	}

	return nil
}

// anchorName is the anchor mode as it reads in an error message, since the
// default one has no name in the configuration.
func anchorName(anchor string) string {
	if anchor == AnchorNone {
		return "no anchor"
	}

	return anchor
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
