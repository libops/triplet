# Benchmarks

Triplet's benchmark suite exercises representative IIIF Image API requests
across source formats, cache states, and concurrency levels. The full results
are in
[`fixtures/benchmark/README.md`](https://github.com/libops/triplet/blob/main/fixtures/benchmark/README.md).

Benchmarks are most useful when the source and cache locations are explicit:

```yaml
sources:
  default: file
  file:
    root: ./fixtures/benchmark/images

cache:
  root: ./deploy/compose/cache
  source_root: ./deploy/compose/source-cache

iiif:
  image:
    max_output_pixels: 100000000
    max_source_pixels: 250000000
    max_concurrent_transforms: 4
```

## Summary

| Dimension | Result |
|---|---|
| Success rate | 100% across the benchmark request matrix. |
| Uncached latency | Median latency stays in the low single-digit second range for the tested transforms, with p99 latency remaining bounded under concurrent load. |
| Cached latency | Cached responses return substantially faster than uncached transforms because encoded derivatives are served from Triplet's cache layer. |
| JP2 support | JP2 source images complete successfully when the runtime libvips build includes OpenJPEG. |
| Concurrency | Triplet handles concurrent transform traffic with bounded latency growth across the tested concurrency levels. |
| CPU efficiency | Cached responses avoid repeat transform work; uncached responses keep CPU use proportional to requested image operations. |
| Memory footprint | Runtime memory remains bounded across source formats and concurrency levels in the benchmark matrix. |
| Output size | Derivative output sizes are tracked per request type so format and quality changes can be evaluated over time. |

## Running Benchmarks

Use the project Make targets to regenerate benchmark fixtures and run the IIIF
benchmark suite:

```bash
make benchmark-fixtures
make benchmark-iiif
```

Pull request benchmark runs use:

```bash
make benchmark-iiif-pr
```
