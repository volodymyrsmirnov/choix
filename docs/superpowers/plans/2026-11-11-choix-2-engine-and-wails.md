# Choix 2.0 Engine and Wails Shell — Implementation Plan

Superseded in part by `2026-11-12-builtin-grouping-and-quality-design.md`: the models, ONNX Runtime and the download are gone.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** revision 4 (final), after three review rounds by Codex gpt-6-astra and Fable and the controller's rulings R1–R24 (all folded in).

**Goal:** Turn the v1 Go culling engine into a sandboxed Wails v3 macOS (arm64) desktop app: native media instead of exiftool/ffmpeg/sips, a Library of bookmarked folders with a one-at-a-time job queue, services with bindings and typed events instead of HTTP and SSE, and the build, packaging and test tooling around it.

**Architecture:** Three layers. `internal/platform/darwin/*` is cgo + Objective-C behind the cgo-free interfaces in `internal/platform`. The engine (`store`, `scanner`, `pipeline`, `group`, `picks`, `quality`, `ai/local`, `config`, `thumb`, `meta`, `appdir`, plus new `safefs` and `rawpreview`) keeps its shape and loses every external tool. `internal/app/*` holds one package per service: a **core** type with the Go API other packages use, and a stateless **`Service` facade** whose exported methods are exactly the frontend bindings (Wails binds every exported method of a registered service, so nothing else may be exported on it). `internal/app/runtime` wires the cores and owns start-up and teardown order; `cmd/choix` adapts it to Wails. Native UI (window, menu, dialogs) and all app UI code sit behind the owner's design gate.

**Tech Stack:** Go 1.26, cgo + Objective-C (ImageIO, AVFoundation, Foundation, AppKit), Wails v3.1.0 (`github.com/wailsapp/wails/v3`), `modernc.org/sqlite`, `github.com/yalue/onnxruntime_go` v1.28.0 with ONNX Runtime 1.25.0 (CoreML provider), React 18 + TypeScript + Tailwind + Vite + bun (frontend, owned by the UI plan), Playwright, Task.

