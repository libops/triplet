# IIIF benchmark fixtures

Place source images in this directory before running the benchmark harness.
Nested directories are supported; identifiers are URL-encoded relative paths.

Run this to download the default public fixture suite:

```sh
make benchmark-fixtures
```

The default suite includes:

- IIIF validator color grid PNG
- small/medium/large RGB TIFFs from FileSamples
- valid JP2 fixtures from the jpylyzer test corpus
- any local files you add under this directory

Downloaded fixtures are stored in `fixtures/benchmark/images/` and ignored by
git. Your local 1 GiB TIFF can be added anywhere under `fixtures/benchmark/`;
the benchmark discovers files recursively.

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
# Benchmark: Triplet vs Cantaloupe

> **Run ID:** `20260426T191109Z`
> **Date:** 2026-04-26
> **Images:** 6
> **Request types:** 9
> **Passes:** 5 (1 warmup)
> **Triplet:** `triplet-benchmark:dev` · color management: `preserve` · load access: `auto`
> **Cantaloupe:** `islandora/cantaloupe:main`

---

## Success Rate

| Server | Overall | Notes |
|---|---|---|
| **Triplet** | **270/270 (100%)** | Zero failures across all modes and concurrency levels |
| Cantaloupe | 255/270 (94%) | `large_jpg` HTTP 400 (10/run), `square_jpg` HTTP 400 (5/run) |

Cantaloupe's failures are consistent across all six runs and unrelated to load — they represent specific request/image combinations that return 400.

---

## Latency: Uncached

All derivatives computed from scratch on every request.

### Overall median latency (ms)

| Concurrency | Triplet | Cantaloupe | Triplet advantage |
|---|---|---|---|
| c1 | 51.6 | 85.3 | **1.7×** faster |
| c4 | 96.1 | 152.0 | **1.6×** faster |
| c8 | 260.8 | 962.5 | **3.7×** faster |

### Overall p99 latency (ms)

| Concurrency | Triplet | Cantaloupe | Triplet advantage |
|---|---|---|---|
| c1 | 564.5 | 2,262.6 | **4.0×** faster |
| c4 | 1,330.7 | 5,602.1 | **4.2×** faster |
| c8 | 3,700.3 | 18,512.3 | **5.0×** faster |

Cantaloupe's uncached p99 at c8 reaches **18.5 seconds**. Triplet's is 3.7 seconds — substantially better, though neither server should be serving cold derivatives at c8 in production without additional infrastructure.

### Uncached speedup by request type (Triplet median ÷ Cantaloupe median)

| Request | c1 | c4 | c8 |
|---|---|---|---|
| `square_jpg` | 3.15× faster | 3.52× faster | **10.55×** faster |
| `large_jpg` | 3.95× faster | 3.16× faster | 5.50× faster |
| `rotate_jpg` | 2.87× faster | 2.07× faster | 4.29× faster |
| `bitonal_png` | 3.12× faster | 2.23× faster | 4.94× faster |
| `gray_jpg` | 2.33× faster | 2.18× faster | 4.19× faster |
| `full_jpg` | 1.69× faster | 2.38× faster | 3.88× faster |
| `info` | 1.78× faster | 1.87× faster | 5.26× faster |
| `thumb_jpg` | 1.71× faster | 1.51× faster | 2.45× faster |
| `crop_jpg` | 1.16× faster | 1.20× faster | 1.94× faster |

Triplet is faster on every request type at every concurrency level. The advantage compounds with concurrency — at c8 `square_jpg` is over 10× faster and `large_jpg` is 5.5× faster.

---

## Latency: Cached

Derivative caches warm. Both servers serve pre-computed files.

### Overall median latency (ms)

| Concurrency | Triplet | Cantaloupe | Triplet advantage |
|---|---|---|---|
| c1 | 7.3 | 10.1 | **1.4×** faster |
| c4 | 9.4 | 12.4 | **1.3×** faster |
| c8 | 13.2 | 19.8 | **1.5×** faster |

### Overall p99 latency (ms)

| Concurrency | Triplet | Cantaloupe | Triplet advantage |
|---|---|---|---|
| c1 | 52.2 | 66.7 | **1.3×** faster |
| c4 | 76.4 | 77.6 | ~equal |
| c8 | 127.9 | 161.8 | **1.3×** faster |

