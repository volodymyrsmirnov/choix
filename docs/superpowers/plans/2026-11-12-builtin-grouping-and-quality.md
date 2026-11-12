# Built-in Grouping and Quality Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the downloaded CLIP and TOPIQ models and ONNX Runtime with Apple's Vision feature print (grouping, complete-link) and a pure-Go quality formula, and remove every trace of the old pieces from code, build, UI and living documentation.

**Architecture:** A new cgo-free `platform.Vision` interface with a darwin implementation feeds a new `internal/analyze` stage that stores feature prints in two new `ai_signals` columns; `internal/group` clusters them with complete-link. `internal/quality` gains the formula and the store-backed scorer. `internal/ai/local`, `internal/app/inference`, the model manager, the download UI and the ONNX build steps are deleted. The work has two phases around the design gate: phase A changes no gated file and leaves a small compatibility shim for the unchanged app code; phase B, after the owner's approval, edits the app code and deletes the shim.

**Tech Stack:** Go 1.26 (cgo + Objective-C for Vision), SQLite (`modernc.org/sqlite`), Wails v3, React/TypeScript/bun, Task, Playwright.

**Spec:** `docs/superpowers/specs/2026-11-12-builtin-grouping-and-quality-design.md`. Read it first; section numbers below (§) refer to it. `CLAUDE.md` is the project rule book.

## Global Constraints

- macOS on Apple Silicon only; every Go command runs through `task` so it gets the Taskfile's environment (arm64, `MACOSX_DEPLOYMENT_TARGET=14.0`). Run one test with `task test -- -run '<regexp>'`.
- **Every commit leaves the tree green.** `task test` and `task lint` (0 issues) pass before every commit that touches Go; `cd frontend && bun test && bun run check && bun run lint:design` before every commit that touches the frontend. When a task changes a type or a function, it also adapts every caller and test the compiler or the test run names, whether or not the task's file list names them.
- The work is unattended: commit with `git -c commit.gpgsign=false commit` (a per-command flag; never change git config); push with `git push https://github.com/<org>/<repo>.git <branch>`; open the pull request with `gh pr create --head <branch>`; say in the pull request body that the commits are unsigned. Never merge, never push to `main`.
- Commit prefixes: `feat(scope):`, `fix(scope):`, `test(scope):`, `chore:`, `docs:`, `plan:`. One topic per commit.
- **Design gate.** Never run `task design:init`, `task design:approve`, `task design:confirm-no-ui`, nor anything from `tools/design/gate_owner.py`; never write `docs/design/approvals.json` or `docs/design/approvers.txt`. **Phase A (Tasks 1–13) edits no gated path**: nothing under `frontend/` outside the exempt list of `docs/design/gate.json`, nothing under `internal/app/{menu,dialogs,window}/`. Test files (`*.test.ts`, `*.test.tsx`, `*.pw.ts`, `*_test.go`) are exempt everywhere. Gated paths are edited only in phase B (Task 14), after the owner's approval. If a hook blocks anything, do not work around it: stop that step, carry on with what does not depend on it, and report.
- One design build at a time: never run two of `design:build`, `design:check-states`, `design:flows`, `design:selftest`, `design:snapshot` together.
- Do not edit `frontend/tools/extract.ts`, do not upgrade Playwright or Chromium, do not touch a pseudo-element, transition, animation, `@keyframes`, `@font-face` or `<html>` style rule: each re-stales every state.
- Originals are read-only; inside a folder write only through `internal/safefs`. Native calls return bytes and run through `platform.Detach`. No `os/exec` in non-test code. Only `cmd/choix` and `internal/app/{menu,dialogs,window}` import Wails. Every worker returns within about a second of cancellation.
- Tests never use the network. cgo tests are `//go:build darwin`. Every new Go file ships with a `_test.go` next to it.
- Nothing from a real photo archive goes into the repository: no folder or file name, serial number, date or place. `frontend/prototype/mock/privacy.test.ts` scans for it.
- Never use bare `git stash`; set work aside with a WIP commit.
- Exact values from the spec: `platform.FeaturePrintLen = 768`; `analyze.PrintID = "vnfp-r2/fill/preview/v1"`; `quality.ScorerID = "choix-sne-1024/v1"`; default similarity `0.90`; TOML key `visual_similarity`; cross-device anchor `0.93`; analyze workers `4`; window `4000`; `K_1 = 0.11196346895601238`, `K_2 = 0.0022760448925639087`; `Display(raw) = clamp((raw + 60) / 2, 0, 10)`; migration `007`, `user_version` 7.

## Review Focus

1. **A folder clustered by an earlier build is opened.** It must re-cluster by itself and keep every pick. Test: Task 7, `TestEnsureClustered_OldParamsRecluster` and the smoke test's pick assertion.
2. **A preview that Vision cannot read (corrupt JPEG, zero bytes) in a reachable folder.** The file is still `analyzed`, is stamped with `PrintID` and a NULL print, and is not re-analyzed on the next job. Test: Task 7, `TestAnalyze_VisionFailureIsStampedOnce`.
3. **A file edited while visual grouping was off.** Its old print must not survive: after a rescan and switching grouping on, the print is made again from the new content. Test: Task 6, `TestUpsertOnRescan_ClearsPrint`; Task 7, `TestJob_ChangedFileWhileOffIsReprinted`.
4. **`config.toml` written by an earlier build**, holding `visual_cluster_threshold = 0.7`. The app must start with similarity 0.90 and write `visual_similarity` on the next save. Test: Task 7, `TestLoad_IgnoresRetiredThresholdKey`.
5. **A time bucket of several thousand near-identical frames (a timelapse), cancelled half-way.** Clustering must stay within memory, stop within a second, and leave the old clusters in place. Test: Task 4, `TestCluster_LargeBucketIsWindowed`, `TestCluster_Cancelled`, `TestRebuild_CancelledKeepsClusters`.

---

# Phase A: unattended, no gated file

### Task 1: Change file and baseline

**Files:**
- Create: `docs/design/changes/<branch-slug>.md` (slug = the branch name lower-cased with every run of characters outside `[a-z0-9]` turned into `-`; `git branch --show-current` gives the branch)

- [ ] **Step 1: Record the baseline.** Run `task design:status`. Expected: `Merge check: pass` and 159 approved states. Run `task test`, `task lint`, `task test:frontend`. Expected: all pass. If the baseline is not clean, stop and report; do not build on a broken base.
- [ ] **Step 2: Write the change file** with front matter `branch: <branch name>`, `states: []`, `retired: []`, `ui: true`, and one paragraph: "Removes the model download UI; the download states are retired and the Settings copy changes. The lists are filled in when the design changes (Task 10)."
- [ ] **Step 3: Verify.** Run `python3 tools/design/gate.py check --mode edit`. Expected: no findings.
- [ ] **Step 4: Commit** `docs(design): change file for built-in grouping and quality`.

