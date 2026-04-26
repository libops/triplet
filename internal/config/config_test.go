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
			name: "relative public_base_url rejected",
			body: `
server:
  public_base_url: example
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "server.public_base_url",
		},
		{
			name: "public_base_url query rejected",
			body: `
server:
  public_base_url: https://example.org/iiif?token=secret
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "server.public_base_url",
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
			name: "image allowed origins valid",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  allowed_origins:
    - https://global-viewer.example.edu
  image:
    allowed_origins:
      - https://viewer.example.edu
      - viewer2.example.edu
      - "*"
sources:
  default: file
  file:
    root: /tmp
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
			wantErr: "iiif.presentation.root or iiif.presentation.dsn is required",
		},
		{
			name: "presentation root and dsn conflict",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  presentation:
    enabled: true
    root: /tmp
    dsn: scribe:scribe@tcp(mariadb:3306)/scribe?parseTime=true
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "iiif.presentation.root and iiif.presentation.dsn are mutually exclusive",
		},
		{
			name: "presentation write enabled requires token",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  presentation:
    enabled: true
    root: /tmp
    write_enabled: true
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "iiif.presentation.write_token is required",
		},
		{
			name: "auth enabled requires permit-all opt-in",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  auth:
    enabled: true
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "iiif.auth.development_permit_all is required",
		},
		{
			name: "auth enabled with development permit-all opt-in",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  auth:
    enabled: true
    development_permit_all: true
sources:
  default: file
  file:
    root: /tmp
`,
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
		{
			name: "image allowed origins rejects empty entry",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    allowed_origins: [""]
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "iiif.image.allowed_origins",
		},
		{
			name: "explicit unlimited output pixels requires unsafe opt-in",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    max_output_pixels: 0
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "iiif.image.max_output_pixels",
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
	if c.IIIF.Image.MaxOutputPixels != 100_000_000 {
		t.Errorf("Image.MaxOutputPixels default = %d", c.IIIF.Image.MaxOutputPixels)
	}
	if c.IIIF.Image.MaxSourcePixels != 250_000_000 {
		t.Errorf("Image.MaxSourcePixels default = %d", c.IIIF.Image.MaxSourcePixels)
	}
	if c.IIIF.Image.MaxDerivativeBytes != 512<<20 {
		t.Errorf("Image.MaxDerivativeBytes default = %d", c.IIIF.Image.MaxDerivativeBytes)
	}
	if c.IIIF.Image.MaxConcurrentTransforms < 1 {
		t.Errorf("Image.MaxConcurrentTransforms default = %d", c.IIIF.Image.MaxConcurrentTransforms)
	}
	if len(c.IIIF.Image.AllowedOrigins) != 0 {
		t.Errorf("Image.AllowedOrigins default = %#v", c.IIIF.Image.AllowedOrigins)
	}
	if len(c.IIIF.AllowedOrigins) != 0 {
		t.Errorf("IIIF.AllowedOrigins default = %#v", c.IIIF.AllowedOrigins)
	}
	if c.IIIF.Search.Prefix != "/search/v2" {
		t.Errorf("Search.Prefix default = %q", c.IIIF.Search.Prefix)
	}
	if c.IIIF.Auth.Prefix != "/auth/v2" {
		t.Errorf("Auth.Prefix default = %q", c.IIIF.Auth.Prefix)
	}
	if c.Logging.Level != "info" {
		t.Errorf("Logging.Level default = %q", c.Logging.Level)
	}
	if c.Vips.BlockUntrusted == nil || !*c.Vips.BlockUntrusted {
		t.Errorf("Vips.BlockUntrusted default = %#v", c.Vips.BlockUntrusted)
	}
}

func TestLoadExpandsEnvironmentVariables(t *testing.T) {
	t.Setenv("TRIPLET_PUBLIC_BASE_URL", "https://iiif.example.org")
	t.Setenv("TRIPLET_IMAGE_ROOT", "/srv/images")
	t.Setenv("TRIPLET_CACHE_MAX_BYTES", "2048")

	path := writeConfig(t, `
server:
  public_base_url: ${TRIPLET_PUBLIC_BASE_URL}
sources:
  default: file
  file:
    root: $TRIPLET_IMAGE_ROOT
cache:
  max_bytes: ${TRIPLET_CACHE_MAX_BYTES}
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Server.PublicBaseURL != "https://iiif.example.org" {
		t.Errorf("PublicBaseURL = %q", c.Server.PublicBaseURL)
	}
	if c.Sources.File == nil || c.Sources.File.Root != "/srv/images" {
		t.Errorf("Sources.File.Root = %#v", c.Sources.File)
	}
	if c.Cache.MaxBytes != 2048 {
		t.Errorf("Cache.MaxBytes = %d", c.Cache.MaxBytes)
	}
}

func TestLoadRejectsBadSearchPrefix(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
iiif:
  search:
    prefix: search/v2
sources:
  default: file
  file:
    root: /tmp
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "iiif.search.prefix") {
		t.Fatalf("err = %v, want iiif.search.prefix validation error", err)
	}
}

func TestLoadRejectsBadAuthPrefix(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
iiif:
  auth:
    prefix: auth/v2
sources:
  default: file
  file:
    root: /tmp
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "iiif.auth.prefix") {
		t.Fatalf("err = %v, want iiif.auth.prefix validation error", err)
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

func TestLoadAllowsExplicitUnsafeUnlimitedOutputPixels(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    max_output_pixels: 0
    allow_unsafe_unlimited_output_pixels: true
sources:
  default: file
  file:
    root: /tmp
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.IIIF.Image.MaxOutputPixels != 0 {
		t.Fatalf("MaxOutputPixels = %d, want unlimited", c.IIIF.Image.MaxOutputPixels)
	}
}

func TestLoadRejectsBadImageCgoLimits(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "negative source pixels",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    max_source_pixels: -1
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "iiif.image.max_source_pixels",
		},
		{
			name: "negative derivative bytes",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    max_derivative_bytes: -1
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "iiif.image.max_derivative_bytes",
		},
		{
			name: "negative concurrent transforms",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    max_concurrent_transforms: -1
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "iiif.image.max_concurrent_transforms",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %s", err, tc.wantErr)
			}
		})
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

func TestLoadRejectsNegativeSourceStaleAfter(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
sources:
  default: file
  file:
    root: /tmp
cache:
  source_stale_after: -1s
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "cache.source_stale_after") {
		t.Fatalf("err = %v, want cache.source_stale_after validation error", err)
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

func TestLoadRejectsBadVipsBlockedOperation(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
vips:
  blocked_operations: ["VipsForeignLoad Pdf"]
sources:
  default: file
  file:
    root: /tmp
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "vips.blocked_operations") {
		t.Fatalf("err = %v, want vips.blocked_operations validation error", err)
	}
}
