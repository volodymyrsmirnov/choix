# Built-in grouping and quality: design

Date: 2026-11-12. Status: the owner set the direction in conversation (Apple Vision for grouping, a built-in quality score, no downloads, no ONNX Runtime) and asked for it to be built while they were away. The calls marked **[call]** were made by the author without them and are listed again in §16 for the owner to check.

This spec replaces the two downloaded models and ONNX Runtime with code that ships inside the app:

- **Visual grouping** uses Apple's Vision image feature print and complete-link clustering.
- **Quality rating** uses a small arithmetic formula (sharpness, noise, exposure) written in Go.
- **ONNX Runtime, CLIP, TOPIQ, the model downloader and the shared inference engine are removed**, with everything that exists only for them: build steps, the bundled library, the download UI, settings copy, tests and documentation.

Where this spec contradicts `2026-11-11-choix-2-desktop-design.md`, this spec wins; that document is updated in the same change (§13).

---

## 1. Why

The owner's complaint: visual grouping does not help, and picking is still a lot of work. Reading the code and measuring found:

- The app clusters with single-link chaining at one fixed CLIP threshold (0.92). On a labelled private folder of 110 photos that produces 73 groups, 47 of them single photos.
- CLIP is fed the 256 px thumbnail squashed to a square.
- The quality model is 177 MB, takes 375–600 ms per photo and carries a non-commercial licence.

The owner then set the direction: Apple Vision for grouping, a built-in score for quality, no downloads, no ONNX Runtime. Speed and the absence of a licence-restricted model matter more than the last points of accuracy.

## 2. Evidence

All numbers come from a throwaway benchmark run on 2026-11-12. Its code is not part of the repository. Two data sets:

- **Public:** INRIA Holidays, 1,491 personal holiday photos in 500 known groups. It has no timestamps, so time buckets were simulated (about 30 photos each); thresholds were tuned on half the buckets and scored on the other half.
- **Private:** one folder of 110 photos from one camera, 13 time buckets, hand-labelled into 68 groups (35 singles, 24 pairs, 9 triples) by the author. Thresholds cross-validated by leaving one bucket out.

The score is pairwise F1 against the labels (1.00 reproduces the groups exactly).

### 2.1 Grouping

Best grouping method per model family:

| Model | Public, held out | Private, cross-validated | Time per photo | Download |
|---|---|---|---|---|
| DINOv2 base | 0.81 | 0.95 | 218 ms | 347 MB |
| DINOv2 small | 0.79 | 0.92 | 74 ms | 88.5 MB |
| Apple Vision feature print | 0.75 | 0.89 | 7–10 ms | none |
| CLIP ViT-B/32 | 0.72 | 0.89 | 39 ms | 352 MB |
| The app today (CLIP, single-link, 0.92) | 0.58 | 0.68 | 39 ms | 352 MB |
| Time buckets only | 0.17 | 0.12 | | |

- Vision is behind DINOv2 base on the public set by more than the noise, and within the noise on the private folder.
- Single-link is the weakest or near-weakest method for every family. Complete-link lifts every model.
- Thresholds do not transfer between the two sets: thresholds tuned on the public set scored 0.30–0.77 on the private folder.

Vision feature print (scaleFill, preview tier) with complete-link on the private folder, F1 by threshold:

| Threshold | 0.85 | 0.87 | 0.88 | 0.89 | 0.90 | 0.91 | 0.92 | 0.93 | 0.94 |
|---|---|---|---|---|---|---|---|---|---|
| F1 | 0.79 | 0.81 | 0.85 | 0.86 | 0.88 | 0.93 | 0.89 | 0.88 | 0.80 |

At 0.91 every pair it formed was a labelled pair (precision 1.00, recall 0.86). On the public set, whose groups vary in viewpoint far more than bursts do, the same method peaks at 0.78.

Feature print facts measured on macOS 27: revisions 1 and 2 are supported and 2 is the default; a vector has 768 `float` elements and is already unit length (norms 0.9995–1.0012); the default crop option is `scaleFill`.

### 2.2 Quality

150 sharp public photos were damaged by known amounts (defocus, motion blur, missed focus on the subject only, noise, under- and overexposure; 3,150 images). A score is right for a pair when it ranks the undamaged photo above its damaged copy. Weights were fitted on 75 sources; the numbers below are from the other 75.

