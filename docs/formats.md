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
    max_source_bytes: 1GiB
    max_derivative_bytes: 512MiB
    color_management: preserve
    load_access: auto
```

Source backends determine where identifiers resolve from. A file source is the
default; HTTP sources can be added for URL identifiers.

```yaml
sources:
  default: file
  file:
    root: ./testdata/images
  http:
    allowed_origins:
      - https://repository.example.edu
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