### Task 2: The quality formula

**Files:**
- Create: `internal/quality/measure.go`, `internal/quality/measure_test.go`

**Interfaces:**
- Produces:
  ```go
  const ScorerID = "choix-sne-1024/v1"
  const MinShortSide = 384

  type Features struct {
  	Var1, Var2, Noise, DN1, DN2, MidGreyEV, ClipHi float64
  }

  // Measure computes the features of img (§6.2). ok is false, and f is the
  // zero value, when a side of the working copy is under 17 px.
  func Measure(img image.Image) (f Features, ok bool)
  func Raw(f Features) float64
  func Display(raw float64) float64
  ```

- [ ] **Step 1: Write the failing tests.**
  - `TestMeasure_ReferenceImages`: build images A–E exactly as §6.3 defines them (as `*image.NRGBA`), check each channel sum where the spec gives one, and every feature and `Raw` against the table within 1e-6 relative (absolute for values that are 0, and 1e-6 absolute on `raw`). `Display` within 1e-3.
  - `TestKernelEnergy`: the squared norm of the discrete Laplacian-of-Gaussian impulse response at σ = 1 and σ = 2 equals `K_1` and `K_2` within 1e-12.
  - `TestMeasure_NeverNaN`: uniform black and uniform white at 640×480 give finite features and a finite `Raw`; a 1×1, a 15×200 and a 16×200 image give `ok == false` and zero features; a 17×200 image gives `ok == true` and finite features.
  - `TestMeasure_Orders`: starting from image A, a Gaussian-blurred copy (σ = 2), a copy with noise added (`((x·73856093 XOR y·19349663) mod 81) − 40` per pixel, clamped) and a copy with every channel doubled (clamped) each have a lower `Raw` than A.
  - `TestDisplay`: `Display(-60) == 0`, `Display(-40) == 10`, `Display(-50) == 5`, `Display(-200) == 0`, `Display(0) == 10`.
- [ ] **Step 2: Run** `task test -- -run 'TestMeasure|TestKernelEnergy|TestDisplay'`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement** §6.2 step by step in float64. The details that decide the numbers: reflect padding without repeating the edge sample; Gaussian radius `ceil(3σ)`; variance over the interior with a border of `3σ + 2`; the 30th percentile by linear interpolation between order statistics at position `0.30·(n−1)`; the median as the mean of the two middle values for an even count; gradient by central differences inside and one-sided differences at the edges; box resampling to `round(w·s) × round(h·s)` with 8-bit rounding half up, only when the long side exceeds 1024.
- [ ] **Step 4: Run** the same tests. Expected: PASS.
- [ ] **Step 5: Benchmark** `BenchmarkMeasure1600` on a generated 1600×1067 image; note the time per image in the commit message.
- [ ] **Step 6: Commit** `feat(quality): built-in sharpness, noise and exposure score`.

### Task 3: Switch the rating pass to the formula

