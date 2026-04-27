package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrNotFound is returned when a manifest does not exist.
var ErrNotFound = errors.New("presentation store: not found")

// ErrPreconditionFailed is returned when a conditional write precondition does
// not match the current stored representation.
var ErrPreconditionFailed = errors.New("presentation store: precondition failed")

// Store reads Presentation API documents by item id.
type Store interface {
	GetManifest(ctx context.Context, itemID string) ([]byte, error)
	GetAnnotationPage(ctx context.Context, itemID, canvasID string) ([]byte, error)
	PutAnnotationPage(ctx context.Context, itemID, canvasID string, body []byte, ifMatch string) error
}

// DocumentETag returns the strong ETag used for stored Presentation documents.
func DocumentETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func IfMatchMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimSpace(candidate) == etag {
			return true
		}
	}
	return false
}
