# Choix 2.0 — spike results

Superseded in part by `2026-11-12-builtin-grouping-and-quality-design.md`: the models, ONNX Runtime and the download are gone.

Measured answers to the questions in §12 of `2026-11-11-choix-2-desktop-design.md`, one section per spike (question, what was run, raw output, PASS/FAIL, consequence). The spike code is throwaway and lives outside the repository. Nothing here identifies the private photo archive the measurements were taken on: folders are `folder-N`, cameras are named by make and model only, and no file name, serial number, date, location or archive count appears.

Machine: Apple Silicon (arm64), macOS 27.2, Xcode 27.2, Go 1.26.5, Wails v3.1.0.

## S4 — Camera serial numbers through ImageIO; fixture dictionaries

Apple Silicon (arm64), macOS 27.2, Go 1.26.5, the owner's corpus opened read-only.

### Question

Does ImageIO expose the serial that v1 puts in `device_key`, and under which key? v1 uses the first non-empty value of `MakerNotes:SerialNumber`, `EXIF:SerialNumber` and `MakerNotes:InternalSerialNumber`. Does AVFoundation give Task 14's video values? Which ImageIO keys feed the rest of Task 19's table?

### What was run

```sh
exiftool -q -r -fast -i .choix -p '$Make|$Model|$FileType' <ROOT>     # bodies and formats
s4 serials <files…>   # exiftool's 3 v1 keys vs every ImageIO key containing "Serial"
s4 both <files…>      # full exiftool record + ImageIO dump (RAF: also the JPEG at header offsets 84/88) or AVFoundation dump
s4 iiserial < <list>  # ImageIO serial keys for every Fujifilm file, joined with an exiftool sweep of the same files
s4 mkjpeg <scratch>/gps.jpg   # synthetic JPEG (not corpus) to check the GPS and lens keys
python3 -I scrub.py           # writes fixtures/
```

`s4` is cgo plus Objective-C. Originals are opened only through `os.Open`, `CGImageSourceCreateWithURL` (`ShouldCache = NO`) and `AVURLAsset`. The comparison used 20 files per body and format; the serial check ran on every Fujifilm file.

### Raw output

**Bodies and formats evaluated:**
- FUJIFILM X100VI: RAF, in-camera JPEG, and JPEGs re-exported by an editor.
- JPEGs with no EXIF at all, which v1 keys as `"Unknown"`.
- DJI Air3s MP4s; the device appears only in `QuickTime:Encoder`.
- An MP4 written by ffmpeg (`Encoder = Lavf…`).

**Not evaluated in this spike** (no sample was available to it): iPhone HEIC/MOV, DNG, other makers, and GPS or lens tags written by a camera. The GPS and lens keys were checked on a synthetic JPEG only.

**Serials:**
- In every Fujifilm file checked, v1's serial came from `EXIF:SerialNumber`, and it equals `{Exif}.BodySerialNumber` in the RAF container, the RAF embedded JPEG and the JPEGs.
- The RAF container and the editor re-exports also carry `{ExifAux}.SerialNumber` with the same value.
- Exiftool never reports `MakerNotes:SerialNumber` for Fujifilm. ImageIO has no `{MakerFuji}` dictionary and no value equal to `MakerNotes:InternalSerialNumber`; `{ExifAux}.SerialNumber` is not that value.
- MP4s carry no serial key in exiftool.

Side by side, per body and format (`<serial>` is the body serial, `<internal>` a different maker-note value; no real value is shown):

| Body / format | exiftool `MakerNotes:SerialNumber` | exiftool `EXIF:SerialNumber` | exiftool `MakerNotes:InternalSerialNumber` | ImageIO `{Exif}.BodySerialNumber` | ImageIO `{ExifAux}.SerialNumber` | other ImageIO `*Serial*` keys |
|---|---|---|---|---|---|---|
| X100VI RAF, container | absent | `<serial>` | `<internal>` | = `<serial>` | = `<serial>` | none |
| X100VI RAF, embedded JPEG | absent | `<serial>` | `<internal>` | = `<serial>` | absent | none |
| X100VI JPEG, in-camera | absent | `<serial>` | `<internal>` | = `<serial>` | absent | none |
| X100VI JPEG, re-exported | absent | `<serial>` | not compared | = `<serial>` | = `<serial>` | none |
| JPEG without EXIF | absent | absent | absent | absent | absent | none |
| DJI Air3s MP4, ffmpeg-written MP4 | absent | absent | absent | n/a (AVFoundation exposes no serial) | n/a | none |

**RAF container vs embedded JPEG** (the JPEG at header offsets 84/88):
- **Identical:** Make, Model, `DateTime`, `Orientation`, `Software`, `BodySerialNumber`, `PixelX/YDimension` (4416×2944), ISO, `FNumber`, `ExposureTime`, `FocalLength`, `DateTimeOriginal`, `DateTimeDigitized`, `SubsecTime*`.
- **Container only:** `{ExifAux}.SerialNumber` and `{TIFF}.Compression`.
- **Embedded JPEG only:** `{Exif}.OffsetTime`, `OffsetTimeOriginal` and `OffsetTimeDigitized`, `CompositeImage`, IPTC, DPI keys, and, in some files, `{TIFF}.Artist` and `Copyright`.
- **Different values:** `PixelWidth/Height` (the container gives the sensor's 7728×5152; the embedded JPEG gives 4416×2944, which is exiftool's `File:ImageWidth/Height`), `Depth` (16 vs 8), `ProfileName`, and `AFInfo` (rounding).
- **Speed:** properties of the embedded JPEG take about 3.5 ms median, against 6.5–9 ms for the container (loaded machine; see S7).

**Each Task 19 field, exiftool vs ImageIO,** over all sampled files. `eq` means equal in every file; `—` means absent on both sides.

| exiftool | ImageIO | RAF JPEG | RAF container | X100VI JPEG | re-exported JPEG | no-EXIF JPEG |
|---|---|---|---|---|---|---|
| `EXIF:Make/Model` | `{TIFF}.Make/Model` | eq | eq | eq | eq | — |
| `EXIF:SerialNumber` | `{Exif}.BodySerialNumber` | eq | eq | eq | eq | — |
| `EXIF:ExifImageWidth/Height` | `{Exif}.PixelX/YDimension` | eq | eq | eq | — | — |
| `File:ImageWidth/Height` | `PixelWidth/Height` | eq | **differs** | eq | eq | eq |
| `EXIF:ISO` | `{Exif}.ISOSpeedRatings[0]` | eq | eq | eq | eq | — |
| `EXIF:FNumber/ExposureTime/FocalLength` | `{Exif}.` same names | eq | eq | eq | eq | — |
| `EXIF:DateTimeOriginal/CreateDate/ModifyDate` | `{Exif}.DateTimeOriginal/DateTimeDigitized`, `{TIFF}.DateTime` | eq | eq | eq | eq | — |
| `EXIF:OffsetTime/OffsetTimeOriginal` | `{Exif}.OffsetTime/OffsetTimeOriginal` | eq | **missing** | eq | eq | — |
| `EXIF:SubSecTimeOriginal` | `{Exif}.SubsecTimeOriginal` | eq | eq | eq | eq | — |
| `EXIF:Orientation` | `{TIFF}.Orientation` | eq | eq | eq | eq | — |

