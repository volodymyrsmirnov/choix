# CLAUDE.md

Guidance for Claude (and future contributors) working in this repo. Read this first; it explains *why* the code is shaped the way it is and how to extend it without breaking the contract with the user.

---

## What choix is

`choix` is a single-binary photo-and-video culling tool for macOS. Drop the binary into a folder of mixed media, run it, and a localhost web UI groups the files by **device → time bucket → visual cluster** so the user can quickly pick favorites. Picks are copied (never moved) into a `./picks/` subfolder for hand-off to Luminar Neo.

**One-sentence north star:** walk in with a 2 000-photo trip dump, walk out an hour later with a `picks/` folder ready to edit, with the visual clusterer doing the heavy lifting on "which of these eight near-identical sunsets do I want to look at."

The full design is in `docs/superpowers/specs/2026-04-27-choix-design.md`. The implementation roadmap is split across plan documents in `docs/superpowers/plans/`. Anything material in this CLAUDE.md that contradicts those documents is a bug; the spec is authoritative.

---

## Hard rules

These are non-negotiable. Breaking any of them is a regression.

1. **Originals are sacred.** No code path opens an original file in anything other than `O_RDONLY`. The only thing that ever writes inside the scan root is the `picks/` subfolder (via `internal/picks`) and the `.choix/` working directory.
2. **Never lose a pick.** Pick state is fsync'd to SQLite synchronously before the API returns success. The DB is the source of truth; everything else is cache.
3. **Per-file failures are isolated.** A corrupt RAF or a video without keyframes marks that one file `scan_status='failed'` with the error text. The pipeline keeps going.
4. **Cancellation is honored within ~1 s.** All worker goroutines select on `ctx.Done()` between files.
5. **macOS only for v1.** Apple Silicon first; the build is a universal binary so x86_64 still runs. Linux/Windows are deferred — see `v2.md`.
6. **No auto-installs of network resources at runtime by Claude.** External tools (`exiftool`, `ffmpeg`, ML models) ship via brew or via the explicit first-run wizard in `internal/firstrun`. Don't add code that silently fetches binaries on the user's behalf.
7. **No duplicate files at the schema level.** A file is identified by its relative path (case-insensitive). Migration 003 enforces `UNIQUE(LOWER(path))` and `Insert` is an atomic UPSERT — there is no TOCTOU window in which a duplicate row can appear, even on macOS APFS where casing varies between scans.

---

## Architecture in 30 seconds

```
                 ┌──────────────────────────────────────────────┐
   user runs     │  cmd/choix  ──▶  internal/cli                │
   `choix .`     │  (single default action: scan + serve)       │
                 └────────┬───────────────────────┬─────────────┘
                          │                       │
                          ▼                       ▼
                  ┌───────────────┐       ┌───────────────────────────┐
                  │ internal/     │       │ internal/server (chi)     │
                  │  pipeline     │       │   JSON + SSE + SPA host   │
                  └───────┬───────┘       └─────────────┬─────────────┘
            ┌────────────┬┼┬─────────────┐              │
            ▼            ▼ ▼             ▼              ▼
  ┌────────────┐ ┌──────────┐ ┌──────────┐ ┌────────────────────────┐
  │ scanner    │ │ meta     │ │ thumb    │ │ internal/ui/web        │
  │ (walk fs)  │ │ (exif)   │ │ (sips/   │ │  React + Tailwind SPA  │
  └─────┬──────┘ └────┬─────┘ │  ffmpeg) │ │  built with bun        │
        │             │       └────┬─────┘ └────────────────────────┘
        │             │            │            ▲
        ▼             ▼            ▼            │ /api/* + /thumb/* + /full/*
                ┌──────────────────────────────────────┐
                │  internal/store  (SQLite, modernc)   │
                │  files → group → clusters → picks    │
                └──────────────────────────────────────┘
                                ▲
                                │
                 ┌──────────────┴───────────┐
                 │ internal/ai/local/clip.go│  ONNX CLIP embeddings
                 │ (CoreML EP via onnxrt)   │  (optional, model-dependent)
                 └──────────────────────────┘
```

**Pipeline stages run in order, advancing each file's `scan_status`:**

```
discover → metadata → thumb → analyze → cluster
```

