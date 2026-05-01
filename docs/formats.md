# Format Support

The IIIF Image API calls the original asset the `identifier` source and the
returned derivative the response image.

The Image API surface and transform limits are configured under `iiif.image`:

```yaml
iiif:
  image:
    enabled: true
    prefix: /iiif/3
    max_output_pixels: 100000000
    max_source_pixels: 250000000
    max_source_bytes: 1073741824
    max_derivative_bytes: 536870912
    color_management: preserve
    load_access: auto
```

Source backends determine where identifiers resolve from. A file source is the
default; HTTP and GCS sources can be added for URL and bucket-backed
identifiers. The GCS backend is implemented but has not yet been
deployment-tested.

```yaml
sources:
  default: file
  file:
    root: ./testdata/images
  http:
    allowed_origins:
      - https://repository.example.edu
  gcs:
    # Implemented, but not deployment-tested yet.
    bucket_url: gs://my-bucket
    prefix: images
```

| Format | Source / Input | Response / Output | Notes |
|---|---|---|---|
| JPEG (`jpg`) | Yes | Yes | |
| PNG (`png`) | Yes | Yes | |
| TIFF (`tif`) | Yes | Yes | |
| WebP (`webp`) | Yes | Yes | |
| GIF (`gif`) | Yes | Yes | GIF output is implemented in the pipeline. |
| JP2 (`jp2`) | Yes | Yes | JP2 source/input and response/output work when libvips has OpenJPEG. |
| PDF (`pdf`) | No | Yes | PDF response/output wraps the transformed raster as a single-page PDF. PDF source/input is disabled by default. |
