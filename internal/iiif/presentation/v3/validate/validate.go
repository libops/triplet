package validate

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	textgranularityschema "github.com/libops/iiif-spec/extension/textgranularity/schema"
	presentationschema "github.com/libops/iiif-spec/presentation/v3/schema"
	"github.com/libops/triplet/internal/iiif/presentation/v3/types"
)

// Resource identifies a validated top-level IIIF Presentation resource.
type Resource struct {
	ID   string
	Type string
}

// ValidateResourceBytes validates a byte-exact top-level Presentation API
// resource with libops/iiif-spec's extension-aware schemas. Text Granularity
// annotations use the extension's generic schema; application-specific OCR
// profiles do not belong at this protocol boundary.
func ValidateResourceBytes(body []byte) (Resource, error) {
	var resource Resource
	if err := json.Unmarshal(body, &resource); err != nil {
		return Resource{}, fmt.Errorf("decode presentation resource: %w", err)
	}
	if strings.TrimSpace(resource.ID) == "" {
		return Resource{}, errors.New("presentation resource id is required")
	}
	if err := validateHTTPID(resource.ID); err != nil {
		return Resource{}, err
	}

	var err error
	switch resource.Type {
	case types.TypeManifest:
		err = presentationschema.ValidateExtensibleManifestBytes(body)
	case types.TypeCanvas:
		err = presentationschema.ValidateExtensibleCanvasBytes(body)
	case types.TypeAnnotationPage:
		err = textgranularityschema.ValidateAnnotationPageBytes(body)
	case types.TypeAnnotation:
		err = textgranularityschema.ValidateAnnotationBytes(body)
	case types.TypeRange:
		err = presentationschema.ValidateExtensibleRangeBytes(body)
	case types.TypeCollection, types.TypeAnnotationCollection:
		err = presentationschema.ValidateExtensibleBytes(body)
	case "":
		return Resource{}, errors.New("presentation resource type is required")
	default:
		return Resource{}, fmt.Errorf("presentation resource type %q is not supported", resource.Type)
	}
	if err != nil {
		return Resource{}, err
	}
	return resource, nil
}

func validateHTTPID(id string) error {
	parsed, err := url.Parse(id)
	if err != nil {
		return fmt.Errorf("presentation resource id: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("presentation resource id must be an absolute http(s) URL")
	}
	return nil
}
