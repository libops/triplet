package store

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a manifest does not exist.
var ErrNotFound = errors.New("presentation store: not found")

// Store reads Presentation API documents by item id.
type Store interface {
	GetManifest(ctx context.Context, itemID string) ([]byte, error)
	GetAnnotationPage(ctx context.Context, itemID, canvasID string) ([]byte, error)
	PutAnnotationPage(ctx context.Context, itemID, canvasID string, body []byte) error
}