GPS and lens were confirmed on the synthetic JPEG only:
- `EXIF:GPSLatitude/Longitude` ← `{GPS}.Latitude/Longitude`. ImageIO gives an unsigned magnitude, which is the same as exiftool's `-n` value.
- `EXIF:GPSLatitudeRef/LongitudeRef` ← `{GPS}.LatitudeRef/LongitudeRef`.
- `EXIF:LensModel` ← `{Exif}.LensModel`.

**Video (AVFoundation vs exiftool):**
- **Size and duration:** the first video track's `naturalSize` equals `QuickTime:ImageWidth/Height`, and `asset.duration` equals `QuickTime:Duration` (±0.05 s), in every file.
- **DJI MP4:** `QuickTime:Encoder` equals the item with key `©too` in the **iTunes key space** (`itsk/%A9too`, `commonKey = software`). It is not in user data, and there is no `©enc`.
- **ffmpeg MP4:** the `mdta`-keyed `ilst` comes back as `itsk/0x00000001…` with its names lost, so there is no encoder.
- **Absent everywhere:** `com.apple.quicktime.make/model/location` and GPS.

### PASS / FAIL

| Body | Formats | v1 serial source | Native source | Result |
|---|---|---|---|---|
| FUJIFILM X100VI | RAF (embedded JPEG), JPEG, re-exported JPEG | `EXIF:SerialNumber` | `{Exif}.BodySerialNumber`, equal in every file | **PASS** |
| DJI Air3s | MP4 | none (`"DJI Air3s#"`) | no serial on either side; the encoder string comes from AVFoundation `©too` / `commonKey software` | serial: **N/A**; `device_key` parity through the encoder: **PASS** |
| no make/model | JPEG | none (`"Unknown"`) | none | serial: **N/A**; `device_key` equal |
| ffmpeg-written | MP4 | none (`"Unknown"`) | none (the raw `Encoder` field differs, `device_key` does not) | serial: **N/A**; `device_key` equal |
| iPhone, DNG, other makers | HEIC, MOV, DNG | — | — | **NOT TESTED**: not evaluated in this spike |

### Consequence

For the bodies and formats evaluated (Fujifilm RAF and JPEG, DJI MP4, files without metadata) no S4 fallback is needed: no maker-note reader in Go and no `device_key` backfill. iPhone HEIC/MOV, DNG and other makers are unresolved; the parity test of Task 19 decides them the first time such files are in a corpus, and the spec's S4 fallbacks stay available for them.

### Recorded for later tasks

**For `imageio_map.go` (Task 19):**

| Scope | exiftool key | Source |
|---|---|---|
| all stills | `EXIF:Make`, `EXIF:Model` | `{TIFF}.Make`, `{TIFF}.Model` |
| FUJIFILM | `EXIF:SerialNumber` | `{Exif}.BodySerialNumber`; fallback `{ExifAux}.SerialNumber` (same value) |
| FUJIFILM | `MakerNotes:SerialNumber`, `MakerNotes:InternalSerialNumber` | **none**: do not emit |
| all stills | `EXIF:ExifImageWidth/Height` | `{Exif}.PixelXDimension/PixelYDimension` |
| stills (RAF: embedded JPEG only) | `File:ImageWidth/Height` | `PixelWidth/PixelHeight` |
| all stills | `EXIF:ISO` | `{Exif}.ISOSpeedRatings[0]` |
| all stills | `EXIF:FNumber`, `EXIF:ExposureTime`, `EXIF:FocalLength` | `{Exif}.FNumber`, `{Exif}.ExposureTime`, `{Exif}.FocalLength` |
| all stills | `EXIF:DateTimeOriginal`, `EXIF:CreateDate`, `EXIF:ModifyDate` | `{Exif}.DateTimeOriginal`, `{Exif}.DateTimeDigitized`, `{TIFF}.DateTime` |
| stills (RAF: embedded JPEG only) | `EXIF:OffsetTime`, `EXIF:OffsetTimeOriginal` | `{Exif}.OffsetTime`, `{Exif}.OffsetTimeOriginal` |
| all stills | `EXIF:SubSecTimeOriginal`, `EXIF:Orientation` | `{Exif}.SubsecTimeOriginal`, `{TIFF}.Orientation` |
| synthetic only | `EXIF:GPSLatitude`/`Ref`, `EXIF:GPSLongitude`/`Ref`, `EXIF:LensModel` | `{GPS}.Latitude`/`LatitudeRef`, `{GPS}.Longitude`/`LongitudeRef`, `{Exif}.LensModel` |
| DJI MP4 | `QuickTime:Encoder` | item key `©too`, key space `itsk` (= `commonKey software`) |
| all video | `QuickTime:ImageWidth/Height`, `QuickTime:Duration` | first video track `naturalSize`, `asset.duration` |

**Corrections:**
- **Task 19 starting point:** "`{ExifAux}.SerialNumber` → `MakerNotes:SerialNumber`" is wrong for Fujifilm. That key carries the body serial, so use it only as a fallback for `EXIF:SerialNumber`.
- **RAF metadata source:** reading it from the embedded JPEG is confirmed. Of the two sources, it is the only one with `OffsetTime*` and exiftool's pixel size.
- **Task 14:** match `©enc`/`©too` by key across all of `asset.metadata`, not only `AVMetadataKeySpaceQuickTimeUserData`. Otherwise Task 19's `encoder (else software)` fallback is what carries DJI.
- **Still unverified:** the parity test must cover iPhone HEIC/MOV, DNG container and preview, and other makers once such files are available.

