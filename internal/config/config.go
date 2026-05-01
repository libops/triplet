// Package config loads and validates the triplet YAML configuration.
//
// Configuration is a single YAML file. There are no environment-variable
// overrides by design — render the file before launch if you need templating.
package config

import (
	"errors"
	"fmt"
	"net"
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
	Metrics    Metrics    `yaml:"metrics"`
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

const (
	DefaultServerReadTimeout  = 60 * time.Second
	DefaultServerWriteTimeout = 5 * time.Minute
)

// Logging controls slog setup.
type Logging struct {
	Level             string   `yaml:"level"`
	Format            string   `yaml:"format"`
	TrustedProxyCIDRs []string `yaml:"trusted_proxy_cidrs"`
}

// Debug controls optional diagnostic endpoints.
type Debug struct {
	PprofEnabled bool   `yaml:"pprof_enabled"`
	PprofPrefix  string `yaml:"pprof_prefix"`
	PprofToken   string `yaml:"pprof_token"`
}

// Metrics controls Prometheus endpoint exposure.
type Metrics struct {
	Enabled bool `yaml:"enabled"`
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
}

// Image holds Image API 3.0 settings.
type Image struct {
	Enabled                          bool     `yaml:"enabled"`
	Prefix                           string   `yaml:"prefix"`
	AllowedOrigins                   []string `yaml:"allowed_origins"`
	CacheInvalidationToken           string   `yaml:"cache_invalidation_token"`
	CacheInvalidationAllowedCIDRs    []string `yaml:"cache_invalidation_allowed_cidrs"`
	MaxOutputPixels                  int64    `yaml:"max_output_pixels"`
	AllowUnsafeUnlimitedOutputPixels bool     `yaml:"allow_unsafe_unlimited_output_pixels"`
	MaxSourcePixels                  int64    `yaml:"max_source_pixels"`
	MaxSourceBytes                   ByteSize `yaml:"max_source_bytes"`
	MaxDerivativeBytes               ByteSize `yaml:"max_derivative_bytes"`
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

// Sources declares identifier-resolution backends. Exactly one of the
// declared sources must match Default.
type Sources struct {
	Default string      `yaml:"default"`
	File    *FileSource `yaml:"file,omitempty"`
	HTTP    *HTTPSource `yaml:"http,omitempty"`
}

// FileSource resolves identifiers as paths under Root.
type FileSource struct {
	Root               string           `yaml:"root"`
	URLPrefixes        []string         `yaml:"url_prefixes"`
	URLPrefixesAreOCFL bool             `yaml:"url_prefixes_are_ocfl"`
	URLMappings        []FileURLMapping `yaml:"url_mappings"`
}

// FileURLMapping maps a URL identifier prefix to a local filesystem root.
type FileURLMapping struct {
	Prefix    string `yaml:"prefix"`
	Root      string `yaml:"root"`
	OCFL      bool   `yaml:"ocfl"`
	AuthProbe bool   `yaml:"auth_probe"`
}

// HTTPSource resolves identifiers that are HTTP(S) URLs.
type HTTPSource struct {
	AllowedOrigins    []string      `yaml:"allowed_origins"`
	AllowPrivateHosts bool          `yaml:"allow_private_hosts"`
	RequestTimeout    time.Duration `yaml:"request_timeout"`
	MaxBytes          ByteSize      `yaml:"max_bytes"`
	MetadataCacheTTL  time.Duration `yaml:"metadata_cache_ttl"`
}

// Cache declares optional derivative-cache settings.
type Cache struct {
	Root             string        `yaml:"root"`
	MaxBytes         ByteSize      `yaml:"max_bytes"`
	MaxAge           time.Duration `yaml:"max_age"`
	SourceRoot       string        `yaml:"source_root"`
	SourceMaxBytes   ByteSize      `yaml:"source_max_bytes"`
	SourceStaleAfter time.Duration `yaml:"source_stale_after"`
}

// Extensions enables non-spec endpoints.
type Extensions struct {
	Transform Transform `yaml:"transform"`
	Uploads   Uploads   `yaml:"uploads"`
}

// Transform configures the POST /v1/transform endpoint.
type Transform struct {
	Enabled        bool     `yaml:"enabled"`
	MaxUploadBytes ByteSize `yaml:"max_upload_bytes"`
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
	env, err := configEnv()
	if err != nil {
		return nil, fmt.Errorf("load image cache invalidation token file: %w", err)
	}
	expanded := os.Expand(string(b), env)
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

func configEnv() (func(string) string, error) {
	token, err := imageCacheInvalidationTokenEnv()
	if err != nil {
		return nil, err
	}
	return func(key string) string {
		if key == "TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN" && token != "" {
			return token
		}
		return os.Getenv(key)
	}, nil
}

func imageCacheInvalidationTokenEnv() (string, error) {
	const tokenEnv = "TRIPLET_IMAGE_CACHE_INVALIDATION_TOKEN"
	const fileEnv = tokenEnv + "_FILE"
	if token := os.Getenv(tokenEnv); token != "" {
		return token, nil
	}
	path := strings.TrimSpace(os.Getenv(fileEnv))
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileEnv, err)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
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
		c.Server.ReadTimeout = DefaultServerReadTimeout
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = DefaultServerWriteTimeout
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
	for _, cidr := range c.Logging.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("logging.trusted_proxy_cidrs: invalid CIDR %q: %w", cidr, err)
		}
	}
	if !strings.HasPrefix(c.Debug.PprofPrefix, "/") {
		return fmt.Errorf("debug.pprof_prefix: must start with `/`, got %q", c.Debug.PprofPrefix)
	}
	if c.Debug.PprofEnabled && c.Debug.PprofToken == "" {
		return errors.New("debug.pprof_token is required when debug.pprof_enabled = true")
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
	for _, cidr := range c.IIIF.Image.CacheInvalidationAllowedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("iiif.image.cache_invalidation_allowed_cidrs: invalid CIDR %q: %w", cidr, err)
		}
	}
	if !strings.HasPrefix(c.IIIF.Presentation.Prefix, "/") {
		return fmt.Errorf("iiif.presentation.prefix: must start with `/`, got %q", c.IIIF.Presentation.Prefix)
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
	if c.Cache.MaxAge < 0 {
		return errors.New("cache.max_age: must be >= 0")
	}
	if c.Cache.SourceMaxBytes < 0 {
		return errors.New("cache.source_max_bytes: must be >= 0")
	}
	if c.Cache.SourceStaleAfter < 0 {
		return errors.New("cache.source_stale_after: must be >= 0")
	}
	if c.Sources.HTTP != nil {
		if len(c.Sources.HTTP.AllowedOrigins) == 0 {
			return errors.New("sources.http.allowed_origins is required when sources.http is configured")
		}
		if err := validateHTTPSourceOrigins("sources.http.allowed_origins", c.Sources.HTTP.AllowedOrigins); err != nil {
			return err
		}
		if c.Sources.HTTP.MaxBytes < 0 {
			return errors.New("sources.http.max_bytes: must be >= 0")
		}
		if c.Sources.HTTP.MetadataCacheTTL < 0 {
			return errors.New("sources.http.metadata_cache_ttl: must be >= 0")
		}
	}
	if c.Sources.File != nil {
		for _, prefix := range c.Sources.File.URLPrefixes {
			if strings.TrimSpace(prefix) == "" {
				return errors.New("sources.file.url_prefixes: entries must not be empty")
			}
		}
		if len(c.Sources.File.URLPrefixes) > 0 && c.Sources.File.Root == "" {
			return errors.New("sources.file.root is required when sources.file.url_prefixes is configured")
		}
		if c.Sources.File.URLPrefixesAreOCFL && len(c.Sources.File.URLPrefixes) == 0 {
			return errors.New("sources.file.url_prefixes is required when sources.file.url_prefixes_are_ocfl = true")
		}
		if c.IIIF.Image.Enabled && (len(c.Sources.File.URLPrefixes) > 0 || len(c.Sources.File.URLMappings) > 0) && c.Sources.HTTP == nil {
			return errors.New("sources.http is required when sources.file URL mappings are configured")
		}
		for i, mapping := range c.Sources.File.URLMappings {
			if strings.TrimSpace(mapping.Prefix) == "" {
				return fmt.Errorf("sources.file.url_mappings[%d].prefix is required", i)
			}
			if mapping.Root == "" {
				return fmt.Errorf("sources.file.url_mappings[%d].root is required", i)
			}
		}
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
		default:
			return fmt.Errorf("sources.default: %q not supported in this build", c.Sources.Default)
		}
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
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%s: origin %q must be an absolute http(s) origin or *", name, origin)
		}
	}
	return nil
}

func validateHTTPSourceOrigins(name string, origins []string) error {
	if err := validateAllowedOrigins(name, origins); err != nil {
		return err
	}
	for _, origin := range origins {
		if strings.TrimSpace(origin) == "*" {
			return fmt.Errorf("%s: wildcard is not allowed; list exact trusted origins", name)
		}
	}
	return nil
}
