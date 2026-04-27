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
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of the YAML document.
type Config struct {
	Server     Server     `yaml:"server"`
	Debug      Debug      `yaml:"debug"`
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

// Debug controls optional diagnostic endpoints.
type Debug struct {
	PprofEnabled bool   `yaml:"pprof_enabled"`
	PprofPrefix  string `yaml:"pprof_prefix"`
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
	AllowedOrigins []string     `yaml:"allowed_origins"`
	Image          Image        `yaml:"image"`
	Presentation   Presentation `yaml:"presentation"`
	Search         Search       `yaml:"search"`
	Auth           Auth         `yaml:"auth"`
}

// Image holds Image API 3.0 settings.
type Image struct {
	Enabled                          bool     `yaml:"enabled"`
	Prefix                           string   `yaml:"prefix"`
	AllowedOrigins                   []string `yaml:"allowed_origins"`
	MaxOutputPixels                  int64    `yaml:"max_output_pixels"`
	AllowUnsafeUnlimitedOutputPixels bool     `yaml:"allow_unsafe_unlimited_output_pixels"`
	MaxSourcePixels                  int64    `yaml:"max_source_pixels"`
	MaxSourceBytes                   int64    `yaml:"max_source_bytes"`
	MaxDerivativeBytes               int64    `yaml:"max_derivative_bytes"`
	MaxConcurrentTransforms          int      `yaml:"max_concurrent_transforms"`
	MaxWidth                         int      `yaml:"max_width"`
	MaxHeight                        int      `yaml:"max_height"`
	ColorManagement                  string   `yaml:"color_management"`
	LoadAccess                       string   `yaml:"load_access"`
	InfoDimensionCache               *bool    `yaml:"info_dimension_cache"`
}

// Presentation holds Presentation API 3.0 settings (milestone 2).
type Presentation struct {
	Enabled      bool   `yaml:"enabled"`
	Prefix       string `yaml:"prefix"`
	Root         string `yaml:"root"`
	DSN          string `yaml:"dsn"`
	WriteEnabled bool   `yaml:"write_enabled"`
	WriteToken   string `yaml:"write_token"`
}

// Search holds Content Search API 2.0 settings.
type Search struct {
	Enabled bool   `yaml:"enabled"`
	Prefix  string `yaml:"prefix"`
}

// Auth holds Authorization Flow API 2.0 settings.
type Auth struct {
	Enabled              bool   `yaml:"enabled"`
	Prefix               string `yaml:"prefix"`
	DevelopmentPermitAll bool   `yaml:"development_permit_all"`
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
	AllowedHosts      []string      `yaml:"allowed_hosts"`
	AllowPrivateHosts bool          `yaml:"allow_private_hosts"`
	RequestTimeout    time.Duration `yaml:"request_timeout"`
	MaxBytes          int64         `yaml:"max_bytes"`
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
	expanded := os.ExpandEnv(string(b))
	explicitUnlimitedOutput, err := explicitZeroYAMLField([]byte(expanded), "iiif", "image", "max_output_pixels")
	if err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if explicitUnlimitedOutput && !c.IIIF.Image.AllowUnsafeUnlimitedOutputPixels {
		return nil, fmt.Errorf("validate config %q: iiif.image.max_output_pixels: must be > 0 unless iiif.image.allow_unsafe_unlimited_output_pixels = true", path)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("validate config %q: %w", path, err)
	}
	return &c, nil
}

func explicitZeroYAMLField(body []byte, path ...string) (bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(body, &root); err != nil {
		return false, err
	}
	node := &root
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return false, nil
		}
		node = node.Content[0]
	}
	for _, key := range path {
		if node.Kind != yaml.MappingNode {
			return false, nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				next = node.Content[i+1]
				break
			}
		}
		if next == nil {
			return false, nil
		}
		node = next
	}
	if node.Kind != yaml.ScalarNode {
		return false, nil
	}
	v, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil {
		return false, nil
	}
	return v == 0, nil
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
	if c.Debug.PprofPrefix == "" {
		c.Debug.PprofPrefix = "/debug/pprof"
	}
	if c.Vips.BlockUntrusted == nil {
		v := true
		c.Vips.BlockUntrusted = &v
	}
	if c.IIIF.Image.Prefix == "" {
		c.IIIF.Image.Prefix = "/iiif/3"
	}
	if c.IIIF.Image.MaxOutputPixels == 0 && !c.IIIF.Image.AllowUnsafeUnlimitedOutputPixels {
		c.IIIF.Image.MaxOutputPixels = 100_000_000
	}
	if c.IIIF.Image.MaxSourcePixels == 0 {
		c.IIIF.Image.MaxSourcePixels = 250_000_000
	}
	if c.IIIF.Image.MaxSourceBytes == 0 {
		c.IIIF.Image.MaxSourceBytes = 1 << 30
	}
	if c.IIIF.Image.MaxDerivativeBytes == 0 {
		c.IIIF.Image.MaxDerivativeBytes = 512 << 20
	}
	if c.IIIF.Image.MaxConcurrentTransforms == 0 {
		c.IIIF.Image.MaxConcurrentTransforms = max(1, min(runtime.GOMAXPROCS(0), 4))
	}
	if c.IIIF.Image.ColorManagement == "" {
		c.IIIF.Image.ColorManagement = "preserve"
	}
	if c.IIIF.Image.LoadAccess == "" {
		c.IIIF.Image.LoadAccess = "auto"
	}
	if c.IIIF.Image.InfoDimensionCache == nil {
		v := true
		c.IIIF.Image.InfoDimensionCache = &v
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
	u, err := url.Parse(c.Server.PublicBaseURL)
	if err != nil {
		return fmt.Errorf("server.public_base_url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("server.public_base_url: must be an absolute http(s) URL, got %q", c.Server.PublicBaseURL)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("server.public_base_url: must not include query or fragment")
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
	if !strings.HasPrefix(c.Debug.PprofPrefix, "/") {
		return fmt.Errorf("debug.pprof_prefix: must start with `/`, got %q", c.Debug.PprofPrefix)
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
	if err := validateAllowedOrigins("iiif.allowed_origins", c.IIIF.AllowedOrigins); err != nil {
		return err
	}
	if err := validateAllowedOrigins("iiif.image.allowed_origins", c.IIIF.Image.AllowedOrigins); err != nil {
		return err
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
	if c.IIIF.Auth.Enabled && !c.IIIF.Auth.DevelopmentPermitAll {
		return errors.New("iiif.auth.development_permit_all is required when iiif.auth.enabled = true")
	}
	if c.IIIF.Image.MaxOutputPixels < 0 {
		return errors.New("iiif.image.max_output_pixels: must be >= 0")
	}
	if c.IIIF.Image.MaxOutputPixels == 0 && !c.IIIF.Image.AllowUnsafeUnlimitedOutputPixels {
		return errors.New("iiif.image.max_output_pixels: must be > 0 unless iiif.image.allow_unsafe_unlimited_output_pixels = true")
	}
	if c.IIIF.Image.MaxSourcePixels < 0 {
		return errors.New("iiif.image.max_source_pixels: must be >= 0")
	}
	if c.IIIF.Image.MaxSourceBytes < 0 {
		return errors.New("iiif.image.max_source_bytes: must be >= 0")
	}
	if c.IIIF.Image.MaxDerivativeBytes < 0 {
		return errors.New("iiif.image.max_derivative_bytes: must be >= 0")
	}
	if c.IIIF.Image.MaxConcurrentTransforms < 1 {
		return errors.New("iiif.image.max_concurrent_transforms: must be >= 1")
	}
	if c.IIIF.Image.MaxWidth < 0 {
		return errors.New("iiif.image.max_width: must be >= 0")
	}
	if c.IIIF.Image.MaxHeight < 0 {
		return errors.New("iiif.image.max_height: must be >= 0")
	}
	switch c.IIIF.Image.ColorManagement {
	case "preserve", "normalize", "none":
	default:
		return fmt.Errorf("iiif.image.color_management: %q not one of preserve|normalize|none", c.IIIF.Image.ColorManagement)
	}
	switch c.IIIF.Image.LoadAccess {
	case "auto", "sequential", "random":
	default:
		return fmt.Errorf("iiif.image.load_access: %q not one of auto|sequential|random", c.IIIF.Image.LoadAccess)
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
	if c.IIIF.Image.Enabled {
		switch c.Sources.Default {
		case "":
			return errors.New("sources.default is required when iiif.image.enabled = true")
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
	if c.IIIF.Presentation.WriteEnabled && c.IIIF.Presentation.WriteToken == "" {
		return errors.New("iiif.presentation.write_token is required when iiif.presentation.write_enabled = true")
	}
	return nil
}

func validateAllowedOrigins(name string, origins []string) error {
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			return fmt.Errorf("%s: entries must not be empty", name)
		}
		if origin == "*" {
			continue
		}
		if strings.ContainsAny(origin, " \t\r\n") {
			return fmt.Errorf("%s: origin %q must not contain whitespace", name, origin)
		}
	}
	return nil
}
