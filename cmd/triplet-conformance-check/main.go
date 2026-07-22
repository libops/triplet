// Command triplet-conformance-check validates responses captured by the
// repository's HTTP conformance smoke test without embedding a scripting
// language in shell automation.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	imageschema "github.com/libops/triplet/internal/iiif/image/v3/schema"
	presentationvalidate "github.com/libops/triplet/internal/iiif/presentation/v3/validate"
)

func main() {
	infoPath := flag.String("info", "", "captured Image API info.json")
	manifestPath := flag.String("manifest", "", "captured Presentation API Manifest")
	annotationPagePath := flag.String("annotation-page", "", "captured Presentation API AnnotationPage")
	flag.Parse()
	if err := run(*infoPath, *manifestPath, *annotationPagePath); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(infoPath, manifestPath, annotationPagePath string) error {
	if infoPath == "" || manifestPath == "" || annotationPagePath == "" {
		return errors.New("-info, -manifest, and -annotation-page are required")
	}
	info, err := os.ReadFile(infoPath)
	if err != nil {
		return fmt.Errorf("read info.json: %w", err)
	}
	if err := validateInfo(info); err != nil {
		return err
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	page, err := os.ReadFile(annotationPagePath)
	if err != nil {
		return fmt.Errorf("read annotation page: %w", err)
	}
	return validateAnnotationPage(page)
}

func validateInfo(body []byte) error {
	if err := imageschema.ValidateInfoBytes(body); err != nil {
		return fmt.Errorf("validate info.json: %w", err)
	}
	return nil
}

func validateManifest(body []byte) error {
	resource, err := presentationvalidate.ValidateResourceBytes(body)
	if err != nil {
		return fmt.Errorf("validate manifest: %w", err)
	}
	if resource.Type != "Manifest" {
		return fmt.Errorf("manifest resource type is %q", resource.Type)
	}
	var document struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if len(document.Items) == 0 {
		return errors.New("manifest items must not be empty")
	}
	return nil
}

func validateAnnotationPage(body []byte) error {
	resource, err := presentationvalidate.ValidateResourceBytes(body)
	if err != nil {
		return fmt.Errorf("validate annotation page: %w", err)
	}
	if resource.Type != "AnnotationPage" {
		return fmt.Errorf("annotation page resource type is %q", resource.Type)
	}
	var document struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("decode annotation page: %w", err)
	}
	if len(document.Items) == 0 {
		return errors.New("annotation page items must not be empty")
	}
	return nil
}
