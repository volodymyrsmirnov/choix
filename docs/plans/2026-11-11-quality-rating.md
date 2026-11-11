# Advisory photo quality rating — plan

Date: 2026-11-11. Status: revised after two plan reviews (see "Review log" at the end); implemented in the same PR.

## Goal

Inside a cluster of near-identical frames the owner wants to see which frame is
technically cleanest (focus, motion blur, noise). choix gets an **advisory**
per-photo rating, 0–10 with one decimal (0 = worst), shown in the top-right
corner of photo previews. Enabled by default, model downloaded automatically,
never picks, rejects or reorders anything.

Non-goals: aesthetic/composition scoring, auto-pick, auto-reject, sorting by
rating, rating videos, any new keyboard shortcut.

## Research findings

### 1. Model artefact

TOPIQ no-reference (CFANet, ResNet-50), from `pyiqa` (`topiq_nr`).

| Candidate on Hugging Face | Verdict |
|---|---|
| `nocturna/topiq-nr-koniq-onnx` @ `3dab0f80c8a3afa0356c763b0e741564096783a8`, `model.onnx`, 176 803 037 bytes, SHA-256 `9aa87559a8b8f6084ed5ab5eb0d489e1351ec9ad7e733213cdff14a5dccf34c2` | **Pinned.** Model card names the source checkpoint (`cfanet_nr_koniq_res50-9a73138b.pth`), licence (CC BY-NC-SA 4.0, same as the weights), opset 18, fixed input `image` `[1,3,384,384]` RGB 0..1 (ImageNet normalisation is inside the graph), output `score` `[1,1]`. |
| `Skulleton12/TOPIQ` (`topiq_nr.onnx`) | Rejected: no model card, licence "unknown", provenance not stated. |
| `86Cao/IQA-ONNX-Models` | Has MUSIQ and others but no TOPIQ; labelled Apache-2.0 although the source weights are CC BY-NC-SA. Not used. |

Verification of the pinned file (scripts live under `build/quality-model/`):

- Structure: single file, no external data, no custom ops or functions, 1 856
  standard-domain nodes.
- Numerics: compared with `pyiqa.create_metric('topiq_nr')` (PyTorch 2.15,
  pyiqa 0.1.16) on 16 real-photo 384×384 crops: max absolute difference
  **5.4e-7**. It is a faithful export.
- Runtime: loads through `local.NewSession` (onnxruntime_go 1.28, CoreML EP
  flag 0, the same path CLIP uses). Apple M5: session load ≈ 4.5 s (CoreML
  compile, once per process), **≈ 46 ms per 384×384 inference**; CPU-only is
  ≈ 129 ms. The CoreML *MLProgram* format fails to compile this graph
  (`AveragePool` pad), so the existing NeuralNetwork path is kept.

Because a verified public artefact exists, nothing is uploaded anywhere and no
manual hosting step remains. `build/quality-model/export.py` is committed
anyway so the file can be reproduced (and re-hosted by the owner) if the
third-party repo disappears; the SHA-256 pin makes any silent change of the
upstream file a hard download failure, not a behaviour change.

**KonIQ vs SPAQ.** Both checkpoints were run on 14 Wikimedia Commons "Quality
images" (≥ 4800 px wide), each degraded at capture resolution (Gaussian blur
σ = 2/4/8 px, 21 px motion blur, Gaussian noise σ = 12/30) and then reduced
to a 1600 px preview. With the scoring recipe below:

| | sharp | blur σ2 | blur σ8 | motion | noise σ12 | noise σ30 |
|---|---|---|---|---|---|---|
| KonIQ | 0.687 | 0.391 | 0.164 | 0.245 | 0.608 | 0.517 |
| SPAQ | 0.770 | 0.581 | 0.308 | 0.421 | 0.734 | 0.664 |

The two checkpoints have different output scales, so raw gaps are not
comparable; normalised by the spread among the sharp originals (sd 0.111 KonIQ,
0.108 SPAQ) the gap is 2.7 sd vs 1.7 sd for mild blur and 0.7 sd vs 0.3 sd for
mild noise, in KonIQ's favour. Paired effect sizes are about equal (≈ 3 for
both). KonIQ is at least as discriminating, and it is the checkpoint with a
public ONNX export, so KonIQ is chosen. In every one of the 14 × 6 pairs the sharp original scored
higher than its degraded copy (smallest margin +0.022 raw, i.e. +0.3 on the
displayed scale).

