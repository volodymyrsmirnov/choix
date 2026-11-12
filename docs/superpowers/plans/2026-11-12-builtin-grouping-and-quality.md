# Built-in Grouping and Quality Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the downloaded CLIP and TOPIQ models and ONNX Runtime with Apple's Vision feature print (grouping, complete-link) and a pure-Go quality formula, and remove every trace of the old pieces from code, build, UI and living documentation.

**Architecture:** A new cgo-free `platform.Vision` interface with a darwin implementation feeds a new `internal/analyze` stage that stores feature prints in two new `ai_signals` columns; `internal/group` clusters them with complete-link. `internal/quality` gains the formula and the store-backed scorer. `internal/ai/local`, `internal/app/inference`, `internal/app/models`, the model download UI and the ONNX build steps are deleted. Gated app UI edits are made early, while every design state is still approved; the design and prototype change last.

**Tech Stack:** Go 1.26 (cgo + Objective-C for Vision), SQLite (`modernc.org/sqlite`), Wails v3, React/TypeScript/bun, Task, Playwright.

**Spec:** `docs/superpowers/specs/2026-11-12-builtin-grouping-and-quality-design.md`. Read it first; section numbers below (§) refer to it. `CLAUDE.md` is the project rule book.

## Global Constraints

- macOS on Apple Silicon only; every Go command runs through `task` so it gets the Taskfile's environment (arm64, `MACOSX_DEPLOYMENT_TARGET=14.0`). Run one test with `task test -- -run '<regexp>'`.
- `task test` and `task lint` are clean (lint: 0 issues) before every commit that touches Go. Frontend commits: `cd frontend && bun test && bun run check && bun run lint:design`.
- The work is unattended: commit with `git -c commit.gpgsign=false commit` (a per-command flag; never change git config); push with `git push https://github.com/<org>/<repo>.git <branch>`; open the pull request with `gh pr create --head <branch>`; say in the pull request body that the commits are unsigned. Never merge, never push to `main`.
- Commit prefixes: `feat(scope):`, `fix(scope):`, `test(scope):`, `chore:`, `docs:`, `plan:`. One topic per commit.
- **Design gate.** Never run `task design:init`, `task design:approve`, `task design:confirm-no-ui`, nor anything from `tools/design/gate_owner.py`; never write `docs/design/approvals.json` or `docs/design/approvers.txt`. Gated paths (`frontend/**` minus the exempt list in `docs/design/gate.json`, and `internal/app/{menu,dialogs,window}/**`) are edited **only in Task 4**. If a hook blocks an edit, do not work around it: follow §9.3's fallback and carry on with the ungated tasks.
- One design build at a time: never run two of `design:build`, `design:check-states`, `design:flows`, `design:selftest`, `design:snapshot` together.
- Do not edit `frontend/tools/extract.ts`, do not upgrade Playwright or Chromium, do not touch a pseudo-element, transition, animation, `@keyframes`, `@font-face` or `<html>` style rule: each re-stales every state.
- Originals are read-only; inside a folder write only through `internal/safefs`. Native calls return bytes and run through `platform.Detach`. No `os/exec` in non-test code. Only `cmd/choix` and `internal/app/{menu,dialogs,window}` import Wails.
- Tests never use the network. cgo tests are `//go:build darwin`. Every new Go file ships with a `_test.go` next to it.
- Nothing from a real photo archive goes into the repository: no folder or file name, serial number, date or place. `frontend/prototype/mock/privacy.test.ts` scans for it.
- Never use bare `git stash`; set work aside with a WIP commit.
- Exact values from the spec: `platform.FeaturePrintLen = 768`; `analyze.PrintID = "vnfp-r2/fill/preview/v1"`; `quality.ScorerID = "choix-sne-1024/v1"`; default similarity `0.90`; TOML key `visual_similarity`; cross-device anchor `0.93`; analyze workers `4`; window `4000`; `K_1 = 0.11196346895601238`, `K_2 = 0.0022760448925639087`; `Display(raw) = clamp((raw + 60) / 2, 0, 10)`; migration `007`, `user_version` 7.

## Review Focus

1. **A folder clustered by an earlier build is opened.** It must re-cluster by itself and keep every pick. Test: Task 10, `TestEnsureClustered_OldParamsRecluster` and the smoke test's pick assertion.
2. **A preview that Vision cannot read (corrupt JPEG, zero bytes) in a reachable folder.** The file is still `analyzed`, is stamped with `PrintID` and a NULL print, and is not re-analyzed on the next job. Test: Task 10, `TestAnalyze_VisionFailureIsStampedOnce`.
3. **`config.toml` written by an earlier build**, holding `visual_cluster_threshold = 0.7`. The app must start with similarity 0.90 and write `visual_similarity` on the next save. Test: Task 10, `TestLoad_IgnoresRetiredThresholdKey`.
4. **A blank or tiny frame.** A uniform image must score without NaN or Inf (`raw` finite, display 0); a preview under 384 px on its short side is unrated, not failed. Test: Task 5 (`TestMeasure_ReferenceImages` image E, `TestMeasure_NeverNaN`) and Task 6 (`TestScorer_SmallPreviewIsUnrated`).
5. **A time bucket of several thousand near-identical frames (a timelapse).** Clustering must finish and stay within memory. Test: Task 7, `TestCluster_LargeBucketIsWindowed` and `TestCluster_TwoThousandUnderFiveSeconds`.