**Files:**
- Create: `internal/quality/scorer.go`, `internal/quality/scorer_test.go`
- Modify: `internal/quality/pass.go`, `pass_test.go`
- Modify: `internal/app/session/session.go` (`Scorer()`, `sessionNoInferer`), `job.go` (`rate`, `jobScorer`, `rating`), `quality.go` (tracker)
- Modify: `internal/app/runtime/runtime.go` (the tracker's construction: no `Model` option), `runtime_test.go` (the test that waits for an inference call during rating)
- Modify: `internal/app/catalog/format.go`
- Modify their tests: `session/quality_test.go`, `quality_folders_test.go`, `job_test.go`, `catalog/format_test.go`, `catalog_test.go`

**Interfaces:**
- Consumes: Task 2.
- Produces:
  ```go
  // FileScorer rates the photos of one folder's store with Measure.
  type FileScorer struct {
  	FailureGuard func() bool // reports whether the folder is reachable
  }
  func NewFileScorer(s *store.Store, previewPath func(fileID int64) (string, error)) *FileScorer
  func (q *FileScorer) CountPending() (int, error)
  func (q *FileScorer) ListPending(afterID int64, limit int) ([]store.QualityItem, error)
  func (q *FileScorer) Score(ctx context.Context, item store.QualityItem) error
  ```
  `FileScorer` satisfies the existing `quality.Scorer` interface in `pass.go`. `Pass.IsUnavailable` is deleted. The catalog shows a rating only when `quality_model == quality.ScorerID` and displays `quality.Display(raw)`. The job rates whenever `quality_rating` is on (no model readiness). `jobScorer` no longer calls `acquireInference`. The tracker never reports `downloading`, `download_failed` or `error`: its `Model` option and its last-error text are deleted; a pass that ends with an error other than cancellation is logged and leaves the status `idle`. The Go fields `Received`, `Size` and `Error` of `events.Quality` stay declared (unused) until phase B.

- [ ] **Step 1: Write the failing tests.**
  - `TestScorer_RatesAPreview`: a store with one photo and a generated 1200×800 preview on disk; after `Score`, `quality_raw` equals `Raw(Measure(decoded preview))` within 1e-9 and `quality_model == ScorerID`.
  - `TestScorer_SmallPreviewIsUnrated`: a 500×300 preview gives a NULL `quality_raw` stamped with `ScorerID`; `CountPending` then excludes it.
  - `TestScorer_UnreadablePreview`: a zero-byte preview in a reachable folder is stamped unrated; with `FailureGuard` returning false the row stays pending and `Score` returns an error wrapping `pipeline.ErrFolderUnavailable`.
  - `TestScorer_PendingByID`: a row stamped `"other-scorer/v0"` is pending.
  - `TestScorer_Cancelled`: a cancelled context leaves the row pending.
  - `TestPass_ThreeFailuresStop`: three consecutive `Score` errors stop the pass and return the error; the items stay pending (existing behaviour, re-asserted without `IsUnavailable`).
  - Tracker: `TestTracker_NoDownloadOrErrorStates`: with the setting on and no pass it reports `idle`; while a pass runs, `scoring`; after a pass ended with a database error, `idle` with an empty error text; with the setting off, `disabled`.
  - In `catalog/format_test.go`: a member stamped with another scorer id shows no rating; one stamped `ScorerID` with raw −46 shows 7.0.
- [ ] **Step 2: Run** `task test -- -run 'TestScorer|TestPass_|TestTracker_|TestMemberQuality'`. Expected: FAIL.
- [ ] **Step 3: Implement** `scorer.go` (decode with `image/jpeg`; short side under `MinShortSide` is unrated) and switch the session, the job, the tracker, the runtime's tracker construction and the catalog to it. The engine's crop inferer is no longer used by the session; `internal/ai/local` and `internal/app/inference` still compile and are deleted in Task 8.
- [ ] **Step 4: Run** `task test`, `task lint`. Expected: pass.
- [ ] **Step 5: Commit** `feat(quality): rate photos with the built-in score`.

### Task 4: Complete-link clustering, cancellable

**Files:**
- Modify: `internal/group/visual.go`, `visual_test.go`, `group.go`, `group_test.go`, `skew.go`, `skew_test.go`, and every caller of `Cluster`, `clusterIDs` and `EstimateSkew`

**Interfaces:**
- Produces: `func Cluster(ctx context.Context, vectors map[int64][]float32, threshold float64) ([][]int64, error)` with the complete-link rule of §5.4 (strictly greater than the threshold; tie rule; output ordering). It checks `ctx` at least once per matrix row and once per merge. `Cluster` clusters exactly what it is given. The window is the caller's job: `const MaxClusterWindow = 4000` in `group.go`, and `clusterIDs` (which now takes `ctx` and returns an error) cuts a bucket's ids that have a vector, in the bucket's time order, into consecutive runs of at most `MaxClusterWindow` and calls `Cluster` once per run. `RebuildBucket` goes through the same helper, with its files sorted by (captured_at, id) first: no path calls `Cluster` on more than `MaxClusterWindow` vectors. `EstimateSkew` and its pair loop (`skew.go`) take `ctx` too and check it at least once per outer iteration; the cross-device rebuild passes it. Every rebuild path writes clusters only after all of its clustering returned without error; `RebuildAll` keeps its single write after every bucket is computed.

- [ ] **Step 1: Write the failing tests.**
  - `TestCluster_CompleteLinkDoesNotChain`: with θ = acos(0.95), the float32 vectors a = (cos θ, sin θ), b = (1, 0), c = (cos θ, −sin θ) and ids a < b < c. The test first asserts that the two dot products a·b and b·c, computed the way `Cluster` computes them, are exactly equal (the symmetric construction guarantees it after the float32 conversion) and that a·c is about 0.805. At threshold 0.90 the result is `{a, b}` and `{c}`: the two candidate merges tie and (a, b) wins by the tie rule; single-link would have returned one group.
  - `TestCluster_StrictThreshold`: two vectors whose similarity equals the threshold stay apart (use identical vectors and threshold 1.0).
  - `TestCluster_MatchesNaive`: 200 seeded random cases of 2–40 random unit vectors in 8 dimensions and a random threshold; the result equals a naive O(n³) implementation of the rule written in the test.
  - `TestCluster_Ordering`: members ascending, clusters by smallest member.
  - `TestCluster_LengthMismatch`: vectors of different lengths never merge.
  - `TestCluster_Cancelled`: 3,000 random 768-dimensional unit vectors with a context cancelled after 50 ms return `context.Canceled` within 1 s.
  - `TestCluster_TwoThousandUnderFiveSeconds`: 2,000 such vectors finish in under 5 s (skipped under `-short`).
  - `TestCluster_LargeBucketIsWindowed` (in `group_test.go`): a bucket of 4,001 identical vectors yields two clusters, of 4,000 and 1.
  - `TestRebuild_CancelledKeepsClusters` (in `group_test.go`): a store with existing clusters and a rebuild whose context is cancelled during clustering returns the context's error and leaves `clusters` and `cluster_members` exactly as they were. Run it for the per-device path, the cross-device path (cancelled inside `EstimateSkew`) and `RebuildBucket`.
  - `TestRebuildBucket_IsWindowed`: `RebuildBucket` over 4,001 identical vectors yields clusters of 4,000 and 1, the 4,000 being the earliest by capture time.
- [ ] **Step 2: Run** `task test -- -run 'TestCluster|TestRebuild_'`. Expected: FAIL.
- [ ] **Step 3: Implement.** A similarity matrix per call plus either a nearest-neighbour chain or a heap of candidate merges; whichever is chosen must match the naive rule exactly, tie rule included. Existing grouping tests that assumed chaining are updated to the new rule.
- [ ] **Step 4: Run** `task test`, `task lint`. Expected: pass.
- [ ] **Step 5: Commit** `feat(group): complete-link clustering`.

### Task 5: Vision feature print in the platform layer

**Files:**
- Create: `internal/platform/vision.go`, `internal/platform/vision_test.go`
- Create: `internal/platform/darwin/vision/vision.go`, `vision.h`, `vision.m`, `vision_test.go`
- Create: `internal/platform/platformtest/vision.go`; modify `fakes_test.go`

**Interfaces:**
- Produces:
  ```go
  // package platform
  const FeaturePrintLen = 768
  type Vision interface {
  	FeaturePrint(ctx context.Context, in ImageInput) ([]float32, error)
  }
  // package vision (darwin)
  func New() platform.Vision
  // package platformtest: same shape as the Stills fake
  type Vision struct { /* Fail map keyed like Stills' input key; Gate chan struct{} */ }
  func (v *Vision) Calls() []string
  ```

- [ ] **Step 1: Write the failing tests.**
  - darwin `TestFeaturePrint_LengthAndFinite`: a generated 800×600 JPEG (path input, then data input) gives 768 finite values with an L2 norm between 0.9 and 1.1.
  - `TestFeaturePrint_Deterministic`: the same image twice gives identical vectors.
  - `TestFeaturePrint_DifferentImagesDiffer`: a gradient and a checkerboard have a dot product below 0.999.
  - `TestFeaturePrint_Unreadable`: a zero-byte file and a text file return an error starting with `vision:`; no panic.
  - `TestFeaturePrint_Cancelled`: with the test slow hook set, a cancelled context returns within 1 s and `platform.NativeRunning()` returns to its earlier value once the call ends.
  - `TestFeaturePrint_InputValidation`: both or neither of `Path`/`Data`, and a path containing NUL, are rejected in Go.
  - platformtest: equal images give equal vectors, different images differ, length 768, unit norm, `Fail` and `Gate` behave like the `Stills` fake.
- [ ] **Step 2: Run** `task test -- -run 'TestFeaturePrint|TestVisionFake'`. Expected: FAIL.
- [ ] **Step 3: Implement** following `internal/platform/darwin/imageio` file for file (build tags, cgo preamble with `-fobjc-arc -mmacosx-version-min=14.0`, `guarded()` pattern, result struct freed in Go, `platform.Detach`, `slowHook`). Pin `VNGenerateImageFeaturePrintRequestRevision2` and `VNImageCropAndScaleOptionScaleFill`; fail with a message when the revision is unsupported, the element type is not float, or the count is not 768 (§5.1).
- [ ] **Step 4: Run** `task test`, `task lint`. Expected: pass; `internal/app/nativeui_test.go` and `internal/notools_test.go` still pass.
- [ ] **Step 5: Commit** `feat(platform): Vision image feature print`.

### Task 6: Store the prints

**Files:**
- Create: `internal/store/migrations/007_vision_print.sql`
- Modify: `internal/store/ai_signals.go`, `files.go`, `migrate_test.go` (`wantVersion` 7), `ai_signals_test.go`, the test file that covers `UpsertOnRescan`

**Interfaces:**
- Produces, added next to the existing embedding API (which stays until Task 8, because `internal/ai/local` still uses it):
  ```go
  // AISignals gains: Print []float32; PrintID string
  func (r *AISignalsRepo) UpsertPrint(fileID int64, print []float32, printID string) error
  func (r *AISignalsRepo) AllPrints(printID string) (map[int64][]float32, error)
  func (r *FilesRepo) CountAnalyzedMissingPrint(printID string) (int, error)
  func (r *FilesRepo) BumpAnalyzedMissingPrint(printID string) (int64, error)
  ```
  `UpsertPrint` with a nil print stores NULL with the id and leaves `clip_embedding` alone. `BumpAnalyzedMissingPrint` moves the same files to the same status `BumpAnalyzedMissingEmbedding` moves them to. `UpsertOnRescan` clears `vision_print` and `vision_print_id` of a file whose content changed together with its quality rating: it gains a transaction around the two updates (`files` and `ai_signals`), rolled back if either fails. `UpsertPrint` is the final name of the writer; the old `Upsert` goes in Task 8.

- [ ] **Step 1: Write the failing tests**: the migration reaches `user_version` 7 from a version-6 database and keeps existing rows; `UpsertPrint` round-trips 768 floats exactly; a nil print reads back as absent but stamped; `AllPrints` skips NULL prints, other ids and blobs whose length is not 3,072 bytes; `Count`/`Bump…MissingPrint` count analyzed files with no row, a NULL id or another id, and skip stamped ones (including stamped-NULL); `TestUpsertOnRescan_ClearsPrint`: a stamped print disappears when the content hash changes and stays when it does not; the file's pick row is untouched either way.
- [ ] **Step 2: Run** `task test -- -run 'TestMigrate|TestPrint|TestUpsertOnRescan'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `task test`, `task lint`. Expected: pass.
- [ ] **Step 5: Commit** `feat(store): vision print columns`.

### Task 7: Switch grouping to the feature print

**Files:**
- Create: `internal/analyze/analyze.go`, `internal/analyze/analyze_test.go`
- Modify: `internal/app/session/session.go`, `pipeline.go`, `job.go`
- Modify: `internal/app/runtime/runtime.go` (`Deps.Vision`, required, passed on in `session.Deps`), `fakes_test.go`, `runtime_test.go`; `internal/app/testsupport/*` test fixtures that build a runtime; `cmd/choix/main.go` (`Vision: vision.New()` where it builds `runtime.Deps`, shared by the desktop and the server shell); `internal/e2e/uat_test.go` (its runtime dependencies)
- Modify: `internal/group/group.go`, `skew.go` (`anchorSimilarity = 0.93`); `internal/config/config.go`, `toml.go`, `web.go`; `internal/app/settings/store.go`, `service.go`
- Modify tests: `session/pipeline_test.go`, `job_test.go`, `manager_test.go`; `group/*_test.go`; `config/*_test.go`; `settings/store_test.go`; `pipeline/integration_test.go`; `e2e/smoke_test.go`. `session/testdata/_make_v1_state.go` stays as it is (v1 really wrote `clip_embedding`).

**Interfaces:**
- Consumes: Tasks 4, 5, 6.
- Produces: `internal/analyze` as in §5.2 (`PrintID`, `ErrPrintsOff`, `Printer`, `New`, `Analyze`); the session's printer over `platform.Vision`, answering `analyze.ErrPrintsOff` while `visual_grouping` is off; the analyze stage reading `thumb.TierPreview` with 4 workers and without `acquireInference`; `backfillPrints` replacing `backfillEmbeddings`, run whenever `visual_grouping` is on; `Grouper` reading `AllPrints(printID)` with the id passed in by the session (`internal/group` does not import `internal/analyze`); `config.Config.VisualSimilarity` (TOML `visual_similarity`, default 0.90); `session.ClusterParams` ending in `;m=vnfp-r2/fill/preview/v1+complete`.
- After this task nothing in the app calls the CLIP embedder. `internal/ai/local` and the store's embedding methods still compile and are deleted in Task 8.

- [ ] **Step 1: Write the failing tests.**
  - `analyze`: `TestAnalyze_StoresNormalisedPrint` (a fake printer returning a vector of norm 2 stores a unit vector stamped `PrintID`); `TestAnalyze_NoPrinter` and `TestAnalyze_PrintsOff` (analyzed, row unstamped, `CountAnalyzedMissingPrint` is 1); `TestAnalyze_UnreadableImageIsStampedOnce` (printer error wrapping `platform.ErrUnreadableImage`: analyzed, NULL print stamped, `CountAnalyzedMissingPrint` is 0); `TestAnalyze_TransientVisionFailureIsRetried` (any other printer error: analyzed, row unstamped, `CountAnalyzedMissingPrint` is 1); `TestAnalyze_BadVector` (wrong length, zero norm, NaN: stamped like an unreadable image); `TestAnalyze_Cancelled` (row untouched); `TestAnalyze_FolderUnreachable` (returns `pipeline.ErrFolderUnavailable`, nothing recorded); `TestAnalyze_UnreadablePreview` (file `failed` with the error text).
  - session: `TestClusterParams` (exact string for the defaults: `b=600;t=0.9000;x=false;v=true;m=vnfp-r2/fill/preview/v1+complete`); `TestEnsureClustered_OldParamsRecluster` (a store stamped `b=600;t=0.9200;x=false;v=true` re-clusters and its picks table is unchanged); `TestJob_BackfillsPendingPrints` (a folder analyzed with no prints gets them and is re-clustered; a second job re-analyzes nothing); `TestJob_VisualGroupingOff` (no print is made; clusters equal the time buckets even when prints are stored); `TestJob_OffDuringAnalysisThenOn` (grouping switched off while a job analyzes: the remaining files are analyzed unstamped; switching it on and running a backfill gives every file a print); `TestJob_ChangedFileWhileOffIsReprinted` (print stored; grouping off; the file's content changes and the folder is rescanned; grouping on; after the backfill the stored print is the fake's vector for the new content).
  - config: `TestLoad_IgnoresRetiredThresholdKey` (`visual_cluster_threshold = 0.7` loads as 0.90; saving writes `visual_similarity` and no old key); `TestDefaults` (0.90).
  - group: the existing tests on prints instead of embeddings; `TestSkew_AnchorThreshold` (pairs at 0.94 anchor, pairs at 0.92 do not).
- [ ] **Step 2: Run** `task test -- -run 'TestAnalyze|TestClusterParams|TestEnsureClustered|TestJob_|TestLoad_|TestSkew_'`. Expected: FAIL.
- [ ] **Step 3: Implement** in this order, compiling after each: config rename; `internal/analyze`; the runtime's `Deps.Vision` with every fixture that builds a runtime; session printer, stage and backfill; grouper on prints.
- [ ] **Step 4: Run** `task test`, `task lint`, `task test:smoke`. Expected: pass. The smoke test asserts every analyzed file has a print through the `platformtest` fake and that picks survive a re-cluster. Compile the UAT build, which no other task compiles: `task uat -- -run '^$'`. Expected: it builds and runs no test (no archive is needed).
- [ ] **Step 5: Commit** `feat(grouping): group by Vision feature print`.

### Task 8: Delete the models, the engine and the downloader; leave the shim

**Files:**
- Delete: `internal/ai/local/` (all), `internal/app/inference/` (all), `internal/app/session/inference.go`, `inference_test.go`, `internal/app/models/manager.go`, `manager_test.go`
- Rewrite: `internal/app/models/service.go`, `service_test.go` (the shim)
- Modify: `internal/app/catalog/service.go` and its test (`QualityStatus`)
- Modify: `frontend/e2e/parity/mock-bindings/catalog/service.ts` and `mock-bindings/control.test.ts` (the mock must keep matching the generated catalog module's type)
- Modify: `internal/app/runtime/runtime.go`, `fakes_test.go`, `runtime_test.go`; `cmd/choix/main.go`, `selfcheck.go`, `selfcheck_test.go`
- Modify: `internal/app/session/job.go`, `quality.go`, `queue.go` (comment); `internal/app/testsupport/service.go`, `service_test.go`
- Modify: `internal/store/ai_signals.go`, `files.go` and tests (delete `Upsert` of `clip_embedding`, `AllEmbeddings`, `…MissingEmbedding`, the `ClipEmbedding`/`Embedding` fields)
- Modify: `internal/appdir/appdir.go`, `appdir_darwin.go` and its Objective-C bridge, `appdir_test.go`, `appdir_darwin_test.go`, `override_dev_test.go`; create `internal/app/runtime/legacy.go`, `legacy_test.go`
- Modify: `internal/e2e/uat_test.go`, `smoke_test.go`; `internal/pipeline/stage.go` (comment); `go.mod`, `go.sum`
- Regenerated: `frontend/bindings/**`

**Interfaces:**
- Produces:
  - **The shim** (§9.3): `models.Service` with `NewService(quality func() events.Quality, g *opgate.Gate)`, `Status() ([]events.ModelStatus, error)` returning `clip` then `quality`, both `State: "ready"`, sizes 0; `Retry(kind string) error` returning nil for `"clip"` and `"quality"` and the existing invalid-kind error otherwise; `Quality() (events.Quality, error)` as today. A comment at the top says it is a compatibility shim to delete in Task 15.
  - `func (s *Service) QualityStatus() (events.Quality, error)` on the catalog service, same snapshot: the runtime passes the **same** `r.quality.Status` callback to the catalog service and to the shim.
  - The parity mock of the catalog service gains `QualityStatus`, answering from the same fixture field the `Models.Quality` mock reads; both exist during phase A.
  - `events.ModelStatus`, `events.ModelsProgress` and `events.Quality` unchanged in shape (phase B slims them). After `task bindings`, `git diff --stat frontend/bindings` shows changes only in the catalog service files.
  - `runtime.Deps` without `Downloader`, `CPUOnly`, `Embedder`, `CropInferer`; no model manager, no engine, no model part in `Idle()`.
  - `appdir.LegacyModels() string` (the old `models` path, not created); `appdir.Models()`, `appdir.BundleFrameworksDir()`, `bundleFrameworksDir` and the bridge function deleted.
  - `func removeLegacyModels(dir string) (removed []string, err error)` in `runtime/legacy.go`, called once at start-up, not in the self-check.
  - The self-check report of §10.
- Settings change: switching `visual_grouping` or `quality_rating` from off to on queues `session.JobBackfill` for every reachable folder (no readiness condition).

- [ ] **Step 1: Write the failing tests.**
  - `TestRemoveLegacyModels`: a directory holding `clip_vit_b32.onnx`, `topiq_nr_koniq_384.onnx`, `topiq_nr_koniq_384.onnx.lock`, `clip_vit_b32.onnx.part` and `notes.txt` keeps only `notes.txt` and the directory; a directory holding only model files is removed; a missing directory is not an error; a symlink named `clip_vit_b32.onnx` is removed as a link, its target untouched; a symlink in place of the directory itself is left alone.
  - `TestSelfCheckReport`: the JSON has `vision.featurePrint == "ok"`, `vision.length == 768`, `vision.revision == 2`, `quality == "ok"`, and no `onnxRuntime` or `providers` key; with a failing Vision fake the report carries the message and the check fails.
  - `TestShim_Status`: two entries, kinds `clip` and `quality`, both `ready`. `TestShim_Retry`: nil for the two kinds, the invalid-kind error for another. `TestService_QualityStatus` in the catalog: returns the source's snapshot, `disabled` with no source, and the "Choix is quitting." error after the gate closes. In `control.test.ts`: the catalog mock still satisfies `typeof RealCatalog`, and `QualityStatus` resolves with the fixture's quality status.
  - `TestRuntime_SettingsOnQueuesBackfill`: switching `visual_grouping` on queues a backfill for every reachable folder; switching it off queues none.
  - `TestRuntime_IdleWithEmptyQueue`: `Idle()` is true with nothing queued.
- [ ] **Step 2: Run** `task test -- -run 'TestRemoveLegacyModels|TestSelfCheckReport|TestShim_|TestService_QualityStatus|TestRuntime_'`. Expected: FAIL.
- [ ] **Step 3: Delete and rewire.** Remove the packages and files; fix every importer the compiler names; drop the shutdown steps and log lines for models and the inference engine; replace the self-check's ONNX parts; run `go mod tidy`.
- [ ] **Step 4: Run** `task bindings`, then `task test`, `task lint`, `task test:smoke`, `task uat -- -run '^$'` (compile only), `cd frontend && bun run check && bun test`. Expected: pass, with no edit under `frontend/src/` and the bindings diff limited to the catalog service.
- [ ] **Step 5: Commit** `refactor: remove ONNX Runtime, the models and the downloader`.

### Task 9: Build, CI and release scripts

**Files:**
- Delete: `build/onnxruntime/` (all), `build/quality-model/` (all)
- Modify: `Taskfile.yml`, `build/darwin/bundle.sh`, `build/darwin/bundle_test.sh`, `build/taskfile_test.sh`, `build/smoke/sandbox-smoke.sh`, `build/smoke/sandbox-smoke_test.sh`, `.github/workflows/ci.yml`

**Interfaces:**
- Produces: no `deps:onnxruntime` task, no `ORT_DYLIB`, no `CHOIX_ONNXRUNTIME_DYLIB`, no `--dylib`, no `Contents/Frameworks`; `sign:dev` unchanged in what it signs with, its comment now saying the hardened runtime is off for ad hoc builds pending a follow-up; the smoke script and CI check `"featurePrint":"ok"` and `"quality":"ok"` in the self-check report; the four entitlements are still expected.

- [ ] **Step 1: Update the shell tests first** (`bundle_test.sh`: the expected file list has no `Frameworks/`, and `--dylib` is an unknown option; `taskfile_test.sh`: no `deps:onnxruntime`; `sandbox-smoke_test.sh`: the new report). Run each with `sh <file>`. Expected: FAIL.
- [ ] **Step 2: Edit** the Taskfile, the scripts and the workflow.
- [ ] **Step 3: Run** the three shell tests, `task build`, `task package`, then `CHOIX_SELFCHECK=1 bin/Choix.app/Contents/MacOS/Choix`. Expected: tests pass; the bundle has no `Contents/Frameworks`; the report shows `"featurePrint":"ok"` and `"quality":"ok"`.
- [ ] **Step 4: Run** `task smoke:sandbox -- --head-only`. Expected: pass.
- [ ] **Step 5: Commit** `chore(build): drop the ONNX Runtime fetch, bundle and signing steps`.

### Task 10: Design, prototype, parity fixtures and E2E

Everything here is exempt from the gate: `frontend/src/design/**`, `frontend/prototype/**`, `frontend/tools/**`, `frontend/e2e/**`, test files, `docs/design/**`.

**Files:**
- Delete: `frontend/src/design/components/ModelRow.tsx`, `ModelBanner.tsx`, `SettingsModelCard.tsx`, `frontend/src/design/screens/welcome/` (all), and the tests that import them (among them the card's test in `components/Field.test.tsx`)
- Modify: `frontend/src/design/components/index.ts`, `QualityIndicator.tsx`; `frontend/src/design/types.ts`, `format.ts`; `screens/library/*`, `screens/folder/*`, `screens/settings/*` (props, screens, `draft.ts`) and their tests
- Modify: `frontend/prototype/mock/data.ts`, `settings.ts`; `frontend/prototype/scenarios/welcome.tsx`, `library.tsx`, `libraryWiring.ts`, `folder.tsx`, `settings.tsx`, `gallery.tsx`
- Modify: `frontend/tools/flows/welcome.ts` (rewritten for states 30–37 and 40), `flows/settings.ts`, `frontend/tools/state-inventory.ts` and its test
- Modify: `frontend/e2e/parity/cases.ts`, `fixtures/*.ts`, the `*Cases.test.*` files; `frontend/e2e/api/library.e2e.ts`, `ui/fixtures.ts`, `global-setup.ts`
- Modify: tests under `frontend/src/app/**` (`*.test.ts(x)` only) that assert the removed UI
- Modify: `docs/design/retired.json`, `docs/design/changes/<branch-slug>.md`; regenerated `docs/design/state-hashes.json`, `docs/design/inventory.md`

**Interfaces:**
- Produces: the rendering and copy of §9.1, character for character; `DEFAULTS.similarity = 0.9`; states 11–17, 112, 120, 216, 228, 505, 506, 514, 515, 516, 517 retired.
- **Compatibility (§9.3).** No non-test file under `frontend/src/app/` is edited, and `bun run check` still passes. Wherever the unchanged app code needs a name, keep it as an optional, ignored member marked `/** @deprecated removed in phase B */`: the download props of `LibraryProps` and `SettingsProps`, the four members of `FolderGrouping`, the `ModelStatus` type, the old members of `QualityState` and `QualityStatus`. Put these only in `types.ts` and `props.ts` files. `SettingsScreen.tsx`, `FolderScreen.tsx`, `ClusterList.tsx`, `ClusterStrip.tsx`, `FocusMedia.tsx`, `FocusRail.tsx`, `screens/focus/logic.ts` and `screens/focus/props.ts` are hashed into states: they must reach their final content in this task, reference no deprecated member, and not be touched in phase B.
- The parity mock bindings and the E2E harness keep their `Models` entries until Task 14 (the unchanged app still calls them). In this task the parity mock's `Models.Status` stops reading the fixture and always resolves with two entries, `clip` and `quality`, both `ready`, like the Go shim: the unchanged Library code waits for that answer before it leaves its loading state. Fixtures no longer carry a `models` field, and no fixture or prototype file imports a model mock (`MODELS_READY`, `model`, `ERR_CLIP`, `ERR_QUALITY`) or the `ModelStatus` type after this task, so phase B can delete the type by touching only `types.ts` and the three `props.ts` files.
- Details the unchanged app code relies on, to keep until Task 14 or 15: `ModelStatus` keeps its field types (`kind` and `state` stay required); the model callbacks and props of `LibraryProps` and `SettingsProps` stay explicitly typed and optional; `FolderGrouping` keeps its four values; `QualityStatus` keeps an optional `error`, and its `received` and `size` stay as the generated event has them.

- [ ] **Step 1: Update the design tests** (`LibraryScreen.test.tsx`, `FolderScreen.test.tsx`, `settings/draft.test.ts`, `validate.test.ts`, `Field.test.tsx`) to the new rendering and copy. Run `cd frontend && bun test`. Expected: FAIL.
- [ ] **Step 2: Edit the design.** Delete the components and the welcome screen. `LibraryScreen` shows the empty state whenever there is no folder and the list has loaded, shows no model banner, and its title bar shows no quality indicator. `FolderScreen` shows no time-only notice for any grouping value. `SettingsScreen` renders no model card, the status line has no download or error text, and the three hints carry the copy of §9.1. `QualityIndicator` handles `scoring` and `waiting` only.
- [ ] **Step 3: Edit the prototype.** Delete the scenarios of the retired states; set `docs/design/retired.json` to `[11, 12, 13, 14, 15, 16, 17, 112, 120, 216, 228, 505, 506, 514, 515, 516, 517]`; update the mocks, the gallery states 803, 805 and 807, and the flows (the welcome flow now walks the scan-progress states of its group).
- [ ] **Step 4: Update parity fixtures, cases, mocks and E2E**: make the `Models.Status` mock answer `ready` for both kinds without a fixture, and add a test in the parity tests that a Library fixture with no `models` field reaches its screen (not the loading state); remove the cases and fixtures of retired states and of download events; delete the "no model download is attempted" test and the "models directory is empty" assertion; in the E2E config keep `visual_grouping = false` and `quality_rating = false` only where a test depends on time-only clusters or on no ratings.
- [ ] **Step 5: Run** `task design:inventory` first (the frontend tests compare `docs/design/inventory.md` with the scenarios and with `retired.json`), then `cd frontend && bun test && bun run check && bun run lint:design`, then, one at a time, `task design:check-states`, `task design:flows`, `task design:snapshot`. Expected: 0 failures each. Run `git status --short frontend/src/app | grep -v '\.test\.'`. Expected: no output.
- [ ] **Step 6: Fill in the change file**: `retired:` the seventeen numbers; `states:` every state `task design:status` lists as new or stale. Run `task design:status`. Expected findings: only `unapproved` and `unretired`; no `unlisted-state`, no `stale-snapshot`, no `bad-change-file`.
- [ ] **Step 7: Run** `task test:frontend`, `task bindings:check`, `task test:e2e`. Expected: pass. Run `task parity` and record its result as partial: it covers only states still approved at their current hash (§12). A failure in a state it does cover is a real failure: fix it.
- [ ] **Step 8: Commit** `feat(design): remove the model download UI`, with `docs/design/state-hashes.json`, `inventory.md`, `retired.json` and the change file in the same commit.

### Task 11: Documentation

**Files:**
- Modify: `CLAUDE.md`, `README.md`, `v2.md`, `docs/release/uat.md`, `docs/release/signing-checklist.md`, `docs/release/sandbox-smoke.md`, `docs/superpowers/specs/2026-11-11-choix-2-desktop-design.md`
- Modify (one line under the title each): `docs/superpowers/specs/2026-11-11-choix-2-spike-results.md`, `docs/superpowers/plans/2026-11-11-choix-2-engine-and-wails.md`, `docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md`, `docs/plans/2026-11-11-quality-rating.md`

- [ ] **Step 1: Rewrite `CLAUDE.md`** so it describes the code as it now is: hard rules 4, 5, 6 and 8 as §4 words them; the architecture tree and repository layout (no `ai/local`, `inference`; add `analyze`, `platform/darwin/vision`); the build table (no `deps:onnxruntime`, no bundled library; `sign:dev` note); the "Models and inference" section replaced by "Visual grouping and quality rating" summarising §5 and §6, including that changing the formula bumps `quality.ScorerID` and changing how prints are made bumps `analyze.PrintID`; the SQLite section (migrations 001–007, `user_version` 7, retired column `clip_embedding`); the configuration table (`visual_similarity`, default 0.90; no downloads); "Where data lives" (no `models/`); the caveats (drop the ONNX and hardened-runtime-reason entries, keep the facts that still hold); "Where to stop and ask" (any network access at all: ask first). Add one clearly marked temporary paragraph: the `Models` binding in `internal/app/models` and the unused event types are a compatibility shim that phase B deletes.
- [ ] **Step 2: Rewrite the other living documents** the same way; in `README.md` delete the TOPIQ licence section and the model sizes; in `docs/release/sandbox-smoke.md` add the runbook of §14 for removing `network.client`; in the 2.0 design spec replace the models section with a short summary and a link to the new spec, and fix the entitlement rationale.
- [ ] **Step 3: Add the superseded line** to the four historical documents (§13 gives the wording).
- [ ] **Step 4: Verify.** Run `git grep -n -i -E 'onnx|topiq|coreml|hugging ?face' -- . ':!docs/superpowers/specs/2026-11-12-builtin-grouping-and-quality-design.md' ':!docs/superpowers/plans/2026-11-12-builtin-grouping-and-quality.md' ':!docs/superpowers/specs/2026-11-11-choix-2-spike-results.md' ':!docs/superpowers/plans/2026-11-11-choix-2-engine-and-wails.md' ':!docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md' ':!docs/plans/2026-11-11-quality-rating.md' ':!docs/design/changes/feat-choix-2-backend-9051c1.md' ':!internal/retired_test.go' ':!internal/app/runtime/legacy.go' ':!internal/app/runtime/legacy_test.go' ':!cmd/choix/selfcheck_test.go'`. Expected: no output (the self-check test is excluded because it asserts that the report has no `onnxRuntime` key). Run `git grep -n -w -i clip` with the same exclusions and check every remaining hit is one of: a name unrelated to the model (`clip.mov`, the CSS clip check, `clip_hi`), the retired column `clip_embedding`, or the phase A shim, the parity `Models` mock and the unchanged app code that phase B removes.
- [ ] **Step 5: Run** `cd frontend && bun test prototype/mock/privacy.test.ts`. Expected: pass.
- [ ] **Step 6: Commit** `docs: built-in grouping and quality`.

### Task 12: Keep it removed

**Files:**
- Modify: `internal/retired_test.go`

- [ ] **Step 1: Extend the test**: add `internal/ai/local`, `internal/app/inference`, `internal/app/session/inference.go`, `internal/app/models/manager.go`, `build/onnxruntime`, `build/quality-model` to `retiredPaths`; remove `internal/ai/local` from `netHTTPAllowed`; add `TestNoONNX`, which walks `cmd/` and `internal/` and fails on a non-test Go file that imports `github.com/yalue/onnxruntime_go` or contains `onnxruntime`, and on `go.mod` naming that module.
- [ ] **Step 2: Run** `task test -- -run 'TestRetired|TestNoONNX|TestNetHTTP'`. Expected: PASS. Prove it bites: add a scratch non-test file under `internal/` containing the import, run again, expected FAIL; delete the scratch file.
- [ ] **Step 3: Commit** `test: keep ONNX Runtime and the models removed`.

### Task 13: Verification, review, pull request, stop

- [ ] **Step 1: Run every suite**, in this order, and keep the output: `task fmt` (no diff), `task lint`, `task test`, `task test:smoke`, `task test:frontend`, `task bindings:check`, `task test:e2e`, `task parity` (partial, see Task 10), `task design:status`, `task package`, `task smoke:sandbox -- --head-only`. Expected: all pass; `design:status` reports only `unapproved` and `unretired`.
- [ ] **Step 2: Try the real engine end to end.** Generate a folder with `internal/e2e/genfolder`, start the server build (`task run:server`), add the folder through the test-support binding, wait for idle, then check: `sqlite3 <folder>/.choix/state.db "select count(*) from ai_signals where vision_print is not null"` equals the number of files with a preview; every rated row has `quality_model = 'choix-sne-1024/v1'`; a pick copies the file into `picks/` and an undo removes the pick row but, as before, never the original. Stop the server.
- [ ] **Step 3: Independent review.** Per the owner's standing roles for this project: a review round by a Fable sub-agent (`model: "fable"`, up to 3 iterations) and one by Codex (`codex exec -m gpt-6-astra -s read-only -C <worktree> -o <file> "<prompt>"`, up to 3 iterations), then `gpt-6.1-sol` (up to 5). Fix what they find; re-run the affected suites.
- [ ] **Step 4: Review build.** `task design:build && task design:serve` in the background (port 8811). Collect one Markdown link per new or changed state, grouped by screen, exactly as `task design:status` prints them.
- [ ] **Step 5: Push and open the pull request** (unattended rules in Global Constraints). The body states: what changed; that commits are unsigned; the suites run and their results, parity marked partial; that CI's `design` job fails until the owner approves; the review links; the seventeen numbers to retire; that phase B (Tasks 14–16) follows the approval; the calls of spec §16; the deferred items of §14.
- [ ] **Step 6: Stop and report.** Give the owner: the pull request link; the review links; the two commands that are theirs to run in their own terminal, `task design:approve -- all` (or the listed numbers) and `task design:approve -- --retire 11 12 13 14 15 16 17 112 120 216 228 505 506 514 515 516 517`, followed by committing `docs/design/approvals.json`; and that phase B starts when they say the states are approved. Do nothing further until then.

---

# Phase B: after the owner has approved and retired the states

Start only when the owner says so. If they ask for a design change instead, go back to Task 10.

### Task 14: App code

The only task that edits gated files.

**Files:**
- Modify: `frontend/src/app/shell/api.ts`, `events.ts`, `App.tsx`, `context.tsx`, `qualityStore.ts`
- Delete: `frontend/src/app/shell/modelsStore.ts`, `modelsStore.test.ts`
- Modify: `frontend/src/app/library/LibraryContainer.tsx`, `libraryStore.ts`, `windowSources.ts`
- Modify: `frontend/src/app/settings/SettingsContainer.tsx`, `settingsDraft.ts`
- Modify: `frontend/src/app/folder/FolderContainer.tsx`, `folderProps.ts`
- Modify: the `*.test.ts(x)` files next to them
- Modify: `frontend/e2e/parity/mock-bindings/catalog/service.ts` (add `QualityStatus`, taking over the quality fixture behaviour of the `Models` mock), `mock-bindings/control.ts` and its test; delete `frontend/e2e/parity/mock-bindings/models/`; modify `frontend/e2e/harness/main.ts`, `frontend/e2e/api/fixtures.ts` (no `Models`)

**Interfaces:**
- Produces: no file under `frontend/src/app/**` imports the `Models` binding, subscribes to `models:progress`, or passes a deprecated prop; `groupingOf` is deleted and the Folder container passes no `grouping` (nothing in the design reads it since phase A); the quality store starts from `Catalog.QualityStatus()` and applies `quality:status`; `onlineSource` is deleted. The parity mocks lose `wireQuality` and `mock-bindings/models/` (with its `shim.ts`), and the tests that import the shim are updated.

- [ ] **Step 1: Check the gate.** Pull the owner's approval commit. Run `task design:status`. Expected: `Merge check: pass` apart from `parity-not-enabled`-type findings this task resolves; no `unapproved`, no `unretired`. For each file to edit run `python3 tools/design/gate.py check --mode edit --assume-changed <path>`. Expected: no findings. Otherwise stop and report.
- [ ] **Step 2: Update the tests first**: `folderProps.test.tsx` (no `groupingOf`; the props carry no `grouping`), `libraryStore.test.ts` (no models in `libraryView`; no `retryModel`/`toggleModel`), `LibraryContainer.test.tsx`, `settingsDraft.test.ts` (no retry), `qualityStore.test.ts` (starts from `Catalog.QualityStatus`, then applies an event), `store.test.ts`, `control.test.ts`. Run `cd frontend && bun test`. Expected: FAIL.
- [ ] **Step 3: Edit the app files and the mocks** to the interface above. Containers only stop passing props; no markup or style changes.
- [ ] **Step 4: Run** `cd frontend && bun test && bun run check`. Run `git grep -n "Models\.\|models:progress\|modelsStore" -- frontend/src/app frontend/e2e`. Expected: no match.
- [ ] **Step 5: Commit** in one commit, `refactor(app): read the quality status from the catalog, drop the model bindings`.

### Task 15: Delete the shim and the deprecated names

**Files:**
- Delete: `internal/app/models/` (all)
- Modify: `cmd/choix/main.go` (four services), `events.go`, `events_test.go`; `internal/app/events/events.go`, `events_test.go`, `coalescer_test.go`; `internal/app/runtime/runtime.go` and tests; `internal/retired_test.go` (add `internal/app/models`)
- Modify: `frontend/src/design/types.ts`, `screens/library/props.ts`, `screens/settings/props.ts`, `screens/folder/props.ts` (delete every `@deprecated` member); nothing else under `frontend/src/design/`
- Modify: `CLAUDE.md` (delete the temporary paragraph; four services)
- Regenerated: `frontend/bindings/**`, `docs/design/state-hashes.json`

**Interfaces:**
- Produces: the end state of §8: four services; no `events.ModelsProgress`, no `events.ModelStatus`; `events.Quality` without `Received`, `Size`, `Error` (the tracker's comparison in `internal/app/session/quality.go` loses them too); no `FolderGrouping` type and no `grouping` prop (the prototype's state 227 scenario stops passing it; its rendering does not change); no `ModelStatus` type.

- [ ] **Step 1: Write the failing tests**: `events.Names()` does not list `models:progress`; a `Quality` value marshals without `received`, `size` and `error`; the registration test in `cmd/choix` expects four services.
- [ ] **Step 2: Delete and regenerate.** Remove the package and the types; run `task bindings`; delete the deprecated design members.
- [ ] **Step 3: Run** `task test`, `task lint`, `cd frontend && bun test && bun run check && bun run lint:design`. Expected: pass.
- [ ] **Step 4: Snapshot.** Run `task design:snapshot`, then `git diff docs/design/state-hashes.json`. Expected: only `sourceDigest` differs. If a state hash changed, a hashed file was touched: undo that edit and find another way; do not ask for a re-approval over a wiring change.
- [ ] **Step 5: Verify the grep.** `git grep -n -w -i clip`, with the path exclusions of Task 11 Step 4, now matches only names unrelated to the model and the retired column. `git grep -n -i "model" -- frontend/src/app internal/app` matches nothing about a downloaded model.
- [ ] **Step 6: Commit** `refactor: delete the model binding shim`.

### Task 16: Final verification

- [ ] **Step 1: Run every suite**: `task fmt` (no diff), `task lint`, `task test`, `task test:smoke`, `task test:frontend`, `task bindings:check`, `task test:e2e`, `task parity` (full: every group in `enabled.json`, all pass), `task design:check`, `task package`, `task smoke:sandbox -- --head-only`. Expected: all pass, the merge check included.
- [ ] **Step 2: Push** to the same branch and update the pull request body: phase B done, parity full, the gate's merge check passing.
- [ ] **Step 3: Report** to the owner. Merging is theirs.
