# triplet

A IIIF server in Go. Triplet implements the [IIIF Image API 3.0][image-api] and
[IIIF Presentation API 3.0][presentation-api].

All image processing is done by [libvips] through [govips].

## Quick start

```bash
docker run -p 8080:8080 ghcr.io/libops/triplet:main
```

Then try the bundled sample image:

```bash
curl http://localhost:8080/iiif/3/sample.png/info.json
```

## Documentation

The project documentation lives at <https://libops.github.io/triplet>.

## License

MIT. See [LICENSE](LICENSE).

[govips]: https://github.com/davidbyttow/govips
[image-api]: https://iiif.io/api/image/3.0/
[libvips]: https://github.com/libvips/libvips
[presentation-api]: https://iiif.io/api/presentation/3.0/
