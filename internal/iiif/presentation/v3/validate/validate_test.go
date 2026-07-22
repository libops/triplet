package validate

import "testing"

func TestValidateResourceBytes(t *testing.T) {
	tests := []struct {
		name     string
		doc      string
		typeName string
	}{
		{
			name:     "manifest",
			doc:      `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/manifest","type":"Manifest","label":{"en":["Manifest"]},"items":[]}`,
			typeName: "Manifest",
		},
		{
			name:     "canvas",
			doc:      `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/canvas/1","type":"Canvas","width":100,"height":200,"items":[]}`,
			typeName: "Canvas",
		},
		{
			name:     "annotation page with compact target and extension property",
			doc:      `{"@context":["https://example.org/extension/context.json","http://iiif.io/api/extension/text-granularity/context.json","http://iiif.io/api/presentation/3/context.json"],"id":"https://example.org/pages/1","type":"AnnotationPage","items":[{"id":"https://example.org/annotations/1","type":"Annotation","motivation":"supplementing","body":{"type":"TextualBody","value":"hello"},"target":"https://example.org/canvas/1#xywh=1,2,3,4","textGranularity":"line"}],"example:custom":7}`,
			typeName: "AnnotationPage",
		},
		{
			name:     "standalone annotation",
			doc:      `{"@context":["http://iiif.io/api/extension/text-granularity/context.json","http://iiif.io/api/presentation/3/context.json"],"id":"https://example.org/annotations/1","type":"Annotation","target":"https://example.org/canvas/1","textGranularity":"token"}`,
			typeName: "Annotation",
		},
		{
			name:     "collection",
			doc:      `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/collection","type":"Collection","label":{"en":["Collection"]},"items":[]}`,
			typeName: "Collection",
		},
		{
			name:     "annotation collection",
			doc:      `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/annotation-collection","type":"AnnotationCollection"}`,
			typeName: "AnnotationCollection",
		},
		{
			name:     "range",
			doc:      `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/range/1","type":"Range","label":{"en":["Chapter one"]},"items":[{"id":"https://example.org/canvas/1","type":"Canvas"}]}`,
			typeName: "Range",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resource, err := ValidateResourceBytes([]byte(test.doc))
			if err != nil {
				t.Fatalf("valid resource rejected: %v", err)
			}
			if resource.Type != test.typeName {
				t.Fatalf("type = %q, want %q", resource.Type, test.typeName)
			}
		})
	}
}

func TestValidateResourceBytesRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{name: "malformed JSON", doc: `{`},
		{name: "missing id", doc: `{"@context":"http://iiif.io/api/presentation/3/context.json","type":"AnnotationPage","items":[]}`},
		{name: "non HTTP id", doc: `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"urn:example:1","type":"AnnotationPage","items":[]}`},
		{name: "missing context", doc: `{"id":"https://example.org/pages/1","type":"AnnotationPage","items":[]}`},
		{name: "Presentation context not final", doc: `{"@context":["http://iiif.io/api/presentation/3/context.json","https://example.org/extension/context.json"],"id":"https://example.org/pages/1","type":"AnnotationPage","items":[]}`},
		{name: "text granularity context missing", doc: `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/pages/1","type":"AnnotationPage","items":[{"id":"https://example.org/annotations/1","type":"Annotation","textGranularity":"line"}]}`},
		{name: "text granularity is not string", doc: `{"@context":["http://iiif.io/api/extension/text-granularity/context.json","http://iiif.io/api/presentation/3/context.json"],"id":"https://example.org/pages/1","type":"AnnotationPage","items":[{"id":"https://example.org/annotations/1","type":"Annotation","textGranularity":["line"]}]}`},
		{name: "unsupported top-level type", doc: `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/sequence/1","type":"Sequence","items":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateResourceBytes([]byte(test.doc)); err == nil {
				t.Fatal("invalid resource accepted")
			}
		})
	}
}
