# triplet

A IIIF server in Go. Implements the [IIIF Image API 3.0][image-api] and (planned)
[IIIF Presentation API 3.0][presentation-api], with optional non-spec extensions
for byte-stream transforms and uploads.

Designed as a replacement for resource-heavy JVM IIIF servers in
container-first deployments. Single static binary, configured by a YAML file.

[image-api]: https://iiif.io/api/image/3.0/
[presentation-api]: https://iiif.io/api/presentation/3.0/

## Status

The Image API 3.0 surface is landed and exercised through the Docker-backed
`make test` path. Presentation 3.0 now includes manifest reads plus
annotation-page `GET`/`PUT` with text-granularity-aware validation. Search 2.0
has the HTTP surface and default no-op backend. Auth remains a follow-on
milestone.

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

## Configuration

triplet is configured by a single YAML file. See `config.example.yaml` for the
full surface. Environment variables are not supported by design; if you need
templating, render the YAML before launch.

Current notable knobs:

- `vips.*` tunes libvips startup and enables `block_untrusted` hardening.
- `iiif.search.*` enables the Content Search 2.0 route with the default no-op backend.
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
| JP2 (`jp2`) | Yes | No | JP2 source/input works via libvips. JP2 response/output is not implemented. |
| PDF (`pdf`) | No | No | Not implemented. |

## Deploys

- `deploy/cloudrun/` — multi-region Cloud Run, mirrors the
  [`cantaloupe-cloudrun`](https://github.com/libops/cantaloupe-cloudrun) layout.
- `deploy/compose/` — single-host docker-compose for self-hosters.

GCP is treated as a first-class target but no Google API leaks above the
storage abstraction. S3 remains a future backend. The runtime also exposes
Prometheus metrics at `/metrics`.

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

Triplet also tracks extension compliance references locally. In particular,
`text-granularity.txt` records the current support target for the IIIF Text
Granularity extension used with Presentation annotations, and `search.txt`
tracks the IIIF Content Search target.

## License

MIT — see [LICENSE](LICENSE).