---

### Task 1: Change file and baseline

**Files:**
- Create: `docs/design/changes/<branch-slug>.md` (slug = the branch name lower-cased with every run of characters outside `[a-z0-9]` turned into `-`; `git branch --show-current` gives the branch)

**Interfaces:**
- Produces: a valid change file, so the gate's edit check can pass in Task 4.

- [ ] **Step 1: Record the baseline.** Run `task design:status`. Expected: `Merge check: pass` and 159 approved states. Run `task test`, `task lint`, `task test:frontend`. Expected: all pass. If the baseline is not clean, stop and report; do not build on a broken base.
- [ ] **Step 2: Write the change file** with front matter `branch: <branch name>`, `states: []`, `retired: []`, `ui: true`, and one paragraph: "Removes the model download UI; the download states are retired and the Settings copy changes. The lists are filled in when the design changes (Task 13)."
- [ ] **Step 3: Verify.** Run `python3 tools/design/gate.py check --mode edit --assume-changed frontend/src/app/shell/api.ts`. Expected: no findings.
- [ ] **Step 4: Commit** `docs(design): change file for built-in grouping and quality`.

### Task 2: Design preparation, no visible change

Make the app able to stop passing download-related props, without changing any rendered state.

**Files:**
- Modify: `frontend/src/design/screens/library/props.ts`, `LibraryScreen.tsx`; `frontend/src/design/screens/settings/props.ts`, `SettingsScreen.tsx`; `frontend/src/design/screens/folder/props.ts`; `frontend/src/design/types.ts`
- Modify: the matching `*.test.tsx` files in those folders
- Regenerated: `docs/design/state-hashes.json`

**Interfaces:**
- Produces: in `LibraryProps`, `models`, `online`, `onRetryModel`, `onToggleModel` are optional; absent `models` means "settled" (never the welcome panel, never a model banner). In `SettingsProps`, `models`, `retrying`, `onRetryModel`, `onRetryQuality` are optional; absent means no model card. In `QualityStatus`, `received` and `size` are optional. `FolderGrouping` is unchanged in this task.

- [ ] **Step 1: Write failing tests** in `LibraryScreen.test.tsx` and the Settings screen test: rendering without the optional props shows the empty Library (no welcome panel, no element with `data-testid="model-banner"`) and a Settings screen with no model card.
- [ ] **Step 2: Run** `cd frontend && bun test`. Expected: the new tests fail to type-check or fail.
- [ ] **Step 3: Make the props optional** with those defaults. Do not touch any scenario, mock, copy string or style.
- [ ] **Step 4: Run** `cd frontend && bun test && bun run check && bun run lint:design`. Expected: pass.
- [ ] **Step 5: Snapshot.** Run `task design:snapshot`, then `git diff docs/design/state-hashes.json`. Expected: only `sourceDigest` differs. If any state hash changed, a default changed a rendered tree: fix it and snapshot again. Run `task design:status`: still 159 approved.
- [ ] **Step 6: Commit** `refactor(design): make the model download props optional`.

### Task 3: `Catalog.QualityStatus` binding

**Files:**
- Modify: `internal/app/catalog/` (the `Service` facade file and its test), `internal/app/runtime/runtime.go` (pass the quality status source to the catalog service)
- Regenerated: `frontend/bindings/**`

**Interfaces:**
- Produces: `func (s *Service) QualityStatus() (events.Quality, error)` on the catalog `Service`: enters the admission gate, returns the same snapshot `Models.Quality` returns today (`{State: "disabled"}` when there is no source). `Models.Quality` stays until Task 11.

- [ ] **Step 1: Write the failing test** `TestService_QualityStatus` in the catalog service test: with a source returning `events.Quality{State: "scoring", Done: 3, Total: 9}` the binding returns it; with no source it returns `State == "disabled"`; after the gate closes it returns the "Choix is quitting." error like every other binding.
- [ ] **Step 2: Run** `task test -- -run TestService_QualityStatus`. Expected: FAIL (method missing).
- [ ] **Step 3: Implement** the method and the wiring.
- [ ] **Step 4: Run** `task test -- -run TestService_QualityStatus`, then `task bindings`. Expected: PASS; `frontend/bindings/.../catalog/service.ts` gains `QualityStatus`.
- [ ] **Step 5: Commit** `feat(catalog): quality status binding`.

### Task 4: Gated app edits

The only task that edits gated files. Everything here is in `frontend/src/app/**`.

