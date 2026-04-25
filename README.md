# triplet

A IIIF server in Go. Implements the [IIIF Image API 3.0][image-api],
[IIIF Presentation API 3.0][presentation-api], and minimal Auth/Search
surfaces, with optional non-spec extensions for byte-stream transforms and
uploads.

Designed as a replacement for resource-heavy JVM IIIF servers in
container-first deployments. Single static binary, configured by a YAML file.

[image-api]: https://iiif.io/api/image/3.0/
[presentation-api]: https://iiif.io/api/presentation/3.0/

## Status

The Image API 3.0 surface is landed and exercised through the Docker-backed
`make test` path. Presentation 3.0 now includes manifest reads plus
annotation-page `GET`/`PUT` with text-granularity-aware validation. Search 2.0
has the HTTP surface and default no-op backend. Auth 2.0 has the
probe/access/token/logout surface with a default permit-all authorizer.

## Quick start

```sh
make build
./bin/triplet -config config.example.yaml
```

`make build` uses Docker via `scripts/build.sh`, so the default build path does
not require a host libvips installation.

Or with Docker:

```sh
cd deploy/compose && docker compose up
```

## Tests

`make test` runs the suite inside Docker via `scripts/test.sh`. `make build`
does the same for the binary via `scripts/build.sh`. Both scripts mount or
reuse cached artifacts so repeated host runs do not start from scratch.
`make test-integration` starts the `mariadb` service from `deploy/compose` and
then runs the Docker-backed test suite with `TEST_DSN`; `make test` remains
deterministic and skips DB integration tests when no database is available.
`make test-asan` runs the same Docker-backed test path with Go's `-asan`
instrumentation for CGO-heavy checks.
`make conformance` runs HTTP-level IIIF smoke checks against a running server
and calls `iiif-validate.py` when that tool is installed.

## Benchmark Against Cantaloupe

Put TIFF/JP2/JPEG fixtures under `fixtures/benchmark/`, then run:

```sh
make benchmark-iiif
```

The benchmark builds the Triplet runtime image, starts it next to
`islandora/cantaloupe:main`, runs every request in
`fixtures/benchmark/requests.tsv` against every fixture image, and writes CSV /
JSONL output under `results/benchmarks/<timestamp>/`.

Useful knobs:

```sh
BENCH_IMAGE_DIR=/path/to/images \
BENCH_PASSES=5 \
BENCH_CONCURRENCY=4 \
BENCH_CANTALOUPE_IMAGE=islandora/cantaloupe:main \
make benchmark-iiif
```

Outputs include request latency (`requests.csv`, `summary.csv`) and sampled
container CPU/memory (`container-stats.jsonl`, `resource-summary.csv`).

## Configuration

triplet is configured by a single YAML file. See `config.example.yaml` for the
full surface. Environment variables are not supported by design; if you need
templating, render the YAML before launch.

Current notable knobs:

- `vips.*` tunes libvips startup, `block_untrusted`, and operation blocklists.
- `iiif.search.*` enables the Content Search 2.0 route with the default no-op backend.
- `iiif.auth.*` enables the Authorization Flow 2.0 route with the default permit-all authorizer.
- `iiif.presentation.root` or `iiif.presentation.dsn` selects filesystem or MariaDB storage.
- `sources.default` can be `file`, `http`, or `gcs`.
- `cache.root` / `cache.bucket_url` configure derivative caching.
- `cache.source_root` / `cache.source_bucket_url` configure HTTP source caching.

## Format Support

The IIIF Image API calls the original asset the `identifier` source and the
returned derivative the response image. Current format support:

| Format | Source / Input | Response / Output | Notes |
|---|---|---|---|
| JPEG (`jpg`) | Yes | Yes | |
| PNG (`png`) | Yes | Yes | |
| TIFF (`tif`) | Yes | Yes | |
| WebP (`webp`) | Yes | Yes | |
| GIF (`gif`) | Yes | Yes | GIF output is implemented in the pipeline. |
| JP2 (`jp2`) | Yes | Yes | JP2 source/input and response/output work when libvips has OpenJPEG. |
| PDF (`pdf`) | No | Yes | PDF response/output wraps the transformed raster as a single-page PDF. PDF source/input is disabled by default. |

## Deploys

- `deploy/cloudrun/` — multi-region Cloud Run, mirrors the
  [`cantaloupe-cloudrun`](https://github.com/libops/cantaloupe-cloudrun) layout.
- `deploy/compose/` — single-host docker-compose for self-hosters.

GCP is treated as a first-class target but no Google API leaks above the
storage abstraction. AWS/S3 is intentionally out of scope for this spike. The
runtime also exposes Prometheus metrics at `/metrics`.

## Conformance

Triplet now consumes machine-readable IIIF artifacts from
[`github.com/libops/iiif-spec`](https://github.com/libops/iiif-spec) instead
of owning local spec vendoring and schema generation tooling.

For Go code, triplet imports:

- `github.com/libops/iiif-spec/image/v3/gen`
- `github.com/libops/iiif-spec/image/v3/schema`
- `github.com/libops/iiif-spec/presentation/v3/gen/...`

Triplet’s local `types/` packages are thin aliases or wrappers on top of those
imported wire types where the server needs stable names or extension fields
beyond the upstream schemas.

Triplet also tracks extension support in code and tests. In particular, the
Presentation annotation path validates the IIIF Text Granularity extension,
and the Search 2.0 route exposes a default no-op Content Search surface.

## License

MIT — see [LICENSE](LICENSE).