* `discover` / `metadata` / `thumb` / `analyze` are **per-file** (worker pool, NumCPU for I/O, 1 for AI).
* `cluster` is global: `group.RebuildAll` sorts every analyzed file by (device, captured_at, id) and emits gap-based time buckets, then runs CLIP-similarity sub-clustering inside each bucket. Re-clustering wipes and rebuilds the `clusters` and `cluster_members` rows; picks are stored in a separate table and survive untouched.
* There is **no rerank stage**. Cloud LLM ranking and the local face/NIMA/sharpness/exposure/pHash signals were removed in v1 — only CLIP embeddings remain.

If a stage fails for a file, that file ends `scan_status='failed'` with `error` populated; the pipeline continues. On restart, the pipeline resumes from each file's current status — running `choix .` twice is idempotent, and the file row is keyed by case-insensitive path so a re-walk that visits the same file with different casing does not duplicate rows.

---

## Repository layout

```
choix/
├── cmd/choix/                      Single binary entry point. Just calls cli.Execute.
├── internal/
│   ├── cli/                        Single cobra command — the default
│   │                               `choix [folder]` action. No subcommands;
│   │                               the web UI is the only interface. Wires
│   │                               the pipeline + server, honors persisted
│   │                               BucketSizeSec / VisualClusterThreshold
│   │                               from config.toml at startup.
│   ├── config/                     Layered config (CLI > env > TOML > defaults).
│   ├── store/                      SQLite + migrations + per-table repos.
│   │   └── migrations/             Embedded SQL, applied at Open():
│   │                                 001_initial.sql       schema
│   │                                 002_kv.sql            per-folder KV
│   │                                 003_path_nocase.sql   UNIQUE(LOWER(path))
│   ├── deps/                       External tool resolution (exiftool, ffmpeg).
│   ├── scanner/                    Walks the scan root, computes content hashes,
│   │                               atomically upserts files rows, marks deleted
│   │                               as missing.
│   ├── meta/                       Wraps exiftool, parses JSON, derives device_key
│   │                               and captured_at.
│   ├── thumb/                      Three-tier thumbnail pipeline:
│   │   sips.go                     macOS sips (HEIC/JPEG/PNG/TIFF) — primary.
│   │   photo.go                    Tier-1/2 with sips → exiftool → ffmpeg fallback.
│   │   video.go                    ffmpeg keyframes (5 evenly spaced).
│   ├── ai/local/                   CLIP embeddings only (clip.go). ONNX Runtime
│   │                               + CoreML EP for ANE/Metal acceleration.
│   ├── group/                      Device → time-bucket (gap-based) → CLIP visual
│   │                               sub-cluster. RebuildAll is the only entry the
│   │                               pipeline + recluster handler call.
│   ├── pipeline/                   Orchestration: stages, worker pool, progress
│   │                               channel, Run/Resume.
│   ├── picks/                      Pick/Unpick/Reject/Unreject state machine,
│   │                               atomic copy to picks/, collision suffixing.
│   ├── server/                     chi router; JSON + SSE API; serves the SPA's
│   │                               index.html for /, /focus/{id}, /settings;
│   │                               graceful + idle shutdown; browser auto-open.
│   ├── ui/                         Embedded SPA bundle:
│   │   ui.go                       go:embed all:web/dist
│   │   web/                        React + TS + Tailwind sources.
│   │     ├── package.json          bun-managed deps (react, react-dom, tailwindcss).
│   │     ├── tailwind.config.js    Design tokens (oklch colour scale).
│   │     ├── tsconfig.json
│   │     ├── index.html
│   │     ├── scripts/build.ts      Hashed-filename builder (bun + tailwind CLI).
│   │     ├── src/
│   │     │   ├── main.tsx          React entry.
│   │     │   ├── App.tsx           Routing + fetch coordination.
│   │     │   ├── api.ts            Typed fetch wrappers + thumbURL/fullURL.
│   │     │   ├── styles.css        Tailwind + .btn/.thumb/.kbd/.imgph etc.
│   │     │   └── components/       Library, Focus, Settings, Empty, Toaster,
│   │     │                         primitives (icons, AppBar).
│   │     └── dist/                 Build output: index.html + main.<hash>.js/css.
│   ├── firstrun/                   First-run setup wizard (tool detection, model
│   │                               install with SSE progress, AI provider config).
│   └── e2e/                        End-to-end CLI test (-short skips by default).
├── build/                          Release tooling: lipo, sign, notarize, staple,
│                                   verify, brew formula, update-formula.
├── docs/
│   ├── superpowers/
│   │   ├── specs/                  Authoritative design (one file per spec).
│   │   └── plans/                  Implementation plans (TDD format, per phase).
│   └── release-checklist.md        Manual release-day checklist.
├── testdata/                       Hand-checked-in fixtures for tests.
├── v2.md                           Deferred features list.
└── CLAUDE.md                       This file.
```

