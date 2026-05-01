# Configuration

Triplet is configured by a single YAML file. See
[`config.example.yaml`](https://github.com/libops/triplet/blob/main/config.example.yaml)
for the full surface.
Environment variables can be referenced in YAML:

```yaml
server:
  public_base_url: "${TRIPLET_PUBLIC_BASE_URL}"
```

Byte-size fields accept either raw bytes or unit strings such as `50MiB`,
`1GiB`, and `500GiB`. Binary units use powers of 1024 (`KiB`, `MiB`, `GiB`);
decimal units use powers of 1000 (`KB`, `MB`, `GB`).

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

The Image API is enabled by default. Presentation is a separate surface and can
be enabled independently.

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
```

When Presentation is enabled, Triplet exposes `ETag` through CORS so browser
annotation editors can read it and send optimistic-concurrency writes with
`If-Match`.

## Image safety limits

These limits bound libvips request work and protect public deployments from
oversized source images, oversized decoded outputs, and unexpectedly large
encoded responses.

```yaml
iiif:
  image:
    max_output_pixels: 100000000
    allow_unsafe_unlimited_output_pixels: false
    max_source_pixels: 250000000
    max_source_bytes: 1GiB
    max_derivative_bytes: 512MiB
```

`max_output_pixels` is the decoded derivative size limit after the IIIF region,
size, and rotation parameters are applied. It is cheap protection against
requests that expand a source into a very large output. The default is
100,000,000 pixels. Setting it to `0` is only accepted when
`allow_unsafe_unlimited_output_pixels: true`; do not enable unlimited output
pixels for public HTTP deployments.

`max_source_pixels` rejects sources whose decoded width multiplied by height is
too large before Triplet transforms them. The default is 250,000,000 pixels.
`max_source_bytes` applies while Triplet is reading an encoded source that is
not already available as a local file path.

`max_derivative_bytes` is a per-request encoded response limit. It applies after
libvips export and prevents returning or caching one unexpectedly large
derivative response. It is not the total derivative cache budget; `cache.max_bytes`
controls the aggregate filesystem cache footprint.

## Image processing

These settings tune how much image work Triplet runs concurrently and how
libvips reads and color-manages source images.

```yaml
iiif:
  image:
    max_concurrent_transforms: 4
    color_management: preserve
    load_access: auto
```

`max_concurrent_transforms` bounds concurrent libvips jobs across image
derivatives and `info.json` probes. When omitted, Triplet uses the smaller of
`GOMAXPROCS` and 4, with a minimum of 1.

`color_management` controls whether Triplet preserves the source image's color
profile or normalizes pixels before encoding the derivative:

| Value | Behavior | Implications |
|---|---|---|
| `preserve` | Default. Leaves the image in its current color space and keeps embedded metadata and ICC profiles where the output codec supports them. | Best match for preservation-oriented repositories and common Cantaloupe behavior. It avoids extra color conversion work and keeps profile information available to color-managed viewers, but clients that ignore embedded profiles may display wide-gamut, CMYK, or otherwise non-sRGB material differently. |
| `normalize` | Optimizes the embedded ICC profile, then converts supported non-sRGB color images to sRGB. Grayscale images remain grayscale. Exported derivatives strip metadata where the codec supports stripping. | Best for web-oriented delivery when predictable browser display is more important than retaining source profiles. It can change pixel values by design, adds conversion cost, and may remove metadata/profiles from derivatives. |
| `none` | Does not convert color space and asks the encoder to strip metadata where supported. | Best when derivatives should avoid metadata/profile retention but you do not want Triplet to alter pixel values through color conversion. Non-sRGB images remain non-sRGB, so display still depends on client interpretation. |

`load_access` controls how libvips reads pixels from disk or spooled source
files:

| Value | Behavior | When to use |
|---|---|---|
| `auto` | Default. Uses random access for region crops and sequential access for full-image or resize requests. | Best production default for mixed IIIF viewer traffic. |
| `sequential` | Streams source pixels forward. | Useful for profiling whole-image derivatives or source formats where sequential reads are materially cheaper. Poor fit for tile-heavy crop workloads. |
| `random` | Allows libvips to seek around the source. | Useful for profiling tile and region workloads. Can do unnecessary work for simple full-image derivatives. |

## Advertised image limits

These fields do not enforce additional server-side work limits. They advertise
client-facing size limits in Image API `info.json` so viewers can avoid sending
requests Triplet does not want to serve.

```yaml
iiif:
  image:
    max_width: 0
    max_height: 0
```

`0` omits the corresponding field from `info.json`.

## Caching

Cache-related settings, including derivative caches, source caches,
`info_dimension_cache`, local URL auth-probe caching, and libvips operation
caches are covered in [Caching](caching.md).

## Source selection

Exactly one source is the default. HTTP sources are selected by URL identifier
schemes such as `https://...`.

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
    max_bytes: 50MiB
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
      - prefix: /fedora
        root: /fcrepo
        ocfl: true
        auth_probe: true
```

For protected paths, `auth_probe: true` asks the original Drupal URL for
authorization before serving the local file. Anonymous and credentialed probe
results are cached separately; see [Authorization](authorization.md) for
TTL behavior and invalidation.

## HTTP source boundary

When using HTTP identifiers, Triplet treats the identifier as a source URL and
fetches it before passing bytes to libvips.

```yaml
sources:
  http:
    allowed_origins:
      - https://repository.example.edu
    allow_private_hosts: false
    request_timeout: 2m
    max_bytes: 50MiB
    metadata_cache_ttl: 5m
```

The HTTP host allowlist is a source-fetch boundary. See
[Authorization](authorization.md#source-fetch-boundary) for origin, redirect,
private host, and DNS rebinding behavior.

`metadata_cache_ttl` gives remote URL identifiers the same kind of explicit
staleness window that local URL auth-probe mappings use. While a remote
identifier's metadata cache entry is fresh, Triplet can build derivative cache
keys from cached `ETag`, `Last-Modified`, and size metadata instead of making a
new upstream `HEAD` or range request before checking the derivative cache. If
the upstream source changes, disappears, or changes authorization during that
TTL, Triplet may continue serving the locally cached derivative until the
metadata entry expires. Local URL mappings with `auth_probe: true` inherit the
same TTL for anonymous and credentialed auth-probe decisions. Leave the TTL
unset or `0` when every derivative request must revalidate source metadata and
every auth-probed local URL request must recheck upstream authorization.
