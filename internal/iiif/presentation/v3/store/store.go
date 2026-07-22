package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const MaxResourceKeyBytes = 768

// ErrNotFound is returned when a Presentation resource does not exist.
var ErrNotFound = errors.New("presentation store: not found")

// ErrPreconditionFailed is returned when a conditional mutation precondition
// does not match the current stored representation.
var ErrPreconditionFailed = errors.New("presentation store: precondition failed")

// Document is the byte-exact stored representation of a Presentation resource.
type Document struct {
	Body       []byte
	ModifiedAt time.Time
}

// Preconditions contains the HTTP validators used for an atomic PUT.
// Handlers require exactly one of IfMatch or IfNoneMatch.
type Preconditions struct {
	IfMatch     string
	IfNoneMatch string
}

// Store persists arbitrary Presentation API resources by their normalized
// relative public path. Application-specific identifiers and policy stay
// outside this protocol-oriented boundary.
type Store interface {
	Get(ctx context.Context, resourceKey string) (Document, error)
	Put(ctx context.Context, resourceKey string, body []byte, conditions Preconditions) (created bool, err error)
	Delete(ctx context.Context, resourceKey, ifMatch string) error
}

// DocumentETag returns the strong ETag used for stored Presentation documents.
func DocumentETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// IfMatchMatches reports whether an If-Match field allows the current strong
// entity tag. Weak validators never satisfy If-Match.
func IfMatchMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if !strings.HasPrefix(candidate, "W/") && candidate == etag {
			return true
		}
	}
	return false
}

func validResourceKey(key string) bool {
	return key != "" && len(key) <= MaxResourceKeyBytes && !strings.ContainsAny(key, "\x00\n\r")
}

func putPreconditionMatches(exists bool, currentETag string, conditions Preconditions) bool {
	if conditions.IfNoneMatch != "" {
		return conditions.IfMatch == "" && conditions.IfNoneMatch == "*" && !exists
	}
	return conditions.IfMatch != "" && exists && IfMatchMatches(conditions.IfMatch, currentETag)
}