**Files:**
- Modify: `frontend/src/app/shell/api.ts`, `events.ts`, `App.tsx`, `context.tsx`, `qualityStore.ts`
- Delete: `frontend/src/app/shell/modelsStore.ts`, `modelsStore.test.ts`
- Modify: `frontend/src/app/library/LibraryContainer.tsx`, `libraryStore.ts`, `windowSources.ts`
- Modify: `frontend/src/app/settings/SettingsContainer.tsx`, `settingsDraft.ts`
- Modify: `frontend/src/app/folder/FolderContainer.tsx`, `folderProps.ts`
- Modify: the `*.test.ts(x)` files next to them (exempt from the gate)

**Interfaces:**
- Consumes: Task 2's optional props; Task 3's `Catalog.QualityStatus`.
- Produces: no file under `frontend/src/app/**` imports the `Models` binding, subscribes to `models:progress`, or reads `received`/`size`; `groupingOf(settings)` returns `'visual'` when `visual_grouping` is on and `'time-off'` otherwise; the quality store starts from `Catalog.QualityStatus()`; `onlineSource` is deleted.

- [ ] **Step 1: Check the gate** for each file to be edited: `python3 tools/design/gate.py check --mode edit --assume-changed <path>`. Expected: no findings. If there are findings, stop this task and apply §9.3's fallback.
- [ ] **Step 2: Update the tests first**: `folderProps.test.tsx` (`groupingOf` has two outcomes), `libraryStore.test.ts` (no models in `libraryView`; no `retryModel`/`toggleModel`), `LibraryContainer.test.tsx`, `settingsDraft.test.ts` (no retry), `qualityStore.test.ts` (starts from `Catalog.QualityStatus`), `store.test.ts`. Run `cd frontend && bun test`. Expected: FAIL.
- [ ] **Step 3: Edit the app files** to the interface above. Do not change markup or styles; containers only stop passing props.
- [ ] **Step 4: Run** `cd frontend && bun test && bun run check`. Expected: pass. Run `grep -rn "Models\.\|models:progress\|modelsStore" frontend/src/app`. Expected: no match.
- [ ] **Step 5: Commit** all of it in one commit, `refactor(app): stop using the model bindings`. The commit hook re-checks the gate; expected: allowed.

