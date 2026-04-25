// Package config loads and validates the triplet YAML configuration.
//
// Configuration is a single YAML file. There are no environment-variable
// overrides by design — render the file before launch if you need templating.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of the YAML document.
type Config struct {
	Server     Server     `yaml:"server"`
	Logging    Logging    `yaml:"logging"`
	Vips       Vips       `yaml:"vips"`
	IIIF       IIIF       `yaml:"iiif"`
	Sources    Sources    `yaml:"sources"`
	Cache      Cache      `yaml:"cache"`
	Extensions Extensions `yaml:"extensions"`
}

// Server holds HTTP listener settings.
type Server struct {
	Listen        string        `yaml:"listen"`
	ReadTimeout   time.Duration `yaml:"read_timeout"`
	WriteTimeout  time.Duration `yaml:"write_timeout"`
	PublicBaseURL string        `yaml:"public_base_url"`
}

// Logging controls slog setup.
type Logging struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Vips controls libvips runtime initialization.
type Vips struct {
	Concurrency       int      `yaml:"concurrency"`
	CacheMaxMem       int      `yaml:"cache_max_mem"`
	CacheMaxFiles     int      `yaml:"cache_max_files"`
	ReportLeaks       bool     `yaml:"report_leaks"`
	BlockUntrusted    *bool    `yaml:"block_untrusted"`
	BlockedOperations []string `yaml:"blocked_operations"`
}

// IIIF groups per-API enablement and settings.
type IIIF struct {
	Image        Image        `yaml:"image"`
	Presentation Presentation `yaml:"presentation"`
	Search       Search       `yaml:"search"`
	Auth         Auth         `yaml:"auth"`
}

// Image holds Image API 3.0 settings.
type Image struct {
	Enabled         bool   `yaml:"enabled"`
	Prefix          string `yaml:"prefix"`
	MaxOutputPixels int64  `yaml:"max_output_pixels"`
	MaxWidth        int    `yaml:"max_width"`
	MaxHeight       int    `yaml:"max_height"`
}

// Presentation holds Presentation API 3.0 settings (milestone 2).
type Presentation struct {
	Enabled bool   `yaml:"enabled"`
	Prefix  string `yaml:"prefix"`
	Root    string `yaml:"root"`
	DSN     string `yaml:"dsn"`
}

// Search holds Content Search API 2.0 settings.
type Search struct {
	Enabled bool   `yaml:"enabled"`
	Prefix  string `yaml:"prefix"`
}

// Auth holds Authorization Flow API 2.0 settings.
type Auth struct {
	Enabled bool   `yaml:"enabled"`
	Prefix  string `yaml:"prefix"`
}

// Sources declares identifier-resolution backends. Exactly one of the
// declared sources must match Default.
type Sources struct {
	Default string      `yaml:"default"`
	File    *FileSource `yaml:"file,omitempty"`
	HTTP    *HTTPSource `yaml:"http,omitempty"`
	GCS     *GCSSource  `yaml:"gcs,omitempty"`
}

// FileSource resolves identifiers as paths under Root.
type FileSource struct {
	Root string `yaml:"root"`
}