**Fixtures** (`<SPIKE>/s4/fixtures/`, for `testdata/`):
- **Scrubbing:** each fixture is reduced to Task 19's allow-list. Serials become `TESTSERIAL01` and every date becomes `2026:01:02 03:04:05`. Owner, lens-serial and maker-note values are removed. Make and model are kept.
- **Written:**
  - `fuji_raf_jpeg.json` (RAF embedded JPEG);
  - `plain_jpeg.json` (no EXIF; only `PixelWidth/Height`);
  - `dji_mp4.json` (Task 14's `Properties` shape: `duration`, `width`, `height`, `software`, `encoder`).
- **Not written, because no sample was available:** `iphone_heic.json`, `dng_container.json`, `iphone_mov.json`.
- **No GPS:** the written fixtures carry no GPS, so the GPS and location tests must build their inputs inline.
- **Leak check:** every serial, date and file or folder name in the raw outputs was searched for in the fixtures. None was found.

## S7 — Embedded-JPEG speed for RAF and DNG; DNG preview, orientation and metadata

Apple Silicon (arm64, 10 cores, 32 GB), macOS 27.2, Go 1.26.5, the owner's corpus opened read-only. The machine was heavily loaded by unrelated work during every run (load average 41–77), so wall-clock times include queueing; CPU time (user + system, all threads) is given next to them.

### Question

1. How fast is (a) reading the embedded JPEG plus (b) `CGImageSourceCreateThumbnailAtIndex` at 256, 1600 and 4096, for RAF and DNG?
2. Does every DNG have a preview at least 1600 wide?
3. Where are a DNG's orientation and metadata?
4. Are DNG container properties under 50 ms?

### What was run

```sh
s7 selftest                # DNG IFD walker on a synthetic in-memory DNG (no corpus access)
s7 pickdir <ROOT>          # smallest folder with >= 50 RAFs -> folder-1
s7 raf <folder-1> 50 5     # first 50 RAF files (sorted); full RAW decode for five files; run three times
s7 dng <ROOT>              # every DNG under the root
```

**(a):**
- **RAF:** `os.Open` (read-only), then `ReadAt` of the range the header gives at offsets 84 (offset) and 88 (length).
- **DNG:** walk IFD0, its chained IFDs and every SubIFD. A candidate needs `NewSubFileType` bit 0, `Compression` 6 or 7, and `PhotometricInterpretation` 6 or 2.

**(b):** `CGImageSourceCreateWithData` with `FromImageAlways`, `MaxPixelSize` (long side), `WithTransform` and `ShouldCacheImmediately`, so the decode happens inside the timed call.

**Full decode:** the same call on the RAF's URL. The page cache was warm.

### Raw output

**FUJIFILM X100VI, 50 RAF files:**
- The embedded JPEG is 4416×2944 in every file, median 5.5 MB. The container reports 7728×5152.
- Thumbnails come out at 256×171, 1600×1067 and 4096×2731 (transposed for portrait shots).

All times in ms, given as median / p95. The wall-clock range runs over three runs; CPU time is from run 3.

| Step | Wall median (runs 1–3) | Wall p95 (runs 1–3) | CPU median / p95 |
|---|---|---|---|
| (a) read embedded JPEG | 3.4 | 25–55 | 2.1 / 4.5 |
| (b) thumbnail 256 | 353–580 | 953–1564 | 59.6 / 66.4 |
| (b) thumbnail 1600 | 166–404 | 787–1193 | 54.2 / 70.4 |
| (b) thumbnail 4096 | 894–2129 | 3018–4294 | 225.1 / 372.5 |
| **(a)+(b) at 1600** | **167–436** | 798–1219 | **57.0 / 71.9** |
| properties, embedded JPEG (bytes) | 3.5–3.6 | 28–127 | 2.0 / 3.3 |
| properties, RAF container (path) | 6.5–9.3 | 61–265 | 4.6 / 5.5 |
| full ImageIO RAW decode at 1600 (five files) | 584–1632 | 1002–4579 | 289.4 / 436.0 |

Peak RSS of the single-threaded spike was under 0.5 GB.

**DNG:** not evaluated — no DNG sample was available to the spike. None of the DNG measurements could be taken: IFD0 `Orientation`, the preview's own orientation, whether the preview is upright only after applying IFD0's value, where the metadata lives, missing previews, container-properties time.

The walker passes its self-test on a synthetic DNG: IFD0 RGB thumbnail with orientation 6, CFA SubIFD, a 1600-wide JPEG SubIFD and a chained 256-wide JPEG IFD. It finds exactly the two JPEG previews, at "SubIFD1 of IFD0" and "chained IFD1". Both decode, and IFD0 orientation reads as 6.

### PASS / FAIL

| Criterion | Result |
|---|---|
| Median (a)+(b) at 1600 ≤ 0.5 s per RAF | **PASS**: wall median 167–436 ms under heavy load; CPU median 57 ms |
| Every DNG has a preview ≥ 1600 wide | **NOT RUN**: no DNG sample available |
| DNG container properties < 50 ms, no pixel decode | **NOT RUN**: no DNG sample available. Stand-in: RAF container properties take 6.5 ms wall / 4.6 ms CPU median, with no pixel decode. |

### Consequence

**RAF speed needs no fallback:** Task 20 uses the embedded JPEG at 256 and 1600. **The DNG fallbacks stay undecided.** Both stay in the plan as written:
- Task 20's RAW-decode path for DNGs without a preview;
- Go IFD0/ExifIFD reading if container properties are slow.

### Recorded for later tasks

- **Full RAW decode cost:** about 5× the CPU of the embedded path (289 ms vs 57 ms at 1600), so keep it the exception path.
- **4096 is the expensive tier:** 225 ms CPU median, with multi-second wall p95 under load. Measure RSS at Task 20's real concurrency before lowering it; the spike text allows that only when memory is the limit.
- **Task 19's DNG behaviour is unverified:** container for metadata, preview for dimensions. The parity test has exercised no DNG yet.
- **Before relying on DNG:** run `s7 dng <dir>` on real DNGs (DJI drone, iPhone ProRAW, Leica, …).
- **For clean wall-clock numbers:** re-run `s7 raf <folder-1> 50 5` on an idle machine. The verdict should not change.

## S6 — A hardened, sandboxed build loads the bundled ONNX Runtime

### Question
Can a sandboxed, hardened Wails v3.1.0 arm64 app with ad-hoc signatures load the official ONNX Runtime 1.25.0 dylib
from `Contents/Frameworks/` through `github.com/yalue/onnxruntime_go` v1.28.0? Is the archive pinnable, what is its
`minos`, and does it depend only on the OS?

### What was run
- Downloaded `https://github.com/microsoft/onnxruntime/releases/download/v1.25.0/onnxruntime-osx-arm64-1.25.0.tgz`;
  `shasum -a 256` compared with the release asset's `digest` from `gh api repos/microsoft/onnxruntime/releases/tags/v1.25.0`.
- `otool -l … | grep -A4 LC_BUILD_VERSION`, `otool -L`, `codesign -dv` on `lib/libonnxruntime.1.25.0.dylib`.
- Spike app `SpikeORT` (bundle id `com.github.volodymyrsmirnov.choix.spike.s6s5`). `Spike.LoadRuntime()`:
  `ort.SetSharedLibraryPath(<os.Executable()>/../Frameworks/libonnxruntime.dylib)`, `ort.InitializeEnvironment()`,
  return `ort.GetVersion()` or the error. Four builds, each run as
  `SPIKE_RUN=Ping,LoadRuntime SPIKE_EXIT=1 <absolute path>/SpikeORT.app/Contents/MacOS/SpikeORT`
  (sandboxed builds also via `open -W --env …`, same results).

### Raw output
```
archive sha256  65405dc8793c86cadb98b5e07f6d3bdde84f8300f1b030d4736b41c17610d6c1  (31 792 081 B)
gh digest       sha256:65405dc8793c86cadb98b5e07f6d3bdde84f8300f1b030d4736b41c17610d6c1  -> identical
lib/libonnxruntime.1.25.0.dylib and lib/libonnxruntime.dylib: two identical regular files (no symlink),
  36 679 544 B, sha256 2475226c9bc2c5dde0204e0db3c79fa0c03dca0cfcf2b22469da4c0a6c145f08, thin arm64,
  install name @rpath/libonnxruntime.1.25.0.dylib, shipped signature flags=0x20002(adhoc,linker-signed)
LC_BUILD_VERSION  platform 1  minos 14.0  sdk 15.5
otool -L  only /System/Library/Frameworks/{CoreML,Cocoa,IOKit,QuartzCore,Metal,IOSurface,Foundation,CoreFoundation}
          and /usr/lib/{libiconv.2,libc++.1,libSystem.B,libobjc.A}.dylib (plus its own @rpath install name)
```
```
A --sandbox (dylib re-signed ad-hoc)                  LoadRuntime: ok version=1.25.0
B --sandbox --hardened (dylib ad-hoc, runtime flag)  LoadRuntime: error … dlopen(…/Frameworks/libonnxruntime.dylib, 0x0001):
    tried: '…' (code signature in <UUID> '…/libonnxruntime.dylib' not valid for use in process:
    mapping process and mapped file (non-platform) have different Team IDs)
C --sandbox --hardened, dylib stripped (codesign --remove-signature; codesign -dv: "code object is not signed at all")
    bundle.sh: codesign of the app fails — "bin/SpikeORT.app: code object is not signed at all /
    In subcomponent: …/Contents/Frameworks/libonnxruntime.dylib"; the app keeps only the Go linker signature,
    has no entitlements, runs unsandboxed: LoadRuntime: error … (missing code signature in <UUID> '…/libonnxruntime.dylib')
    Stripped dylib swapped into the signed build B instead: sandboxed, same "missing code signature" error.
D unsandboxed, not hardened (dylib ad-hoc)            LoadRuntime: ok version=1.25.0
Informational — never to be used, the product allows no such entitlement:
  B + com.apple.security.cs.disable-library-validation  LoadRuntime: ok version=1.25.0
```
`os.Executable()` finding: with a relative `argv[0]` (`bin/SpikeORT.app/Contents/MacOS/SpikeORT`), the sandboxed app
resolved it against the container, `~/Library/Containers/<bundle id>/Data/bin/SpikeORT.app/…`, and dlopen failed
with "no such file". Unsandboxed, or launched by absolute path or LaunchServices, the path is correct.

### PASS / FAIL
| Criterion / build | Result |
|---|---|
| Archive SHA-256 = GitHub asset digest | PASS |
| `otool -L` only `/usr/lib` and `/System` | PASS |
| `minos` ≤ 13.0 | FAIL — 14.0 |
| A `--sandbox`, ad-hoc | PASS — 1.25.0 |
| B `--sandbox --hardened`, ad-hoc dylib (the pass criterion) | **FAIL** — different Team IDs |
| C `--sandbox --hardened`, unsigned dylib | FAIL — cannot be signed; dyld: missing code signature |
| D unsandboxed | PASS — 1.25.0 |

### Consequence
- Fallback "only `--sandbox` passes under ad-hoc signing" applies: library validation needs a real identity.
  `sign:dev` (Task 47) signs without `--options runtime`; hardened runtime is verified in the signing conversation.
  Spec §12 S6 gains that sentence.
- Fallback "`minos` > 13.0" applies: the deployment target is **14.0** in spec §10 and Global Constraints.
- `otool -L` is clean, so ONNX Runtime is not built from source. Task 22 proceeds.
- The bundled dylib must be signed with the app; an unsigned one can be neither sealed nor loaded.
- Task 22 derives the Frameworks path from the main bundle (`NSBundle.mainBundle.privateFrameworksPath`) or an
  absolute executable path, never from a relative `argv[0]`.

### Recorded for later tasks
- Task 22 pin: URL above, SHA-256 `65405dc8793c86cadb98b5e07f6d3bdde84f8300f1b030d4736b41c17610d6c1`.
- Dylib in the archive: `onnxruntime-osx-arm64-1.25.0/lib/libonnxruntime.1.25.0.dylib` → bundled as
  `Contents/Frameworks/libonnxruntime.dylib`.
- `minos` 14.0. Hardened + ad-hoc: does **not** work; sandboxed + ad-hoc without hardened runtime works.

## S5 — CoreML execution provider inside the sandbox

### Question
In the App Sandbox, does ONNX Runtime 1.25.0's CoreML execution provider with v1's flags work for the CLIP ViT-B/32
ONNX export (`clip_vit_b32.onnx`) and the TOPIQ-NR quality model (`topiq_nr_koniq_384.onnx`): correct output, at least
one node on CoreML, no sandbox denial, not slower than CPU when warm? Where does CoreML write?

### What was run
- S6's app `SpikeORT` (bundle id `com.github.volodymyrsmirnov.choix.spike.s6s5`), `--sandbox`, not hardened; models
  copied into `~/Library/Containers/<bundle id>/Data/Library/Application Support/Spike/`.
- `Spike.Infer(model, provider)`, with no-argument wrappers `InferClipCoreML`, `InferClipCPU`, `InferQualityCoreML`,
  `InferQualityCPU`. For CoreML: `AppendExecutionProviderCoreML(0)` (v1's exact call), and an error fails the call
  (v1 ignores it). Then: session log severity verbose with stderr captured, `NewDynamicAdvancedSession` like v1, a zero
  tensor 1×3×224×224 / 1×3×384×384, `find` of new files in the container during and after the call.
- Each wrapper ran twice in a fresh process (call 1 = cold, call 2 = warm), in two processes. Then 3 interleaved rounds
  with logging off and 20 extra runs per session (median). CLIP was also run once unsandboxed (models read from `~/.choix/models`).

### Raw output
```
CLIP     in pixel_values[-1 -1 -1 -1]  out image_embeds[-1 512]  outLen=512
  CoreML: GetCapability "partitions 1, nodes in graph 830, supported by CoreML 4" -> placed CoreML=1 (fused) CPU=582
          rejected ops: "has no shape info" (dynamic input), "[Concat] Input type: [7] is not currently supported"
  CPU:    "All nodes placed on [CPUExecutionProvider]. Number of nodes: 586"
QUALITY  in image[1 3 384 384]  out score[1 1]  outLen=1
  CoreML: "partitions 41, nodes in graph 679, supported by CoreML 575" -> placed CoreML=41 CPU=105
  CPU:    "All nodes placed on [CPUExecutionProvider]. Number of nodes: 442"
  raw score for the zero image: CoreML 0.4048, CPU 0.4060
```
Timings (session create / warm per-inference median). Other jobs loaded the machine (load average 7–38), so these are
ranges, not benchmarks. For CLIP, CoreML was slower than CPU in the quiet rounds (21–44 ms against 18–23 ms); in the
loaded round the two were within noise of each other. For the quality model CoreML was faster in every round.
```
                cold create      warm create      warm inference (median of 20, per round)
CLIP coreml     0.19–6.4 s       0.16–5.9 s       21–44 ms (quiet rounds), 179–208 ms (loaded round)
CLIP cpu        0.11–1.8 s       0.12–2.5 s       18–23 ms (quiet rounds), 203–227 ms (loaded round)
QUALITY coreml  4.5–22.8 s       4.9–34.3 s       53–86 ms (109–371 ms in the most loaded round)
QUALITY cpu     0.07–0.15 s      0.07–0.84 s      97–146 ms (895 ms in the most loaded round)
cold first inference (fresh process): CLIP coreml 29–1075 ms · CLIP cpu 23–916 ms · QUALITY coreml 73–376 ms · QUALITY cpu 140–272 ms
CLIP unsandboxed (load 17–31): same placement (CoreML=1/582, 4 of 830 nodes); CoreML medians 61–277 ms vs CPU 61–247 ms.
```
CoreML files (flags 0): per session in the container's temp dir,
`~/Library/Containers/<bundle id>/Data/tmp/onnxruntime-<uuid>-<pid>-<n>.model.mlmodel` + `….mlmodelc/`. That is 8 files
and ≈ 19 MB per CLIP session, and 41+41 models (328 files) and ≈ 354 MB per quality session. All of it is deleted on
session destroy. Nothing is written outside the container. Unsandboxed, the same files go to `$TMPDIR`.

Other flags (informational): `COREML_FLAG_CREATE_MLPROGRAM` (0x010) puts 128/830 CLIP nodes on CoreML (14 partitions),
ties with CPU (median 20 ms), and adds a persistent `<container>/Library/Caches/<bundle id>/com.apple.e5rt.e5bundlecache/`
(≈ 1 MB). Quality fails to compile in that mode: "Unable to parse ML Program: in operation /AveragePool_3: Required
param 'pad' is missing".

Log queries, all without privileges:
- `log show --last 2m --predicate 'process == "sandboxd"'` works but showed nothing about the app.
- `eventMessage CONTAINS "SpikeORT" AND eventMessage CONTAINS "deny"` works, but its only hits were another daemon
  being denied `process-info-pidinfo` on the app.
- `eventMessage CONTAINS "Sandbox: SpikeORT("`, the denials made by the app itself, returned 0 lines in every run.

### PASS / FAIL
| Criterion | CLIP | Quality |
|---|---|---|
| Append CoreML without error | PASS | PASS |
| Output length 512 / 1 | PASS | PASS |
| ≥ 1 node on CoreML | PASS (1 fused node = 4/830) | PASS (41 = 575/679) |
| No sandbox denial | PASS | PASS |
| CoreML warm not slower than CPU | **FAIL** (21–44 vs 18–23 ms) | PASS (53–86 vs 97–146 ms) |
| **Model** | **FAIL** | **PASS** |

### Consequence
- CLIP: the spec §12 S5 fallback applies, so `local.SessionOptions.CPUOnly` defaults to true when `appdir.Sandboxed()`.
  The cause is not the sandbox: unsandboxed, CoreML gets the same 4 of 830 nodes, because the export's input is fully
  dynamic. CPU cost: create 0.11–0.14 s, 18–23 ms per embedding (quiet).
- Quality: CoreML stays the default. Session creation costs 4.5–5.3 s quiet (up to 34 s loaded) every time, since
  nothing persists, plus ≈ 354 MB of temp space in the container per live session. The provider changes the raw
  score (0.4048 vs 0.4060), so it belongs in the scorer identity (`QualityScorerID`) if scores must stay comparable.
- Keep v1's flags. MLProgram mode does not help CLIP and breaks quality. The spike failed the call on a provider-append error in order to see it; production (Task 22) logs a warning and falls back to the CPU provider, as planned.

### Recorded for later tasks
- Flags: `AppendExecutionProviderCoreML(0)`. Defaults when sandboxed: CLIP = CPU-only, quality = CoreML.
- Nodes: CLIP CoreML 1/CPU 582 (CPU-only 586); quality CoreML 41/CPU 105 (CPU-only 442).
- CoreML cache: `<container>/Data/tmp/onnxruntime-*.model.mlmodel[c]`, per session, deleted on destroy, ≈ 19 MB / ≈ 354 MB.
- Precise denial query: `log show --predicate 'eventMessage CONTAINS "Sandbox: <AppName>("'`.

## S3 — Video over the Wails asset scheme with Range

### Question
Can a sandboxed Wails v3.1.0 app play large videos in WKWebView from a Go `http.Handler` mounted at `/media` on the `wails://` scheme with `http.ServeContent`? Specifically: does WebKit send `Range`, does the handler answer `206`, and does memory stay bounded instead of the file being buffered whole?

### What was run
- **App and handler.** Throwaway arm64 Wails app, ad-hoc signed, run sandboxed (3 clean runs) and un-sandboxed (1 run). `Media` implements `ServeHTTP` and is registered with `application.NewServiceWithOptions(media, application.ServiceOptions{Route: "/media"})`. It calls `http.ServeContent` with an `*os.File` and logs, per request, `Range`, status, `Content-Range`, bytes written and the app's `phys_footprint`.
- **Test files.** A synthetic 260 MB H.264/AAC .mov (test pattern), and a second, smaller MP4 with a different container layout. In the sandboxed runs both were copied into the app container (`~/Library/Containers/<bundle id>/Data/…`). The spike tests the transport; folder access is S1's question.
- **Page.** One `<video controls preload="none" muted playsinline poster=…>` per file. A script runs by itself on load: `play()` → first frame (`requestVideoFrameCallback`) → seek to 90 % → seek to 10 %. It logs `error`, `stalled`, `waiting` and `buffered`, then unloads both elements.
- **Memory.** `footprint` (phys_footprint) of the app, plus the `WebKit.WebContent`, `WebKit.GPU` and `WebKit.Networking` processes whose responsible pid is the app, at each checkpoint.
- **No click needed.** Muted (and even unmuted) `play()` was allowed without a user gesture, so the test ran fully scripted.

### Raw output
| | synthetic .mov | second MP4 |
|---|---|---|
| video requests per run | 425–641, all `206`, all with a closed `Range` | 673–690, all `206`, all with a closed `Range` |
| open-ended or no-`Range` requests | 0 | 0 |
| range sizes | 2–8 B atom probes, then median ≈ 40 KB, max 0.09 MB | 2–8 B probes (head, then tail), median ≈ 96 KB, max 0.87 MB |
| bytes sent | ≈ 18 MB of 260 MB | the whole file once (played to the end, then sought) |
| `play()` → first frame, sandboxed | 122–420 ms | 182–540 ms |
| 90 % / 10 % seek: `seeked` / first frame | 34–64 / 95–127 ms; 29–55 / 80–112 ms | 75–144 / 97–172 ms; 85–149 / 115–177 ms |
| `error` / `stalled` | none | none |

Memory growth from the start of the run to its peak (phys_footprint, sandboxed; the un-sandboxed run matched):
- **App: 34–35 → 40–41 MB (+5 to +7 MB).**
- WebContent: 54–57 → 195–219 MB at peak (+138 to +165 MB), about 100 MB after unload.
- GPU: 60–65 → 139–153 MB (+79 to +93 MB), about 74 MB after unload.
- Networking: 5–6.5 MB, flat.

### PASS / FAIL
| criterion | result |
|---|---|
| Both files play (sandboxed) | **PASS** |
| Both seek to 90 % and back | **PASS**: first frame at the target in 80–177 ms |
| Handler sees `Range`, answers `206` | **PASS**: 100 % of video requests |
| Memory growth < 200 MB (not buffered whole) | **PASS** on the app process: +5 to +7 MB |

The plan's memory criterion is judged on the app process, and the app stays flat. WebKit's own helpers also grow, and their numbers sit next to it: WebContent up to +165 MB and GPU up to +93 MB. Each helper is under 200 MB on its own, but together they peaked at +221 to +257 MB while both videos were loaded. That growth follows the media data received and the decoder state, and most of it is freed on unload. It is WebKit's media pipeline, not the transport.

### Consequence
The S3 fallback (poster + keyframes + "Open in QuickTime") is **not** needed. In-app video playback stays for states s213, s306 and s405.

### Recorded for later tasks
How the transport works:
- **Mounting.** A service with a `Route` that implements `http.Handler` is mounted on the asset server (`pkg/application/services.go:26-29`, `pkg/application/application.go:811-826`). The prefix is removed with `http.StripPrefix` (`internal/assetserver/assetserver.go:150-152`), and requests are matched with `strings.HasPrefix` (`internal/assetserver/assetserver.go:136-143`).
- **Request path.**
  1. `startURLSchemeTask:` (`pkg/application/webview_window_darwin.m:550-557`) calls `processURLRequest`, which queues the task on `webviewRequests` (`pkg/application/application_darwin_shared.go:245-257`).
  2. A goroutine per request (`pkg/application/application.go:725-729`, `internal/assetserver/assetserver_webview.go:75-102`) builds an `http.NewRequestWithContext` (`internal/assetserver/assetserver_webview.go:168`).
  3. Headers, including `Range`, are copied from `allHTTPHeaderFields` (`internal/assetserver/webview/request_darwin.go:37-50`).
  4. GET requests carry `http.NoBody`, and no request body is seekable. Only the file handed to `ServeContent` needs to be seekable.
- **Response path: streamed, not buffered.** Each `Write` becomes a `didReceiveData:` call with one NSData copy and no backpressure (`internal/assetserver/webview/responsewriter_darwin_shared.go:36-54`, `internal/assetserver/webview/responsewriter_darwin.go:39-46`). `WriteHeader` becomes `didReceiveResponse:`, which keeps only the first value of each header (`internal/assetserver/webview/responsewriter_darwin.go:97-118`). The content-type sniffer holds back at most 512 bytes, and only while no `Content-Type` is set (`internal/assetserver/content_type_sniffer.go:39-97`).

Rules for Task 37's media route:
1. **Set `Content-Type` explicitly** (`.mov` → `video/quicktime`, `.mp4`/`.m4v` → `video/mp4`). Go's built-in table has no `.mov`, and inside the sandbox Go does not read `/etc/apache2/mime.types`, so `.mov` went out as `application/octet-stream` after a 512-byte sniff read on every request. WebKit played it anyway.
2. **Use the route `/media/`** (or check the prefix in the handler). Because of the `HasPrefix` match, the route `/media` also catches `/media-x`.
3. **Stop reading when the request is cancelled.** After `stopURLSchemeTask:` (`pkg/application/webview_window_darwin.m:558-566` → `internal/assetserver/webview/request_finalizer.go:90-104`), the writer discards data but still reports success (`internal/assetserver/assetserver_webview.go:20-25`). Wrap the file in a reader that checks `r.Context()` so `ServeContent` stops early.
4. **No need to cap range size.** WebKit asks only for closed ranges of at most 0.87 MB.
5. **Focus releases a video when it leaves it** (`removeAttribute("src"); load()`). That frees most of the helper memory.
6. **A hidden window pauses muted video.** A seek on a paused, hidden element completes only on the next `play()`.
7. **No autoplay setting is needed.** `EnableAutoplayWithoutUserAction` (`pkg/application/webview_window_darwin.go:166-168`) can stay unset.

## S1 — Folder access in the sandbox: picker, drop, bookmarks, Finder

### Question
A sandboxed Wails v3.1.0 app (ad-hoc signed; `app-sandbox`, `files.user-selected.read-write`, `files.bookmarks.app-scope`, `network.client`) receives a folder as a plain path string. Can it make an app-scoped security-scoped bookmark and get the folder back after a relaunch, a rename, the Trash, a deletion, an exFAT or SMB volume going away or being remounted, and an ad-hoc rebuild? Which exact error codes does it see? Do the `NSWorkspace` calls work without a sandbox denial?

### What was run
- **No GUI control was available**: no clicks, drags or key presses. So every sandboxed grant came from **Launch Services** (`open -a <App> <folder>`, cold start and running app), not from the open panel or a drop. Everything after the grant is the same code as after a pick: bookmark from the path string, relaunches, rename, Trash, delete, volumes, rebuild and Finder calls.
- Wails delivers opened files through `application:openFile:` (`pkg/application/application_darwin_delegate.m:14`), which calls `HandleOpenFile` (`application_darwin.go:546`). That emits `events.Common.ApplicationOpenedWithFile` (`application_darwin.go:553`), and the path is a string in `Context().Filename()` (`context_application_event.go:82`). The app's `Info.plist` declares `public.folder` with `LSHandlerRank=None`.
- The grant is real: in the same sandboxed build, a path that was never granted fails the same code (raw output below).
- Bookmarks were created with `[NSURL fileURLWithPath:isDirectory:YES]` and `NSURLBookmarkCreationWithSecurityScope`, and resolved with `WithSecurityScope | WithoutUI | WithoutMounting`. Each check ran in a new process.
- Test volumes:
  - exFAT: a 2 GB disk image attached with `hdiutil` (mounted through fskit).
  - SMB: a local Samba container on `127.0.0.1:1445`, mounted with `mount_smbfs`. "Disconnect" = unmount plus stopping the server.
- (g) "another identity": no certificate was created, because that would change the keychain. Instead the app was rebuilt ad-hoc after a trivial source change, under the same bundle id, which gives a different CDHash.
- Finder evidence was gathered without looking at the screen:
  - the return values;
  - sandbox denials from `log show`;
  - Finder and browser window counts from `CGWindowListCopyWindowInfo` (owner names only, no Screen Recording).
- "Deleted from the Trash" means this one folder was deleted inside `~/.Trash`; the whole Trash was not emptied.

### Raw output (sandboxed, sanitised)
```
open -a: Adopt: bookmark from path string: ok len=964 · write .choix/spike-1.txt: ok   (no denial)
never granted: NSError{domain=NSCocoaErrorDomain code=256 "Could not open() the item"} · mkdir → errno=1(EPERM)
               kernel: deny(1) file-read-data <folder-6> · deny(1) file-write-create <folder-6>/.choix
relaunch:  Reopen: resolve ok stale=false · startAccessing=true · list ok · write ok
rename:    Reopen: resolve ok path=…/folder-1r stale=true · Refresh (from resolved URL, inside start/stop) ok
           relaunch with refreshed bytes: resolve ok stale=false · list ok · write ok
Trash:     Reopen: resolve ok path=~/.Trash/folder-3 stale=true · access ok
           getRelationship(NSTrashDirectory): without access → 0 (Contains) · inside start/stop → 0 (Contains)
rm -rf:    Reopen: NSError{domain=NSCocoaErrorDomain code=4 "The file doesn’t exist."} (no underlying) · lstat → ENOENT(2)
from Trash, then deleted: NSCocoaErrorDomain 4 · lstat → ENOENT(2)
exists, never granted: lstat → ok (no errno)
exFAT eject: NSCocoaErrorDomain 4 · lstat(stored path) → ENOENT(2)
exFAT remounted elsewhere: resolve ok, new path, stale=false · access ok · lstat(old path) → ENOENT(2)
SMB unmount + server stopped: NSCocoaErrorDomain 4 · lstat → ENOENT(2)
g1 rebuild (different CDHash): Reopen of the first build's bookmark: resolve ok stale=false · startAccessing=true · list/write ok
Finder: activateFileViewerSelectingURLs (void, main thread) · openURL(picks) → true · openURL(https) → true · no denial
```

### PASS / FAIL
| Criterion | Verdict |
|---|---|
| Picker: bookmark from the path string; lists and writes after relaunch; rename resolves to the new path; `Refresh` from the resolved URL works after relaunch | **PASS on bookmark behaviour, with the grant from Launch Services.** The open panel's own grant: **NOT RUN — needs a human hand** |
| Drop: the same for a dropped folder | **NOT RUN — needs a human hand** |
| Finder: three `NSWorkspace` calls work, no denial | **PENDING on the visual part.** Measured: both `openURL:` calls return `true`, no sandbox denial is logged, and one more Finder window exists after the call (the browser's window count rose in one run and stayed the same in the other, likely a new tab). Whether Finder selected the folder and showed `picks`, and whether the browser opened the page: **NOT RUN — needs a human hand** |
| (g) Another signing identity | **NOT RUN as specified** — it needs a certificate created for the spike, which would change the keychain; left for the owner. Separate evidence, not a substitute: after an ad-hoc rebuild (a different CDHash, same bundle id) Resolve and StartAccess both still succeed |
| Trash (d): "contains" without access or inside the lease | **PASS**: Contains in both cases (APFS, user Trash) |
| Deleted (e): `ENOENT` for the deleted path, a different result for an existing path with no access | **PASS**: `ENOENT` vs success |

### Consequence
- The picker fallback (own `NSOpenPanel`) applies only if the open panel's grant turns out to differ from the Launch Services grant.
- The drop decision (state s101, `EnableFileDrop`) stays open until the hand step is run.
- Finder: no fallback so far; Open Picks and Reveal stay as specified, subject to the owner's visual check.
- Trash: spec §4.1 keeps "or resolves into the Trash" for the boot volume. On exFAT and SMB the Trash relationship is unavailable, so a trashed folder there looks normal until the Trash is emptied.
- Deleted: no Task 29 fallback needed. But an ejected volume gives the same pair (code 4 + `ENOENT`) as a deleted folder, so treat a folder as gone only when its volume is mounted (stored volume UUID; SMB has none).
- Not tested: lstat inside TCC-protected folders (Desktop, Documents, Downloads), where `EPERM` is possible.
- An ad-hoc rebuild does not break bookmarks, so development rebuilds keep the library working.
- **Warning:** stopping the SMB server while the share stays mounted blocked the app's first directory read in the kernel for over 2 minutes, and the process could not be killed meanwhile (measured unsandboxed). Folder I/O on non-local volumes needs its own thread and timeout.

### Recorded for Tasks 10, 11, 16, 29
| Case | Value (sandboxed) |
|---|---|
| Resolve: deleted (`rm -rf`) / deleted from the Trash / exFAT ejected / SMB unmounted | `NSCocoaErrorDomain` **4**, no underlying error |
| Resolve: in the Trash | ok, `~/.Trash/<name>`, `stale=true` |
| Resolve: renamed | ok, new path, `stale=true`; after Refresh `stale=false` |
| Resolve: exFAT remounted at another mount point | ok, new path, `stale=false`; Refresh gives new bytes |
| Resolve + StartAccess after an ad-hoc rebuild (g1) | both ok |
| Bookmark from a never-granted path | `NSCocoaErrorDomain` **256** ("Could not open() the item"); write → `EPERM` (1) |
| Bookmark from a path string without `startAccessing` in a later process (refresh without access) | `NSCocoaErrorDomain` **256**; if the path is missing, **260** ("Scoped bookmarks can only be created for existing files or directories") |
| `getRelationship(NSTrashDirectory)` | APFS user Trash: Contains (0) without and inside access; outside the Trash: Other (2); exFAT and SMB: fails with `NSCocoaErrorDomain` **3328**, underlying `NSPOSIXErrorDomain` 2 |
| lstat of the stored path: deleted, ejected, unmounted, or the old path after a remount | `ENOENT` (2) |
| lstat: exists, no access | succeeds |
| Volume: APFS | UUID present · Local=1 · Removable=0 · SupportsExclusiveRenaming=1 |
| Volume: exFAT (disk image) | UUID present · Local=1 · Removable=1 · Ejectable=1 · SupportsExclusiveRenaming=0 |
| Volume: SMB (local Samba container) | UUID **absent** · Local=0 · Removable=0 · SupportsExclusiveRenaming=0 |
| `fsync` file / `fsync` directory | ok on APFS, exFAT and SMB |
| `F_FULLFSYNC` (file and directory) | ok on APFS and exFAT; `ENOTSUP` (45) on SMB |
| `renameatx_np(RENAME_EXCL)` onto a free name | ok on APFS; `ENOTSUP` (45) on exFAT and SMB |
| `renameatx_np(RENAME_EXCL)` onto an existing name | `EEXIST` (17) on all three; `EEXIST` does not prove support |
| Harmless denials | `file-read-data` on the SMB mount point during the grant; `iokit-open-user-client AppleAPFSUserClient` while reading APFS volume values (the values were still returned) |
| Proposed `ErrGone` | resolve code 4 **and** lstat `ENOENT` **and** the stored volume is mounted; everything else → `ErrUnusable` |

### Left for the owner
These are the spike runbook's hand steps:
1. Pick a folder in Wails' open panel and confirm the returned path carries a grant (bookmark, then Reopen after relaunch).
2. Drop a folder on the window and confirm the same.
3. Look at Finder and the browser after `Finder`: is the folder selected, is `picks` open, did the https page open?
4. (g) Rebuild the app signed with a different identity (a self-signed certificate created for the test) and `Reopen` with the bookmark made by the first build; record whether Resolve or StartAccess fails and with which error.

## S2 — Menu shortcuts: single keys, and who receives ⌘ combinations

### Question
With native menu items Pick (`Space`), Reject (`X`), Compare (`C`), Next Photo (`Right`), Add Folder (`CmdOrCtrl+O`) and Undo (`CmdOrCtrl+Z`): does a single key typed in an `<input>` insert its character without firing the menu, while outside an input it fires the item once with no page `keydown`? Does the page see ⌘O/⌘Z when the menu fires, and does ⌘Z in the input still undo the field?

### What was run
A sandboxed arm64 Wails v3.1.0 app (cgo backend, macOS 27.2, ad-hoc signed). Its menu bar has the AppMenu role, an Edit menu with the Cut/Copy/Paste/Select All roles, and a Spike menu with the six items; each item's `OnClick` writes a line to the log. The page has a focusable `<div>`, an `<input>`, capture-phase `keydown`/`keyup` loggers and `beforeinput`/`input` loggers, all writing to the same numbered log. A control file switches focus and drops markers from the shell. A summary script turns the log into the key × focus result table. Everything except the key presses was checked: the build, the native menu, focus switching, the markers and the summary script.

### Raw output
`NSApp.mainMenu` as AppKit holds it (key equivalent as code points, modifier mask):
```
'Pick'       key=U+0020 mask=0x0                    action=handleClick target=MenuItem
'Reject'     key=U+0078 mask=0x0                    action=handleClick target=MenuItem
'Compare'    key=U+0063 mask=0x0                    action=handleClick target=MenuItem
'Next Photo' key=U+F703 mask=0x0                    action=handleClick target=MenuItem
'Add Folder' key=U+006F mask=0x100000 (Command)     action=handleClick target=MenuItem
'Undo'       key=U+007A mask=0x100000 (Command)     action=handleClick target=MenuItem
```
When the role Undo (Edit, ⌘Z, `undo:`) is also present, the Spike `Undo` reads back `key=none`.

### What the Wails v3.1.0 source says
Paths are under `pkg/application/`.
- **Parser:** `SetAccelerator` (`menuitem.go:276-287`) → `parseAccelerator` (`keys.go:182-224`). The last component goes through `parseKey` (`keys.go:149-179`), which accepts a named key (`keys.go:91-147`, `right` l.98, `space` l.101) or any single printable character. No modifier is required. The key is lowercased (`keys.go:206`). An invalid string is only logged (`menuitem.go:278-281`).
- **Native item:** `setAccelerator` (`menuitem_darwin.go:397-408`) sets the mask from `toMacModifier` (`keys_darwin.go:22-28`), so no modifiers gives mask 0. `setMenuItemKeyEquivalent` (`menuitem_darwin.go:319-325`) applies `translateKey` (`155-316`): space → U+0020 (l.189-191), right → U+F703 (l.180-182), letters unchanged (l.315). Wails' own Delete role already uses a modifier-less `"backspace"` (`menuitem_roles.go:113-115`).
- **OnClick timing:** a custom item's action is `handleClick` with the item as target (`menuitem_darwin.go:34-41`). The click goes to `processMenuItemClick` (`menuitem_darwin.m:9-11`), then onto a channel (`application_darwin.go:521-524`), then to `MenuItem.handleClick`, which runs `OnClick` in a new goroutine (`menuitem.go:268-273`). By the time Go runs it, AppKit has already used up the key.
- **Key routing:** Wails subclasses `NSWindow` (`webview_window_darwin.h:14`) but not `WKWebView` on macOS. So whether the page or the menu gets a key is decided by AppKit and WebKit.
  - `performKeyEquivalent:` (`webview_window_darwin.m:96-107`) first checks Wails key bindings: `processWindowKeyEquivalent` (`application_darwin.go:437-465`) → `processKeyBinding` (`webview_window.go:1769-1795`). App-menu items are never added to those maps on macOS (`addMenuBinding` is called only in `menu_windows.go:148-150` and `popupmenu_windows.go:203-205`). It then falls through to `super`.
  - `keyDown:` (`webview_window_darwin.m:86-95`) gets only presses no view handled. It never calls `super` and sends them to Wails key bindings (`application_darwin.go:429-435`).

### PASS / FAIL
- Single keys: **NOT RUN** — needs key presses by hand.
- ⌘ combinations: **NOT RUN** — needs key presses by hand.
- Prerequisite "accelerators parse and become native key equivalents": **PASS** (source and menu dump).

### Consequence
Until the key presses are run, the spec §4.4 fallback is the safe default: single-key items are built without an accelerator and stay owned by the webview; the key appears only in the shortcut sheet. What each outcome would change:
- **Single keys PASS:** Tasks 50/52 may give Pick/Reject/Compare/Next Photo their accelerators.
- **Menu takes the key inside the input, or the page also gets `keydown` while the menu fires:** the fallback stays.
- **The page sees ⌘O/⌘Z while the menu fires:** Task 52's rule holds as written, and the webview never acts on a manifest accelerator.
- **⌘Z in the input does not undo the field:** the `undo` action calls `document.execCommand('undo')` for text fields.
- **The field undoes and the menu also fires:** that is a double undo, so the same rule applies.

### Recorded for Tasks 50 and 52
- AppKit keeps only the first of two identical key equivalents. A custom Undo and the role Undo cannot both carry ⌘Z, so the Edit menu holds exactly one of them.
- The window's `WindowShow` listener sometimes does not fire (about 1 launch in 7). Start-up work must not depend on it alone.
- No Wails `KeyBinding` may reuse a manifest accelerator. In `performKeyEquivalent:` it would claim the key before both the webview and the menu.

### Left for the owner
13 key presses (x, c, Space, → and ⌘O with the page and then the input focused; ⌘Z with the page focused; typing `ab` into the input, then ⌘Z there), as listed step by step in the spike runbook. The summary script then prints the verdicts.
