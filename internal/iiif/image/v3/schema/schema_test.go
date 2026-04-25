package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	imagetypes "github.com/libops/triplet/internal/iiif/image/v3/types"
)

func TestValidateInfoBytesBuildLevel2Info(t *testing.T) {
	doc, err := json.Marshal(imagetypes.BuildLevel2Info(
		"http://example.test/iiif/3/sample.png",
		200,
		100,
		imagetypes.Limits{
			MaxArea:   10_000_000,
			MaxWidth:  4096,
			MaxHeight: 4096,
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateInfoBytes(doc); err != nil {
		t.Fatalf("validate built info: %v", err)
	}
}

func TestValidateInfoBytesValidatorExamples(t *testing.T) {
	base := filepath.Join("..", "testdata", "validator")
	cases := []struct {
		name    string
		file    string
		wantErr string
	}{
		{name: "plain", file: "info-3.0.json"},
		{name: "logo", file: "info-3.0-logo.json"},
		{name: "service", file: "info-3.0-service.json"},
		{name: "service_label", file: "info-3.0-service-label.json"},
		{name: "service_badlabel", file: "info-3.0-service-badlabel.json", wantErr: "validate info.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := os.ReadFile(filepath.Join(base, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateInfoBytes(doc)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateInfoBytes(%s): %v", tc.file, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateInfoBytes(%s): got nil error, want %q", tc.file, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateInfoBytes(%s): error = %q, want substring %q", tc.file, err, tc.wantErr)
			}
		})
	}
}
