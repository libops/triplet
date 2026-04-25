package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "minimal valid",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: file
  file:
    root: /tmp
`,
		},
		{
			name: "missing public_base_url",
			body: `
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "public_base_url is required",
		},
		{
			name: "unknown source",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: azure
`,
			wantErr: "not supported in this build",
		},
		{
			name: "http source valid",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  http:
    allowed_hosts: [example.org]
    max_bytes: 1048576
`,
		},
		{
			name: "gcs source valid",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: gcs
  gcs:
    bucket_url: gs://example-bucket
`,
		},
		{
			name: "file source missing root",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: file
`,
			wantErr: "sources.file.root is required",
		},
		{
			name: "http source missing hosts",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  http: {}
`,
			wantErr: "sources.http.allowed_hosts is required",
		},
		{
			name: "bad logging level",
			body: `
server:
  public_base_url: http://localhost:8080
logging:
  level: trace
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "logging.level",
		},
		{
			name: "unknown field rejected",
			body: `
server:
  public_base_url: http://localhost:8080
  bogus: 1
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "field bogus",
		},
		{
			name: "presentation enabled requires root",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  presentation:
    enabled: true
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "iiif.presentation.root is required",
		},
		{
			name: "cache root and bucket url conflict",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: file
  file:
    root: /tmp
cache:
  root: /tmp/cache
  bucket_url: gs://cache-bucket
`,
			wantErr: "cache.root and cache.bucket_url are mutually exclusive",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.body)
			_, err := Load(path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
sources:
  default: file
  file:
    root: /tmp
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Server.Listen != ":8080" {
		t.Errorf("Listen default = %q", c.Server.Listen)
	}
	if c.IIIF.Image.Prefix != "/iiif/3" {
		t.Errorf("Image.Prefix default = %q", c.IIIF.Image.Prefix)
	}
	if c.Logging.Level != "info" {
		t.Errorf("Logging.Level default = %q", c.Logging.Level)
	}
	if c.Vips.BlockUntrusted == nil || !*c.Vips.BlockUntrusted {
		t.Errorf("Vips.BlockUntrusted default = %#v", c.Vips.BlockUntrusted)
	}
}

func TestLoadRejectsNegativeImageLimits(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    max_width: -1
sources:
  default: file
  file:
    root: /tmp
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "iiif.image.max_width") {
		t.Fatalf("err = %v, want iiif.image.max_width validation error", err)
	}
}

func TestLoadRejectsNegativeHTTPMaxBytes(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  http:
    allowed_hosts: [example.org]
    max_bytes: -1
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "sources.http.max_bytes") {
		t.Fatalf("err = %v, want sources.http.max_bytes validation error", err)
	}
}

func TestLoadRejectsNegativeSourceCacheMaxBytes(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
sources:
  default: file
  file:
    root: /tmp
cache:
  source_max_bytes: -1
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "cache.source_max_bytes") {
		t.Fatalf("err = %v, want cache.source_max_bytes validation error", err)
	}
}

func TestLoadRejectsNegativeVipsConcurrency(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
vips:
  concurrency: -1
sources:
  default: file
  file:
    root: /tmp
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "vips.concurrency") {
		t.Fatalf("err = %v, want vips.concurrency validation error", err)
	}
}
