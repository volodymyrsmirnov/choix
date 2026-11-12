# CLAUDE.md

Guidance for Claude (and future contributors) working in this repo. Read this first; it explains *why* the code is shaped the way it is and how to extend it without breaking the contract with the user.

---

## What Choix is

Choix is a photo-and-video culling app for macOS on Apple Silicon: a sandboxed Wails v3 desktop app. The user adds folders of mixed media to a **Library**; Choix scans each one in the background and groups its files by **device → time bucket → visual cluster**, so the user can quickly pick favourites. Picks are copied (never moved) into the folder's `picks/` subfolder for hand-off to an editor such as Luminar Neo.

**One-sentence north star:** walk in with a 2 000-photo trip dump, walk out an hour later with a `picks/` folder ready to edit, with the visual clusterer doing the heavy lifting on "which of these eight near-identical sunsets do I want to look at."

Choix 1.x was a CLI that served a web UI on localhost. That shell (the CLI, the HTTP server, the embedded SPA, the Makefile) was removed; the engine (scan → metadata → thumbnails → CLIP → clusters, picks copied to `picks/`) was kept and now runs inside the app.

The design is `docs/superpowers/specs/2026-11-11-choix-2-desktop-design.md`; the spike results it relies on are in `docs/superpowers/specs/2026-11-11-choix-2-spike-results.md`. Anything material in this CLAUDE.md that contradicts the spec is a bug; the spec is authoritative, and the code is the final word on what exists.

---

## Hard rules

These are non-negotiable. Breaking any of them is a regression.