---

## Build / test / lint

Everything goes through `make`. The project assumes `make`, `go ≥ 1.22`, `bun` (for the SPA build), and the macOS command-line tools (`sips`, `lipo`, `codesign`) are available.

| Command | What it does |
|---|---|
| `make build` | Runs the SPA build (`make webui`), then `go build` with version stamping via `git describe`. Output: `./choix`. |
| `make webui` | Just rebuilds the SPA bundle: `bun install --frozen-lockfile && bun run build` inside `internal/ui/web/`. Output: `internal/ui/web/dist/{index.html, main.<hash>.js, main.<hash>.css}`. |
| `make test` | `go test -race -count=1 ./...`. Race detector is on by default. |
| `make lint` | `golangci-lint run`. **Must be 0 issues** before commit. |
| `make fmt` | `gofmt -s -w .`. |
| `make release` | Cross-builds arm64 + x86_64 and `lipo`s them into `dist/choix`. |
| `make verify-signed` | Runs `codesign -v` and `spctl --assess` on `dist/choix`. |

When adding a Go dependency, run `go get <pkg>` and commit `go.mod` + `go.sum` together. When adding an npm dependency, edit `internal/ui/web/package.json` and run `bun install` (which updates `bun.lock`); commit both. Don't bypass either signing or hooks; they exist for a reason (the user's commits are SSH-signed via 1Password). If `op-ssh-sign` fails, surface the issue rather than `--no-gpg-sign`.

---

## Common runtime invocations

```bash
# Scan + serve + open browser (the only command).
choix /path/to/folder

# Flags on the same command:
choix /path/to/folder --port 8080 --no-open --idle-after 60m
```

The web UI is the only interface — picks, rejects, scans, settings, and CLIP-model installs all live in the SPA. There are no subcommands.

State lives at `<folder>/.choix/state.db`. Picks are copied to `<folder>/picks/` (configurable). User-level config and downloaded tools/models live under `~/.choix/`.

---

## Conventions you must follow

### Errors
- Wrap with `%w` and meaningful context: `fmt.Errorf("decode jpeg: %w", err)`.
- Per-file failures must not panic. They write `scan_status='failed'` with the error text.
- Never log secrets (Apple Developer cert, hypothetical future API keys, SSH key material).

### File paths
- Inside the DB, all paths are **relative to the scan root** with forward slashes (`filepath.ToSlash`). Reconstruct absolute paths only at I/O boundaries.
- Lookups by path are **case-insensitive** (`LOWER(path) = LOWER(?)`); the unique index in migration 003 makes that the canonical identity.
- Atomic writes: write to `<dst>.tmp`, then `os.Rename`. Never overwrite originals.

### Concurrency
- Workers in `internal/pipeline/pool.go` use `golang.org/x/sync/errgroup` with `SetLimit(workers + 1)` (the `+1` is the producer goroutine — without it, single-worker pools deadlock).
- The store enforces `MaxOpenConns = 1` and `PRAGMA busy_timeout = 10000`. Don't change either casually.
- Don't share `*sql.Tx` across goroutines. Per-stage workers each call repo methods which open their own short-lived statements.

### Logging
- Use `log/slog` for structured logs. The binary sets the default text handler.
- Production-relevant events: pipeline start/done, per-file warnings on failure, SSE client connect/disconnect, progress events published.
- Don't log per-file *success* — too noisy. Failures only.

### Tests
- TDD discipline: every new Go file ships with a `_test.go` next to it. Race detector is required (`go test -race`).
- Tests that need `exiftool` / `ffmpeg` / ONNX models must `t.Skip` cleanly when those are absent (`exec.LookPath("exiftool")` guard).
- Tests must not hit the network.
- Server tests assert on the JSON API + media routes — there are no HTML-template tests because there are no HTML templates.
- Pipeline tests with the real components are tagged `//go:build smoke` to keep `go test ./...` fast.

