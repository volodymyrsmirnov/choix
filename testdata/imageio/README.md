# testdata/imageio

ImageIO property dictionaries (`CGImageSourceCopyPropertiesAtIndex`, as
returned by `platform.Stills.Properties`) for the tests of
`internal/meta/imageio_map.go`. Every file is reduced to the keys the mapper
reads, and `internal/meta/fixtures_test.go` enforces that: serials are
`TESTSERIAL01`, dates `2026:01:02 03:04:05`, latitude/longitude 10.5 / 20.25,
and no maker-note blobs, owner or lens-serial values. Make and model are kept.

| File | Origin |
|---|---|
| `fuji_raf_jpeg.json` | Captured by spike S4 from the embedded JPEG of a FUJIFILM X100VI RAF, then scrubbed: serial and dates replaced by the fixed values above, everything outside the allow-list removed. |
| `plain_jpeg.json` | Captured by spike S4 from a JPEG without EXIF; only `PixelWidth`/`PixelHeight` remain. |
| `iphone_heic.json` | **Synthetic.** Captured with the real ImageIO reader from a synthetic HEIC: `testdata/media/sample.heic` with `exiftool` writing make `Apple`, model `iPhone 15 Pro`, the fixed dates, ISO 64, f/1.78, 1/250 s, 6.86 mm, `ExifImageWidth/Height` 800×600 and GPS 10.5 N / 20.25 E. Reduced to the allow-list. Not from a device. |
| `dng_container.json` | **Hand-written from documented keys, not captured from a device.** ImageIO cannot read the synthetic DNG of `platformtest.MakeDNG` (its raw strip is not real sensor data), so the dictionary is written by hand with Apple's documented key names (`{TIFF}`, `{Exif}`, `{GPS}`, `PixelWidth/Height`). Make `TESTCAM` and model `D1` follow `MakeDNG`; 6000×4000 stands for a container's sensor size. |

Nothing here comes from a real photo other than the two spike captures,
which keep only make, model, pixel sizes and exposure values.
