package searcher

import (
	"context"

	"github.com/libops/triplet/internal/iiif/search/v2/types"
)

// Request captures the spec query parameters triplet needs to route a Content
// Search request. Backend-specific ranking and indexing stay outside triplet.
type Request struct {
	ItemID string
	Query  string
}

// Searcher resolves a Content Search query into an AnnotationPage.
type Searcher interface {
	Search(ctx context.Context, req Request) (types.AnnotationPage, error)
}

// Noop is the default backend. It preserves the IIIF HTTP surface without
// making triplet responsible for indexing.
type Noop struct{}

func (Noop) Search(context.Context, Request) (types.AnnotationPage, error) {
	return types.AnnotationPage{
		Context: types.ContextSearch2,
		Type:    types.TypeAnnotationPage,
		Items:   []types.Annotation{},
	}, nil
}