### UI
- Single-page React app under `internal/ui/web/`. Built with `bun build` (TSX → ES module bundle) and Tailwind v3 standalone CLI; output goes to `internal/ui/web/dist/` and is embedded into the Go binary via `//go:embed all:web/dist`.
- The Go server serves `index.html` for `/`, `/focus/{id}`, `/settings` (anything not matching `/api/`, `/static/`, `/thumb/`, `/full/`); React handles client-side routing.
- All page data comes from JSON endpoints: `GET /api/library`, `GET /api/clusters/{id}`, `GET /api/settings`. State changes go through `POST /api/picks`, `POST /api/recluster`, `POST /api/settings`. SSE progress over `GET /api/progress`.
- **Static assets are content-hashed** (`main.<hash>.js`, `main.<hash>.css`) so `Cache-Control: public, max-age=31536000, immutable` is safe; index.html is served `no-cache` so the next deploy's hashed URLs are picked up immediately.
- **Media URLs are path-keyed**: `/thumb/<rel-path>?tier=thumb|preview&v=<content-hash>` and `/full/<rel-path>?v=<content-hash>`. The path is stable across cluster rebuilds (clusters renumber, files don't), and the `v` query string busts the browser cache when the file's bytes change. Use the `thumbURL` / `fullURL` helpers in `api.ts` instead of building URLs by hand.
- The server binds to `127.0.0.1` only — no auth, no CSRF.
- All user-supplied path inputs to `/full/{path}` and `/thumb/{path}` are looked up via the store's `Files().GetByPath` and `Thumbs().Get` so there's no traversal surface; the handler joins the resolved relative path with the scan root via `safeJoinUnderRoot`.

---

## SQLite specifics (read this before changing the store)

- Driver: `modernc.org/sqlite` (pure-Go). Don't switch to `mattn/go-sqlite3` without weighing CGO costs.
- Schema lives in `internal/store/migrations/NNN_*.sql`, embedded via `go:embed`. Migrations run at `Open()`. Bump `PRAGMA user_version` in the migration file. **Update the constant in `migrate_test.go`** when adding a migration.
- All repos share the single `*sql.DB` from `Store`. Repos do not own connections; they hand them back to the pool after each call.
- `MaxOpenConns = 1` is intentional — concurrent writes from the worker pool tripped "database is locked" without it. Reads still fan out via the same single connection because the workload is metadata-heavy with very small queries.
- The DB file is at `<scanRoot>/.choix/state.db`. WAL means the directory also contains `state.db-wal` and `state.db-shm` while the server runs; both are recreated on start.
- Path columns store relative paths (e.g., `thumbs.rel_path = ".choix/thumbs/<bucket>/<id>-<tier>.jpg"`). Do **not** double-prefix when reading them back in handlers.
- **`files.path` is unique by `LOWER(path)`** (migration 003). Insert is an atomic UPSERT (`INSERT … ON CONFLICT(path) DO UPDATE … RETURNING id`); never reach for the old GetByPath-then-Insert pattern.
- The schema still has unused columns from retired features (`clusters.label`, `clusters.ai_top_file_id`, `clusters.cloud_reasoning`, all of `ai_signals` apart from `clip_embedding`). They're left in place because dropping them isn't load-bearing; just don't read or write them.

---

## Thumbnails — why the code looks the way it does

The thumbnail pipeline (`internal/thumb/photo.go`) tries three strategies in order:

1. **`sips`** (macOS native) — handles HEIC, JPEG, PNG, TIFF, GIF, BMP via Apple's ImageIO. Reliable on every iPhone HEIC the user throws at it.
2. **`exiftool -b -PreviewImage` + ffmpeg downscale** — fast for RAFs (Fujifilm RAW) where the embedded JPEG is 6+MP.
3. **Direct ffmpeg decode of the source** — last-resort fallback.

Why not just ffmpeg everywhere? Because ffmpeg's HEIC pipeline produces a "complex filtergraph" (heif demuxer → HEVC video stream) that **cannot be combined with `-vf scale=...`**. You'll see "Filtergraph 'scale=256:-1' was specified for a stream fed from a complex filtergraph. Simple and complex filtering cannot be used together for the same stream." and exit code 234. We hit this in production with iPhone HEIC files. `sips` solves it cleanly.

Output is always a JPEG. Tier-1 is **256w** (Library grid + Focus rail), tier-2 is **1600w** (Focus preview). For HEIC and RAF originals the Focus hero also requests a higher-res JPEG transcode via `/full`, cached as `<id>-full.jpg` under `.choix/thumbs/<bucket>/`.

Cache layout: `<scanRoot>/.choix/thumbs/<bucket>/<file_id>-<tier>.jpg`, where `<bucket>` is `file_id % 256` in 2-hex.

---

## CLIP embeddings (the only AI signal)

Located in `internal/ai/local/clip.go`. Uses `github.com/yalue/onnxruntime_go` with the **CoreML execution provider** for ANE/Metal acceleration. Models are NOT bundled in the binary — they're downloaded into `~/.choix/models/` by the first-run wizard.

When the CLIP model is absent, the `analyze` stage skips the file's embedding (the column stays NULL) and the grouper drops the file into the bucket-level fallback cluster — time proximity does the work alone. Without a model, near-identical bursts won't collapse into a single cluster, but the pipeline never fails the analyze step.

Visual clustering uses cosine similarity ≥ `VisualClusterThreshold` (default 0.92) inside each gap-based time bucket. The threshold is exposed in Settings and changing it triggers an immediate re-cluster.

The "AI" — whatever that meant in earlier drafts — is **advisory only**. There is no auto-pick, no rerank, no scored top candidate. The user's manual pick is always the source of truth.

---

## Configuration precedence

CLI flags > environment variables > `~/.choix/config.toml` > built-in defaults.

Loaded by `internal/config`. Settings of note:

| Key | Default | TOML field | What |
|---|---|---|---|
| `BucketSizeSec` | 600 | `bucket_size_sec` | Time-bucket gap (seconds) inside a device |
| `VisualClusterThreshold` | 0.92 | `visual_cluster_threshold` | CLIP cosine similarity for the same-scene cluster |
| `PicksDir` | `picks` | `picks_dir` | Where pick exports land (relative to scan root) |
| `AdvanceOnAction` | `false` | `advance_on_action` | Cycle to the next photo after Pick/Reject in Focus |
| `HideRejectedPhotos` | `false` | `hide_rejected_photos` | Hide rejected photos from the Library view |
| `CrossDeviceMerging` | `false` | `cross_device_merging` | Merge clusters across devices using clock-skew detection |

**All settings are machine-wide** — they live only in the global `config.toml`. The per-folder SQLite `kv` table (`migrations/002_kv.sql`) used to hold `picks_dir`, `advance_on_action`, and `hide_rejected_photos`; on first launch after the upgrade, `internal/server/settings_migration.go` carries any pre-existing KV values into TOML and deletes the legacy rows. First-folder-wins: once one scan-root has migrated a value, subsequent scan-roots leave the TOML alone (their KV rows still get cleaned up).

The Settings page reads + writes via `GET/POST /api/settings`; saving `bucket_size`, `similarity`, or `cross_device_merging` triggers an immediate `recluster`. The serve command re-loads the config at startup — restarting after changing the threshold no longer reverts to defaults.

**Cross-device merging** (toggled via Settings, off by default): when enabled, `internal/group/skew.go` estimates a per-device clock offset from high-confidence (≥0.95 cosine) CLIP anchor pairs taken within ±1 hour of each other. The grouper then runs the existing gap-bucketing + sub-clustering on the merged canonical timeline. Clusters whose members span multiple devices are tagged with the `group.MergedDeviceKey` sentinel (`"*merged*"`) and the Library renders them with a sorted, deduped camera list (`"X-T5 + iPhone 15"`). Files without embeddings never merge across devices — without a visual signal we have no evidence the cameras shot the same scene. If no analyzed file has an embedding at all, cross-device mode short-circuits to the per-device path.

---

## Web UI invariants

- Server binds **127.0.0.1** only.
- Idle shutdown after `--idle-after` (default 10 min). The SSE keepalive does **not** reset the timer (otherwise a long-lived browser tab defeats shutdown).
- Library auto-refreshes on:
  - cluster=done events from the SSE progress stream,
  - any pick/reject/recluster from the Focus page (via the `onChanged` callback),
  - settings save (via the `onSaved` callback).
- Library scroll position is preserved in App-level state (a `useRef`) so navigating into Focus and back doesn't lose the user's place.
- Focus mode persists the Info panel toggle to `localStorage["choix.focus.showInfo"]`; the rail auto-scrolls the active thumb into view on every navigation.
- Pixel-peep zoom is discrete (1× → 4×) and click-and-drag pans when zoomed. Compare mode shares the same `pan`/`zoom` state across every visible image so all panes track in lockstep.
- The progress SSE hub uses `r3labs/sse/v2`. We enable `AutoReplay = true` with `BufferSize = 64` so a browser that connects after a fast scan finishes still sees the cluster=done event.

---

## Plans and specs

These are the source of truth for *intent*. If you're adding a feature, find it (or its absence) in the spec first.

- `docs/superpowers/specs/2026-04-27-choix-design.md` — the v1 design, including non-goals.
- `docs/superpowers/plans/2026-04-27-choix-engine.md` — Plan 1: engine + CLI.
- `docs/superpowers/plans/2026-04-27-choix-web-ui.md` — Plan 2: web UI.
- `docs/superpowers/plans/2026-04-27-choix-distribution.md` — first-run + distribution.
- `v2.md` — explicitly deferred ideas (cloud rerank, advanced AI signals, Linux/Windows ports). Don't sneak these into v1 without renegotiating the scope.

---

## Known caveats / pitfalls

- **`make build` always rebuilds the SPA.** That's intentional; bun + tailwind CLI are fast and deterministic. If you really need a no-bundle build for an experiment, run `go build ./cmd/choix` directly — but be aware `internal/ui/web/dist/` must contain a non-empty `index.html` for `//go:embed all:web/dist` to compile. The `dist/` directory is gitignored, so a fresh clone has no SPA bundle until you run `make webui` (or `make build`) at least once; bare `go build ./cmd/choix` on a fresh clone fails with an empty-embed error.
- **`exiftool` and `ffmpeg` must be on `$PATH` or `~/.choix/bin/`.** First-run wizard handles the latter. `brew install exiftool ffmpeg` is the simplest setup.
- **HEIC-only folders show 1-file clusters by default.** That's because the pipeline can't compute CLIP embeddings without the model installed; the visual-cluster step has nothing to merge. Install the CLIP ONNX model via the first-run wizard, and same-scene shots will collapse into one cluster.
- **`SetMaxOpenConns(1)` is load-bearing.** Removing it brings back "database is locked" under heavy worker contention. If you need parallel reads, profile first.
- **`r.Path` in `internal/deps/runner.go` is trusted input** (resolved via PATH or pinned download) — not user-supplied. The `//nolint:gosec G204` annotation reflects that.
- **Pipeline progress events have lowercase JSON tags** (`stage`, `done`, `total`, `failed`, `phase`). The browser reads them; renaming would silently break the SSE indicator.
- **Picks are copies, not symlinks.** Symlinks save space but break when the user moves the folder, and Luminar Neo sometimes chokes on them.
- **No CLI subcommands.** The web UI is the only interface — the binary's only job is to scan + serve + open a browser. The pipeline's progress is published over SSE (`/api/progress`), not stderr.
- **Media URL identity is the relative path, not the file ID.** A `/thumb/123` URL would have referred to the same file across runs anyway (file IDs are stable), but path-keyed URLs survived the move from numeric IDs and are easier to debug. Always build them via `thumbURL(member)` / `fullURL(member)` in TSX so the `?v=<content_hash>` cache buster is included.

---

## Adding a feature — checklist

1. **Spec it.** If the spec doesn't cover it, write a one-paragraph addition there first (or, for big stuff, a new spec doc).
2. **Plan it.** A new TDD-format plan task in the appropriate `docs/superpowers/plans/*.md` (or a new sibling plan if it's a whole new subsystem).
3. **Test first.** Failing test → run-fail → minimal impl → run-pass → commit.
4. **Run the full suite.** `make test && make lint`. Both must be clean.
5. **Smoke against a real corpus.** Run `./choix /path/to/photos` and verify: no stage warnings, all files reach `analyzed`, the Library renders, `/thumb/<path>` and `/full/<path>` return 200, pick + unpick round-trip cleanly, Settings save applies live.
6. **Commit small.** One topic per commit. Use the existing prefixes: `feat(scope):`, `fix(scope):`, `test(scope):`, `chore:`, `docs:`, `plan:`.

---

## Where to stop and ask

- The user is in control. If a request would change the spec's non-goals (e.g., bundling an SDK, adding cloud telemetry, ranking auto-picks), surface the conflict before writing code.
- For destructive operations on the user's data — modifying originals, deleting `.choix/state.db`, renaming files in the scan root — confirm first. Auto mode is not a license to destroy.
- For credentials (Apple Developer cert, etc.) — never write them into the repo, never log them.
