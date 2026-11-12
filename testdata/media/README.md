# testdata/media

Synthetic fixtures for `internal/platform/darwin/imageio`. Nothing here comes
from a real photo.

| File | What it is |
|---|---|
| `orient6.jpg` | 64×32 stored pixels, left half red, right half blue, EXIF Orientation 6. Shown upright it is 32×64 with the top half red. |
| `nan-exif.jpg` | 64×32 JPEG whose EXIF `ExposureTime` rational is 1/0. `FNumber` (2.8), `ISOSpeedRatings` (200) and IFD0 `Make` (`TESTCAM`) are ordinary. |
| `sample.heic` | 800×600 HEIC of the gradient `platformtest.MakeJPEG(t, 800, 600)` draws. |

## Regenerating

`orient6.jpg` and `nan-exif.jpg` come from `_make_fixtures.go`. The Go tool
skips files whose names start with `_`, so copy it before running it, from
the repository root:

```sh
tmp=$(mktemp -d)
cp testdata/media/_make_fixtures.go "$tmp/make_fixtures.go"
go run "$tmp/make_fixtures.go"
```

`sample.heic` was made once with `sips` from the same gradient, which the
generator writes with `-heic-source`:

```sh
go run "$tmp/make_fixtures.go" -heic-source "$tmp/gradient.jpg"
sips -s format heic "$tmp/gradient.jpg" --out testdata/media/sample.heic
```
