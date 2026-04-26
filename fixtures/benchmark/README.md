# IIIF benchmark fixtures

Place source images in this directory before running the benchmark harness.
Nested directories are supported; identifiers are URL-encoded relative paths.

Supported source extensions:

- `.tif`, `.tiff`
- `.jp2`, `.j2k`
- `.jpg`, `.jpeg`
- `.png`, `.webp`, `.gif`

The default request matrix lives in `requests.tsv`. Add rows as:

```text
name<TAB>iiif-request-path
```

Use paths after the identifier, for example:

```text
thumb_jpg	full/256,/0/default.jpg
```