### Task 5: The quality formula

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

  // Measure computes the features of img (§6.2). ok is false when the image
  // is empty.
  func Measure(img image.Image) (f Features, ok bool)
  func Raw(f Features) float64
  func Display(raw float64) float64
  ```

- [ ] **Step 1: Write the failing tests.**
  - `TestMeasure_ReferenceImages`: build images A–E exactly as §6.3 defines them (as `*image.NRGBA`), check each channel sum where the spec gives one, and every feature and `Raw` against the table within 1e-6 relative (absolute for values that are 0, and 1e-6 absolute on `raw`). `Display` within 1e-3.
  - `TestKernelEnergy`: the squared norm of the discrete Laplacian-of-Gaussian impulse response at σ = 1 and σ = 2 equals `K_1` and `K_2` within 1e-12.
  - `TestMeasure_NeverNaN`: uniform black, uniform white, a 1×1 image and a 9×9 image give finite features and a finite `Raw`.
  - `TestMeasure_Orders`: starting from image A, a Gaussian-blurred copy, a copy with added noise (±40 by the hash in §6.3 with modulus 81) and a copy with every channel doubled each have a lower `Raw` than A.
  - `TestDisplay`: `Display(-60) == 0`, `Display(-40) == 10`, `Display(-50) == 5`, `Display(-200) == 0`, `Display(0) == 10`.
- [ ] **Step 2: Run** `task test -- -run 'TestMeasure|TestKernelEnergy|TestDisplay'`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement** §6.2 step by step in float64. The details that decide the numbers: reflect padding without repeating the edge sample; Gaussian radius `ceil(3σ)`; variance over the interior with a border of `3σ + 2`; the 30th percentile by linear interpolation between order statistics at position `0.30·(n−1)`; the median as the mean of the two middle values for an even count; gradient by central differences inside and one-sided differences at the edges; box resampling to `round(w·s) × round(h·s)` with 8-bit rounding half up, only when the long side exceeds 1024.
- [ ] **Step 4: Run** the same tests. Expected: PASS.
- [ ] **Step 5: Benchmark** `BenchmarkMeasure1600` on a generated 1600×1067 image; note the time per image in the commit message.
- [ ] **Step 6: Commit** `feat(quality): built-in sharpness, noise and exposure score`.

### Task 6: Switch the rating pass to the formula

**Files:**
- Create: `internal/quality/scorer.go`, `internal/quality/scorer_test.go`
- Modify: `internal/app/session/session.go` (`Scorer()`), `job.go` (`rate`, `jobScorer`, `rating`), `quality.go` (tracker), `internal/app/catalog/format.go`, `internal/quality/pass.go`
- Modify: their tests (`session/quality_test.go`, `quality_folders_test.go`, `job_test.go`, `catalog/format_test.go`, `catalog_test.go`, `quality/pass_test.go`)

**Interfaces:**
- Consumes: Task 5.
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
  `FileScorer` satisfies the existing `quality.Scorer` interface in `pass.go`. `Pass.IsUnavailable` is deleted. The catalog shows a rating only when `quality_model == quality.ScorerID` and displays `quality.Display(raw)`. The job rates whenever `quality_rating` is on (no model readiness). `jobScorer` no longer calls `acquireInference`.

- [ ] **Step 1: Write the failing tests.**
  - `TestScorer_RatesAPreview`: a store with one photo and a generated 1200×800 preview on disk; after `Score`, `quality_raw` equals `Raw(Measure(decoded preview))` within 1e-9 and `quality_model == ScorerID`.
  - `TestScorer_SmallPreviewIsUnrated`: a 500×300 preview gives a NULL `quality_raw` stamped with `ScorerID`; `CountPending` then excludes it.
  - `TestScorer_UnreadablePreview`: a zero-byte preview in a reachable folder is stamped unrated; with `FailureGuard` returning false the row stays pending and `Score` returns an error wrapping `pipeline.ErrFolderUnavailable`.
  - `TestScorer_PendingByID`: a row stamped `"topiq-nr-koniq-384/crop5-top3/v1"` is pending.
  - `TestScorer_Cancelled`: a cancelled context leaves the row pending.
  - In `catalog/format_test.go`: a member stamped with another scorer id shows no rating; one stamped `ScorerID` with raw −46 shows 7.0.
- [ ] **Step 2: Run** `task test -- -run 'TestScorer|TestMemberQuality'`. Expected: FAIL.
- [ ] **Step 3: Implement** `scorer.go` (decode with `image/jpeg`; short side under `MinShortSide` is unrated) and switch the session, the job, the tracker and the catalog to it. In the tracker delete the states `downloading` and `download_failed` and the `Model` option; keep the Go fields `Received` and `Size` of `events.Quality` for now (they go in Task 11).
- [ ] **Step 4: Run** `task test`, `task lint`. Expected: pass.
- [ ] **Step 5: Commit** `feat(quality): rate photos with the built-in score`.

### Task 7: Complete-link clustering

**Files:**
- Modify: `internal/group/visual.go`, `internal/group/visual_test.go`, `internal/group/group.go` (the comment and the call in `clusterIDs` only)

**Interfaces:**
- Produces: `func Cluster(vectors map[int64][]float32, threshold float64) [][]int64` with the complete-link rule of §5.4 (strictly greater than the threshold; tie rule; output ordering). `Cluster` clusters exactly what it is given. The window is the caller's job: `const MaxClusterWindow = 4000` in `group.go`, and `clusterIDs` cuts a bucket's printed ids, in the bucket's time order, into consecutive runs of at most `MaxClusterWindow` and calls `Cluster` once per run.

- [ ] **Step 1: Write the failing tests.**
  - `TestCluster_CompleteLinkDoesNotChain`: three unit vectors with sim(a,b) = 0.95, sim(b,c) = 0.95, sim(a,c) = 0.80 at threshold 0.90 give `{a,b}` and `{c}` (single-link gave one group).
  - `TestCluster_StrictThreshold`: two vectors with similarity exactly equal to the threshold stay apart.
  - `TestCluster_TieBreak`: with sim(a,b) = sim(b,c) = 0.95 and sim(a,c) = 0.80, ids a<b<c, the result is `{a,b}`, `{c}`.
  - `TestCluster_MatchesNaive`: 200 random cases (seeded) of 2–40 random unit vectors in 8 dimensions and a random threshold; the result equals a naive O(n³) implementation of the rule written in the test.
  - `TestCluster_Ordering`: members ascending, clusters by smallest member.
  - `TestCluster_TwoThousandUnderFiveSeconds`: 2,000 random 768-dimensional unit vectors finish in under 5 s (skipped under `-short`).
  - `TestCluster_LargeBucketIsWindowed` (in `group_test.go`): a bucket of 4,001 identical prints yields two clusters, of 4,000 and 1.
  - `TestCluster_LengthMismatch`: vectors of different lengths never merge.
- [ ] **Step 2: Run** `task test -- -run TestCluster`. Expected: FAIL.
- [ ] **Step 3: Implement.** A similarity matrix per call plus either a nearest-neighbour chain or a heap of candidate merges; whichever is chosen must match the naive rule exactly, tie rule included.
- [ ] **Step 4: Run** `task test`, `task lint`. Expected: pass (existing grouping tests that assumed chaining are updated to the new rule).
- [ ] **Step 5: Commit** `feat(group): complete-link clustering`.

### Task 8: Vision feature print in the platform layer

**Files:**
- Create: `internal/platform/vision.go`, `internal/platform/vision_test.go` (interface compile check)
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
  // package platformtest
  type Vision struct {
  	Fail map[string]error // keyed like the Stills fake's input key
  	Gate chan struct{}
  }
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

### Task 9: Store the prints

**Files:**
- Create: `internal/store/migrations/007_vision_print.sql`
- Modify: `internal/store/ai_signals.go`, `files.go`, `migrate_test.go` (`wantVersion` 7), `ai_signals_test.go`

**Interfaces:**
- Produces (added next to the existing embedding methods, which stay until Task 10):
  ```go
  // AISignals gains: Print []float32; PrintID string
  func (r *AISignalsRepo) UpsertPrint(fileID int64, print []float32, printID string) error
  func (r *AISignalsRepo) AllPrints(printID string) (map[int64][]float32, error)
  func (r *FilesRepo) CountAnalyzedMissingPrint(printID string) (int, error)
  func (r *FilesRepo) BumpAnalyzedMissingPrint(printID string) (int64, error)
  ```
  Use the repository type names the package already has. `UpsertPrint` with a nil print stores NULL with the id. `BumpAnalyzedMissingPrint` moves the same files to the same status `BumpAnalyzedMissingEmbedding` moves them to.

- [ ] **Step 1: Write the failing tests**: the migration reaches `user_version` 7 from a version-6 database and keeps existing rows; `UpsertPrint` round-trips 768 floats exactly; a nil print reads back as absent but stamped; `AllPrints` skips NULL prints, other ids and blobs whose length is not 3,072 bytes; `Count`/`Bump…MissingPrint` count files with no row, a NULL id or another id, and skip stamped ones (including stamped-NULL).
- [ ] **Step 2: Run** `task test -- -run 'TestMigrate|TestPrint'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `task test`, `task lint`. Expected: pass.
- [ ] **Step 5: Commit** `feat(store): vision print columns`.

