// Package types defines the JSON-LD shapes returned by the Image API 3.0.
//
// The wire-format `Info` type is generated from the iiif-spec Image v3 schema.
package types

import gen "github.com/libops/iiif-spec/image/v3/gen"

// Context is the JSON-LD @context for Image API 3.0 responses.
const Context = "http://iiif.io/api/image/3/context.json"

// ProtocolURI identifies the Image API protocol in info.json.
const ProtocolURI = "http://iiif.io/api/image"

// TypeImageService3 is the @type value for an Image API 3.0 service.
const TypeImageService3 = "ImageService3"

// Profile values per the spec.
const (
	ProfileLevel0 = "level0"
	ProfileLevel1 = "level1"
	ProfileLevel2 = "level2"
)

// Level2Features is the full set of extraFeatures triplet declares for a
// level 2 implementation. Tracks the spec at
// https://iiif.io/api/image/3.0/#57-extra-functionality.
var Level2Features = gen.StringList{
	"baseUriRedirect",
	"canonicalLinkHeader",
	"cors",
	"jsonldMediaType",
	"mirroring",
	"profileLinkHeader",
	"regionByPct",
	"regionByPx",
	"regionSquare",
	"rotationArbitrary",
	"rotationBy90s",
	"sizeByConfinedWh",
	"sizeByH",
	"sizeByPct",
	"sizeByW",
	"sizeByWh",
	"sizeUpscaling",
}

// Level2Qualities are qualities every level 2 implementation must support.
var Level2Qualities = gen.StringList{"color", "gray", "bitonal", "default"}

// Level2Formats are formats triplet supports today (subset of spec optionals).
var Level2Formats = gen.StringList{"jpg", "png", "gif", "webp", "tif"}

type (
	// Info is the document returned by /{id}/info.json.
	Info = gen.InfoSchemaJson
	// LanguageMap is the IIIF language map shape shared with linked resources.
	LanguageMap = gen.LanguageMap
	// Reference is a linked IIIF resource with the common id/type/label shape.
	Reference = gen.Reference
	// ExternalResource is a linked machine-readable resource.
	ExternalResource = gen.ExternalResource
	// Service is a linked service resource.
	Service = gen.Service
	// Size is an entry in info.json's `sizes` array.
	Size = gen.Size
	// Tile is an entry in info.json's `tiles` array.
	Tile = gen.Tile
)

// Limits declares server-advertised transform caps for info.json.
type Limits struct {
	MaxArea   int64
	MaxWidth  int
	MaxHeight int
}

// BuildLevel2Info populates a Level 2 info document for an image with the
// given source dimensions. The id field is the canonical service URL
// (no trailing slash, no /info.json).
func BuildLevel2Info(id string, width, height int, limits Limits) Info {
	info := Info{
		Context:        Context,
		Id:             id,
		Type:           TypeImageService3,
		Protocol:       ProtocolURI,
		Profile:        gen.InfoSchemaJsonProfile(ProfileLevel2),
		Width:          gen.PositiveInteger(width),
		Height:         gen.PositiveInteger(height),
		ExtraQualities: Level2Qualities,
		ExtraFormats:   Level2Formats,
		ExtraFeatures:  Level2Features,
		Tiles: []gen.Tile{{
			Width:        gen.PositiveInteger(512),
			ScaleFactors: pyramidScaleFactors(width, height, 512),
		}},
	}
	if limits.MaxArea > 0 {
		v := gen.PositiveInteger(limits.MaxArea)
		info.MaxArea = &v
	}
	if limits.MaxWidth > 0 {
		v := gen.PositiveInteger(limits.MaxWidth)
		info.MaxWidth = &v
	}
	if limits.MaxHeight > 0 {
		v := gen.PositiveInteger(limits.MaxHeight)
		info.MaxHeight = &v
	}
	return info
}

func pyramidScaleFactors(width, height, tile int) []gen.PositiveInteger {
	max := width
	if height > max {
		max = height
	}
	out := []gen.PositiveInteger{1}
	f := 2
	for max/f > tile {
		out = append(out, gen.PositiveInteger(f))
		f *= 2
	}
	return out
}
