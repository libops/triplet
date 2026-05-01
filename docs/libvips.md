# libvips Build Surface

The runtime image builds libvips from source with a narrow, explicit feature
set. Enabled options map to IIIF formats Triplet serves today. Disabled options
either add unused source formats, text/PDF/SVG rendering stacks, dynamic module
loading, or broader parser surface area.

Runtime libvips behavior is configured under `vips`:

```yaml
vips:
  concurrency: 0
  cache_max_mem: 0
  cache_max_files: 0
  report_leaks: false
  block_untrusted: true
  blocked_operations:
    - VipsForeignLoadPdf
```

Image request limits live under `iiif.image`:

```yaml
iiif:
  image:
    max_output_pixels: 100000000
    max_source_pixels: 250000000
    max_derivative_bytes: 512MiB
    max_concurrent_transforms: 4
    color_management: preserve
    load_access: auto
```

`color_management: preserve` is the default and leaves source profiles attached
where the output codec supports them. `normalize` uses LCMS to convert supported
non-sRGB color images to sRGB and strips export metadata; `none` skips color
conversion while still stripping export metadata where supported. See
[Configuration](configuration.md#image-processing) for the operator tradeoffs.

## Enabled

| Option | What it enables | libvips implication |
|---|---|---|
| `cgif` | Native GIF save support in libvips. | Required for IIIF `gif` response/output without ImageMagick. |
| `imagequant` | Palette quantization support. | Required for native `gifsave_buffer`; also improves palette output quality. |
| `jpeg` | JPEG load/save. | Required for common IIIF JPEG source and derivative traffic. |
| `lcms` | Little CMS color management. | Required for optional ICC normalization. The default path preserves embedded profiles without converting unless normalization is configured. |
| `openjpeg` | JPEG 2000 load/save. | Required for JP2 source/input and response/output. |
| `png` | PNG load/save. | Required for PNG source/input and response/output. |
| `spng` | libspng PNG codec support. | Keeps PNG support fast and modern where libvips uses SPNG. |
| `tiff` | TIFF load/save. | Required for common preservation/master source images and TIFF output. |
| `webp` | WebP load/save. | Required for WebP source/input and response/output. |
| `zlib` | Deflate compression support. | Required by PNG/TIFF-related compression paths. |

When `vips.block_untrusted` is enabled, Triplet still unblocks the libvips
JPEG, PNG, WebP, GIF, JPEG 2000, and TIFF load/save operations because these
are the image source and response formats Triplet intentionally serves.
Operators can re-block those classes with `vips.blocked_operations` if their
deployment does not accept a format.

```yaml
vips:
  block_untrusted: true
  blocked_operations:
    - VipsForeignLoadPdf
```

## Disabled

| Option | What disabling means | libvips implication |
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