### Task 10: Switch grouping to the feature print

**Files:**
- Create: `internal/analyze/analyze.go`, `internal/analyze/analyze_test.go`
- Modify: `internal/app/session/session.go`, `pipeline.go`, `job.go`; `internal/app/runtime/runtime.go` (`Deps.Vision`, required); `cmd/choix/main.go` (`Vision: vision.New()`)
- Modify: `internal/group/group.go`, `skew.go` (`anchorSimilarity = 0.93`); `internal/config/config.go`, `toml.go`, `web.go`; `internal/app/settings/store.go`, `service.go`
- Modify: `internal/store/ai_signals.go`, `files.go` (delete the embedding methods and the `ClipEmbedding`/`Embedding` fields)
- Modify tests: `session/pipeline_test.go`, `job_test.go`, `manager_test.go`; `group/*_test.go`; `config/*_test.go`; `settings/store_test.go`; `pipeline/integration_test.go`; `e2e/smoke_test.go`; `session/testdata/_make_v1_state.go` stays as it is (v1 really wrote `clip_embedding`)

**Interfaces:**
- Consumes: Tasks 7, 8, 9.
- Produces: `internal/analyze` as in §5.2 (`PrintID`, `Printer`, `New`, `Analyze`); the session's printer over `platform.Vision`, gated on `visual_grouping`; the analyze stage reading `thumb.TierPreview` with 4 workers; `backfillPrints` replacing `backfillEmbeddings`, run whenever `visual_grouping` is on; `Grouper` reading `AllPrints(analyze.PrintID)` (the session passes the id; `internal/group` does not import `internal/analyze`); `config.Config.VisualSimilarity` (TOML `visual_similarity`, default 0.90); `session.ClusterParams` ending in `;m=vnfp-r2/fill/preview/v1+complete`.

- [ ] **Step 1: Write the failing tests.**
  - `analyze`: `TestAnalyze_StoresNormalisedPrint` (a fake printer returning a vector of norm 2 stores a unit vector stamped `PrintID`); `TestAnalyze_NoPrinter` (analyzed, row unstamped); `TestAnalyze_VisionFailureIsStampedOnce` (printer error on a readable preview: analyzed, NULL print stamped, and `CountAnalyzedMissingPrint` is 0 afterwards); `TestAnalyze_BadVector` (wrong length, zero norm, NaN: treated as a Vision failure); `TestAnalyze_Cancelled` (row untouched); `TestAnalyze_FolderUnreachable` (returns `pipeline.ErrFolderUnavailable`, nothing recorded); `TestAnalyze_UnreadablePreview` (file `failed` with the error text).
  - session: `TestClusterParams` (exact string for the defaults: `b=600;t=0.9000;x=false;v=true;m=vnfp-r2/fill/preview/v1+complete`); `TestEnsureClustered_OldParamsRecluster` (a store stamped `b=600;t=0.9200;x=false;v=true` re-clusters and its picks table is unchanged); `TestJob_BackfillsPendingPrints` (a folder analyzed with no prints gets them and is re-clustered; a second job re-analyzes nothing); `TestJob_VisualGroupingOff` (no print is made; clusters equal the time buckets even when prints are stored).
  - config: `TestLoad_IgnoresRetiredThresholdKey` (`visual_cluster_threshold = 0.7` loads as 0.90; saving writes `visual_similarity` and no old key); `TestDefaults` (0.90).
  - group: the existing tests on prints instead of embeddings; `TestSkew_AnchorThreshold` (pairs at 0.94 anchor, pairs at 0.92 do not).
