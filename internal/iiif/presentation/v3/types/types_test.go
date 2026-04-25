package types

import (
	"encoding/json"
	"testing"

	textualbodygen "github.com/libops/iiif-spec/presentation/v3/gen/textualbody"
)

func TestAnnotationTextGranularityRoundTrip(t *testing.T) {
	page := AnnotationPage{
		Context: []string{ContextTextGranularity, Context},
		ID:      "https://example.org/iiif/aeneid/book1/annotations/page-1",
		Type:    TypeAnnotationPage,
		Items: []Annotation{{
			ID:              "https://example.org/iiif/aeneid/book1/transcription-line1",
			Type:            TypeAnnotation,
			TextGranularity: TextGranularityLine,
			Motivation:      []string{MotivationSupplementing},
			Body: TextualBody{
				Type:     TypeTextualBody,
				Language: []textualbodygen.BCP47{"la"},
				Value:    "arma virumque cano, Troiae qui primus ab oris",
			},
			Target: SpecificResource{
				Id:     "https://example.org/aeneid/target/line-1",
				Type:   TypeSpecificResource,
				Source: "https://example.org/aeneid/canvas/1r",
				Selector: FragmentSelector{
					"type":  "FragmentSelector",
					"value": "xywh=500,1100,3500,100",
				},
			},
		}},
	}

	b, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if got := raw["@context"]; got == nil {
		t.Fatal("missing @context")
	}

	items, ok := raw["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", raw["items"])
	}
	anno, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("annotation = %#v", items[0])
	}
	if got := anno["textGranularity"]; got != TextGranularityLine {
		t.Fatalf("textGranularity = %#v", got)
	}
	body, ok := anno["body"].(map[string]any)
	if !ok {
		t.Fatalf("body = %#v", anno["body"])
	}
	if got := body["type"]; got != TypeTextualBody {
		t.Fatalf("body.type = %#v", got)
	}
	target, ok := anno["target"].(map[string]any)
	if !ok {
		t.Fatalf("target = %#v", anno["target"])
	}
	if got := target["type"]; got != TypeSpecificResource {
		t.Fatalf("target.type = %#v", got)
	}
}