### 2. Input size and preprocessing

The tier-1 thumbnail (256 px) cannot show focus errors. The model input is
fixed at 384×384. Options measured on the same corpus (KonIQ, mean raw score):

| Recipe | sharp | blur σ2 | Comment |
|---|---|---|---|
| pyiqa on the whole 1600 px preview (reference) | 0.694 | 0.376 | not possible with a fixed-size graph |
| whole preview squashed to 384×384 | 0.752 | 0.687 | downscaling hides the blur — rejected |
| short side → 384, centre crop | 0.753 | 0.652 | same problem |
| one native-resolution 384 px centre crop | 0.742 | 0.395 | sharp, but only looks at the centre |
| **five native-resolution 384 px crops** (centre + four quadrant centres) | 0.687 | 0.391 | matches the whole-preview reference |

**Decision:** score from the **tier-2 preview** (a JPEG already on disk for
every photo after the thumb stage — no extra decode of originals, no RAW/HEIC
handling), taking five 384×384 crops **without resizing** at fractional
centres (0.5,0.5), (0.25,0.25), (0.75,0.25), (0.25,0.75), (0.75,0.75),
clamped to the image.

Canonical geometry (added after review): previews are not uniform. `sips -Z
1600` caps the *long* side, the ffmpeg path (`scale=1600:-1`, used for RAW
embedded previews) fixes the *width* and can upscale. Before cropping, the
decoded preview is therefore reduced so that its long side is at most 1600 px
and at most the original's long side (`files.width/height`, when known), so a
portrait RAF and its portrait JPEG are sampled alike and an upscaled preview
is brought back to real resolution. Nothing is ever scaled up: if the short
side ends below 384 px (tiny images, extreme panoramas) the photo is left
unrated.

Aggregation: **mean of the three highest crop scores.** A plain mean punishes
shallow depth of field (sharp subject, blurred surroundings: 0.50 vs 0.69 for
the all-sharp frame); the top-3 mean is tolerant of it (0.58) while a soft
subject in front of a blurred background still scores low (0.32). Limitation,
documented in the README: no crop rule can know whether the sharp region is
the intended subject; a frame focused on the background rates like a sharp one.

Trade-off of using the preview instead of the original: the preview is a ~4×
reduction of a 24–40 MP frame, so blur below ~2 px at capture resolution is
not visible to the model. That is the price for a bounded, format-independent
cost. Pixel-peeping (Z in Focus) remains the way to judge critical sharpness.

Cost: 5 inferences ≈ 230 ms + JPEG decode ≈ **0.25 s per photo** on an M5
(≈ 4 photos/s, 1 000 photos ≈ 4 min), single worker. It runs after
clustering, so the Library is usable long before ratings finish.

### 3. Licence

- Weights (`chaofengc/IQA-PyTorch-Weights`): **CC BY-NC-SA 4.0**.
- `pyiqa` code: **PolyForm Noncommercial 1.0.0** (+ NTU S-Lab for some
  components). choix ships no pyiqa code; Python is used only by the optional
  build-time scripts.
- The pinned ONNX export: CC BY-NC-SA 4.0 (an adaptation, shared alike, with
  attribution, by its uploader).

choix does not redistribute the model; each installation downloads it from
its upstream location. Non-commercial personal use is permitted by all three
licences, so TOPIQ stays and MUSIQ is not needed. Consequence to document:
the rating must not be used commercially; anyone using choix in paid work
should turn it off (`quality_rating = false`). README carries the attribution
and this note.

### 4. Score semantics

Raw output is roughly 0..1 (KonIQ MOS scaled; observed 0.14 for heavy blur to
0.80 for the best crop sets; sharp "quality images" 0.43–0.80, mean 0.72 with
the top-3 rule). Stored value: the raw top-3 mean, untouched.

Displayed value, computed in exactly one function (`local.QualityDisplay`):