- [ ] **Step 2: Run** `task test -- -run 'TestAnalyze|TestClusterParams|TestEnsureClustered|TestJob_|TestLoad_|TestSkew_'`. Expected: FAIL.
- [ ] **Step 3: Implement** in this order, compiling after each: config rename; `internal/analyze`; session printer, stage and backfill; grouper on prints; delete the store's embedding methods and `local.NewAnalyzer`'s use. After this task nothing calls the CLIP embedder; `internal/ai/local` is still present and is deleted in Task 11.
- [ ] **Step 4: Run** `task test`, `task lint`, `task test:smoke`. Expected: pass. The smoke test asserts every analyzed file has a print through the `platformtest` fake and that picks survive a re-cluster.
- [ ] **Step 5: Commit** `feat(grouping): group by Vision feature print`.

### Task 11: Delete the models, the engine and the downloader

**Files:**
- Delete: `internal/ai/local/` (all), `internal/app/inference/` (all), `internal/app/models/` (all), `internal/app/session/inference.go`, `inference_test.go`
- Modify: `internal/app/runtime/runtime.go`, `fakes_test.go`, `runtime_test.go`; `cmd/choix/main.go`, `events.go`, `events_test.go`, `selfcheck.go`, `selfcheck_test.go`
- Modify: `internal/app/events/events.go`, `events_test.go`, `coalescer_test.go`; `internal/app/session/job.go`, `quality.go`, `queue.go` (comment); `internal/app/testsupport/service.go`, `service_test.go`
- Modify: `internal/appdir/appdir.go`, `override_dev_test.go`; create `internal/app/runtime/legacy.go`, `legacy_test.go`
- Modify: `internal/e2e/uat_test.go`, `smoke_test.go` (comments, model plumbing); `internal/pipeline/stage.go` (comment); `go.mod`, `go.sum`
- Regenerated: `frontend/bindings/**`

**Interfaces:**
- Produces: four registered services (`Library`, `Catalog`, `Settings`, `System`) plus `Media`; `events.Quality` without `Received`/`Size`; no `events.ModelsProgress`, no `events.ModelStatus`; `runtime.Deps` without `Downloader`, `CPUOnly`, `Embedder`, `CropInferer`; `appdir.LegacyModels() string` (the old `models` path, not created); `func removeLegacyModels(dir string) (removed []string, err error)` in `runtime/legacy.go`, called once at start-up, not in the self-check; the self-check report of §10.
- Settings change: switching `visual_grouping` or `quality_rating` from off to on queues `session.JobBackfill` for every reachable folder (no readiness condition).

- [ ] **Step 1: Write the failing tests.**
  - `TestRemoveLegacyModels`: a directory holding `clip_vit_b32.onnx`, `topiq_nr_koniq_384.onnx`, `topiq_nr_koniq_384.onnx.lock`, `clip_vit_b32.onnx.part` and `notes.txt` keeps only `notes.txt` and the directory; a directory holding only model files is removed; a missing directory is not an error; a symlink named `clip_vit_b32.onnx` is removed as a link, its target untouched.
  - `TestSelfCheckReport`: the JSON has `vision.featurePrint == "ok"`, `vision.length == 768`, `vision.revision == 2`, `quality == "ok"`, and no `onnxRuntime` or `providers` key; with a failing Vision fake the report carries the message and the check fails.
  - `TestRuntime_SettingsOnQueuesBackfill`: switching `visual_grouping` on queues a backfill for every reachable folder; switching it off queues none.
  - `TestRuntime_IdleIgnoresNothingButJobs`: `Idle()` is true with an empty queue.
  - `events`: `Names()` no longer lists `models:progress`; a `Quality` value marshals without `received` and `size`.
- [ ] **Step 2: Run** `task test -- -run 'TestRemoveLegacyModels|TestSelfCheckReport|TestRuntime_'`. Expected: FAIL.
- [ ] **Step 3: Delete and rewire.** Remove the packages; fix every importer the compiler names; drop the shutdown steps and log lines for models and the inference engine; replace the self-check's ONNX parts; remove `appdir.Models()` and every caller; run `go mod tidy`.
- [ ] **Step 4: Run** `task bindings`, then `task test`, `task lint`, `task test:smoke`, and `cd frontend && bun run check`. Expected: pass; `frontend/bindings` has no `models` service directory and no `ModelStatus`.
- [ ] **Step 5: Commit** `refactor: remove ONNX Runtime, the models and the downloader`.