| Score | Blur | Missed focus | Noise | Exposure | Overall |
|---|---|---|---|---|---|
| TOPIQ (the app today) | 1.00 | 0.98 | 0.95 | 0.85 | 0.94 |
| Hand-set built-in formula (§6.2) | 1.00 | 0.99 | 0.99 | 0.91 | 0.97 |
| Apple Vision aesthetics score alone | 0.89 | 0.33 | 0.68 | 0.69 | 0.73 |
| Plain sharpness alone | 1.00 | 1.00 | 0.00 | 0.62 | 0.69 |

- Adding Vision's saliency box or aesthetics score to the formula changed nothing measurable, so the quality score makes no Vision call.
- The formula's weak spot is +1 EV overexposure (0.64–0.71 against TOPIQ's 0.89).
- On the private folder the formula orders the photos much like TOPIQ (rank correlation 0.73) and puts the three ruined frames last. It picks TOPIQ's best-of-group in 19 of 33 groups (chance is about 15).
- Across 150 undamaged, sharp photos of different scenes it does not track TOPIQ at all (0.02). **It tells a damaged frame from a good one; it does not rank good photos of different scenes.**
- The lead over TOPIQ is partly circular (the weights were set on the same kinds of synthetic damage). Read it as "good enough".

## 3. Goals, non-goals, anti-goals

**Goals**

1. Visual grouping that groups near-identical frames, with no download.
2. A quality rating with no download and no licence restriction.
3. No ONNX Runtime, no model files, no downloader, no inference engine, no network request anywhere in the app.
4. Existing folders upgrade themselves the next time they are opened or scanned; picks are untouched.
5. No dead code and no stale mention of the removed pieces in living documentation.

**Non-goals**

- A new quality UI. The 0–10 badge stays; only its calibration changes (§6.4).
- Face checks, saliency, text search, person grouping, auto-pick, "best in group".
- Tuning thresholds per folder.
- Removing the `com.apple.security.network.client` entitlement (§14).

**Anti-goals**

- A fallback that keeps ONNX or a model "just in case".
- Any change to originals, picks, or how picks are exported.
- Ranking or picking by the quality score. It stays display-only.

## 4. Hard rules after this change

The eight hard rules in `CLAUDE.md` stay, with these edits:

- **Rule 4 (cancellation).** The exception "one ONNX session creation or one inference is awaited" is deleted. A Vision feature-print request is a native call like an ImageIO call: it runs detached through `platform.Detach`, holds a native slot until it really returns, returns bytes and writes nothing.
- **Rule 5 (platform).** The deployment target stays macOS 14.0. The reason becomes: the Vision feature print revision the app pins (revision 2) needs macOS 14.
- **Rule 6 (downloads)** is replaced by: **"The app makes no network request."** Nothing is downloaded at run time or at build time for the app's own function. `net/http` appears only where it serves the webview (`internal/app/media`, `cmd/choix`), as `internal/retired_test.go` pins.
- **Rule 8 (sandbox).** Unchanged, including the four entitlements. The rationale for `network.client` becomes "unused; removal pending the owner's check of the packaged app" (§14).

## 5. Visual grouping

### 5.1 Platform interface

New file `internal/platform/vision.go`:

```go
// FeaturePrintLen is the length of a Vision image feature print at revision 2.
const FeaturePrintLen = 768

// Vision is Apple's Vision framework, as far as the app uses it.
type Vision interface {
	// FeaturePrint returns the image feature print of a still: revision 2,
	// crop-and-scale "scale fill", FeaturePrintLen float32 values, as Vision
	// returns them. Exactly one of in.Path and in.Data is set.
	FeaturePrint(ctx context.Context, in ImageInput) ([]float32, error)
}
```

New package `internal/platform/darwin/vision` (`vision.go`, `vision.h`, `vision.m`, `vision_test.go`), built like `internal/platform/darwin/imageio`: every file `//go:build darwin`, ARC, each entry point inside an autorelease pool and `@try`, a C result struct the Go side frees, input validated in Go, the call made inside `platform.Detach`. It links `Vision`, `ImageIO`, `CoreGraphics`, `Foundation`. It imports no Wails package.

The Objective-C side:

1. Creates a `VNImageRequestHandler` for the file URL or the data.
2. Creates a `VNGenerateImageFeaturePrintRequest`, sets `revision = VNGenerateImageFeaturePrintRequestRevision2` and `imageCropAndScaleOption = VNImageCropAndScaleOptionScaleFill`.
3. Performs the request and copies the observation's data out.
4. Fails with a message, never a crash, when: the revision is not in `supportedRevisions`, the element type is not float, the element count is not 768, or Vision reports an error.

