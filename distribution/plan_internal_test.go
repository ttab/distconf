package distribution

import (
	"slices"
	"testing"
)

// The distribution service stores the compiled rendition configuration,
// with the per-kind defaults resolved. If we sent the raw configuration
// the active generation would never match the desired one, and every apply
// would report the same diff.
func TestRenditionConfigToRPCResolvesDefaults(t *testing.T) {
	entry := renditionConfigToRPC(RenditionsConfig{
		Kind: "image",
		Sources: []RenditionSourceConfig{
			{
				Name:       "tt-archive",
				Namespace:  "mm",
				LinkTypes:  []string{"tt/picture"},
				URIPattern: "^https?://tt\\.se/media/image/sdl([A-Za-z0-9._-]+)$",
			},
			{
				Name:       "repo",
				Namespace:  "repo",
				BlockTypes: []string{"core/graphic"},
				LinkRel:    "graphic",
				URIPattern: "^repo://([A-Za-z0-9._-]+)$",
			},
		},
	})

	defaulted := entry.Sources[0]

	if !slices.Equal(defaulted.BlockTypes, []string{"core/image"}) {
		t.Errorf("got block types %v, want [core/image]",
			defaulted.BlockTypes)
	}

	if defaulted.LinkRel != "image" {
		t.Errorf("got link rel %q, want image", defaulted.LinkRel)
	}

	explicit := entry.Sources[1]

	if !slices.Equal(explicit.BlockTypes, []string{"core/graphic"}) {
		t.Errorf("got block types %v, want [core/graphic]",
			explicit.BlockTypes)
	}

	if explicit.LinkRel != "graphic" {
		t.Errorf("got link rel %q, want graphic", explicit.LinkRel)
	}
}

// Kinds without built-in defaults are left as declared; the server rejects
// the registration if they are incomplete, and inventing values here would
// hide that.
func TestRenditionConfigToRPCUnknownKind(t *testing.T) {
	entry := renditionConfigToRPC(RenditionsConfig{
		Kind: "audio",
		Sources: []RenditionSourceConfig{
			{
				Name:       "repo",
				Namespace:  "repo",
				URIPattern: "^repo://([A-Za-z0-9._-]+)$",
			},
		},
	})

	src := entry.Sources[0]

	if len(src.BlockTypes) != 0 || src.LinkRel != "" {
		t.Errorf("got block types %v and link rel %q, want both empty",
			src.BlockTypes, src.LinkRel)
	}
}

// A delivery field name is one field per type, so the declarations are a
// set and their order in the HCL is not information. If the conversion did
// not put them in a canonical order, reordering two blocks would print a
// diff line and register a whole new configuration generation - on every
// apply, forever, since the server would keep storing the order it was
// last sent.
func TestDeliveryFieldsToRPC(t *testing.T) {
	if got := deliveryFieldsToRPC(nil); got != nil {
		t.Errorf("got %v for an empty set, want nil", got)
	}

	fields := []DeliveryFieldConfig{
		{
			Name:        "section",
			Kind:        KindKeyword,
			Expression:  ".links(rel='section')@{uuid}",
			Description: "The section.",
		},
		{
			Name:       "headline",
			Kind:       KindText,
			Expression: "@{title}",
		},
		{
			Name:       "newsvalue",
			Kind:       KindNumber,
			Expression: ".meta(type='core/newsvalue')@{value}",
		},
	}

	converted := deliveryFieldsToRPC(fields)

	names := make([]string, len(converted))
	for i, f := range converted {
		names[i] = f.GetName()
	}

	if !slices.Equal(names, []string{"headline", "newsvalue", "section"}) {
		t.Errorf("got the order %v, want it sorted by name", names)
	}

	section := converted[2]

	if section.GetKind() != KindKeyword ||
		section.GetExpression() != ".links(rel='section')@{uuid}" ||
		section.GetDescription() != "The section." {
		t.Errorf("field not converted whole: %+v", section)
	}

	reordered := deliveryFieldsToRPC([]DeliveryFieldConfig{
		fields[2], fields[0], fields[1],
	})

	if !deliveryFieldsEqual(converted, reordered) {
		t.Error("reordering the blocks is not a no-op")
	}

	// The description is what an editor shows beside the name, so a
	// change to it is a change an apply has to carry.
	described := slices.Clone(fields)
	described[1].Description = "The headline."

	if deliveryFieldsEqual(converted, deliveryFieldsToRPC(described)) {
		t.Error("a description-only change compares equal")
	}
}
