# Video fixtures

Synthetic, made once and committed. Used by `internal/platform/darwin/avmedia`.

| File | Content |
|---|---|
| `clip.mov` | 2 s, 64×36, 10 fps, H.264, every frame a keyframe; QuickTime metadata `make=TESTCAM`, `model=T1`, `location.ISO6709=+10.5000+020.2500/`, `creationdate=2026-01-02T03:04:05Z`; movie header (`mvhd`) creation and modification times 2026-01-02T03:04:05Z. The red channel grows with the frame index, so frame order is visible. |
| `audio-only.mov` | 1 s sine, AAC, no video track. ffmpeg writes its version as user data `©swr` (AVFoundation: common key `software`). |
| `encoders.mov` | 0.2 s of the same video; only metadata QuickTime user data `©too` = `TOOL-TOO`, then `©enc` = `TOOL-ENC`, in that file order (AVFoundation reports items in file order), so a reader must prefer `©enc` by key, not by position. |
| `encoder.mp4` | 0.1 s sine, AAC, MP4. ffmpeg writes its version as an iTunes `©too` item (`itsk/%A9too`, common key `software`), the key space where drones put their encoder. |

## clip.mov

The command the plan names first was

```sh
ffmpeg -f lavfi -i testsrc=d=2:s=64x36:r=10 -movflags use_metadata_tags \
  -metadata com.apple.quicktime.make=TESTCAM -metadata com.apple.quicktime.model=T1 \
  -metadata com.apple.quicktime.location.ISO6709=+10.5000+020.2500/ \
  -metadata creation_time=2026-01-02T03:04:05Z \
  -c:v libx264 -pix_fmt yuv420p clip.mov
```

but AVFoundation does not report `make` for that file: ffmpeg (8.1) puts the
`mdta` keys inside `moov/udta/meta`, and AVFoundation reads them as unnamed
iTunes items (`itsk/%00%00%00%01` …). The fixture is therefore written with
AVAssetWriter and `AVMetadataItem`s for the same four keys:

```sh
swift testdata/media/_make_clip.swift testdata/media/clip.mov
```

AVAssetWriter stamps the movie header (`moov/mvhd`) with the current time,
so the creation and modification times are then fixed to the fixture's
date with `_patch_mvhd.go`, which rewrites only those two fields (8 bytes of
a version-0 `mvhd`). `internal/meta`'s `TestReadMovieHeader_Clip` expects
2026-01-02T03:04:05Z. The track and media headers keep the time the file was
written. From the repository root:

```sh
tmp=$(mktemp -d)
cp testdata/media/_patch_mvhd.go "$tmp/patch_mvhd.go"
go run "$tmp/patch_mvhd.go" testdata/media/clip.mov 2026-01-02T03:04:05Z
```

## encoders.mov

```sh
swift testdata/media/_make_clip.swift testdata/media/encoders.mov encoders
```

AVAssetWriter always writes `©enc` before `©too`, whatever the order of
`writer.metadata`; the script then swaps the two equal-sized `udta` atoms in
place (no offsets change) so `©too` comes first.

## audio-only.mov

```sh
ffmpeg -f lavfi -i sine=d=1 audio-only.mov
```

## encoder.mp4

```sh
ffmpeg -f lavfi -i sine=d=0.1 encoder.mp4
```

ffmpeg replaces a user-supplied `-metadata encoder=…` with its own version
string (and `-fflags +bitexact` drops the item), so tests expect the prefix
`Lavf`.
