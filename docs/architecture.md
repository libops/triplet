# triplet architecture & status

A Go IIIF server intended to replace two production systems for libops:

1. **Cantaloupe** in the Islandora stack — the IIIF Image API surface that
   accepts URL-as-identifier requests and returns transformed images.
2. **Scribe's `internal/imageservice` and `internal/annotationserver`** —
   Scribe will consume IIIF APIs on triplet directly; no Scribe-specific
   extension routes live on triplet.

This is a **spike PoC**. The goal is to determine whether libvips + Go can
replace Cantaloupe (JVM, OpenJpegProcessor) at lower memory/cost while
remaining spec-compliant, and whether a unified IIIF surface can serve both
the Islandora and Scribe use cases.

## High-level shape

```
                  ┌──────────────────────────────────────────────────┐
                  │                  cmd/triplet                     │
                  │                                                  │
                  │  config.Load → vips.Startup → server.Build → Run │
                  └──────────────────────────────────────────────────┘
                                          │
                                          ▼
                  ┌──────────────────────────────────────────────────┐
                  │                internal/server                   │
                  │                                                  │
                  │      net/http ServeMux + slog middleware         │
                  │      mounts handlers; no third-party router      │
                  └──────────────────────────────────────────────────┘
                                          │
                ┌─────────────────────────┼─────────────────────────┐
                ▼                         ▼                         ▼
      ┌──────────────────┐      ┌──────────────────┐      ┌──────────────────┐
      │  iiif/image/v3   │      │ iiif/presentation│      │      health      │
      │     handler      │      │   /v3 handler    │      │  /healthz, etc.  │
      │                  │      │                  │      │                  │
      │  /iiif/3/{id}/.. │      │  /presentation/  │      │                  │
      └──────────────────┘      │     v3/...       │      └──────────────────┘
                │               └──────────────────┘
                ▼                         │
       ┌──────────────────┐               ▼
       │     pipeline     │      ┌──────────────────┐
       │                  │      │  presentation    │
       │  parse.Request → │      │     store        │
       │  vipsgen ops →   │      │  (filesystem or  │
       │  io.Writer       │      │   sql later)     │
       └──────────────────┘      └──────────────────┘
                │
                ▼
       ┌──────────────────┐         ┌──────────────────┐
       │  storage.Opener  │ ◀──────▶│   cache.Store    │
       │                  │ wrapped │                  │
       │  File / HTTP /   │   by    │  File or GCS     │
       │  GCS / Multiplex │ Caching │                  │
       └──────────────────┘         └──────────────────┘
                │
                ▼
       ┌──────────────────┐
       │      libvips     │  via internal/vips → cshum/vipsgen
       │                  │
       │  decode → region │
       │  → resize → rot  │
       │  → quality →     │
       │  encode          │
       └──────────────────┘
```

### Why these splits

- **`pipeline` is the only package that talks to vipsgen for transforms.**
  Handlers know nothing about libvips. This keeps the IIIF surface stable
  even if we swap to a different image library later.
- **`storage.Opener` is a single-method interface.** Anything that can produce
  bytes for an identifier (filesystem, HTTP, in-memory upload) plugs in.
  HTTPOpener is what replaces Cantaloupe's `HttpSource` — the identifier
  *is* the URL.
- **`cache.Store` is a single-method interface decorating Openers** and used
  separately for derivative responses. File-backed and GCS-backed today;
  additional backends drop in without touching handlers or pipeline.
- **`internal/vips` owns libvips lifecycle** so no other package calls
  `vips_init`. Per the supply-chain review, `SetLogging` is a global state
  change and only this package is allowed to touch it.

### Why not Connect/buf for IIIF

IIIF is a REST + JSON-LD spec; the URL grammar *is* the contract. Generating
RPC clients adds nothing for IIIF spec routes. Connect remains the right
choice for Scribe's *internal* operations (split/join/crosswalk in the parent
repo's `proto/scribe/v1/`), but those don't belong on triplet — Scribe will
keep that surface in-house and consume only IIIF from triplet.

## Component status

### Done

