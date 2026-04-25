package validate

import "testing"

func TestValidateManifestBytes(t *testing.T) {
	valid := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"http://example.test/manifest","type":"Manifest","label":{"en":["Manifest"]},"items":[{"id":"http://example.test/canvas/1","type":"Canvas"}]}`)
	if err := ValidateManifestBytes(valid); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	invalid := []byte(`{"id":"http://example.test/manifest","type":"Manifest","items":[]}`)
	if err := ValidateManifestBytes(invalid); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateAnnotationPageBytes(t *testing.T) {
	valid := []byte(`{"@context":["http://iiif.io/api/extension/text-granularity/context.json","http://iiif.io/api/presentation/3/context.json"],"id":"http://example.test/annotations","type":"AnnotationPage","items":[{"id":"http://example.test/annotations/1","type":"Annotation","textGranularity":"line","motivation":["supplementing"],"body":{"type":"TextualBody","value":"hello"},"target":{"type":"SpecificResource","source":"http://example.test/canvas/1","selector":{"type":"FragmentSelector","value":"xywh=1,2,3,4"}}}]}`)
	if err := ValidateAnnotationPageBytes(valid); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	invalid := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"http://example.test/annotations","type":"AnnotationPage","items":[{"id":"http://example.test/annotations/1","type":"Annotation","textGranularity":"line","body":{"type":"TextualBody","value":"hello"},"target":{"id":"http://example.test/canvas/1"}}]}`)
	if err := ValidateAnnotationPageBytes(invalid); err == nil {
		t.Fatal("expected validation error")
	}
}
