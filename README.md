# triplet

A IIIF server in Go. Implements the [IIIF Image API 3.0][image-api],
[IIIF Presentation API 3.0][presentation-api], and minimal Auth/Search
surfaces, with optional non-spec extensions for byte-stream transforms and
uploads.

All image processing is done by [libvips](https://github.com/libvips/libvips).

[image-api]: https://iiif.io/api/image/3.0/
[presentation-api]: https://iiif.io/api/presentation/3.0/

## Quick start

```sh
docker run -p 8080:8080 ghcr.io/libops/triplet:main
```

## Configuration

triplet is configured by a single YAML file. See `config.example.yaml` for the
full surface. Environment variables can be referenced in the YAML. e.g.

```yaml
server:
  public_base_url: "${TRIPLET_PUBLIC_BASE_URL}"
```

Current notable knobs:

- `vips.*` tunes libvips startup, `block_untrusted`, and operation blocklists.
- `iiif.image.max_output_pixels`, `iiif.image.max_source_pixels`,
  `iiif.image.max_derivative_bytes`, and `iiif.image.max_concurrent_transforms`
  bound libvips request work.
- `iiif.search.*` enables an experimental Content Search 2.0 route with the
  default no-op backend. It is not a production search index.
- `iiif.auth.*` enables the Authorization Flow 2.0 route. The built-in
  permit-all authorizer requires `iiif.auth.development_permit_all: true` and
  is intended only for development.
- `iiif.presentation.root` or `iiif.presentation.dsn` selects filesystem or MariaDB storage.
- `sources.default` can be `file`, `http`, or `gcs`.
- `sources.http.allowed_hosts` is the primary security boundary for remote
  source images. Keep it to the exact upstream hostnames Triplet is allowed to
  fetch; an empty list denies all HTTP sources and `*` should only be used in
  closed internal deployments. Private, loopback, link-local, and metadata
  addresses are blocked unless `sources.http.allow_private_hosts` is explicitly
  enabled.
- `cache.root` / `cache.bucket_url` configure derivative caching.
- `cache.source_root` / `cache.source_bucket_url` configure HTTP source caching.

When using HTTP identifiers, Triplet treats the identifier as a source URL and
fetches it before passing bytes to libvips. The HTTP host allowlist is therefore
more than routing configuration: it prevents arbitrary URL fetches, constrains
redirect targets, and keeps the native image parser surface limited to trusted
repositories. Source caching improves performance but does not replace the
allowlist; cache fills still pass through the same host checks.

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

## libvips Build Surface

The runtime image builds libvips from source with a narrow, explicit feature
set. Enabled options map to IIIF formats Triplet serves today; disabled options
either add unused source formats, text/PDF/SVG rendering stacks, dynamic module
loading, or broader parser surface area.

### Enabled

| Option | What it enables | Triplet implication |
|---|---|---|
| `cgif` | Native GIF save support in libvips. | Required for IIIF `gif` response/output without ImageMagick. |
| `imagequant` | Palette quantization support. | Required for native `gifsave_buffer`; also improves palette output quality. |
| `jpeg` | JPEG load/save. | Required for common IIIF JPEG source and derivative traffic. |
| `lcms` | Little CMS color management. | Required for optional ICC normalization. The default path preserves embedded profiles without converting, closer to Cantaloupe behavior. |
| `openjpeg` | JPEG 2000 load/save. | Required for JP2 source/input and response/output. |
| `png` | PNG load/save. | Required for PNG source/input and response/output. |
| `spng` | libspng PNG codec support. | Keeps PNG support fast and modern where libvips uses SPNG. |
| `tiff` | TIFF load/save. | Required for common preservation/master source images and TIFF output. |
| `webp` | WebP load/save. | Required for WebP source/input and response/output. |
| `zlib` | Deflate compression support. | Required by PNG/TIFF-related compression paths. |

When `vips.block_untrusted` is enabled, Triplet still unblocks the libvips
JPEG 2000 load/save operations because JP2 is an advertised IIIF source and
response format. Operators can re-block those classes with
`vips.blocked_operations` if their deployment does not accept JP2 collections.

### Disabled

| Option | What disabling means | Triplet implication |
|---|---|---|
| `modules` | No dynamic libvips plugin modules loaded at runtime. | Improves container predictability and security; needed support is compiled in. |
| `introspection` | No GObject introspection metadata. | Fine; govips uses cgo bindings, not runtime GIR metadata. |
| `cfitsio` | No FITS astronomy image load/save. | Not needed unless serving FITS. |
| `exif` | No libexif metadata support. | Reduces metadata parser surface; EXIF orientation/metadata handling is limited. |
| `fftw` | No FFT/frequency-domain operations. | Not used for IIIF resize/crop/encode. |
| `fontconfig` | No font discovery. | Not needed while text rendering is disabled. |
| `archive` | No archive-backed pyramid/deepzoom packaging. | Not used by current handlers. |
| `heif` | No HEIC/AVIF load/save. | Revisit only if HEIC/AVIF source or output support is required. |
| `uhdr` | No Ultra HDR JPEG gain-map support. | Not needed for current IIIF output. |
| `jpeg-xl` | No JPEG XL load/save. | Revisit only if JXL support is required. |
| `magick` | No ImageMagick fallback formats. | Intentional; avoids a large dependency and parser surface. |
| `matio` | No MATLAB `.mat` image load. | Not needed. |
| `nifti` | No NIfTI medical image load/save. | Not needed. |
| `openexr` | No OpenEXR HDR image load/save. | Not needed. |
| `openslide` | No whole-slide formats such as SVS/NDPI/MRXS. | Revisit if Triplet targets pathology or whole-slide imaging. |
| `highway` | No Highway SIMD acceleration. | Possible performance cost; benchmark before enabling. |
| `orc` | No ORC vectorized/JIT pixel operations. | Possible performance cost; benchmark before enabling. |
| `pangocairo` | No text rendering. | Not needed unless Triplet adds labels, watermarks, or rendered text. |
| `pdfium` | No PDF load via PDFium. | PDF source/input stays disabled; Triplet only writes simple PDF output. |
| `poppler` | No PDF load via Poppler. | Same as `pdfium`; avoids PDF parser surface. |
| `quantizr` | No quantizr quantization backend. | Fine; native GIF output uses `imagequant`. |
| `raw` | No camera RAW load. | Not needed. |
| `rsvg` | No SVG load. | Intentional unless SVG sources become required. |

## Deploys

- `deploy/cloudrun/` — multi-region Cloud Run, mirrors the
  [`cantaloupe-cloudrun`](https://github.com/libops/cantaloupe-cloudrun) layout.
- `deploy/compose/` — single-host docker-compose for self-hosters.

GCP is treated as a first-class target but no Google API leaks above the
storage abstraction. AWS/S3 is intentionally out of scope for this spike. The
runtime also exposes Prometheus metrics at `/metrics`.

When using MariaDB for Presentation storage, apply the schema as a migration
step with a DDL-capable account:

```sh
triplet -config config.yaml -migrate-presentation-mariadb
```

Normal server startup does not run DDL, so the runtime DSN can use a
least-privilege account after migration.

## Conformance

Triplet consumes machine-readable IIIF artifacts from
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

## Benchmark Against Cantaloupe

A comparison between cantaloupe and triplet was performed. The full results are in [./fixtures/benchmark/README.md](./fixtures/benchmark#benchmark-triplet-vs-cantaloupe). Here is a summary:


| Dimension | Winner | Detail |
|---|---|---|
| Success rate | **Triplet** | 100% vs 94% — Cantaloupe has persistent `large_jpg`/`square_jpg` 400s |
| Latency — uncached median | **Triplet** | 1.6–3.7× faster; advantage grows with concurrency |
| Latency — uncached p99 | **Triplet** | 4–5× faster across all concurrency levels |
| Latency — cached | **Triplet** | 1.3–2.1× faster; wins every request type |
| JP2 support | **Triplet** | 100% success, 2.4–5.7× faster uncached, 1.3–1.6× faster cached |
| Concurrency scaling | **Triplet** | 5× latency growth c1→c8 vs Cantaloupe's 11.3× |
| CPU efficiency | **Triplet** | 3–3.5× less CPU/req uncached; ~6× less CPU/req cached |
| Memory footprint | **Triplet** | 6–11× lower uncached; 2–3× lower cached |
| Output size (all types except `full_jpg`) | **Triplet** | Smaller across 8 of 9 request types and both JP2 sources |
| Output size — `full_jpg` | **Cantaloupe** | Triplet 1.36× larger on full-resolution JPEG from large TIFF |


## License

MIT — see [LICENSE](LICENSE).
