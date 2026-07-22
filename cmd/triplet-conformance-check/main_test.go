package main

import "testing"

func TestValidatePresentationFixtures(t *testing.T) {
	manifest := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/manifest","type":"Manifest","label":{"en":["Manifest"]},"items":[{"id":"https://example.org/canvas/1","type":"Canvas","width":100,"height":100,"items":[]}]}`)
	if err := validateManifest(manifest); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	page := []byte(`{"@context":["http://iiif.io/api/extension/text-granularity/context.json","http://iiif.io/api/presentation/3/context.json"],"id":"https://example.org/page/1","type":"AnnotationPage","items":[{"id":"https://example.org/annotation/1","type":"Annotation","target":"https://example.org/canvas/1","textGranularity":"line"}]}`)
	if err := validateAnnotationPage(page); err != nil {
		t.Fatalf("valid annotation page rejected: %v", err)
	}
}

func TestValidatePresentationFixturesRejectsEmptyResources(t *testing.T) {
	manifest := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/manifest","type":"Manifest","label":{"en":["Manifest"]},"items":[]}`)
	if err := validateManifest(manifest); err == nil {
		t.Fatal("empty manifest accepted")
	}
	page := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/page/1","type":"AnnotationPage","items":[]}`)
	if err := validateAnnotationPage(page); err == nil {
		t.Fatal("empty annotation page accepted")
	}
}