Fake: `internal/platform/platformtest.Vision`, in the style of the other fakes (`Fail` map, `Gate`, call log, `platform.Detach`). Its vector is a deterministic function of the decoded pixels, so equal images give equal vectors and visibly different images give different ones; it is unit length and has 768 elements.

`cmd/choix/main.go` passes `vision.New()` in `runtime.Deps.Vision`, a required dependency.

### 5.2 Analyze stage

New package `internal/analyze` replaces the analyzer in `internal/ai/local`:

```go
// PrintID identifies how stored feature prints were made. Rows stamped with
// another id are pending again.
const PrintID = "vnfp-r2/fill/preview/v1"

// Printer makes the feature print of the image at path. A nil Printer means
// prints are not computed.
type Printer interface {
	Print(ctx context.Context, path string) ([]float32, error)
}

func New(s *store.Store, p Printer) *Analyzer
func (a *Analyzer) Analyze(ctx context.Context, fileID int64, previewPath string) error
```

- The stage reads the **preview tier** (`thumb.TierPreview`, at most 1600 px), not the thumb tier. That is the input the benchmark measured. For a video it is the preview made from the first keyframe, as today.
- The session's printer wraps `platform.Vision` and answers "off" while `visual_grouping` is off, exactly as `sessionGatedEmbedder` does today.
- `Analyze` L2-normalises the vector before storing it and rejects a vector whose length is not `platform.FeaturePrintLen` or whose norm is zero or not finite.
- Outcomes, each ending with the file `analyzed` unless noted:
  - print made: store it with `PrintID`;
  - `visual_grouping` off or no printer: store nothing, leave the row unstamped;
  - Vision failed on a readable preview: store a NULL print **stamped with `PrintID`**, so the file is not retried on every job, and log it;
  - cancelled, or the folder is unreachable: leave the row as it was; an unreachable folder is `pipeline.ErrFolderUnavailable`, as today;
  - the preview cannot be read while the folder is reachable: the file is `failed` with the error text, as today.
- `sessionAnalyzeWorkers` becomes 4 **[call]**. Feature-print calls are independent and already bounded by `platform.NativeSlots`.
- `session.acquireInference` and `internal/app/session/inference.go` are deleted: nothing is serialised process-wide any more.

### 5.3 Storage

Migration `internal/store/migrations/007_vision_print.sql`:

```sql
ALTER TABLE ai_signals ADD COLUMN vision_print BLOB;
ALTER TABLE ai_signals ADD COLUMN vision_print_id TEXT;
PRAGMA user_version = 7;
```

- `vision_print` holds 768 little-endian float32 values (3,072 bytes) or NULL.
- `ai_signals.clip_embedding` joins the list of retired columns: never read, never written, never dropped.
- Store API (`internal/store/ai_signals.go`, `files.go`), renamed from "embedding" to "print":
  - `AISignals{FileID int64; Print []float32; PrintID string; ComputedAt sql.NullInt64}`;
  - `Upsert(AISignals)` writes `vision_print`, `vision_print_id`, `computed_at`;
  - `GetByFileID(id)`;
  - `AllPrints(printID string) (map[int64][]float32, error)`: rows with a non-NULL print and that id;
  - `CountAnalyzedMissingPrint(printID)` and `BumpAnalyzedMissingPrint(printID)`: analyzed files with no `ai_signals` row, or with `vision_print_id` NULL or different. They replace the two `…MissingEmbedding` methods.
- A stored blob whose length is not 3,072 bytes is treated as no print.

### 5.4 Clustering

`internal/group/visual.go` `Cluster(prints map[int64][]float32, threshold float64) [][]int64` becomes **complete-link**:

- Similarity is the dot product of two unit vectors.
- Start with every id alone. Repeatedly merge the two clusters with the highest linkage, where a pair's linkage is the **lowest** similarity between any member of one and any member of the other, while that linkage is **strictly greater** than the threshold. A tie in linkage goes to the pair whose smaller minimum id is smaller, then whose other minimum id is smaller.
- Output ordering is unchanged: members ascending, clusters by smallest member.
- Any implementation that returns exactly these groups is acceptable. It must handle a bucket of 2,000 prints in under 5 s on the development machine, and a property test compares it with a naive version of the rule above on random inputs.
- A bucket with more than 4,000 printed files is clustered in consecutive windows of 4,000 in the bucket's time order **[call]**, to bound memory at one similarity matrix of 4,000². Groups never span two windows.