| Path | What it does | Notes |
|---|---|---|
| `cmd/triplet/main.go` | Entry point: load config → start libvips with configured hardening/tuning → build server → graceful shutdown on SIGTERM. | |
| `internal/config/` | YAML config loader. Strict (`KnownFields=true`), validated. Defaults applied. Supports filesystem, HTTP, and GCS source/cache configuration plus libvips runtime settings. | Tests: ✓ |
| `internal/observability/` | `slog` setup (json/text), request-id middleware, panic recovery. | |
| `internal/metrics/` | Prometheus metrics endpoint and middleware. Exposes HTTP counters/histograms plus libvips memory/file gauges at `/metrics`. | |
| `internal/server/` | `net/http` ServeMux composition. Wires handlers, middleware, graceful shutdown. Builds file / HTTP / GCS / multiplex source openers from config, optionally wraps HTTP with source caching, wires derivative cache, mounts `/metrics`, and mounts the Presentation handler when enabled. | |
| `internal/storage/opener.go` | `Opener` interface + `Meta` + `ErrNotFound`. | |
| `internal/storage/file.go` | `FileOpener` with path-traversal protection. | Tests: ✓ |
| `internal/storage/http.go` | `HTTPOpener` — URL-encoded identifier → fetch with allow-listed hosts, max-bytes cap, redacted error logging. | **Cantaloupe HttpSource parity.** Tests: ✓ |
| `internal/storage/gcs.go` | GCS opener using `cloud.google.com/go/storage`, with tempfile-backed seekable reads for the image pipeline. | Tests: ✓ |
| `internal/storage/multiplex.go` | Routes identifiers to backends by prefix or scheme. | Tests: ✓ |
| `internal/storage/caching.go` | `Caching` decorator: memoise upstream fetches via `cache.Store`. | Tests: ✓ |
| `internal/cache/cache.go` | `Store` interface + `Noop`. | |
| `internal/cache/file.go` | Filesystem backend. SHA-256-keyed two-level fan-out, mtime LRU eviction bounded by `MaxBytes`. | Tests: ✓ |
| `internal/cache/gcs.go` | GCS-backed cache store using `cloud.google.com/go/storage`. | Tests: ✓ |
| `internal/cache/keys.go` | Canonical derivative cache key from a parsed `parse.Request`. | |
| `internal/iiif/image/v3/parse/` | Full Image API 3.0 URL grammar parser. Region (full/square/pixels/pct), Size (max/^max/w,/,h/w,h/!w,h/pct: with optional ^ upscale), Rotation (with `!` mirror), Quality, Format. | Tests: ✓ comprehensive table-driven. |
| `internal/iiif/image/v3/types/info.go` | Thin aliases/wrappers over `github.com/libops/iiif-spec` generated Image wire types plus `BuildLevel2Info` and Level-2 capability declarations. | Advertises `jpg`, `png`, `gif`, `webp`, `tif`. |
| `internal/iiif/image/v3/schema/` | Small adapter over `github.com/libops/iiif-spec`’s derived Image schema validator. | Triplet no longer owns the Image schema artifact itself. |
| `internal/iiif/image/v3/pipeline/pipeline.go` | `Transform(ctx, req, w)` — vipsgen-backed: decode source, region (clipped), size (with upscale rules and aspect-preserving best-fit), rotation+mirror, quality (color/gray/bitonal), encode (JPEG/PNG/GIF/WebP/TIFF). Streams encoded bytes through `vg.NewTarget(io.Writer)`. | vips-backed tests cover resize, region+rotation, grayscale quality, bitonal thresholding, GIF output, JP2 rejection, max_output_pixels enforcement, and color-space normalization (including embedded ICC when the test environment exposes a named sRGB profile). |
| `internal/iiif/image/v3/handler/handler.go` | Handler wired to pipeline + derivative cache. Base→info redirect (303), info.json (CORS, profile link header, configured maxArea/maxWidth/maxHeight), image transform with cache hit/miss path, canonical Link header, and derivative `ETag` / `If-None-Match` handling. Derivatives now stream to the response and cache simultaneously instead of buffering the full payload in memory first. | HTTP-surface tests cover info, canonical link, `ETag`, and `304 Not Modified`. |
| `internal/iiif/presentation/v3/types/` | Thin aliases/wrappers over `github.com/libops/iiif-spec` generated Presentation wire types. Keeps constants for the IIIF Text Granularity extension and small handwritten wrappers where the upstream schema does not model extension fields like `textGranularity`. | Still not a full Presentation model; enough to represent manifest and text-annotation slices cleanly. |
| `internal/iiif/presentation/v3/validate/` | Structural validation for the Presentation documents triplet currently serves and accepts: manifests and annotation pages, including the Text Granularity extension rules triplet explicitly supports. | Tests: ✓ |
| `internal/iiif/presentation/v3/store/` | `Store` interface plus filesystem-backed manifest and annotation-page storage. Manifests live at `{root}/{itemID}/manifest.json`; annotation pages at `{root}/{itemID}/canvas/{canvasID}/annotations.json`. | Tests: ✓ |
| `internal/iiif/presentation/v3/handler/` | Presentation API handler. Supports `GET /presentation/v3/{itemID}/manifest`, `GET|HEAD /presentation/v3/{itemID}/canvas/{canvasID}/annotations`, and `PUT /presentation/v3/{itemID}/canvas/{canvasID}/annotations`, with JSON-LD validation and CORS. | Tests: ✓ |
| `internal/vips/vips.go` | libvips lifecycle wrapper: `Startup(Config, *slog.Logger)`, `Shutdown()`, `ReadMemStats()`, error wrapping, `NopWriteCloser`. Sets `VIPS_BLOCK_UNTRUSTED=1` when configured. | |
| `github.com/libops/iiif-spec` | External module containing vendored upstream IIIF machine-readable artifacts, derived schemas, OpenAPI documents, and public Go wire-type packages. | Triplet now consumes machine-readable contract artifacts from a versioned upstream dependency instead of owning vendoring/generation tooling locally. |
| `Dockerfile` | Multi-stage build/test/runtime image. Builds libvips 8.18 from upstream source in a Debian bookworm stage, layers the Go toolchain on top for build/test, and reuses that libvips-capable base for runtime. Includes a `test-runner` stage for containerized `go test`. | Verified through the current `make test` path on a Docker-enabled host. |
| `scripts/build.sh` | Containerized build wrapper. Builds the Docker `build` stage and copies `/out/triplet` back to the host so default builds do not require host libvips/pkg-config setup. | Current default build path. |
| `scripts/test.sh` | Containerized test runner. Builds the `test-runner` stage, reuses Go module/build caches via mounted volumes, and joins a Compose MariaDB network when present to enable integration tests. | Current default test path; `make test` succeeds. |
| `image.txt`, `presentation.txt`, `auth.txt`, `search.txt`, `text-granularity.txt` | Local compliance-reference files pointing to the canonical IIIF Image / Presentation / Auth / Search specs and the Text Granularity extension, listing the repo’s current obligations and gaps. | Keep these aligned with implementation and tests. |
| `Makefile` | build / test / test-race / lint / generate / docker / clean. `build` delegates to `scripts/build.sh`; `test` delegates to `scripts/test.sh`; `generate` is now only local formatting because spec artifact generation moved to `iiif-spec`. | |
| `config.example.yaml` | Documents the full configuration surface with comments, including derivative cache, optional source cache, and HTTP source examples. | |
| `deploy/cloudrun/{main,variables}.tf` | Multi-region Cloud Run skeleton mirroring `cantaloupe-cloudrun`. | Missing the LB module port. |
| `deploy/compose/` | Single-host docker-compose for self-host. Tracks the current image entrypoint, mounts config/images/presentation/cache paths, and applies read-only/no-new-privileges/cap-drop hardening. | |
| `.github/workflows/ci.yml` | CI workflow running the Docker-backed `make test` path and a compose smoke test against a freshly built runtime image. | |
| `.github/workflows/publish.yml` | GHCR publish workflow for tagged releases. | |
| `LICENSE`, `README.md`, `.gitignore` | MIT (LibOps LLC). | |