### Task 12: Build, CI and release scripts

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

### Task 13: Design, prototype, parity and E2E, final

No gated file is touched here: everything is under `frontend/src/design/**`, `frontend/prototype/**`, `frontend/tools/**`, `frontend/e2e/**`, `docs/design/**`.

**Files:**
- Delete: `frontend/src/design/components/ModelRow.tsx`, `ModelBanner.tsx`, `SettingsModelCard.tsx`, `frontend/src/design/screens/welcome/` (all); `frontend/tools/flows/welcome.ts`; `frontend/e2e/parity/mock-bindings/models/`
- Modify: `frontend/src/design/components/index.ts`, `QualityIndicator.tsx`; `frontend/src/design/types.ts`, `format.ts`; `screens/library/*`, `screens/folder/*`, `screens/settings/*` (props, screens, `draft.ts`)
- Modify: `frontend/prototype/mock/data.ts`, `settings.ts`; `frontend/prototype/scenarios/welcome.tsx`, `library.tsx`, `libraryWiring.ts`, `folder.tsx`, `settings.tsx`, `gallery.tsx`
- Modify: `frontend/tools/flows/settings.ts`, `frontend/tools/state-inventory.ts`
- Modify: `frontend/e2e/parity/cases.ts`, `fixtures/*.ts`, `mock-bindings/control.ts`, the `*Cases.test.*` files; `frontend/e2e/api/fixtures.ts`, `api/library.e2e.ts`, `ui/fixtures.ts`, `harness/main.ts`, `global-setup.ts`
- Modify: `docs/design/retired.json`, `docs/design/changes/<branch-slug>.md`; regenerated `docs/design/state-hashes.json`, `docs/design/inventory.md`

**Interfaces:**
- Consumes: Task 4 (the app passes none of the props deleted here) and Task 11 (the bindings).
- Produces: the design of §9.1 with its exact copy; `FolderGrouping = 'visual' | 'time-off'`; `DEFAULTS.similarity = 0.9`; states 11–17, 112, 120, 216, 228, 505, 506, 515, 516, 517 retired.

- [ ] **Step 1: Update the design tests** (`LibraryScreen.test.tsx`, `FolderScreen.test.tsx`, `settings/draft.test.ts`, `validate.test.ts`) to the new props and copy. Run `cd frontend && bun test`. Expected: FAIL.
- [ ] **Step 2: Edit the design**: delete the components and the welcome screen, remove the optional props added in Task 2, apply the copy of §9.1 character for character.
- [ ] **Step 3: Edit the prototype**: delete the scenarios of the retired states; put their numbers in `docs/design/retired.json` (`[11, 12, 13, 14, 15, 16, 17, 112, 120, 216, 228, 505, 506, 515, 516, 517]`); update mocks, the gallery states 803, 805 and 807, state 514's error text, and the flows.
- [ ] **Step 4: Update parity and E2E**: remove the cases, fixtures and mock bindings of retired states and of `models:progress`; `Models` leaves the harness and the API fixtures; delete the "no model download is attempted" test and the "models directory is empty" assertion; keep `visual_grouping = false` and `quality_rating = false` in the E2E config only where a test depends on time-only clusters, otherwise drop them.
- [ ] **Step 5: Run** `cd frontend && bun test && bun run check && bun run lint:design`, then, one at a time, `task design:check-states`, `task design:flows`, `task design:inventory`, `task design:snapshot`. Expected: 0 failures each.
- [ ] **Step 6: Fill in the change file**: `retired:` the sixteen numbers; `states:` every state `task design:status` lists as new or stale. Run `task design:status`. Expected findings: only `unapproved` (and `unretired` until the owner retires the numbers); no `unlisted-state`, no `stale-snapshot`, no `bad-change-file`.
- [ ] **Step 7: Run** `task test:frontend`, `task bindings:check`, `task test:e2e`, `task parity`. Expected: pass.
- [ ] **Step 8: Commit** `feat(design): remove the model download UI`, with `docs/design/state-hashes.json`, `inventory.md`, `retired.json` and the change file in the same commit.

### Task 14: Documentation

**Files:**
- Modify: `CLAUDE.md`, `README.md`, `v2.md`, `docs/release/uat.md`, `docs/release/signing-checklist.md`, `docs/release/sandbox-smoke.md`, `docs/superpowers/specs/2026-11-11-choix-2-desktop-design.md`
- Modify (one line under the title each): `docs/superpowers/specs/2026-11-11-choix-2-spike-results.md`, `docs/superpowers/plans/2026-11-11-choix-2-engine-and-wails.md`, `docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md`, `docs/plans/2026-11-11-quality-rating.md`