**Spec:** `docs/superpowers/specs/2026-11-11-choix-2-desktop-design.md` (§5–§14 are this plan's; §15 is the gate it obeys). Companion plan: `docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md` ("the UI plan"). `CLAUDE.md` describes v1; its conventions (errors, paths, concurrency, logging, tests, SQLite) hold unless a task here changes them.

## Global Constraints

- macOS on Apple Silicon only: `GOOS=darwin GOARCH=arm64 CGO_ENABLED=1`. No universal binary. Deployment target `MACOSX_DEPLOYMENT_TARGET=14.0` (raised from 13.0 by spike S6: the official ONNX Runtime 1.25.0 arm64 library has `minos` 14.0).
- Wails `v3.1.0`, Go ≥ 1.26. The `private_mac_apis` build tag is never used. No temporary-exception entitlements, no helper executables, and **no `os/exec` in non-test code** after Task 21 (v1's `internal/server/browser.go` excepted until Task 62 deletes it). Wails' `app.Env.OpenFileManager` and `app.Browser.OpenURL` are never called: both run `/usr/bin/open` (`v3/internal/fileexplorer`, `v3/internal/browser`).
- Entitlements, both builds, exactly: `com.apple.security.app-sandbox`, `com.apple.security.files.user-selected.read-write`, `com.apple.security.files.bookmarks.app-scope`, `com.apple.security.network.client`.
- Bundle identifier `com.github.volodymyrsmirnov.choix`, product name `Choix`.
- **Originals are sacred.** Originals are opened `O_RDONLY` only. Inside a folder the app writes only under `<folder>/.choix/` and the folder's picks directory, and every such write goes through `internal/safefs`: the directory is opened component by component, each must be a real directory (never a symlink) inside the root, files are created with `O_CREAT|O_EXCL` under a random temporary name and renamed within the same directory. Nothing else creates, truncates or renames a file inside a folder.
- **What confinement defends against (threat model, R16).** Accidental or stale symlinks and Choix's own bugs — not a concurrent local attacker with write access to the folder, who could change the originals directly. So: checks are made when a directory is opened and when a file is published, not continuously; SQLite opens `state.db` by path after `.choix` and the database files have been verified at session open; exported picks are published **exclusively** (an existing file is never overwritten); cache files may replace an older version of themselves.
- **Databases are opened by path, never by a hand-built DSN.** `store.Open` and `library.OpenDB` take a filesystem path and build the SQLite URI with `store.FileURI`, which percent-encodes `%`, `#` and `?`. A `file:` string concatenated with a folder path is forbidden everywhere else: with SQLite's URI parsing a folder named `Day #2` would put the database beside the folder, outside the scan root.
- **Never lose a pick.** A pick, reject or undo is committed in a transaction run with `PRAGMA synchronous=FULL` and `PRAGMA fullfsync=1`; the copied file and its directory are fsynced before that commit (a volume that answers "not supported" to a sync is tolerated and logged once; a real I/O error fails the pick); mutations of one folder are serialised.
- Per-file failures set `scan_status='failed'` with the error text and never stop a job.
- **Cancellation within ~1 s** (hard rule 4, amended). Every worker returns within ~1 s of `ctx.Done()`. Exceptions: one ONNX session creation or one inference is awaited; a native ImageIO call cannot be interrupted, so it runs **detached** — the Go caller returns on `ctx.Done()`, the call's result is discarded, and because native calls return bytes and never write files, an abandoned call leaves nothing behind. Detached calls take one of a fixed, process-wide number of slots and keep it until the native call has really returned, so abandoned calls cannot pile up. A session's store and bookmark are released only after that folder's detached calls have returned and nothing references the session. Quit has one global deadline (2 s for abandoned native calls, 5 s overall); what is still in use at the deadline is **not closed** — the process exits with it open (SQLite's WAL is crash-safe, originals are read-only). Video frames use AVFoundation's asynchronous, cancellable generator, whose callbacks own their resources until the last one has run.
- Network: only the two pinned model downloads (`local.KnownModels`), SHA-256 verified before the file is put in place, visible, non-fatal, and not attempted while the model's setting (`visual_grouping`, `quality_rating`) is off; switching the setting off also stops work already running. The ONNX Runtime library is fetched at build time only. Tests never use the network.
- The quality model is never bundled or redistributed.
- Thumbnail contract unchanged from v1, including how the tier number is applied (R21): tiers `thumb` 256, `preview` 1600, `full` 4096. A thumbnail made from an **embedded JPEG** (RAF, DNG — v1's ffmpeg `scale=W:-1` path) is at most that many pixels **wide**; one made from **any other still** (v1's `sips -Z` path), or from a RAW decoded as a last resort, has its **long side** at most that many pixels. JPEG; `<folder>/.choix/thumbs/<file_id % 256 as %02x>/<file_id>-<tier>.jpg`. RAW stills (RAF, DNG) use the embedded JPEG; a RAW decode is the last resort.
- Only `cmd/choix` imports Wails. `internal/app/menu`, `internal/app/dialogs`, `internal/app/window` (gated) may too. Every other `internal/app/*` package, the engine and the platform layer do not.
- Design gate: `frontend/**` (except `frontend/src/design/**`, `frontend/prototype/**`, `frontend/tools/**`, `frontend/e2e/**`, `frontend/bindings/**`, build outputs, the listed config files and test files) and `internal/app/{menu,dialogs,window}/**` are edited only after `task design:status` shows every state approved. No agent runs `design:init`, `design:approve` or `design:confirm-no-ui`, or writes `docs/design/approvals.json` / `approvers.txt`. Files owned by the UI plan (`frontend/src/design/**`, `frontend/prototype/**`, `frontend/tools/**`, `tools/design/**`, `.claude/hooks/**`, `docs/design/**`, and the digest-input files `frontend/package.json`, `bun.lock`, `tsconfig.json`, `tailwind.config.ts`) are never edited by this plan; the one exception is the branch's change file `docs/design/changes/<branch-slug>.md`, which the coordinating session keeps current.
- v1 keeps compiling until Task 62: `internal/cli` and `internal/server` are updated only at the call sites a task lists and are never extended. `internal/ui/web/` is not touched (the UI team ports from it).
- `go test -race -count=1 ./...` and `golangci-lint run` (0 issues) are clean before every commit (`make test && make lint` until Task 47, `task test && task lint` after). Every new Go file ships with a `_test.go`.
- Test skips: cgo tests are `//go:build darwin`; tests needing a real corpus skip unless the owner's photo folder (`$CHOIX_UAT_ROOT`) is set and exists; exiftool comparison tests skip when `exec.LookPath("exiftool")` fails; ONNX tests skip when the runtime library or the model file is absent. Corpus files are never written to: tests and spikes work on `cp -cR` clones in a temporary directory.
- **Nothing from the corpus is public (R22).** The repository is public. No folder name, file name, serial number, date or location from the corpus appears in this plan, in a committed fixture, in a test expectation, in a log pasted into a document, or in a commit message. Tests and spikes choose corpus folders **by rule** ("the smallest folder with at least 50 RAF files", "the largest folder"), never by name, and print folders as `folder-1`, `folder-2`. Any dictionary captured from a real file is reduced to an allow-list of keys and has serials, owner and lens names, maker-note blobs, dates and GPS replaced by fixed synthetic values before it is committed.
- Errors are wrapped with `%w` and context. `log/slog` only; failures are logged, per-file successes are not. Paths stored in any database are relative to the folder root with forward slashes, except `library.db`'s `folders.path`.
- Code is written by Opus sub-agents, never by Fable. Commits: one topic each, prefixes `feat(scope):`, `fix(scope):`, `test(scope):`, `chore:`, `docs:`, `plan:`. Never bypass hooks or change git config.

## Review Focus

1. **A drive unplugged while its folder is scanning or open.** Expected: the job stops without a crash and stays pending, the row turns `offline` (not removed), the queue moves on, a pick attempt returns the `offline` error, and the job resumes when the drive returns — also when it returns before the next presence tick. Pinned in Tasks 29, 31, 32, 36 and 40.
2. **A bookmark that no longer grants access** (app re-signed with another identity, rebuilt ad-hoc build, kernel limit on scoped resources). Expected: the row shows `error` with "Choix lost access to this folder. Add it again."; adding the same folder again replaces the bookmark instead of answering "already in Library". Pinned in Tasks 28 and 29.
3. **The same folder under another spelling** (`/Volumes/X/Trip/`, `/volumes/x/trip`, a symlink to it, a folder inside it, its own `picks/`). Expected: one row, never two. Pinned in Task 28.
4. **A folder last scanned by v1** (`state.db` at `user_version` 5, exiftool-produced `raw_exif`, thumbnails on disk, picks exported under a non-default directory). Expected: opens with its clusters and thumbnails before any job finishes, nothing is re-processed, no camera splits into two devices, its picks are not walked as originals and unpick still works. Pinned in Tasks 17, 19 and 30.
5. **Broken or hostile content in a folder** (zero-byte file, RAF whose header points past the end, DNG without a preview, a `.jpg` that is not a JPEG, a video with no video track, `picks` or a cache directory that is a symlink, a planted `*.tmp` symlink, a folder whose name contains `#`, `?` or `%`). Expected: the file is `failed` with a readable reason, a symlinked output directory is refused, no original is ever truncated, nothing is ever created outside the folder, and the job completes. Pinned in Tasks 16, 17, 18, 20, 27, 30 and 32.

## Gates and dependencies

Every task has two lines. **Gate:** `none` (no approval needed), `spike Sx` (do not start until that spike's result is recorded in `docs/superpowers/specs/2026-11-11-choix-2-spike-results.md`; a failed spike edits the spec section named in the spike first), or `design` (every state approved; checked with the preflight in Phase 6). **Depends on:** the tasks whose output it consumes — a task may be dispatched only when all of them are merged.

## What this plan assumes from the UI plan

Settled by the controller (R12, R13); this plan does not make these edits and stops with a report if one is missing when a task needs it.

1. **Shared frontend config (digest inputs), applied by the UI team before its first committed snapshot:** `frontend/package.json` dependency `"@wailsio/runtime": "3.1.0"` (Wails pins the npm runtime to the CLI version) and scripts `"dev": "vite"`, `"build": "tsc --noEmit && vite build"`, `"build:parity": "vite build --mode parity"`, `"harness:build": "vite build --config e2e/harness/vite.config.ts"`, `"test:e2e": "playwright test --config e2e/playwright.config.ts"`; `bun.lock` regenerated; `frontend/tsconfig.json` `compilerOptions.paths` `{ "@bindings/*": ["./bindings/github.com/volodymyrsmirnov/choix/internal/app/*"] }`, `include` gains `"bindings"`, `exclude` gains `"e2e/harness/dist"`; `frontend/tailwind.config.ts` `content.files` gains `'./index.html'`.
2. **Parity contract:** the UI plan's parity spec reads the app's URL from `PARITY_APP_URL`; in its merge check "every group" means every group except `gallery`, and `native` is satisfied by skip entries.
3. **Design components and manifests:** a shared `AppRoot` component renders `[data-app-root]` and the app mounts its screens inside it; `LibraryProps.onMenu(id, at?: { x: number; y: number })` carries coordinates for both the ⋯ button and right-click; alert copy lives in `frontend/src/design/dialogs.json` (`remove-folder` → `title` with `{name}`, `message`, `buttons` with `label` and `role`); role items in `frontend/src/design/menus.json` carry a `label`; `validateSettings` in the design folder is the single source of picks-folder copy and is relative-only.
4. **Menu vocabulary** (the manifest already uses it): roles `about`, `hide`, `hideOthers`, `showAll`, `quit`, `cut`, `copy`, `paste`, `selectAll`, `closeWindow`, `minimize`, `zoom`, `toggleFullscreen`, `front`; `display` values `Space`, a single letter, `→`, `←`, `↩`, `⇧↩`, `?`; every `accelerator` parses in Wails v3.1.0 (`keys.go`), which Task 50's test keeps true.

Open towards the UI team (not blocking Phases 0–5): R7 makes welcome state s10 ("both downloading") unreachable; the Settings hint must not offer an absolute picks path.

## Calls and rulings (each can be vetoed by the owner)

Marked **[call]** at the task that implements them. Rows marked † differ from the spec's wording and need the owner's nod: C6, C7, C8, C16, C27, C28, C32.

| # | Decision | Source | Task |
|---|---|---|---|
| C1 | Service packages do not import Wails; each has a core type plus a binding-only `Service` facade | plan | Phase 4 |
| C2 | Interfaces live in cgo-free `internal/platform`; fakes in `internal/platform/platformtest` | plan | 9 |
| C3 | Native ImageIO calls run detached, in a bounded number of process-wide slots, and are abandoned on cancel; native calls return bytes, never write files; release waits for them; quit has one deadline and exits without closing what is still in use. Amends hard rule 4's exception list | R1, R17, R18 | 9, 13, 14, 30, 40 |
| C4 | Picks are committed with `synchronous=FULL` + `fullfsync` on the pinned single connection (a second pooled connection would break the load-bearing `SetMaxOpenConns(1)`); export file and directory are fsynced first, tolerating only "not supported" answers from the volume; per-folder serialisation | R2, R20 | 16, 17, 36 |
| C5 | Symlink-resistant writes (`safefs`) for the picks directory and `.choix/`; exports are published exclusively; a picks directory of `.`, `.choix`, or one that holds files Choix has scanned as originals is refused (unknown files never block). The threat model excludes a concurrent local attacker; no SQLite VFS is built | R3, R16 | 16, 17, 30 |
| C6 † | Finder and URL opening go through a native `NSWorkspace` adapter, not Wails' helpers | R4 | 12, 39 |
| C7 † | Presence: a row is dropped only when the folder is verified to be in the Trash, or its absence is verified independently of the bookmark (the stored path reports "no such file") on its identified, mounted volume. A bookmark that merely fails to resolve ⇒ `offline`, `missing` ("can no longer be found", shown as an error row) or `access_lost` — never a silent removal | R5, R19 | 11, 29 |
| C8 † | Tier sizing is v1's, per source kind: embedded JPEG by width, every other still by long side | R21 | 13, 20 |
| C9 | Video keyframes are v1's (five full-size frames at the same fractions), stored as `<id>-keyframes/1.jpg … 5.jpg`. New: the video `thumb` row is the first frame scaled to 256 wide (v1 stored the full-size frame under that name) and a `preview` row (1600 wide) is added for the poster | plan | 20 |
| C10 | `QuickTime:CreateDate` / `ModifyDate` come from the `mvhd` atom read in Go, not from `AVAsset.creationDate` | plan | 19 |
| C11 | `CHOIX_DATA_DIR` overrides the data root in non-shipping builds only. Environment and per-folder `config.toml` layering are removed; v1's global config is not imported; the legacy KV→TOML migration is dropped | plan, R9 | 15 |
| C12 | A folder whose `state.db` has picks exported under another directory keeps that directory (derived from `picks.exported_path` and `files.path`); the `picks_dir` setting applies to folders with no exported picks | R9 | 17, 30 |
| C13 | `picks_dir` must be a relative path inside the folder; the Go side returns a reason code and the frontend shows the design's copy | R13 | 17, 35 |
| C14 | Media is mounted under one route: `/media/thumb/…`, `/media/full/…`, `/media/preview/…` | plan | 37 |
| C15 | One `menu:action` event with `{ id, data }` instead of one event per menu item | plan | 24, 50 |
| C16 † | Model downloads are sequential — CLIP, then the quality model (state `queued`); three attempts (pauses 2 s, 10 s), then `failed` until Retry | R7 | 25 |
| C17 | Job kinds `full` (walk first) and `backfill` (no walk). A model finishing its download in this session, or its setting being switched on, queues `backfill` for every reachable folder; a model already present at launch does not | plan, R14 | 31, 40 |
| C18 | Opening a folder puts its job at the front **and preempts** the running job, which is re-queued directly behind it | R8 | 31 |
| C19 | Queued work is persisted at enqueue with a sequence number; a finished run acknowledges only its own sequence; an unreachable folder suspends its job (still pending) and every presence pass re-queues a present folder that is pending but not queued; only removal cancels | R14, R23 | 27, 31, 32 |
| C20 | Each `state.db` records the parameters of its last clustering. A v1 folder has none: it is left as it is (nothing is stamped) until a job or a recluster actually rebuilds its clusters. Clustering always reads the live settings, under one per-session lock, and stamps what it used | R10, R23 | 30 |
| C21 | Library counts include every non-missing file; previews are six 512-px-wide JPEGs over non-rejected files; both are refreshed after a job and after picks or rejects | R10 | 34, 36 |
| C22 | Undo is a per-folder in-memory stack (100 entries), cleared when the folder closes | plan | 36 |
| C23 | `embed.go` at the repository root (package `choix`) embeds `frontend/dist`, `menus.json` and `dialogs.json`; `task frontend:bootstrap` writes a stub `frontend/dist/index.html` before any Go package is loaded | plan, R14 | 41, 47 |
| C24 | Before design approval the desktop binary opens no window. `CHOIX_SELFCHECK=1` runs a read-only self-check and exits; it stays in the shipped build because the signed bundle is verified with it | plan | 42 |
| C25 | Binding-level E2E runs before approval through a blank harness page under `frontend/e2e/harness/` (an exempt path, no screens) | plan | 65 |
| C26 | Generated bindings are committed; CI regenerates and diff-checks them | R11 | 45, 48 |
| C27 † | The bundle ships `choix.icns`; an Icon Composer `Assets.car` is added only when the owner provides the `.icon` file | plan | 46 |
| C28 † | Closing the window quits the app | plan | 41 |
| C29 | Window size and position are saved in `library.db`'s `kv` table | plan | 49 |
| C30 | Hash routes in the app (`#/`, `#/folder/<id>`, `#/folder/<id>/focus/<cluster>`, `#/settings`); one folder session spans Folder, Focus and Compare | plan | 52 |
| C31 | Shortcut ownership: a combination the manifest lists as `accelerator` belongs to the native menu and the webview ignores it; single keys belong to the webview unless spike S2 proves the menu can own them without touching typing | R14 | 5, 50, 52 |
| C32 † | App store tests run under `bun test` (the UI plan's runner), not Vitest as spec §11 says | plan | 52 |
| C33 | `CHOIX_ONNXRUNTIME_DYLIB` and the Homebrew library paths exist only in builds without the `production` tag | review | 22 |
| C34 | For RAF and DNG the stored width and height are the selected embedded preview's (spec §7), even where exiftool reported another number for a DNG | R23 | 18, 19 |
| C35 | `Models.Status`, `Models.Quality` and `System.Info` return `(value, error)` so they can answer `unavailable` while the app quits | R17 | 38, 39 |
| C36 | Missing pick copies are counted and reported (`picks_missing`), never copied back; opening a folder copies nothing | R24 | 17, 30, 36 |
| C37 | On a volume that cannot rename exclusively (`ENOTSUP`), an export is published by "check absent, then rename" — inside the threat model, logged once per volume | review | 16 |
| C38 | `store.Open` takes a path and escapes it; the `Workspace` calls have no timeout (they return only after AppKit ran) | review | 12, 17 |

---

## File Structure

```
embed.go                                  package choix: Frontend (frontend/dist), MenuManifest, DialogManifest
cmd/choix/
  main.go  events.go  emitter.go          Wails app, service registration, lifecycle
  selfcheck.go
  shell_desktop.go   (!server)            window, menu, dialogs wiring (calls the gated packages)
  shell_server.go    (server)             test-support service, fake dialogs
internal/platform/
  bookmark.go  media.go  volumes.go  workspace.go  detach.go    interfaces, value types, detached calls (no cgo)
  platformtest/                           fakes + fixture builders (MakeRAF, MakeDNG, MakeJPEG)
  darwin/bookmark/  darwin/volumes/  darwin/workspace/          cgo + Objective-C
  darwin/imageio/   darwin/avmedia/
internal/appdir/                          Application Support paths (cgo: NSSearchPathForDirectoriesInDomains)
internal/safefs/                          symlink-resistant directory handles and atomic writes
internal/rawpreview/                      RAF and DNG embedded-JPEG readers (pure Go)
internal/meta/
  native.go  imageio_map.go  av_map.go  mvhd.go    native reader → exiftool-keyed record
  parse.go  device.go  captured.go  backfill.go    kept unchanged
internal/thumb/                           builder.go, photo.go, video.go, cache.go on platform.Stills / platform.Video
internal/picks/                           durable, serialisable pick state machine; picks-directory rules
internal/ai/local/                        runtime.go (library lookup), sessions.go (shared ONNX sessions)
internal/quality/pass.go                  one scoring pass (worker.go deleted in Task 62)
internal/app/
  apperr/  opgate/                        error type + MarshalError; admission gate for shutdown
  events/                                 names, payloads, Emitter, Coalescer
  settings/  models/  inference/          cores + Service facades
  library/                                library.db, folders, presence, stats, previews
  session/                                per-folder session, job queue, job, quality status
  catalog/  media/  system/
  runtime/                                composition root; start and teardown order
  testsupport/        (server)            path injection for E2E
  menu/  dialogs/  window/                GATED
  nativeui_test.go                        native constructors only in the three gated packages
build/
  onnxruntime/{pin.env,fetch.sh}
  darwin/{Info.plist.tmpl,entitlements.plist,entitlements.mas.plist.tmpl,bundle.sh}
  frontend-stub/index.html  config.yml  smoke/sandbox-smoke.sh  homebrew/choix.rb.tmpl
frontend/
  vite.config.ts                          app entry config (exempt from the gate, not a digest input)
  bindings/**                             generated, committed
  index.html  src/main.tsx  src/app/**    GATED (Phase 6)
  e2e/harness/**  e2e/api/*.e2e.ts  e2e/ui/*.e2e.ts  e2e/playwright.config.ts
  e2e/parity/mock-bindings/**  e2e/parity/fixtures/**  e2e/parity/serve-app.mjs
Taskfile.yml                              extended (the UI plan creates it)
.github/workflows/{ci.yml,release.yml}
docs/release/{sandbox-smoke.md,signing-checklist.md,uat.md}
docs/superpowers/specs/2026-11-11-choix-2-spike-results.md
```

Deleted by this plan: `internal/deps`, `internal/firstrun` (Task 21); `internal/cli`, `internal/server`, `internal/ui`, `internal/quality/worker.go`, `Makefile`, `install.sh` (Task 62).

---

## Phase 0 — Spikes

Throwaway code in `${CHOIX_SPIKE_DIR:-$HOME/choix-spikes}/<name>/`, never in the repository. **Spikes never touch a real folder:** every folder a spike adds, scans or writes into is a `cp -cR` clone under `$CHOIX_SPIKE_DIR/data/`, and the only thing written inside a clone is `<clone>/.choix/spike-*`. Each spike ends by appending a section to `docs/superpowers/specs/2026-11-11-choix-2-spike-results.md` (question, what was run, raw output, PASS/FAIL, consequence) and committing only that file (`docs: record spike Sx result`). On FAIL the named spec section is edited in the same commit and the dependent tasks follow the fallback.

### Task 1: Spike harness

**Gate:** none. **Depends on:** nothing. **Unblocks:** Tasks 2–6.

**Files:** (outside the repo) `spike-shell/{main.go,go.mod,frontend/index.html,Info.plist,ent.plist,bundle.sh}`; Create: `docs/superpowers/specs/2026-11-11-choix-2-spike-results.md` (title and one-line purpose only).

**Interfaces:**
- Produces: `bundle.sh [--sandbox] [--hardened] [--dylib <path>] [--identity <name>]` → `spike-shell/bin/Spike.app` (arm64, bundle id `com.github.volodymyrsmirnov.choix.spike`), signed (ad-hoc by default) with `ent.plist` (the four entitlements from Global Constraints when `--sandbox`). The app is a Wails v3.1.0 window that loads `frontend/index.html` (plain HTML, a `<pre id="log">`) and one service `Spike` whose methods each spike adds.

- [ ] **Step 1:** Install `wails3` v3.1.0 (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`) and scaffold `spike-shell` by hand (no template): `application.New`, one window with `Mac: application.MacWindow{TitleBar: application.MacTitleBarHiddenInset}`, `EnableFileDrop: true`.
- [ ] **Step 2:** Write `bundle.sh` (plain shell: `go build -tags production`, create `Contents/{MacOS,Resources,Frameworks}`, copy `Info.plist`, `codesign --force --sign <identity> [--options runtime] --entitlements ent.plist`).
- [ ] **Step 3: Verify.** `./bundle.sh --sandbox && open bin/Spike.app`, then `codesign -d --entitlements - bin/Spike.app` lists the four entitlements and `ls ~/Library/Containers/com.github.volodymyrsmirnov.choix.spike` exists. Expected: both true.
- [ ] **Step 4:** Create the results file; commit `docs: start spike results`.

### Task 2: Spike S6 — a hardened, sandboxed build loads the bundled ONNX Runtime

**Gate:** none. **Depends on:** Task 1. **Unblocks:** Tasks 22, 46, 47, 67.

- [ ] **Step 1:** Download `https://github.com/microsoft/onnxruntime/releases/download/v1.25.0/onnxruntime-osx-arm64-1.25.0.tgz` into the spike directory. Record `shasum -a 256` of the archive and compare it with `gh api repos/microsoft/onnxruntime/releases/tags/v1.25.0 --jq '.assets[] | select(.name=="onnxruntime-osx-arm64-1.25.0.tgz") | .digest'`. Record `otool -l lib/libonnxruntime.1.25.0.dylib | grep -A4 LC_BUILD_VERSION` (the `minos` value) and `otool -L` (must list only `/usr/lib` and `/System` paths).
- [ ] **Step 2:** Add `Spike.LoadRuntime() string`: `ort.SetSharedLibraryPath(<exe>/../Frameworks/libonnxruntime.dylib)`, `ort.InitializeEnvironment()`, return `ort.GetVersion()` or the error.
- [ ] **Step 3:** Run four builds and record each result: `--sandbox`; `--sandbox --hardened` with the dylib signed ad-hoc (`codesign --force --sign -`); the same with the dylib unsigned; unsandboxed.
- [ ] **Pass:** `--sandbox --hardened` with the ad-hoc-signed dylib returns `1.25.0`.
- [ ] **If only `--sandbox` (not hardened) passes under ad-hoc signing:** record that library validation needs a real identity. Consequence: `sign:dev` (Task 47) signs without `--options runtime`; hardened runtime is verified in the signing conversation. Spec §12 S6 gains that sentence.
- [ ] **If `minos` > 13.0:** set the deployment target to that value in spec §10 and in Global Constraints here.
- [ ] **If `otool -L` shows a non-system dependency:** FAIL; spec §8 changes to building ONNX Runtime from source, and Task 22 stops until the owner decides.
- [ ] **Record** the archive SHA-256 — Task 22 pins it.

### Task 3: Spike S5 — CoreML execution provider inside the sandbox, for both models

**Gate:** none. **Depends on:** Task 2. **Unblocks:** Tasks 22, 26.

- [ ] **Step 1:** Copy `clip_vit_b32.onnx` and `topiq_nr_koniq_384.onnx` (from a v1 install's `~/.choix/models/`, or the pinned URLs in `local.KnownModels`) into the spike app's container (`…/Data/Library/Application Support/Spike/`).
- [ ] **Step 2:** Add `Spike.Infer(model string, provider string) string`. For `provider = "coreml"`: `AppendExecutionProviderCoreML(0)` and **fail if it returns an error** (v1's `ort.go` ignores it); create the session with ORT's log severity set to verbose and capture the log; run one zero tensor (1×3×224×224 for CLIP, 1×3×384×384 for quality). Return: output length, session-creation and inference times, the number of graph nodes the log assigns to `CoreMLExecutionProvider` and to `CPUExecutionProvider`, and every file created under the container during the call (`find … -newer <marker>`). For `provider = "cpu"`: no provider appended.
- [ ] **Step 3:** Sandboxed build, each model: `coreml` cold, `coreml` warm, `cpu`. Record all values and `log show --last 2m --predicate 'process == "sandboxd"'`.
- [ ] **Pass (per model):** output length 512 (CLIP) or 1 (quality); the verbose log assigns at least one node to `CoreMLExecutionProvider`; no sandbox denial; the CoreML run is not slower than the CPU run when warm.
- [ ] **On FAIL for a model:** spec §12 S5 fallback for that model: `local.SessionOptions.CPUOnly` is set for it when `appdir.Sandboxed()` (Task 22 implements both paths regardless; this only picks the default). Record the CPU timing so the owner sees the cost. A pass that only "works" with zero CoreML nodes counts as FAIL.

### Task 4: Spike S1 — folder access in the sandbox: picker, drop, bookmarks, Finder

**Gate:** none. **Depends on:** Task 1. **Unblocks:** Tasks 10, 11, 12, 16, 29, 49, 51; decides whether state s101 (drop target) exists.

- [ ] **Step 1:** Clone a small folder, chosen by rule (the smallest folder under the owner's photo folder (`$CHOIX_UAT_ROOT`) with at least 50 media files; the step exits with a message when `CHOIX_UAT_ROOT` is unset): `cp -cR "$SRC" "$CHOIX_SPIKE_DIR/data/folder-1"`. Add `Spike.Pick() string` (`app.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).PromptForMultipleSelection()`) and a `events.Common.WindowFilesDropped` handler. For each returned path (a plain string, not an `NSURL`): create an app-scoped bookmark (`NSURLBookmarkCreationWithSecurityScope`), write `<path>/.choix/spike-1.txt`, store the bookmark bytes in the container.
- [ ] **Step 2:** Add `Spike.Reopen() string`: resolve the stored bookmark (`NSURLBookmarkResolutionWithSecurityScope | WithoutUI | WithoutMounting`), report `stale`, `startAccessingSecurityScopedResource`, list the folder, write `.choix/spike-2.txt`, stop. Add `Spike.Refresh() string`: resolve, start access, create a new bookmark **from that resolved URL**, stop, store the new bytes — and the variant that tries to create it from the path string without starting access.
- [ ] **Step 3:** Sandboxed build, with the clone: (a) pick, quit, relaunch, `Reopen`; (b) drop instead of pick; (c) rename the clone in Finder, `Reopen` (expect the new path, record `stale`), `Refresh`, quit, relaunch, `Reopen` with the refreshed bytes; (d) move the clone to the Trash, `Reopen` — record the resolved path or the `NSError` domain and code, and what `getRelationship:ofDirectory:NSTrashDirectory inDomain:0 toItemAtURL:` returns **without** scoped access and again **inside** a `startAccessing…`/`stop…` pair; (e) delete the clone (`rm -rf`, and separately: empty the Trash), `Reopen` — record the error domain and code, and the `errno` of `lstat` on the stored path made by the sandboxed app without access (`ENOENT` or `EPERM`); also record that `errno` for a folder that still exists but that the app has no access to; (f) repeat (a) on clones placed on an **exFAT** removable volume and on an **SMB** share: record `VolumeInfo` (is there a UUID), the result of `fsync` and `F_FULLFSYNC` on a copied file and of `fsync` on its directory (the exact `errno`s), **the result of `renameatx_np(…, RENAME_EXCL)` onto a free name and onto an existing one** (success, `EEXIST`, or `ENOTSUP` where the volume lacks `VOL_CAP_INT_RENAME_EXCL`), then eject or disconnect and `Reopen` — record the error; then remount the removable volume **at a different mount point** and record what `Reopen` returns and what `lstat` of the old stored path reports; (g) rebuild the app signed with a different identity (`--identity` with a self-signed certificate created for the spike), relaunch, `Reopen` with the bookmark made by the first build — record whether Resolve or StartAccess fails and with which error.
- [ ] **Step 4:** Add `Spike.Finder() string`: under scoped access, `[[NSWorkspace sharedWorkspace] activateFileViewerSelectingURLs:@[folderURL]]`, then `openURL:` on `<folder>/picks` (created first), then `openURL:` on an `https` URL. Record whether Finder selects the folder, opens `picks`, the browser opens, and any `sandboxd` denial.
- [ ] **Pass (picker):** bookmark created from the path string; after relaunch the folder lists and is writable; the renamed folder resolves to its new path; `Refresh` from the resolved URL succeeds and its bytes work after relaunch.
- [ ] **Pass (drop):** the same for the dropped folder.
- [ ] **Pass (Finder):** all three `NSWorkspace` calls work in the sandbox without a denial.
- [ ] **Pass (Trash, d):** the trashed folder is recognised — the relationship call reports "contains" either without access or inside the short access lease. On FAIL: spec §4.1 loses "or resolves into the Trash"; a trashed folder then shows as a normal row until it is removed by hand or deleted for good.
- [ ] **Pass (deleted, e):** the sandboxed app sees `ENOENT` for the deleted path and a different `errno` for an existing path it cannot access — so absence can be verified independently of the bookmark. On FAIL: Task 29 never drops a row for a failed bookmark; the folder shows as `missing` ("can no longer be found") until the user removes it, and spec §4.1's "disappear by themselves" is amended to cover Trash only.
- [ ] **Record for Tasks 10, 11, 16, 29:** the exact error codes for (d), (e), (f), (g) — they become the `ErrGone` code list and confirm that everything else maps to `ErrUnusable`; the sync `errno`s from (f) become `safefs`'s "not supported" list.
- [ ] **On picker FAIL:** stop and report: the library cannot work on Wails' dialog; `internal/app/dialogs` would need its own `NSOpenPanel` (spec §6 is edited to say so before Task 51).
- [ ] **On drop FAIL:** spec §4.1 drops "Dragging folders onto the window"; state s101 is retired by the UI team; Task 49 omits `EnableFileDrop`.
- [ ] **On Finder FAIL:** stop and report; spec §4.1's Open Picks and Reveal need the owner's decision (no fallback keeps hard rule 8).

### Task 5: Spike S2 — menu shortcuts: single keys, and who receives ⌘ combinations

**Gate:** none. **Depends on:** Task 1. **Unblocks:** Tasks 50, 52.

- [ ] **Step 1:** In the spike, build a menu with items `Pick` (`SetAccelerator("Space")`), `Reject` (`"X"`), `Compare` (`"C"`), `Next Photo` (`"Right"`), `Add Folder` (`"CmdOrCtrl+O"`), `Undo` (`"CmdOrCtrl+Z"`), each with an `OnClick` that appends to the log; the page has an `<input>` and a `keydown` logger that records `key`, `metaKey` and `defaultPrevented`.
- [ ] **Step 2:** Record, for each key: pressed with the page focused (does the menu fire; does the page also get `keydown`), and pressed inside the `<input>` (does the character arrive or the field's own undo run; does the menu fire).
- [ ] **Pass (single keys):** typing `x`, `c` and space into the input inserts the characters and fires no menu item, while the same keys outside an input fire the menu item exactly once **and the page receives no `keydown` for them**.
- [ ] **Record (⌘ combinations):** whether the page receives `keydown` for ⌘O and ⌘Z when the menu fires, and whether ⌘Z in the input reaches the field. This fixes Task 52's rule: the webview never acts on a manifest `accelerator`; if the page does not see ⌘Z at all, text-field undo is done by the `undo` action calling `document.execCommand('undo')`.
- [ ] **On single-key FAIL (expected to be likely):** single-key items are built without an accelerator and stay owned by the webview; the key is shown only in the shortcut sheet (spec §4.4 fallback, already written).

### Task 6: Spike S3 — video over the Wails asset scheme with Range

**Gate:** none. **Depends on:** Task 1. **Unblocks:** Task 37's video route; decides states s213, s306, s405.

- [ ] **Step 1:** Mount an `http.Handler` service at route `/media` that serves a ≥ 200 MB `.mov` and a `.mp4` from a cloned folder with `http.ServeContent`, logging each request's `Range` header and the status written. The page has `<video controls preload="none" poster=…>` for each.
- [ ] **Step 2:** Sandboxed build: play, seek to 90 %, seek back; record the logged ranges, time to first frame after a seek, and the process's memory before and after (`footprint`).
- [ ] **Pass:** both files play and seek; the handler sees `Range` requests answered `206`; memory growth stays under 200 MB (the file is not buffered whole).
- [ ] **On FAIL:** spec §12 S3 fallback: Focus shows the poster and the five keyframes with an "Open in QuickTime" action; the UI team reworks the three video states; Task 39 gains `System.OpenInDefaultApp(folderID, relPath string) error` on `platform.Workspace`.

### Task 7: Spike S4 — camera serial numbers through ImageIO; dictionaries for fixtures

**Gate:** none. **Depends on:** nothing (no Wails). **Unblocks:** Task 19.

- [ ] **Step 1:** Write `s4/main.go` (cgo): for a file path, print the JSON of `CGImageSourceCopyPropertiesAtIndex` (index 0) — for RAF also of the embedded JPEG located by the header at offsets 84/88; for a video, the AVFoundation values Task 14 will return (duration, track size, common and `com.apple.quicktime.*` metadata, user-data `©enc` / `©too`).
- [ ] **Step 2:** For one file per camera body and format under the owner's photo folder (`$CHOIX_UAT_ROOT`) (the step exits with a message when `CHOIX_UAT_ROOT` is unset; list them with `exiftool -q -r -p '$Make|$Model|$FileType' "$ROOT" | sort -u`; read-only; the results file names bodies by make and model only — no file names, serials or dates), record side by side: exiftool's `MakerNotes:SerialNumber`, `EXIF:SerialNumber`, `MakerNotes:InternalSerialNumber` (`exiftool -j -G -n`), and every ImageIO key whose name contains `Serial` with its dictionary (`{Exif}`, `{ExifAux}`, `{MakerFuji}`, `{MakerCanon}`, `{MakerApple}`, …). For DNG, record this for the container (`Path`) and for the embedded preview bytes.
- [ ] **Step 3:** Save the dictionaries Task 19 needs as fixtures — a Fuji RAF's embedded JPEG, an iPhone HEIC, a plain JPEG, a DNG container, an iPhone MOV, a drone MP4 — **reduced to the keys in Task 19's table** and scrubbed: serials → `TESTSERIAL01`; every date → `2026:01:02 03:04:05`; GPS → latitude 10.5 N, longitude 20.25 E (ISO 6709 `+10.5000+020.2500/`); no owner, lens-serial or maker-note values; make and model are kept (the per-maker serial table needs them). Keep them in the spike directory; Task 19 commits them.
- [ ] **Pass:** for every body, the value v1 would put in `device_key` (first non-empty of the three exiftool keys) equals one ImageIO value at a stable key. The result is a table: `exiftool key → ImageIO dictionary.key`, per maker — Task 19 copies it into `imageio_map.go`.
- [ ] **On FAIL for a body:** spec §12 S4 fallback, in this order: read the maker note in Go for that maker (Fuji: tag `0x0010` in the maker-note IFD); if that is not practical, Task 19 adds a `device_key` normalisation backfill and the spec §7 parity sentence is amended to name the exception.

### Task 8: Spike S7 — embedded-JPEG speed for RAF and DNG; DNG preview, orientation and metadata

**Gate:** none. **Depends on:** nothing. **Unblocks:** Tasks 18, 19, 20.

- [ ] **Step 1:** Write `s7/main.go`: for each of 50 RAF files (the first 50, sorted, of the smallest folder under the owner's photo folder (`$CHOIX_UAT_ROOT`) that has at least 50; `s7` exits with a message when `CHOIX_UAT_ROOT` is unset) and every DNG under that root, time (a) reading the embedded JPEG bytes (RAF header; DNG: IFD0, the IFDs chained from it by the next-IFD offset, and the SubIFDs of each; candidates being IFDs with `NewSubFileType` bit 0 set, `Compression` 7 or 6 and `PhotometricInterpretation` 6 (YCbCr) or 2 (RGB) — never 32803 (CFA) or 34892 (LinearRaw)) and (b) `CGImageSourceCreateThumbnailAtIndex` from those bytes at widths 256, 1600 and 4096. For each DNG also record **where** each candidate was found (IFD0, a chained IFD, a SubIFD) and whether every candidate's bytes decode. Also time a full ImageIO decode of the RAW at 1600 for five files, and `CGImageSourceCopyPropertiesAtIndex` on the DNG container.
- [ ] **Step 2:** Record median and p95 per step; per body the embedded JPEG's pixel size; for each DNG: IFD0 `Orientation`, whether the preview JPEG carries its own orientation, whether the preview appears upright only after applying IFD0's value, where Make/Model/serial/dates/GPS are found (container vs preview), and every DNG with no preview of width ≥ 1600.
- [ ] **Pass:** median (a)+(b) at 1600 ≤ 0.5 s per RAF; every DNG has a preview with width ≥ 1600; reading the DNG container's properties takes < 50 ms (no pixel decode).
- [ ] **On FAIL (speed):** record the numbers; Task 20 lowers thumb-stage concurrency only if memory, not time, is the limit.
- [ ] **On FAIL (DNG without preview, or container properties decode pixels):** spec §7 fallback: Task 20's RAW-decode path is used for those files (detached, so it never blocks cancellation); if container properties are slow, Task 19 reads DNG tags from IFD0/ExifIFD in Go instead.

---

## Phase 1 — Platform layer

All cgo files are `//go:build darwin`. Every Objective-C entry point is wrapped in `@autoreleasepool { @try { … } @catch (NSException *e) { … } }` and turns both `NSError` and a caught exception into a Go error — an Objective-C exception crossing cgo kills the process. Compile flags: `-x objective-c -fobjc-arc -mmacosx-version-min=14.0`.

### Task 9: Platform interfaces, detached calls, fakes

**Gate:** none. **Depends on:** nothing.

**Files:**
- Create: `internal/platform/{bookmark.go,media.go,volumes.go,workspace.go,detach.go}`, `internal/platform/platformtest/{bookmarks.go,volumes.go,media.go,workspace.go,fixtures.go}`
- Test: `internal/platform/detach_test.go`, `internal/platform/platformtest/{fakes_test.go,fixtures_test.go}`

**Interfaces:**
- Produces (**[call] C2, C3**):
  ```go
  package platform
  var ErrGone = errors.New("platform: bookmark target no longer exists")   // only the "no such file / volume" codes recorded by spike S1
  var ErrUnusable = errors.New("platform: bookmark cannot be used")        // every other resolve or access failure
  var ErrNoVideoTrack = errors.New("platform: no video track")

  type Bookmarks interface {
      Create(path string) ([]byte, error)                             // the process must have access now (picker or drop grant, or an active StartAccess)
      Resolve(data []byte) (path string, stale bool, err error)       // never mounts, never shows UI
      StartAccess(data []byte) (path string, stop func(), err error)  // stop is idempotent. In the sandbox a refused start is ErrUnusable; outside it, a refused start is a lease whose stop does nothing
      Refresh(data []byte) ([]byte, error)                            // resolve, start access, create a new bookmark from that same URL, stop. On any error the caller keeps the old bytes
  }
  type VolumeInfo struct{ UUID, Name string; ReadOnly, Removable bool } // UUID "" when the volume has none (network shares)
  type Volumes interface {
      Info(path string) (VolumeInfo, error)
      Mounted(uuid string) bool
      InTrash(path string) (bool, error) // true only for a verified relationship to the user's or the item's volume's Trash; an error when it cannot be determined (the caller retries under an access lease)
      AbsentOn(path, uuid string) bool   // true only when lstat(path) fails with ENOENT ("no such file", never "not permitted") AND the nearest existing ancestor of path lies on the mounted volume whose UUID is uuid — so the probe was made on the volume the folder lived on, not at a stale mount point
  }
  type Workspace interface { // each method returns only after the AppKit call has actually run — there is no early return — so a caller's access lease always covers it
      Reveal(path string) error     // Finder window with the item selected
      OpenFolder(path string) error // Finder window showing the folder's contents
      OpenURL(url string) error     // https only
  }
  type ImageInput struct{ Path string; Data []byte; Orientation int } // exactly one of Path/Data; Orientation 1–8 overrides the source's own tag, 0 keeps it
  type Image struct{ JPEG []byte; Width, Height int }
  type Fit int
  const ( FitWidth Fit = iota; FitLongSide ) // which dimension `limit` bounds
  type Stills interface {
      Properties(ctx context.Context, in ImageInput) (map[string]any, error)             // ImageIO's dictionary for image 0, JSON-shaped; non-finite numbers dropped; no pixel decode
      Thumbnail(ctx context.Context, in ImageInput, limit int, fit Fit) (Image, error)   // JPEG q=0.85, orientation applied, width (FitWidth) or long side (FitLongSide) at most `limit`, aspect kept, never upscaled; writes no file
  }
  type Video interface {
      Properties(ctx context.Context, path string) (map[string]any, error)
      Frames(ctx context.Context, path string, fractions []float64) ([]Image, error) // one full-size JPEG per fraction of the duration; ErrNoVideoTrack
  }

  // Detached native calls
  type Inflight struct{ /* counts running detached calls */ }
  func (i *Inflight) Wait(timeout time.Duration) bool               // true once none is running; a negative timeout waits without limit
  func WithInflight(ctx context.Context, i *Inflight) context.Context
  var NativeSlots = runtime.NumCPU() + 4 // process-wide; read once, at the first Acquire
  func Acquire(ctx context.Context) (release func(), err error)            // takes one native slot (waiting for one honours ctx) and counts in the context's Inflight; release — idempotent — must be called when the native work has really finished, however late
  func Detach[T any](ctx context.Context, fn func() (T, error)) (T, error) // Acquire, then fn on its own goroutine; returns ctx.Err() as soon as ctx is done and discards fn's later result; the slot and the Inflight count are released only when fn returns
  ```
  Because a slot is held until the native call returns, at most `NativeSlots` native calls exist at any time, abandoned ones included: after a preemption the new job simply runs with fewer slots until the abandoned calls finish.
- `platformtest`: `Bookmarks` (in-memory; `Move(old, new)`, `Delete(path)`, `MakeUnusable(path)`, `DenyAccess(path)`, `MarkStale(path)`, `Active() []string`, `Refreshed() int`), `Volumes` (`Add(prefix string, v platform.VolumeInfo)`, `Unmount(uuid)`, `Mount(uuid)`, `Trash(path)`, `TrashUnknown(path)`, `TrashNeedsAccess(path, b *Bookmarks)` — `InTrash` errors unless that path's access is active —, `SetAbsent(path, uuid string, absent bool)`), `Workspace` (records calls), `Stills` and `Video` (configurable `Props map[string]map[string]any`; `Thumbnail`/`Frames` return a real JPEG of the requested size made with `image/jpeg` — and, for `ImageInput.Data`, first decode the bytes completely with `image/jpeg` and fail when that fails, so a damaged preview body behaves as it does natively; `Fail map[string]error`; `Gate chan struct{}` to hold a call open; both go through `platform.Detach`). Fixture builders (no `os/exec`): `MakeJPEG(t, w, h int) []byte`; `MakeRAF(t, jpeg []byte) string` (16-byte magic `FUJIFILMCCD-RAW `, big-endian JPEG offset at byte 84 and length at byte 88, JPEG placed at offset 148); `MakeDNG(t, o DNGOptions) string` with `DNGOptions{Orientation int; Previews []DNGPreview{JPEG []byte; Width, Height int; DamageBody bool /* keep the SOI/SOF/SOS header, cut the scan data to 16 bytes */; Chained bool /* reachable only through IFD0's next-IFD offset */}; RawStrip []byte}` — little-endian TIFF: IFD0 has `DNGVersion` (0xC612), `Make` `TESTCAM`, `Model` `D1`, `Orientation`, `NewSubFileType`=1 and `SubIFDs` (0x014A); the first SubIFD is the raw image (`NewSubFileType`=0, `Compression`=7, `PhotometricInterpretation`=32803, strip = `RawStrip`), the following ones are the previews (`NewSubFileType`=1, `Compression`=7, `PhotometricInterpretation`=6, `ImageWidth`, `ImageLength`, single strip).

- [ ] **Step 1: Write the failing tests.** `detach_test.go`: `TestDetach_ReturnsResult`; `TestDetach_SlotsBoundAbandonedCalls` (with 2 slots: two calls are abandoned while their fn still blocks; a third `Detach` does not start its fn; cancelling the third's context returns `context.Canceled` without ever running fn; when one abandoned fn returns, a fourth call runs); `TestAcquire_ReleaseIsIdempotent`; `TestDetach_ReturnsOnCancelWhileRunning` (fn blocks on a channel; cancel ⇒ `context.Canceled` in < 100 ms; `Inflight.Wait(10*time.Millisecond) == false`; release the channel ⇒ `Wait(time.Second) == true`); `TestDetach_WithoutInflightStillCancels`; `TestInflight_WaitNegativeBlocksUntilDone`. `fakes_test.go`: `TestFakeBookmarks_ResolveFollowsMove`; `TestFakeBookmarks_DeleteIsErrGone`; `TestFakeBookmarks_UnusableIsErrUnusable`; `TestFakeBookmarks_StartStopTracksActive` (after start `Active() == ["/a"]`, after stop twice `[]`); `TestFakeBookmarks_RefreshReturnsNewBytes`; `TestFakeVolumes_UnmountMount`; `TestFakeVolumes_TrashUnknownIsError`. `fixtures_test.go`: `TestMakeRAF_HeaderOffsets` (bytes 84–87 decode to 148, 88–91 to `len(jpeg)`); `TestMakeDNG_Layout` (starts `II*\x00`; the raw strip and each preview are found at their recorded offsets; IFD0 `Orientation` equals the option).
- [ ] **Step 2: Run** `go test ./internal/platform/...` — expected: FAIL, packages do not exist.
- [ ] **Step 3: Write the interfaces, `Detach`, fakes and builders.**
- [ ] **Step 4: Run** `go test -race ./internal/platform/...` — expected: PASS.
- [ ] **Step 5: Commit** `feat(platform): interfaces, detached calls, fakes and fixture builders`.

### Task 10: `darwin/bookmark`

**Gate:** spike S1. **Depends on:** Tasks 4, 9.

**Files:**
- Create: `internal/platform/darwin/bookmark/{bookmark.go,bookmark.m,bookmark.h}`
- Test: `internal/platform/darwin/bookmark/bookmark_test.go`

**Interfaces:**
- Produces: `func New() platform.Bookmarks`. Create uses `NSURLBookmarkCreationWithSecurityScope` and retries with options `0` when that fails outside the sandbox. Resolve, StartAccess and Refresh use `NSURLBookmarkResolutionWithSecurityScope | WithoutUI | WithoutMounting`. Error mapping: the `NSError` codes spike S1 recorded for a deleted target and an unmounted volume ⇒ `platform.ErrGone`; every other failure, including a bookmark made under another code signature ⇒ `platform.ErrUnusable` (both wrap the system message). "In the sandbox" is `os.Getenv("APP_SANDBOX_CONTAINER_ID") != ""`. `Refresh` holds one balanced start/stop around the create and never returns partial bytes.

- [ ] **Step 1: Write the failing tests** (unsandboxed, `t.TempDir()`): `TestBookmark_RoundTrip` (Create then Resolve returns the directory's `filepath.EvalSymlinks` path, `stale == false`); `TestBookmark_FollowsRename`; `TestBookmark_DeletedIsErrGone`; `TestBookmark_GarbageIsErrUnusable` (`Resolve([]byte("x"))` ⇒ `errors.Is(err, platform.ErrUnusable)`, no crash); `TestBookmark_StartAccessStopTwice` (no panic; outside the sandbox the lease is granted); `TestBookmark_RefreshAfterRename` (new bytes differ, resolve to the new path, the old bytes are untouched in the caller); `TestBookmark_RefreshOfDeletedKeepsError` (`ErrGone`, nil bytes); `TestBookmark_CreateOnFileFails` (error containing `not a directory`).
- [ ] **Step 2: Run** `go test ./internal/platform/darwin/bookmark/` — expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race -count=3 ./internal/platform/darwin/bookmark/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(platform): security-scoped bookmarks`.

### Task 11: `darwin/volumes`

**Gate:** spike S1. **Depends on:** Tasks 4, 9.

**Files:**
- Create: `internal/platform/darwin/volumes/{volumes.go,volumes.m,volumes.h}`
- Test: `internal/platform/darwin/volumes/volumes_test.go`

**Interfaces:**
- Produces: `func New() platform.Volumes`. `Info` reads `NSURLVolumeUUIDStringKey` (absent ⇒ `""`), `NSURLVolumeLocalizedNameKey`, `NSURLVolumeIsReadOnlyKey`, `NSURLVolumeIsRemovableKey`. `Mounted` compares against `mountedVolumeURLsIncludingResourceValuesForKeys:options:0`; `Mounted("")` is `false`. `InTrash` (**[call] C7**) returns `true` only when `getRelationship:ofDirectory:NSTrashDirectory inDomain:0 toItemAtURL:error:` reports `NSURLRelationshipContains`; when that call fails it returns the error (Task 29 then repeats it under a short access lease). No path-name heuristic. `AbsentOn(path, uuid)`: `lstat(path)` must fail with `ENOENT` (`EPERM`, `EACCES` and every other answer ⇒ `false`); then it walks up to the nearest ancestor that exists, reads that ancestor's volume UUID with `Info`, and returns `true` only when it equals `uuid` and `uuid != ""`. If the ancestor's volume cannot be read, or is another volume (the drive is mounted somewhere else and the stored path now points into, say, an empty `/Volumes`), the answer is `false`.

- [ ] **Step 1: Write the failing tests.** `TestVolumes_InfoOfTempDir` (UUID non-empty, `ReadOnly == false`); `TestVolumes_MountedBootVolume`; `TestVolumes_UnknownAndEmptyUUIDNotMounted`; `TestVolumes_InTrash_UserTrash` (a directory created under `~/.Trash/choix-test-<random>` and removed afterwards ⇒ `true`; skipped when `~/.Trash` is not writable); `TestVolumes_NotInTrash_LookalikeName` (`<tmp>/.Trash/x` and `<tmp>/.Trashes/501/x` created in `t.TempDir()` ⇒ `false`); `TestVolumes_InTrash_MissingPathIsError`; `TestVolumes_AbsentOn` (a removed temp directory with the temp volume's UUID ⇒ `true`; the same path with another UUID ⇒ `false`; with `""` ⇒ `false`; an existing directory ⇒ `false`; a path under a directory with mode 0000 ⇒ `false`; `/Volumes/choix-no-such-volume/x` with the boot volume's UUID ⇒ `true` only if `/Volumes` is on the boot volume — and with any other UUID ⇒ `false`, which is the mounted-elsewhere case); `TestVolumes_InfoMissingPath` (error).
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/platform/darwin/volumes/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(platform): volume state and verified trash detection`.

### Task 12: `darwin/workspace`

**Gate:** spike S1. **Depends on:** Tasks 4, 9.

**Files:**
- Create: `internal/platform/darwin/workspace/{workspace.go,workspace.m,workspace.h}`
- Test: `internal/platform/darwin/workspace/workspace_test.go`

**Interfaces:**
- Produces (**[call] C6**): `func New() platform.Workspace`. `Reveal`: `[[NSWorkspace sharedWorkspace] activateFileViewerSelectingURLs:@[url]]`. `OpenFolder`: `openURL:` on the directory's file URL; the path must be an existing directory. `OpenURL`: parses with `net/url`, accepts only scheme `https`, then `openURL:`. The AppKit call is dispatched to the main queue and the method **waits until the block has run** (a semaphore signalled at the end of the block). There is no timeout and no early return: a method that returned before its block ran would let the caller release the access lease while the block is still queued. The caller therefore always holds its lease while AppKit runs. It is never called from the main thread (bindings run on goroutines). If the main thread is busy with shutdown, the call simply waits; shutdown does not wait for it (it is an in-flight binding, covered by the quit deadline) and the session it holds is left open, as R17 prescribes. A package-level `var dispatch = realDispatch` lets tests replace the AppKit call.

- [ ] **Step 1: Write the failing tests** (with `dispatch` replaced by a recorder; no Finder window opens): `TestReveal_PassesFileURL`; `TestReveal_MissingPath` (error, nothing dispatched); `TestOpenFolder_RejectsFile`; `TestReveal_ReturnsOnlyAfterDispatchedBlockRan` (the recorder's block sleeps 200 ms ⇒ `Reveal` takes at least that long); `TestReveal_NoEarlyReturnWhenBlockIsDelayed` (the recorder holds the block for 6 s — longer than any old timeout — and records, at the moment the block finally runs, whether `Reveal` has returned: it has not; `Reveal` returns right after); `TestOpenURL_HTTPSOnly` (`http://x`, `file:///etc/passwd`, `javascript:1` ⇒ error, nothing dispatched; `https://github.com/volodymyrsmirnov/choix` dispatched once).
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/platform/darwin/workspace/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(platform): Finder and URL opening through NSWorkspace`.

### Task 13: `darwin/imageio`

**Gate:** none. **Depends on:** Task 9.

**Files:**
- Create: `internal/platform/darwin/imageio/{imageio.go,imageio.m,imageio.h}`, `testdata/media/{orient6.jpg,nan-exif.jpg,sample.heic,README.md}`, `testdata/media/_make_fixtures.go` (`//go:build ignore`; writes `orient6.jpg` — 64×32 stored pixels, EXIF Orientation 6, left half red — and `nan-exif.jpg` — a JPEG whose EXIF `ExposureTime` rational has denominator 0)
- Test: `internal/platform/darwin/imageio/imageio_test.go`

**Interfaces:**
- Produces: `func New() platform.Stills`. Both methods run their native part through `platform.Detach` (**[call] C3**).
  - `Properties`: `CGImageSourceCreateWithURL` / `…WithData`, `CGImageSourceCopyPropertiesAtIndex(src, 0, nil)`, converted in Objective-C to JSON-safe values (dictionaries, arrays, strings; numbers only when finite — NaN and ±Inf are dropped with their key; `NSData` → lowercase hex string; anything else → `description`), then `NSJSONSerialization` → `json.Unmarshal`.
  - `Thumbnail` (**[call] C8**): `FitLongSide` passes `limit` straight to `kCGImageSourceThumbnailMaxPixelSize` (what `sips -Z` did). `FitWidth` reads the source's pixel size and orientation and computes the max-pixel value that yields a width of `limit` after the orientation transform (never above the source size). It then calls `CGImageSourceCreateThumbnailAtIndex` with `kCGImageSourceCreateThumbnailFromImageAlways`, `kCGImageSourceCreateThumbnailWithTransform` and that `kCGImageSourceThumbnailMaxPixelSize`; with `ImageInput.Orientation` set, the transform comes from that value instead of the file's tag. Output through `CGImageDestinationCreateWithData` (`public.jpeg`, `kCGImageDestinationLossyCompressionQuality = 0.85`). No file is written.
- Errors: a source ImageIO cannot open → `fmt.Errorf("imageio: cannot read %s", name)`.
- `sample.heic` is a committed 800×600 synthetic image made once with `sips -s format heic` from a `MakeJPEG` output (command in `README.md`).

- [ ] **Step 1: Write the failing tests.** `TestProperties_JPEG` on `testdata/exif/sample.jpg` (`PixelWidth`, `PixelHeight` numbers; a `{TIFF}` or `{Exif}` dictionary). `TestProperties_Data` (same result from the bytes). `TestProperties_NonFiniteNumbersDropped` (`nan-exif.jpg` ⇒ no error; `{Exif}` has no `ExposureTime`; the other keys are present). `TestThumbnail_FitWidth` (`MakeJPEG(1000, 500)`, 256 ⇒ 256×128; `MakeJPEG(500, 1000)` ⇒ 256×512). `TestThumbnail_FitLongSide` (`MakeJPEG(1000, 500)`, 256 ⇒ 256×128; `MakeJPEG(500, 1000)` ⇒ 128×256). `TestThumbnail_NeverUpscales` (`MakeJPEG(100, 50)`, 256, both fits ⇒ 100×50). `TestThumbnail_AppliesOrientation` (`orient6.jpg`: `FitWidth` 16 ⇒ 16×32; `FitLongSide` 32 ⇒ 16×32). `TestThumbnail_OrientationOverride` (`MakeJPEG(64, 32)` with `Orientation: 6`, `FitWidth` 32 ⇒ 32×64). `TestThumbnail_HEIC` (`sample.heic`, `FitLongSide` 256 ⇒ 256×192; the bytes decode with `image/jpeg`). `TestThumbnail_NotAnImage` (text bytes ⇒ error containing `cannot read`). `TestThumbnail_ZeroBytes` (error). `TestThumbnail_ReturnsOnCancelDuringCall` (an unexported `slowHook` sleeps 3 s inside the detached function; cancel after 100 ms ⇒ `context.Canceled` in under 1 s; `Inflight.Wait(5*time.Second) == true`). `TestEntryPoints_CatchObjCException` (an unexported test entry that raises `NSException` inside the guarded block returns a Go error containing the exception's name).
- [ ] **Step 2: Run** `go test ./internal/platform/darwin/imageio/` — expected: FAIL.
- [ ] **Step 3: Generate the fixtures** (`go run testdata/media/_make_fixtures.go`) **and implement.**
- [ ] **Step 4: Run** `go test -race ./internal/platform/darwin/imageio/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(platform): ImageIO metadata and thumbnails`.

### Task 14: `darwin/avmedia`

**Gate:** none. **Depends on:** Task 9.

**Files:**
- Create: `internal/platform/darwin/avmedia/{avmedia.go,avmedia.m,avmedia.h}`, `testdata/media/clip.mov`, `testdata/media/audio-only.mov`
- Test: `internal/platform/darwin/avmedia/avmedia_test.go`

**Interfaces:**
- Produces: `func New() platform.Video`.
  - `Properties`: loads `duration`, `tracks`, `commonMetadata`, `metadata` with `loadValuesAsynchronouslyForKeys:completionHandler:` and waits for the handler or `ctx.Done()`. Returns `{"duration": seconds, "width": n, "height": n, "make": s, "model": s, "software": s, "location": "+10.5000+020.2500/", "encoder": s}` — width/height are the first video track's `naturalSize` (not rotated); `make`, `model`, `software`, `location` from the common keys and `com.apple.quicktime.{make,model,software,location.ISO6709}`; `encoder` from the user-data keys `©enc`, then `©too`; absent values are omitted.
  - `Frames`: `AVAssetImageGenerator` with `appliesPreferredTrackTransform = YES`, default tolerances, `generateCGImagesAsynchronouslyForTimes:completionHandler:`; on `ctx.Done()` it calls `cancelAllCGImageGeneration` and returns `ctx.Err()`; each image is encoded as JPEG (q 0.85) in memory.
  - Ownership (**R18**): both methods take a slot with `platform.Acquire` and release it only when the **last** completion callback has run (AVFoundation calls the handler once per requested time, also for cancelled ones; `Properties`' handler once). The asset, the generator and the result buffers are retained by the callback block until then, every callback body runs inside `@autoreleasepool`, and a callback that arrives after the Go caller has returned drops its result. So a session's `Inflight` — and with it the folder's scoped access — outlives every callback.
- Fixtures: `clip.mov` (2 s, 64×36, H.264, ≤ 20 KB, synthetic), made once with `ffmpeg -f lavfi -i testsrc=d=2:s=64x36:r=10 -movflags use_metadata_tags -metadata com.apple.quicktime.make=TESTCAM -metadata com.apple.quicktime.model=T1 -metadata com.apple.quicktime.location.ISO6709=+10.5000+020.2500/ -metadata creation_time=2026-01-02T03:04:05Z -c:v libx264 -pix_fmt yuv420p clip.mov`; `audio-only.mov` from `-f lavfi -i sine=d=1`. Both commands go into `testdata/media/README.md`. If AVFoundation does not report `make` for the file ffmpeg wrote, the fixture is instead written by `testdata/media/_make_clip.swift` (AVAssetWriter with `AVMetadataItem`s for the same four keys; run once with `swift`), and the README records that.

- [ ] **Step 1: Write the failing tests.** `TestProperties_Clip` (`duration` within 0.2 of 2.0, `width == 64`, `height == 36`, `make == "TESTCAM"`, `model == "T1"`, `location` starts with `+10.5000+020.2500`). `TestFrames_FiveFractions` (fractions `0, 0.25, 0.5, 0.75, 0.95` ⇒ five images that decode as 64×36 JPEGs, in order). `TestFrames_NoVideoTrack` (`audio-only.mov` ⇒ `errors.Is(err, platform.ErrNoVideoTrack)`). `TestProperties_NotAMovie` (a text file named `x.mov` ⇒ error). `TestFrames_CancelDuringGeneration` (cancel from a goroutine 1 ms after the call on a request for 200 fractions ⇒ `context.Canceled` in under 1 s). `TestFrames_InflightOutlivesCancel` (after that cancelled call returns, `Inflight.Wait(0)` may be `false`; `Inflight.Wait(10*time.Second)` is `true`; an unexported counter shows as many callbacks ran as times were requested). `TestFrames_HoldsOneSlot` (with `NativeSlots = 1`, a second `Frames` call starts only after the first one's last callback). `TestProperties_CancelledContext`.
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Make the fixtures and implement.** **Step 4: Run** `go test -race ./internal/platform/darwin/avmedia/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(platform): AVFoundation metadata and cancellable frames`.

---

## Phase 2 — Engine changes

### Task 15: `appdir` on Application Support; `visual_grouping`; one config source

**Gate:** none. **Depends on:** nothing.

**Files:**
- Modify: `internal/appdir/appdir.go` (rewritten), `internal/config/{config.go,toml.go,web.go}`, `internal/cli/pipeline_build.go` (`loadEffectiveConfig` returns `config.Load()` only), `internal/ai/local/ort.go` (drop the `appdir.RuntimeDylib` candidate)
- Create: `internal/appdir/appdir_darwin.go` (cgo), `internal/appdir/{override_dev.go,override_ship.go}`
- Delete: `internal/config/env.go`, `internal/server/settings_migration.go` and its call in `server.New`
- Test: `internal/appdir/appdir_test.go`, `internal/config/web_test.go`

**Interfaces:**
- Produces (**[call] C11**):
  ```go
  package appdir
  func Root() (string, error)                    // the override if allowed and set, else <NSApplicationSupportDirectory, user domain>/Choix; created 0700 on every call
  func Config() (string, error)                  // Root/config.toml
  func Models() string                           // Root/models, created 0750
  func LibraryDB() (string, error)               // Root/library.db
  func Previews(folderID string) (string, error) // Root/previews/<folderID>, created; folderID must match ^[0-9a-f]{16}$
  func Logs() (string, error)                    // <override>/logs if overridden, else <NSLibraryDirectory>/Logs/Choix, created
  func Sandboxed() bool                          // os.Getenv("APP_SANDBOX_CONTAINER_ID") != ""
  ```
  `override_dev.go` (`//go:build !production || server`) reads `CHOIX_DATA_DIR`; `override_ship.go` (`//go:build production && !server`) ignores it. `Bin` and `RuntimeDylib` are removed. `config.Config` gains `VisualGrouping bool` (default `true`, TOML `visual_grouping`).

- [ ] **Step 1: Write the failing tests.** `TestRoot_EnvOverride` (`t.Setenv("CHOIX_DATA_DIR", dir+"/new")` ⇒ `Root() == dir+"/new"` **and the directory now exists**; `Config()`, `LibraryDB()`, `Logs()` are under it). `TestRoot_ApplicationSupport` (no override ⇒ ends with `/Library/Application Support/Choix`). `TestPreviews_RejectsBadID` (`"../x"`, `""`, `"ABC"` ⇒ error; `"0123456789abcdef"` ⇒ directory exists). `TestDefaults_VisualGroupingOn`. `TestSaveLoad_RoundTripsVisualGrouping`. `TestLoad_IgnoresEnvironment` (`t.Setenv("CHOIX_BUCKET_SIZE","60")` ⇒ `BucketSizeSec == 600`). The override tests live in `override_dev_test.go` (`//go:build !production || server`); `override_ship_test.go` (`//go:build production && !server`) has `TestOverride_IgnoredInShippingBuild`.
- [ ] **Step 2: Run** `go test ./internal/appdir/ ./internal/config/` — expected: FAIL.
- [ ] **Step 3: Implement**; update the v1 call sites listed above so `go build ./...` passes.
- [ ] **Step 4: Run** `make test && make lint && go test -tags production ./internal/appdir/` — expected: clean.
- [ ] **Step 5: Commit** `feat(appdir): Application Support paths and visual_grouping setting`.

### Task 16: `safefs` — symlink-resistant writes

**Gate:** spike S1 (for the sync errors of step f). **Depends on:** Task 4.

**Files:**
- Create: `internal/safefs/safefs.go`
- Test: `internal/safefs/safefs_test.go`

**Interfaces:**
- Produces (**[call] C5**; built on `os.Root`, Go ≥ 1.25):
  ```go
  var ErrSymlink = errors.New("safefs: path component is a symbolic link")
  var ErrNotDir = errors.New("safefs: path component is not a directory")
  type Dir struct{ /* an opened directory inside a root */ }
  func OpenDir(root, rel string, create bool) (*Dir, error) // rel is slash-separated, local (filepath.IsLocal) and not "."; walks one component at a time: Lstat must report a directory (symlink ⇒ ErrSymlink, other ⇒ ErrNotDir), the component is opened and must be os.SameFile with what Lstat saw; create makes missing components 0750
  var ErrExists = errors.New("safefs: the target already exists")
  func (d *Dir) CreateTemp(base string) (f *os.File, tmpName string, err error) // O_WRONLY|O_CREATE|O_EXCL, 0600, name base + "." + 8 random hex + ".tmp"
  func (d *Dir) Replace(f *os.File, tmpName, final string) error               // for caches: close, rename over final inside d (an older version of the same cache file is replaced); a failure removes tmpName
  func (d *Dir) Publish(f *os.File, tmpName, final string, durable bool) error // for exports: never overwrites — unix.RenameatxNp(…, unix.RENAME_EXCL) on d's descriptor; an existing final ⇒ ErrExists and tmpName is removed. durable: sync the file before the rename and the directory after it. Fallback below when the volume cannot rename exclusively
  func (d *Dir) WriteCache(final string, data []byte) error                     // CreateTemp + write + Replace
  func (d *Dir) OpenRead(name string) (*os.File, error)                         // O_RDONLY|O_NOFOLLOW; os.ErrNotExist when absent
  func (d *Dir) VerifyRegular(names ...string) error                            // each name that exists must be a regular file (Lstat): a symlink ⇒ ErrSymlink, anything else ⇒ an error naming it
  func (d *Dir) Remove(name string) error                                       // regular files only (Lstat); missing is not an error
  func (d *Dir) RemoveTemps() (int, error)                                      // unlinks regular files named *.tmp in d
  func (d *Dir) Sync() error                                                    // tolerant, see below
  func (d *Dir) Close() error
  ```
  `final`, `base` and `name` are single path elements (no separator), else an error.
- **Sync tolerance (R20).** A file or directory sync that fails with an "operation not supported" class error — `ENOTSUP`, `EINVAL`, `ENOTTY`, plus whatever spike S1(f) recorded for exFAT and SMB — is treated as success and logged once per volume (`slog.Warn("volume cannot sync", "device", st_dev)`). Any other error (`EIO`, `ENOSPC`, `EROFS`…) is returned, and `Publish` then removes the temporary file and fails.
- **Exclusive rename not supported.** `renameatx_np(RENAME_EXCL)` answers `ENOTSUP` on a file system without `VOL_CAP_INT_RENAME_EXCL` (spike S1(f) records whether exFAT and SMB do). `Publish` then falls back, for that call, to: `Lstat(final)` must report "does not exist" (anything else ⇒ `ErrExists`), then a plain rename inside `d`; logged once per volume (`slog.Warn("volume cannot rename exclusively", "device", st_dev)`). The window between the check and the rename is inside the declared threat model (no concurrent local attacker); without the fallback every pick on such a volume would fail.
- This is the whole confinement mechanism (R16): checks happen when a directory is opened and when a file is published; nothing re-checks a directory that is swapped afterwards by another process.

- [ ] **Step 1: Write the failing tests.** `TestOpenDir_CreatesNested` (`a/b/c` under a temp root). `TestOpenDir_SymlinkComponentRefused` (root contains `picks -> originals`; `OpenDir(root, "picks", true)` ⇒ `ErrSymlink`; the same for a symlink in the middle of `a/link/c` and for a symlink pointing outside the root). `TestOpenDir_FileComponentRefused` (`ErrNotDir`). `TestOpenDir_RejectsNonLocal` (`".."`, `"/abs"`, `"."`, `"a/../.."`). `TestCreateTemp_DoesNotFollowPlantedSymlink` (Review Focus 5: an original `o.jpg` with known bytes; plant `x.jpg.tmp -> ../o.jpg` and 50 more guessed names in the directory; `WriteCache("x.jpg", data)` succeeds; `o.jpg` is byte-identical). `TestReplace_ReplacesExistingFileAtomically`. `TestReplace_FailureRemovesTemp`. `TestPublish_NeverOverwrites` (final exists with other bytes ⇒ `ErrExists`; final is byte-identical; no `*.tmp` left). `TestPublish_CollisionArrivingAfterCheck` (an unexported `beforeRename` hook creates `final` between the caller's existence check and the rename ⇒ `ErrExists`, the late file is untouched). `TestPublish_FinalIsSymlinkIsNotFollowed` (`final -> ../o.jpg` ⇒ `ErrExists`; `o.jpg` intact). `TestPublish_DurableCallsSyncInOrder` (an unexported `trace` hook records `file-sync`, `rename`, `dir-sync`). `TestPublish_SyncNotSupportedIsTolerated` (an injected sync function returning `ENOTSUP` ⇒ success, one warning for two publishes on the same device). `TestPublish_SyncIOErrorFails` (`EIO` ⇒ error, no final, no temp). `TestPublish_ExclusiveRenameNotSupportedFallsBack` (an injected rename function returning `ENOTSUP`: a free name ⇒ published with a plain rename, one warning for two publishes on the same device; an existing `final` ⇒ `ErrExists`, its bytes intact; a `final` that is a symlink ⇒ `ErrExists`). `TestVerifyRegular` (absent names pass; a symlink ⇒ `ErrSymlink`; a directory ⇒ error). `TestRemove_RefusesDirectoryAndSymlink` (a symlink is removed as a link, its target stays). `TestRemoveTemps`. `TestWriteCache_RejectsPathSeparator`.
- [ ] **Step 2: Run** `go test ./internal/safefs/` — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/safefs/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(safefs): symlink-resistant directory handles and atomic writes`.

### Task 17: Durable, confined picks

**Gate:** none. **Depends on:** Task 16.

**Files:**
- Create: `internal/store/durable.go`, `internal/picks/{dir.go,reconcile.go}`
- Modify: `internal/picks/{picks.go,export.go}` (rewritten on `safefs`), `internal/store/{picks.go,store.go,files.go,testhelper.go}`, `internal/scanner/scanner.go` (`SetExtraSkipDirs`), `internal/cli/serve.go` (pass the database **path**, not a DSN, to `store.Open`), and every test that builds a `file:` DSN for `store.Open` (find them with `grep -rn 'Open("file:\|"file:" +' internal`)
- Test: `internal/store/{durable_test.go,open_test.go}`, `internal/picks/{picks_test.go,export_test.go,dir_test.go,reconcile_test.go}` (v1 tests kept where the behaviour is unchanged)

**Interfaces:**
- Produces (**[call] C4, C5, C12, C13**):
  ```go
  // store
  func Open(path string) (*Store, error)          // CHANGED: takes a filesystem path, not a DSN
  func FileURI(path string, pragmas ...string) string // "file:" + path with every '%', '#' and '?' percent-encoded ('%' first) + "?_pragma=<p>&…"; shared with library.OpenDB
  func (s *Store) Durable(ctx context.Context, fn func(tx *sql.Tx) error) error
  func (r *PicksRepo) SetTx(tx *sql.Tx, fileID int64, state, exportedPath string) error // state "" deletes the row
  func (r *FilesRepo) CountUnderPath(dir string) (int, error)                            // rows with scan_status <> 'missing' whose path is under dir
  type ExportDir struct{ Dir string; Count int }
  func (r *PicksRepo) ExportDirs() ([]ExportDir, error) // for every row with exported_path E and file path P: Dir(E) with Dir(P) stripped from its end, leading "/" trimmed; most used first

  // picks
  var ErrDirHasOriginals = errors.New("picks: the picks folder holds photos Choix has scanned as originals")
  type DirError struct{ Reason string } // empty | absolute | traversal | reserved
  func ValidateDir(p string) error      // *DirError: "" ⇒ empty; absolute ⇒ absolute; not filepath.IsLocal ⇒ traversal; ".", ".choix" or anything under ".choix" ⇒ reserved
  func EffectiveDir(st *store.Store, configured string) (dir string, others []string, err error) // the most used ExportDir if any picks are exported, else configured; others = the remaining export dirs
  func CheckDir(st *store.Store, dir string) error // ErrDirHasOriginals when Files().CountUnderPath(dir) > 0 — one SQL query, no directory walk. Files on disk that the database does not know never block
  type State string // "", "picked", "rejected"
  type Result struct{ State State; ExportedPath string }
  type ReconcileReport struct{ Adopted, Missing, Orphans int }
  func New(s *store.Store, scanRoot, picksDir string) *Service
  func (s *Service) Set(ctx context.Context, fileID int64, target State) (Result, error)
  func (s *Service) Reconcile(ctx context.Context) (ReconcileReport, error)
  func (s *Service) Pick(fileID int64) error   // Pick, Unpick, Reject, Unreject stay as wrappers over Set until Task 62
  ```
- `Durable`, exactly: `conn, _ := db.Conn(ctx)` (the store's only connection, so nothing else runs meanwhile); `PRAGMA synchronous=FULL`, `PRAGMA fullfsync=1`; `tx, _ := conn.BeginTx(ctx, nil)` (a `*sql.Tx` — there is no hand-written `BEGIN`); `fn(tx)`; commit or roll back. Then, with `context.WithoutCancel(ctx)`, restore `synchronous=NORMAL` and `fullfsync=0`; if restoring fails the connection is discarded (`conn.Raw` returning `driver.ErrBadConn`) so the pool opens a fresh one. For that fresh connection to be configured, `store.Open` carries its four pragmas in the URI it builds (`FileURI`, below) instead of executing them once after opening. Result: an error from `fn` or from the commit ⇒ that error (nothing was committed); a successful commit ⇒ `nil`, **even if the clean-up failed** (logged) — callers may delete an export only after a non-nil result.
- **Which directory, and when it is refused.** `EffectiveDir` picks it (C12). `CheckDir` refuses it only when the database has scanned originals under it — e.g. the user pointed `picks_dir` at a folder of photos. An orphan copy (left by a failed removal, a crash between publish and commit, or dropped in by the user) is not in `files` and never blocks. While a directory is refused, nothing is exported and the scanner keeps treating it as originals; otherwise the scanner **always** skips the effective directory (and the other legacy export directories), so exported copies are never walked.
- **The database is opened by path, safely (found in review; v1 has the same bug at `internal/cli/serve.go:149`).** `modernc.org/sqlite` passes a name that starts with `file:` whole to `sqlite3_open_v2` with `SQLITE_OPEN_URI` (`conn.go`), so in a folder path `#` starts a fragment, `?` a query and `%41` is decoded: a folder called `Day #2` would make SQLite create a database named `Day ` **beside** the folder — a write outside the scan root. `store.Open(path)` therefore builds the URI itself with `FileURI(path, "journal_mode(WAL)", "foreign_keys(on)", "busy_timeout(10000)", "synchronous(NORMAL)")`; no caller ever concatenates a DSN again. The path SQLite opens is, byte for byte, the path the caller checked.
- `Reconcile` (at session open; **R24: it never copies anything**): a `picked` row without `exported_path` whose expected target already holds identical content gets the path recorded (`Adopted`); a `picked` row whose recorded copy is not on disk is counted (`Missing`) and left as it is — the user may have moved their picks away on purpose, and v1 never copied them back; media files under the directory that no row refers to are counted (`Orphans`). Nothing is exported, nothing is deleted, and the call costs one `Lstat` per picked row, so opening a session never waits on copies. The counts are logged once and `Missing` is shown to the frontend (`FolderView.picks_missing`, Task 36); there is no automatic repair.
- `Set`, exact order (an unexported `trace func(step string)` records it for the tests):
  - **to `picked`:** open the source `O_RDONLY`; `safefs.OpenDir(root, <dir>/<Dir(file.path)>, true)`; the target name is the file's base name, with `_2`, `_3`… on a different-content collision and reuse on identical content (as v1); `CreateTemp` → copy → `Publish(durable=true)` (`copy`, `file-sync`, `rename`, `dir-sync`) — exclusive, so a file that appeared at the target after the check is never overwritten: on `safefs.ErrExists` the target is chosen again (reuse if identical, else the next suffix; at most 100 tries); then `Store.Durable` writing `state=picked, exported_path` (`tx-commit`). If `Durable` returns an error, a copy this call published is removed.
  - **from `picked` to `rejected` or `""`:** `Store.Durable` first (`tx-commit`), then `Dir.Remove` of the copy and `Dir.Sync` (`remove`, `dir-sync`); a failed removal is logged and leaves an orphan copy, never a lost state. The copy's directory is resolved from the stored `exported_path` through `safefs`, so a pick exported under an older directory is still removed.
  - **`rejected` ↔ `""`:** `Store.Durable` only.
  - `ValidateDir` and `CheckDir` run before a pick; either failing returns the error and changes nothing.
- `Service` is not safe for concurrent use; the catalog serialises per folder (Task 36).

- [ ] **Step 1: Write the failing tests.**
  - `durable_test.go`: `TestDurable_RunsWithFullSync` (inside `fn`, `PRAGMA synchronous` reads 2 and `PRAGMA fullfsync` reads 1 through the `tx`; after it, 1 and 0); `TestDurable_RestoresAfterError` (fn returns an error ⇒ rolled back, pragmas restored, the error returned); `TestDurable_RestoresWhenContextIsCancelledInsideFn` (fn cancels the context and returns its error ⇒ afterwards `PRAGMA synchronous` reads 1); `TestDurable_CleanupFailureAfterCommitReturnsNil` (an injected failing restore ⇒ `nil`, the row is committed, and the next statement runs on a fresh connection that has `busy_timeout` 10000 and `foreign_keys` on); `TestDurable_SerialisesWithOtherWriters` (20 goroutines mixing `Durable` and plain `Files().UpdateStatus` finish without `database is locked`); `TestOpen_PragmasComeFromDSN` (a second connection forced by discarding the first has all four); `TestExportDirs_Derives` (`picks/a/b.jpg` for `a/b.jpg` ⇒ `picks`; `exports/out/c.jpg` for `c.jpg` ⇒ `exports/out`; `/Users/tester/out/a/b.jpg` for `a/b.jpg` ⇒ `Users/tester/out`; `picks/a/b_2.jpg` for `a/b.jpg` ⇒ `picks`); `TestCountUnderPath` (`shoot1/a.jpg`, `shoot1/x/b.jpg`, a missing `shoot1/c.jpg`, `shoot10/d.jpg` ⇒ 2 for `shoot1`).
  - `picks_test.go`: `TestSet_PickOrder` (trace is exactly `copy, file-sync, rename, dir-sync, tx-commit`). `TestSet_UnpickOrder` (`tx-commit, remove, dir-sync`). `TestSet_PickTxFailureRemovesCopy` (a closed store ⇒ error, no file under `picks/`). `TestSet_SourceOpenedReadOnly` (source mode 0400 in a 0500 directory succeeds; bytes unchanged). `TestSet_SymlinkedPicksDirRefused` (Review Focus 5: `picks -> .` ⇒ `safefs.ErrSymlink`, state unchanged, no new file anywhere). `TestSet_PlantedTmpSymlinkDoesNotTruncateOriginal` (v1's fixed name `picks/a.jpg.tmp -> ../a.jpg` planted ⇒ pick succeeds, `a.jpg` byte-identical). `TestSet_CollisionSuffix` and `TestSet_IdempotentOnSameContent` (v1's cases). `TestSet_CollisionArrivingAfterCheckIsNotOverwritten` (a hook creates `picks/a.jpg` with other bytes after the target was chosen ⇒ the pick lands in `picks/a_2.jpg`; the late file is byte-identical). `TestSet_SyncNotSupportedStillPicks` and `TestSet_SyncIOErrorFailsPick` (injected through `safefs`; on `EIO` the state is unchanged and no copy remains). `TestSet_UnpickUnderLegacyDir` (Review Focus 4: row exported to `exports/out/a.jpg`, service constructed with `picks` ⇒ unpick removes that file).
  - `dir_test.go`: `TestValidateDir` (`""` empty; `/x` absolute; `../x`, `a/../../b` traversal; `.`, `.choix`, `.choix/x` reserved; `picks`, `exports/luminar` accepted). `TestCheckDir_ScannedOriginalsBlock` (`shoot1/` with two rows in `files` ⇒ `ErrDirHasOriginals`). `TestCheckDir_UnknownFilesDoNotBlock` (a picks dir holding an orphan copy and a stray JPEG, neither in `files` ⇒ nil; a pick then succeeds). `TestCheckDir_MissingRowsDoNotBlock`. `TestEffectiveDir_LegacyWins` (rows exported under `exports/out`, configured `picks` ⇒ `exports/out`). `TestEffectiveDir_NoExportsUsesConfigured`.
  - `reconcile_test.go`: `TestReconcile_NeverReexports` (a picked row whose copy was deleted ⇒ `Missing == 1`; the picks directory is still empty afterwards; the row is still `picked` with its `exported_path`); `TestReconcile_AdoptsIdenticalCopy`; `TestReconcile_CountsOrphansAndDeletesNothing` (an orphan copy and a stray file ⇒ `Orphans == 2`, both still on disk); `TestReconcile_OpensNoOriginal` (the originals' directory is mode 0000 ⇒ still succeeds: it only looks at the picks directory).
  - `open_test.go`: `TestFileURI_Escapes` (`/t/a #1?b%41/.choix/state.db` ⇒ `file:/t/a %231%3Fb%2541/.choix/state.db?_pragma=…`; a plain path is unchanged). `TestOpen_FolderNameWithURICharacters` (create `<tmp>/a #1?b%41/.choix/`, `Open(<that>/state.db)`: the file `state.db` exists inside that `.choix`; `<tmp>` contains exactly one entry — nothing named `a ` or `a #1` appeared beside the folder; a row written through the store is read back after reopening). `TestOpen_PathWithSpacesAndUnicode`. `TestNoHandBuiltDSN` (in `internal/notools_test.go`'s style: no non-test `.go` file outside `internal/store` contains the string literal `"file:"`).
- [ ] **Step 2: Run** `go test ./internal/store/ ./internal/picks/` — expected: FAIL.
- [ ] **Step 3: Implement.** (`modernc.org/sqlite` v1.60.1 issues `F_FULLFSYNC` on darwin when `fullfsync` is on; the readback proves the pragma is set, spike S1(f) records what the volume does with it.)
- [ ] **Step 4: Run** `make test && make lint` — expected: clean (the v1 server still calls `Pick`/`Unpick`/`Reject`/`Unreject`).
- [ ] **Step 5: Commit** `feat(picks): durable commits and symlink-resistant exports`.

### Task 18: Embedded-JPEG readers for RAF and DNG

**Gate:** spike S7. **Depends on:** Tasks 8, 9.

**Files:**
- Create: `internal/rawpreview/{rawpreview.go,raf.go,dng.go}`
- Test: `internal/rawpreview/{raf_test.go,dng_test.go}`

**Interfaces:**
- Produces:
  ```go
  var ErrNoPreview = errors.New("rawpreview: no embedded JPEG")
  const MaxPreviewBytes = 64 << 20
  type Preview struct{ JPEG []byte; Orientation, Width, Height int } // Orientation: 0 for RAF (the JPEG carries its own); IFD0's value for DNG. Width/Height: read from the returned JPEG's own header (image.DecodeConfig)
  func Kind(path string) string                // "raf", "dng" or "" by extension, case-insensitive
  func Embedded(path string, try func(Preview) error) (Preview, error)
  // file opened O_RDONLY. Offers the candidates in descending width and returns the first for which try returns nil. A context error from try stops the search and is returned; any other error moves on to the next candidate. try == nil accepts the first candidate whose header parses. ErrNoPreview when none is accepted
  func Usable(ctx context.Context, stills platform.Stills) func(Preview) error
  // the standard try: a real decode — stills.Thumbnail(ctx, ImageInput{Data: p.JPEG, Orientation: p.Orientation}, 64, platform.FitWidth). A header that parses proves nothing about the scan data
  ```
  RAF: magic `FUJIFILMCCD-RAW ` (16 bytes); JPEG offset = big-endian `uint32` at byte 84, length at byte 88. DNG: TIFF header (`II*\0` or `MM\0*`); visit IFD0, every IFD chained from it through the next-IFD offset, and each IFD listed in the `SubIFDs` tag (0x014A) of any of those — at most 64 IFDs, at most 4096 entries each, never the same offset twice. A **candidate** has `NewSubFileType` (0x00FE) bit 0 set, `Compression` (0x0103) 7 or 6, `PhotometricInterpretation` (0x0106) 6 or 2 — never 32803 or 34892 — and either one strip (`StripOffsets`/`StripByteCounts`) or `JPEGInterchangeFormat`/`…Length`. Candidates are **offered in descending `ImageWidth`** (ties by byte count). One is offered only when its bytes are in range, start with `FF D8` and pass `image.DecodeConfig`; whether it is *usable* is decided by `try`, which callers make an actual decode — so a preview with a valid header and damaged scan data is passed over for the next smaller one **before** any RAW decode is considered (spec §7: RAW only "for a file with no usable embedded preview"). RAF has a single candidate. `Orientation` is IFD0's tag 0x0112 (1 when absent). Both formats: a candidate is out of range when offset + length exceeds the file size or length is < 4 or > `MaxPreviewBytes`.

- [ ] **Step 1: Write the failing tests.** `TestRAF_ReturnsEmbeddedJPEG` (bytes equal, `Orientation == 0`). `TestRAF_BadMagic`, `TestRAF_OffsetPastEOF` (length 0xFFFFFFF0), `TestRAF_Truncated` (cut at byte 90), `TestRAF_ZeroBytes`, `TestRAF_NotJPEGAtOffset` — each `errors.Is(err, ErrNoPreview)`. `TestDNG_PicksWidestPreview` (`MakeDNG` with previews 256 and 1600 wide ⇒ the 1600 one; `Width == 1600` and `Height` from the JPEG itself, not from the IFD tags). `TestDNG_SkipsWidestPreviewWithGarbageHeader` (the 1600 preview's bytes are `FF D8` followed by garbage ⇒ it is never offered; the 256 one is returned, also with `try == nil`). `TestDNG_HeaderValidBodyDamaged` (`MakeDNG` with the 1600 preview's scan data cut to 16 bytes after an intact SOI/SOF/SOS header: with `try == nil` the 1600 one is returned — the header parses; with a `try` that decodes fully with `image/jpeg` the 256 one is returned, and `Width == 256`). `TestEmbedded_TryContextErrorStopsSearch` (`try` returns `context.Canceled` for the first candidate ⇒ that error, the second is never offered). `TestEmbedded_AllCandidatesRejected` (`ErrNoPreview`). `TestDNG_PreviewInChainedIFD` (a preview reachable only through IFD0's next-IFD offset is found). `TestDNG_NeverPicksRawStrip` (a 5 MB lossless-JPEG `RawStrip` that starts with `FF D8` and a 200 KB preview ⇒ the preview). `TestDNG_OrientationFromIFD0` (`Orientation: 8` ⇒ 8). `TestDNG_NoPreview` (`ErrNoPreview`). `TestDNG_BigEndian` (hand-built `MM` header). `TestDNG_IFDLoop` (a SubIFD pointing at IFD0 ⇒ returns; `-timeout 5s`). `TestDNG_OversizePreviewRefused` (declared count 65 MB ⇒ `ErrNoPreview`, nothing allocated). `TestEmbedded_OpensReadOnly`. `TestKind`.
- [ ] **Step 2: Run** `go test ./internal/rawpreview/` — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/rawpreview/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(rawpreview): embedded JPEG readers for RAF and DNG`.

### Task 19: Native metadata through the one parser

**Gate:** spike S4. **Depends on:** Tasks 7, 9, 13, 14, 15, 18 (15 edits `internal/cli/pipeline_build.go` first; Tasks 19 → 20 → 21 → 22 edit the same v1 files and run in that order).

**Files:**
- Create: `internal/meta/{native.go,imageio_map.go,av_map.go,mvhd.go}`, `testdata/imageio/{fuji_raf_jpeg.json,iphone_heic.json,plain_jpeg.json,dng_container.json}`, `testdata/av/{iphone_mov.json,dji_mp4.json}` (the scrubbed, key-reduced dictionaries saved by Task 7), `internal/meta/fixtures_test.go`
- Modify: `internal/meta/meta.go` (`Extractor` takes a `Reader`), `internal/cli/pipeline_build.go` (`meta.NewExtractor(&meta.NativeReader{…}, st, scanRoot)`; `metaProcessorAdapter` unchanged)
- Test: `internal/meta/{imageio_map_test.go,av_map_test.go,mvhd_test.go,native_test.go,parity_test.go}`

**Interfaces:**
- Consumes: `platform.Stills`, `platform.Video`, `rawpreview.Embedded`, `rawpreview.Kind`.
- Produces:
  ```go
  type Reader interface { Read(ctx context.Context, absPath string) ([]byte, error) } // a JSON array with one exiftool-keyed object
  func NewExtractor(r Reader, s *store.Store, root string) *Extractor
  type NativeReader struct { Stills platform.Stills; Video platform.Video }
  type FileInfo struct { Name string; ModTime time.Time }
  type MovieHeader struct { Created, Modified time.Time }             // zero when the atom's value is 0
  func ReadMovieHeader(path string) (MovieHeader, error)              // moov → mvhd, version 0 and 1; seconds since 1904-01-01 UTC; atom sizes are bounded by the file size
  func FromImageIO(props map[string]any, fi FileInfo) map[string]any
  func FromAVAsset(props map[string]any, mh MovieHeader, fi FileInfo) map[string]any
  ```
- `NativeReader.Read`, by `rawpreview.Kind` and extension:
  - video (`.mov`, `.mp4`, `.m4v`) → `Video.Properties` + `ReadMovieHeader` → `FromAVAsset`;
  - **RAF** → `Stills.Properties(ImageInput{Data: embedded JPEG})` (the embedded JPEG carries the full EXIF and its pixel size is what v1 reported); on `ErrNoPreview` → `ImageInput{Path}`;
  - **DNG** → `Stills.Properties(ImageInput{Path})` — the container's own IFD0/ExifIFD hold make, model, serial, dates and GPS; the preview is a bare JPEG and is never used for those. The **dimensions** are then overridden with those of the preview that actually decodes — `rawpreview.Embedded(path, rawpreview.Usable(ctx, n.Stills))`, the same selection the thumbnail stage makes — emitted as `EXIF:ExifImageWidth` / `EXIF:ExifImageHeight`, which `meta.Parse` reads first; spec §7: "the reported width … is the embedded JPEG's" (**[call] C34**). With no usable preview the container's dimensions stay;
  - other stills → `ImageInput{Path}`.
  The record always has `SourceFile` and `File:FileModifyDate`.
- Key table (emit only when the source value is present and non-empty; numbers stay numbers):

  | exiftool key | source |
  |---|---|
  | `EXIF:Make`, `EXIF:Model` | `{TIFF}.Make`, `{TIFF}.Model` |
  | `EXIF:SerialNumber`, `MakerNotes:SerialNumber`, `MakerNotes:InternalSerialNumber` | the per-maker table recorded by spike S4 (starting point: `{Exif}.BodySerialNumber` → `EXIF:SerialNumber`; `{ExifAux}.SerialNumber` → `MakerNotes:SerialNumber`) |
  | `EXIF:ExifImageWidth`, `EXIF:ExifImageHeight` | `{Exif}.PixelXDimension`, `{Exif}.PixelYDimension` |
  | `File:ImageWidth`, `File:ImageHeight` | `PixelWidth`, `PixelHeight` |
  | `EXIF:ISO` | first element of `{Exif}.ISOSpeedRatings` |
  | `EXIF:FNumber`, `EXIF:ExposureTime`, `EXIF:FocalLength` | same names in `{Exif}` |
  | `EXIF:DateTimeOriginal`, `EXIF:CreateDate`, `EXIF:ModifyDate` | `{Exif}.DateTimeOriginal`, `{Exif}.DateTimeDigitized`, `{TIFF}.DateTime` |
  | `EXIF:GPSLatitude`, `EXIF:GPSLatitudeRef`, `EXIF:GPSLongitude`, `EXIF:GPSLongitudeRef` | `{GPS}.Latitude`, `LatitudeRef`, `Longitude`, `LongitudeRef` |
  | `File:FileModifyDate` | `fi.ModTime` as `2006:01:02 15:04:05-07:00` |
  | `QuickTime:Make`, `QuickTime:Model`, `QuickTime:Encoder` | `make`, `model`, `encoder` (else `software`) |
  | `QuickTime:CreateDate`, `QuickTime:ModifyDate` | `mh.Created`, `mh.Modified` in UTC as `2006:01:02 15:04:05` (**[call] C10**) |
  | `QuickTime:ImageWidth`, `QuickTime:ImageHeight` | `width`, `height` |
  | `Composite:GPSLatitude`, `Composite:GPSLongitude` | signed decimals parsed from the ISO 6709 `location` string |

- `fixtures_test.go` — `TestFixtures_OnlyAllowListedKeys`: every committed file under `testdata/imageio` and `testdata/av` contains only the source keys named in the table (plus `PixelWidth`/`PixelHeight`, `duration`), every serial is `TESTSERIAL01`, every date is `2026:01:02 03:04:05`, every latitude/longitude is the fixed pair (10.5, 20.25), and no string longer than 64 characters appears (maker-note blobs). Test expectations read make and model from the fixture instead of spelling them.

- [ ] **Step 1: Write the failing tests.**
  - `imageio_map_test.go`: `TestFromImageIO_FujiDeviceKey` — `meta.Parse` of the record from `fuji_raf_jpeg.json` gives `DeviceKey == <fixture Make> + " " + <fixture Model> + "#TESTSERIAL01"`, `DateTimeOriginal == 2026-01-02T03:04:05Z`, `Width`/`Height` equal to the fixture's `PixelXDimension`/`PixelYDimension`. `TestFromImageIO_DNGContainer` (`dng_container.json` ⇒ a device key ending `#TESTSERIAL01`, `HasCaptured` from `DateTimeOriginal`, not from the file time). `TestFromImageIO_GPSSouthWest`. `TestFromImageIO_NoExifFallsBackToMtime` (`plain_jpeg.json` ⇒ `Captured` equals `fi.ModTime.Unix()`, `DeviceKey == "Unknown"`). `TestFromImageIO_OmitsEmptyValues`.
  - `av_map_test.go`: `TestFromAVAsset_Location` (`"+10.5000+020.2500+010.000/"` ⇒ `HasGPS`, 10.5, 20.25; `"-10.5000-020.2500/"` ⇒ −10.5, −20.25). `TestFromAVAsset_EncoderFallback` (no make/model, `encoder: "ACME D100"` ⇒ `DeviceKey == "ACME D100#"`). `TestFromAVAsset_ZeroHeaderUsesMtime`.
  - `mvhd_test.go`: `TestReadMovieHeader_Clip` (`testdata/media/clip.mov` ⇒ `Created == 2026-01-02T03:04:05Z`). `TestReadMovieHeader_Version1`. `TestReadMovieHeader_NoMoov` (error). `TestReadMovieHeader_HugeAtomSize` (declared size past EOF ⇒ error, no allocation of that size).
  - `native_test.go` (fakes): `TestNativeReader_RAFUsesEmbeddedJPEG` (the fake `Stills` receives `ImageInput.Data` equal to the JPEG inside `MakeRAF`). `TestNativeReader_DNGUsesContainer` (the fake receives `ImageInput.Path`, never `Data`). `TestNativeReader_DNGDimensionsFromPreview` (container props say 6000×4000, `MakeDNG` preview is 1600×1067 ⇒ `Parse` gives 1600×1067; with no preview ⇒ 6000×4000). `TestNativeReader_DNGDimensionsFromThePreviewThatDecodes` (previews 1600 wide with a damaged body and 256×171 intact; the fake `Stills` fails on the damaged bytes ⇒ `Parse` gives 256×171). `TestNativeReader_VideoByExtension`. `TestExtractor_StoresGzippedRecord` (`raw_exif` gunzips to a JSON array whose first object has `SourceFile`; `scan_status == "metadata"`). `TestExtractor_UnreadableFileIsFailed`. `TestExtractor_CancelLeavesRowUntouched` (`context.Canceled` from the reader ⇒ status still `discovered`, not `failed`). `TestBackfillDeviceKey_OnNativeRow` (Review Focus 4).
  - `parity_test.go` (`//go:build darwin`; skips without exiftool or `CHOIX_UAT_ROOT`): `TestParity_Corpus` — for up to 40 files per extension (sorted, evenly spaced; `-parity.all` checks every file), opened read-only in place, run `exiftool -j -G -n` and `NativeReader.Read` with the real platform, `Parse` both, and compare `DeviceKey`, `Captured`, `Width`, `Height`, `ISO`, `Aperture`, `Shutter`, `FocalLength`, `HasGPS`, GPS (±1e-6). Every difference is reported as `<extension> #<n> (<make> <model>): field: differs` — values and paths are printed only with `-parity.verbose`, for the owner's terminal, and are never pasted into a committed file; the test fails when there is any difference. DNG `Width`/`Height` are compared against the preview's size, not exiftool's number (C34).
- [ ] **Step 2: Run** `go test ./internal/meta/` — expected: FAIL (undefined `FromImageIO`, `NativeReader`).
- [ ] **Step 3: Implement**; commit the fixtures from Task 7; update the v1 call site.
- [ ] **Step 4: Run** `go test -race ./internal/meta/` (PASS), then `go test ./internal/meta/ -run TestParity -v` on the owner's machine (with the owner's photo folder in `$CHOIX_UAT_ROOT`) — expected: `PASS` with zero differences. A difference is fixed in the mapper, or recorded in the spike results file as an accepted exception (by format, make and model only) with the owner's agreement.
- [ ] **Step 5: Commit** `feat(meta): native metadata with exiftool-compatible records`.

### Task 20: Thumbnails on the platform layer

**Gate:** spike S7. **Depends on:** Tasks 8, 13, 14, 16, 17, 18, 19 (17 edits `internal/cli/serve.go` before this task does).

**Files:**
- Create: `internal/thumb/cache.go`
- Modify: `internal/thumb/{builder.go,photo.go,video.go}` (rewritten), `internal/cli/pipeline_build.go` (`thumb.Builder{ScanRoot, Store, Stills, Video}`), `internal/server/full.go` (`ensureFullJPEG` calls `thumb.Photo` then `thumb.WriteCache`), `internal/server/server.go` (`Config.Exiftool`, `Config.Ffmpeg` replaced by `Stills platform.Stills`), `internal/cli/serve.go` (pass the real `Stills`; delete `resolveMediaTools`)
- Delete: `internal/thumb/sips.go`
- Test: `internal/thumb/{photo_test.go,video_test.go,builder_test.go,cache_test.go}` (replace the v1 tests)

**Interfaces:**
- Consumes: `platform.Stills`, `platform.Video`, `rawpreview`, `safefs`.
- Produces:
  ```go
  type Builder struct { ScanRoot string; Store *store.Store; Stills platform.Stills; Video platform.Video }
  func (b *Builder) Build(ctx context.Context, fileID int64) error   // thumbs rows + scan_status 'thumbed' | 'failed'; a cancelled context returns ctx.Err() and leaves the row's status unchanged
  func Photo(ctx context.Context, stills platform.Stills, srcAbs string, limit int) (platform.Image, error) // limit is the tier number; how it is applied follows the source kind, below
  func WriteCache(root string, fileID int64, tier string, jpeg []byte) (rel string, err error)      // safefs (replacing publication) into .choix/thumbs/<bucket>/, name <id>-<tier>.jpg; not durable
  func WriteKeyframe(root string, fileID int64, n int, jpeg []byte) (rel string, err error)          // .choix/thumbs/<bucket>/<id>-keyframes/<n>.jpg
  func SweepTemps(root string) (int, error)                                                          // removes leftover *.tmp under .choix/thumbs through safefs
  var KeyframeFractions = []float64{0, 0.25, 0.5, 0.75, 0.95}
  ```
  Sizing is v1's, per source kind (**[call] C8**, R21):

  | Source | v1 path | Call |
  |---|---|---|
  | RAF, DNG with an embedded JPEG | exiftool preview → ffmpeg `scale=W:-1` | `rawpreview.Embedded(src, try)` where `try` is `Stills.Thumbnail(ImageInput{Data, Orientation}, limit, platform.FitWidth)` and keeps its result: every candidate is tried, widest first, before the last-resort row below |
  | every other still (JPEG, HEIC, TIFF, PNG) | `sips -Z W` | `Stills.Thumbnail(ImageInput{Path}, limit, platform.FitLongSide)` |
  | RAW none of whose embedded previews decodes (last resort) | `sips -Z W` on the RAW | `Stills.Thumbnail(ImageInput{Path}, limit, platform.FitLongSide)` with one `slog.Warn("raw decode fallback", "file", …)` |
  | video keyframes | five frames at the fractions below, full size | `Video.Frames(src, KeyframeFractions)`, each written with `WriteKeyframe` — unchanged |
  | video `thumb` and `preview` rows (**[call] C9**, new) | v1: `thumb` was a copy of frame 1 | `Stills.Thumbnail(ImageInput{Data: frame 1}, 256 / 1600, platform.FitWidth)`; both rows upserted | `TierThumb`, `TierPreview`, `TierFullJPEG`, `WidthThumb` 256, `WidthPreview` 1600, `WidthFullJPEG` 4096, `CachePath`, `KeyframesDir` are unchanged.

- [ ] **Step 1: Write the failing tests** (fakes from `platformtest`, `store.NewTestStore`). `TestBuild_PhotoWritesBothTiers` (rows `thumb` and `preview` with `rel_path` `.choix/thumbs/<%02x>/<id>-thumb.jpg` / `-preview.jpg`, files exist, status `thumbed`). One sizing test per source kind: `TestBuild_JPEGScalesByLongSide` (the fake saw `Path` with `(256, FitLongSide)` and `(1600, FitLongSide)`; a 500×1000 source yields a 128×256 thumb); `TestBuild_HEICScalesByLongSide`; `TestBuild_RAFScalesByWidth` (the fake saw `Data` with `(256, FitWidth)` and `(1600, FitWidth)`; a 500×1000 embedded JPEG yields a 256×512 thumb); `TestBuild_DNGScalesByWidth`; `TestBuild_RAWDecodeFallbackScalesByLongSide`; `TestBuild_VideoTiersScaleByWidth`. `TestBuild_RAFUsesEmbeddedJPEG` (the fake saw `Data`, never `Path`). `TestBuild_DNGPassesIFD0Orientation` (`MakeDNG{Orientation: 6}` ⇒ the fake saw `Orientation == 6`). `TestBuild_DNGWithoutPreviewFallsBackToDecode` (the fake saw `Path`; status `thumbed`). `TestBuild_PreviewThumbnailFailureFallsBackToPath` (a RAF whose single embedded JPEG the fake fails for `Data` ⇒ `Path` is used, with the warning). `TestBuild_DNGDamagedWidestPreviewUsesNextPreview` (`MakeDNG` with a 1600-wide preview whose body is damaged and an intact 256-wide one ⇒ both tiers are built from the 256-wide preview through `Data`; the fake never saw `Path`; no fallback warning). `TestBuild_DNGAllPreviewsDamagedFallsBackToDecode` (only then `Path`). `TestBuild_VideoWritesKeyframesAndTiers` (`1.jpg…5.jpg`; rows `thumb` and `preview`). `TestBuild_VideoWithoutTrackIsFailed` (error contains `no video track`). `TestBuild_ZeroByteJPEGIsFailed`. `TestBuild_TruncatedRAFIsFailed` (falls through to the decode path, which the fake fails). `TestBuild_MissingSourceIsFailed` (error contains `stat source`). `TestBuild_CancelDuringNativeCall` (the fake's `Gate` holds the call; cancel ⇒ `Build` returns `context.Canceled` within 1 s; status still `metadata`; no file was written, also after the gate is released). `TestBuild_SymlinkedCacheDirRefused` (Review Focus 5: `.choix/thumbs -> ..` ⇒ status `failed` with `symbolic link`; nothing written outside `.choix`). `TestBuild_NeverOpensSourceForWrite` (source directory mode 0500). `TestWriteCache_PlantedTmpSymlink` (original untouched). `TestSweepTemps`. `TestPhoto_Full4096FollowsSourceKind` (HEIC ⇒ `(4096, FitLongSide)`; RAF ⇒ `(4096, FitWidth)` on the embedded JPEG).
- [ ] **Step 2: Run** `go test ./internal/thumb/` — expected: FAIL.
- [ ] **Step 3: Implement**; update the v1 call sites and their tests.
- [ ] **Step 4: Run** `make test && make lint`, then smoke the v1 binary against a clone of a corpus folder chosen by rule (the smallest folder under the owner's photo folder (`$CHOIX_UAT_ROOT`) with at least 50 RAF files): `make build && cp -cR "$SRC" "$TMPDIR/folder-1" && ./choix "$TMPDIR/folder-1" --no-open`, wait for the log line `pipeline run done`, then Ctrl-C — expected: tests clean; no `stage failed` lines; `find "$TMPDIR/folder-1/.choix/thumbs" -name '*-preview.jpg' | wc -l` equals `find "$TMPDIR/folder-1" -iname '*.raf' -o -iname '*.jpg' -o -iname '*.heic' -o -iname '*.dng' | grep -v '/.choix/' | wc -l`.
- [ ] **Step 5: Commit** `feat(thumb): build thumbnails with ImageIO and AVFoundation`.

### Task 21: Remove external tools and the wizard

**Gate:** none. **Depends on:** Tasks 19, 20.

**Files:**
- Delete: `internal/deps/`, `internal/firstrun/`, `internal/meta/exiftool.go` (+ test), `internal/server/setup.go` (+ test)
- Modify: `internal/ai/local/install.go` (remove `Install`, `send`, the `deps` import, and the method `func (i *Installer) Has(ctx, kind) (bool, error)` — with it gone, the embedded `*ModelStore`'s `Has(kind ModelKind) bool` is promoted to `*Installer`, which is what Task 25's `Downloader` needs), `internal/server/{server.go,router.go}` (remove `detector`, `ready`, the `/setup` and `/api/setup/*` routes and the redirect in `serveIndex`), `internal/cli/serve.go`
- Test: `internal/notools_test.go` (package `internal_test`), `internal/ai/local/install_test.go` (add `TestInstaller_HasIsModelStoreHas`: `var _ interface{ Has(ModelKind) bool } = (*Installer)(nil)`)

**Interfaces:**
- Produces: `local.Installer.Download(ctx, kind, onProgress)` and `local.Installer.Has(kind) bool` are the only download API left.

- [ ] **Step 1: Write the failing test** `TestNoExternalTools`: walk every non-test `.go` file under `cmd/` and `internal/` (files named `*_test.go` and files with a `//go:build ignore` line are skipped); fail on an import of `os/exec`, or on a string literal equal to `exiftool`, `ffmpeg`, `ffprobe`, `sips` or `open`. `internal/server/browser.go` is the single allowed `os/exec` import until Task 62 (listed in an `allow` slice with that comment).
- [ ] **Step 2: Run** `go test ./internal/ -run TestNoExternalTools` — expected: FAIL listing the files above.
- [ ] **Step 3: Delete and update.**
- [ ] **Step 4: Run** `make test && make lint && go mod tidy` — expected: clean; commit any `go.mod` change.
- [ ] **Step 5: Commit** `chore: remove exiftool, ffmpeg, sips and the first-run wizard`.

### Task 22: ONNX Runtime — pinned library, bundle lookup, shared sessions

**Gate:** spikes S5, S6. **Depends on:** Tasks 2, 3, 15, 21 (it edits `internal/cli/serve.go` and `pipeline_build.go` after Task 21 has).

**Files:**
- Create: `build/onnxruntime/{pin.env,fetch.sh}`, `internal/ai/local/{runtime.go,runtime_dev.go,runtime_ship.go,sessions.go}`
- Modify: `internal/ai/local/{ort.go,local.go,quality.go}`, `internal/cli/{serve.go,pipeline_build.go}` (construct one `local.Sessions`, pass it to both, close it last), `.gitignore` (already ignores `build/.tools/`)
- Test: `internal/ai/local/{runtime_test.go,sessions_test.go}`, `build/onnxruntime/fetch_test.sh`

**Interfaces:**
- `pin.env`: `ORT_VERSION=1.25.0`, `ORT_URL=https://github.com/microsoft/onnxruntime/releases/download/v1.25.0/onnxruntime-osx-arm64-1.25.0.tgz`, `ORT_SHA256=<the digest recorded in Task 2>`. `fetch.sh`: downloads to `build/.tools/onnxruntime/` unless `lib/libonnxruntime.dylib` exists and `build/.tools/onnxruntime/.sha256` equals the pin; verifies the archive's SHA-256 before unpacking (mismatch: delete, exit 1); copies the versioned library to `lib/libonnxruntime.dylib`. `ORT_URL` may be overridden by the environment (the test uses a `file://` URL).
- Produces:
  ```go
  func RuntimeLibraryCandidates(exe string) []string
  // always first: <dir(exe)>/../Frameworks/libonnxruntime.dylib
  // runtime_dev.go (//go:build !production) adds: $CHOIX_ONNXRUNTIME_DYLIB, $HOMEBREW_PREFIX/opt/onnxruntime/lib/libonnxruntime.dylib, /opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib
  // runtime_ship.go (//go:build production) adds nothing ([call] C33)
  var ErrModelMissing = errors.New("model not installed")
  type SessionOptions struct{ CPUOnly map[ModelKind]bool }
  type Sessions struct{ /* one mutex: one inference at a time, both models */ }
  func NewSessions(models *ModelStore, o SessionOptions) *Sessions
  func (s *Sessions) Embed(img image.Image) ([]float32, error)       // ErrModelMissing when the CLIP file is absent
  func (s *Sessions) QualityCrop(input []float32) (float64, error)   // wraps ErrQualityUnavailable
  func (s *Sessions) Provider(kind ModelKind) string                  // "coreml", "cpu" or "" before the session exists
  func (s *Sessions) Close() error                                    // waits for the in-flight inference; later calls return an error
  type Embedder interface{ Embed(img image.Image) ([]float32, error) }
  type CropInferer interface{ QualityCrop(input []float32) (float64, error) }
  func NewAnalyzer(s *store.Store, e Embedder) *Analyzer             // e == nil: no embedding is computed
  func NewQualityScorer(s *store.Store, inf CropInferer, previewPath func(int64) (string, error)) *QualityScorer
  ```
  `Analyzer.Close` and `QualityScorer.Close` are removed. `NewSession(modelPath, cpuOnly bool)`: unless `cpuOnly`, `AppendExecutionProviderCoreML(0)` — its error is no longer ignored: it is logged (`slog.Warn`) and the session falls back to CPU, recorded in `Provider`. An `Embedder` error of any kind, `ErrModelMissing` included, leaves the embedding NULL and the file `analyzed` (as v1).

- [ ] **Step 1: Write the failing tests.** `TestRuntimeLibraryCandidates_BundleFirst` (in `runtime_dev_test.go`, `//go:build !production`; `exe = /x/Choix.app/Contents/MacOS/Choix` ⇒ first element `/x/Choix.app/Contents/Frameworks/libonnxruntime.dylib`; with `CHOIX_ONNXRUNTIME_DYLIB=/d/l.dylib` it is second; no `/usr/local` entry; no path under the user's home). `runtime_ship_test.go` (`//go:build production`): `TestRuntimeLibraryCandidates_ShippingHasBundleOnly` (exactly one element even with the variable set). `TestSessions_EmbedWithoutModel` (`errors.Is(err, ErrModelMissing)`). `TestSessions_CloseThenEmbedErrors`. `TestSessions_Serialised` (fake inference functions injected through unexported fields record overlap; 8 goroutines mixing `Embed` and `QualityCrop` never overlap). `TestAnalyzer_NilEmbedderWritesNullEmbedding` (row exists, `clip_embedding` NULL, status `analyzed`). `TestAnalyzer_ModelMissingLeavesFileAnalyzed` (a non-nil embedder returning `ErrModelMissing` ⇒ status `analyzed`, embedding NULL, no `error` text). `TestSessions_RealCLIP` (skips without library or model: output length 512, L2 norm within 1e-3 of 1, `Provider(ModelCLIP)` non-empty). `fetch_test.sh`: with a local archive and a wrong `ORT_SHA256` the script exits 1 and leaves no `lib/`; with the right one it creates `lib/libonnxruntime.dylib`; a second run downloads nothing.
- [ ] **Step 2: Run** `go test ./internal/ai/local/ && sh build/onnxruntime/fetch_test.sh` — expected: FAIL.
- [ ] **Step 3: Implement**; update the v1 call sites.
- [ ] **Step 4: Run** `sh build/onnxruntime/fetch.sh && CHOIX_ONNXRUNTIME_DYLIB=$PWD/build/.tools/onnxruntime/lib/libonnxruntime.dylib make test && make lint && go test -tags production ./internal/ai/local/ -run ShippingHasBundleOnly` — expected: clean.
- [ ] **Step 5: Commit** `feat(ai): pinned ONNX Runtime, bundle lookup and shared sessions`.

### Task 23: Engine pieces the job needs

**Gate:** none. **Depends on:** nothing.

**Files:**
- Create: `internal/quality/pass.go`, `internal/store/library_stats.go`
- Modify: `internal/quality/worker.go` (the `Scorer` interface moves, unchanged, into `pass.go`; `worker.go` keeps using it), `internal/pipeline/pipeline.go`, `internal/group/group.go`, `internal/store/files.go`
- Test: `internal/quality/pass_test.go`, `internal/store/library_stats_test.go`, additions to `internal/pipeline/pipeline_test.go`, `internal/group/group_test.go`

**Interfaces:**
- Produces:
  ```go
  // quality/pass.go — owns the Scorer interface from now on; worker.go is deleted in Task 62
  type Scorer interface { CountPending() (int, error); ListPending(afterID int64, limit int) ([]store.QualityItem, error); Score(ctx context.Context, item store.QualityItem) error }
  type Pass struct { Scorer Scorer; IsUnavailable func(error) bool; OnProgress func(done, total int) }
  func (p *Pass) Run(ctx context.Context) (rated int, err error) // rates every pending photo; stops with the error after 3 consecutive non-photo failures or when IsUnavailable(err); ctx is checked before every photo
  // pipeline
  func (p *Pipeline) Pending(ctx context.Context) (int, error)    // files in 'discovered' + 'metadata' + 'thumbed'
  // group
  func (g *Grouper) SetUseEmbeddings(on bool)                     // off: every file is treated as having no embedding; default on
  // store
  func (r *FilesRepo) CountAnalyzedMissingEmbedding() (int, error)
  type LibraryStats struct { Photos, Videos, Failed int; FirstCaptured, LastCaptured sql.NullInt64 }
  func (r *FilesRepo) LibraryStats() (LibraryStats, error)        // rows with scan_status <> 'missing'; Failed counts scan_status = 'failed'
  type PreviewCandidate struct { FileID int64; Kind string }
  func (r *FilesRepo) PreviewCandidates() ([]PreviewCandidate, error) // non-missing, non-rejected files that have a 'thumb' row, ordered by captured_at (NULLs last), id
  ```
- A stage `Process` that returns a context error must not have marked the file `failed`: `pipeline.RunPoolWithProgress` callers already stop on cancellation; this task adds the test that pins it.

- [ ] **Step 1: Write the failing tests.** `TestPass_RatesAllPending` (5 items ⇒ `rated == 5`, `OnProgress` last call `(5, 5)`). `TestPass_StopsAfterThreeConsecutiveFailures`. `TestPass_UnavailableStopsImmediately`. `TestPass_CancelBetweenPhotos` (`rated == 2`, `context.Canceled`). `TestPipeline_Pending` (2 discovered + 1 metadata + 3 thumbed + 4 analyzed ⇒ 6). `TestPipeline_CancelDoesNotCountAsFailure` (a processor returning `context.Canceled` ⇒ `Run` returns it; progress `failed == 0`). `TestGrouper_WithoutEmbeddingsIsTimeOnly` (two files 5 s apart with cosine 0.1 land in one cluster when off, two when on with threshold 0.92). `TestLibraryStats` (3 photos, 1 video, 1 missing photo, 1 failed photo ⇒ `Photos 4, Videos 1, Failed 1`, first/last = min/max `captured_at`). `TestPreviewCandidates_SkipsRejectedAndThumbless`. `TestCountAnalyzedMissingEmbedding`.
- [ ] **Step 2: Run** the four packages — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `make test && make lint && go test -tags smoke ./internal/e2e/` — expected: clean (the `smoke`-tagged pipeline test still compiles and passes).
- [ ] **Step 5: Commit** `feat(engine): quality pass, pending count, time-only grouping, library stats`.

### Task 24: Events — names, payloads, coalescing

**Gate:** none. **Depends on:** nothing.

**Files:**
- Create: `internal/app/events/{events.go,coalescer.go}`
- Test: `internal/app/events/{events_test.go,coalescer_test.go}`

**Interfaces:**
- Produces (JSON tags are the frontend's field names; `Progress`, `ModelStatus`, `Quality` match `frontend/src/design/types.ts`):
  ```go
  const (
      LibraryChanged = "library:changed"; ScanProgress = "scan:progress"; ScanState = "scan:state"
      ModelsProgress = "models:progress"; QualityStatus = "quality:status"
      FolderChanged = "folder:changed";   MenuAction = "menu:action"
  )
  func Names() []string // the seven names, in that order
  type Progress struct { Stage string `json:"stage"`; Done int `json:"done"`; Total int `json:"total"`; Failed int `json:"failed"`; State string `json:"state"` } // design ScanProgress; stage discover|metadata|thumb|analyze|cluster; state running|stopped
  type ModelStatus struct { Kind string `json:"kind"`; State string `json:"state"`; Received int64 `json:"received"`; Size int64 `json:"size"`; Error string `json:"error,omitempty"` } // kind clip|quality; state disabled|queued|downloading|failed|ready
  type Quality struct { Enabled bool `json:"enabled"`; State string `json:"state"`; Active bool `json:"active"`; Received int64 `json:"received"`; Size int64 `json:"size"`; Done int `json:"done"`; Total int `json:"total"`; Rated int `json:"rated"`; Error string `json:"error,omitempty"` } // v1's QualityStatus, same state names
  type ScanProgressEvent struct { FolderID string `json:"folderId"`; Progress Progress `json:"progress"`; Phase string `json:"phase"` } // phase starting|running|done|failed
  type ScanStateEvent struct { FolderID string `json:"folderId"`; State string `json:"state"`; Error string `json:"error,omitempty"` }  // state ready|scanning|queued|offline|error
  type LibraryChangedEvent struct { Reason string `json:"reason"`; FolderID string `json:"folderId,omitempty"`; Name string `json:"name,omitempty"` } // reason added|removed|gone|offline|online|moved|stats
  type FolderChangedEvent struct { FolderID string `json:"folderId"`; Reason string `json:"reason"` } // reason clusters|ratings|settings
  type MenuActionEvent struct { ID string `json:"id"`; Data string `json:"data,omitempty"` }         // [call] C15; data: the folder a context-menu click came from

  type Emitter interface { Emit(name string, data any) }
  type Recorder struct{ /* test helper: records every Emit; Events(name) []any */ }
  type Coalescer struct{ /* … */ }
  func NewCoalescer(out Emitter, interval time.Duration) *Coalescer // interval 100 ms in production
  func (c *Coalescer) Emit(name, key string, data any)             // latest value per (name, key) wins; delivered on the next tick
  func (c *Coalescer) EmitNow(name, key string, data any)          // drops a pending value for (name, key), delivers immediately
  func (c *Coalescer) Flush()
  func (c *Coalescer) Run(ctx context.Context)                     // ticks until ctx is done, then flushes once
  func ProgressFromPipeline(p pipeline.Progress) (Progress, string) // State is "stopped" when Phase == "failed", else "running"; second value is the phase
  ```

- [ ] **Step 1: Write the failing tests.** `TestPayloadJSON_MatchesDesignTypes` (exact key sets: `Progress` ⇒ `stage,done,total,failed,state`; `ModelStatus` without error ⇒ `kind,state,received,size`; `ScanProgressEvent` ⇒ `folderId,progress,phase`). `TestNames_Seven`. `TestProgressFromPipeline` (`failed` ⇒ `stopped`). `TestCoalescer_LatestWins` (100 `Emit` calls for one key then `Flush` ⇒ one event with the last value). `TestCoalescer_KeysAreIndependent`. `TestCoalescer_EmitNowBypassesAndClearsPending`. `TestCoalescer_RateBound` (`Run` with 100 ms while one key is emitted every millisecond for 1 s ⇒ between 8 and 12 events). `TestCoalescer_FlushesOnStop`.
- [ ] **Step 2: Run** `go test ./internal/app/events/` — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/events/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(app): event names, payloads and coalescing`.

### Task 25: Model downloads (CLIP becomes automatic)

**Gate:** none. **Depends on:** Tasks 21, 24.

**Files:**
- Create: `internal/app/models/manager.go`
- Modify: `internal/ai/local/models.go` (`Model.Size int64`: the files' exact byte lengths, read once with `curl -sIL <URL> | grep -i '^content-length' | tail -1`)
- Test: `internal/app/models/manager_test.go`

**Interfaces:**
- Consumes: `events.Coalescer`, `events.ModelStatus`, `events.ModelsProgress`, `local.ModelKind`, `*local.Installer` (satisfies `Downloader` after Task 21).
- Produces:
  ```go
  type Downloader interface {
      Has(kind local.ModelKind) bool
      Download(ctx context.Context, kind local.ModelKind, onProgress func(received, total int64)) error
  }
  type Options struct {
      Downloader   Downloader
      Enabled      func(kind local.ModelKind) bool // clip ← visual_grouping, quality ← quality_rating
      Emit         *events.Coalescer               // key = kind
      Backoff      []time.Duration                 // nil → 2 s, 10 s
      OnDownloaded func(kind local.ModelKind)      // called when a download finishes in this process — never for a model that was already on disk
  }
  type Manager struct{ /* … */ }
  func New(o Options) *Manager
  func (m *Manager) Start(ctx context.Context)    // one goroutine; downloads what is enabled and missing, CLIP first, then quality ([call] C16)
  func (m *Manager) Stop()                        // cancels and joins; .part files stay for resume
  func (m *Manager) Status() []events.ModelStatus // always [clip, quality]
  func (m *Manager) Ready(kind local.ModelKind) bool
  func (m *Manager) Retry(kind local.ModelKind)
  func (m *Manager) Refresh()                     // re-reads Enabled: off cancels that model's download and reports disabled; on queues it
  ```
  States: `disabled` (setting off), `ready` (file present), `downloading`, `queued` (enabled, missing, waiting for its turn or for a retry pause), `failed` (three attempts used). `Size` is `local.KnownModels[kind].Size` until the server reports a total. Every status change is emitted as `models:progress`; `ready`, `failed`, `disabled` and `queued` use `EmitNow`.

- [ ] **Step 1: Write the failing tests** (fake downloader with controllable progress and failure). `TestStart_Sequential_ClipFirst` (while CLIP is `downloading`, quality is `queued` and its `Download` has not been called; after CLIP finishes, quality starts). `TestStatus_OrderAndInitialSize`. `TestDisabledModelIsNeverDownloaded`. `TestClipDisabled_QualityDownloadsAlone`. `TestFailure_RetriesTwiceThenFailed` (three attempts with `Backoff: {1ms, 1ms}`, then `failed` with the error text; the other model still gets its turn). `TestRetry_AfterFailure` (⇒ `ready`). `TestRefresh_OffCancelsInFlight` (the fake's context is cancelled; state `disabled`; the next model starts). `TestRefresh_OnQueues`. `TestOnDownloaded_OnlyForCompletedDownloads` (a model present at `Start` ⇒ never called; a downloaded one ⇒ once). `TestStop_JoinsWithinOneSecond`. `TestEmits_ModelsProgress`.
- [ ] **Step 2: Run** `go test ./internal/app/models/` — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race -count=3 ./internal/app/models/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(models): automatic, opt-out, sequential model downloads`.

### Task 26: App-level inference

**Gate:** spike S5. **Depends on:** Tasks 3, 22.

**Files:**
- Create: `internal/app/inference/engine.go`
- Test: `internal/app/inference/engine_test.go`

**Interfaces:**
- Produces:
  ```go
  type Engine struct{ /* … */ }
  func New(models *local.ModelStore, cpuOnly map[local.ModelKind]bool) *Engine // sessions are created lazily on first use
  func (e *Engine) Embedder(enabled func() bool) local.Embedder // returns local.ErrModelMissing while enabled() is false
  func (e *Engine) CropInferer() local.CropInferer
  func (e *Engine) Provider(kind local.ModelKind) string
  func (e *Engine) Close() error                              // idempotent; blocks until the in-flight inference returns
  ```
  `cpuOnly[kind]` is `true` only if spike S5 failed for that model and `appdir.Sandboxed()`.

- [ ] **Step 1: Write the failing tests.** `TestEmbedder_DisabledIsModelMissing`. `TestEngine_NoSessionBeforeFirstUse`. `TestClose_Idempotent`. `TestClose_WaitsForInFlight` (a fake slow inference injected through an unexported hook; `Close` returns only after it does).
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/inference/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(inference): one app-level owner of the ONNX sessions`.

---

## Phase 3 — Library and jobs

### Task 27: `library.db`

**Gate:** none. **Depends on:** Tasks 9, 17 (`store.FileURI`).

**Files:**
- Create: `internal/app/library/{db.go,migrations/001_initial.sql}`
- Test: `internal/app/library/db_test.go`

**Interfaces:**
- Produces:
  ```sql
  CREATE TABLE folders (
    id TEXT PRIMARY KEY,                 -- 16 lowercase hex characters
    path TEXT NOT NULL, name TEXT NOT NULL, bookmark BLOB NOT NULL,
    volume_uuid TEXT NOT NULL DEFAULT '', volume_name TEXT NOT NULL DEFAULT '',
    added_at INTEGER NOT NULL,
    presence TEXT NOT NULL DEFAULT 'present',   -- present | offline | missing | access_lost
    job_pending INTEGER NOT NULL DEFAULT 0,     -- work is queued, running or was interrupted
    job_kind TEXT NOT NULL DEFAULT '',          -- '' | full | backfill
    job_seq INTEGER NOT NULL DEFAULT 0,         -- sequence number of the latest enqueue; orders the queue and identifies a run
    job_error TEXT NOT NULL DEFAULT '',
    photos INTEGER NOT NULL DEFAULT 0, videos INTEGER NOT NULL DEFAULT 0, clusters INTEGER NOT NULL DEFAULT 0,
    picked INTEGER NOT NULL DEFAULT 0, rejected INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0,
    first_captured INTEGER, last_captured INTEGER,
    previews INTEGER NOT NULL DEFAULT 0, previews_stamp INTEGER NOT NULL DEFAULT 0
  );
  CREATE UNIQUE INDEX idx_folders_path ON folders(LOWER(path));
  CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT NOT NULL);
  PRAGMA user_version = 1;
  ```
  ```go
  var ErrNotFound = errors.New("library: folder not found")
  type Stats struct { Photos, Videos, Clusters, Picked, Rejected, Failed int; FirstCaptured, LastCaptured sql.NullInt64 }
  type Folder struct { ID, Path, Name string; Bookmark []byte; VolumeUUID, VolumeName string; AddedAt int64; Presence string; JobPending bool; JobKind, JobError string; Stats Stats; Previews int; PreviewsStamp int64 }
  type PendingJob struct{ ID, Kind string; Seq int64 }
  func OpenDB(path string) (*DB, error) // takes a filesystem path and opens store.FileURI(path, "journal_mode(WAL)", "foreign_keys(on)", "busy_timeout(10000)") — never a hand-built DSN; MaxOpenConns(1); embedded migrations applied by user_version, as internal/store does
  func (d *DB) Close() error
  func (d *DB) InsertFolder(f Folder) error
  func (d *DB) Folders() ([]Folder, error)             // ordered by added_at, id
  func (d *DB) Folder(id string) (Folder, error)
  func (d *DB) DeleteFolder(id string) error
  func (d *DB) UpdateLocation(id, path, name string, v platform.VolumeInfo) error
  func (d *DB) SetBookmark(id string, bookmark []byte) error
  func (d *DB) SetPresence(id, presence string) error
  func (d *DB) EnqueueJob(id, kind string) (seq int64, err error)   // one transaction: seq = the kv counter "job_seq" + 1; job_pending = 1, job_seq = seq, job_error = ''; job_kind becomes kind, except that an already pending "full" is not downgraded to "backfill"
  func (d *DB) FinishJob(id string, seq int64, errText string) (cleared bool, err error) // clears job_pending and job_kind and stores errText only WHERE job_seq = seq — a newer enqueue survives the older run's completion
  func (d *DB) ClearJob(id string) error                            // removal: clears whatever is pending
  func (d *DB) PendingJobs() ([]PendingJob, error)     // job_pending = 1, in job_seq order (the order of enqueueing)
  func (d *DB) SetStats(id string, s Stats) error
  func (d *DB) SetPreviews(id string, n int, stamp int64) error
  func (d *DB) KVGet(k string) (v string, ok bool, err error)
  func (d *DB) KVSet(k, v string) error
  ```

- [ ] **Step 1: Write the failing tests.** `TestOpenDB_MigratesToVersion1` (reopening is a no-op). `TestOpenDB_CreatesFileInExistingDir`. `TestOpenDB_DirectoryNameWithURICharacters` (data directory `<tmp>/a #1?b%41/` ⇒ `library.db` is created inside it and `<tmp>` has no other entry). `TestInsertFolder_Defaults` (`JobPending == false`, `Presence == "present"`). `TestInsertFolder_PathIsUniqueIgnoringCase`. `TestFolder_NotFound`. `TestEnqueueJob_SequenceGrows` (three enqueues over two folders ⇒ seq 1, 2, 3). `TestEnqueueJob_FullIsNotDowngraded` (pending `full`, then `backfill` ⇒ still `full`, new seq). `TestFinishJob_OnlyItsOwnSequence` (enqueue seq 1, enqueue again seq 2, `FinishJob(id, 1, "")` ⇒ `cleared == false`, still pending; `FinishJob(id, 2, "")` ⇒ cleared). `TestFinishJob_StoresError`. `TestClearJob`. `TestPendingJobs_InEnqueueOrder` (the folder added last but enqueued first comes first). `TestSetStats_RoundTrip`, `TestUpdateLocation_RoundTrip`, `TestSetBookmark_RoundTrip`, `TestKV_RoundTrip` (missing key ⇒ `ok == false`). `TestFolders_OrderedByAddedAt`.
- [ ] **Step 2: Run** `go test ./internal/app/library/` — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/library/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(library): library database`.

### Task 28: Folders — add, remove, access

**Gate:** none. **Depends on:** Tasks 9, 24, 27.

**Files:**
- Create: `internal/app/library/{library.go,paths.go}`
- Test: `internal/app/library/{library_test.go,paths_test.go}`

**Interfaces:**
- Consumes: `DB`, `platform.Bookmarks`, `platform.Volumes`, `events.Emitter`, `events.LibraryChangedEvent`.
- Produces:
  ```go
  var ErrNotDirectory = errors.New("library: not a folder")
  var ErrOffline = errors.New("library: folder is offline")
  var ErrAccessLost = errors.New("library: access to the folder was lost")
  const AccessLostMessage = "Choix lost access to this folder. Add it again."
  type Options struct {
      DB *DB; Bookmarks platform.Bookmarks; Volumes platform.Volumes; Emit events.Emitter
      Now func() time.Time; NewID func() string // nil → 8 random bytes as hex
      OnOffline func(id string)                 // offline, missing, access lost, or moved while in use: jobs are suspended, the catalog lets go
      OnRemoved func(id string)                 // removed by the user or verified gone from disk: jobs are cancelled
      OnOnline  func(id string)                 // the folder is present and has pending work that may not be queued: called on a transition back to present AND on every presence pass for each present folder with job_pending = 1 (the queue's Resume is a no-op when it is already queued or running)
  }
  type AddResult struct { ID string `json:"id"`; Name string `json:"name"`; Existing bool `json:"existing"`; Relation string `json:"relation,omitempty"` } // relation: same | inside | contains
  func New(o Options) *Library
  func (l *Library) Add(path string) (AddResult, error)
  func (l *Library) Remove(id string) error     // forgets the folder and its previews directory; touches nothing in the folder
  func (l *Library) Folders() ([]Folder, error)
  func (l *Library) Folder(id string) (Folder, error)
  func (l *Library) Acquire(id string) (root string, release func(), err error) // reference-counted StartAccess; the last release stops access
  func (l *Library) InUse(id string) bool
  func (l *Library) EnqueueJob(id, kind string) (int64, error)   // pass-throughs to DB
  func (l *Library) FinishJob(id string, seq int64, errText string) (bool, error)
  func (l *Library) ClearJob(id string) error
  func (l *Library) PendingJobs() ([]PendingJob, error)
  func relation(existing, candidate string) string // "", same, inside, contains — case-insensitive, on cleaned paths
  ```
  `Add`: `filepath.EvalSymlinks(filepath.Clean(path))`; not a directory ⇒ `ErrNotDirectory`; relation with any folder ⇒ `{ID: thatFolder, Existing: true, Relation}` and nothing is stored — **except** `same` on a folder whose presence is `access_lost`: a new bookmark replaces the old one, presence becomes `present`, `Existing` is `false`, `OnOnline` is called. New folder: bookmark, `Volumes.Info`, insert, emit `library:changed {reason: "added"}`. `Acquire`: `ErrGone` ⇒ `ErrOffline`; `ErrUnusable` ⇒ presence `access_lost`, emit `library:changed {reason: "offline"}`, `ErrAccessLost`; a resolved path different from the stored one ⇒ `UpdateLocation`. Presence `access_lost` is cleared only by a successful `Acquire` or by adding the folder again.

- [ ] **Step 1: Write the failing tests.** `TestAdd_NewFolder` (row stored, bookmark non-empty, event `added`). `TestAdd_FileIsErrNotDirectory`. `TestAdd_SameFolderTwice` (`Existing`, `Relation == "same"`, one row). `TestAdd_Variants` (Review Focus 3; table: trailing slash, different case, a symlink to the folder ⇒ `same`; a subfolder and the folder's own `picks/` ⇒ `inside`; the parent ⇒ `contains` — one row every time). `TestRelation_PrefixIsNotInside` (`/a/Trip` vs `/a/Trip2` ⇒ `""`). `TestAcquire_UnusableBookmarkIsAccessLost` (Review Focus 2: `MakeUnusable(path)` ⇒ `ErrAccessLost`, presence `access_lost`, event emitted, `OnOffline` called). `TestAdd_AccessLostFolderReplacesBookmark` (after the above, make it usable again; `Add(samePath)` ⇒ `Existing == false`, presence `present`, bookmark bytes changed, still one row, `OnOnline` called). `TestRemove_DeletesRowKeepsFolder` (the directory and a file in it still exist; event `removed`; `OnRemoved` called). `TestAcquire_RefCounted` (two acquires ⇒ active once; after both releases empty; releasing twice is safe). `TestAcquire_FollowsMove`. `TestAcquire_GoneIsErrOffline`.
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/library/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(library): add, remove and scoped access to folders`.

### Task 29: Presence — gone, offline, missing, moved, access lost

**Gate:** spike S1. **Depends on:** Tasks 4, 28.

**Files:**
- Create: `internal/app/library/presence.go`
- Test: `internal/app/library/presence_test.go`

**Interfaces:**
- Produces:
  ```go
  const MissingMessage = "This folder can no longer be found."
  type PresenceResult struct { Removed, Offline, Missing, Online, Moved, AccessLost []string } // folder ids
  func (l *Library) CheckPresence(ctx context.Context) (PresenceResult, error) // every folder
  func (l *Library) CheckFolder(ctx context.Context, id string) (PresenceResult, error) // one folder, at once — the queue calls it when a job is suspended
  func (l *Library) RunPresence(ctx context.Context, every time.Duration, visible func() bool) // every = 30 s; skips a tick while visible() is false
  ```
- Per folder, in `added_at` order (**[call] C7**, R19). **A row is dropped only in the two rows marked "dropped"**: the folder is verified to be in the Trash, or its absence is verified independently of the bookmark on its identified, mounted volume. A bookmark that fails to resolve is never enough by itself.

  | `Bookmarks.Resolve` | Condition | Outcome |
  |---|---|---|
  | path | `InTrash(path)` is `true` — directly, or, when the first call returned an error, repeated inside a short `StartAccess` lease | row **dropped**; `Removed`; event `gone` with the name |
  | path | `InTrash` still returns an error under the lease | treated as not in the Trash; `slog.Warn` once per folder |
  | path | path differs from the stored one | `UpdateLocation`; `Moved`; event `moved` |
  | path | `stale` | `Bookmarks.Refresh`; on success `SetBookmark`, on failure keep the old bytes and `slog.Warn` |
  | path | presence was `offline` or `missing` | `present`; `Online`; event `online` |
  | `ErrGone` | `volume_uuid == ""`, or the volume is not mounted | presence `offline` (event `offline` on the transition only); `Offline`; never dropped |
  | `ErrGone` | volume identified and mounted, and `Volumes.AbsentOn(storedPath, volume_uuid)` is `true` — the stored path answers "no such file" **and that probe was made on the folder's own volume** (its nearest existing ancestor has the stored UUID) | row **dropped**; `Removed`; event `gone` |
  | `ErrGone` | volume identified and mounted, but `AbsentOn` is `false` — not verifiable ("not permitted"), or the volume is mounted **at another path** so the old path proves nothing | presence `missing` (event `offline` on the transition); `Missing`; the row stays and shows `MissingMessage` until the folder resolves again or the user removes it |
  | `ErrUnusable` | — | presence `access_lost` (event `offline` on the transition); `AccessLost` |
  | other error | — | row unchanged; `slog.Warn` |

  If spike S1(e) failed (the sandbox does not let `lstat` tell "missing" from "not permitted"), the second `ErrGone` row never fires and deleted folders stay as `missing` — that is the documented fallback.
  `OnOffline(id)` is called for every id in `Offline`, `Missing` and `AccessLost`, and for an id in `Moved` when `InUse(id)`. `OnRemoved(id)` for every id in `Removed`. `OnOnline(id)` for every id in `Online` **and, on every pass, for every folder that is `present` with `job_pending = 1`** — so a suspended job resumes on the next pass even when the drive came back before any pass saw it gone, and after a move.

- [ ] **Step 1: Write the failing tests.** `TestPresence_VerifiedAbsentOnMountedVolumeIsRemoved` (bookmark `ErrGone`, volume mounted, fake `AbsentOn(path, uuid) == true` ⇒ dropped; event `gone` carries the name; `OnRemoved`). `TestPresence_VolumeMountedAtAnotherPathKeepsRow` (the fake volume with the stored UUID is mounted under a different prefix, the bookmark returns `ErrGone`, and the old stored path does not exist — `AbsentOn(oldPath, uuid)` is `false` because that path is no longer on that volume ⇒ the row is **kept** with presence `missing`; never dropped). `TestPresence_UnresolvedButNotVerifiedIsMissing` (bookmark `ErrGone`, volume mounted, `AbsentOn == false` ⇒ row kept, presence `missing`, `OnOffline`; a later successful resolve ⇒ `present`, `online`). `TestPresence_VerifiedTrashIsRemoved`. `TestPresence_TrashCheckRetriedUnderLease` (the fake's `InTrash` errors unless access is active ⇒ the folder is still recognised as trashed and dropped; the lease is stopped afterwards: `Active()` empty). `TestPresence_TrashUnknownKeepsRow` (`TrashUnknown(path)` ⇒ still present). `TestPresence_PendingPresentFolderIsResumedEveryPass` (a present folder with `job_pending = 1` ⇒ `OnOnline(id)` on each of two passes; a present folder without pending ⇒ never). `TestCheckFolder_OnlyThatFolder`. `TestPresence_UnmountedVolumeIsOffline` (row kept, one `offline` event across two checks, `OnOffline` once). `TestPresence_NoVolumeIdentityIsOfflineNotRemoved` (`volume_uuid == ""`, target gone ⇒ `offline`, row kept). `TestPresence_RemountComesBack` (`online`, `OnOnline`). `TestPresence_RenameIsFollowed`. `TestPresence_StaleBookmarkIsRefreshed` (`Refreshed() == 1`, stored bytes changed). `TestPresence_StaleRefreshFailureKeepsOldBytes`. `TestPresence_UnusableBookmarkIsAccessLost` (Review Focus 2: row kept, presence `access_lost`, never dropped). `TestPresence_UnpluggedWhileAcquired` (Review Focus 1: `Offline`, `OnOffline(id)` once, row kept). `TestPresence_RenamedWhileAcquired` (`Moved`, `OnOffline(id)`; not called when the folder is not in use). `TestRunPresence_SkipsWhileHidden`.
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/library/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(library): follow, drop or park folders as the disk changes`.

### Task 30: Sessions

**Gate:** none. **Depends on:** Tasks 16, 17, 19, 20, 22, 23, 28.

**Files:**
- Create: `internal/app/session/{session.go,manager.go,pipeline.go}`, `internal/app/session/testdata/v1-state.db` (a `user_version` 5 database produced by the v1 code through the committed helper `testdata/_make_v1_state.go`, `//go:build ignore`: 6 analyzed files, 2 clusters, 2 picks exported under `exports/out/`, exiftool-keyed `raw_exif`, no `cluster_params` key)
- Test: `internal/app/session/{manager_test.go,pipeline_test.go}`

**Interfaces:**
- Consumes: `library.Library.Acquire`, `safefs.OpenDir`, `store.Open`, `meta.NativeReader`, `thumb.Builder`, `thumb.SweepTemps`, `picks.{New,EffectiveDir,CheckDir}`, `local.NewAnalyzer`, `local.NewQualityScorer`, `group.NewGrouper`, `scanner.New`, `pipeline.Pipeline`, `platform.{Inflight,WithInflight}`, `config.Config`.
- Produces:
  ```go
  var ErrReadOnly = errors.New("session: the folder is read-only")
  const ReadOnlyMessage = "Choix cannot write its .choix folder here — the volume is read-only."
  var ErrUnsafeState = errors.New("session: .choix or its database is not a plain directory and files")
  const UnsafeStateMessage = "Choix will not open this folder: its .choix folder or database is a symbolic link."
  type Deps struct {
      Library *library.Library; Settings func() config.Config
      Stills platform.Stills; Video platform.Video
      Embedder local.Embedder; CropInferer local.CropInferer
  }
  type Session struct { FolderID, Root string; Store *store.Store }
  func (s *Session) Context(parent context.Context) context.Context   // carries the session's Inflight: every native call for this folder is counted
  func (s *Session) PicksDir(cfg config.Config) (dir string, err error) // picks.EffectiveDir, then ValidateDir and CheckDir; the error (if any) is why this folder cannot take picks
  func (s *Session) Picks(cfg config.Config) (*picks.Service, error)
  func (s *Session) NewPipeline(settings func() config.Config) (*pipeline.Pipeline, <-chan pipeline.Progress) // ported from internal/cli/pipeline_build.go: metadata and thumb NumCPU, analyze 1, batch 100; the scanner skips the folder's effective picks dir and the other legacy export dirs (SetExtraSkipDirs) — unless CheckDir refused the directory, in which case it is walked as originals; the pipeline's Grouper is the session's live clusterer, below
  func (s *Session) Scorer() *local.QualityScorer
  func (s *Session) Recluster(ctx context.Context, settings func() config.Config) error               // the live clusterer, once
  func (s *Session) EnsureClustered(ctx context.Context, settings func() config.Config) (bool, error) // [call] C20
  func ClusterParams(cfg config.Config) string // fmt.Sprintf("b=%d;t=%.4f;x=%t;v=%t", BucketSizeSec, VisualClusterThreshold, CrossDeviceMerging, VisualGrouping)
  type Manager struct{ /* … */ }
  func NewManager(d Deps) *Manager
  func (m *Manager) Acquire(folderID string) (*Session, func(), error)    // one shared Session per folder, reference-counted
  func (m *Manager) AcquireOpen(folderID string) (*Session, func(), bool) // only if a session is already open
  func (m *Manager) Shutdown(ctx context.Context) (stillOpen int)         // refuses new Acquires; closes each session as soon as it has no reference and no detached call; returns when all are closed or ctx is done. A session still in use at that point is NOT closed (its store and bookmark stay open; the process is about to exit)
  ```
- **Opening** (the checks R16 promises, made once per open): `Library.Acquire` → `safefs.OpenDir(root, ".choix", true)` — `.choix` must be a real directory inside the root (a symlink ⇒ `ErrUnsafeState`; permission or read-only-filesystem ⇒ `ErrReadOnly`) → `Dir.VerifyRegular("state.db", "state.db-wal", "state.db-shm")` — each, if present, must be a regular file (else `ErrUnsafeState`) → `store.Open(filepath.Join(root, ".choix", "state.db"))` — a filesystem path; `store.Open` escapes it (Task 17), so the file SQLite opens is exactly the `state.db` that `VerifyRegular` just checked, whatever characters the folder's name contains (no SQLite VFS is built; a directory swapped by another process after this point is outside the threat model) → `meta.BackfillGPS`, `meta.BackfillDeviceKey` → `thumb.SweepTemps` → `Picks(cfg).Reconcile` when the picks dir is usable (it copies nothing — R24 — so opening never waits on file copies; its `Missing` count is kept on the session for `FolderView.picks_missing`). A folder that fails these checks shows as `error` with `UnsafeStateMessage` / `ReadOnlyMessage`.
- **Last release** (**[call] C3**): the session stops being handed out, then in the background `Inflight.Wait(-1)`, `Store.Close()`, the library release; an `Acquire` that arrives meanwhile revives the same session. A store is never closed while a reference or a detached call exists.
- **Clustering is live and serialised** (**[call] C20**). The session has one clustering lock. The live clusterer — used as the pipeline's `Grouper`, by `Recluster` and by `EnsureClustered` — takes the lock, reads `settings()` **at that moment**, builds `group.NewGrouper(store, bucket, threshold, crossDevice)` with `SetUseEmbeddings(cfg.VisualGrouping)`, runs `RebuildAll`, and stores `ClusterParams(cfg)` in the folder's `kv` before releasing the lock. A job that started under older settings therefore clusters with — and stamps — the settings in force when its cluster stage runs, and can never overwrite a newer clustering with an older one. The analyzer's embedder is gated by the live `visual_grouping` value too. `EnsureClustered`: the stored `cluster_params` is absent ⇒ **do nothing and store nothing** (a v1 folder keeps its clusters until something rebuilds them); present and different from `ClusterParams(settings())` ⇒ recluster, return `true`.

- [ ] **Step 1: Write the failing tests.** `TestAcquire_SharesOneSession` (same pointer; the store closes only after both releases; bookmark access stopped after the last one). `TestAcquire_CreatesChoixDir`. `TestAcquire_ReadOnlyFolder` (directory mode 0500 ⇒ `ErrReadOnly`, library access released). `TestAcquire_SymlinkedChoixDirRefused` (`.choix -> /tmp/x` ⇒ `ErrUnsafeState`, nothing created at the target). `TestAcquire_SymlinkedDatabaseRefused` (`.choix/state.db -> ../../elsewhere.db`, and separately a symlinked `state.db-wal` ⇒ `ErrUnsafeState`; the link target is not opened or modified). `TestAcquire_ChoixSwappedBetweenSessions` (open, close, replace `.choix` with a symlink to a copy, open again ⇒ `ErrUnsafeState`). `TestAcquire_FolderNameWithURICharacters` (a folder named `a #1?b%41`: after `Acquire`, `state.db` is inside its `.choix`, the folder's parent has no new entry, and a second `Acquire` after closing reads the same data). `TestAcquire_ReconcileCopiesNothing` (the v1 fixture with one exported copy deleted ⇒ opening does not recreate it; the session reports one missing pick). `TestAcquire_OfflineFolder` (`library.ErrOffline`). `TestAcquireOpen_DoesNotOpen`. `TestRelease_WaitsForDetachedCalls` (a fake `Stills` call held by its `Gate` under `Session.Context`; after the last release the bookmark is still active; releasing the gate ⇒ access stopped and the store closed). `TestShutdown_ClosesIdleSessions` (`stillOpen == 0`). `TestShutdown_LeavesBusySessionOpen` (one session with a held reference and one with a detached call that never returns ⇒ `Shutdown` with a 50 ms context returns `2` in under 1 s; both stores still answer a query; bookmark access for both is still active). `TestShutdown_RefusesNewAcquires`. `TestAcquire_RevivesDrainingSession`. `TestAcquire_V1FolderOpensUntouched` (Review Focus 4: copy `v1-state.db` and its `exports/out/` files into a temp folder; after `Acquire` + `EnsureClustered` with default settings: 6 files still `analyzed`, the same 2 cluster ids, both picks present, **no `cluster_params` key stored**, `reclustered == false`; `PicksDir(default) == "exports/out"`; `NewPipeline(...).Pending() == 0`; after a walk the two exported copies are not in `files`). `TestEnsureClustered_ReclustersOnChange` (key present with bucket 600, settings 60 ⇒ `true`, key updated). `TestClusterer_UsesSettingsAtClusterTime` (a pipeline built while bucket is 600; the setting changes to 60 while the thumb stage is held; after the run the stored params say 60 and the clusters match a 60-second bucket). `TestClusterer_OlderRunCannotOverwriteNewer` (`Recluster` with bucket 60 completes while a pipeline's cluster stage waits on the lock; when it runs it reads 60 too — the stored params never go back to 600). `TestClusterer_VisualGroupingOffIgnoresEmbeddings` (switched off mid-run ⇒ time-only clusters). `TestPicksDir_HoldsOriginals` (configured dir `shoot1` containing scanned JPEGs ⇒ `picks.ErrDirHasOriginals`; the pipeline's scanner still discovers those JPEGs). `TestPicksDir_OrphanDoesNotBlock` (an unknown JPEG in `picks/` ⇒ no error; the scanner does not discover it). `TestNewPipeline_VisualGroupingOffComputesNoEmbeddings`. `TestNewPipeline_SkipsPicksDir` (`PicksDir: "exports/out"` ⇒ a JPEG placed there is not discovered).
- [ ] **Step 2: Run** `go test ./internal/app/session/` — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/session/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(session): per-folder store and pipeline with shared access`.

### Task 31: The queue — order, persistence, preemption, suspension

**Gate:** none. **Depends on:** Tasks 24, 28.

**Files:**
- Create: `internal/app/session/queue.go`
- Test: `internal/app/session/queue_test.go`

**Interfaces:**
- Consumes: `library.Library.{Folders,EnqueueJob,FinishJob,ClearJob,PendingJobs}`, `events.Coalescer`.
- Produces:
  ```go
  type JobKind int
  const ( JobBackfill JobKind = iota; JobFull )          // stored as "backfill" / "full" ([call] C17)
  type Outcome int
  const ( Done Outcome = iota; Failed; Suspended; Stopped ) // Stopped: the context was cancelled
  type Runner interface { // the real one is Task 32's Job
      Run(ctx context.Context, folderID string, kind JobKind, report func(p events.Progress, phase string)) (Outcome, string)
  }
  type QueueOptions struct {
      Library *library.Library; Runner Runner; Emit *events.Coalescer
      OnSuspended func(folderID string) // the runtime runs Library.CheckFolder for it at once, so the row turns offline (or the job is re-queued) without waiting for the 30 s tick
  }
  type Queue struct{ /* … */ }
  func NewQueue(o QueueOptions) *Queue
  func (q *Queue) Start(ctx context.Context)                        // one worker goroutine; first enqueues every PendingJobs() entry whose folder is present, in sequence order, with its stored kind
  func (q *Queue) Enqueue(folderID string, kind JobKind) error      // FIFO; a queued folder is not queued twice (full replaces backfill, never the reverse); enqueueing the folder that is running marks it to run again afterwards
  func (q *Queue) EnqueueFront(folderID string, kind JobKind) error // [call] C18: front of the queue; a different running job is stopped and re-queued second
  func (q *Queue) EnqueueAllReachable(kind JobKind)                 // every folder with presence "present"
  func (q *Queue) Suspend(folderID string)                          // drop it from the queue or stop it; it stays pending in library.db
  func (q *Queue) Resume(folderID string)                           // if library.db says pending and it is neither queued nor running: enqueue it with the stored kind; otherwise nothing
  func (q *Queue) Cancel(folderID string)                           // drop or stop it and ClearJob; returns once the runner has returned
  func (q *Queue) State(folderID string) (state string, p *events.Progress) // "", "queued" or "scanning"
  func (q *Queue) Idle() bool
  func (q *Queue) Stop()                                            // stop the running job, join the worker; pending rows stay
  ```
- Persistence and generations (**[call] C19**): `Enqueue`/`EnqueueFront` call `Library.EnqueueJob(id, kind)` **before** returning and remember the returned sequence number; a run carries the sequence it started with. After a run:
  - `Done` ⇒ `FinishJob(id, seq, "")`. If it reports `cleared == false`, a newer enqueue arrived during the run: the folder is queued again (at the tail) instead of being marked ready. Otherwise `scan:state ready`.
  - `Failed` ⇒ `FinishJob(id, seq, message)`, `scan:state error` (again: a newer enqueue keeps it pending and re-queues it).
  - `Suspended` ⇒ the row is untouched (still pending), no state event from the queue; `OnSuspended(id)` is called. A folder that suspends three times in a row while the library still reports it present becomes `Failed` with "The folder could not be read." (no endless loop).
  - `Stopped` ⇒ row untouched unless `Cancel` asked for it. A preempted job is `Stopped` and re-queued with its sequence kept.
  Events: `scan:state queued` on enqueue, `scanning` on start (all `EmitNow`); `report` goes to `scan:progress` through the coalescer (key = folder id), `starting`/`done`/`failed` phases with `EmitNow`.

- [ ] **Step 1: Write the failing tests** (a fake `Runner` with per-folder gates and scripted outcomes; a real `library.DB` in a temp file). `TestQueue_OneAtATimeFIFO` (A, B, C; never two running). `TestQueue_DedupesQueuedFolder`. `TestQueue_FullReplacesBackfillNotReverse`. `TestEnqueue_PersistsBeforeRunning` (enqueue B behind a held A, `Stop()`, reopen the DB ⇒ B is in `PendingJobs()` with kind `full`). `TestStart_RequeuesPendingWithStoredKind` (a row `pending/backfill` ⇒ the runner is called with `JobBackfill`; a row without pending is never run). `TestStart_RequeuesInEnqueueOrder` (B enqueued before A although A was added first ⇒ B runs first after a restart). `TestEnqueueDuringRun_RunsAgain` (A is running; `Enqueue(A, JobFull)`; A's first run returns `Done` ⇒ `job_pending` is still 1 and the runner is called for A a second time; only after that is it `ready`). `TestDone_DoesNotEraseNewerEnqueue` (the same through the database: `FinishJob` with the older sequence clears nothing). `TestStart_SkipsOfflinePending` (pending but presence `offline` ⇒ not run, still pending). `TestEnqueueFront_PreemptsRunningJob` (A held; `EnqueueFront(B)` ⇒ A's context cancelled within 1 s; completion order B then A). `TestEnqueueFront_SameFolderDoesNotRestart`. `TestSuspend_KeepsPending` (running A ⇒ stopped, `job_pending == 1`, state `""`; the queue takes B). `TestResume_RequeuesSuspended`. `TestResume_NoOpWhenQueuedOrRunning` (calling it ten times while A runs starts nothing new). `TestCancel_ClearsPendingAndWaits` (returns only after the runner returned; `job_pending == 0`). `TestOutcome_FailedStoresMessage` (`job_error`, `scan:state error`). `TestOutcome_SuspendedKeepsPendingAndContinues` (Review Focus 1: A returns `Suspended` ⇒ pending kept, no `job_error`, `OnSuspended("A")` called once, no `scan:state` event for A from the queue, B runs). `TestOutcome_ThreeSuspensionsOfPresentFolderFail` (A suspends, is resumed, three times ⇒ `Failed`, `job_error` set). `TestStop_JoinsAndLeavesPending`. `TestProgress_Coalesced` (the runner reports 3000 times in 1 s ⇒ fewer than 15 `scan:progress` events and a final `phase: "done"`). `TestState_QueuedAndScanning`. `TestIdle`.
- [ ] **Step 2: Run** `go test ./internal/app/session/ -run 'Queue|Enqueue|Suspend|Resume|Cancel|Outcome|Start|Stop|Progress|State|Idle'` — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race -count=3 ./internal/app/session/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(session): persistent one-at-a-time job queue with preemption`.

### Task 32: The folder job

**Gate:** none. **Depends on:** Tasks 23, 25, 30, 31.

**Files:**
- Create: `internal/app/session/job.go`
- Test: `internal/app/session/job_test.go`

**Interfaces:**
- Consumes: `Manager`, `Session`, `quality.Pass`, `store.FilesRepo.{CountAnalyzedMissingEmbedding,BumpAnalyzedMissingEmbedding}`, `local.ModelKind`.
- Produces:
  ```go
  type ModelState interface { Ready(kind local.ModelKind) bool }
  type QualityRun interface { // Task 33's tracker
      Begin(ctx context.Context) (passCtx context.Context, progress func(done, total int), end func(rated int, err error))
  }
  type JobOptions struct {
      Sessions *Manager; Settings func() config.Config; Models ModelState; Quality QualityRun
      Emit *events.Coalescer
      AfterJob func(ctx context.Context, s *Session) // Task 34's stats and previews refresh
  }
  type Job struct{ /* implements Runner */ }
  func NewJob(o JobOptions) *Job
  ```
- `Run`, in order; every pipeline call uses `s.Context(ctx)`; `ctx` is checked between steps:
  1. `Sessions.Acquire` (its error decides the outcome, see below).
  2. `JobFull`: `pipeline.Run`. `JobBackfill`: only when `Pending() > 0`: `pipeline.Resume`. The pipeline comes from `s.NewPipeline(o.Settings)`, so its cluster stage is the session's live clusterer: it reads the settings when it runs and stamps what it used.
  3. CLIP backfill: when the live `VisualGrouping` is on and `Models.Ready(ModelCLIP)` and `CountAnalyzedMissingEmbedding() > 0`: `BumpAnalyzedMissingEmbedding()`, `pipeline.Resume`.
  4. `s.EnsureClustered(ctx, o.Settings)` — catches a setting changed after the cluster stage.
  5. The clusters are final: `AfterJob`; emit `folder:changed {reason: "clusters"}`, before the quality pass, so the folder can be reviewed while it is rated (PR #11 review). With no pass due this is the job's end: return `Done`, even if `ctx` was cancelled during `AfterJob`. Otherwise `ctx` and the folder are checked as between any two steps.
  6. Quality: when `QualityRating` and `Models.Ready(ModelQuality)`: `Quality.Begin`, `quality.Pass{Scorer: s.Scorer(), …}.Run(passCtx)`, `end`; each time `rated` grows, emit `folder:changed {reason: "ratings"}` at most once per second, and once more when the pass ends if a growth was held back. A cancelled `passCtx` (the setting was switched off) ends this step only, not the job. The pass writes only ratings: `AfterJob` and `"clusters"` are not repeated after it.
- Outcome of an error: `ctx` cancelled ⇒ `Stopped`. `library.ErrOffline` or `library.ErrAccessLost` from `Acquire`, or any step error while the folder's root no longer exists (`os.Lstat(root)` fails) ⇒ **`Suspended`** (**[call] C19**). `session.ErrReadOnly` ⇒ `Failed` with `ReadOnlyMessage`; `session.ErrUnsafeState` ⇒ `Failed` with `UnsafeStateMessage`. Anything else ⇒ `Failed` with the error text. The session is always released.

- [ ] **Step 1: Write the failing tests** (real `Manager` over temp folders of generated JPEGs; platform fakes; fake embedder and crop inferer; the fake `Stills`' `Gate` to hold a job mid-stage). `TestJob_FullRunClusters` (`Done`; clusters exist; `cluster_params` stored). `TestJob_BrokenFilesDoNotStopIt` (Review Focus 5: a zero-byte `.jpg`, a text file named `.jpg`, a RAF from `MakeRAF` cut at byte 90, a `.mov` the fake reports as `ErrNoVideoTrack`, and three good JPEGs ⇒ `Done`; exactly four rows `failed`, each with a non-empty `error`; three `analyzed`). `TestJob_CancelDuringNativeCallReturnsWithinOneSecond` (gate held; cancel ⇒ `Stopped` in under 1 s; no file is marked `failed`; after the gate opens nothing new appears on disk). `TestJob_FolderVanishesMidScan` (Review Focus 1: remove the folder's directory while the gate is held ⇒ `Suspended`, not `Failed`). `TestJob_OfflineAtStartIsSuspended`. `TestJob_ReadOnlyFolderIsFailed` (message `ReadOnlyMessage`). `TestJob_SymlinkedDatabaseIsFailed` (message `UnsafeStateMessage`). `TestJob_SettingChangedMidRunWins` (bucket changes from 600 to 60 while the thumb stage is held ⇒ the stored params and the clusters are for 60). `TestJob_ClipBackfillAfterModelArrives` (first run with `Ready(clip) == false` ⇒ no embeddings; a `JobBackfill` with it ready ⇒ every file has one; the walk did not run: a file added to the folder in between is not in `files`). `TestJob_BackfillWithNothingPendingDoesNotRecluster` (cluster ids unchanged). `TestJob_VisualGroupingOffSkipsBackfill`. `TestJob_QualityStepUsesTracker` (`Begin`, progress `(n, n)`, `end(n, nil)`; `folder:changed ratings` emitted). `TestJob_QualityCancelledByTrackerStillFinishes` (`passCtx` cancelled mid-pass ⇒ outcome `Done`, `AfterJob` called). `TestJob_V1FolderFullRunStampsParams` (the v1 fixture: after `JobFull`, `cluster_params` exists).
- [ ] **Step 2: Run** `go test ./internal/app/session/ -run TestJob` — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race -count=3 ./internal/app/session/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(session): folder job with backfill, quality pass and suspension`.

### Task 33: Quality status and opt-out

**Gate:** none. **Depends on:** Tasks 24, 25, 31, 32 (it implements Task 32's `QualityRun`).

**Files:**
- Create: `internal/app/session/quality.go`
- Test: `internal/app/session/quality_test.go`

**Interfaces:**
- Produces:
  ```go
  type QualityOptions struct {
      Enabled func() bool                          // the quality_rating setting
      Model   func() events.ModelStatus            // the quality model's download status
      Busy    func() bool                          // a job is queued or running
      Emit    *events.Coalescer                    // name quality:status, key "quality"
  }
  type QualityTracker struct{ /* implements QualityRun */ }
  func NewQualityTracker(o QualityOptions) *QualityTracker
  func (t *QualityTracker) Status() events.Quality
  func (t *QualityTracker) SettingChanged()        // off: cancels the pass in flight at once; always re-emits the status
  func (t *QualityTracker) Changed()               // called on every model-status and queue-state change; emits when the status differs from the last one sent
  ```
  `Status`: `Enabled`; `State` — setting off ⇒ `disabled`; model `downloading` ⇒ `downloading` with `Received`/`Size`; model `failed` ⇒ `download_failed` with the error; a pass running ⇒ `scoring` with `Done`/`Total`; the last pass ended with `local.ErrQualityUnavailable` ⇒ `error` with its text; `Busy()` ⇒ `waiting`; otherwise `idle`. `Active` is true for `downloading`, `waiting`, `scoring`. `Rated` only grows. Every change of `State` is sent with `EmitNow`; progress within `scoring` and `downloading` is coalesced.

- [ ] **Step 1: Write the failing tests.** One case per state (`TestStatus_Disabled`, `…_Downloading`, `…_DownloadFailed`, `…_Scoring`, `…_Error`, `…_Waiting`, `…_Idle`). `TestSettingOff_CancelsPassImmediately` (a pass begun, `Enabled` flips to false, `SettingChanged()` ⇒ `passCtx.Done()` closed within 100 ms and a `quality:status` with `state: "disabled"` emitted). `TestBegin_WhenDisabledReturnsCancelledContext`. `TestTransitions_AllEmitted` (waiting → scoring → idle each produce exactly one immediate event). `TestRated_Monotonic` (two passes of 3 and 2 ⇒ `Rated == 5`). `TestScoringProgress_Coalesced`.
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/session/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(session): quality status with immediate opt-out`.

### Task 34: Cached stats and preview strips

**Gate:** none. **Depends on:** Tasks 9, 15, 16, 23, 28.

**Files:**
- Create: `internal/app/library/{stats.go,previews.go}`
- Test: `internal/app/library/{stats_test.go,previews_test.go}`

**Interfaces:**
- Produces:
  ```go
  func (l *Library) RefreshStats(id string, st *store.Store) error // files (LibraryStats), COUNT(clusters), picks by state → SetStats; emits library:changed {reason: "stats"}
  func (l *Library) RefreshPreviews(ctx context.Context, id string, st *store.Store, root string, stills platform.Stills, dir string) error
  func SampleIndices(n, k int) []int // n ≤ k: 0…n-1; else round(i·(n-1)/(k-1)) for i in 0…k-1
  ```
  `RefreshPreviews` (**[call] C21**): `PreviewCandidates()`, `SampleIndices(len, 6)`; for each, the source is the file's `preview` tier row if present, else `thumb`, read through `safefs` from `.choix/thumbs`; `stills.Thumbnail(ImageInput{Data: …}, 512, platform.FitWidth)`; written as `<dir>/<i>.jpg` (temporary name, then rename); files `<n>.jpg … 5.jpg` beyond the new count are removed; `SetPreviews(id, n, Now().Unix())`; emits `library:changed {reason: "stats"}`. It is called after a job and, debounced, after picks and rejects (Task 36).

- [ ] **Step 1: Write the failing tests.** `TestSampleIndices` (`(0,6) → []`, `(3,6) → [0 1 2]`, `(6,6) → [0 1 2 3 4 5]`, `(11,6) → [0 2 4 6 8 10]`, `(5000,6) → [0 1000 2000 2999 3999 4999]`). `TestRefreshStats_CountsAndRange` (4 photos, 1 video, 2 clusters, 1 picked, 1 rejected, captured 2026-03-04 and 2026-03-06 ⇒ those numbers and timestamps; event `stats`). `TestRefreshPreviews_WritesSix` (20 files ⇒ `0.jpg…5.jpg`, each at most 512 px wide, `Previews == 6`). `TestRefreshPreviews_FewerThanSix` (2 files ⇒ two files; a stale `4.jpg` is deleted). `TestRefreshPreviews_SkipsRejected` (rejecting a sampled file and refreshing replaces it; the stamp grows). `TestRefreshPreviews_EmptyFolder` (`Previews == 0`, no error). `TestRefreshPreviews_CancelledContext` (no partial set: the previous files are intact).
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/library/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(library): cached counts and preview strips`.

---

## Phase 4 — Services and events

Rules for every `Service` type in this phase: unexported fields only; its exported methods are exactly the bindings listed; the constructor is a package function; every binding starts with `leave, err := gate.Enter()` and returns that error when the app is quitting. A test in each package (`TestService_ExportedMethods`) compares `reflect.TypeOf(&Service{})`'s method set with the listed names, so an accidental export fails the build. Errors returned to the frontend are `*apperr.Error`.

### Task 35: Errors, admission gate, settings

**Gate:** none. **Depends on:** Tasks 15, 17, 28, 30.

**Files:**
- Create: `internal/app/apperr/apperr.go`, `internal/app/opgate/opgate.go`, `internal/app/settings/{store.go,service.go}`
- Modify: `internal/app/library/library.go`, `internal/app/session/session.go` (each registers its sentinel errors in an `init`)
- Test: `internal/app/apperr/apperr_test.go`, `internal/app/opgate/opgate_test.go`, `internal/app/settings/{store_test.go,service_test.go}`

**Interfaces:**
- Produces:
  ```go
  package apperr
  type Error struct { Code string `json:"code"`; Message string `json:"message"`; Field string `json:"field,omitempty"`; Reason string `json:"reason,omitempty"` }
  // codes: not_found, not_open, offline, access_lost, read_only, invalid, unavailable, internal
  func New(code, message string) *Error
  func Invalid(field, reason, message string) *Error
  func Register(target error, code, message string) // message "" → the error's own text
  func From(err error) *Error    // *Error passes through (errors.As); a registered sentinel (errors.Is) → its code and message; anything else → internal with err.Error()
  func Marshal(err error) []byte // JSON of From(err); used as application.Options.MarshalError

  package opgate
  type Gate struct{ /* … */ }
  func (g *Gate) Enter() (leave func(), err error) // after Close: apperr unavailable
  func (g *Gate) Close(wait time.Duration) bool    // refuses new entries, waits up to `wait` for those inside; true when none is left

  package settings
  type Body struct { // design SettingsBody
      BucketSize int `json:"bucket_size"`; Similarity float64 `json:"similarity"`; PicksDir string `json:"picks_dir"`
      AdvanceOnAction bool `json:"advance_on_action"`; HideRejectedPhotos bool `json:"hide_rejected_photos"`
      CrossDeviceMerging bool `json:"cross_device_merging"`; QualityRating bool `json:"quality_rating"`; VisualGrouping bool `json:"visual_grouping"`
  }
  type Patch struct { /* the same eight fields as pointers, same JSON names */ }
  type SaveResult struct { Saved bool `json:"saved"`; Reclustered bool `json:"reclustered"` }
  type Store struct{ /* … */ }
  func NewStore() (*Store, error)                   // config.Load()
  func (s *Store) Get() config.Config
  func (s *Store) Apply(p Patch) (old, cur config.Config, err error) // validate, config.Save, swap
  func (s *Store) OnChange(f func(old, cur config.Config))
  type Hooks struct { Recluster func() (bool, error) }               // re-clusters the open folder; false when none is open
  func NewService(s *Store, h Hooks, g *opgate.Gate) *Service
  func (s *Service) Get() (Body, error)
  func (s *Service) Save(p Patch) (SaveResult, error)
  ```
  Registered sentinels: `library.ErrOffline` → `offline`; `library.ErrAccessLost` → `access_lost` with `library.AccessLostMessage`; `library.ErrNotFound` → `not_found`; `library.ErrNotDirectory` → `invalid`; `session.ErrReadOnly` → `read_only` with `session.ReadOnlyMessage`; `session.ErrUnsafeState` → `invalid` with reason `unsafe_state` and `session.UnsafeStateMessage`; `store.ErrNotFound` → `not_found`; `picks.ErrDirHasOriginals` → `invalid` with field `picks_dir`, reason `has_originals`; `safefs.ErrSymlink` → `invalid` with reason `symlink`.
  Validation returns `invalid` with a field and a **reason code** (**[call] C13** — the frontend shows the design's `validateSettings` copy for the reason; the Go `Message` is for logs): `bucket_size` < 60 ⇒ reason `min`; `similarity` outside (0, 1] ⇒ `range`; `picks_dir` through `picks.ValidateDir` ⇒ `empty`, `absolute`, `traversal` or `reserved`. `Save` calls `Hooks.Recluster` when `session.ClusterParams` changed; what else reacts to a change (models, queue, quality) subscribes through `OnChange` in Task 40.

- [ ] **Step 1: Write the failing tests.** `TestMarshal_AppError` (`{"code":"invalid","message":"…","field":"picks_dir","reason":"absolute"}` key set and values). `TestMarshal_PlainErrorIsInternal`. `TestFrom_RegisteredSentinel` (wrapped with `%w` still maps). `opgate`: `TestEnter_AfterCloseIsUnavailable`; `TestClose_WaitsForInFlight` (returns `true` once `leave` is called); `TestClose_TimesOut` (`false` after the wait when an entry never leaves). `TestGet_Defaults` (`600, 0.92, "picks", false, false, false, true, true`). `TestSave_PartialPatchKeepsOtherFields`. `TestSave_Validation` (table: bucket 59 ⇒ `bucket_size`/`min`; similarity 0 and 1.01 ⇒ `similarity`/`range`; picks `""` ⇒ `empty`, `"../x"` and `"a/../../b"` ⇒ `traversal`, `"/Users/x/picks"` ⇒ `absolute`, `"."` and `".choix/x"` ⇒ `reserved`; `"exports/luminar"` accepted). `TestSave_PersistsToTOML`. `TestSave_ReclustersOnlyWhenClusteringChanged`. `TestOnChange_ReceivesOldAndNew`. `TestSave_AfterGateClosedIsUnavailable`. `TestService_ExportedMethods` (`Get`, `Save`).
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/apperr/ ./internal/app/opgate/ ./internal/app/settings/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(settings): settings service, frontend errors and admission gate`.

### Task 36: Catalog — clusters, focus, picks, undo, recluster

**Gate:** none. **Depends on:** Tasks 17, 30, 34, 35.

**Files:**
- Create: `internal/app/catalog/{catalog.go,views.go,format.go,undo.go,refresh.go,service.go}`
- Test: `internal/app/catalog/{catalog_test.go,views_test.go,format_test.go,undo_test.go,refresh_test.go,service_test.go}`

**Interfaces:**
- Consumes: `session.Manager`, `session.Session.{Picks,PicksDir,Recluster,EnsureClustered}`, `picks.Service.Set`, `library.Library.{RefreshStats,RefreshPreviews}`, `local.QualityDisplay`, `local.QualityScorerID`, `group.MergedDeviceKey`.
- Produces (JSON identical to v1's `/api/library` and `/api/clusters/{id}`; these are the design types `Member`, `Cluster`, `ExifPayload`, `FocusResponse`):
  ```go
  type Member struct { FileID int64 `json:"file_id"`; Path string `json:"path"`; ContentHash string `json:"content_hash"`; Kind string `json:"kind"`; Format string `json:"format"`; Picked bool `json:"picked"`; Rejected bool `json:"rejected"`; Quality *float64 `json:"quality,omitempty"` }
  type Cluster struct { ID int64 `json:"id"`; Device string `json:"device"`; Time string `json:"time"`; Members []Member `json:"members"` }
  type FolderView struct { Folder string `json:"folder"`; Picked int `json:"picked"`; Total int `json:"total"`; Clusters []Cluster `json:"clusters"`; Devices []string `json:"devices"`; Failed int `json:"failed"`; PicksDir string `json:"picks_dir"`; PicksBlocked string `json:"picks_blocked,omitempty"`; PicksMissing int `json:"picks_missing"` } // devices: sorted distinct single-camera labels; picks_blocked: the reason code when this folder cannot take picks; picks_missing: picked photos whose copy is no longer in the picks folder (R24 — reported, never repaired; the design has no state for it yet, so no screen shows it until the UI team adds one)
  type Exif struct { /* v1 exifJSON: camera, aperture, shutter, iso, focal_length, captured, size, gps_lat, gps_lon */ }
  type FocusResponse struct { Cluster Cluster `json:"cluster"`; ClusterIndex int `json:"cluster_index"`; ClusterCount int `json:"cluster_count"`; Exif map[string]Exif `json:"exif"`; NextClusterID int64 `json:"next_cluster_id"`; PrevClusterID int64 `json:"prev_cluster_id"` }
  type PickResult struct { FileID int64 `json:"file_id"`; State string `json:"state"`; ExportedPath string `json:"exported_path,omitempty"` } // state picked|rejected|unmarked
  type Deps struct {
      Sessions *session.Manager; Library *library.Library; Settings func() config.Config
      EnqueueFront func(folderID string) // the runtime binds it to queue.EnqueueFront(id, session.JobFull): opening a folder rescans it
      Emit events.Emitter
      Stills platform.Stills; PreviewsDir func(id string) (string, error)
      RefreshDelay time.Duration // 500 ms
      Context context.Context    // the runtime's root context; cancelled at shutdown
  }
  type Catalog struct{ /* … */ }
  func New(d Deps) *Catalog
  func (c *Catalog) Open(ctx context.Context, folderID string) (FolderView, error) // closes the previously open folder; holds one session reference; EnsureClustered; EnqueueFront(folderID)
  func (c *Catalog) Close(folderID string)                                          // flushes a pending refresh, drops the undo stack, releases the reference
  func (c *Catalog) CloseAll()
  func (c *Catalog) OpenFolder() string
  func (c *Catalog) Clusters(folderID string) (FolderView, error)
  func (c *Catalog) Cluster(folderID string, clusterID int64) (FocusResponse, error)
  func (c *Catalog) SetPick(folderID string, fileID int64, action string) (PickResult, error) // pick|unpick|reject|unreject
  func (c *Catalog) Undo(folderID string) (PickResult, error)                                 // [call] C22; apperr not_found "nothing to undo" on an empty stack
  func (c *Catalog) Recluster(ctx context.Context, folderID string) error                     // emits folder:changed {clusters}
  func (c *Catalog) Ratings(folderID string) (map[string]float64, error)                      // decimal file id → 0–10 rating; empty when quality_rating is off
  // Service: bound methods with the same names and parameters without ctx — Open, Close, Clusters, Cluster, SetPick, Undo, Recluster, Ratings
  ```
  `views.go` and `format.go` are `internal/server/library.go`'s `buildLibraryJSON`, `handleClusterJSON`, `memberQuality`, `formatDeviceLabel`, `formatClusterDeviceLabel`, `formatBucketLabel` moved without behavioural change. A method on a folder that is not open returns `not_open`.
  **Serialisation (**[call] C4**):** `SetPick`, `Undo` and `Recluster` for one folder run under that folder's mutex, each holding its own session reference (`AcquireOpen`) for the whole operation, so a `Close` or shutdown that arrives meanwhile waits for it. When the folder's root no longer exists, `SetPick` returns `offline`.
  **Refresh (**[call] C21**):** after `RefreshDelay` without a further pick, reject or undo in a folder, one `RefreshStats` and one `RefreshPreviews` run under a session reference and `Deps.Context`. `Close` runs a pending refresh at once and waits for it. `CloseAll` (shutdown) waits for in-flight picks, runs only `RefreshStats` for a refresh that had not started, lets a running `RefreshPreviews` end on its cancelled context (its previous files stay intact), then releases the references.

- [ ] **Step 1: Write the failing tests.** Port `internal/server/library_test.go` and `picks_test.go` cases to `views_test.go` / `catalog_test.go` with the same expectations (JSON key sets included). Add: `TestOpen_ClosesPreviousFolder` (bookmark access for A is stopped once B is open and no job holds A). `TestOpen_EnqueuesFront`. `TestOpen_ReportsPicksBlocked` (configured picks dir holds originals ⇒ `picks_blocked == "has_originals"`; `SetPick(pick)` ⇒ `invalid`/`has_originals`; `SetPick(reject)` still works). `TestOpen_LegacyPicksDir` (the v1 fixture ⇒ `picks_dir == "exports/out"`; unpick removes the old copy; a new pick lands under `exports/out/`). `TestOpen_ReportsMissingPicksWithoutCopying` (one exported copy deleted before opening ⇒ `picks_missing == 1`; the copy is not recreated; unpicking that photo succeeds). `TestClusters_NotOpen` (`code == "not_open"`). `TestSetPick_CopiesIntoPicksDir` (bytes equal; the original's mtime and bytes unchanged). `TestSetPick_UsesDurableCommit` (the picks trace ends with `tx-commit` after `dir-sync`). `TestSetPick_UnknownAction` (`invalid`). `TestSetPick_FolderUnplugged` (Review Focus 1: remove the root directory ⇒ `code == "offline"`, no panic). `TestSetPick_ConcurrentMutationsStayConsistent` (50 goroutines issuing random pick/unpick/reject/unreject/undo over 5 files, under `-race`: afterwards every `picked` row has exactly its copy on disk with matching bytes, no other file exists under the picks dir, and every non-picked row has `exported_path` NULL). `TestClose_WaitsForInFlightPick` (a pick held inside the fake copy; `Close` returns only after it finishes; the store is open until then). `TestUndo_RestoresPreviousState` (unmarked → pick → reject, then two undos ⇒ `picked`, then `unmarked`; the copy is removed by the second). `TestUndo_EmptyStack`. `TestUndo_ClearedOnClose`. `TestUndo_CappedAt100`. `TestRecluster_EmitsFolderChanged`. `TestRatings_OffIsEmpty`. `TestRefresh_DebouncedStatsAndPreviews` (ten picks ⇒ one `library:changed {stats}`; a reject of a sampled file changes the previews stamp). `TestRefresh_FlushedOnClose`. `TestService_ExportedMethods`.
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race -count=3 ./internal/app/catalog/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(catalog): clusters, focus payloads, serialised durable picks with undo`.

### Task 37: Media handler

**Gate:** spike S3 for the video route. **Depends on:** Tasks 6, 20, 30, 35.

**Files:**
- Create: `internal/app/media/{handler.go,safejoin.go,urls.go}`
- Test: `internal/app/media/{handler_test.go,safejoin_test.go,urls_test.go}`

**Interfaces:**
- Produces (**[call] C14** — the service is registered with `Route: "/media"`, which Wails strips):
  ```go
  type Handler struct{ /* unexported */ }
  func New(root context.Context, sessions *session.Manager, stills platform.Stills, previewsDir func(folderID string) (string, error), g *opgate.Gate) *Handler
  func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request)
  func ThumbURL(folderID, relPath, tier, contentHash string) string // /media/thumb/<id>/<escaped path>?tier=<tier>&v=<hash>
  func FullURL(folderID, relPath, contentHash string) string        // /media/full/<id>/<escaped path>?v=<hash>
  func PreviewURL(folderID string, n int, stamp int64) string       // /media/preview/<id>/<n>?v=<stamp>
  ```
  Every request enters the gate (503 after shutdown began) and, for `thumb` and `full`, holds a session reference (`AcquireOpen`; 404 when the folder is not open) until the response is written, including a transcode — so a store is never closed under a request. Request contexts are derived from the runtime's root context (the handler wraps `r.Context()`), so shutdown cancels requests and transcodes in flight. Routes (GET and HEAD only, else 405): `/thumb/<folder-id>/<rel-path>?tier=thumb|preview`, `/full/<folder-id>/<rel-path>`, `/preview/<folder-id>/<n>`. Folder ids must match `^[0-9a-f]{16}$` (else 404). Files are resolved with `Files().GetByPath(relPath)` — never by joining the URL path — then `safeJoinUnderRoot` (moved verbatim from `internal/server/full.go`). `thumb`: the `Thumbs().Get` row; a video with no `preview` row falls back to its `thumb` row; ETag and `Cache-Control: public, max-age=86400` as v1. `full`: a photo whose format is not `jpeg` or `png` is transcoded once to `<id>-full.jpg` with `thumb.Photo(s.Context(ctx), stills, src, 4096)` (sized by the source kind's rule, Task 20) + `thumb.WriteCache`, under `singleflight` and a semaphore of 2, falling back to the `preview` tier; everything else is the original through `http.ServeContent` (Range). Content types: `jpeg`→`image/jpeg`, `png`→`image/png`, `heic`→`image/heic`, `mov`→`video/quicktime`, `mp4`→`video/mp4`, else `application/octet-stream`. `preview`: `n` in 0…5 (else 404), file `<previewsDir>/<n>.jpg`, served even when the folder is offline or closed.

- [ ] **Step 1: Write the failing tests.** Port `internal/server/full_test.go`, `thumb_test.go` and the `safeJoinUnderRoot` tests with the new URL shapes. Add: `TestThumb_FolderNotOpenIs404`. `TestThumb_BadFolderID` (`../x`, `ABCDEF0123456789`, 15 characters ⇒ 404). `TestThumb_TraversalIs404` (`/thumb/<id>/../../etc/passwd` and the percent-encoded form). `TestThumb_SymlinkedCacheEscapesIs403`. `TestThumb_BadTierIs400`. `TestThumb_VideoPreviewFallsBackToThumb`. `TestFull_VideoRange` (`Range: bytes=10-19` on a 100-byte `.mp4` ⇒ 206, `Content-Range: bytes 10-19/100`, `Content-Type: video/mp4`). `TestFull_HEICTranscodesOnce` (five concurrent requests ⇒ the fake `Stills` called once, with `(4096, FitLongSide)`). `TestFull_RAFTranscodesByWidth` (`(4096, FitWidth)` on the embedded JPEG). `TestFull_RootContextCancelsTranscode` (cancelling the root context ends a gated transcode; the response is an error and the session reference is released). `TestFull_TranscodeFailureFallsBackToPreview`. `TestFull_TranscodeHoldsSession` (the fake transcode is gated; releasing the UI's reference meanwhile does not close the store until the response is written). `TestFull_NeverOpensOriginalForWrite`. `TestPreview_ServedWhileFolderClosed`. `TestPreview_OutOfRange` (`6`, `-1`, `x` ⇒ 404). `TestMethod_PostIs405`. `TestAfterGateClosedIs503`. `TestURLs_EscapePath` (`a b/ü#1.jpg` ⇒ `/media/thumb/<id>/a%20b/%C3%BC%231.jpg?tier=thumb&v=h`).
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/media/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(media): thumbnails, originals and library previews over the asset server`.

### Task 38: Library and models bindings

**Gate:** none. **Depends on:** Tasks 25, 29, 31, 33, 34, 35.

**Files:**
- Create: `internal/app/library/{service.go,summary.go}`, `internal/app/models/service.go`
- Test: `internal/app/library/{service_test.go,summary_test.go}`, `internal/app/models/service_test.go`

**Interfaces:**
- Produces:
  ```go
  // library — design FolderSummary
  type FolderSummary struct {
      ID string `json:"id"`; Name string `json:"name"`; ParentPath string `json:"parentPath"`; State string `json:"state"`
      Month *string `json:"month"`; FirstCaptured *string `json:"firstCaptured"`; LastCaptured *string `json:"lastCaptured"`
      Photos int `json:"photos"`; Videos int `json:"videos"`; Clusters int `json:"clusters"`; Picked int `json:"picked"`; Rejected int `json:"rejected"`
      Previews []string `json:"previews"`; Scan *events.Progress `json:"scan,omitempty"`; VolumeName string `json:"volumeName,omitempty"`; Error string `json:"error,omitempty"`
  }
  type JobStates interface { State(folderID string) (state string, p *events.Progress) }
  type FolderPicker interface { PickFolders() ([]string, error) }          // implemented by internal/app/dialogs (gated) and by testsupport
  type Confirmer interface { ConfirmRemove(folderName string) (bool, error) }
  type ServiceDeps struct { Library *Library; Jobs JobStates; Enqueue func(id string, full bool) error; Picker FolderPicker; Confirm Confirmer; Home string; PreviewURL func(id string, n int, stamp int64) string; Gate *opgate.Gate }
  func NewService(d ServiceDeps) *Service
  func Summarise(f Folder, jobState string, p *events.Progress, home string, previewURL func(string, int, int64) string) FolderSummary
  func (s *Service) List() ([]FolderSummary, error)       // newest month first, undated last, then by firstCaptured descending
  func (s *Service) AddFolders() ([]AddResult, error)     // Picker, then — after re-checking the gate — Add for each path; a new folder is enqueued as full; cancelled picker ⇒ empty slice
  func (s *Service) Remove(id string) (bool, error)       // Confirmer; false when declined; then — after re-checking the gate — Library.Remove (whose OnRemoved cancels the job)
  func (s *Service) Rescan(id string) error               // Enqueue(id, true); offline ⇒ apperr offline
  func (s *Service) CheckNow() error                      // CheckPresence
  // Go-only entry for dropped folders (called by the gated window package, never bound):
  func (l *Library) AddPaths(paths []string, enqueue func(id string)) []AddResult
  // models
  func NewService(m *Manager, quality func() events.Quality, g *opgate.Gate) *Service
  func (s *Service) Status() ([]events.ModelStatus, error) // [call] C35: every binding can answer `unavailable` while the app quits
  func (s *Service) Retry(kind string) error               // "clip" | "quality"; anything else ⇒ invalid
  func (s *Service) Quality() (events.Quality, error)
  ```
  The picker and the confirmer block on the main thread, and shutdown runs there: a binding that was waiting in a dialog when shutdown began must not continue. `AddFolders` and `Remove` therefore leave the gate while the dialog is up and **enter it again** when it returns; a closed gate ⇒ `unavailable` and nothing is added or removed.
  `Summarise`: `state` — presence `offline` ⇒ `offline`; else job `scanning` / `queued`; else `job_pending` (present, pending, not yet re-queued — e.g. just after a suspension) ⇒ `queued`; else presence `missing` ⇒ `error` with `MissingMessage`; else presence `access_lost` ⇒ `error` with `AccessLostMessage`; else `job_error != ""` ⇒ `error`; else `ready`. A folder with pending work is therefore never reported `ready`. `month` = `YYYY-MM` of `first_captured` in UTC, `null` when undated; `firstCaptured`/`lastCaptured` RFC 3339 UTC; `parentPath` = the folder's parent with a leading `home` replaced by `~`; `previews` = `PreviewURL(id, i, stamp)` for `i < Previews`; `volumeName` only when offline.

- [ ] **Step 1: Write the failing tests.** `TestSummarise_States` (table for the eight rows above; a scanning folder with an old `job_error` is `scanning`; a present folder with `job_pending` and no queue state is `queued`, never `ready`; `missing` ⇒ `error` with `MissingMessage`). `TestSummarise_MonthAndRange` (first 2026-03-04T08:00:00Z, last 2026-03-06T19:30:00Z ⇒ `month "2026-03"`, both strings RFC 3339; undated ⇒ all three `null` in JSON). `TestSummarise_ParentPathAbbreviated` (`/Users/tester/Photos/Trip`, home `/Users/tester` ⇒ `~/Photos`; `/Volumes/SD/Trip` ⇒ `/Volumes/SD`). `TestAddFolders_GateClosedWhileDialogOpen` (the gate closes while the fake picker blocks; the picker then returns two paths ⇒ `unavailable`, nothing added). `TestRemove_GateClosedWhileConfirming` (⇒ `unavailable`, the folder is still there). `TestModels_StatusAfterGateClosed` (`unavailable`). `TestSummarise_PreviewURLs`. `TestSummarise_JSONKeys` (exactly the design type's keys; `scan`, `volumeName`, `error` omitted when empty). `TestList_Order`. `TestAddFolders_EnqueuesNewOnly`. `TestAddFolders_Cancelled`. `TestRemove_Declined`. `TestRemove_CancelsJobThroughOnRemoved`. `TestRescan_Offline`. `TestModelsRetry_UnknownKind`. `TestService_ExportedMethods` for both (`List, AddFolders, Remove, Rescan, CheckNow`; `Status, Retry, Quality`).
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/library/ ./internal/app/models/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(app): library and models bindings`.

### Task 39: System service

**Gate:** none. **Depends on:** Tasks 9, 16, 28, 30, 35.

**Files:**
- Create: `internal/app/system/service.go`, `internal/buildinfo/buildinfo.go` (`var Version, Commit = "0.0.0-dev", "unknown"`, set with `-ldflags -X`)
- Test: `internal/app/system/service_test.go`

**Interfaces:**
- Produces (**[call] C6**):
  ```go
  type MenuContext struct { Screen string `json:"screen"`; HasSelection bool `json:"hasSelection"`; Flags []string `json:"flags"` } // screen: design ScreenId
  type MenuState interface { // implemented by internal/app/menu (gated); a no-op implementation until then and in server builds
      SetContext(c MenuContext)
      PopupFolderMenu(folderID string, flags []string, x, y int) // opens the native context menu for a folder row at window coordinates
      SingleKeysNative() bool
  }
  type Info struct { Name string `json:"name"`; Version string `json:"version"`; Commit string `json:"commit"`; Sandboxed bool `json:"sandboxed"`; SingleKeysNative bool `json:"singleKeysNative"` }
  type Deps struct {
      Library *library.Library; Sessions *session.Manager; Settings func() config.Config
      FolderState func(id string) (state string, err error) // ready|scanning|queued|offline|error; the runtime (Task 40) supplies it from the library summary
      Workspace platform.Workspace; MenuState MenuState; Gate *opgate.Gate
  }
  func NewService(d Deps) *Service
  func (s *Service) Info() (Info, error)                    // [call] C35
  func (s *Service) OpenPicksFolder(folderID string) error  // under a session reference held until Workspace.OpenFolder has returned: the folder's effective picks dir, created through safefs if missing
  func (s *Service) RevealFolder(folderID string) error     // under Library.Acquire, released only after Workspace.Reveal(folder path) has returned (the platform call waits for AppKit, Task 12)
  func (s *Service) OpenHelp() error                        // Workspace.OpenURL("https://github.com/volodymyrsmirnov/choix") — the only URL the frontend can open
  func (s *Service) SetMenuContext(c MenuContext) error     // screen must be library|folder|focus|compare|settings, else invalid
  func (s *Service) ShowFolderMenu(folderID string, x, y int) error // flags from FolderState ("offline", "scanning"); MenuState.PopupFolderMenu — the one path for both right-click and the ⋯ button
  ```

- [ ] **Step 1: Write the failing tests.** `TestOpenPicksFolder_CreatesAndOpens` (directory created inside the root; the fake `Workspace` got `OpenFolder(root/picks)`; references released). `TestOpenPicksFolder_LegacyDir` (the v1 fixture ⇒ `exports/out`). `TestOpenPicksFolder_SymlinkedPicksRefused` (`picks -> ..` ⇒ `invalid`/`symlink`; nothing opened). `TestOpenPicksFolder_Offline` (`code == "offline"`, nothing opened). `TestRevealFolder_UnderAccess` (the fake `Workspace` saw `Reveal(path)` while `Bookmarks.Active()` contained the path, **and the path was still active when the fake's `Reveal` returned** after a 100 ms delay; empty afterwards). `TestOpenPicksFolder_AccessHeldUntilWorkspaceReturns`. `TestInfo_AfterGateClosed` (`unavailable`). `TestRevealFolder_UnknownID` (`not_found`). `TestOpenHelp_FixedURL`. `TestSetMenuContext_RejectsUnknownScreen`. `TestShowFolderMenu_FlagsFromState` (offline folder ⇒ flags `["offline"]`; scanning ⇒ `["scanning"]`; ready ⇒ none; coordinates passed through). `TestShowFolderMenu_UnknownFolder` (`not_found`). `TestService_ExportedMethods`.
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race ./internal/app/system/` — expected: PASS.
- [ ] **Step 5: Commit** `feat(system): Finder integration, folder menu and app info`.

### Task 40: Runtime — wiring, start-up and teardown order

**Gate:** none. **Depends on:** Tasks 26, 29, 31, 32, 33, 34, 35, 36, 37, 38, 39.

**Files:**
- Create: `internal/app/runtime/{runtime.go,logging.go}`
- Test: `internal/app/runtime/runtime_test.go`

**Interfaces:**
- Produces:
  ```go
  type Deps struct {
      Bookmarks platform.Bookmarks; Volumes platform.Volumes; Workspace platform.Workspace
      Stills platform.Stills; Video platform.Video
      Downloader models.Downloader
      Emit events.Emitter // if it also has Close(), shutdown step 2 calls it (cmd/choix's emitter does)
      Picker library.FolderPicker; Confirm library.Confirmer; MenuState system.MenuState
      Home string; CPUOnly map[local.ModelKind]bool
      Embedder local.Embedder; CropInferer local.CropInferer // nil → the inference.Engine; tests inject fakes
  }
  type Runtime struct {
      Library *library.Service; Catalog *catalog.Service; Settings *settings.Service; Models *models.Service; System *system.Service
      Media *media.Handler
      // unexported: the cores and the gate
  }
  func New(d Deps) (*Runtime, error)       // opens library.db (appdir.LibraryDB), loads settings, builds every core; starts nothing
  func (r *Runtime) Start(ctx context.Context) // coalescer loop, presence check then RunPresence(30 s), models.Start, queue.Start
  func (r *Runtime) SetVisible(v bool)
  func (r *Runtime) AddDropped(paths []string) []library.AddResult // goes through the gate: after shutdown has begun it adds nothing and returns nil
  func (r *Runtime) Idle() bool            // no job queued or running, no download in flight
  const QuitDeadline = 5 * time.Second     // everything
  const NativeQuitWait = 2 * time.Second   // abandoned native calls, inside the deadline
  func (r *Runtime) Shutdown()             // idempotent; returns within QuitDeadline plus scheduling slack
  func SetupLogging() (io.Closer, error)   // slog text handler → appdir.Logs()/choix.log (truncated when > 5 MB) and stderr
  ```
- Subscriptions made in `New`:
  - settings `OnChange` → `models.Refresh()`, `quality.SettingChanged()` (off cancels the pass in flight at once); `quality_rating` switched on with the model ready, or `visual_grouping` switched on with CLIP ready → `queue.EnqueueAllReachable(JobBackfill)`;
  - models `OnDownloaded(kind)` → `queue.EnqueueAllReachable(JobBackfill)` — **not** for a model that was already on disk at launch (**[call] C17**); every model status change → `quality.Changed()`;
  - library `OnOffline(id)` → `queue.Suspend(id)`, `catalog.Close(id)`; `OnRemoved(id)` → `queue.Cancel(id)`, `catalog.Close(id)`; `OnOnline(id)` → `queue.Resume(id)`;
  - queue `OnSuspended(id)` → `library.CheckFolder(ctx, id)` at once (the row turns `offline`/`missing`, or — if the folder is in fact present — the pass's `OnOnline` re-queues it);
  - catalog `EnqueueFront(id)` → `queue.EnqueueFront(id, session.JobFull)` (opening a folder rescans it);
  - job `AfterJob` → `RefreshStats`, `RefreshPreviews`.
- `Shutdown` (R17). It is called on Wails' main thread (`app.OnShutdown` hooks run under `InvokeSync`), so the work is done by a **drain goroutine** and the main thread only waits for it, up to `QuitDeadline`. Nothing in the drain may need the main thread.
  1. **Refuse.** The gate closes: new bindings, media requests and `AddDropped` are refused. A binding that is waiting in a native dialog re-checks the gate when the dialog returns and does nothing (Task 38).
  2. **Cancel.** The runtime's root context is cancelled: the running job, the quality pass, presence, model downloads, in-flight media requests and transcodes, and catalog refreshes all receive it. A refresh that had not started runs its cheap half only (`RefreshStats`, one SQL query) and skips the previews; the next job or open redoes them.
  3. **Silence.** The emitter is closed — from here nothing calls into Wails.
  4. **Drain** (off the main thread): `queue.Stop()` (joins the worker; an in-flight inference is awaited), `catalog.CloseAll()` (waits for in-flight picks — a pick is never cut — then drops its references), `models.Stop()`, then `sessions.Shutdown(ctx)`: each session is closed as soon as it has no reference and no detached native call; abandoned native calls get `NativeQuitWait`.
  5. **Close what is free.** `inference.Close()` once no inference is running; `library DB.Close()` once nothing uses it.
  6. **Deadline.** When `QuitDeadline` passes first, `Shutdown` returns anyway and logs what is still in use. **Nothing that is still referenced is closed**: those stores, bookmarks and ONNX sessions stay open and the process exits with them (SQLite's WAL is crash-safe; originals are only ever read; every pick that returned success is already durable).

- [ ] **Step 1: Write the failing tests** (`CHOIX_DATA_DIR` = temp dir, platform fakes, fake downloader and inference; no Wails). `TestRuntime_AddScanOpenPick` (pick a folder of 12 generated JPEGs through the fake picker → wait `Idle()` → `Library.List()` shows `state "ready"`, `photos 12`, six preview URLs → `Catalog.Open` returns clusters → `SetPick(pick)` → file in `picks/` → `Shutdown`). `TestRuntime_EventsOnTheWire` (`library:changed added`, `scan:state queued`, `scanning`, `ready`, at least one `scan:progress`, `models:progress`). `TestRuntime_ModelDownloadedQueuesBackfill` (CLIP finishes downloading after the first job ⇒ a second job runs and embeddings exist). `TestRuntime_ModelPresentAtLaunchQueuesNothing` (both models on disk, one finished folder ⇒ after `Start` the fake `Stills` saw no call and `Idle()` is true at once). `TestRuntime_VisualGroupingOffNeverDownloadsClip`. `TestRuntime_QualityOffStopsScoringAtOnce` (a pass held by the fake crop inferer; `Settings.Save({quality_rating: false})` ⇒ within 1 s the status is `disabled` and no further photo is scored; the job still ends `ready`). `TestRuntime_UnplugAndReplug` (Review Focus 1: unmount the volume mid-scan ⇒ **without any presence tick** the row is `offline` in `Library.List()` (the suspension triggered `CheckFolder`), `job_pending == 1`, queue idle; remount + `CheckNow` ⇒ the job runs again and ends `ready`). `TestRuntime_ReplugBeforeAnyPresencePass` (the directory disappears and reappears while the job is held, so the job suspends but presence never saw it gone ⇒ after the suspension's `CheckFolder` the folder is `queued` again and finishes; `Library.List()` never reports `ready` while `job_pending == 1`). `TestRuntime_MovedWhileScanning` (rename the folder mid-scan ⇒ after the next presence pass the job resumes at the new path and ends `ready`). `TestRuntime_OpenEnqueuesFullJob`. `TestRuntime_QueuedRescanSurvivesRestart` (enqueue a rescan of a finished folder behind a held job, `Shutdown`, `New` + `Start` ⇒ both run, in the order they were enqueued). `TestRuntime_ShutdownOrder` (instrumented fakes record: gate closed → root context cancelled → emitter closed → queue stopped → sessions closed (bookmark access empty) → inference closed; nothing touches a store after it is closed; `-race`). `TestRuntime_ShutdownWithNativeCallInFlight` (a `Stills` call that never returns ⇒ `Shutdown` returns in under `QuitDeadline` + 1 s; **that folder's store is still open and its bookmark still active**; every other store is closed). `TestRuntime_ShutdownWithBindingStuckInDialog` (a `Remove` blocked in the fake confirmer ⇒ `Shutdown` returns within the deadline; when the confirmer then answers "yes", the folder is **not** removed and the call returns `unavailable`). `TestRuntime_AddDroppedAfterShutdownAddsNothing`. `TestRuntime_ShutdownDuringPickKeepsPick` (a pick held mid-copy when `Shutdown` starts ⇒ the pick completes, and after it a fresh `store.Open` shows the pick and its copy exists). `TestRuntime_ShutdownDoesNotBlockTheCallerOnDrain` (the drain is observed running on another goroutine while the caller waits). `TestRuntime_BindingsAfterShutdownAreUnavailable` (`Library.List`, `Models.Status`, `Models.Quality`, `System.Info`, `Catalog.Clusters`). `TestRuntime_NoEmitAfterShutdown`. `TestRuntime_ShutdownDuringDownloadLeavesPartFile`. `TestRuntime_RestartRestoresLibrary` (a second `New` lists the folder with its counts without scanning). `TestRuntime_RemovedFolderMidScan` (delete the directory, `CheckNow` ⇒ row gone, event `gone`, queue idle, goroutine count back to the baseline).
- [ ] **Step 2: Run** `go test ./internal/app/runtime/` — expected: FAIL. **Step 3: Implement.** **Step 4: Run** `go test -race -count=3 ./internal/app/... && make lint` — expected: clean.
- [ ] **Step 5: Commit** `feat(app): runtime wiring with ordered, bounded shutdown`.

---

## Phase 5 — Wails shell (ungated parts)

### Task 41: `cmd/choix` on Wails — app, services, events

**Gate:** none. **Depends on:** Tasks 10, 11, 12, 13, 14, 40 (it constructs the five real platform implementations).

**Files:**
- Create: `embed.go`, `build/frontend-stub/index.html` (a dark blank page titled "Choix"), `cmd/choix/{events.go,emitter.go,shell_desktop.go}`
- Modify: `cmd/choix/main.go` (replaced), `go.mod` / `go.sum` (`go get github.com/wailsapp/wails/v3@v3.1.0`), `.gitignore` (`frontend/dist/`, `bin/`, `.tmp/`), `Makefile` (a `bootstrap` target that creates, when missing, **both** embedded-asset placeholders — `frontend/dist/index.html` from `build/frontend-stub/index.html`, and v1's `internal/ui/web/dist/index.html` (`internal/ui/ui.go` embeds `all:web/dist`; nothing under it is tracked, and `ui_test.go` requires `<div id="root">`, so the placeholder is `<!doctype html><div id="root"></div>`) — made a prerequisite of `build`, `test`, `lint` and `vet`, so a clean checkout builds and tests between this task and Task 47 without `make webui`; `make webui` and `make build` still overwrite the v1 placeholder with the real bundle; the Makefile goes away in Task 62), `.github/workflows/ci.yml` (run `make bootstrap` before the Go steps)
- Test: `cmd/choix/{events_test.go,emitter_test.go}`

**Interfaces:**
- `embed.go` (**[call] C23**): `package choix`; `//go:embed all:frontend/dist`; `func Frontend() fs.FS` (rooted at `frontend/dist`). Any command that loads Go packages needs `frontend/dist/index.html` to exist; from this task on `make bootstrap` (a prerequisite of the Makefile's Go targets) creates it, and Task 47's `frontend:bootstrap` takes over.
- `main.go`, in order: `runtime.SetupLogging()`; build the five `darwin` platform values and `local.NewInstaller(appdir.Models())`; `app := application.New(application.Options{Name: "Choix", Description: "Photo and video culling", MarshalError: apperr.Marshal, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(choix.Frontend())}, Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true}, SingleInstance: &application.SingleInstanceOptions{UniqueID: "com.github.volodymyrsmirnov.choix", OnSecondInstanceLaunch: shell.Focus}, Server: application.ServerOptions{Host: "127.0.0.1", Port: 8080}})` (**[call] C28**); `rt, err := runtime.New(deps)` with `Emit: emitter`; **`app.OnShutdown(rt.Shutdown)`** (the runtime exists before its hook is registered); `app.RegisterService(application.NewService(rt.Library))`, the same for `rt.Catalog`, `rt.Settings`, `rt.Models`, `rt.System`, and `application.NewServiceWithOptions(rt.Media, application.ServiceOptions{Route: "/media"})`; `shell.Attach(app, rt)`; `rt.Start(ctx)`; `app.Run()`.
- `emitter.go`: `type emitter struct{ … }` implementing `events.Emitter` over `app.Event.Emit`; `Close()` makes every later `Emit` a no-op (the runtime calls it as shutdown step 2 through a `Closer` it is given in `Deps.Emit`).
- `events.go`: one `init` with seven direct calls, e.g. `application.RegisterEvent[events.ScanProgressEvent](events.ScanProgress)` — `LibraryChangedEvent`, `ScanProgressEvent`, `ScanStateEvent`, `ModelStatus` (`models:progress`), `Quality` (`quality:status`), `FolderChangedEvent`, `MenuActionEvent`. Values are emitted by value, never by pointer (Wails matches the type exactly).
- `shell_desktop.go` (`//go:build !server`): `Attach` installs nothing yet; dialogs return `apperr.New("unavailable", …)`; the menu state is a no-op. Phase 6 fills it (**[call] C24**).

- [ ] **Step 1: Write the failing tests.** `TestEvents_EveryNameRegistered` (the source of `events.go` contains one `RegisterEvent` call per name in `events.Names()`). `TestEmitter_NoOpAfterClose` (with a fake sink).
- [ ] **Step 2: Run** `go test ./cmd/choix/` — expected: FAIL.
- [ ] **Step 3: Implement**, including the Makefile's `bootstrap` target.
- [ ] **Step 4: Run** `make build && make test && make lint`, then prove the clean-checkout path: `git worktree add "$TMPDIR/choix-clean" HEAD && make -C "$TMPDIR/choix-clean" lint && make -C "$TMPDIR/choix-clean" test && git worktree remove --force "$TMPDIR/choix-clean"` (after committing; neither `frontend/dist` nor `internal/ui/web/dist` exists there, and `make webui` is not run) — expected: clean every time, including `internal/ui`'s `#root` test against the placeholder.
- [ ] **Step 5: Commit** `feat(shell): Wails application with services and typed events`.

### Task 42: Headless self-check

**Gate:** none. **Depends on:** Task 41.

**Files:**
- Create: `cmd/choix/selfcheck.go`
- Modify: `cmd/choix/main.go` (if `CHOIX_SELFCHECK=1`: run it right after `SetupLogging` and exit)
- Test: `cmd/choix/selfcheck_test.go`

**Interfaces:**
- `selfCheck(w io.Writer) (ok bool)` prints one JSON object: `version`, `dataDir`, `dataDirWritable`, `sandboxed`, `onnxRuntime` (version string or `error: …`), `providers` (`{"clip": "coreml"|"cpu"|"skipped: model missing"|"error: …", "quality": …}` — each present model runs one inference), `bookmarkRoundTrip` (`Create`, `Resolve`, `StartAccess`, `Refresh` on `appdir.Root()`), `trashCheck` (`Volumes.InTrash(appdir.Root())` is `false` with no error). Exit code 0 only when nothing is an error. It reads and writes only inside the data directory and changes no state (**[call] C24**).

- [ ] **Step 1: Write the failing tests.** `TestSelfCheck_ReportsDataDir` (`CHOIX_DATA_DIR` = temp ⇒ valid JSON, `dataDirWritable: true`, `bookmarkRoundTrip: true`, `trashCheck: true`). `TestSelfCheck_MissingRuntimeIsError` (no library reachable ⇒ `onnxRuntime` starts with `error:`, `ok == false`). `TestSelfCheck_ModelsMissingAreSkipped` (not an error).
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./cmd/choix/ && CHOIX_DATA_DIR=$(mktemp -d) CHOIX_SELFCHECK=1 CHOIX_ONNXRUNTIME_DYLIB=$PWD/build/.tools/onnxruntime/lib/libonnxruntime.dylib go run ./cmd/choix` — expected: PASS; the run prints `"onnxRuntime":"1.25.0"` and exits 0.
- [ ] **Step 5: Commit** `feat(shell): headless self-check`.

### Task 43: Server build and test support

**Gate:** none. **Depends on:** Task 41.

**Files:**
- Create: `cmd/choix/shell_server.go` (`//go:build server`), `internal/app/testsupport/{service.go,dialogs.go}` (`//go:build server`)
- Test: `internal/app/testsupport/service_test.go` (`//go:build server`)

**Interfaces:**
- `shell_server.go`: registers `testsupport.Service`, passes `testsupport.Dialogs` as picker and confirmer, and serves `os.DirFS($CHOIX_ASSETS_DIR)` instead of the embedded assets when that variable is set.
- `testsupport` (server builds only; bound):
  ```go
  func New(rt *runtime.Runtime, d *Dialogs, emit events.Emitter, root string) *Service // root = $CHOIX_E2E_ROOT; empty ⇒ every path is refused
  func (s *Service) AddFolderPath(path string) (library.AddResult, error) // path must be inside root after EvalSymlinks, else invalid; adds and enqueues a full job
  func (s *Service) SetPickerPaths(paths []string) error                  // what the next AddFolders() "picks"; same root rule
  func (s *Service) SetConfirmAnswer(yes bool)
  func (s *Service) WaitIdle(timeoutMs int) bool
  func (s *Service) EmitMenuAction(id, data string)
  ```

- [ ] **Step 1: Write the failing tests.** `TestAddFolderPath_OutsideRootIsInvalid`, `TestAddFolderPath_SymlinkOutOfRootIsInvalid`, `TestAddFolderPath_EmptyRootRefusesEverything`, `TestSetPickerPaths_FeedsAddFolders`, `TestSetConfirmAnswer_FeedsRemove`.
- [ ] **Step 2: Run** `go test -tags server ./internal/app/testsupport/` — expected: FAIL. **Step 3: Implement.**
- [ ] **Step 4: Run** `go build -tags server ./cmd/choix && go test -race -tags server ./internal/app/testsupport/`; then `CHOIX_DATA_DIR=$(mktemp -d) go run -tags server ./cmd/choix & sleep 5; curl -s http://127.0.0.1:8080/health; kill %1` — expected: clean; health prints `{"status":"ok"}`.
- [ ] **Step 5: Commit** `feat(shell): server build with test-support bindings`.

### Task 44: Confinement tests

**Gate:** none. **Depends on:** Task 41.

**Files:**
- Test: `internal/app/nativeui_test.go` (package `app_test`)

**Interfaces:**
- The test parses every non-test `.go` file under `cmd/` and `internal/` outside `internal/app/{menu,dialogs,window}`. In a file that imports `github.com/wailsapp/wails/v3/pkg/application` it fails on any use of the identifiers `NewMenu`, `NewContextMenu`, `NewMenuItem`, `NewMenuItemSeparator`, `NewMenuItemCheckbox`, `NewMenuItemRadio`, `NewSubMenuItem`, `NewSubmenu`, `NewRole`, `NewMenuFromItems`, `WebviewWindowOptions`, `MacWindow`, `OpenFileDialogOptions`, `MessageDialog`, and on the selectors `.Window`, `.Menu`, `.ContextMenu`, `.Dialog`, `.Env`, `.Browser` (so `cmd/choix` never names a field `Menu`, `Window` or `Dialog` — the runtime's field is `MenuState`). It also fails when a package other than `cmd/choix` and the three gated ones imports Wails.

- [ ] **Step 1: Write the tests** `TestNativeUIConstructorsAreConfined` and `TestWailsImportIsConfined`, each with a self-check: an in-memory source that uses `app.Window.NewWithOptions`, `app.Env.OpenFileManager` and `app.Browser.OpenURL` must be flagged, and a source using `deps.MenuState` must not.
- [ ] **Step 2: Run** `go test ./internal/app/ -run 'Confined'` — expected: PASS on the current tree (the self-checks prove the detector works).
- [ ] **Step 3: Commit** `test(app): keep native UI and Wails imports confined`.

### Task 45: Bindings (committed) and the app's Vite config

**Gate:** none. **Depends on:** Tasks 41, 43. Requires "What this plan assumes from the UI plan" item 1 to be present; if `frontend/package.json` has no `@wailsio/runtime` or `tsconfig.json` has no `@bindings/*` path, stop and report.

**Files:**
- Create: `frontend/vite.config.ts`, `frontend/bindings/**` (generated, committed — **[call] C26**)

**Interfaces:**
- Bindings: `wails3 generate bindings -ts -d frontend/bindings ./cmd/choix`. Output path of a service: `frontend/bindings/github.com/volodymyrsmirnov/choix/internal/app/<package>/service.ts`. The server-only `testsupport` service is not generated; the E2E harness calls it by name (Task 65).
- `vite.config.ts`: `plugins: [react(), wails('./bindings')]`; `server: { host: '127.0.0.1', port: Number(process.env.WAILS_VITE_PORT) || 9245, strictPort: true }`; `build.outDir` `dist`, or `.design/dist-parity` in mode `parity`; aliases — `@bindings` → `./bindings/github.com/volodymyrsmirnov/choix/internal/app`, or `./e2e/parity/mock-bindings` in mode `parity`; `@wailsio/runtime` → `./e2e/parity/mock-bindings/runtime.ts` in mode `parity` only. The file must not contain the string `prototype/` (the design lint's isolation rule). It is exempt from the gate and is not a digest input.

- [ ] **Step 1: Run** `wails3 generate bindings -ts -d frontend/bindings ./cmd/choix` — expected: exit 0.
- [ ] **Step 2: Verify the surface.** `ls frontend/bindings/github.com/volodymyrsmirnov/choix/internal/app` lists `library catalog settings models system` (and the `events`, `apperr` models); `grep -rl "scan:progress" frontend/bindings` finds the typed-events file and it names all seven events; `grep -c "export function" …/catalog/service.ts` is 8 and `…/system/service.ts` is 6. If the events file misses a name, replace that `RegisterEvent` argument in `cmd/choix/events.go` with the string literal and regenerate.
- [ ] **Step 3: Write `vite.config.ts`.**
- [ ] **Step 4: Run** `cd frontend && bun run check && bun run lint:design && bun test`, then regenerate and `git diff --exit-code -- frontend/bindings` — expected: all pass; the second generation changes nothing.
- [ ] **Step 5: Commit** `feat(bindings): generated TypeScript bindings and app Vite config`.

### Task 46: Bundle definition — Info.plist, entitlements, icon

**Gate:** spike S6. **Depends on:** Task 2.

**Files:**
- Create: `build/darwin/{Info.plist.tmpl,entitlements.plist,entitlements.mas.plist.tmpl,bundle.sh}`, `build/config.yml`
- Test: `build/darwin/bundle_test.sh`

**Interfaces:**
- `bundle.sh --binary <path> --out <Choix.app> --version <v> --build <n> --dylib <path>`: removes `<out>`, creates `Contents/MacOS/Choix`, `Contents/Frameworks/libonnxruntime.dylib`, `Contents/Resources/choix.icns` (copied from `assets/icon/choix.icns`, **[call] C27**), `Contents/Info.plist`, `Contents/PkgInfo` (`APPL????`); runs `xattr -cr`. It does not sign.
- `Info.plist.tmpl` keys: `CFBundleIdentifier` `com.github.volodymyrsmirnov.choix`; `CFBundleName` and `CFBundleDisplayName` `Choix`; `CFBundleExecutable` `Choix`; `CFBundleIconFile` `choix`; `CFBundlePackageType` `APPL`; `CFBundleShortVersionString` `{{VERSION}}`; `CFBundleVersion` `{{BUILD}}`; `LSMinimumSystemVersion` `14.0`; `LSApplicationCategoryType` `public.app-category.photography`; `LSArchitecturePriority` `[arm64]`; `NSHighResolutionCapable` true; `ITSAppUsesNonExemptEncryption` false; `NSHumanReadableCopyright`.
- `entitlements.plist`: the four keys in Global Constraints, each `true`. `entitlements.mas.plist.tmpl`: the same plus `com.apple.application-identifier` `{{TEAM_ID}}.com.github.volodymyrsmirnov.choix` and `com.apple.developer.team-identifier` `{{TEAM_ID}}`.
- `build/config.yml`: `wails3 dev` configuration whose `dev_mode.executes` are `task frontend:bootstrap` (blocking), `task dev:frontend` (background), `task dev:run` (primary).

- [ ] **Step 1: Write the failing test** `bundle_test.sh`: with a dummy binary and dylib, the bundle has exactly the five paths above; `plutil -lint` passes on every plist; `plutil -extract CFBundleIdentifier raw` prints the identifier; `LSMinimumSystemVersion` is `14.0`; `plutil -convert json -o - entitlements.plist` has exactly the four keys, all `true`; no key contains `temporary-exception`.
- [ ] **Step 2: Run** `sh build/darwin/bundle_test.sh` — expected: FAIL. **Step 3: Write the files.** **Step 4: Run** it — expected: `ok`.
- [ ] **Step 5: Commit** `feat(build): app bundle definition and sandbox entitlements`.

### Task 47: Taskfile

**Gate:** none. **Depends on:** Tasks 22, 41, 42, 43, 45, 46.

**Files:**
- Modify: `Taskfile.yml` (created by the UI plan's Task 1 — if that has not landed, create it with `version: '3'` only; its `design:*`, `frontend:install` and `test:frontend` tasks are never renamed or changed)
- Create: `docs/release/signing-checklist.md`, `build/taskfile_test.sh`

**Interfaces:**
- Variables: `VERSION` (`git describe --tags --always --dirty`), `BUILD` (`git rev-list --count HEAD`), `WAILS_VERSION: v3.1.0`, `ORT_DYLIB: build/.tools/onnxruntime/lib/libonnxruntime.dylib`. Environment for every Go command: `GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=14.0 CGO_CFLAGS=-mmacosx-version-min=14.0 CGO_LDFLAGS=-mmacosx-version-min=14.0`.
- Tasks:

  | Task | Does |
  |---|---|
  | `tools` | `go install github.com/wailsapp/wails/v3/cmd/wails3@{{.WAILS_VERSION}}`; checks `golangci-lint`, `bun` |
  | `deps:onnxruntime` | `sh build/onnxruntime/fetch.sh` |
  | `frontend:bootstrap` | when `frontend/dist/index.html` is missing: `mkdir -p frontend/dist` and copy `build/frontend-stub/index.html` there; and, **while `internal/ui/` still exists** (until Task 62 deletes it and this line), when `internal/ui/web/dist/index.html` is missing: write `<!doctype html><div id="root"></div>` there (v1's embed and its `#root` test). **A dependency of every task that loads Go packages**: `bindings`, `bindings:check`, `lint`, `test`, `test:smoke`, `build`, `run:server`, `dev:run` (**[call] C23**) |
  | `frontend:dist` | `frontend:bootstrap`; then, if `frontend/index.html` exists, `cd frontend && bun run build` (the bindings are committed, so nothing is generated here) |
  | `bindings` | dep `frontend:bootstrap`; `wails3 generate bindings -ts -d frontend/bindings ./cmd/choix` |
  | `bindings:check` | `bindings`, then `git diff --exit-code -- frontend/bindings` |
  | `dev` | precondition `frontend/index.html` exists (message: "the app UI is not built yet (design gate); use task run:server"); `wails3 dev -config build/config.yml` |
  | `dev:frontend`, `dev:run` | `cd frontend && bun run dev`; `FRONTEND_DEVSERVER_URL=http://127.0.0.1:9245 CHOIX_ONNXRUNTIME_DYLIB={{.ORT_DYLIB}} go run ./cmd/choix` |
  | `run:server` | `go run -tags server ./cmd/choix` with `CHOIX_DATA_DIR=.tmp/data` |
  | `build` | deps `frontend:dist`, `deps:onnxruntime`; `go build -tags production -trimpath -buildvcs=false -ldflags "-s -w -X github.com/volodymyrsmirnov/choix/internal/buildinfo.Version={{.VERSION}} -X …buildinfo.Commit=<sha>" -o bin/Choix ./cmd/choix`; then asserts `lipo -archs bin/Choix` is `arm64` |
  | `package` | `build`, `sh build/darwin/bundle.sh …`, `sign:dev` |
  | `sign:dev` | ad-hoc: `codesign --force --sign - <dylib>`, then `codesign --force --sign - --entitlements build/darwin/entitlements.plist [--options runtime, if Task 2 allowed it] bin/Choix.app`; `codesign --verify --strict` |
  | `sign:developer-id` | needs `CHOIX_SIGN_IDENTITY`; dylib then app with `--options runtime --timestamp --entitlements build/darwin/entitlements.plist` |
  | `notarize` | needs `CHOIX_NOTARY_PROFILE`; `ditto -c -k --keepParent`, `xcrun notarytool submit --wait --keychain-profile`, `xcrun stapler staple` |
  | `dmg` | `hdiutil create -volname Choix -srcfolder bin/Choix.app -ov -format UDZO bin/Choix-{{.VERSION}}-arm64.dmg`; signed when `CHOIX_SIGN_IDENTITY` is set |
  | `package:mas` | needs `CHOIX_MAS_APP_IDENTITY`, `CHOIX_MAS_INSTALLER_IDENTITY`, `CHOIX_MAS_PROFILE`, `CHOIX_TEAM_ID`; copies the profile to `Contents/embedded.provisionprofile`, renders and signs with `entitlements.mas.plist`, `productbuild --component bin/Choix.app /Applications --sign … bin/Choix-{{.VERSION}}.pkg` |
  | `test` | dep `frontend:bootstrap`; `go test -race -count=1 ./...` and `go test -race -count=1 -tags server ./internal/app/testsupport/` (Task 50 adds `./internal/app/menu/` to this list), with `CHOIX_ONNXRUNTIME_DYLIB` set when the library exists |
  | `test:smoke` | `go test -tags smoke ./internal/e2e/` (v1's synthetic pipeline test, kept) |
  | `test:e2e` | Task 65's Playwright run |
  | `lint` | dep `frontend:bootstrap`; `go vet ./... && golangci-lint run` |
  | `fmt` | `gofmt -s -w .` |
  | `icons` | `bun run assets/icon/build.ts` |
  | `clean` | removes `bin/`, `frontend/dist/`, `.tmp/` (never `build/.tools/`, never `frontend/bindings/`) |

  A signing task with a missing variable exits 1 with `<task> needs <VAR> (<what it is>). See docs/release/signing-checklist.md.` None of `build`, `test`, `lint`, `package` reads a signing variable.
- `signing-checklist.md`: what the owner must have (Apple Developer Program team, Developer ID Application certificate, Mac App Distribution and Installer certificates, an App Store Connect app record for the bundle identifier, a Mac App Store provisioning profile, a `notarytool` keychain profile), the variable each maps to, and the order `package` → `sign:developer-id` → `notarize` → `dmg`, or `package:mas`.

- [ ] **Step 1: Write the failing test** `taskfile_test.sh`: `task --list-all` contains `dev build package test test:e2e lint fmt bindings icons deps:onnxruntime sign:dev sign:developer-id notarize dmg package:mas clean`; each of `sign:developer-id`, `notarize`, `package:mas` run with its variables unset exits non-zero and prints `needs CHOIX_`; `grep -c "universal\|amd64\|x86_64" Taskfile.yml` is 0; **in a fresh `git worktree` of HEAD (no `frontend/dist`), `task lint`, `task bindings:check` and `task test -- -run TestNone` each succeed** (the bootstrap dependency works).
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Write the tasks and the checklist.**
- [ ] **Step 4: Run** `sh build/taskfile_test.sh && task test && task test:smoke && task lint && task package && CHOIX_SELFCHECK=1 bin/Choix.app/Contents/MacOS/Choix; codesign -d --entitlements - bin/Choix.app` — expected: test `ok`; tests and lint clean; the self-check prints `"sandboxed":true`, `"onnxRuntime":"1.25.0"`, `"bookmarkRoundTrip":true` and exits 0 (in the shipped build it ignores `CHOIX_DATA_DIR` and `CHOIX_ONNXRUNTIME_DYLIB`); the four entitlements are listed.
- [ ] **Step 5: Commit** `feat(build): Taskfile for build, packaging and signing`.

### Task 48: CI

**Gate:** none. **Depends on:** Task 47.

**Files:**
- Modify: `.github/workflows/ci.yml` (the `ci` job; the UI plan's `design` job is left alone)

**Interfaces:**
- `ci` job on `macos-latest` (arm64). Every step the UI plan put into this job stays — `setup-bun`, `bun install`, `bunx playwright install chromium`, `bun run design:snapshot --check`. Order: checkout; `actions/setup-go` 1.26.x; `oven-sh/setup-bun`; install Task; cache `build/.tools/onnxruntime` keyed on `build/onnxruntime/pin.env`; `task tools`; `task deps:onnxruntime`; `cd frontend && bun install --frozen-lockfile`; `bunx playwright install chromium`; **`task frontend:bootstrap`**; gofmt check; `go mod tidy` diff check; `task bindings:check`; `task lint`; `gotestsum` over `go test -race ./...` (existing report and coverage steps kept); the `-tags server` test list from `task test`; `task test:smoke`; `task package`; `CHOIX_SELFCHECK=1 bin/Choix.app/Contents/MacOS/Choix`; `task test:e2e` (once Task 65 lands); the UI plan's `design:snapshot --check`; upload `bin/Choix.app` as an artifact; `govulncheck`. The `make webui` step is removed in Task 62, not here.

- [ ] **Step 1: Edit the workflow.**
- [ ] **Step 2: Verify** with `actionlint .github/workflows/ci.yml` — expected: no errors; after the push the `ci` job is green from a clean checkout and the artifact exists.
- [ ] **Step 3: Commit** `chore(ci): build, test and package the desktop app`.

---

## Phase 6 — Gated native UI and app wiring

**Preflight, first step of every task in this phase.** Run `task design:status` and `python3 tools/design/gate.py check --mode edit --assume-changed <the task's first gated file>`. Continue only when the second command exits 0. Otherwise stop, change nothing, and report the listed states to the coordinator — do not work around a refused edit. "What this plan assumes from the UI plan" items 2–4 must be in the tree.

Porting rule for the containers: behaviour comes from v1's `internal/ui/web/src/{App.tsx,components/*.tsx}` (fetch coordination, key handling, optimistic pick updates, scroll restoration); markup comes only from `frontend/src/design/` screens, mounted inside the design's `AppRoot`. A container renders exactly one design screen and passes props; it contains no styled elements of its own.

### Task 49: Window

**Gate:** design; spike S1 for file drop. **Depends on:** Tasks 4, 40, 44.

**Files:**
- Create: `internal/app/window/{window.go,state.go}`
- Modify: `cmd/choix/shell_desktop.go`
- Test: `internal/app/window/{window_test.go,state_test.go}`

**Interfaces:**
- Produces:
  ```go
  type State struct{ X, Y, Width, Height int }
  type KV interface { KVGet(k string) (string, bool, error); KVSet(k, v string) error } // library.DB; key "window" ([call] C29)
  func Options(saved *State, fileDrop bool) application.WebviewWindowOptions
  func Open(app *application.App, rt *runtime.Runtime, kv KV, fileDrop bool) *application.WebviewWindow
  func Restore(saved State, screens []Rect) *State // nil when the saved frame intersects no screen by at least 100×100
  ```
  `Options`: `Title "Choix"`, `Width 1280`, `Height 800`, `MinWidth 960`, `MinHeight 600`, `URL "/"`, `Mac: application.MacWindow{TitleBar: application.MacTitleBarHiddenInset}`, `BackgroundColour` = the page-background colour token of `frontend/src/design/tokens.css` converted to sRGB, `EnableFileDrop: fileDrop`, `ZoomControlEnabled: false`, `DefaultContextMenuDisabled: true`; a saved state sets `X`, `Y`, `Width`, `Height`. **`InvisibleTitleBarHeight` is left at 0**: a non-zero value makes Wails start a native window drag on every mouse-down in that band (`webview_window_darwin.m`, `handleLeftMouseDown`), which would swallow clicks on toolbar buttons; the design's `TitleBar` already marks its drag region with `--wails-draggable`. No backdrop or translucency. `Open` wires: `events.Common.WindowFilesDropped` → `rt.AddDropped(directories only)`; `WindowDidMove` / `WindowDidResize` → save, debounced 500 ms; `WindowFocus` → `rt.SetVisible(true)`; `WindowMinimise` → `rt.SetVisible(false)`; `WindowUnMinimise` → `true`.

- [ ] **Step 1: Preflight.**
- [ ] **Step 2: Write the failing tests.** `TestOptions_Defaults` (1280×800, min 960×600, `MacTitleBarHiddenInset`, `InvisibleTitleBarHeight == 0`, `EnableFileDrop` follows the flag). `TestOptions_SavedFrame`. `TestRestore_OffScreenIsNil`. `TestRestore_PartlyVisibleKept`. `TestState_KVRoundTrip`. `TestState_BelowMinimumIsClamped` (saved 400×300 ⇒ 960×600).
- [ ] **Step 3: Run** — expected: FAIL. **Step 4: Implement** and call `window.Open` from `shell_desktop.go`.
- [ ] **Step 5: Run** `task test && task lint && task package && open bin/Choix.app` (the stub page — `task dev` needs the app entry, which arrives in Task 52) — expected: clean; a window opens at 1280×800 with the traffic lights inset; quitting and reopening restores its frame. The toolbar's drag region and button clicks are checked in Task 52, when a toolbar exists.
- [ ] **Step 6: Commit** `feat(window): main window with restored frame and folder drop`.

### Task 50: Native menu from `menus.json`

**Gate:** design; spike S2. **Depends on:** Tasks 5, 24, 39, 44, 49.

**Files:**
- Create: `internal/app/menu/{manifest.go,build.go,wails.go,state.go}`
- Modify: `embed.go` (`//go:embed frontend/src/design/menus.json` → `var MenuManifest []byte`), `cmd/choix/shell_desktop.go`, `Taskfile.yml` (`./internal/app/menu/` in the `-tags server` test list)
- Test: `internal/app/menu/{manifest_test.go,build_test.go,state_test.go}`, `internal/app/menu/wails_server_test.go` (`//go:build server`)

**Interfaces:**
- Consumes: `frontend/src/design/menus.json` (`{ "menus": MenuDef[], "context": { "folder-row": MenuItemDef[] } }`; role items carry `label`), `events.MenuAction`, `system.MenuContext`, `system.MenuState`.
- Produces:
  ```go
  type Item struct { ID, Label, Accelerator, Display string; EnabledIn []string; NeedsSelection bool; HiddenWhen []string; Separator bool; Role string }
  type Menu struct { ID, Title string; Items []Item }
  type Manifest struct { Menus []Menu; Context map[string][]Item }
  func Parse(data []byte) (Manifest, error) // errors: duplicate id or accelerator among "menus" (a context list may reuse a menu's id), role or display outside the vocabulary, a role item without label, enabledIn value that is not a ScreenId, item with none of id/separator/role
  func DisplayAccelerator(display string) (string, bool) // Space→Space, one letter→itself, →→Right, ←→Left, ↩→Enter, ⇧↩→Shift+Enter, ?→Shift+/
  type Factory interface { // the seam the equality test records through
      Submenu(title string) Factory
      Item(label, accelerator string, onClick func()) Handle
      Role(role, label string)
      Separator()
  }
  type Handle interface { SetEnabled(bool); SetHidden(bool) }
  func Build(m Manifest, f Factory, singleKeys bool, onClick func(id string)) map[string]Handle
  func Install(app *application.App, window func() *application.WebviewWindow, m Manifest, emit events.Emitter, singleKeys bool) *State
  func (s *State) SetContext(c system.MenuContext)
  func (s *State) PopupFolderMenu(folderID string, flags []string, x, y int)
  func (s *State) SingleKeysNative() bool
  func Enabled(it Item, c system.MenuContext) bool // screen in EnabledIn (empty = everywhere) and (not NeedsSelection or c.HasSelection)
  func Hidden(it Item, flags []string) bool        // any of it.HiddenWhen is in flags
  ```
- `wails.go` (the real `Factory`): `Role(role, label)` is `application.NewRole(<mapped role>)` followed by `SetLabel(label)` — Wails' own role labels ("Close", "Toggle Full Screen") differ from the approved ones ("Close Window", "Enter Full Screen"). Role vocabulary and mapping, in order: `about`, `hide`, `hideOthers`, `showAll`, `quit`, `cut`, `copy`, `paste`, `selectAll`, `closeWindow`, `minimize`, `zoom`, `toggleFullscreen`, `front` → `application.About`, `Hide`, `HideOthers`, `ShowAll`, `Quit`, `Cut`, `Copy`, `Paste`, `SelectAll`, `CloseWindow`, `Minimise`, `Zoom`, `ToggleFullscreen`, `Front`. `Install` sets the menu with `app.Menu.Set` and registers three context menus built from `context["folder-row"]`: `folder-row` (flags none), `folder-row-offline`, `folder-row-scanning` (items whose `hiddenWhen` contains the flag are left out).
- A click emits `menu:action {id}`; a context-menu click emits `{id, data: folderID}`. `PopupFolderMenu` picks the registered menu by flag and calls `window().OpenContextMenu(&application.ContextMenuData{Id, X: x, Y: y, Data: folderID})` — this is how both the ⋯ button and right-click open the native menu.
- **Shortcut ownership (**[call] C31**):** every item with an `accelerator` gets it as a native accelerator. An item with only `display` gets `DisplayAccelerator(display)` **only when `singleKeys` is true** (spike S2 passed: typing is untouched and the page receives no `keydown`); `SingleKeysNative()` reports that, the frontend reads it from `System.Info()` and then leaves those keys to the menu. Otherwise single keys stay with the webview and the item has no accelerator.

- [ ] **Step 1: Preflight.**
- [ ] **Step 2: Write the failing tests.** `TestBuild_EqualsManifest` — read `frontend/src/design/menus.json` from disk, `Build` with a recording factory, and assert the recorded tree equals the manifest element by element: menu titles in order; per menu the sequence of (label, accelerator) for custom items, (role, label) for role items, and separators. `TestParse_Errors` (one fixture per error, including a role item without `label`). `TestParse_RealManifest` (the committed file parses; File has `add-folder` with `CmdOrCtrl+O` and `open-picks` with `CmdOrCtrl+Shift+P`; `closeWindow`'s label is `Close Window` and `toggleFullscreen`'s is `Enter Full Screen`). `TestDisplayAccelerator_Table` (the seven mappings; `"F5"` ⇒ `false`). `TestSingleKeys_OffHasNoAccelerator` / `…_OnUsesDisplay`. `TestEnabled_Table`, `TestHidden_Table`. `TestSetContext_TogglesHandles` (recording handles). `TestClick_EmitsMenuAction`. `TestContextMenus_ThreeVariants` (offline variant has only the items without `offline` in `hiddenWhen`). `wails_server_test.go` (run with `-tags server`, where `application.New` builds no native app; `TestMain` calls `application.New(application.Options{Name: "Choix"})` once, because Wails' role constructors and accelerator error path dereference the global application): `TestInstalledMenu_EqualsManifest` — build with the real factory into `application.NewMenu()` and walk it with `ItemAt`, `Label`, `GetAccelerator`, `IsSeparator`, `GetSubmenu`: every item's label equals the manifest's (role items included, proving `SetLabel`), every custom accelerator equals the manifest's after normalising both through `SetAccelerator(…).GetAccelerator()`; `TestRealManifest_AcceleratorsAreValidForWails` (every `accelerator` and every `DisplayAccelerator(display)` yields a non-empty `GetAccelerator()`).
- [ ] **Step 3: Run** `go test ./internal/app/menu/ && go test -tags server ./internal/app/menu/` — expected: FAIL. **Step 4: Implement** and install from `shell_desktop.go` (the menu state replaces the no-op in `runtime.Deps.MenuState`).
- [ ] **Step 5: Run** `task test && task lint` — expected: clean (Task 44's confinement tests still pass).
- [ ] **Step 6: Commit** `feat(menu): native menu and folder context menu built from the approved manifest`.

### Task 51: Native dialogs from `dialogs.json`

**Gate:** design; spike S1. **Depends on:** Tasks 4, 38, 44, 49.

**Files:**
- Create: `internal/app/dialogs/{dialogs.go,manifest.go}`
- Modify: `embed.go` (`//go:embed frontend/src/design/dialogs.json` → `var DialogManifest []byte`), `cmd/choix/shell_desktop.go`
- Test: `internal/app/dialogs/{dialogs_test.go,manifest_test.go}`

**Interfaces:**
- Consumes: `frontend/src/design/dialogs.json`: `{ "remove-folder": { "title": "… {name} …", "message": "…", "buttons": [{ "label": "…", "role": "default" | "cancel" | "destructive" }] } }`.
- Produces:
  ```go
  type Button struct{ Label, Role string }
  type Alert struct{ Title, Message string; Buttons []Button }
  func ParseAlerts(data []byte) (map[string]Alert, error)  // errors: unknown role, no button with role "cancel", more than one "default"
  func (a Alert) For(name string) Alert                    // replaces {name} in the title and the message
  func New(app *application.App, window func() application.Window, alerts map[string]Alert) *Dialogs // implements library.FolderPicker and library.Confirmer
  ```
  `PickFolders`: `app.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(false).AttachToWindow(w).PromptForMultipleSelection()`; a cancelled panel returns an empty slice and no error. `ConfirmRemove(name)`: `app.Dialog.Warning()` attached to the window, built from `alerts["remove-folder"].For(name)`: title, message, one button per manifest entry in order, `SetDefaultButton` for role `default`, `SetCancelButton` for role `cancel`; returns `true` only for the button that is not the cancel button. A package-level `build func(a Alert) spec` produces the plain description (`title`, `message`, `buttons`, `default`, `cancel`) that the dialog is made from, so the test needs no window.

- [ ] **Step 1: Preflight.**
- [ ] **Step 2: Write the failing tests.** `TestRemoveAlert_EqualsManifest` (read `frontend/src/design/dialogs.json` from disk; `build(alerts["remove-folder"].For("Trip"))` has the manifest's title with `Trip` in place of `{name}`, its message, its button labels in order, and default/cancel taken from the roles). `TestFor_LongName` (an 80-character name appears unshortened; a name containing `{name}` is not expanded twice). `TestParseAlerts_Errors`. `TestConfirm_OnlyNonCancelButtonConfirms` (with a fake prompt function).
- [ ] **Step 3: Run** — expected: FAIL. **Step 4: Implement** and pass `dialogs.New(…)` into `runtime.Deps` from `shell_desktop.go`.
- [ ] **Step 5: Run** `task test && task lint` — expected: clean.
- [ ] **Step 6: Commit** `feat(dialogs): folder picker and remove confirmation from the approved manifest`.

### Task 52: App entry and shell

**Gate:** design. **Depends on:** Tasks 5, 45, 49, 50, 51.

**Files:**
- Create: `frontend/index.html`, `frontend/src/main.tsx`, `frontend/src/app/shell/{App.tsx,api.ts,events.ts,errors.ts,router.ts,folderSession.ts,actions.ts,keys.ts,media.ts,toasts.ts,store.ts,modelsStore.ts,qualityStore.ts,scanStore.ts,settingsStore.ts}`
- Test: `frontend/src/app/shell/{router.test.ts,folderSession.test.ts,actions.test.ts,errors.test.ts,store.test.ts,scanStore.test.ts,modelsStore.test.ts,toasts.test.ts}` (run with `bun test`, **[call] C32**)

**Interfaces:**
- `api.ts` is the only file that imports `@bindings/*`; it re-exports `Library`, `Catalog`, `Settings`, `Models`, `System`. `events.ts` is the only file that imports `Events` from `@wailsio/runtime`: `on(name, handler: (data) => void): () => void` with the generated event types.
- `errors.ts`: `appError(e: unknown): { code: string; field?: string; reason?: string; message: string }` from the rejected call's `cause`; `fieldCopy(field, reason)` returns the design's `validateSettings` message for a reason code (`picks_dir`: `empty`, `absolute`, `traversal`, `reserved`, `has_originals`, `symlink`; `bucket_size`: `min`; `similarity`: `range`) — the Go message is never shown for these.
- `store.ts`: `createStore<T>(load: () => Promise<T>, subscribe: (set, reload) => () => void)` — `start()` subscribes first, then loads; every event either patches the value or calls `reload()`, so a missed event is repaired by the next reload (this replaces SSE replay).
- `router.ts` (**[call] C30**): routes `#/`, `#/folder/<id>`, `#/folder/<id>/focus/<clusterId>`, `#/settings`; `back()` goes Focus → Folder → Library, and Settings → where it was opened from; scroll offsets are kept per route in memory.
- `folderSession.ts`: the single owner of `Catalog.Open` / `Catalog.Close`. It opens when the route's folder id appears or changes, stays open across Folder, Focus, Compare **and a visit to Settings**, and closes only on entering the Library or opening another folder. Folder and Focus containers never call `Open` or `Close`. On `library:changed` for the open folder: `moved` → open again; `offline` → mark it (the screens show the notice); `gone` → navigate to the Library.
- `actions.ts`: `runAction(id: string, data?: string): void` — one function for menu ids and keys. `menu:action` calls it. `undo` calls `document.execCommand('undo')` when a text field has focus, else `Catalog.Undo`; `github` calls `System.OpenHelp()`; `settings`, `back`, `shortcuts`, `find` are navigation or overlay actions; the rest map one-to-one to v1's key actions. After every route or selection change: `System.SetMenuContext({ screen, hasSelection, flags })`.
- `keys.ts`: v1's `internal/ui/web/src/keys.ts` with one added rule (**[call] C31**): a `keydown` whose combination equals an `accelerator` in `menus.json` is ignored (the native menu owns it), and when `System.Info().singleKeysNative` is true, so is a key listed as `display`. Every action therefore has exactly one dispatcher.
- `media.ts`: `mediaFor(folderId: string): MediaURLs` producing `/media/thumb/<id>/<encoded path>?tier=…&v=…` and `/media/full/…`. `toasts.ts`: the toast list with its timers (v1's durations).
- `App.tsx`: renders the design `AppRoot` (with `fileDropTarget`) containing the routed container, the `Toaster` and the shortcut sheet; on `common:WindowFocus` calls `Library.CheckNow()`. `index.html` has no favicon link (a desktop window shows none); the title-bar mark is the design's.

- [ ] **Step 1: Preflight.**
- [ ] **Step 2: Write the failing tests.** `store.test.ts`: `subscribes before loading`, `reload after a missed event yields the same value as applying it`. `router.test.ts`: `parses the four routes`, `back from focus goes to the folder, then the library`, `back from settings returns to the folder it was opened from`, `restores scroll per route`. `folderSession.test.ts`: `opens once for folder → focus → folder` (`Catalog.Open` called once, `Close` never), `stays open across settings`, `closes on the library route`, `switching folders closes the first and opens the second`, `moved reopens`, `gone navigates to the library`. `actions.test.ts`: `menu action and key run the same handler` (`open-picks` ⇒ `System.OpenPicksFolder(currentFolder)` once from each), `a keydown matching a manifest accelerator is ignored` (⌘O keydown ⇒ no call; `menu:action add-folder` ⇒ one call), `single keys are ignored when singleKeysNative`, `undo in a text field does not call Catalog.Undo`, `unknown id is ignored`. `errors.test.ts`: `maps picks_dir/absolute to the design copy`, `unknown reason falls back to the message`. `scanStore.test.ts`: `scan:progress updates only its folder`, `scan:state ready clears progress`. `modelsStore.test.ts`: `models:progress replaces the entry of that kind`. `toasts.test.ts`: `dismisses after its duration` (fake timers), `undo keeps the toast until resolved`.
- [ ] **Step 3: Run** `cd frontend && bun test src/app` — expected: FAIL.
- [ ] **Step 4: Implement.** The routed containers are placeholders that render nothing until Tasks 54–60.
- [ ] **Step 5: Run** `bun test && bun run check && bun run lint:design && task build && task design:status`, then `task dev` — expected: pass; the app builds; status unchanged; in the running app the window drags from the empty part of the toolbar and nowhere else, and a toolbar button responds to a single click (this is the check Task 49 could not make without a toolbar).
- [ ] **Step 6: Commit** `feat(app): app entry, router, folder session and shared stores`.

### Task 53: Mock bindings and parity plumbing

**Gate:** design. **Depends on:** Task 52.

**Files:**
- Create: `frontend/e2e/parity/mock-bindings/{runtime.ts,library/service.ts,catalog/service.ts,settings/service.ts,models/service.ts,system/service.ts,control.ts}`, `frontend/e2e/parity/{fixtures.ts,fixtures/overlays.ts,serve-app.mjs}`
- Modify: `frontend/e2e/parity/{cases.ts,enabled.json}` (add `overlays`), `Taskfile.yml` (`parity:app`, `parity`), `.github/workflows/ci.yml` (job `parity`)
- Test: `frontend/e2e/parity/mock-bindings/control.test.ts`

**Interfaces:**
- Mock bindings export the same function names as the generated services (a type test imports both and assigns one to the other's type). `control.ts` installs `window.__choixParity = { load(f: ParityFixture): Promise<void>; emit(name: string, data: unknown): void }`.
- `ParityFixture = { route: string; folders?: FolderSummary[] | 'pending'; models?: ModelStatus[]; quality?: QualityStatus; settings?: SettingsBody | 'error'; view?: FolderView | 'pending' | 'error'; focus?: FocusResponse | 'pending' | 'error'; ratings?: Record<string, number>; toasts?: ToastItem[]; ui?: Record<string, unknown> }` — `ui` carries what is a prop in the prototype and local state in the app (open menu, zoom, active section, selection). `fixtures.ts` exports `fixtureFor(state: number): ParityFixture`, assembling per-group files (`fixtures/<group>.ts`) built from the prototype's `frontend/prototype/mock/*` data. A parity case is `{ state: n, drive: page => page.evaluate(f => window.__choixParity.load(f), fixtureFor(n)) }` plus `interact` where the prototype state uses a forced look.
- `task parity:app`: `cd frontend && bun run build:parity`, then `node e2e/parity/serve-app.mjs --port 8801` (serves `.design/dist-parity/` on `127.0.0.1`). `task parity [-- --grep <group>]`: starts `parity:app` and the prototype server (`task design:serve`), waits for both, runs `PARITY_APP_URL=http://127.0.0.1:8801 bunx playwright test -c playwright.design.config.ts --project parity`, stops both.
- CI job `parity` (ubuntu): `bun install --frozen-lockfile`, `bunx playwright install --with-deps chromium`, `task design:build`, then `task parity`. It replaces the bare parity step the UI plan's Task 9 added to the `design` job (which would run without the app being served).

- [ ] **Step 1: Preflight.**
- [ ] **Step 2: Write the failing test** `control.test.ts`: `load resolves bindings from the fixture` (`Library.List()` returns `fixture.folders`), `'pending' never resolves`, `'error' rejects with an app error`, `emit reaches Events.On subscribers`, `every generated binding name has a mock` (compares exported names of `@bindings/*/service` with the mock modules).
- [ ] **Step 3: Run** `bun test e2e/parity` — expected: FAIL.
- [ ] **Step 4: Implement** the mocks, `fixtures.ts`, `fixtures/overlays.ts` (states 600–605), the cases for 600–605, the tasks and the CI job; add `overlays` to `enabled.json`.
- [ ] **Step 5: Run** `bun test && bun run check && task parity -- --grep overlays && task design:status` — expected: parity reports no difference for 600–605; status unchanged.
- [ ] **Step 6: Commit** `test(parity): mock bindings, fixtures and the app parity surface`.

### Tasks 54–61: Screen containers and their parity cases

**Gate:** design. One task per row. **Container tasks** (54, 56, 58, 60) follow Task 52's steps: preflight → failing store tests → run → implement → `bun test && bun run check && bun run lint:design && task build` → commit `feat(app): <screen> container`. **Parity tasks** (55, 57, 59, 61) follow Task 53's: preflight → write `fixtures/<group>.ts` and the cases for every listed state (one `ParityCase` each; a state that cannot be driven gets `skip` with the reason and is reported to the coordinator) → add the groups to `enabled.json` → `task parity -- --grep <groups> && task design:status` (no difference; status unchanged) → commit `test(parity): <groups> cases`.

| Task | Depends on | Files | What it does |
|---|---|---|---|
| 54 Library container | 52 | `frontend/src/app/library/{LibraryContainer.tsx,libraryStore.ts,libraryStore.test.ts}` | Renders `WelcomePanel` when there are no folders and an enabled model is not `ready`; else `LibraryScreen` (compact banner while a model downloads). Bindings: `Library.List/AddFolders/Remove/Rescan`, `System.OpenPicksFolder/RevealFolder/ShowFolderMenu`, `Models.Status/Retry`, `Settings.Save` (model switches). `onMenu(id, at)` (⋯ button and right-click) → `System.ShowFolderMenu(id, at.x, at.y)`; the replica props `menuFor` and `removePrompt` stay `null` (the native menu and alert replace them). Events: `library:changed` → reload, `gone` → toast with the folder's name; `scan:progress` / `scan:state` → patch the row and the title-bar indicator; `models:progress`. `online` from `navigator.onLine`. Store tests: `library:changed reloads the list`; `gone shows the removed toast once`; `AddFolders existing result selects the row and shows the already-in-Library toast`; `welcome vs library decision` (table); `scan row state follows scan:state`; `onMenu passes coordinates to ShowFolderMenu` |
| 55 Welcome and Library parity | 53, 54 | `frontend/e2e/parity/fixtures/{welcome,library}.ts`, `cases.ts`, `enabled.json` | States 10–17, 30–36, 100–122; groups `welcome`, `library` |
| 56 Folder container | 52 | `frontend/src/app/folder/{FolderContainer.tsx,folderStore.ts,folderStore.test.ts}` | Renders `FolderScreen`. Uses the open folder from `folderSession` (never opens or closes it). Bindings: `Catalog.Clusters`, `SetPick`, `Recluster`. Events: `folder:changed {clusters, settings}` → `Clusters()`, `{ratings}` → `Ratings()`; `scan:progress`. Filter and sort are local state passed as props, using the design's `filterClusters`. `picks_blocked` from the view → the pick action shows the design copy for that reason. Store tests: `optimistic pick is rolled back on error`; `folder:changed clusters refetches`; `ratings patch members without replacing them`; `offline error sets notice 'offline'`; `read_only error sets notice 'read-only'`; `picks_blocked disables picking with the mapped copy` |
| 57 Folder parity | 53, 56 | `fixtures/folder.ts`, `cases.ts`, `enabled.json` | States 200–225; group `folder` |
| 58 Focus and Compare container | 52, 56 | `frontend/src/app/focus/{FocusContainer.tsx,focusStore.ts,focusStore.test.ts}` | Renders `FocusScreen` and its compare mode, inside the same folder session. Bindings: `Catalog.Cluster`, `SetPick`, `Undo`, `Ratings`. Zoom, pan, compare selection, `showInfo` (persisted in `localStorage["choix.focus.showInfo"]`), `mediaState` from image `load`/`error`; advance-on-action from settings; next/previous cluster from `next_cluster_id`/`prev_cluster_id`. Store tests: `photo navigation wraps`; `advance on action moves to the next unreviewed photo`; `busyFileIds covers the in-flight pick`; `cluster load error sets loadError`; `ratings come from the id map`; `undo works after returning from focus to the folder` (the session and its undo stack survived) |
| 59 Focus and Compare parity | 53, 58 | `fixtures/{focus,compare}.ts`, `cases.ts`, `enabled.json` | States 300–317, 400–409; groups `focus`, `compare` |
| 60 Settings container | 52 | `frontend/src/app/settings/{SettingsContainer.tsx,settingsDraft.ts,settingsDraft.test.ts}` | Renders `SettingsScreen`. Bindings: `Settings.Get/Save`, `Models.Status/Retry/Quality`. Validation by the design's `validateSettings` before the call; an `invalid` answer from Go is mapped by `fieldCopy(field, reason)`. Events: `quality:status`, `models:progress`. The leave-without-saving prompt is driven by the router's guard. Store tests: `draft keeps raw text for numeric fields`; `save sends only changed fields`; `invalid from Go maps to the field's design copy`; `leaving with changes opens the prompt` |
| 61 Settings parity | 53, 60 | `fixtures/settings.ts`, `cases.ts`, `enabled.json` | States 500–520; group `settings` |

After Task 61 `frontend/e2e/parity/enabled.json` lists `welcome`, `library`, `folder`, `focus`, `compare`, `settings`, `overlays`, and `python3 tools/design/gate.py check --mode merge` no longer reports `parity-not-enabled` (`gallery` excluded, `native` satisfied by skip entries — assumption 2).

---

## Phase 7 — Removal and distribution

### Task 62: Delete the v1 shell

**Gate:** none (but it must follow the gated work). **Depends on:** Tasks 54–61 and 66 (every container is ported and the UI flows pass), and the UI plan's Task 5 being merged (nothing ports from `internal/ui/web/` any more).

**Files:**
- Delete: `internal/cli/`, `internal/server/`, `internal/ui/`, `internal/quality/worker.go` (+ test), `Makefile`, `install.sh`, `testdata/http/`
- Modify: `internal/picks/picks.go` (remove the `Pick`/`Unpick`/`Reject`/`Unreject` wrappers), `go.mod` / `go.sum` (`go mod tidy`: `chi`, `r3labs/sse`, `cobra` and their indirect modules go), `internal/notools_test.go` (empty the `allow` slice), `.gitignore` (drop `/choix`, `internal/ui/web/dist/`), `.github/workflows/ci.yml` (drop `make webui`), `Taskfile.yml` (`frontend:bootstrap` no longer writes the v1 placeholder under `internal/ui/web/dist/`)
- Kept: `internal/e2e/smoke_test.go` (`//go:build smoke`) — it uses only `scanner`, `store`, `group`, `pipeline` and keeps running under `task test:smoke`
- Test: `internal/retired_test.go`

- [ ] **Step 1: Write the failing test** `TestV1ShellIsGone`: none of the deleted paths exists; no `.go` file imports `net/http` outside `internal/app/media`, `internal/ai/local` (the downloader) and `cmd/choix`; `go.mod` requires none of `github.com/go-chi/chi/v5`, `github.com/r3labs/sse/v2`, `github.com/spf13/cobra`; no source contains `X-Choix-Background`, `idle-after` or `OpenBrowser`; `internal/quality` still declares `Scorer` (in `pass.go`).
- [ ] **Step 2: Run** `go test ./internal/ -run TestV1ShellIsGone` — expected: FAIL.
- [ ] **Step 3: Delete, tidy.**
- [ ] **Step 4: Run** `task test && task test:smoke && task lint && task package` — expected: clean.
- [ ] **Step 5: Commit** `chore: remove the v1 CLI, HTTP server and embedded SPA`.

### Task 63: Homebrew cask and release workflow

**Gate:** none. **Depends on:** Task 47.

**Files:**
- Create: `build/homebrew/{choix.rb.tmpl,render.sh,render_test.sh}`
- Modify: `.github/workflows/release.yml` (rewritten)

**Interfaces:**
- Cask: `cask "choix"`, `version`, `sha256`, `url "https://github.com/volodymyrsmirnov/choix/releases/download/v#{version}/Choix-#{version}-arm64.dmg"`, `name "Choix"`, `desc "Photo and video culling"`, `homepage`, `depends_on arch: :arm64`, `depends_on macos: ">= :ventura"`, `app "Choix.app"`, `zap trash: ["~/Library/Containers/com.github.volodymyrsmirnov.choix"]`. `render.sh <version> <sha256>` prints it.
- `release.yml`: triggers `workflow_dispatch` and tags `v2.*`. Job `build` (macOS): `task package`, upload `bin/Choix.app` (zipped with `ditto`) as an artifact. Job `distribute`: runs only when the repository variable `CHOIX_SIGNING_READY` is `true`; imports the certificate from secrets, then `task sign:developer-id notarize dmg`, creates the GitHub release with the `.dmg`, renders the cask. The variable is unset, the secrets are unnamed placeholders in comments, and the tap update step is commented out with a note that `volodymyrsmirnov/homebrew-tap`'s `update.yml` is only known to handle `type: formula` — all three are agenda items for Task 70. The v1 universal build, the `formula` call and the path filter on `internal/ui/web/**` are removed.

- [ ] **Step 1: Write the failing test** `render_test.sh`: `render.sh 2.0.0 abc…` output passes `ruby -c`, contains `arch: :arm64` and the versioned URL, and contains no `bin.install`.
- [ ] **Step 2: Run** — expected: FAIL. **Step 3: Write the template, script and workflow.**
- [ ] **Step 4: Run** `sh build/homebrew/render_test.sh && actionlint .github/workflows/release.yml` — expected: `ok`, no errors.
- [ ] **Step 5: Commit** `feat(release): cask template and desktop release workflow`.

### Task 64: README and CLAUDE.md

**Gate:** none. **Depends on:** Task 62.

**Files:**
- Modify: `README.md`, `CLAUDE.md` (the UI plan's "Design gate" section is kept as is), `v2.md`, `docs/release-checklist.md`

**Interfaces:**
- `CLAUDE.md` sections, rewritten for the new shape: what Choix is; hard rules 1–8 — 1 with the `safefs` rule; 2 with the durable commit and per-folder serialisation; 4 with the amended exception list (ONNX awaited, native ImageIO calls detached and abandoned, quit waits 2 s); 5 (`macOS on Apple Silicon only. No universal binary.`); 6 (`Downloads are pinned, visible, opt-out, and only for models.` both models, sequential; the ONNX Runtime library is fetched at build time and shipped in the signed bundle); new 8 (`The app never reaches outside the sandbox.` no private APIs, no temporary-exception entitlements, no helper executables — which is why Finder and URLs go through `platform.Workspace`, not Wails' helpers); architecture diagram from spec §5 with the core/facade rule; repository layout (this plan's File Structure); `task` commands; conventions (unchanged ones kept, plus: every exported method of a `Service` is a binding and starts at the gate; events are fetch-then-apply; media URLs come from `media.ThumbURL` / the frontend's `mediaFor`; native calls return bytes and go through `platform.Detach`); SQLite specifics for both databases, including `Store.Durable`; native media (embedded JPEG for RAW, DNG metadata from the container, one parser, parity test); models and inference; folder jobs (persisted, preempted, suspended) and presence; picks directory rules (per-folder legacy directory, refused directories); configuration (`visual_grouping` added; machine-wide only); where to stop and ask. Every statement about exiftool, ffmpeg, sips, the HTTP server, SSE, idle shutdown, `--port`, `make` and `internal/ui/web` is removed.
- `README.md`: install (App Store, `brew install --cask choix`), first launch (model downloads, adding folders), where data lives (spec §9 table), building from source (`task tools deps:onnxruntime build package`), testing.

- [ ] **Step 1: Rewrite the four files.**
- [ ] **Step 2: Verify** `grep -n -i "exiftool\|ffmpeg\|sips\|idle-after\|SSE\|make build\|localhost web UI\|universal binary" README.md CLAUDE.md` — expected: only lines that say these were removed or are not used; and `grep -c "never reaches outside the sandbox" CLAUDE.md` is 1.
- [ ] **Step 3: Commit** `docs: README and CLAUDE.md for the desktop app`.

---

## Phase 8 — E2E and UAT

Spec files here are named `*.e2e.ts`, not `*.pw.ts`, so the UI plan's `playwright.design.config.ts` (`testDir: '.'`, `testMatch: '**/*.pw.ts'`) never collects them.

### Task 65: Binding-level E2E against the server build

**Gate:** none (**[call] C25** — the harness is a blank page under the exempt `frontend/e2e/`). **Depends on:** Tasks 43, 45, 47.

**Files:**
- Create: `frontend/e2e/harness/{index.html,main.ts,vite.config.ts}`, `frontend/e2e/playwright.config.ts`, `frontend/e2e/global-setup.ts`, `frontend/e2e/api/{library,catalog,settings,remove,events,gone,media,quit}.e2e.ts`, `internal/e2e/genfolder/main.go` (+ `main_test.go`)
- Modify: `Taskfile.yml` (`test:e2e`), `.gitignore` (`frontend/e2e/harness/dist/`)

**Interfaces:**
- `genfolder -out <dir> -n <count> [-cameras 2] [-bursts 3]`: writes JPEGs (640×480, distinct colours) with EXIF `Make`, `Model`, `BodySerialNumber` and `DateTimeOriginal` spread over the bursts (10 minutes of shots, 2 hours apart), starting 2026-03-04 08:00:00.
- Harness `main.ts`: imports the committed bindings through `@bindings/*` and `Call`, `Events` from `@wailsio/runtime`; sets `window.choix = { Library, Catalog, Settings, Models, System, TestSupport, events: [] }`, where `TestSupport.<Method>(…)` is `Call.ByName('github.com/volodymyrsmirnov/choix/internal/app/testsupport.Service.<Method>', …)`; pushes every received event `{ name, data, at }` into `events`.
- `playwright.config.ts`: `testDir: './api'` (and `./ui` after Task 66), `testMatch: '**/*.e2e.ts'`, `outputDir` under `frontend/.design/e2e/`, `workers: 1`; `globalSetup` creates a temp root, writes `<data>/config.toml` with `visual_grouping = false` and `quality_rating = false` (so no download is attempted), and generates folders with `genfolder`; `webServer`: `go run -tags server,production ./cmd/choix` from the repository root with `CHOIX_DATA_DIR`, `CHOIX_E2E_ROOT`, `CHOIX_ASSETS_DIR=frontend/e2e/harness/dist`, `WAILS_SERVER_PORT=8090`; `url: http://127.0.0.1:8090/health`. (A `server` build honours `CHOIX_DATA_DIR`; the ONNX library is not needed with both model settings off.)
- `task test:e2e`: `frontend:bootstrap`, `cd frontend && bun run harness:build && bun run test:e2e`.

- [ ] **Step 1: Write the specs (they fail until the harness builds).**
  - `library.e2e.ts`: `AddFolderPath` → `WaitIdle(60000)` → `List()` has one folder with `state: 'ready'`, `photos: 24`, `month: '2026-03'`, six `previews`, each fetching 200 `image/jpeg`. Adding it again returns `existing: true, relation: 'same'`; adding a subfolder returns `relation: 'inside'`.
  - `catalog.e2e.ts`: `Open` returns ≥ 3 clusters, two `devices`, `picks_dir: 'picks'`; a member's thumb URL is 200; `SetPick(pick)` → `state: 'picked'` and the file exists under `<folder>/picks/` (Node `fs`), original byte-identical; `SetPick(reject)` → copy gone; `Undo` twice → `picked`, then `unmarked`; ten parallel `SetPick` calls on different files all succeed and leave ten copies; `Settings.Save({ bucket_size: 60 })` → `reclustered: true` and more clusters than before.
  - `settings.e2e.ts`: `Save({ picks_dir: '/tmp/x' })` rejects with `cause.code === 'invalid'`, `cause.field === 'picks_dir'`, `cause.reason === 'absolute'`; `'.'` → `reserved`; `Get()` unchanged.
  - `remove.e2e.ts`: `SetConfirmAnswer(false)`, `Remove` → `false`, still listed; `SetConfirmAnswer(true)`, `Remove` → `true`, list empty, the folder and its `.choix` still on disk.
  - `events.e2e.ts`: after an add, `events` holds `library:changed added`, `scan:state queued`, `scanning` before `ready`, and `scan:progress` for stages `metadata`, `thumb`, `analyze`, `cluster`; no second of the run has more than 12 `scan:progress` events.
  - `gone.e2e.ts`: delete a listed folder's directory with Node `fs`, `CheckNow()` → list no longer has it; `events` has `library:changed gone` with its name.
  - `media.e2e.ts`: `/media/thumb/<id>/../../etc/passwd` → 404; an unknown folder id → 404; `Range: bytes=0-9` on an original `.jpg` through `/media/full/…` → 206.
  - `quit.e2e.ts`: this spec does not use Playwright's `webServer` (which cannot be restarted): it spawns the server binary built by `globalSetup` (`go build -tags server,production -o <tmp>/choix-e2e ./cmd/choix`) itself, on port 8091 with its own data directory, adds a 300-file folder, sends SIGTERM mid-scan, asserts the process exits within 8 s, starts the binary again with the same data directory → the folder is listed and its job runs to `ready`; a pick made just before the signal is present after the restart.
  - `genfolder/main_test.go`: `TestGenFolder_WritesDatedJPEGs` (24 files; `meta.Parse` of the native record gives two device keys and capture times inside the three bursts).
- [ ] **Step 2: Run** `task test:e2e` — expected: FAIL (harness missing).
- [ ] **Step 3: Write `genfolder`, the harness, the config and global setup.**
- [ ] **Step 4: Run** `task test:e2e`, then `cd frontend && bunx playwright test -c playwright.design.config.ts --list | grep -c e2e/api` — expected: all specs pass; no request left `127.0.0.1`; the design config lists none of these specs (0).
- [ ] **Step 5: Commit** `test(e2e): binding-level end-to-end tests on the server build`.

### Task 66: UI E2E

**Gate:** design. **Depends on:** Tasks 54, 56, 58, 60, 65.

**Files:**
- Create: `frontend/e2e/ui/{cull.e2e.ts,library.e2e.ts,settings.e2e.ts}`
- Modify: `frontend/e2e/playwright.config.ts` (second project `ui`: same server without `CHOIX_ASSETS_DIR`, so the embedded app is served; `task frontend:dist` first)

- [ ] **Step 1: Write the specs** (selectors are v1's `data-testid` values, which the design port keeps). `cull.e2e.ts` — spec §11's flow: `TestSupport.SetPickerPaths([folder])`, click Add Folder, wait for the row to be `ready`, open it, open the first cluster, press Space (picked badge; file in `picks/`), press Space again (gone), pick, go back to the folder with Escape, then `EmitMenuAction('undo', '')` (unmarked — the folder session and its undo stack survived the navigation), Escape back to the Library. `library.e2e.ts`: `EmitMenuAction('remove-folder', <id>)` with `SetConfirmAnswer(true)` (row gone, folder on disk); deleting the folder on disk then focusing the window shows the "removed" toast. `settings.e2e.ts`: change the bucket to 1 minute, Save, back in the folder the cluster count grew; an absolute picks folder shows the design's field error and Save stays disabled.
- [ ] **Step 2: Run** `task test:e2e` — expected: the `ui` project fails first, then passes once selectors are confirmed against the built app; the `api` project still passes.
- [ ] **Step 3: Commit** `test(e2e): UI flows on the server build`.

### Task 67: Sandboxed `.app` smoke and checklist

**Gate:** design for the UI steps; the scripted head (build, sign, scans, self-check) has none. **Depends on:** Task 47 for the head; Tasks 49–61 for the UI steps.

**Files:**
- Create: `build/smoke/sandbox-smoke.sh`, `docs/release/sandbox-smoke.md`
- Modify: `Taskfile.yml` (`smoke:sandbox`)

**Interfaces:**
- `sandbox-smoke.sh [--head-only]` — the source folder is chosen by rule (the smallest folder under the owner's photo folder (`$CHOIX_UAT_ROOT`) with at least 50 media files; the script exits with a message when `CHOIX_UAT_ROOT` is unset) and is only ever called `folder-1` in output:
  1. `task package`.
  2. Static checks on `bin/Choix.app`: the four entitlements (`codesign -d --entitlements -`); `otool -L` on the executable names no path outside `/usr/lib`, `/System`, `@rpath`, `@executable_path`; `go version -m` shows `-tags=production` and neither `private_mac_apis` nor `server`; **no private-API selector from Wails' `mac_private_api_darwin.go` is in the binary** (`strings` finds none of `setGroupIdentifier:`, `setGroupSpacing:`, `developerExtrasEnabled`). (Wails' own `open` helper is linked in but never called — Task 44's test bans `app.Env` and `app.Browser`; whether a child process is ever started is checked at run time in step 6.)
  3. `CHOIX_SELFCHECK=1` run prints `"sandboxed":true`, `"onnxRuntime":"1.25.0"`, `"bookmarkRoundTrip":true`, `"trashCheck":true`, and `providers` without `error`.
  4. (`--head-only` stops here.) `cp -cR "$SRC" "$TMPDIR/choix-smoke/folder-1"`; a manifest (path, size, mtime) of the original is taken before and compared after (kept in the temp directory, never printed).
  5. Launches the app and drives the open panel with `osascript` (System Events: ⌘O, ⇧⌘G, type the path, Return, Return) — when Accessibility permission is missing it prints the manual step and waits for Enter.
  6. Polls the container's `library.db` (`sqlite3 -readonly`) until the folder's `job_pending` is 0; asserts counts > 0 and that `<copy>/.choix/state.db` exists. While it polls, and through the manual checks, it samples `pgrep -P <Choix pid>` once a second: **Choix must never have a child process** (this is what catches a stray `open`).
  7. Prints the remaining manual checks.
- `sandbox-smoke.md`, the checklist from spec §11, each line with what to look at (the record names no folder or file): launch; add a clone of a corpus folder; scan completes with thumbnails; pick a photo and find the copy in `picks/`; Open Picks opens Finder there; Reveal selects the folder in Finder; the ⋯ button and right-click both open the native row menu; ⌘O opens the picker exactly once; delete the test folder in Finder and watch the row leave with a toast; eject a test volume mid-scan and see the row go offline, then resume when it is back; relaunch and see the Library and folder access restored (no "lost access" row); quit during a scan returns within a few seconds; drop a folder on the window (if spike S1 passed); play and seek a video (if spike S3 passed); no `deny` lines from `sandboxd` for Choix in `log show --last 10m --predicate 'process == "sandboxd"'`; `~/.choix` was not created.

- [ ] **Step 1: Write the script and the checklist.**
- [ ] **Step 2: Run** `task smoke:sandbox -- --head-only` (available from Task 47 on) — expected: steps 1–3 print `ok`.
- [ ] **Step 3: After Phase 6: run** `task smoke:sandbox` — expected: steps 1–6 `ok`; the original folder's manifest is unchanged. Walk the manual checks; record the result (date, build, macOS version, each line pass/fail) at the bottom of `sandbox-smoke.md`.
- [ ] **Step 4: Commit** `test(smoke): sandboxed app smoke script and checklist`.

### Task 68: UAT on a real corpus

**Gate:** none. **Depends on:** Tasks 40, 47 (headless, no window; the `uat` task lives in the Taskfile).

**Files:**
- Create: `internal/e2e/uat_test.go` (`//go:build uat && darwin`), `docs/release/uat.md`
- Modify: `Taskfile.yml` (`uat`: `go test -tags uat -timeout 4h -run TestUAT -v ./internal/e2e/`)

**Interfaces:**
- **Privacy (R22).** The corpus root is the owner's photo folder (`$CHOIX_UAT_ROOT`). The test takes **every immediate sub-folder that contains media**, sorted by file count, and calls them `folder-1` (largest) … `folder-N` in all output. Folder names, file names, serials, dates and locations are never printed, never asserted against literals, and never written to `docs/release/uat.md`; a failing file is reported as `folder-K, <extension>, #<index>` plus the error text with paths stripped. Expected counts are computed by the test from the tree itself.
- `TestUAT` (skips unless `CHOIX_UAT_ROOT` is set and the folder exists): per folder — record a manifest (relative path, size, mtime) of the original tree; clone it with `cp -cR` into `t.TempDir()`; build a `runtime.Runtime` with the real platform packages, the real inference engine (models taken from `CHOIX_MODELS_DIR`, default the app's models directory; never downloaded — the fake downloader reports `Has` from disk) and `CHOIX_DATA_DIR` in the temp dir; add the clone; wait for `Idle()`; then assert and log. `-limit N` clones only the first N files per folder (`-short` ⇒ 200).
- Assertions per folder: every file is `analyzed` or `failed`; `failed` ≤ 0.5 % and each failure is logged with its error; photos + videos equals the number of media files the test itself counts by extension in the clone; every non-failed photo has `thumb` and `preview` rows whose files exist and obey the sizing rule of its source kind — RAF/DNG: width 256 and 1600; other stills: long side 256 and 1600 (or the source's size if smaller); every video has five keyframes; clusters > 0; with CLIP present, ≥ 99 % of non-failed files have an embedding; device keys: no camera model appears under two different keys; no DNG has device key `Unknown` or a capture time equal to its file time; three picks (first, middle, last file) are copied to `picks/` byte-identical and removed again by unpick; a preemption test — while the largest folder scans, a second folder is added and opened: its job starts within 2 s; `Library.List()` shows six previews; `Shutdown` during a third scan returns within `QuitDeadline` + 1 s; no more than `platform.NativeSlots` native calls ever run at once (sampled through an exported counter); after `Shutdown`, the original tree's manifest equals the one recorded and contains no `.choix` or `picks`.
- Output: a Markdown table to stdout and `$TMPDIR/choix-uat/report.md` — per `folder-K`: files by format, failed, clusters, wall time per stage, files per second, peak RSS. Nothing else.

- [ ] **Step 1: Write the test.**
- [ ] **Step 2: Run** `go test -tags uat -short -run TestUAT -v ./internal/e2e/` — expected: PASS on 200 files per folder.
- [ ] **Step 3: Run** `task uat` — expected: PASS for every folder. Paste the report table into `docs/release/uat.md` with the date and commit, after checking it contains only `folder-K` labels and numbers. A failure is a bug to fix (or, for a single unreadable source file, an entry in that document with the file's format and error, no name).
- [ ] **Step 4: Commit** `test(uat): headless run over clones of a real corpus`.

---

## Phase 9 — QA rounds and hand-off

### Task 69: Review rounds

**Gate:** none. **Depends on:** Tasks 62–68.

**Files:**
- Create: `docs/superpowers/plans/2026-11-11-choix-2-qa-log.md`

- [ ] **Step 1: Freeze a candidate:** `task test && task test:smoke && task lint && task bindings:check && task test:e2e && task parity && task smoke:sandbox` green; `task design:status` clean; record the commit in the log.
- [ ] **Step 2: Fable rounds (up to 3).** Each round: one whole-branch review against the spec, this plan and `CLAUDE.md`'s hard rules, with the Review Focus list as the prompt's checklist. Findings go into the log as `R<n>-<k>: severity (blocker | major | minor), file:line, finding, decision`. Fixes are written by Opus sub-agents, one finding or one tight group per sub-agent, each with a test that fails before the fix. **A fix is bound by the same rules as the tasks:** one that touches a gated path (`frontend/**` outside the exemptions, `internal/app/{menu,dialogs,window}`) runs Phase 6's preflight first and stops if a state is unapproved or would change; none edits the UI plan's files; the privacy rule (R22) applies to the log itself. A round with no blocker or major ends the Fable rounds early.
- [ ] **Step 3: Codex `gpt-6-astra` rounds (up to 3)**, same log format and fix rule; stop early on a round without blocker or major.
- [ ] **Step 4: Codex `gpt-6.1-sol` rounds (up to 5)**, same; stop early on a round without blocker or major.
- [ ] **Step 5: After the last fix:** rerun Step 1's commands and `task uat`; append the outputs' last lines to the log.
- [ ] **Step 6: Commit** `docs: QA log for Choix 2.0`.

### Task 70: Pull request and the signing conversation

**Gate:** none. **Depends on:** Task 69.

- [ ] **Step 1: Open the PR** from the branch to `main` (never merge it). Body: what changed, by phase; the spike results table; the calls and rulings C1–C38 with their status (the † rows first); the design states approved (`Design approved: [#N Label](http://localhost:8811/#sN)` lines from `task design:status`); the UAT table; the sandbox smoke record; whether the commits are signed.
- [ ] **Step 2: Hand the owner this agenda** (nothing here is done without them):
  1. Apple Developer team and the bundle identifier: confirm or replace `com.github.volodymyrsmirnov.choix` (it is in `Info.plist.tmpl`, both entitlements files, `SingleInstanceOptions.UniqueID` and the cask's `zap`).
  2. Certificates and profiles to create, and the environment variable each fills: `CHOIX_SIGN_IDENTITY`, `CHOIX_NOTARY_PROFILE`, `CHOIX_MAS_APP_IDENTITY`, `CHOIX_MAS_INSTALLER_IDENTITY`, `CHOIX_MAS_PROFILE`, `CHOIX_TEAM_ID` (`docs/release/signing-checklist.md`).
  3. Hardened runtime with the bundled ONNX Runtime library under a real identity (spike S6's open half): run `task sign:developer-id` and the self-check together.
  4. Bookmarks across a signing-identity change (spike S1 step g): folders added under the ad-hoc build show "lost access" once; confirm the re-add path is acceptable for the first signed build.
  5. Deployment target (14.0, set by spike S6) and the App Store category (`public.app-category.photography`).
  6. App Store review notes: the quality model's CC BY-NC-SA licence with a free app, the two model downloads on first launch, `ITSAppUsesNonExemptEncryption = false`.
  7. The Icon Composer `.icon` file for macOS 26 (`Assets.car`), authored by the owner from `assets/icon/layers/`.
  8. CI secrets and the `CHOIX_SIGNING_READY` variable for `release.yml`; whether the Homebrew tap's `update.yml` can publish a cask.
  9. The first release's version number and the retirement note for the v1 formula and `install.sh`.
  10. **Owner-visible consequences of decisions taken in this plan — confirm or veto each:**
      - Open Picks and Reveal in Finder use `NSWorkspace`, not "Wails' file-manager integration" as the original brief said: Wails' helper runs `/usr/bin/open`, which the no-helper-executables rule forbids (C6).
      - If spike S1(e) showed that the sandbox cannot tell "missing" from "not permitted", a folder deleted outright (`rm`, emptied Trash) stays in the Library as a `missing` row ("This folder can no longer be found.") until it is removed by hand; only a folder moved to the Trash vanishes by itself (C7).
      - Quit has a deadline of 5 s (2 s for native image calls already running); whatever is still busy then is left open and the process exits (C3).
      - Welcome state s10 ("both downloading") does not exist: models download one after the other, CLIP first (C16).
      - `picks_dir` must be a relative path inside the folder; v1's "absolute path under your home folder" is gone (C13). A folder whose v1 picks live under another directory keeps using it (C12).
      - Picks that are missing from the picks folder are only counted (`picks_missing`), never copied back; no screen shows the count yet (R24).
      - Tier sizes follow v1 per source kind (width for RAF/DNG previews, long side otherwise), which differs from the spec's "px wide" wording (C8).

---

## Task map

| Phase | Tasks | Gate |
|---|---|---|
| 0 Spikes | 1–8 | none |
| 1 Platform | 9–14 | none (10, 11, 12: S1) |
| 2 Engine | 15–26 | none (16: S1; 18, 20: S7; 19: S4; 22, 26: S5, S6) |
| 3 Library and jobs | 27–34 | none (29: S1) |
| 4 Services | 35–40 | none (37: S3 for video) |
| 5 Shell | 41–48 | none (46: S6) |
| 6 Native UI and app | 49–61 | design (49, 51: S1; 50: S2) |
| 7 Removal, distribution | 62–64 | none; 62 and 64 only after Phase 6 |
| 8 E2E, UAT | 65–68 | 65, 68 none; 66 design; 67 design for its UI half |
| 9 QA, hand-off | 69–70 | none; after everything |

While the owner reviews the design, every task whose gate is not `design` and that does not depend on a gated task proceeds: Tasks 1–48, 63, 65, 68 and the head of 67.
