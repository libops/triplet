# Caching

Triplet has several independent cache layers. They optimize different work and
have different invalidation behavior. The important operational distinction is
whether a cache stores response bytes, source bytes, in-process metadata, or an
authorization decision.

## Derivative cache

Configure either a filesystem root or a blob bucket URL for encoded IIIF image
responses. This is the main cache for public IIIF traffic: once a derivative is
generated, later requests for the same identifier, source version, region, size,
rotation, quality, and format can be served without running libvips again.

GCS bucket configuration is implemented but has not yet been deployment-tested.

```yaml
cache:
  root: /var/lib/triplet/cache
  # bucket_url: gs://triplet-cache
  # prefix: derivatives
  max_bytes: 1073741824
```

`max_bytes` is a best-effort filesystem eviction target. It does not apply to
GCS-backed caches; use object lifecycle policy or bucket tooling there. Failed
transforms and HTTP error responses are not stored.

### Derivative invalidation

The route writes an invalidation marker into the derivative cache. Subsequent
image requests for that identifier use a new cache namespace, so old derivative
objects are ignored even when the source bytes and metadata have not changed.
This is useful for local URL mappings with `auth_probe: true`, where repository
permission changes may need to take effect before the configured auth-probe TTL
expires. When the source backend supports per-identifier auth caching, the same
route also clears those cached auth-probe decisions for the identifier.

The invalidation route is protected by a bearer token and optional caller CIDR
checks. See [Authorization](authorization.md#cache-invalidation-route) for the
route configuration and caller requirements.

### Source version metadata

Derivative cache keys include a source version so Triplet does not reuse old
encoded responses after the source changes. For HTTP sources, Triplet prefers
`ETag` when the upstream provides one. If there is no `ETag`, Triplet uses the
upstream `Last-Modified` value plus the source size. If neither source version
signal is available, Triplet treats the derivative response as uncacheable
because it cannot distinguish a fresh source from a changed one.

`Last-Modified` is also parsed as the source modification time. The in-process
`info.json` dimension cache uses source size and modification time, so updated
HTTP `Last-Modified` metadata causes dimension-cache misses for changed sources.

## Source cache

The optional source cache stores fetched source bytes, primarily for HTTP
identifiers. It is useful when the repository source is remote, expensive to
fetch repeatedly, or slower than Triplet's local cache storage. It does not
replace the HTTP source allowlist: cache fills still pass through the same host
checks.

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

When `source_stale_after` is set, stale hits are served immediately and refreshed
in the background. Upstream 4xx/5xx responses are not stored.

## Authorization decision cache

Local URL mappings with `auth_probe: true` cache anonymous and credentialed
source authorization decisions in process. See [Authorization](authorization.md)
for the full auth-probe flow, IIIF Authorization Flow terminology, and TTL
behavior.

## In-process caches

### Image metadata cache

Triplet can cache the source dimensions used to build Image API `info.json`.
This avoids reopening or reprobing the same source when clients repeatedly load
viewer metadata.

```yaml
iiif:
  image:
    info_dimension_cache: true
```

The dimension cache is in process and keyed by identifier plus source size and
modification time metadata. It is enabled by default. Source changes with
updated metadata miss the cache.

### libvips operation cache

The libvips operation cache is disabled by default because Triplet's derivative
and source caches are usually the right place to retain work across requests.

```yaml
vips:
  cache_max_mem: 0
  cache_max_files: 0
```

The libvips operation cache is process-local and separate from Triplet's
derivative and source caches.

## Cache layer summary

| Layer | Configuration | What is cached | Invalidation / freshness |
|---|---|---|---|
| Derivative cache | `cache.root` or `cache.bucket_url`; optional `cache.max_bytes`, `cache.prefix`, `iiif.image.cache_invalidation_token` | Encoded IIIF image responses, keyed by identifier, source version, invalidation marker, region, size, rotation, quality, and format. | A changed source version produces a new key. The protected invalidation route bumps the per-identifier invalidation marker. Filesystem caches can evict best-effort by size; GCS/object lifecycle is external. Failed transforms and HTTP error responses are not stored. |
| HTTP source cache | `cache.source_root` or `cache.source_bucket_url`; optional `cache.source_max_bytes`, `cache.source_prefix`, `cache.source_stale_after` | Original source bytes fetched through the HTTP source backend. | Keys are source identifiers. When `source_stale_after` is set, stale hits are served immediately and refreshed in the background. Upstream 4xx/5xx responses are not stored. |
| `info.json` dimension cache | `iiif.image.info_dimension_cache` | Source dimensions used to build Image API `info.json`. | In-memory only. Entries are keyed by identifier plus source size/modtime metadata, so source changes with updated metadata miss the cache. |
| Local URL auth-probe cache | `sources.file.url_mappings[].auth_*` | Authorization probe results for local URL mappings with `auth_probe: true`. Anonymous and credentialed probes are cached separately. See [Authorization](authorization.md). | In-memory only. Tier defaults are 5 minutes unless overridden by `auth_anonymous_cache_ttl`, `auth_authenticated_cache_ttl`, or `auth_cache_ttl`. The image cache invalidation route also clears matching auth-probe entries when the source backend supports it. |
| libvips operation cache | `vips.cache_max_mem`, `vips.cache_max_files` | libvips in-process operation results. | Disabled by default in the example config. This is process-local and separate from Triplet's derivative/source caches. |
