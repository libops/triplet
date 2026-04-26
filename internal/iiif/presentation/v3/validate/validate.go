package validate

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/libops/triplet/internal/iiif/presentation/v3/types"
)

// ValidateManifestBytes performs structural validation for the manifest
// surface triplet serves today. It is intentionally narrower than the full
// Presentation 3 validator, but it enforces the required top-level contract.
func ValidateManifestBytes(b []byte) error {
	var doc manifestDocument
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if !hasContext(doc.Context, types.Context) {
		return fmt.Errorf("manifest @context must include %q", types.Context)
	}
	if strings.TrimSpace(doc.ID) == "" {
		return errors.New("manifest id is required")
	}
	if doc.Type != types.TypeManifest {
		return fmt.Errorf("manifest type must be %q", types.TypeManifest)
	}
	if len(doc.Items) == 0 {
		return errors.New("manifest items is required")
	}
	return nil
}

// ValidateAnnotationPageBytes validates the annotation-page authoring surface
// triplet supports. It enforces the required top-level fields plus the text
// granularity extension constraints triplet explicitly supports.
func ValidateAnnotationPageBytes(b []byte) error {
	var doc annotationPageDocument
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("decode annotation page: %w", err)
	}
	if !hasContext(doc.Context, types.Context) {
		return fmt.Errorf("annotation page @context must include %q", types.Context)
	}
	if strings.TrimSpace(doc.ID) == "" {
		return errors.New("annotation page id is required")
	}
	if doc.Type != types.TypeAnnotationPage {
		return fmt.Errorf("annotation page type must be %q", types.TypeAnnotationPage)
	}
	if doc.Items == nil {
		return errors.New("annotation page items is required")
	}

	needsTextGranularityContext := false
	for i, item := range doc.Items {
		if err := item.validate(); err != nil {
			return fmt.Errorf("annotation page items[%d]: %w", i, err)
		}
		if item.TextGranularity != "" {
			needsTextGranularityContext = true
		}
	}
	if needsTextGranularityContext && !hasContext(doc.Context, types.ContextTextGranularity) {
		return fmt.Errorf("annotation page @context must include %q when textGranularity is used", types.ContextTextGranularity)
	}
	return nil
}

type manifestDocument struct {
	Context any               `json:"@context"`
	ID      string            `json:"id"`
	Type    string            `json:"type"`
	Items   []json.RawMessage `json:"items"`
}

type annotationPageDocument struct {
	Context any                  `json:"@context"`
	ID      string               `json:"id"`
	Type    string               `json:"type"`
	Items   []annotationDocument `json:"items"`
}

type annotationDocument struct {
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Motivation      motivationValue `json:"motivation"`
	Body            json.RawMessage `json:"body"`
	Target          json.RawMessage `json:"target"`
	TextGranularity string          `json:"textGranularity,omitempty"`
}

func (a annotationDocument) validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("id is required")
	}
	if a.Type != types.TypeAnnotation {
		return fmt.Errorf("type must be %q", types.TypeAnnotation)
	}
	if len(a.Body) == 0 {
		return errors.New("body is required")
	}
	if len(a.Target) == 0 {
		return errors.New("target is required")
	}
	if err := validateBody(a.Body); err != nil {
		return fmt.Errorf("body: %w", err)
	}
	if err := validateTarget(a.Target); err != nil {
		return fmt.Errorf("target: %w", err)
	}
	if a.TextGranularity != "" {
		if !slices.Contains(validTextGranularity, a.TextGranularity) {
			return fmt.Errorf("textGranularity %q is not supported", a.TextGranularity)
		}
		if !a.Motivation.contains(types.MotivationSupplementing) {
			return fmt.Errorf("textGranularity requires motivation %q", types.MotivationSupplementing)
		}
	}
	return nil
}

func validateBody(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("missing")
	}

	var single map[string]any
	if err := json.Unmarshal(raw, &single); err == nil && len(single) > 0 {
		return validateBodyObject(single)
	}

	var many []map[string]any
	if err := json.Unmarshal(raw, &many); err != nil {
		return errors.New("must be an object or array")
	}
	if len(many) == 0 {
		return errors.New("must not be empty")
	}
	for i, body := range many {
		if err := validateBodyObject(body); err != nil {
			return fmt.Errorf("[%d]: %w", i, err)
		}
	}
	return nil
}

func validateBodyObject(body map[string]any) error {
	typ, _ := body["type"].(string)
	switch typ {
	case types.TypeTextualBody:
		if strings.TrimSpace(stringValue(body["value"])) == "" {
			return errors.New("TextualBody value is required")
		}
		return nil
	case types.TypeSpecificResource:
		if strings.TrimSpace(stringValue(body["source"])) == "" {
			return errors.New("SpecificResource source is required")
		}
		return nil
	default:
		if typ == "" {
			return errors.New("type is required")
		}
		if id := stringValue(body["id"]); strings.TrimSpace(id) == "" {
			return errors.New("body resource id is required for non-TextualBody bodies")
		}
		return nil
	}
}

func validateTarget(raw json.RawMessage) error {
	var target map[string]any
	if err := json.Unmarshal(raw, &target); err != nil {
		return errors.New("must be an object")
	}
	typ := stringValue(target["type"])
	switch typ {
	case "":
		if strings.TrimSpace(stringValue(target["id"])) == "" {
			return errors.New("id is required")
		}
	case types.TypeSpecificResource:
		if strings.TrimSpace(stringValue(target["source"])) == "" {
			return errors.New("SpecificResource source is required")
		}
	default:
		if strings.TrimSpace(stringValue(target["id"])) == "" {
			return errors.New("id is required")
		}
	}
	return nil
}

type motivationValue []string

func (m *motivationValue) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*m = nil
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*m = []string{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*m = many
	return nil
}

func (m motivationValue) contains(needle string) bool {
	for _, v := range m {
		if v == needle {
			return true
		}
	}
	return false
}

func hasContext(ctx any, want string) bool {
	switch v := ctx.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	case map[string]any:
		_, ok := v[want]
		return ok
	}
	return false
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

var validTextGranularity = []string{
	types.TextGranularityPage,
	types.TextGranularityBlock,
	types.TextGranularityParagraph,
	types.TextGranularityLine,
	types.TextGranularityWord,
	types.TextGranularityGlyph,
}