### Cached speedup by request type at c8

| Request | Triplet med | Cantaloupe med | Speedup |
|---|---|---|---|
| `info` | 8.6 ms | 17.5 ms | **2.1×** faster |
| `thumb_jpg` | 15.6 ms | 27.1 ms | **1.7×** faster |
| `square_jpg` | 12.1 ms | 19.6 ms | **1.6×** faster |
| `large_jpg` | 20.9 ms | 35.9 ms | **1.7×** faster |
| `full_jpg` | 36.8 ms | 65.6 ms | **1.8×** faster |
| `bitonal_png` | 11.5 ms | 17.1 ms | **1.5×** faster |
| `crop_jpg` | 11.9 ms | 17.9 ms | **1.5×** faster |
| `rotate_jpg` | 14.4 ms | 21.5 ms | **1.5×** faster |
| `gray_jpg` | 13.1 ms | 18.3 ms | **1.4×** faster |

Triplet wins every cached request type at every concurrency level.

---

## JPEG2000 Performance

Both servers serve JP2 sources successfully at 100%.

### JP2 uncached — per-image median latency (ms)

| Image | Triplet | Cantaloupe | Winner |
|---|---|---|---|
| `jpylyzer-openjpeg15.jp2` (c1) | 56.1 | 187.4 | Triplet **3.3×** faster |
| `jpylyzer-reference.jp2` (c1) | 97.1 | 237.2 | Triplet **2.4×** faster |
| `jpylyzer-openjpeg15.jp2` (c4) | 163.2 | 433.4 | Triplet **2.7×** faster |
| `jpylyzer-reference.jp2` (c4) | 186.8 | 590.6 | Triplet **3.2×** faster |
| `jpylyzer-openjpeg15.jp2` (c8) | 483.8 | 1,794.2 | Triplet **3.7×** faster |
| `jpylyzer-reference.jp2` (c8) | 324.7 | 1,852.7 | Triplet **5.7×** faster |

### JP2 cached — per-image median latency (ms)

| Image | Triplet | Cantaloupe | Winner |
|---|---|---|---|
| `jpylyzer-openjpeg15.jp2` (c1) | 8.1 | 11.4 | Triplet **1.4×** faster |
| `jpylyzer-reference.jp2` (c1) | 8.3 | 11.0 | Triplet **1.3×** faster |
| `jpylyzer-openjpeg15.jp2` (c8) | 14.9 | 24.1 | Triplet **1.6×** faster |
| `jpylyzer-reference.jp2` (c8) | 17.7 | 27.2 | Triplet **1.5×** faster |

Triplet is faster than Cantaloupe on JP2 in both cached and uncached modes. Triplet's JP2 output is also 1.16–1.17× smaller.

---

## Resource Usage

### CPU — uncached

| Server | c1 mean | c4 mean | c8 mean | CPU-sec / req (c1/c4/c8) |
|---|---|---|---|---|
| Triplet | 22.4% | 84.2% | 80.0% | 0.12 / 0.23 / 0.36 |
| Cantaloupe | 75.7% | 269.7% | 262.5% | 0.43 / 0.79 / 1.24 |

Cantaloupe consumes **3–3.5× more CPU per request** than Triplet uncached. At c4 and c8 it saturates multiple cores, which directly explains its latency degradation under concurrent load.

### CPU — cached

| Server | c1 mean | c4 mean | c8 mean | CPU-sec / req (c1/c4/c8) |
|---|---|---|---|---|
| Triplet | 2.5% | 10.9% | 17.0% | 0.007 / 0.009 / 0.010 |
| Cantaloupe | 19.9% | 45.1% | 97.7% | 0.062 / 0.037 / 0.063 |

Cantaloupe cached at c8 consumes 97.7% mean CPU — nearly a full core — for serving pre-computed derivatives. Triplet's cached CPU cost is approximately **6× lower**.

### Memory

| Mode | Server | c1 mean MiB | c4 mean MiB | c8 mean MiB | c8 max MiB |
|---|---|---|---|---|---|
| Uncached | Triplet | 252 | 362 | 459 | 659 |
| Uncached | Cantaloupe | 2,589 | 2,833 | 2,805 | 3,873 |
| Cached | Triplet | 200 | 278 | 479 | 479 |
| Cached | Cantaloupe | 1,196 | 1,252 | 1,021 | 1,022 |

