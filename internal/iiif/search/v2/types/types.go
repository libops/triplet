package types

const (
	ContextSearch2     = "http://iiif.io/api/search/2/context.json"
	TypeAnnotation     = "Annotation"
	TypeAnnotationPage = "AnnotationPage"
)

// AnnotationPage is the Content Search 2.0 response container. Triplet's
// default searcher currently returns an empty page; backend adapters can fill
// Items with Web Annotation matches.
type AnnotationPage struct {
	Context any          `json:"@context,omitempty"`
	ID      string       `json:"id"`
	Type    string       `json:"type"`
	Items   []Annotation `json:"items"`
	PartOf  []ServiceRef `json:"partOf,omitempty"`
}

// Annotation is intentionally permissive: Content Search annotations vary by
// body/target selector shape, so adapters can emit JSON-LD-compatible maps.
type Annotation struct {
	ID         string `json:"id,omitempty"`
	Type       string `json:"type"`
	Motivation any    `json:"motivation,omitempty"`
	Body       any    `json:"body,omitempty"`
	Target     any    `json:"target,omitempty"`
}

type ServiceRef struct {
	ID   string `json:"id"`
	Type string `json:"type,omitempty"`
}
