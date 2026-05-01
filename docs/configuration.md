# Configuration

Triplet is configured by a single YAML file. See
[`config.example.yaml`](https://github.com/libops/triplet/blob/main/config.example.yaml)
for the full surface.
Environment variables can be referenced in YAML:

```yaml
server:
  public_base_url: "${TRIPLET_PUBLIC_BASE_URL}"
```

## Server

The server section controls the HTTP listener and the public URL used to build
canonical IIIF identifiers.

```yaml
server:
  listen: ":8080"
  read_timeout: 60s
  write_timeout: 5m
  public_base_url: "${TRIPLET_PUBLIC_BASE_URL}"
```

## Logging and metrics

Triplet can emit structured logs and expose Prometheus metrics on the main
listener. Keep `/metrics` behind a private scrape path when enabled.

```yaml
logging:
  level: info
  format: json
  trusted_proxy_cidrs:
  # - 10.0.0.0/8

metrics:
  enabled: false
```

## IIIF services

The Image API is enabled by default. Presentation, Search, and Auth are
separate surfaces and can be enabled independently.

```yaml
iiif:
  allowed_origins:
    - https://viewer.example.edu
  image:
    enabled: true
    prefix: /iiif/3
  presentation:
    enabled: false
    prefix: /presentation/v3
  search:
    enabled: false
    prefix: /search/v2
  auth:
    enabled: false
    prefix: /auth/v2
```

## Image limits

These limits bound libvips request work and protect public deployments from
oversized source images or derivatives.

```yaml
iiif:
  image:
    max_output_pixels: 100000000
    allow_unsafe_unlimited_output_pixels: false
    max_source_pixels: 250000000
    max_source_bytes: 1073741824
    max_derivative_bytes: 536870912
    max_concurrent_transforms: 4
    max_width: 0
    max_height: 0
    color_management: preserve
    load_access: auto
    info_dimension_cache: true
```

## Source selection

Exactly one source is the default. Additional sources are selected by identifier
scheme, such as `https://...` or `gs://...`.

```yaml
sources:
  default: file
  file:
    root: ./testdata/images
  http:
    allowed_origins:
      - https://repository.example.edu
    allow_private_hosts: false
    request_timeout: 2m
    max_bytes: 52428800
  gcs:
    # Implemented, but not deployment-tested yet.
    bucket_url: gs://my-bucket
    prefix: images
```

## Local URL mappings

Local URL mappings are useful for distributed deployments where Drupal or Fedora
URLs and Triplet can see the same filesystems. Triplet strips the configured URL
path prefix, checks the mapped root on disk, and falls back to HTTP streaming on
a miss.

For example, `/system/files` can map to `/private`, while
`/_flysystem/fedora` can map to an OCFL root. Path-only mappings are scoped by
`sources.http.allowed_origins`.

```yaml
sources:
  file:
    root: ./testdata/images
    url_mappings:
      - prefix: /sites/default/files
        root: /public
      - prefix: /system/files
        root: /private
        auth_probe: true
        auth_anonymous_cache_ttl: 720h
        auth_authenticated_cache_ttl: 168h
        auth_error_cache_min_age: 5m
        auth_cache_max_entries: 4096
      - prefix: /fedora
        root: /fcrepo
        ocfl: true
        auth_probe: true
```

For protected paths, `auth_probe: true` asks the original Drupal URL for
authorization before serving the local file. Anonymous and credentialed probe
results are cached separately for short, configurable TTLs.

Negative auth-probe caching is intentionally conservative: Triplet does not
cache 5xx results, and it skips 403 or 404 cache writes when the probe response
says the object was modified within `auth_error_cache_min_age`.

## HTTP source boundary

When using HTTP identifiers, Triplet treats the identifier as a source URL and
fetches it before passing bytes to libvips.

The HTTP host allowlist prevents arbitrary URL fetches, constrains redirect
targets, and keeps the native image parser surface limited to trusted
repositories. Keep `sources.http.allowed_origins` to the exact upstream origins
Triplet is allowed to fetch, including scheme and port when needed.

An empty list denies all HTTP sources and wildcards are rejected. Private,
loopback, link-local, and metadata addresses are blocked unless
`sources.http.allow_private_hosts` is explicitly enabled. When private hosts are
blocked, Triplet resolves the hostname once and connects only to a verified
public IP, so DNS rebinding cannot swap the connection target after validation.

```yaml
sources:
  http:
    allowed_origins:
      - https://repository.example.edu
    allow_private_hosts: false
    request_timeout: 2m
    max_bytes: 52428800
```