```
display = round1( clamp( (raw − 0.10) / (0.85 − 0.10) × 10, 0, 10 ) )
```

Anchors: 0.10 (below anything a real photo produced) → 0; 0.85 (above the best
observed) → 10, so good frames do not saturate at 10 and differences at the
top of the scale — where bursts live — survive. Typical values: sharp photo
≈ 8.3, mild blur ≈ 4.3, heavy blur ≈ 1.2, strong noise ≈ 6. One displayed
tenth is 0.0075 raw. Recalibration means editing two constants; no rescoring.

How much of the number is noise: re-framing the same sharp photo eight times
(up to 3 % shift and 3 % zoom, as hand-held burst frames differ) moved the
displayed value with sd 0.05 and a typical range of 0.15. Differences below
about 0.2 are noise; one decimal is the right resolution. Across different
subjects, flawless photos span 4.4–9.3, so the number is only comparable
between frames of the same scene; the badge tooltip and README say so.

## Design (revised after review)

### Storage — migration `005_quality.sql`

```sql
ALTER TABLE files ADD COLUMN quality_raw   REAL;  -- NULL = not rated
ALTER TABLE files ADD COLUMN quality_model TEXT;  -- scorer id that rated it or found it unratable
PRAGMA user_version = 5;
```

On `files` (not `ai_signals`): the member query already joins `files`, the
analyze upsert never touches it, and re-clustering only rewrites
`clusters`/`cluster_members`, so ratings survive both.

- `quality_model` holds `local.QualityScorerID`
  (`"topiq-nr-koniq-384/crop5-top3/v1"`). Changing the model or the recipe
  changes the id, which makes every photo pending again; ratings whose id is
  not the current one are **not shown** (no mixing of scales during a
  re-score).
- Pending set: `kind='photo' AND scan_status='analyzed' AND
  (quality_model IS NULL OR quality_model <> ?)`.
- A photo that can never be rated (preview missing, undecodable or too small)
  gets `quality_raw = NULL` plus the current id, so it is not retried forever.
  Nothing else is ever stamped: cancellation, a disabled setting, an inference
  error or a database error leave the row pending.
- Writes are conditional on the content hash the scorer read
  (`WHERE id=? AND content_hash=?`), so a concurrent rescan that replaced the
  file cannot receive a rating computed from the old bytes. `UpsertOnRescan`
  clears both columns when the hash changes.

### Model registry and download

- `local.ModelQuality` joins `KnownModels` (file `topiq_nr_koniq_384.onnx`,
  pinned URL + SHA-256). `ModelKind.String`/`ParseModelKind` learn the new
  kind. The first-run wizard still asks for CLIP only (`firstrun.Detect`
  lists kinds explicitly).
- `Installer.Download(ctx, kind, onProgress)` is the one download
  implementation; the wizard's `Install` is rebuilt on it.
  - Bytes go to `<file>.part`; a later attempt sends `Range: bytes=N-`,
    re-hashes the existing prefix and appends on `206`, restarts on `200`.
    On `416` the part is hashed: if it is the complete file it is installed,
    otherwise it is deleted and the download restarts.
  - SHA-256 is checked before the rename; a mismatch deletes the part and is
    an error. The part is kept on network errors and cancellation.
  - fsync before rename, as today.
  - Cancellation: the request is bound to `ctx`; body reads fail at once.
  - Two processes: an advisory `flock` on `<file>.lock`, acquired with
    `LOCK_NB` in a 500 ms poll under `ctx`. After acquiring, `Has` is checked
    again (the other process may have finished) and otherwise the part is
    resumed — so a waiter takes over when the owner dies or gives up. The lock
    is held through verification and rename.

No other auto-download is added.

### One background worker instead of a pipeline stage

Both reviewers showed that scoring inside the pipeline's "running" state is
wrong: `/api/setup/finalize` and the initial scan skip their work when the
pipeline reports running, and a multi-minute quality stage would starve them.
The pipeline package is therefore **not changed**. Instead:

`internal/quality.Worker` — one long-lived goroutine that does everything in
sequence, so there are no competing attempts and no generation bookkeeping:

```
loop:
  wait for a wake-up (start, setting changed, retry pressed, a scan finished)
  if disabled                -> state "disabled"
  if model file absent       -> download (3 attempts, backoff 2 s / 10 s);
                                on failure state "download_failed", back to waiting
  for each pending photo:
     stop if cancelled or disabled
     while the pipeline is running -> state "waiting", poll every 500 ms
     score it
  state "idle"
```

- **Preemptible and never blocking:** it is outside the pipeline adapter's
  `run` flag, so scans and CLIP finalization start exactly as before. It
  yields to a running pipeline before every photo, which keeps ONNX inference
  effectively single-worker (worst case one ≤ 0.3 s photo overlaps the first
  CLIP inference of a new run). The download does not wait for the scan.
- **Backfill:** photos analyzed before the model or the feature existed are
  pending by definition. Scoring them touches neither CLIP nor the clusters,
  so cluster ids stay stable under an open Focus page. The serve adapter
  calls `Worker.Wake()` when a pipeline run ends, which is how newly analyzed
  photos get picked up.
- **Setting changes** only flip an atomic flag, cancel the current operation's
  context and wake the loop; because one goroutine owns all state there is no
  stale completion to overwrite a newer attempt. Disabling during a download
  keeps the `.part` for a later resume.
- **Outcomes** of scoring one photo are explicit: rated; unratable (stamped);
  cancelled (pending, pass ends); infrastructure error (pending, pass ends
  with state `error`): the session cannot be created, three inference errors
  in a row, or a database error. ONNX runtime initialisation is cached per
  process, so `error` says a restart is needed.
- **Shutdown:** `Worker.Stop()` cancels and joins the goroutine. Order in
  `serve.go`: server returns → `Worker.Stop()` → `pipe.Stop()` → `pipe.Wait()`
  → `pipe.Close()` → scorer session close. Nothing can start scoring during
  teardown, including on idle shutdown where the root context is not
  cancelled.
- **Cancellation bound:** between photos and between the five crops.
  Creating the CoreML session (≈ 4.5 s, once) and a single inference (≈ 50 ms)
  cannot be interrupted — the same limitation the CLIP session has today.
  Documented as an exception to the ~1 s rule in `CLAUDE.md`.
- Seams for tests: `Fetcher` (`Has`, `Download`), `Scorer` (`CountPending`,
  `ListPending`, `Score`), `Busy func() bool`. No network, no ONNX in tests.

`local.QualityScorer` (`internal/ai/local/quality.go`) is the `Scorer`: lazy
ONNX session under a mutex, loads the tier-2 preview, applies the canonical
geometry, runs five crops, stores the top-3 mean.

### Visibility: a polled snapshot, not SSE

The plan first proposed `stage:"model"` / `stage:"quality"` events on the
progress stream. Review showed why not: the client treats every event other
than `cluster/done` as "scanning" (`App.tsx`), the SSE library replays its
whole event log to each new page (so a past failure would re-toast forever
and per-photo events would grow that log), and SSE cannot tell a late page
the current state. So the stream is left alone and the worker exposes a
snapshot:

`GET /api/quality` →
`{enabled, state, received, total, done, pending, error}` with `state` one of
`disabled | downloading | download_failed | waiting | scoring | idle | error`.
`POST /api/quality/retry` wakes the worker after a failure.

The SPA fetches it on load, after a settings save and when a scan finishes
(`cluster/done`), and keeps polling once a second only while the state is
`downloading`, `waiting` or `scoring`. It feeds the same AppBar indicator
component the scan uses ("Quality model 43 %", "Rate quality 120 / 2 000"),
a persistent warning chip on failure (tooltip = error; no toast, so nothing
re-fires on reload), and the status line in Settings. Start, finish and
failure of the download and of a scoring pass are also logged with `slog`.

### Settings

`Config.QualityRating` (default `true`), TOML `quality_rating`, Settings API
field `quality_rating`, a toggle in Settings with a status line, a Retry
button after a failure and a licence note (non-commercial model). No
environment variable: the owner asked for one machine-wide switch, and the
server reads only the global `config.toml`, so the worker, the API and the
Settings page share that single source. Changing it does not recluster.

### API

