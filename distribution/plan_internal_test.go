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
