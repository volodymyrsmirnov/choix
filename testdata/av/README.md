# testdata/av

AVFoundation property dictionaries (as returned by `platform.Video.Properties`)
for the tests of `internal/meta/av_map.go`. `internal/meta/fixtures_test.go`
keeps them to the allow-list (`make`, `model`, `software`, `encoder`,
`location`, `width`, `height`, `duration`) and the fixed synthetic values.

| File | Origin |
|---|---|
| `dji_mp4.json` | Captured by spike S4 from a DJI Air3s MP4 and reduced to the allow-list; it carries only size, duration and the encoder string (`©too`), no serial, date or location. |
| `iphone_mov.json` | **Synthetic.** Captured with the real AVFoundation reader from `testdata/media/clip.mov` (itself synthetic), then make and model set to `Apple` / `iPhone 15 Pro`. Location is the fixture's fixed `+10.5000+020.2500/`. Not from a device. |
