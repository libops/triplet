// Package types defines JSON-LD shapes for the implemented IIIF Presentation
// API 3.0 surface.
//
// The base wire-format resource types are generated from vendored upstream
// Presentation JSON Schemas in iiif-spec.
// Small wrappers remain where triplet needs extension fields that are not part
// of those upstream schemas, such as `textGranularity`.
package types

import (
	annotationgen "github.com/libops/iiif-spec/presentation/v3/gen/annotation"
	canvasgen "github.com/libops/iiif-spec/presentation/v3/gen/canvas"
	manifestgen "github.com/libops/iiif-spec/presentation/v3/gen/manifest"
	specificresourcegen "github.com/libops/iiif-spec/presentation/v3/gen/specificresource"
	textualbodygen "github.com/libops/iiif-spec/presentation/v3/gen/textualbody"
)

// Context is the JSON-LD @context for Presentation API 3.0 responses.
const Context = "http://iiif.io/api/presentation/3/context.json"

// ContextTextGranularity is the JSON-LD context for the IIIF text granularity
// extension.
const ContextTextGranularity = "http://iiif.io/api/extension/text-granularity/context.json"

// Common resource types.
const (
	TypeManifest             = "Manifest"
	TypeCollection           = "Collection"
	TypeCanvas               = "Canvas"
	TypeRange                = "Range"
	TypeAnnotationCollection = "AnnotationCollection"
	TypeAnnotationPage       = "AnnotationPage"
	TypeAnnotation           = "Annotation"
	TypeSpecificResource     = "SpecificResource"
	TypeTextualBody          = "TextualBody"
	TypeImage                = "Image"
	TypeDataset              = "Dataset"
	TypeSound                = "Sound"
	TypeVideo                = "Video"
	TypeText                 = "Text"
	TypeService              = "Service"
)

// Common presentation motivations and purposes used by text-oriented
// annotations.
const (
	MotivationSupplementing = "supplementing"
	PurposeTranscribing     = "transcribing"
)

// Text granularity levels from the IIIF text-granularity extension.
const (
	TextGranularityPage      = "page"
	TextGranularityBlock     = "block"
	TextGranularityParagraph = "paragraph"
	TextGranularityLine      = "line"
	TextGranularityWord      = "word"
	TextGranularityGlyph     = "glyph"
)

type (
	// LanguageMap is the IIIF language map shape.
	LanguageMap = manifestgen.LngString
	// Manifest is the generated manifest wire type.
	Manifest = manifestgen.ManifestJson
	// Canvas is the generated canvas wire type.
	Canvas = canvasgen.CanvasJson
	// TextualBody is the generated textual body wire type.
	TextualBody = textualbodygen.TextualBodyJson
	// SpecificResource is the generated specific resource wire type.
	SpecificResource = specificresourcegen.SpecificResourceJson
	// FragmentSelector remains open because the upstream schema models selectors
	// as interfaces/maps rather than a stable concrete struct.
	FragmentSelector = map[string]any
	// XPathSelector remains open for the same reason.
	XPathSelector = map[string]any
)

// AnnotationPageRef references an annotation page. The upstream generated type
// is currently map-shaped, so this stays as a small handwritten stable wrapper.
type AnnotationPageRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// Resource is an open JSON-LD IIIF resource wrapper for generated-schema gaps
// such as Collection, Range, Service, provider, homepage, rendering, and
// externally described body resources.
type Resource struct {
	Context any            `json:"@context,omitempty"`
	ID      string         `json:"id,omitempty"`
	Type    string         `json:"type"`
	Label   LanguageMap    `json:"label,omitempty"`
	Items   []Resource     `json:"items,omitempty"`
	PartOf  []Resource     `json:"partOf,omitempty"`
	Service []Service      `json:"service,omitempty"`
	Body    any            `json:"body,omitempty"`
	Target  any            `json:"target,omitempty"`
	Extra   map[string]any `json:"-"`
}

type Collection = Resource
type Range = Resource

// Service is intentionally open because IIIF services span Image, Search,
// Auth, and extension APIs.
type Service struct {
	ID      string      `json:"id,omitempty"`
	Type    string      `json:"type"`
	Profile string      `json:"profile,omitempty"`
	Label   LanguageMap `json:"label,omitempty"`
	Service []Service   `json:"service,omitempty"`
}

// Annotation is a simplified Web Annotation / IIIF annotation resource with
// support for the text-granularity extension.
type Annotation struct {
	ID              string      `json:"id"`
	Type            string      `json:"type"`
	Label           LanguageMap `json:"label,omitempty"`
	Motivation      any         `json:"motivation,omitempty"`
	Body            any         `json:"body,omitempty"`
	Target          any         `json:"target,omitempty"`
	TextGranularity string      `json:"textGranularity,omitempty"`
}

// AnnotationPage is a page of annotation resources. This stays handwritten so
// its `items` can carry the text-granularity-aware `Annotation` wrapper.
type AnnotationPage struct {
	Context any          `json:"@context,omitempty"`
	ID      string       `json:"id"`
	Type    string       `json:"type"`
	Items   []Annotation `json:"items,omitempty"`
}

// AnnotationResource exposes the generated upstream annotation shape for code
// that wants the raw schema-backed type without the text-granularity wrapper.
type AnnotationResource = annotationgen.AnnotationJson