Cantaloupe's uncached memory footprint is **6–11× larger** than Triplet's. At c8 uncached, Cantaloupe peaked at **3.9 GiB**; Triplet at **659 MiB**.

---

## Response Size

| Request | Triplet | Cantaloupe | Notes |
|---|---|---|---|
| `full_jpg` | 1.3 MiB | 960.3 KiB | Triplet **1.36× larger** |
| `large_jpg` | 161.0 KiB | 232.2 KiB | Triplet **1.44× smaller** |
| `bitonal_png` | 10.9 KiB | 16.8 KiB | Triplet **1.54× smaller** |
| `crop_jpg` | 35.7 KiB | 43.7 KiB | Triplet **1.22× smaller** |
| `square_jpg` | 43.4 KiB | 49.6 KiB | Triplet **1.14× smaller** |
| `rotate_jpg` | 45.0 KiB | 51.0 KiB | Triplet **1.13× smaller** |
| `thumb_jpg` | 12.6 KiB | 14.5 KiB | Triplet **1.15× smaller** |
| `gray_jpg` | 40.2 KiB | 43.3 KiB | Triplet **1.08× smaller** |
| `info` | 711 B | 776 B | Triplet **1.09× smaller** |
| `jpylyzer-openjpeg15.jp2` (all types) | — | — | Triplet **1.16× smaller** |
| `jpylyzer-reference.jp2` (all types) | — | — | Triplet **1.17× smaller** |

`full_jpg` is the only type where Cantaloupe produces smaller output. The discrepancy (1.3 MiB vs 960 KiB) is driven by the large 5184×3456 TIFF source and likely reflects a JPEG quality setting difference. All other types — including both JP2 sources — are smaller from Triplet.

---

## Scaling Behaviour

```
Uncached median latency as concurrency increases:

Triplet:      c1=52ms   →  c4=96ms   →  c8=261ms   (5.0× growth)
Cantaloupe:   c1=85ms   →  c4=152ms  →  c8=963ms   (11.3× growth)

Cached median latency as concurrency increases:

Triplet:      c1=7.3ms  →  c4=9.4ms  →  c8=13.2ms  (1.8× growth)
Cantaloupe:   c1=10.1ms →  c4=12.4ms →  c8=19.8ms  (2.0× growth)
```

Triplet's uncached latency grows 5× from c1 to c8. Cantaloupe's grows 11.3×. The difference is Cantaloupe's CPU per request — at c4 it is already consuming 270% mean CPU, leaving no headroom as concurrency increases. Cached, both servers scale well within the tested range.

---

## Per-Image Summary

| Image | Triplet success | Cantaloupe success | Triplet uncached c8 med | Cantaloupe uncached c8 med | Notes |
|---|---|---|---|---|---|
| `filesamples-1920x1280.tiff` | 100% | 100% | 228.6 ms | 788.2 ms | Triplet 3.5× faster; 1.09× smaller |
| `filesamples-5184x3456.tiff` | 100% | 100% | 602.6 ms | 2,933.1 ms | Triplet 4.9× faster; output **2.25× larger** |
| `filesamples-640x426.tiff` | 100% | 78% | 94.2 ms | 285.3 ms | Triplet 3.0× faster; output 1.42× larger |
| `iiif-validator-grid.png` | 100% | 89% | 180.2 ms | 360.4 ms | Triplet 2.0× faster; output 1.12× larger |
| `jpylyzer-openjpeg15.jp2` | 100% | 100% | 483.8 ms | 1,794.2 ms | Triplet 3.7× faster; 1.16× smaller |
| `jpylyzer-reference.jp2` | 100% | 100% | 324.7 ms | 1,852.7 ms | Triplet 5.7× faster; 1.17× smaller |

The 5184×3456 TIFF is the only image where Triplet's output is significantly larger (`full_jpg` derivative specifically). Triplet leads on latency for every image at every concurrency level.

---

## Summary

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

---

*Generated 2026-04-26 · run `20260426T191109Z`*
