# Caching

Triplet has several independent cache layers. They optimize different work and
have different invalidation behavior.

## Derivative cache

Configure either a filesystem root or a blob bucket URL for encoded IIIF image
responses.

GCS bucket configuration is implemented but has not yet been deployment-tested.

```yaml
cache:
  root: /var/lib/triplet/cache
  # bucket_url: gs://triplet-cache
  # prefix: derivatives
  max_bytes: 1073741824
```

## Source cache

The optional source cache stores fetched source bytes, primarily for HTTP
identifiers.

GCS-backed source cache configuration should be treated as untested until it has
been exercised in a real deployment.

```yaml
cache:
  source_root: /var/lib/triplet/source-cache
  # source_bucket_url: gs://triplet-source-cache
  # source_prefix: sources
  source_max_bytes: 1073741824
  source_stale_after: 24h
```

## Dimension and operation caches

`info.json` dimensions are cached in process. The libvips operation cache is
disabled by default because Triplet's derivative and source caches are usually
the right place to retain work across requests.

```yaml
iiif:
  image:
    info_dimension_cache: true

vips:
  cache_max_mem: 0
  cache_max_files: 0
```

## Auth-probe cache

Local URL mappings can cache anonymous and credentialed authorization probe
results separately.

```yaml
sources:
  file:
    url_mappings:
      - prefix: /system/files
        root: /private
        auth_probe: true
        auth_anonymous_cache_ttl: 720h
        auth_authenticated_cache_ttl: 168h
        auth_error_cache_min_age: 5m
        auth_cache_max_entries: 4096
```

| Layer | Configuration | What is cached | Invalidation / freshness |
|---|---|---|---|
| Derivative cache | `cache.root` or `cache.bucket_url`; optional `cache.max_bytes`, `cache.prefix` | Encoded IIIF image responses, keyed by identifier, source version, region, size, rotation, quality, and format. | A changed source version produces a new key. Filesystem caches can evict best-effort by size; GCS/object lifecycle is external. Failed transforms and HTTP error responses are not stored. |
| HTTP source cache | `cache.source_root` or `cache.source_bucket_url`; optional `cache.source_max_bytes`, `cache.source_prefix`, `cache.source_stale_after` | Original source bytes fetched through the HTTP source backend. | Keys are source identifiers. When `source_stale_after` is set, stale hits are served immediately and refreshed in the background. Upstream 4xx/5xx responses are not stored. |
| `info.json` dimension cache | `iiif.image.info_dimension_cache` | Source dimensions used to build Image API `info.json`. | In-memory only. Entries are keyed by identifier plus source size/modtime metadata, so source changes with updated metadata miss the cache. |
| Local URL auth-probe cache | `sources.file.url_mappings[].auth_*` | Authorization probe results for local URL mappings with `auth_probe: true`. Anonymous and credentialed probes are cached separately. | In-memory only. Success, 403, and 404 results are cached for the configured TTL. Other upstream errors are not cached. 403/404 results with a `Last-Modified` newer than `auth_error_cache_min_age` are not cached. |
| libvips operation cache | `vips.cache_max_mem`, `vips.cache_max_files` | libvips in-process operation results. | Disabled by default in the example config. This is process-local and separate from Triplet's derivative/source caches. |

Source caching improves performance but does not replace the HTTP source
allowlist. Cache fills still pass through the same host checks.