`memberJSON.quality` (`*float64`, omitted when absent): the displayed 0–10
value. Present only when the feature is enabled, the member is a photo,
`quality_raw` is non-NULL and `quality_model` is the current scorer id.
`ClusterMemberView` gains the two columns. Mapping lives only in
`local.QualityDisplay`.

### UI

- `QualityBadge` in `primitives.tsx`: mono 11 px, tabular numbers, dark
  translucent background; the value is text, never colour. Tooltip:
  "Technical quality 7.4 of 10 — compare within a cluster".
- Library thumbnail and Focus rail thumbnail: `absolute top-1 right-1` inside
  the thumb, `aria-hidden`; the rating is appended to the button's existing
  accessible name ("IMG_1.jpg, quality 7.4 of 10, picked"), because a nested
  label inside a labelled button is never announced. Videos (which own that
  corner in the Library) have no rating. Absolutely positioned: nothing moves
  when ratings arrive.
- Focus hero: the photo is letterboxed, so its true corner moves; the badge
  is the first item of the existing top-right control group (badge, zoom,
  Info), `role="img"` with `aria-label="Quality 7.4 of 10"`. Hidden while
  comparing (each pane has its own). The top-left path overlay's width cap is
  widened by the badge's width when the feature is enabled.
- Compare pane: `absolute top-2 right-2`, `role="img"`; the pane label's cap
  always leaves room for it when the feature is enabled, so the path does not
  re-ellipsise when a rating arrives.
- Info panel: a `Quality  7.4 / 10` row.
- No "best in cluster" marker (see review log).
- Live updates without disturbing Focus: the App refetches the Library at
  most every 10 s while scoring and once at the end, and hands Focus a
  `file_id → quality` map built from it. Focus reads ratings from that map
  (falling back to its own payload), so member objects — which the image
  loader and prefetcher depend on — are never replaced.
- No shortcut, focus order or flow changes.

### Docs

`CLAUDE.md`: architecture/pipeline section (quality worker), models section,
settings table, migrations list (004 and 005), and hard rule 6 rewritten:
a model listed in `KnownModels` that an enabled feature needs may be
downloaded automatically — pinned URL + SHA-256, visible progress, logged,
opt-out; binaries are still never fetched. Rule 4 gains the session-load
exception. `README.md`: what the rating is and is not, noise level, how to
disable, licence and attribution.

## Implementation steps (TDD)

1. Store: migration 005, `FilesRepo` quality methods, `Members` query,
   rescan reset; tests.
2. Config/TOML/web save + tests.
3. `local`: `ModelQuality`, `Installer.Download` (resume, lock) against
   `httptest` servers, `QualityDisplay`, geometry, `QualityScorer` (session
   test skips without model/runtime).
4. `internal/quality.Worker` with fakes.
5. CLI wiring: worker, wake on run end, shutdown order.
6. Server: `/api/quality`, retry, settings field, member payload; tests.
7. SPA: types, badge, four placements, Info row, indicator, settings toggle.
8. Docs. `make build && make test && make lint`, `bun run check`.
9. Browser check on a generated corpus (sharp / blurred / noisy copies).

## Risks

- Third-party hosting of the ONNX file: mitigated by the hash pin and the
  committed export script; if the repo vanishes the feature degrades to "no
  ratings" with a visible warning.
- 177 MB automatic download on first run with the default-on setting:
  visible, resumable, opt-out.
- Non-commercial model licence on a default-on feature: stated next to the
  toggle and in the README; the owner's own use is personal.
- Ratings are relative evidence, not truth: the UI says "technical quality",
  and nothing acts on the number.

## Review log