### In progress / not done

#### Image API

- **Conformance** — run `iiif-validate.py` against the running container in CI, and keep `triplet` aligned with the artifacts published by `github.com/libops/iiif-spec`.
- **Format gaps**: JP2 and PDF output. GIF output works; JP2 input already works via libvips' `openjpegload`, but JP2 response encoding is still not implemented.

#### Caching

- **S3 backend** — future AWS story. Add it deliberately later, with a clear
  dependency review before any SDK enters the module graph.
- **Stale-while-revalidate** for HTTP source caching when upstream returns slowly.
- **HTTPOpener byte-range support** so libvips can stream from upstream directly. Today HTTPOpener spools upstream bytes into a temporary seekable file instead of memory, which avoids RAM blowups but still fetches the full object eagerly.

#### Presentation API (Scribe parity)

- `GET  /presentation/v3/{itemID}/manifest` now exists with a filesystem-backed manifest store.
- `internal/iiif/presentation/v3/types/` now consumes `github.com/libops/iiif-spec` generated wire types plus a first usable Annotation / AnnotationPage / TextualBody / SpecificResource wrapper slice, including `textGranularity`, but still needs fuller Collection / Service / broader body/selector coverage.
- `internal/iiif/presentation/v3/store/` still needs a Postgres implementation reusing Scribe's existing schema.
- Schema validation and generated wire types are now sourced from `github.com/libops/iiif-spec`. Triplet should not add new repo-local spec vendoring or schema-generation machinery; changes to machine-readable artifacts belong upstream in `iiif-spec`.
- Extension support: the IIIF Text Granularity extension is now modeled in local Presentation types, validated on annotation-page writes, and tracked in `text-granularity.txt`.

