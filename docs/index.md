# Triplet

Triplet is a IIIF server written in Go. It implements the [IIIF Image API
3.0][image-api] and [IIIF Presentation API 3.0][presentation-api].

All image processing is done by [libvips] through [govips].

## Quick start

```bash
docker run -p 8080:8080 ghcr.io/libops/triplet:main
```

Triplet needs a public base URL before generated IIIF identifiers are useful
outside the container:

```yaml
server:
  public_base_url: "${TRIPLET_PUBLIC_BASE_URL}"

sources:
  default: file
  file:
    root: ./testdata/images

iiif:
  image:
    enabled: true
    prefix: /iiif/3
```

## Documentation

- [Configuration](configuration.md) covers the YAML configuration surface.
- [Authorization](authorization.md) explains authentication controls, source authorization, and HTTP source boundaries.
- [Caching](caching.md) explains Triplet's cache layers and invalidation behavior.
- [Format support](formats.md) lists source and response formats.
- [libvips build](libvips.md) documents the runtime image feature surface.
- [Deploys](deploys.md) covers deployment notes and storage backends.
- [Conformance](conformance.md) summarizes IIIF spec integration.
- [Benchmarks](benchmarks.md) summarizes Triplet performance measurements.

[govips]: https://github.com/davidbyttow/govips
[image-api]: https://iiif.io/api/image/3.0/
[libvips]: https://github.com/libvips/libvips
[presentation-api]: https://iiif.io/api/presentation/3.0/