Everything else in `internal/group` keeps its behaviour: time buckets, files without a print going to their bucket's fallback cluster, cross-device mode, `MergedDeviceKey`.

**Cross-device merging.** `skew.go`'s `anchorSimilarity` was 0.95 on CLIP's scale. On the feature print's scale it becomes **0.93** **[call]**: on the private folder every pair at or above 0.91 was a true pair. It is untested on real two-camera data; the feature stays off by default.

### 5.5 Setting and parameters

- The threshold setting is renamed in `config.toml` from `visual_cluster_threshold` to **`visual_similarity`**, default **0.90** **[call]** (Go field `VisualSimilarity`). The old key is ignored on read and no longer written. The similarity scale changed with the model, so an old stored value would silently mean something else; 0.90 sits one step looser than the strict optimum on the private folder, because the owner prefers slightly looser groups.
- The binding field stays `similarity`, the valid range stays `(0, 1]`, the slider stays 0.05–1.00 in steps of 0.01.
- `session.ClusterParams` gains the method: `b=%d;t=%.4f;x=%t;v=%t;m=<analyze.PrintID>+complete`. Every folder clustered by an earlier build therefore re-clusters once.
- `visual_grouping` keeps its meaning with the model taken out:
  - **off**: no print is computed; clustering treats every file as having none; clusters are the time buckets.
  - **on**: new files get a print; the job's backfill re-analyzes analyzed files that are pending for `PrintID`; clustering uses prints stamped `PrintID`.
- Saving `bucket_size_sec`, `visual_similarity`, `cross_device_merging` or `visual_grouping` re-clusters, as today.

### 5.6 What an existing folder does

- Opening or rescanning it runs its full job. The backfill finds every analyzed file pending for `PrintID`, re-analyzes them (about 10 ms each plus decoding the preview), and re-clusters.
- Between opening and the backfill finishing, a folder clustered by a 2.0 build shows time-bucket clusters: `EnsureClustered` sees the changed parameters and re-clusters with no prints yet. `folder:changed` "clusters" then brings the visual groups.
- A v1 folder (no `cluster_params`) still opens as it is and keeps its clusters until a job or a recluster rebuilds them.
- Switching `visual_grouping` on queues a backfill job for every reachable folder, as switching it on with the model ready does today. Nothing is queued at launch for finished folders **[call]**.

## 6. Quality rating

### 6.1 Shape

- `internal/quality` holds everything: `pass.go` (unchanged in behaviour), `measure.go` (the formula, pure Go, no cgo), `scorer.go` (the store-backed `Scorer`, moved from `local.QualityScorer`).
- Scorer id: `quality.ScorerID = "choix-sne-1024/v1"`. Rows stamped with the TOPIQ id become pending and their scores are hidden, by the existing rule.
- Input: the preview tier JPEG, decoded with Go's `image/jpeg`. A preview whose shorter side is under 384 px is unrated (NULL score stamped with the scorer id), as today.
- Storage is unchanged: `files.quality_raw`, `files.quality_model`. No migration.
- The pass runs where it runs today (last step of a folder job, when `quality_rating` is on). Nothing waits for a model. `jobScorer` no longer takes an inference turn. `local.ErrQualityUnavailable` and `Pass.IsUnavailable` are deleted.
- Only "this photo can never be rated" is stamped with NULL. Cancellation, a disabled setting and database errors leave the row pending. Three consecutive failures stop the pass with the `error` state, as today.

### 6.2 The formula

All arithmetic in float64.