#### Auth & Search (later)

- IIIF Auth API 2.0 (`auth.txt`) — probe / access / token services. Pluggable IdP behind an interface; OIDC adapter as a follow-up.
- IIIF Content Search 2.0 (`search.txt`) — `Searcher` interface; default no-op; Solr/OpenSearch adapters later. Triplet does not own indexing.

#### Deploy

- **Cloud Run LB module** ported from `cantaloupe-cloudrun/modules/lb/` with the dashboard.json adapted.

#### Operational hardening (post-spike)

- Per-format libvips loader disable list beyond `VIPS_BLOCK_UNTRUSTED=1`.
- ASAN build target in CI for catching libvips/CGO leaks.
- Container-level hardening outside compose defaults: seccomp profile and platform-specific runtime enforcement.

## Configuration model

Single YAML file. No env var overrides. Schema lives in `internal/config/`
and is documented in `config.example.yaml`. Sources, cache backends, and
extension toggles all live under one document so an operator can render it
once at deploy time.

Today supports `sources.default = file|http|gcs`. When multiple source
backends are configured, `internal/server` builds a `storage.Multiplex` that
routes `http://...` and `https://...` identifiers to `HTTPOpener`, `gs://...`
identifiers to the configured GCS bucket opener, while falling back to the
configured default source for everything else. HTTP source caching and
derivative caching can each target either the filesystem or a GCS bucket.

## Source dependencies

| Dependency | Version | Why | License |
|---|---|---|---|
| `gopkg.in/yaml.v3` | v3.0.1 | YAML config | MIT |
| `github.com/cshum/vipsgen` | v1.3.9 | libvips bindings | MIT |
| `github.com/libops/iiif-spec` | v0.0.1 | Generated IIIF wire types + derived schema validation | MIT |
| `github.com/prometheus/client_golang` | v1.23.2 | `/metrics` endpoint | Apache-2.0 |
| `cloud.google.com/go/storage` | v1.57.2 | GCS-backed cache/source backends | Apache-2.0 |
| `libvips` (system) | 8.18.x | image processing | LGPL-2.1 |

## Known weak spots in the current build

- **HTTPOpener still fetches full upstream responses eagerly.** It now uses a
  tempfile-backed seekable file instead of `io.ReadAll`, which fixes the RAM
  issue, but byte-range or demand-driven remote access would still be better
  for very large pyramidal sources.
- **No validator-backed conformance or broad end-to-end verification yet.**
  `make test` now exercises the Dockerized libvips environment, but
  `iiif-validate.py` and broader running-container checks still need to land
  before production rollout.
- **Cache eviction is naive** (insertion-sort over the whole tree on each
  oversize Put). Fine for tens of thousands of entries; replace with a
  proper LRU map for millions.

## What "done" means for the PoC

The spike answers two questions:

1. **Can triplet serve `cantaloupe.libops.io`'s workload at lower cost?** —
   answered by: pipeline + HTTPOpener + file/GCS cache + cloudrun deploy +
   benchmarks against the existing cantaloupe-cloudrun setup on real
   Islandora pyramidal-TIFF / JP2 inputs.
2. **Can Scribe consume triplet for image and annotation flows without
   bespoke endpoints?** — answered by: presentation/v3 + annotation
   PUT/GET against AnnotationPage fitting the model in Scribe AGENTS.md,
   plus a Scribe-side patch removing `internal/imageservice` and
   `internal/annotationserver` in favor of triplet HTTP calls.

If both yes, this becomes a real project. If either no, we have a clear
signal about which assumption broke.