Two independent reviews of the first draft, before any implementation:
Codex (`gpt-6-astra`, read-only) and a Fable-model subagent. One revision
round; both found blocking flaws in the same area (scoring inside the
pipeline's running state), which the redesign above removes.

### Codex `gpt-6-astra`

| # | Point | Decision |
|---|---|---|
| 1 | Default-on download conflicts with hard rule 6; top-rated marker conflicts with "no scored top candidate"; keeping unratable photos `analyzed` needs an explicit exception; wants a spec first | **Partly accepted.** The download is the owner's explicit instruction of 2026-11-11 and rule 6 is rewritten accordingly (the spec file `CLAUDE.md` points to does not exist; this plan is the spec). The marker is dropped. The "rating failure is not a file failure" exception is written into `CLAUDE.md`. |
| 2 | Backfill could grab the adapter before the initial scan, which then never runs | **Accepted.** Scoring no longer uses the adapter; the worker yields to the pipeline. |
| 3 | A quality run swallows CLIP setup finalization (`setup.go` only starts when not running) | **Accepted**, same fix. |
| 4 | Manager needs shutdown ownership; idle shutdown does not cancel the root context; adapter `Stop()` clears `run` early | **Accepted:** `Worker.Stop()` joins before pipeline teardown. The adapter's early `run=false` is existing behaviour, only reached at shutdown after the worker is gone; left as is. |
| 5 | Env opt-out would not reach the server | **Accepted** by dropping the env var: one source (global TOML). |
| 6 | Atomic flag does not define off/on transitions; stale completions | **Accepted:** one goroutine owns download and scoring, transitions are a flag + cancel + wake. |
| 7 | Lock loser waits forever if the owner fails | **Accepted:** poll `LOCK_NB`, then re-check and resume. |
| 8 | "Downloaded", "usable" and "failed to initialise" conflated | **Accepted:** separate states `download_failed` and `error`; `error` says restart. |
| 9 | Scoring outcomes need explicit semantics | **Accepted:** rated / unratable / cancelled / infrastructure error, only "unratable" is stamped. |
| 10 | Session creation (4.5 s) breaks the 1 s cancellation promise | **Accepted as a documented exception** (same as the CLIP session today). A separate process for inference is disproportionate. |
| 11 | Old-recipe scores shown during a re-score | **Accepted:** rating exposed only when `quality_model` matches. |
| 12 | Stale write after a concurrent rescan | **Accepted:** write conditional on content hash. |
| 13 | SSE replay is an unbounded log, not a 64-event buffer; per-photo events grow it | **Accepted:** no SSE for this feature; polled snapshot. |
| 14 | A quality event does not prove the scan completed | **Accepted**, moot after 13. |
| 15 | Updating members in Focus restarts image loading | **Accepted:** ratings come from a separate map; member objects untouched. |
| 16 | Previews are not uniformly 1600 px wide; upscaling panoramas unbounded | **Accepted:** canonical geometry, never upscale, too-small photos unrated. |
| 17 | Marker population differs between Library and Focus; badge label lost inside buttons | **Accepted:** marker dropped; rating appended to the button's accessible name. |

### Fable reviewer

| # | Point | Decision |
|---|---|---|
| 1 | Quality tail stage breaks the first-run CLIP flow | **Accepted** (same as Codex 2/3). |
| 2 | `model` events on the progress stream fight the client; poll `GET /api/quality` instead | **Accepted.** This departs from the brief's suggestion to reuse the SSE channel; the AppBar indicator pattern is reused, the transport is not. |
| 3 | Shutdown and close order unspecified | **Accepted.** |
| 4 | Enabled flag has two sources; drop the env var | **Accepted.** |
| 5 | Drop the "highest in cluster" marker (out of scope, below the noise floor, inconsistent populations) | **Accepted.** |
| 6 | Failure stamping too eager; stamp only deterministic causes; abort after N inference errors; show only current-id ratings | **Accepted** (N = 3). |
| 7 | Preview not 1600 wide; RAW+JPEG pairs differ; upscaled previews read as blurry | **Accepted** (canonical geometry, undo upscales using `files.width/height`). |
| 8 | Badge accessible name lost inside buttons | **Accepted.** |
| 9 | Hero overlay cap, hide hero badge while comparing, always reserve compare label room | **Accepted** (reserved whenever the feature is enabled). |
| 10 | Research: compare checkpoints by normalised margin; say the number is per-scene; measure burst noise; licence note beside the toggle | **Accepted:** table text corrected, jitter noise measured (sd 0.05), tooltip + README + Settings note added. |
| nits | `416` with a complete part; `ModelKind.String`/`Parse`; session load not cancellable; 5 s library refetch cost; nil installer must be inert; delete probe test; plan status wording | **Accepted** (refetch interval raised to 10 s; a nil worker disables the feature in tests). |
