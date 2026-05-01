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
iiif:
  image:
    enabled: true
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
iiif:
  image:
    enabled: true
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
    allowed_origins: [https://example.org]
    max_bytes: 1048576
`,
		},
		{
			name: "file url prefixes valid",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  file:
    root: /tmp
    url_prefixes:
      - https://repo.example.edu/system/files
    url_prefixes_are_ocfl: true
  http:
    allowed_origins: [https://repo.example.edu]
`,
		},
		{
			name: "file url mappings valid",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  file:
    url_mappings:
      - prefix: https://repo.example.edu/system/files
        root: /mnt/foo
      - prefix: https://repo.example.edu/fedora
        root: /bar
        ocfl: true
        auth_probe: true
        auth_anonymous_cache_ttl: 720h
        auth_authenticated_cache_ttl: 168h
        auth_error_cache_min_age: 5m
        auth_cache_max_entries: 4096
  http:
    allowed_origins: [https://repo.example.edu]
`,
		},
		{
			name: "file url mapping requires root",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  file:
    url_mappings:
      - prefix: https://repo.example.edu/system/files
  http:
    allowed_origins: [https://repo.example.edu]
`,
			wantErr: "sources.file.url_mappings[0].root is required",
		},
		{
			name: "file url mapping rejects negative auth ttl",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  file:
    url_mappings:
      - prefix: https://repo.example.edu/system/files
        root: /tmp
        auth_probe: true
        auth_cache_ttl: -1s
  http:
    allowed_origins: [https://repo.example.edu]
`,
			wantErr: "sources.file.url_mappings[0].auth_cache_ttl",
		},
		{
			name: "file url mapping rejects negative authenticated auth ttl",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  file:
    url_mappings:
      - prefix: https://repo.example.edu/system/files
        root: /tmp
        auth_probe: true
        auth_authenticated_cache_ttl: -1s
  http:
    allowed_origins: [https://repo.example.edu]
`,
			wantErr: "sources.file.url_mappings[0].auth_authenticated_cache_ttl",
		},
		{
			name: "file url mapping rejects negative auth error cache min age",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  file:
    url_mappings:
      - prefix: https://repo.example.edu/system/files
        root: /tmp
        auth_probe: true
        auth_error_cache_min_age: -1s
  http:
    allowed_origins: [https://repo.example.edu]
`,
			wantErr: "sources.file.url_mappings[0].auth_error_cache_min_age",
		},
		{
			name: "file url mapping rejects negative auth max entries",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  file:
    url_mappings:
      - prefix: https://repo.example.edu/system/files
        root: /tmp
        auth_probe: true
        auth_cache_max_entries: -1
  http:
    allowed_origins: [https://repo.example.edu]
`,
			wantErr: "sources.file.url_mappings[0].auth_cache_max_entries",
		},
		{
			name: "file url mapping requires http source",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    enabled: true
sources:
  default: file
  file:
    root: /tmp
    url_mappings:
      - prefix: https://repo.example.edu/system/files
        root: /tmp
`,
			wantErr: "sources.http is required when sources.file URL mappings are configured",
		},
		{
			name: "file url prefix requires root",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  file:
    url_prefixes:
      - https://repo.example.edu/system/files
  http:
    allowed_origins: [https://repo.example.edu]
`,
			wantErr: "sources.file.root is required when sources.file.url_prefixes is configured",
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
      - https://viewer2.example.edu
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
iiif:
  image:
    enabled: true
sources:
  default: file
`,
			wantErr: "sources.file.root is required",
		},
		{
			name: "http source missing origins",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  http: {}
`,
			wantErr: "sources.http.allowed_origins is required",
		},
		{
			name: "http source rejects wildcard origin",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  http:
    allowed_origins: ["*"]
`,
			wantErr: "sources.http.allowed_origins",
		},
		{
			name: "http source rejects bare host origin",
			body: `
server:
  public_base_url: http://localhost:8080
sources:
  default: http
  http:
    allowed_origins: [example.org]
`,
			wantErr: "sources.http.allowed_origins",
		},
		{
			name: "pprof enabled requires token",
			body: `
server:
  public_base_url: http://localhost:8080
debug:
  pprof_enabled: true
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "debug.pprof_token is required",
		},
		{
			name: "trusted proxy cidr invalid",
			body: `
server:
  public_base_url: http://localhost:8080
logging:
  trusted_proxy_cidrs: [not-a-cidr]
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "logging.trusted_proxy_cidrs",
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
			name: "presentation only does not require image source",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    enabled: false
  presentation:
    enabled: true
    root: /tmp
`,
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
			name: "image cache invalidation cidr invalid",
			body: `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    cache_invalidation_allowed_cidrs: [not-a-cidr]
sources:
  default: file
  file:
    root: /tmp
`,
			wantErr: "iiif.image.cache_invalidation_allowed_cidrs",
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

func TestLoadImageCacheInvalidationTokenFromFile(t *testing.T) {
	t.Setenv("TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN", "")
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "image-cache-token")
	if err := os.WriteFile(tokenPath, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN_FILE", tokenPath)
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    cache_invalidation_token: ${TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN}
sources:
  default: file
  file:
    root: /tmp
`)

	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.IIIF.Image.CacheInvalidationToken; got != "file-token" {
		t.Fatalf("cache_invalidation_token = %q", got)
	}
	if got := os.Getenv("TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN"); got != "" {
		t.Fatalf("TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN was mutated to %q", got)
	}
}

func TestLoadImageCacheInvalidationTokenEnvOverridesFile(t *testing.T) {
	t.Setenv("TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN", "env-token")
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "image-cache-token")
	if err := os.WriteFile(tokenPath, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN_FILE", tokenPath)
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    cache_invalidation_token: ${TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN}
sources:
  default: file
  file:
    root: /tmp
`)

	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.IIIF.Image.CacheInvalidationToken; got != "env-token" {
		t.Fatalf("cache_invalidation_token = %q", got)
	}
}

func TestLoadImageCacheInvalidationTokenFileMissing(t *testing.T) {
	t.Setenv("TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN", "")
	t.Setenv("TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
sources:
  default: file
  file:
    root: /tmp
`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN_FILE") {
		t.Fatalf("err = %v, want token file error", err)
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
	if c.Server.ReadTimeout != DefaultServerReadTimeout {
		t.Errorf("ReadTimeout default = %s", c.Server.ReadTimeout)
	}
	if c.Server.WriteTimeout != DefaultServerWriteTimeout {
		t.Errorf("WriteTimeout default = %s", c.Server.WriteTimeout)
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
	if c.Logging.Level != "info" {
		t.Errorf("Logging.Level default = %q", c.Logging.Level)
	}
	if c.Metrics.Enabled {
		t.Errorf("Metrics.Enabled default = %v", c.Metrics.Enabled)
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

func TestLoadParsesHumanReadableByteSizes(t *testing.T) {
	path := writeConfig(t, `
server:
  public_base_url: http://localhost:8080
iiif:
  image:
    max_source_bytes: 1GiB
    max_derivative_bytes: 512MiB
sources:
  default: http
  http:
    allowed_origins: [https://example.org]
    max_bytes: 50MiB
cache:
  max_bytes: 500GiB
  source_max_bytes: 2GB
extensions:
  transform:
    max_upload_bytes: 25MiB
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.IIIF.Image.MaxSourceBytes != 1<<30 {
		t.Errorf("MaxSourceBytes = %d", c.IIIF.Image.MaxSourceBytes)
	}
	if c.IIIF.Image.MaxDerivativeBytes != 512<<20 {
		t.Errorf("MaxDerivativeBytes = %d", c.IIIF.Image.MaxDerivativeBytes)
	}
	if c.Sources.HTTP == nil || c.Sources.HTTP.MaxBytes != 50<<20 {
		t.Errorf("HTTP.MaxBytes = %#v", c.Sources.HTTP)
	}
	if c.Cache.MaxBytes != 500<<30 {
		t.Errorf("Cache.MaxBytes = %d", c.Cache.MaxBytes)
	}
	if c.Cache.SourceMaxBytes != 2_000_000_000 {
		t.Errorf("Cache.SourceMaxBytes = %d", c.Cache.SourceMaxBytes)
	}
	if c.Extensions.Transform.MaxUploadBytes != 25<<20 {
		t.Errorf("Transform.MaxUploadBytes = %d", c.Extensions.Transform.MaxUploadBytes)
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
    allowed_origins: [https://example.org]
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