1. **Working copy.** If the preview's long side is over 1024 px, resample it to long side 1024 with a box (area) filter, target size `round(w·s) × round(h·s)` with `s = 1024 / long side`, and round each channel to 8 bits (half up). Otherwise use it as it is. Never scale up.
2. `rgb` = the working copy's channels divided by 255. `y` = `0.2126·R + 0.7152·G + 0.0722·B` of `rgb` (luma of sRGB code values).
3. **Sharpness at σ = 1 and σ = 2.** `g_σ` = `y` convolved with a separable Gaussian: radius `ceil(3σ)`, taps `exp(-0.5·(i/σ)²)` normalised to sum 1, borders reflected without repeating the edge sample (numpy `reflect`). `L_σ` = the 4-neighbour Laplacian of `g_σ` (up + down + left + right − 4·centre, same reflection). `var_σ` = the population variance of `L_σ` with a border of `3σ + 2` pixels removed on every side.
4. **Noise.** `c` = `y` filtered with the 3×3 mask `[[1,−2,1],[−2,4,−2],[1,−2,1]]` (reflected borders). `gm` = gradient magnitude of `g_2`, using central differences inside and one-sided differences at the edges (numpy `gradient`). `ok` = pixels with `0.03 < y < 0.97`; if fewer than 1,000, all pixels. `thr` = the 30th percentile of `gm` over `ok` (linear interpolation between order statistics, numpy's default). `flat` = `ok` pixels with `gm ≤ thr`. `noise = 1.4826 · median(|c| over flat) / 6`.
5. **Noise-corrected sharpness.** `dn_σ = max(var_σ − K_σ · noise², 1e-9)` with `K_1 = 0.11196346895601238` and `K_2 = 0.0022760448925639087` (the squared norm of the discrete Laplacian-of-Gaussian kernel: the variance white noise of σ = 1 adds to the response).
6. **Exposure.** `lin(v)` = `v / 12.92` when `v ≤ 0.04045`, otherwise `((v + 0.055) / 1.055)^2.4`. `Y` = `0.2126·lin(R) + 0.7152·lin(G) + 0.0722·lin(B)`. `midgrey_ev = | mean(log2(Y + 1e-4)) − log2(0.18) |`. `clip_hi` = the share of pixels whose largest channel is at least 250/255.
7. **Raw score:**

   `raw = 3·ln(dn_1) + 3·ln(dn_2) − ln(noise + 1e-4) − midgrey_ev − 10·clip_hi`

`files.quality_raw` stores `raw`. Changing any step, constant or weight **must** bump `quality.ScorerID`.

### 6.3 Reference values

The Go implementation must reproduce these. The test images are defined by formula, so no fixture file is needed. For `x` in `0…w−1`, `y` in `0…h−1`:

- `a(x, y; k) = floor(255 · clamp(0.5 + 0.25·sin(2πx/(16k))·cos(2πy/(24k)) + 0.1·sin(2π(x + 2y)/(7k)), 0, 1) + 0.5)`
- **A**: 640×480, `R = G = B = a(x, y; 1)`.
- **B**: 640×480, `R = G = B = clamp(a(x, y; 1) + ((x·73856093 XOR y·19349663) mod 41) − 20, 0, 255)` (64-bit integers).
- **C**: 640×480, `R = G = min(255, 2·a(x, y; 1))`, `B = min(255, a(x, y; 1) + 60)`.
- **D**: 2048×1536, `R = G = B = a(x, y; 2)`. It is resampled 2:1 to 1024×768.
- **E**: 640×480, every channel 128.

| Image | var_1 | var_2 | noise | dn_1 | dn_2 | midgrey_ev | clip_hi | raw | display |
|---|---|---|---|---|---|---|---|---|---|
| A | 0.00150935137 | 0.000315702244 | 0.0339156863 | 0.00138056273 | 0.000313084169 | 0.123571826 | 0 | −40.7055461 | 9.647 |
| B | 0.00174632361 | 0.000320880532 | 0.0523270588 | 0.00143975407 | 0.000314648445 | 0.106384177 | 0 | −40.9800644 | 9.510 |
| C | 0.00244116455 | 0.000387579987 | 0.0647634565 | 0.00197155558 | 0.00037803356 | 1.9642239 | 0.536468099 | −46.921814 | 6.539 |
| D | 0.00129932664 | 0.000310955732 | 0.0261635294 | 0.00122268426 | 0.000309397711 | 0.144848853 | 0 | −40.8680438 | 9.566 |
| E | 0 | 0 | 0 | 1e-9 | 1e-9 | 0.262770556 | 0 | −115.392025 | 0 |

Channel sums, to check the generators: A 117,498,873; B 117,439,461; C 195,801,086; D 1,203,225,198. Tolerance: 1e-6 relative on every feature and 1e-6 absolute on `raw`.

### 6.4 The badge

`quality.Display(raw) = clamp((raw + 60) / 2, 0, 10)` **[call]**.

Measured medians under that mapping: sharp public photos 9.0; the private folder 6.6; defocus σ = 2 at 1600 px 6.2; defocus σ = 4 1.6; the three ruined private frames 0. Recalibrate only in `Display`, never by rescoring.

The badge keeps today's look and rules (a value shows only when `quality_model` equals the scorer id). Its known limit, from §2.2: the number is meaningful between frames of one scene, not between scenes. A presentation that says so (flags, or a group-relative mark) is deferred (§14).

### 6.5 Status

`events.Quality` loses `received` and `size`, and the states `downloading` and `download_failed`. The remaining states are `disabled`, `waiting`, `scoring`, `idle`, `error`. `QualityOptions.Model` is deleted.

## 7. What is removed

**Go**

- `internal/ai/local/` (whole package, with `testdata/`).
- `internal/app/inference/` (whole package).
- `internal/app/models/` (whole package): the download manager and the `Models` binding.
- `internal/app/session/inference.go`; in `job.go` the model-readiness plumbing (`ModelState`, `JobOptions.Models`, `ready`); in `quality.go` the download states.
- `appdir.Models()`. A new `appdir.LegacyModels()` returns the old path without creating it (§11).
- In `internal/app/runtime`: `Deps.Downloader`, `Deps.CPUOnly`, `Deps.Embedder`, `Deps.CropInferer`, the model manager, the engine, `modelEnabled`, `modelStatus`, `onModelDownloaded`, the shutdown steps for models and inference, and the model part of `Idle()`.
- `events.ModelsProgress`, `events.ModelStatus`, and their registration in `cmd/choix/events.go`.
- `go.mod`: `github.com/yalue/onnxruntime_go`, and `golang.org/x/image` if nothing imports it after the change (`go mod tidy` decides).

**Build, CI, release**

- `build/onnxruntime/` and `build/quality-model/` (whole directories).
- `Taskfile.yml`: the `deps:onnxruntime` task, the `ORT_DYLIB` variable, every `CHOIX_ONNXRUNTIME_DYLIB` export, the `--dylib` argument to `bundle.sh`, every `codesign` of `Contents/Frameworks/libonnxruntime.dylib`.
- `build/darwin/bundle.sh`: the `--dylib` option and the `Contents/Frameworks` directory.
- `.github/workflows/ci.yml`: the ONNX Runtime cache and fetch steps, the `CHOIX_ONNXRUNTIME_DYLIB` environment, `fetch_test.sh`, the hard-coded `"onnxRuntime":"1.25.0"` check.
- Their tests: `build/darwin/bundle_test.sh`, `build/taskfile_test.sh`, `build/smoke/sandbox-smoke_test.sh`, updated to the new shape.

**Kept on purpose:** `sign:dev` still signs ad hoc without the hardened runtime. Its reason (library validation refusing the bundled library) is gone; turning the hardened runtime on for ad hoc builds is a follow-up (§14), and the Taskfile comment says so.

## 8. Bindings and events

- `cmd/choix` registers four services: `Library`, `Catalog`, `Settings`, `System`; `Media` stays mounted at `/media`.
- `Models.Status` and `Models.Retry` are deleted. `Models.Quality` becomes **`Catalog.QualityStatus() (events.Quality, error)`**.
- Events: `models:progress` is deleted. `quality:status` stays with the slimmer payload (§6.5). `folder:changed` reasons are unchanged.
- `task bindings` regenerates `frontend/bindings`; the generated `models` service directory disappears.
- `Runtime.Idle()` and `testsupport.WaitIdle` no longer consider downloads.

## 9. User interface

The UI loses everything that exists to show a download. Nothing new is added.

### 9.1 Changes in the design folder and the prototype

- **Deleted components:** `ModelRow`, `ModelBanner`, `SettingsModelCard`, the whole `screens/welcome/` screen.
- **Library:** with no folders it shows the existing "Empty (drop zone)" state as soon as the list has loaded. The props `models`, `online`, `onRetryModel`, `onToggleModel` are deleted.
- **Folder:** `FolderGrouping` becomes `'visual' | 'time-off'`. The two time-only notices about a model download are deleted.
- **Settings:** the two model cards and the `models`, `retrying`, `onRetryModel`, `onRetryQuality` props are deleted. The status line under Quality rating no longer has download states.
- **Quality indicator:** the `downloading` and `download_failed` variants and their copy are deleted.
- **Types:** `ModelStatus` is deleted; `QualityState` and `QualityStatus` follow §6.5; `DEFAULTS.similarity` is 0.90.
- **Copy** **[call]**, exact strings:
  - Visual grouping hint: "Group shots that look alike, using Apple’s on-device image analysis. Nothing is downloaded and nothing leaves this Mac. Off: folders are grouped by time only."
  - Visual similarity hint: "How alike two shots in the same time group must look to share a cluster. Only takes effect with visual grouping on; without it, each time group is one cluster."
  - Quality rating hint: "Show a 0–10 technical-quality rating (sharpness, noise, exposure) on every photo, to tell near-identical frames apart. Advisory only: it never picks, rejects or reorders. It compares frames of the same scene; it does not rank different scenes."
  - Quality status when off: "Off: no ratings."
  - The mock rating error in state 514 becomes "the database is busy".

### 9.2 States

- **Retired** (scenario deleted, number added to `docs/design/retired.json` and the change file's `retired:`): 11, 12, 13, 14, 15, 16, 17, 112, 120, 216, 228, 505, 506, 515, 516, 517.
- **Changed:** every state whose rendered tree changes. At least the Settings states that show the Grouping or Quality section (500, 507, 514, 518 and their neighbours) and the gallery states 803, 805 and 807. The exact list is whatever `task design:snapshot` re-hashes; all of it goes into the change file's `states:`.
- `frontend/tools/state-inventory.ts` drops the "No. 10, both models downloading" not-a-state entry; `frontend/tools/flows/welcome.ts` and the model parts of `flows/settings.ts` are deleted.
- `docs/design/gate.json` is not changed.

### 9.3 Order of work under the design gate

The owner asked for the UI to be implemented and confirmed once, at the end. The gate allows gated edits only while every state is approved at its current hash, so the work is ordered so that it is:

1. **Change file first.** `docs/design/changes/<branch-slug>.md` with `ui: true`, written before anything else.
2. **Design preparation, no visible change.** In `frontend/src/design/**`, make every download-related prop optional, with the no-download behaviour when it is absent. Scenarios keep passing them, so no rendered tree changes. Run `task design:snapshot`: `state-hashes.json` must differ only in `sourceDigest`. Commit.
3. **Gated app edits**, while `python3 tools/design/gate.py check --mode edit --assume-changed <path>` passes: the files under `frontend/src/app/**` stop using the `Models` bindings, the `models:progress` event and the optional props, and read the quality status from `Catalog.QualityStatus`. Commit.
4. **Design and prototype, final.** Delete the optional props, the components, the scenarios; change the copy; retire the states; update the parity cases, fixtures and mock bindings under `frontend/e2e/parity/`. Snapshot, inventory, checks. Commit.
5. **Review build and links**, then stop for the owner's approval of the changed states. No gated file is touched after step 4.

If a hook blocks a gated edit at step 3: do not work around it. Keep a `Models` binding that reports both models `disabled` and an idle quality status, so the unchanged app code keeps working; leave the gated edits and the removal of that shim for after the owner's approval; say so in the pull request.

Until the owner approves, CI's `design` job fails with `unapproved`. That is the gate working.

## 10. Self-check, smoke and CI

- `CHOIX_SELFCHECK=1` report: `onnxRuntime` and `providers` are deleted. New keys: `"vision": {"featurePrint": "ok", "length": 768, "revision": 2}` (one feature print of a generated image through the real platform) and `"quality": "ok"` (the formula on a generated image, checked against a fixed value). A failure puts the message in place of `"ok"` and fails the self-check.
- The self-check no longer creates a `models/` directory.
- `build/smoke/sandbox-smoke.sh`, its test, `.github/workflows/ci.yml` and `docs/release/signing-checklist.md` follow the new report.
- E2E no longer needs both settings off to avoid downloads. `frontend/e2e` keeps them off only where a test wants time-only clusters; the "no model download is attempted" test and the "models directory is empty" assertion are deleted.

## 11. Leftovers in the data directory

Existing installs hold about 530 MB of model files in `<data dir>/models/`. On start-up (not in the self-check) the runtime removes, best effort and logged once: `clip_vit_b32.onnx`, `topiq_nr_koniq_384.onnx`, any file in that directory whose name starts with one of those names (lock and partial files), and then the directory if it is empty **[call]**. Anything else in it is left alone. This is the app's own download cache, not user data.

## 12. Tests

- **Unit:** the formula against §6.3; `Display`; complete-link against the naive rule, its ordering, the 4,000 window, a 2,000-print timing bound; the store migration (`wantVersion` 7) and the print methods; the analyzer's five outcomes (§5.2) with the `platformtest` fake; the scorer's unrated and pending rules; `ClusterParams`; config reading ignores `visual_cluster_threshold`.
- **Platform (darwin):** `vision_test.go` on generated images: length 768, finite, two different images differ, the same image twice is identical, cancellation returns within a second.
- **Retired:** `internal/retired_test.go` lists `internal/ai/local`, `internal/app/inference`, `internal/app/models`, `build/onnxruntime`, `build/quality-model` as retired paths, drops `internal/ai/local` from `netHTTPAllowed`, and fails on an import of `onnxruntime_go` or the string `onnxruntime` in non-test Go code.
- **Suites that must pass before the pull request:** `task test`, `task lint`, `task test:smoke`, `task test:frontend`, `task bindings:check`, `task test:e2e`, `task parity`, the design checks of the gate workflow, and `task package` followed by the head-only sandbox smoke (`task smoke:sandbox -- --head-only`).

## 13. Documentation

- **Living documents are rewritten in place** so that none describes ONNX, CLIP, TOPIQ, a model download or CoreML as part of the app: `CLAUDE.md`, `README.md` (including the removal of the TOPIQ licence section), `v2.md`, `docs/release/uat.md`, `docs/release/signing-checklist.md`, `docs/release/sandbox-smoke.md`, and `docs/superpowers/specs/2026-11-11-choix-2-desktop-design.md` (its models section is replaced by a summary of this spec and a link to it).
- **Historical documents keep their text** and get one line under the title, "Superseded in part by `2026-11-12-builtin-grouping-and-quality-design.md`: the models, ONNX Runtime and the download are gone.": `docs/superpowers/specs/2026-11-11-choix-2-spike-results.md`, `docs/superpowers/plans/2026-11-11-choix-2-engine-and-wails.md`, `docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md`, `docs/plans/2026-11-11-quality-rating.md` **[call]**.
- `docs/design/inventory.md` is regenerated.
- After the change, `grep -riE 'onnx|topiq|coreml|hugging ?face'` over tracked files matches only: this spec, its plan, the four historical documents above, `internal/retired_test.go`, and the old design change file `docs/design/changes/feat-choix-2-backend-9051c1.md`. `CLIP` as a word may also remain in names unrelated to the model (`clip.mov`, the CSS clip check).

## 14. Deferred

- **Removing `com.apple.security.network.client`.** The app no longer needs it, but removing an entitlement changes the sandbox the webview runs in, and only a look at the packaged, sandboxed app proves the UI still loads. `docs/release/sandbox-smoke.md` gets a short runbook for the owner: remove the key from both entitlement files, `task package`, launch, confirm the window renders and a folder scans, then update hard rule 8, the smoke script and the tests.
- **Hardened runtime for ad hoc builds.**
- **A quality presentation that fits what the score means** (flags such as soft, noisy, dark, blown out; or a mark relative to the group).
- **Face checks, subject-aware sharpness, the aesthetics score.** Vision can do them; the benchmark found no gain for the quality score from saliency or aesthetics.
- **Threshold help.** Thresholds do not transfer between kinds of shooting; a per-folder suggestion would help.
- **Validating the grouping on more labelled folders**, and cross-device merging on real two-camera data.

## 15. Acceptance

1. A fresh install makes no network request and creates no `models/` directory.
2. A folder scanned with visual grouping on has a feature print for every file with a readable preview, and its clusters come from complete-link at the configured similarity.
3. A folder last clustered by a 2.0 build re-clusters after it is opened, without the user doing anything, and its picks are unchanged.
4. Every rated photo has a `quality_model` of `choix-sne-1024/v1`; a full rating pass over 2,000 photos takes well under the old model's time.
5. No file in the repository builds, bundles, signs or tests ONNX Runtime or a model.
6. The prototype shows no download anywhere; the app's parity suite passes against it.
7. The suites of §12 pass, except CI's `design` job, which waits for the owner's approval.

## 16. Calls made without the owner

1. Default similarity 0.90, and the old `visual_cluster_threshold` key is ignored (§5.5).
2. Analyze runs four workers (§5.2).
3. Buckets over 4,000 printed files are clustered in windows (§5.4).
4. The cross-device anchor is 0.93, untested (§5.4).
5. No backfill is queued at launch; a folder upgrades when opened or scanned (§5.6).
6. The badge mapping `(raw + 60) / 2` (§6.4).
7. The new copy (§9.1), and the Library's empty state replacing the welcome panel.
8. Gated app edits are made before the design changes, while the gate still passes (§9.3).
9. Old model files are deleted from the app's data directory at start-up (§11).
10. Historical documents keep their text under a "superseded" line (§13).
11. The network entitlement stays until the owner has checked the packaged app without it (§14).