- [ ] **Step 1: Rewrite `CLAUDE.md`** so it describes the code as it now is: hard rules 4, 5, 6 and 8 as §4 words them; the architecture tree and repository layout (no `ai/local`, `inference`, `models`; add `analyze`, `platform/darwin/vision`; four services); the build table (no `deps:onnxruntime`, no bundled library; `sign:dev` note); the "Models and inference" section replaced by "Visual grouping and quality rating" summarising §5 and §6, including that changing the formula bumps `quality.ScorerID` and changing how prints are made bumps `analyze.PrintID`; the SQLite section (migrations 001–007, `user_version` 7, retired column `clip_embedding`); the configuration table (`visual_similarity`, default 0.90; no downloads); "Where data lives" (no `models/`); the caveats (drop the ONNX and hardened-runtime-reason entries, keep the facts that still hold); "Where to stop and ask" (any network access at all: ask first).
- [ ] **Step 2: Rewrite the other living documents** the same way; in `README.md` delete the TOPIQ licence section and the model sizes; in `docs/release/sandbox-smoke.md` add the runbook of §14 for removing `network.client`; in the 2.0 design spec replace the models section with a short summary and a link to the new spec, and fix the entitlement rationale.
- [ ] **Step 3: Add the superseded line** to the four historical documents (§13 gives the wording).
- [ ] **Step 4: Verify.** Run `git grep -n -i -E 'onnx|topiq|coreml|hugging ?face' -- . ':!docs/superpowers/specs/2026-11-12-builtin-grouping-and-quality-design.md' ':!docs/superpowers/plans/2026-11-12-builtin-grouping-and-quality.md' ':!docs/superpowers/specs/2026-11-11-choix-2-spike-results.md' ':!docs/superpowers/plans/2026-11-11-choix-2-engine-and-wails.md' ':!docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md' ':!docs/plans/2026-11-11-quality-rating.md' ':!docs/design/changes/feat-choix-2-backend-9051c1.md' ':!internal/retired_test.go'`. Expected: no output. Run `git grep -n -w -i clip` and check every remaining hit is unrelated to the model (`clip.mov`, the CSS clip check, `clip_hi`, `clip_embedding` as a retired column).
- [ ] **Step 5: Run** `cd frontend && bun test prototype/mock/privacy.test.ts`. Expected: pass.
- [ ] **Step 6: Commit** `docs: built-in grouping and quality`.

### Task 15: Keep it removed

**Files:**
- Modify: `internal/retired_test.go`

- [ ] **Step 1: Extend the test**: add `internal/ai/local`, `internal/app/inference`, `internal/app/models`, `internal/app/session/inference.go`, `build/onnxruntime`, `build/quality-model` to `retiredPaths`; remove `internal/ai/local` from `netHTTPAllowed`; add `TestNoONNX`, which walks `cmd/` and `internal/` and fails on a non-test Go file that imports `github.com/yalue/onnxruntime_go` or contains `onnxruntime`, and on `go.mod` naming that module.
- [ ] **Step 2: Run** `task test -- -run 'TestRetired|TestNoONNX|TestNetHTTP'`. Expected: PASS (and, with a scratch file containing the import added temporarily, FAIL; remove the scratch file).
- [ ] **Step 3: Commit** `test: keep ONNX Runtime and the models removed`.

### Task 16: Full verification and hand-over

- [ ] **Step 1: Run every suite**, in this order, and keep the output: `task fmt` (no diff), `task lint`, `task test`, `task test:smoke`, `task test:frontend`, `task bindings:check`, `task test:e2e`, `task parity`, `task design:status`, `task package`, `task smoke:sandbox -- --head-only`. Expected: all pass; `design:status` reports only `unapproved` / `unretired`.
- [ ] **Step 2: Try the real app.** `task package`, then run `bin/Choix.app` against a generated folder (`internal/e2e/genfolder`) through the server build or the UI: add the folder, let the job finish, check that every file has a print (`sqlite3 <folder>/.choix/state.db "select count(*) from ai_signals where vision_print is not null"`), that ratings are stamped `choix-sne-1024/v1`, pick and unpick one photo, and confirm the copy appears in `picks/`.
- [ ] **Step 3: Independent review.** Per the owner's standing roles for this project: a review round by a Fable sub-agent (`model: "fable"`, up to 3 iterations) and one by Codex (`codex exec -m gpt-6-astra -s read-only -C <worktree> -o <file> "<prompt>"`, up to 3 iterations), then `gpt-6.1-sol` (up to 5). Fix what they find; re-run the affected suites.
- [ ] **Step 4: Review build.** `task design:build && task design:serve` in the background (port 8811). Collect one Markdown link per new or changed state, grouped by screen, exactly as `task design:status` prints them.
- [ ] **Step 5: Push and open the pull request** (unattended rules in Global Constraints). The body states: what changed; that commits are unsigned; the suites run and their results; that CI's `design` job fails until the owner approves; the review links; the sixteen numbers to retire (`task design:approve -- --retire …` is the owner's command); the calls of spec §16; the deferred items of §14.
- [ ] **Step 6: Stop.** Report the pull request link, the review links and anything not done or not verified. Touch no gated file after this point.