1. **Originals are sacred.** Originals are opened `O_RDONLY` only. Inside a folder Choix writes only under `<folder>/.choix/` and the folder's picks directory, and every such write goes through `internal/safefs`: the directory is opened component by component from the folder root (a symbolic link anywhere is refused), a file is created exclusively (`O_CREAT|O_EXCL`) under a random temporary name and renamed within the same directory, and an exported pick is published exclusively (an existing file is never overwritten). Nothing else creates, truncates or renames a file inside a folder. The threat model is stale symlinks and Choix's own bugs, not a concurrent local attacker with write access to the folder.
2. **Never lose a pick.** A pick, reject or undo is committed with `store.Durable` (`PRAGMA synchronous=FULL` and `PRAGMA fullfsync=1`) after the exported copy and its directory have been fsynced, and the binding returns only after that commit. A volume that answers "not supported" to a sync is tolerated and logged once; a real I/O error fails the pick. The mutations of one folder (pick, undo, recluster) are serialised by that folder's lock. `state.db` is the source of truth; everything else is cache.
3. **Per-file failures are isolated.** A corrupt RAF, a `.jpg` that is not a JPEG or a video with no video track marks that one file `scan_status='failed'` with the error text. The job keeps going.
4. **Cancellation is honoured within ~1 s.** Every worker returns within ~1 s of `ctx.Done()`. Exceptions: one ONNX session creation or one inference is awaited. A native ImageIO call cannot be interrupted, so it runs **detached** (`platform.Detach`): the Go caller returns on `ctx.Done()` and the result is discarded; because native calls return bytes and never write files, an abandoned call leaves nothing behind. Detached calls hold one of a fixed number of process-wide slots (`platform.NativeSlots`) until the native call really returns, so abandoned calls cannot pile up, and a folder session's store and bookmark are released only after its detached calls have returned. Video frames use AVFoundation's asynchronous, cancellable generator. Quit has one deadline: 2 s for abandoned native calls, 5 s overall (`runtime.NativeQuitWait`, `runtime.QuitDeadline`); what is still in use then is not closed, and the process exits with it open (SQLite's WAL is crash-safe, originals are read-only).
5. **macOS on Apple Silicon only. No universal binary.** Every build is `GOOS=darwin GOARCH=arm64 CGO_ENABLED=1` with deployment target macOS 14.0 (the bundled ONNX Runtime library's minimum, spike S6); `task build` fails when `bin/Choix` is not arm64 only. Intel Macs, Linux and Windows are non-goals.
6. **Downloads are pinned, visible, opt-out, and only for models.** The two models in `local.KnownModels` (CLIP and the quality model) download automatically on first launch, one after the other, CLIP first. Each download uses its pinned URL, is resumable, and is SHA-256 verified before the file is put in place; it is visible (`models:progress` events, shown in the Library's welcome panel, plus `slog` lines); it is non-fatal (three attempts, then `failed` until the user retries); and it is not attempted while its setting (`visual_grouping` for CLIP, `quality_rating` for the quality model) is off. Switching the setting off also stops a download or rating pass already running. No executable is ever fetched at run time: the ONNX Runtime library is fetched at *build* time (`task deps:onnxruntime`, pinned in `build/onnxruntime/pin.env`) and shipped inside the signed bundle. The quality model is never bundled or redistributed. Don't add any other network access.
7. **No duplicate files at the schema level.** A file is identified by its relative path (case-insensitive). Migration 003 enforces `UNIQUE(LOWER(path))`, and `Files().Insert` is an atomic UPSERT that falls back to the case-insensitive row when only the casing differs, so no duplicate row can appear, even on APFS where casing varies between scans.
8. **The app never reaches outside the sandbox.** The App Sandbox is on in every packaged build. The ad hoc (`sign:dev`) and Developer ID (`sign:developer-id`) builds carry exactly four entitlements (`build/darwin/entitlements.plist`): `com.apple.security.app-sandbox`, `com.apple.security.files.user-selected.read-write`, `com.apple.security.files.bookmarks.app-scope` and `com.apple.security.network.client`. The Mac App Store build (`package:mas`) carries those four plus the two identifiers its provisioning profile requires, `com.apple.application-identifier` and `com.apple.developer.team-identifier` (`build/darwin/entitlements.mas.plist.tmpl`, rendered with `CHOIX_TEAM_ID`); they identify the app and add no access. No private APIs (the Wails `private_mac_apis` build tag is never used), no temporary-exception entitlements, no helper executables, and no `os/exec` in non-test code (`internal/notools_test.go`). That is why Finder and URLs go through `platform.Workspace` (an `NSWorkspace` adapter in `internal/platform/darwin/workspace`), never Wails' `Env.OpenFileManager` or `Browser.OpenURL`, which run `/usr/bin/open` (`internal/app/nativeui_test.go` forbids them). A folder is reachable only through the security-scoped bookmark made when the user picked or dropped it.

---

## Design gate (Choix 2.0 UI)

Choix 2.0's desktop UI is held to prototype states the owner approves. Reference: `docs/design/README.md`. State list: `docs/design/inventory.md`.

- **Check first.** Run `task design:status` before touching `frontend/` or `internal/app/{menu,dialogs,window}/`. It lists every state with its review link and status, and the merge-check findings. It never fails and writes nothing.
- **Gated** (`gated` minus `exempt` in `docs/design/gate.json`): the app under `frontend/` (`index.html`, `src/main.tsx`, `src/app/**`, and any new file not exempted) and `internal/app/menu/**`, `internal/app/dialogs/**`, `internal/app/window/**`. Edit them only while `python3 tools/design/gate.py check --mode edit --assume-changed <the gated path you are about to edit>` passes (the form the hooks run; without `--assume-changed`, a branch with no gated change yet is not checked for `unapproved`): every state approved at its current hash, the change file present and valid, the snapshot current. `task design:status` shows the same for the whole branch: every state's status and the findings. The hooks block the edit otherwise; do not work around a block, stop and tell the owner.
- **Free**: `frontend/src/design/**`, `frontend/prototype/**`, `frontend/tools/**`, `frontend/e2e/**`, the config files `docs/design/gate.json` lists under `exempt`, every test file (`*.test.ts`, `*.test.tsx`, `*.pw.ts`, `*_test.go`), and everything outside `frontend/` and those three packages. The Go engine is not gated.
- **Workflow:**
  1. Change the design (`frontend/src/design/`) or a scenario (`frontend/prototype/scenarios/<group>.tsx`). New numbers go inside the group's range; never renumber or reuse one; a removed number goes into `docs/design/retired.json`. As soon as a digest input changes, and before you end the turn, write or update `docs/design/changes/<branch-slug>.md` (front matter `branch:`, `states:` with every state the branch adds or changes, `retired:`, `ui:`): the Stop hook blocks on `no-change-file` / `bad-change-file`, and the merge check reports `unlisted-state` for a changed state missing from `states:`.
  2. `cd frontend && bun test && bun run check && bun run lint:design`, then `task design:check-states`, `task design:flows`, `task design:inventory` (if states were added, removed or relabelled) and `task design:snapshot`.
  3. `task design:build && task design:serve` in the background (port 8811). Never review on `task design:dev`.
  4. Ask for review with one Markdown link per new or changed state, grouped by screen, as `task design:status` prints them: `[#102 Library · Filled, six folders](http://localhost:8811/#s102)`. Never a bare number. **Then stop.**
  5. Once the owner has approved: implement in the app by mounting the design folder's screens and components, and enable the group's parity cases (`frontend/e2e/parity/`).
  6. A branch that touches gated files without a visible change sets `ui: false`; after you commit, the owner confirms it with `task design:confirm-no-ui`.
- **Gated edits happen on the feature branch**, not in throwaway agent worktrees: the gate finds the change file by branch name, so a `worktree-agent-…` branch fails with `no-change-file` (README §11).
- **Never** run `task design:init`, `task design:approve` or `task design:confirm-no-ui` (nor `gate.py init|approve|confirm-no-ui`, nor anything from `tools/design/gate_owner.py`), never type them into the owner's terminal, and never write `docs/design/approvals.json` or `docs/design/approvers.txt`. Reading them is fine, and so is committing the owner's changes to them as they are, naming the files in `git add`.
- **Keep the snapshot current.** After changing any digest input (`digestInputs` in `gate.json`), run `task design:snapshot` before you end the turn; the Stop hook enforces it.
- **`frontend/tools/extract.ts` re-stales everything.** Any edit to it, even a comment, changes every state's hash, and the owner must re-approve every state. So does a Playwright/Chromium upgrade, and any change to a pseudo-element rule, a transition or animation rule, a `@keyframes` or other at-rule, an `@font-face` or the `<html>` style. Ask before making one.
- **One design build at a time.** `design:build`, `design:check-states`, `design:flows`, `design:selftest` and `design:snapshot` all rewrite `frontend/dist-prototype/`, which the review server serves. Do not run them while the owner is approving.
- **A violation** (gated files changed while the gate fails): run `python3 tools/design/gate.py restore-violations` and do what it prints.
- **Commit the owner's ledger first.** While `approvals.json` or `approvers.txt` has uncommitted changes, `git stash`, `checkout <ref>`, `switch`, `reset --hard` and `restore` of them are blocked.

---

## Architecture in 30 seconds

```
cmd/choix                 main.go: the Wails app, service registration, typed events, the /media route
  │                       shell_desktop.go (window, menu, dialogs) · shell_server.go (server build, tests only)
  │                       selfcheck.go (CHOIX_SELFCHECK=1: headless self-check, then exit)
  ▼
internal/app/runtime      composition root: builds every core, connects them, owns start-up and teardown order
  │
  ├─ internal/app/…       one package per service: a core type + a binding-only Service facade
  │    library            library.db: folders, bookmarks, presence, cached stats, preview strips   [binding]
  │    catalog            cluster grid, Focus payloads, picks, undo, recluster, ratings          [binding]
  │    settings           the machine-wide settings (config.toml)                                [binding]
  │    models             model downloads and status, quality-rating status                       [binding]
  │    system             Finder, help link, app info, menu context, folder context menu         [binding]
  │    media              http.Handler on the asset server at /media (thumb, full, preview); not a binding
  │    session            one folder's store + pipeline; the folder-job queue
  │    inference          app-level ONNX sessions (CLIP, quality), shared and serialised
  │    events · opgate · apperr     event names and payloads · admission gate · binding errors
  │    testsupport        (server build only) path injection and fake dialogs for E2E
  │    menu · dialogs · window      native UI, design-gated: with cmd/choix, the only importers of Wails
  │
  ├─ internal/platform    cgo-free interfaces (Bookmarks, Volumes, Workspace, Stills, Video), platform.Detach,
  │    │                  fakes in platformtest
  │    └─ darwin/{bookmark,volumes,workspace,imageio,avmedia}   cgo + Objective-C, no Wails
  │
  └─ engine               store, scanner, pipeline, group, picks, quality, ai/local, config, thumb, meta,
                          rawpreview, safefs, appdir
```

**Core and facade.** Each `internal/app/*` service package has a **core** type with the Go API other packages use, and a stateless **`Service` facade** whose exported methods are exactly the frontend bindings. Wails binds every exported method of a registered service, so nothing else may be exported on a `Service`. `cmd/choix` registers five: `Library`, `Catalog`, `Settings`, `Models`, `System`; the `media` handler is mounted with route `/media`. Services take their dependencies through constructors (platform interfaces, clock, emitter), so their tests construct them directly with the `platformtest` fakes: no window, no cgo.

**Wails stays at the edge.** Only `cmd/choix` and the three design-gated packages `internal/app/{menu,dialogs,window}` import Wails, and only those three build native UI (`internal/app/nativeui_test.go`). The engine, the platform layer and every other service package do not.

**A folder job is everything one folder needs**, run by `session.Queue` up to two folders at a time (`session.DefaultSlots`), never one folder twice at once: the pipeline pass, then the CLIP backfill for files without an embedding (and a re-cluster if any were added), then the refresh of the folder's cached stats and preview strip in `library.db` and the `folder:changed` "clusters" announcement, so the folder can be reviewed while it is rated, then the quality-rating pass, which changes neither and is announced by `folder:changed` "ratings". The pipeline stages run in order, advancing each file's `scan_status`:

```
discover → metadata → thumb → analyze → cluster
```

- `discover` walks the folder (skipping `.choix/` and the picks directories), content-hashes new and changed files, and marks deleted ones missing.
- `metadata`, `thumb` and `analyze` are per-file (worker pool: NumCPU for metadata and thumbnails, 1 for inference).
- `cluster` is global: `group.RebuildAll` sorts every analyzed file by (device, captured_at, id), cuts gap-based time buckets, then sub-clusters each bucket by CLIP similarity. It wipes and rebuilds `clusters` and `cluster_members`; picks live in their own table and survive.
- A job resumes from each file's status, so running it twice is idempotent. There is no rerank stage; CLIP embeddings are the only grouping signal. The quality rating is display-only and ranks nothing.

**Sessions and teardown.** A folder session (`session.Manager`) is reference-counted: the open folder holds one reference, the job queue another while it works on that folder. After the last release it waits for the folder's detached native calls, then closes the store and releases the folder's access; a store is never closed while a reference or a detached call exists. At quit `runtime.Shutdown` closes the admission gate (new binding calls answer "Choix is quitting."), cancels the root context, silences the emitter, stops the queue, closes the catalog, stops the model downloads, tears down the sessions, then closes the ONNX sessions and `library.db`, all within the quit deadline of hard rule 4. Closing the window quits the app.

---

## Repository layout

```
choix/
├── embed.go                  package choix: embeds frontend/dist, frontend/src/design/menus.json and dialogs.json
├── cmd/choix/                the app: main.go, events.go, emitter.go, selfcheck.go, shell_desktop.go, shell_server.go
├── internal/
│   ├── app/
│   │   ├── runtime/          composition root, logging, start and teardown order
│   │   ├── library/          library.db (own migrations/), folders, presence, stats, previews
│   │   ├── session/          per-folder session, job queue, folder job, quality status
│   │   ├── catalog/  media/  system/  settings/  models/  inference/
│   │   ├── events/  opgate/  apperr/
│   │   ├── testsupport/      server build only: E2E remote control
│   │   ├── menu/  dialogs/  window/      GATED (design gate)
│   │   └── nativeui_test.go  native UI only in the gated packages; Wails only in cmd/choix and them
│   ├── platform/             interfaces, value types, detached calls (no cgo); platformtest/ fakes and fixture builders
│   │   └── darwin/           bookmark/ volumes/ workspace/ imageio/ avmedia/ (cgo + Objective-C)
│   ├── appdir/               Application Support, Logs and bundle paths (NSSearchPathForDirectoriesInDomains)
│   ├── safefs/               the only way to write inside a folder: confined, symlink-resistant, exclusive
│   ├── rawpreview/           RAF and DNG embedded-JPEG readers (pure Go, read-only)
│   ├── meta/                 native reader → v1-compatible record → the one parser (parse.go); backfills
│   ├── thumb/                thumbnail tiers and cache on platform.Stills / platform.Video
│   ├── store/                per-folder state.db: migrations/, repos, Durable
│   ├── scanner/  pipeline/  group/       walk, stages and worker pool, device → bucket → cluster
│   ├── picks/                durable pick state machine, export, picks-directory rules, reconcile
│   ├── quality/              one scoring pass (pass.go)
│   ├── ai/local/             ONNX Runtime lookup, shared sessions, CLIP, quality scorer, model registry, installer
│   ├── config/               machine-wide settings: defaults + config.toml
│   ├── buildinfo/            version and commit stamped by task build
│   ├── e2e/                  smoke (synthetic) and UAT (real corpus) tests; genfolder/
│   ├── notools_test.go       no os/exec, no external program names
│   └── retired_test.go       the v1 shell stays deleted
├── frontend/                 Wails frontend (bun, React, TypeScript, Tailwind, Vite)
│   ├── index.html  src/main.tsx  src/app/**      the app's containers and stores: GATED
│   ├── src/design/           design system and presentational screens, shared by app and prototype
│   ├── prototype/            the clickable prototype: one numbered scenario per state
│   ├── tools/                design tooling (lint, state checks, flows, extract, snapshot, review server)
│   ├── bindings/             generated by task bindings, committed
│   └── e2e/                  harness/, api/ and ui/ (*.e2e.ts, playwright.config.ts), parity/
├── build/
│   ├── darwin/               Info.plist.tmpl, entitlements.plist, entitlements.mas.plist.tmpl, bundle.sh
│   ├── onnxruntime/          pin.env, fetch.sh (build-time ONNX Runtime fetch)
│   ├── smoke/                sandbox-smoke.sh
│   ├── quality-model/        scripts that export and verify the quality ONNX model (build-time only)
│   ├── frontend-stub/        placeholder index.html for the Go embed
│   └── config.yml            wails3 dev configuration
├── tools/design/             gate.py and gate_owner.py (design gate)
├── assets/icon/              app icon set (task icons)
├── testdata/                 checked-in fixtures (av/, exif/, fixtures/, imageio/, media/)
├── docs/
│   ├── design/               the design gate: README.md, inventory.md, gate.json, ledger, changes/
│   ├── release/              sandbox-smoke.md, signing-checklist.md, uat.md
│   ├── release-checklist.md  release day
│   ├── plans/                feature plans (quality rating)
│   └── superpowers/          specs/ and plans/
├── .github/workflows/        ci.yml, release.yml
├── .claude/                  settings.json + hooks/design_gate.py
├── Taskfile.yml              every build, test, sign and design task
└── v2.md                     deferred and out of scope
```

---

## Build / test / lint

Everything goes through [Task](https://taskfile.dev) (`task --list`). Every Go command runs with the Taskfile's environment: arm64 only, `MACOSX_DEPLOYMENT_TARGET=14.0`. You need macOS 14 or later on Apple Silicon, the Xcode command-line tools, Go 1.26.2 or later, bun, golangci-lint and Task; `task tools` installs `wails3` at the pinned version and checks the rest.

| Command | What it does |
|---|---|
| `task tools` | `go install` wails3 v3.1.0; checks that golangci-lint and bun are installed. |
| `task deps:onnxruntime` | Fetches the pinned ONNX Runtime library into `build/.tools/onnxruntime` (SHA-256 verified; skipped when up to date). Build time only. |
| `task build` | Builds the frontend and `bin/Choix` (`-tags production`, version-stamped), and fails unless it is arm64 only. |
| `task package` | `build`, then `build/darwin/bundle.sh` assembles `bin/Choix.app` (with `Contents/Frameworks/libonnxruntime.dylib`), then `sign:dev`. |
| `task sign:dev` | Ad hoc signature with the sandbox entitlements and **without** the hardened runtime: signed ad hoc, library validation refuses the bundled library (spike S6). |
| `task sign:developer-id` · `task notarize` · `task dmg` · `task package:mas` | Release signing; read identities from environment variables (`docs/release/signing-checklist.md`) and stop when one is missing. Developer ID signing uses the hardened runtime. |
| `task dev` | `wails3 dev` with hot reload (unsigned, so not sandboxed). `task dev:frontend` + `task dev:run` are its two halves. |
| `task run:server` | The server build (no window, test-support bindings) with its data in `.tmp/data`. |
| `task bindings` · `task bindings:check` | Regenerate the committed TypeScript bindings in `frontend/bindings`; the check fails when they differ (CI runs it). |
| `task fmt` · `task lint` | `gofmt -s -w .` · `go vet` and golangci-lint. **Lint must be 0 issues** before a commit. |
| `task icons` · `task clean` | Regenerate `assets/icon/` · remove `bin/`, `frontend/dist/`, `.tmp/`. |

Tests, and what each proves:

| Command | Proves | Needs |
|---|---|---|
| `task test` | Every Go package, race detector on, plus the server-tagged `testsupport` and `menu` packages. | Nothing extra. Uses the fetched ONNX Runtime when present; ONNX tests skip without the runtime or a model, cgo tests are `//go:build darwin`. |
| `task lint` | `go vet` and golangci-lint, 0 issues. | golangci-lint. |
| `task test:smoke` | The synthetic end-to-end pipeline (`-tags smoke ./internal/e2e/`). | Nothing: no network, no models. |
| `task test:e2e` | Playwright against the server build of the real app on 127.0.0.1:8090/8091: the `api` project drives the bindings, the `ui` project the app's UI. Both model settings are off, so nothing downloads. | Playwright Chromium (`cd frontend && bunx playwright install chromium`). |
| `task parity` | The app renders exactly what the owner approved: the app's parity build (mock bindings) against the prototype, for every group in `frontend/e2e/parity/enabled.json`. | Playwright Chromium; ports 8801 and 8811 free. |
| `task test:frontend` | Frontend unit tests (`bun test`), type check, design lint. | bun. |
| `task design:*` | The design gate's checks, builds and status (see the section below and `docs/design/README.md`). | bun, Playwright Chromium and WebKit. |
| `task smoke:sandbox` | The packaged, sandboxed `bin/Choix.app`: static checks and the sandboxed self-check (`-- --head-only` stops there), then a scan of a clone of a real folder and the manual checks of `docs/release/sandbox-smoke.md`. | A terminal. `CHOIX_UAT_ROOT` (no default) unless `--head-only`. |
| `task uat` | The whole headless runtime with the real platform and inference over clones of a real photo archive (`docs/release/uat.md`). | `CHOIX_UAT_ROOT` (no default; skips without it). Hours on a full archive (`-- -limit N`). |

`task test` and `task lint` must be clean before every commit. When adding a Go dependency, run `go get <pkg>` and commit `go.mod` and `go.sum` together; for a frontend dependency, `bun add` in `frontend/` and commit `package.json` and `bun.lock` (both are design digest inputs: see the gate). Don't bypass signing or hooks: the owner's commits are SSH-signed.

---

## Conventions you must follow

### Errors
- Wrap with `%w` and meaningful context: `fmt.Errorf("decode jpeg: %w", err)`.
- Per-file failures must not panic. They write `scan_status='failed'` with the error text. A failure while the folder is unreachable is not a file failure: nothing is recorded and the job is suspended.
- A binding returns an `*apperr.Error` (code + message); sentinel errors are registered with `apperr.Register` so the frontend receives a stable code.
- Never log secrets (signing identities, keys, certificates).

### Services and bindings
- Every exported method of a `Service` is a binding. It starts at the admission gate (`leave, err := s.gate.Enter()`; `defer leave()`), works through the core, and returns an `*apperr.Error`. Nothing else is exported on a `Service`.
- After changing a binding's signature or payload, run `task bindings` and commit `frontend/bindings`.

### Events
- Event names and payloads live in `internal/app/events` (`library:changed`, `scan:progress`, `scan:state`, `models:progress`, `quality:status`, `folder:changed`, `menu:action`). Services emit through the `events.Emitter` interface; `cmd/choix` adapts it to Wails. Noisy events go through the `Coalescer` (about ten deliveries a second).
- **Fetch, then apply.** The frontend reads current state through a binding when a store starts, then applies events: an event that changes a list or a state makes the store reload or reconcile from a binding; `scan:progress` is transient and only patches the row. A missed event therefore cannot leave the UI stale. Keep JSON tags in sync with `frontend/src/design/types.ts`.

### Media URLs
- Media is served by `internal/app/media` under `/media` on the Wails asset server: `/media/thumb/<folder-id>/<rel-path>?tier=thumb|preview&v=<content-hash>`, `/media/full/<folder-id>/<rel-path>?v=<content-hash>`, `/media/preview/<folder-id>/<n>?v=<stamp>`. Build them with `media.ThumbURL` / `FullURL` / `PreviewURL` in Go and `mediaFor(folderId)` (`frontend/src/app/shell/media.ts`) in the frontend, which produce identical strings. Never by hand.
- The path is looked up through the folder's store (`Files().GetByPath`) and joined under the root with `safeJoinUnderRoot`, so there is no traversal surface. `v` busts the webview's cache when the bytes change. Video is served with HTTP Range (`http.ServeContent`).

### Native calls
- Platform interfaces return bytes (an encoded JPEG, a property dictionary) and never write files; cache files are written by `internal/thumb` through `safefs`.
- Every ImageIO call goes through `platform.Detach`; AVFoundation frame generation holds a slot through `platform.Acquire` until its last callback has run. See hard rule 4.

### Shortcuts and menus
- The native menu is built from `frontend/src/design/menus.json` (embedded by `embed.go`); a Go test asserts the installed menu equals the manifest. Menu clicks reach the frontend as `menu:action` and run the same action the key runs.
- `menu.NativeAccelerator` is the one place that decides who owns a shortcut: an accelerator with ⌘, ⌃ or ⌥ belongs to the native menu (the webview ignores that keydown); single keys (Space, X, arrows, C, Z, I, ?) belong to the webview (`frontend/src/app/shell/keys.ts`) and never fire while a text field has focus. They move to the menu only once the owner has verified spike S2's key presses.

### File paths
- Inside a database, file paths are **relative to the folder root** with forward slashes (`filepath.ToSlash`), except `library.db`'s `folders.path`, which is absolute. Reconstruct absolute paths only at I/O boundaries.
- Lookups by path are **case-insensitive** (`LOWER(path) = LOWER(?)`).
- Inside a folder, write only through `safefs`. In the app's own data directory, write to a temporary file and rename it into place.
- App-level paths come from `internal/appdir` (`NSSearchPathForDirectoriesInDomains`), right inside and outside the sandbox. Never use `$HOME`: inside the sandbox it is the container. Builds without the `production` tag (and the `server` build) honour `CHOIX_DATA_DIR` as the data root; the shipped build ignores it.

### Concurrency
- Workers in `internal/pipeline/pool.go` use `errgroup` with `SetLimit(workers + 1)` (the `+1` is the producer goroutine; without it single-worker pools deadlock).
- Both databases use `MaxOpenConns = 1` and `busy_timeout = 10000`. Don't change either casually.
- Don't share `*sql.Tx` across goroutines. Inside `store.Durable`, use only the `tx` it hands you: it holds the store's only connection.
- Up to `session.DefaultSlots` (2) folder jobs at once, never two on the same folder (one writer per `state.db`); one inference at a time (`internal/app/inference`). Jobs take the cancellable, process-wide inference turn (`session.acquireInference`) before they call the engine, the analyze stage for each file it embeds and the rating pass for each photo, so a cancelled job never waits in the engine behind another job's inference.

### Logging
- `log/slog` only. Logs go to `~/Library/Logs/Choix/choix.log` (inside the container when sandboxed) and stderr.
- Log failures, job and stage start and end, model downloads. Don't log per-file success.

### Tests
- TDD: every new Go file ships with a `_test.go` next to it. Race detector on.
- Tests never use the network. cgo tests are `//go:build darwin`. ONNX tests skip when the runtime library or the model is absent. Tests that need a real corpus skip unless `CHOIX_UAT_ROOT` names an existing folder; there is no default.
- **Nothing from a real corpus is public.** The repository is public: no folder or file name, serial number, date or location from a real photo archive goes into a fixture, a test expectation, a log pasted into a document, or a commit message. Tests choose corpus folders by rule and print them as `folder-1`, `folder-2`; they work on `cp -cR` clones, never on the originals. `frontend/prototype/mock/privacy.test.ts` scans the repository for denied terms.
- Service tests use the `internal/platform/platformtest` fakes; platform tests run against `testdata/` fixtures.

---

## SQLite specifics (read this before changing a database)

Driver: `modernc.org/sqlite` (pure Go). Don't switch to `mattn/go-sqlite3` without weighing the cgo cost. There are two databases:

| Database | Where | Holds | Migrations |
|---|---|---|---|
| `state.db` | `<folder>/.choix/state.db` | files, thumbs, clusters, picks, `ai_signals`, `kv` | `internal/store/migrations/` (001–005), schema unchanged from v1 (`user_version` 5) |
| `library.db` | the app's data directory | `folders` (path, bookmark, presence, persisted job, cached stats, preview stamp), `kv` (job sequence, window frame) | `internal/app/library/migrations/001_initial.sql` |

- **Opened by path, never by a hand-built DSN.** `store.Open` and `library.OpenDB` take a filesystem path and build the URI with `store.FileURI`, which percent-encodes `%`, `#` and `?`. A `file:` string concatenated with a folder path is forbidden: a folder named `Day #2` would put the database outside the folder.
- WAL, foreign keys on, `busy_timeout(10000)`, `MaxOpenConns = 1`. `MaxOpenConns = 1` is load-bearing: concurrent writes tripped "database is locked" without it.
- `state.db` opens with `synchronous=NORMAL`; `Store.Durable` switches the connection to `synchronous=FULL` + `fullfsync=1` for one transaction and restores it (a connection it cannot restore is discarded). It is how picks, rejects and undo are recorded.
- A session checks once, when it opens, that `.choix` is a real directory inside the folder and the database files are regular files; then SQLite opens `state.db` by path.
- **A v1 folder opens as it is.** Its clusters and thumbnails show before any job runs; nothing is re-processed. Each `state.db` records the parameters of its last clustering in `kv` (`cluster_params`); a v1 folder has none and keeps its clusters until a job or a recluster rebuilds them.
- `files.path` is unique by `LOWER(path)` (migration 003). `Insert` is an UPSERT; never reach for a GetByPath-then-Insert pattern.
- Adding a `state.db` migration: add `NNN_*.sql`, bump `PRAGMA user_version` in it, and update `wantVersion` in `internal/store/migrate_test.go`. `library.db` has its own migrations in `internal/app/library/migrations/`.
- Unused columns from retired features (`clusters.label`, `clusters.ai_top_file_id`, `clusters.cloud_reasoning`, all of `ai_signals` apart from `clip_embedding`) stay; don't read or write them.
- Thumbnail rows store relative paths (`.choix/thumbs/<bucket>/<id>-<tier>.jpg`). Don't double-prefix when reading them back.

---

## Native media

Choix runs no external tool: exiftool, ffmpeg and sips were removed and replaced by Apple frameworks in-process (`internal/platform/darwin/imageio` for stills, `avmedia` for video).

- **Formats** (`internal/scanner/format.go`): RAF, DNG, HEIC/HEIF, JPEG, TIFF and PNG stills; mov, mp4 and m4v video.
- **RAW stills use the embedded JPEG.** `internal/rawpreview` reads it straight from the RAF or DNG container (pure Go, read-only); a full RAW decode is the last resort, for a file without a usable preview. The embedded JPEG carries the in-camera look CLIP and the quality score were computed on, and is far faster.
- **Metadata from the right source.** A RAF's metadata comes from its embedded JPEG; a DNG's from the container (IFD0 and Exif IFD), with the pixel size of the embedded preview the thumbnails use; a video's from AVFoundation plus the `mvhd` atom read in Go (`QuickTime:CreateDate`).
- **One parser.** `meta.NativeReader` maps the ImageIO and AVFoundation dictionaries to a JSON record keyed like exiftool's `-j -G -n` output (`EXIF:Make`, `MakerNotes:SerialNumber`, `QuickTime:CreateDate`, …), though the app never runs exiftool. That record goes through the unchanged `meta.Parse` and is stored in `raw_exif`, so rows written by v1 and by the app are indistinguishable to `BackfillGPS` and `BackfillDeviceKey`.
- **The device key is make + model + serial number** (`meta.DeviceKey`: `"<Make> <Model>#<Serial>"`, `"Unknown"` without make or model). It must match what v1 stored, or a folder scanned by v1 and extended by the app splits one camera into two devices.
- **Parity test.** `internal/meta/parity_test.go` compares the native record with exiftool's on a real corpus. It is a dev-only check: the app never uses exiftool, and the test skips when exiftool is not installed or `CHOIX_UAT_ROOT` is not set.
- **Thumbnails** (v1's contract): tiers `thumb` 256 (Library grid, Focus rail), `preview` 1600 (Focus), `full` 4096 (made on demand by the media handler for originals the webview cannot show, such as HEIC and RAW). A tier made from an embedded JPEG is at most that many pixels **wide**; one made from any other still, or a decoded RAW, has its **long side** at most that. Output is JPEG at `<folder>/.choix/thumbs/<file_id % 256 as %02x>/<file_id>-<tier>.jpg`.
- **Video:** five full-size keyframes (at 0, 25, 50, 75 and 95 % of the duration) in `<id>-keyframes/`; the `thumb` (256 wide) and `preview` (1600 wide) tiers are made from the first one.

---

## Models and inference

- **Registry:** `local.KnownModels` (`internal/ai/local/models.go`): pinned Hugging Face revisions, sizes and SHA-256. CLIP ViT-B/32 vision (about 352 MB) for visual grouping; TOPIQ-NR (about 177 MB) for the quality rating. Both are installed in `<data dir>/models/` by `local.Installer` (resumable, verified before the file is put in place).
- **Downloads** (`internal/app/models`): sequential, CLIP first, three attempts (pauses 2 s, 10 s), then `failed` until `Models.Retry`. A model finishing its download in this session, or its setting being switched on with the model ready, queues a backfill job for every reachable folder; a model already on disk at launch queues nothing.
- **Runtime:** ONNX Runtime 1.25.0 (official `onnxruntime-osx-arm64` release, MIT) for `onnxruntime_go` v1.28.0. The shipped build loads only `Contents/Frameworks/libonnxruntime.dylib`; builds without the `production` tag also try `CHOIX_ONNXRUNTIME_DYLIB` and the Homebrew paths.
- **Sessions:** one `inference.Engine` per process owns the CLIP and quality sessions, creates each lazily, shares them with every folder session, runs one inference at a time and closes them once, at quit. Inside the App Sandbox CLIP runs on the CPU provider (spike S5: CoreML takes almost none of its graph and is slower); the quality model keeps the CoreML provider.
- **Computing embeddings and using them are separate.** The analyze stage computes an embedding only while `visual_grouping` is on and the CLIP model is ready; otherwise it leaves `ai_signals.clip_embedding` NULL and the file is still `analyzed`. Clustering (`session.clusterLocked`) uses stored embeddings according to `visual_grouping` alone, whether or not the model is present:
  - `visual_grouping` **off**: every file is treated as having no embedding, stored ones included, so clusters are the time buckets alone (and cross-device merging falls back to per-device grouping).
  - **on, model ready**: new files are embedded; files that have an embedding are sub-clustered visually; the job's CLIP backfill re-analyzes files analyzed without one, which re-clusters.
  - **on, model missing** (not downloaded yet, or failed): no new embeddings; files with stored embeddings are still sub-clustered visually, and files without one go into their time bucket's fallback cluster. When the model arrives, a backfill job embeds them.

  Visual clustering uses cosine similarity ≥ `visual_cluster_threshold` (default 0.92) inside each time bucket; changing it re-clusters.
- **Advisory only.** No auto-pick, no rerank, no scored top candidate. The user's manual pick is the source of truth.

### Quality rating

Spec and research: `docs/plans/2026-11-11-quality-rating.md`.

- **What:** a per-photo technical-quality score (focus, motion blur, noise) from TOPIQ-NR (ResNet-50, KonIQ weights), shown as a 0–10 badge. Advisory only: nothing picks, rejects, sorts or highlights by it, and there is deliberately no "best in cluster" marker (differences under ~0.2 are noise).
- **Licence:** CC BY-NC-SA 4.0 (non-commercial). Never bundled or redistributed; `build/quality-model/export.py` reproduces the ONNX file.
- **Scoring recipe** (`internal/ai/local/quality.go`): tier-2 preview → long side ≤ 1600 px and ≤ the original's (never scaled up; short side < 384 px ⇒ unrated) → five native-resolution 384 px crops (centre + quadrant centres) → mean of the three best. Changing the model or the recipe **must** bump `QualityScorerID`: rows stamped with another id become pending and their old scores are hidden.
- **Storage:** `files.quality_raw` (NULL = unrated) and `files.quality_model` (scorer id), migration 005. The 0–10 value is computed only in `local.QualityDisplay`: recalibrate there, never rescore. Only "this photo can never be rated" is stamped with a NULL score; cancellation, a disabled setting, inference or database errors leave the row pending. A changed file loses its rating in `UpsertOnRescan`.
- **Runtime:** the rating pass (`quality.Pass`, `internal/quality/pass.go`) is the last step of every folder job, after the stats and preview refresh and the "clusters" announcement, run when `quality_rating` is on and the model is ready. Switching the setting off cancels the pass in flight. A photo that cannot be rated keeps `scan_status='analyzed'`; a missing rating is not a file failure.
- **API:** `Models.Quality` returns the status snapshot and `quality:status` carries every change, so nothing polls. New ratings arrive as `folder:changed` with reason `ratings`, and the folder store then refetches `Catalog.Ratings`.

---

## Library, folder jobs and presence

- **Adding a folder** (the native picker, ⌘O, or a drop on the window) creates an app-scoped security-scoped bookmark and stores it in `library.db`. A folder that is the same as, inside, or containing one already in the Library (under any spelling: case, trailing slash, a symlink) adds nothing; the existing row is returned. Adding again a folder whose access was lost replaces its bookmark.
- **Access** is started when a folder is opened or scanned and stopped when its session closes: the kernel limits concurrently open scoped resources.
- **Removing** a folder only forgets it (after a native confirmation); nothing on disk is touched.
- **Jobs** (`session.Queue`): FIFO, up to `session.DefaultSlots` (2) at once, each on a different folder; a folder enqueued while it runs runs again afterwards. Kinds: `full` (walk first) and `backfill` (no walk). A job is persisted in `library.db` when it is queued, with a sequence number; a finished run acknowledges only its own sequence, whatever order the runs finish in. Opening a folder puts its full job at the front: it starts in the next free slot, and when both slots are busy and neither job is winding down it **preempts** the running job with the lowest priority (one no open made before one an earlier open made; among equals the one started last), which is re-queued directly behind it. An unreachable folder **suspends** its job (still pending); the presence pass re-queues a present folder that is pending but not queued. Only removing the folder cancels its job. On launch only pending jobs are re-queued; finished folders are not re-walked until they are opened or rescanned.
- **Presence** (`internal/app/library/presence.go`) is checked on launch, when the window gains focus, and every 30 s while it is visible. A folder whose volume is not mounted is `offline` (dimmed, kept). A folder whose bookmark target is gone on its mounted volume, without that absence being verified, is `missing` ("This folder can no longer be found."). A bookmark that can no longer be used makes it `access_lost` ("Choix lost access to this folder. Add it again."). A row is dropped only when the folder is verified to be in the Trash, or verified absent on its own mounted volume; never only because its bookmark fails to resolve. A renamed or moved folder is followed.
- **Read-only folders:** the folder is added, and opening it reports that Choix cannot write its `.choix` folder there. There is no fallback location.
- **Undo** is a per-folder in-memory stack of 100 entries, cleared when the folder closes.

---

## Picks directory

- `picks_dir` (default `picks`) must be a relative path inside the folder. `picks.ValidateDir` refuses an empty value, an absolute path, a path that leaves the folder, and `.`, `.choix` or anything under `.choix`. The Go side returns a reason code; the frontend shows the design's copy.
- A picks directory under which the folder has files Choix scanned as originals is refused (`picks.ErrDirHasOriginals`); unknown files never block it.
- **Per-folder legacy directory.** A folder that already has exported picks keeps exporting where most of them are (`picks.EffectiveDir`, derived from `picks.exported_path`), whatever `picks_dir` says; the setting applies to folders with no exported pick. The scanner skips every export directory, so picks are never walked as originals.
- When a session opens, `picks.Reconcile` compares pick rows with the directory. It never copies or deletes: a byte-identical copy is adopted, a missing copy is counted (`picks_missing`) and reported, never copied back.
- Picks are copies, not symlinks: symlinks break when the user moves the folder, and editors sometimes choke on them.

---

## Configuration

One source: the machine-wide `config.toml` in the app's data directory (`appdir.Config`), layered over `config.Defaults()`. No environment variables, no CLI flags, no per-folder settings. v1's `~/.choix/config.toml` is not imported (a sandboxed app cannot read it). The Settings page reads and writes it through the `Settings` binding.

| TOML key | Default | What |
|---|---|---|
| `bucket_size_sec` | 600 | Time-bucket gap (seconds) inside a device |
| `visual_cluster_threshold` | 0.92 | CLIP cosine similarity for the same-scene cluster |
| `visual_grouping` | `true` | CLIP: download the model, compute embeddings, sub-cluster visually. Off: time-only clusters, no download |
| `cross_device_merging` | `false` | Merge clusters across devices using clock-skew detection |
| `picks_dir` | `picks` | Where picks are exported (relative to the folder) |
| `advance_on_action` | `false` | Move to the next photo after Pick/Reject in Focus |
| `hide_rejected_photos` | `false` | Hide rejected photos in the Folder view |
| `quality_rating` | `true` | Advisory quality rating: download the model, score photos, show badges |

Saving `bucket_size_sec`, `visual_cluster_threshold`, `cross_device_merging` or `visual_grouping` re-clusters; clustering always reads the live settings under the session's clustering lock and stamps what it used.

**Cross-device merging** (off by default): `internal/group/skew.go` estimates a per-device clock offset from high-confidence (≥ 0.95 cosine) CLIP anchor pairs taken within ±1 hour of each other, and the grouper runs on the merged timeline. Clusters spanning devices carry the `group.MergedDeviceKey` sentinel (`"*merged*"`). Files without embeddings never merge across devices; with no embedding at all, it falls back to the per-device path.

### Where data lives

| Data | Location |
|---|---|
| Settings (`config.toml`), `library.db`, preview strips (`previews/<folder-id>/<n>.jpg`), models (`models/`) | `~/Library/Application Support/Choix/`, inside `~/Library/Containers/com.github.volodymyrsmirnov.choix/Data/` when sandboxed |
| Logs (`choix.log`) | `~/Library/Logs/Choix/`, container-relative when sandboxed |
| Per-folder state | `<folder>/.choix/state.db`, `<folder>/.choix/thumbs/` |
| Picks | `<folder>/picks/` (or the folder's picks directory) |

---

## Plans and specs

These are the source of truth for *intent*. If you're adding a feature, find it (or its absence) in the spec first.

- `docs/superpowers/specs/2026-11-11-choix-2-desktop-design.md` — the Choix 2.0 design: goals, non-goals, architecture, sandbox, native media, hard rules (§14), design gate (§15).
- `docs/superpowers/specs/2026-11-11-choix-2-spike-results.md` — measured results of spikes S1–S7.
- `docs/superpowers/plans/2026-11-11-choix-2-engine-and-wails.md` — the engine and Wails shell plan, with the calls and rulings (C1–C38) the code implements.
- `docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md` — the design system, the prototype and the approval gate. Day to day: `docs/design/README.md`.
- `docs/plans/2026-11-11-quality-rating.md` — the quality rating.
- `docs/release/` — `signing-checklist.md`, `sandbox-smoke.md`, `uat.md`; `docs/release-checklist.md` for release day.
- `v2.md` — deferred and out of scope. Don't sneak these in without renegotiating the scope.

---

## Known caveats / pitfalls

- **The Go embed needs `frontend/dist/index.html`.** `embed.go` embeds `frontend/dist`, so a fresh clone does not compile until something writes it. Every Go task runs `task frontend:bootstrap` first (a stub page from `build/frontend-stub/`); `task build` builds the real frontend.
- **Rebuilt ad hoc apps lose folder access.** Security-scoped bookmarks are tied to the signing identity, so a folder added by a differently signed build shows "Choix lost access to this folder. Add it again." Adding it again replaces the bookmark.
- **`task dev` is not sandboxed** (unsigned). The bookmark code still runs, so the same path is exercised, but sandbox denials only show in a `task package` build. Use `task smoke:sandbox` before a release.
- **The hardened runtime needs a real identity.** Under ad hoc signing, library validation refuses the bundled ONNX Runtime ("different Team IDs"), so `sign:dev` leaves the hardened runtime off. Never "fix" that with `com.apple.security.cs.disable-library-validation`.
- **`CHOIX_SELFCHECK=1`** runs a headless self-check instead of the app, prints one JSON report and exits; it stays in the shipped build because CI verifies the signed bundle with it (sandboxed, data directory writable, ONNX Runtime version, one inference per installed model, bookmark round trip, Trash check). It touches only the app's own directories, never a user folder, and makes no network request: like any start it creates the data directory, its `models/` directory and the logs directory, and opens `choix.log` for appending; its write probe creates a `.selfcheck-*.tmp` file in the data directory, writes, fsyncs and closes it, then removes it; and it makes a bookmark of the data directory in memory, storing nothing.
- **The `server` build tag is test-only.** It serves the same services and assets over HTTP on 127.0.0.1 for Playwright, with `testsupport` bound; it is never shipped. There is no HTTP API, no SSE, no idle shutdown and no browser UI in the app: the v1 shell was removed, and `internal/retired_test.go` keeps it removed.
- **Generated bindings are committed.** CI regenerates them and fails on a diff.
- **`SetMaxOpenConns(1)` is load-bearing.** If you need parallel reads, profile first.
- **Progress payloads have lowercase JSON tags** (`stage`, `done`, `total`, `failed`, `state`, `phase`). The frontend reads them; renaming silently breaks the progress display.
- **Media URL identity is folder id + relative path**, not the file id: stable across cluster rebuilds, easier to debug. Always build URLs with the helpers.

---

## Adding a feature — checklist

1. **Spec it.** If the spec doesn't cover it, write a one-paragraph addition there first (or, for big stuff, a new spec doc).
2. **Design it first if it shows.** Any visible change goes through the design gate above: prototype state, review link, owner approval, then app code.
3. **Plan it.** A TDD-format task in the relevant plan under `docs/superpowers/plans/` (or a new plan for a new subsystem).
4. **Test first.** Failing test → run-fail → minimal implementation → run-pass → commit.
5. **Run the suites.** `task test && task lint`, plus `task test:frontend`, `task test:e2e` and `task parity` when the frontend or a binding changed. All must be clean.
6. **Try the real app.** `task package`, then open `bin/Choix.app`: add a folder, let the job finish, open it, pick and unpick, check the copy in `picks/`, change a setting. For a release candidate, `task smoke:sandbox`.
7. **Commit small.** One topic per commit. Prefixes: `feat(scope):`, `fix(scope):`, `test(scope):`, `chore:`, `docs:`, `plan:`.

---

## Where to stop and ask

- The user is in control. If a request would change the spec's non-goals or anti-goals (Intel or other platforms, bundling the quality model, private APIs, network access beyond the two models, cloud telemetry, ranking auto-picks), surface the conflict before writing code.
- **Anything that touches the sandbox:** a new entitlement, a temporary exception, a helper executable, `os/exec`, a private API. The answer is almost certainly no; ask first.
- **A new download or a change to `local.KnownModels`** (URL, hash, model): ask first.
- **The design gate:** when a hook blocks a gated edit, or a change would re-stale every state, stop and tell the owner. Never approve.
- **Destructive operations on the user's data** — modifying originals, deleting `.choix/state.db` or `library.db`, renaming files in a folder: confirm first. Auto mode is not a license to destroy.
- **Signing and release:** identities, certificates, profiles and secrets belong to the owner. Never write them into the repository, never log them, never invent them.
- **Anything a person must do by hand** (key presses for spike S2, a pick through the real open panel, the hardened Developer ID run): write the runbook and hand it to the owner.
