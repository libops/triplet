package config

import (
	"image/color"
	"strings"
	"testing"
)

func TestParseColor(t *testing.T) {
	tests := []struct {
		raw     string
		want    color.RGBA
		wantErr bool
	}{
		{raw: "white", want: color.RGBA{255, 255, 255, 255}},
		{raw: " Navy ", want: color.RGBA{0, 0, 128, 255}},
		{raw: "#fff", want: color.RGBA{255, 255, 255, 255}},
		{raw: "#1a2B3c", want: color.RGBA{0x1a, 0x2b, 0x3c, 255}},
		{raw: "", wantErr: true},
		{raw: "#ffff", wantErr: true},
		{raw: "#ffffff00", wantErr: true},
		{raw: "#gggggg", wantErr: true},
		{raw: "ffffff", wantErr: true},
		{raw: "rgb(1, 2, 3)", wantErr: true},
		{raw: "notacolor", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := parseColor(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseColor(%q) = %v, want error", tc.raw, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("parseColor(%q) = %v, %v; want %v", tc.raw, got, err, tc.want)
			}
		})
	}
}

func TestLoadBackgroundColor(t *testing.T) {
	body := func(value string) string {
		return `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
` + value + `
sources:
  default: file
  file:
    root: /tmp
`
	}

	c, err := Load(writeConfig(t, body(`    background_color: "#ffffff"`)))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.IIIF.Image.BackgroundColor; got == nil || *got != (Color{255, 255, 255, 255}) {
		t.Fatalf("BackgroundColor = %v", got)
	}

	c, err = Load(writeConfig(t, body(`    enabled: true`)))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.IIIF.Image.BackgroundColor != nil {
		t.Fatalf("BackgroundColor default = %v, want nil", c.IIIF.Image.BackgroundColor)
	}

	_, err = Load(writeConfig(t, body(`    background_color: transparent`)))
	if err == nil || !strings.Contains(err.Error(), "transparent") {
		t.Fatalf("err = %v, want invalid color error", err)
	}
}