// HTTPSource resolves identifiers that are HTTP(S) URLs.
type HTTPSource struct {
	AllowedHosts   []string      `yaml:"allowed_hosts"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
	MaxBytes       int64         `yaml:"max_bytes"`
}

// GCSSource resolves identifiers as object keys in a GCS bucket.
type GCSSource struct {
	BucketURL string `yaml:"bucket_url"`
	Prefix    string `yaml:"prefix"`
}

// Cache declares optional derivative-cache settings.
type Cache struct {
	Root             string        `yaml:"root"`
	MaxBytes         int64         `yaml:"max_bytes"`
	BucketURL        string        `yaml:"bucket_url"`
	Prefix           string        `yaml:"prefix"`
	SourceRoot       string        `yaml:"source_root"`
	SourceMaxBytes   int64         `yaml:"source_max_bytes"`
	SourceBucketURL  string        `yaml:"source_bucket_url"`
	SourcePrefix     string        `yaml:"source_prefix"`
	SourceStaleAfter time.Duration `yaml:"source_stale_after"`
}

// Extensions enables non-spec endpoints.
type Extensions struct {
	Transform Transform `yaml:"transform"`
	Uploads   Uploads   `yaml:"uploads"`
}

// Transform configures the POST /v1/transform endpoint.
type Transform struct {
	Enabled        bool  `yaml:"enabled"`
	MaxUploadBytes int64 `yaml:"max_upload_bytes"`
}

// Uploads configures the POST /v1/uploads endpoint.
type Uploads struct {
	Enabled bool `yaml:"enabled"`
}

// Load reads and validates a YAML configuration file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("validate config %q: %w", path, err)
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8080"
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 30 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 120 * time.Second
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.Vips.BlockUntrusted == nil {
		v := true
		c.Vips.BlockUntrusted = &v
	}
	if c.IIIF.Image.Prefix == "" {
		c.IIIF.Image.Prefix = "/iiif/3"
	}
	if c.IIIF.Presentation.Prefix == "" {
		c.IIIF.Presentation.Prefix = "/presentation/v3"
	}
	if c.IIIF.Search.Prefix == "" {
		c.IIIF.Search.Prefix = "/search/v2"
	}
	if c.IIIF.Auth.Prefix == "" {
		c.IIIF.Auth.Prefix = "/auth/v2"
	}
}

func (c *Config) validate() error {
	if c.Server.PublicBaseURL == "" {
		return errors.New("server.public_base_url is required")
	}
	if _, err := url.Parse(c.Server.PublicBaseURL); err != nil {
		return fmt.Errorf("server.public_base_url: %w", err)
	}
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level: %q not one of debug|info|warn|error", c.Logging.Level)
	}
	switch c.Logging.Format {
	case "json", "text":
	default:
		return fmt.Errorf("logging.format: %q not one of json|text", c.Logging.Format)
	}
	if c.Vips.Concurrency < 0 {
		return errors.New("vips.concurrency: must be >= 0")
	}
	if c.Vips.CacheMaxMem < 0 {
		return errors.New("vips.cache_max_mem: must be >= 0")
	}
	if c.Vips.CacheMaxFiles < 0 {
		return errors.New("vips.cache_max_files: must be >= 0")
	}
	for _, op := range c.Vips.BlockedOperations {
		if strings.TrimSpace(op) == "" {
			return errors.New("vips.blocked_operations: entries must not be empty")
		}
		if strings.ContainsAny(op, " \t\r\n") {
			return fmt.Errorf("vips.blocked_operations: operation %q must not contain whitespace", op)
		}
	}
	if !strings.HasPrefix(c.IIIF.Image.Prefix, "/") {
		return fmt.Errorf("iiif.image.prefix: must start with `/`, got %q", c.IIIF.Image.Prefix)
	}
	if !strings.HasPrefix(c.IIIF.Presentation.Prefix, "/") {
		return fmt.Errorf("iiif.presentation.prefix: must start with `/`, got %q", c.IIIF.Presentation.Prefix)
	}
	if !strings.HasPrefix(c.IIIF.Search.Prefix, "/") {
		return fmt.Errorf("iiif.search.prefix: must start with `/`, got %q", c.IIIF.Search.Prefix)
	}
	if !strings.HasPrefix(c.IIIF.Auth.Prefix, "/") {
		return fmt.Errorf("iiif.auth.prefix: must start with `/`, got %q", c.IIIF.Auth.Prefix)
	}
	if c.IIIF.Image.MaxOutputPixels < 0 {
		return errors.New("iiif.image.max_output_pixels: must be >= 0")
	}
	if c.IIIF.Image.MaxWidth < 0 {
		return errors.New("iiif.image.max_width: must be >= 0")
	}
	if c.IIIF.Image.MaxHeight < 0 {
		return errors.New("iiif.image.max_height: must be >= 0")
	}
	if c.Cache.MaxBytes < 0 {
		return errors.New("cache.max_bytes: must be >= 0")
	}
	if c.Cache.SourceMaxBytes < 0 {
		return errors.New("cache.source_max_bytes: must be >= 0")
	}
	if c.Cache.SourceStaleAfter < 0 {
		return errors.New("cache.source_stale_after: must be >= 0")
	}
	if c.Sources.HTTP != nil {
		if len(c.Sources.HTTP.AllowedHosts) == 0 {
			return errors.New("sources.http.allowed_hosts is required when sources.http is configured")
		}
		if c.Sources.HTTP.MaxBytes < 0 {
			return errors.New("sources.http.max_bytes: must be >= 0")
		}
	}
	if c.Sources.GCS != nil && c.Sources.GCS.BucketURL == "" {
		return errors.New("sources.gcs.bucket_url is required when sources.gcs is configured")
	}
	switch c.Sources.Default {
	case "":
		return errors.New("sources.default is required")
	case "file":
		if c.Sources.File == nil || c.Sources.File.Root == "" {
			return errors.New("sources.file.root is required when sources.default = file")
		}
	case "http":
		if c.Sources.HTTP == nil {
			return errors.New("sources.http is required when sources.default = http")
		}
	case "gcs":
		if c.Sources.GCS == nil || c.Sources.GCS.BucketURL == "" {
			return errors.New("sources.gcs.bucket_url is required when sources.default = gcs")
		}
	default:
		return fmt.Errorf("sources.default: %q not supported in this build", c.Sources.Default)
	}
	if c.Cache.Root != "" && c.Cache.BucketURL != "" {
		return errors.New("cache.root and cache.bucket_url are mutually exclusive")
	}
	if c.Cache.SourceRoot != "" && c.Cache.SourceBucketURL != "" {
		return errors.New("cache.source_root and cache.source_bucket_url are mutually exclusive")
	}
	if c.IIIF.Presentation.Enabled && c.IIIF.Presentation.Root == "" && c.IIIF.Presentation.DSN == "" {
		return errors.New("iiif.presentation.root or iiif.presentation.dsn is required when iiif.presentation.enabled = true")
	}
	if c.IIIF.Presentation.Root != "" && c.IIIF.Presentation.DSN != "" {
		return errors.New("iiif.presentation.root and iiif.presentation.dsn are mutually exclusive")
	}
	return nil
}
