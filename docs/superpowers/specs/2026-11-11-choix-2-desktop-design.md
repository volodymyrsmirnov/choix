# Choix 2.0 — desktop app design

Status: **Library layout A approved by the owner on 2026-11-11.** Every
screen state is approved individually through the design gate (§15) before
its app code is written.
Date: 2026-11-11. Supersedes the v1 "single binary + localhost web UI" shape.
Amended by `2026-11-12-builtin-grouping-and-quality-design.md` (visual grouping by Apple's Vision
feature print, a built-in quality rating, no network access): where they differ,
that spec wins, and the sections below have been updated to match.

## 1. Intent

Choix 1.x is a CLI that scans one folder and serves a web UI on localhost.
Choix 2.0 is a native-feeling macOS app: open it, see every shoot you have
added, click one, cull it. The engine (scan → metadata → thumbnails → analysis →
clusters, picks copied to `picks/`) stays. What changes is the shell around
it, and everything the Mac App Store sandbox forces.

Why now: the owner wants to distribute through the Mac App Store, and a
sandboxed app cannot depend on Homebrew's `exiftool`/`ffmpeg` or on a
browser tab talking to a local port.

## 2. Goals, non-goals, anti-goals

**Goals**

1. A Wails v3 desktop app, macOS only, Apple Silicon only (arm64).
2. Runs correctly inside the App Sandbox. One code path for the App Store
   build and the Homebrew cask build (both sandboxed).
3. A **Library** page: the list of folders the user added, grouped by month,
   each with faded preview thumbnails; add folders through the native picker.
4. No external tools. Metadata, thumbnails and video frames come from Apple
   frameworks in-process.
5. Go services with bindings and events replace the HTTP JSON API and SSE.
   Each service is isolated and unit-testable without a window.
6. A real macOS menu bar carrying the app's shortcuts.
7. A Taskfile for every common operation.

**Non-goals**

- Intel Macs, Linux, Windows.
- Redesigning the cluster grid, Focus, Compare or Settings. They are ported.
- Keeping the `choix <folder>` CLI, the HTTP server, or the browser UI.
- Importing v1's `~/.choix/config.toml` (a sandboxed app cannot read it).
- Signing, notarization and App Store submission themselves. The build
  produces everything they need; credentials and the walkthrough are a
  separate conversation with the owner once the PR is ready.

**Anti-goals** (a failure even if it "works")

- Any write to an original file, or any write in a scan root outside
  `picks/` and `.choix/`.
- A lost pick.
- Private Apple APIs (the Wails `private_mac_apis` build tag) — App Review
  rejects them.
- Any network request, and any downloaded or bundled model (amended by
  `2026-11-12-builtin-grouping-and-quality-design.md`).

## 3. Decisions made by the owner (2026-11-11)

| Topic | Decision |
|---|---|
| Framework | Wails v3 |
| Platform | macOS, arm64 only |
| Distribution | Mac App Store (sandbox) + Homebrew cask of the same `.app` |
| exiftool / ffmpeg | Replaced by Apple frameworks (ImageIO + AVFoundation) |
| Visual grouping and quality | Built into the app, nothing downloaded (owner, 2026-11-12; `2026-11-12-builtin-grouping-and-quality-design.md`) |
| App Store price | Free, so the quality rating ships there too |
| Transport | Bindings + Wails events; no HTTP server, no SSE |
| Per-folder state | Unchanged: `<folder>/.choix/` and `<folder>/picks/` |
| Icons | From `assets/icon/` |
| UI | Layout A (month timeline) approved from the mockup |
| Copy | Text never wraps into an orphan (a lone word or scrap on the last line) |
| Design process | Clickable prototype + design-approval gate (§15) |

Everything below that is not in this table is the designer's call and is
marked **[call]** where it matters.

## 4. Product

### 4.1 Library (the new home screen)

- The approved mockup (six screens, real thumbnails, so not committed) was
  reviewed by the owner in the session of 2026-11-11; this section is its
  written form.
- A list of the folders the user has added. **Layout A (approved):**
  month headers (`OCTOBER 2026`, newest first); a folder sits under the
  month of its earliest capture date and shows its full date range.
  Folders with no dated media go under "Undated" at the end.
- Each folder row: name, parent path (abbreviated with `~`), date range,
  photo/video counts, cluster count, picked and rejected counts, and a strip
  of **six preview thumbnails** sampled evenly across the shoot by capture
  time. The strip is dimmed (≈50 % opacity, slightly desaturated) and fades
  into the row from the right; it goes to full strength on hover/focus.
- Row states: `ready`, `scanning` (stage label + progress bar in the row and
  a compact indicator in the title bar), `queued`, `offline`, `error`.
- Click or Enter opens the folder. Hover/focus reveals **Open Picks** and a
  ⋯ menu (also the right-click menu): Open, Open Picks Folder, Reveal in
  Finder, Rescan, Remove from Library….
- **Add Folder** (title-bar button, File menu, ⌘O) opens the native folder
  picker; multiple selection allowed. Dragging folders onto the window does
  the same (subject to spike S1).
- Adding a folder that is already in the Library, or that is inside /
  contains one already added, selects the existing row and shows a toast
  instead of adding a duplicate **[call]**.
- **Removal:** "Remove from Library" asks for confirmation in a native alert
  and only forgets the folder. Nothing on disk is touched.
- **Folders removed in the OS disappear by themselves.** The app re-checks
  every folder on launch, when the window regains focus, and every 30 s
  while visible. A folder that no longer exists, or resolves into the Trash,
  is dropped from the Library with a toast. A folder that was renamed or
  moved is followed (the bookmark resolves to the new path).
- **Offline is not deleted [call]:** when the folder's volume is not mounted
  the row stays, dimmed, labelled `Drive "<name>" is not connected`, and
  cannot be opened. It comes back when the drive does.
- **Background work is a queue of folder jobs, two at a time, never one
  folder twice at once [call; owner, round 1: one at a time left every
  other folder "Waiting to scan" through a long quality pass].** A job is
  everything one folder needs: the pipeline pass (discover → cluster,
  resuming from each file's status), then, while visual grouping is on, the
  print backfill for files without a current feature print (they are
  analyzed again, which re-clusters), then the refresh of
  the folder's cached stats and preview strip and the `folder:changed`
  "clusters" announcement (so the folder can be reviewed while it is
  rated), then quality rating when that setting is on, announced by
  `folder:changed` "ratings". A job is queued when a folder is added, opened
  or rescanned, and for every reachable folder when visual grouping or the
  quality rating is switched on. FIFO, up to `session.DefaultSlots` (2) jobs at once, each on
  a different folder, so each folder's `state.db` keeps a single writer.
  The open folder's job jumps the queue and starts in the next free slot.
  When both slots are busy and neither job is winding down, it preempts
  the running job with the lowest priority: one no open made before one an
  earlier open made, and among equals the one started last. The preempted
  job is re-queued directly behind it. Nothing else is serialised across
  jobs: feature prints are native calls, capped by the process-wide slots
  whatever the number of jobs, and the quality rating is pure Go.
- On launch only folders whose last job did not finish are re-queued (a
  flag in `library.db`). Finished folders are not re-walked until they are
  opened or rescanned **[call]** — a 5 000-file folder on a slow disk
  should not be walked just because the app started.
- Security-scoped access to a folder is held for the length of its job and
  while it is open, and released otherwise.

### 4.2 First launch

- No wizard and nothing to download. With no folder added, the Library
  shows its empty state (the drop zone).
- Visual grouping (`visual_grouping`, default on) and the quality rating
  (`quality_rating`, default on) are switches in Settings. With visual
  grouping off, folders cluster by time only. Both run inside the app
  (§8).

### 4.3 Inside a folder

- The v1 Library page becomes the **Folder** page (cluster grid), with a new
  title bar: back to Library, folder name, counts, **Open Picks** (⌘P).
- Focus, Compare, pixel-peep, Info panel, Settings, the shortcut sheet and
  toasts keep their design and behaviour.
- Esc / ⌘[ go back one level (Focus → Folder → Library). Scroll positions of
  the Library and of each Folder page are remembered for the session.

### 4.4 Menu bar and shortcuts

| Menu | Items |
|---|---|
| Choix | About, Settings… ⌘, , Hide, Quit |
| File | Add Folder… ⌘O · Open Picks Folder ⌘P · Reveal Folder in Finder ⇧⌘R · Rescan Folder ⌘R · Remove Folder from Library ⌘⌫ · Close Window ⌘W |
| Edit | Undo Pick/Reject ⌘Z · Cut/Copy/Paste/Select All · Find… ⌘F |
| Photo | Pick Space · Reject X · Next/Previous Photo →/← · Next/Previous Cluster ↩/⇧↩ · Open in Focus F |
| View | Back ⌘[ · Compare C · Pixel-Peep Z · Info Panel I · Zoom In/Out/Fit ⌘+/⌘−/⌘0 · Enter Full Screen ⌃⌘F |
| Window | Minimize, Zoom |
| Help | Keyboard Shortcuts ? · Choix on GitHub |

- Every v1 key keeps working, including ⌘K for search.
- Menu items are enabled/disabled from the current screen and selection.
- Modifier shortcuts (⌘O, ⌘R, …) are native menu accelerators. Menu clicks
  reach the frontend as events and run the same action the key runs.
- Single-key shortcuts stay handled in the webview by the existing
  layout-independent code (`keys.ts`) and must not fire while a text field
  has focus. Whether they can also be *displayed* in the menu without the
  menu stealing the keystroke is spike S2; the fallback is to list them
  only in the shortcut sheet.

### 4.5 Dialogs **[call]**

- Native: folder picker, menus, confirmation alerts, Finder integration.
- In-window, as today: Settings page, shortcut sheet, toasts with Undo.

### 4.6 Look

- Dark only, v1 colour tokens. Hidden-inset title bar with the traffic
  lights; the app draws its own 48 px toolbar, which is the drag region.
- No translucency (it needs private APIs).
- Inter and JetBrains Mono are bundled as `woff2` files (OFL); v1 loaded
  them from Google Fonts, which a sandboxed offline app must not rely on.
- Minimum window 960 × 600; default 1280 × 800; size and position restored.
  The design is reviewed at 960 × 600, 1280 × 800 and 1680 × 1050.
- **No orphans.** A multi-line text block whose last line is a single short
  word is a defect. Copy goes through a helper that binds its last two words
  with a no-break space, because `text-wrap: pretty` (Safari 26) and
  `balance` (Safari 17.5) are missing on the older macOS versions the app
  supports; those CSS properties are used on top where available. An
  automated check runs on every prototype state, in Chromium and WebKit,
  with and without `text-wrap`.

## 5. Architecture

```
cmd/choix (main.go: Wails app, window, menu, service registration)
  └─ internal/app/…            one package per service, Wails-facing
       library   folders: add / remove / list / presence / previews
       session   one folder's store + pipeline; the folder-job queue
       catalog   clusters, focus payloads, picks, undo, recluster,
                 quality-rating status
       settings  machine-wide settings
       system    Finder integration, app info
       menu      builds the native menu from frontend/src/design/menus.json
       dialogs   native alerts and the folder picker
       window    window options and chrome
                 (native UI is constructed only in menu, dialogs and window;
                 all three are design-gated)
       media     http.Handler for /thumb, /full, /preview (not bindings)
  └─ internal/platform/darwin  cgo + Objective-C, no Wails imports
       bookmark  security-scoped bookmarks
       imageio   metadata + thumbnails for stills
       avmedia   metadata + frames for video
       volumes   mount state, trash detection
       vision    Vision image feature print
  └─ engine (kept): store, scanner, pipeline, analyze, group, picks,
       quality, config, thumb (rewritten on top of platform/darwin),
       meta (parser kept for old rows; extractor rewritten)
```

Rules:

- A service is a plain Go struct with constructor-injected dependencies
  (interfaces for the platform layer, clock, event emitter). Tests construct
  it directly; nothing in a service test needs a window or cgo.
- Only `cmd/choix` and `internal/app/*` may import Wails. The engine and
  the platform layer do not.
- Platform packages expose small Go interfaces; services depend on the
  interface, and tests use fakes. The cgo implementations have their own
  tests that run against `testdata/` fixtures.
- **Events** (typed, registered with `application.RegisterEvent`):
  `library:changed`, `scan:progress` (folder id, stage, done, total, failed,
  phase), `scan:state`, `quality:status`,
  `menu:<action>`, `folder:changed`. Progress events are coalesced to at
  most ~10 per second per folder. The frontend fetches current state through
  a binding when it mounts, then applies events, so a missed event cannot
  leave it stale (this replaces SSE replay).
- **Media** is served by an `http.Handler` mounted on the Wails asset server
  (`/thumb/<folder-id>/<rel-path>?tier=…&v=<hash>`,
  `/full/<folder-id>/<rel-path>?v=<hash>`, and
  `/preview/<folder-id>/<n>` for Library strips). Same lookup-through-store
  rule as v1: paths are resolved by `Files().GetByPath` and joined under the
  root; no traversal surface. `http.ServeContent` gives Range support for
  video. Video playback over the `wails://` scheme is spike S3.
- A `session` is one folder's SQLite store plus its pipeline. The open
  folder has one; the job queue may hold a second, headless one for the
  folder it is working on (the same session when they are the same folder).
- Feature prints are made through `platform.Vision`, a native call run
  detached like an ImageIO call; the quality rating is pure Go. Neither
  holds app-level state, so sessions share nothing but the native-call
  slots.
- Teardown order per session is v1's: quality pass → pipeline stop → wait
  for the session's detached native calls → close store. At quit: stop the
  queue, tear down sessions, then close `library.db`, within the quit
  deadline.
- The idle-shutdown timer, the `X-Choix-Background` header, the port picker
  and browser auto-open are deleted with the server.

## 6. Sandbox and folder access

- Entitlements (both builds): `com.apple.security.app-sandbox`,
  `com.apple.security.files.user-selected.read-write`,
  `com.apple.security.files.bookmarks.app-scope`. There is no network
  entitlement: the app makes no network request, and the owner removed
  `com.apple.security.network.client` in November 2026. The Mac App Store
  build adds only the two identifiers its provisioning profile requires
  (`com.apple.application-identifier`, `com.apple.developer.team-identifier`).
- When the user picks or drops a folder, the app creates an **app-scoped
  security-scoped bookmark** and stores it in the library database. Wails
  has no API for this; `internal/platform/darwin/bookmark` implements
  create / resolve / start-access / stop-access in Objective-C.
- On launch each bookmark is resolved. `stale` bookmarks are re-created.
  Access is started when a folder is opened or scanned and stopped when its
  session closes (the kernel limits concurrently open scoped resources).
- Read-write on the selected folder is what lets the app keep writing
  `<folder>/.choix/` and `<folder>/picks/`. `picks_dir` is already
  restricted to a path inside the folder in v1 (`validatePicksDir`), which
  is exactly what the sandbox needs; that rule is kept.
- Read-only volumes (a locked SD card): the folder is added, and the app
  reports clearly that it cannot write `.choix/` there. It does not fall
  back to storing state elsewhere in 2.0 **[call]**.
- In unsigned dev builds (`task dev`) the sandbox is off; the bookmark code
  still runs (plain bookmarks) so the same path is exercised.

## 7. Native media (replaces exiftool, ffmpeg, sips)

- **Stills** — ImageIO: `CGImageSourceCopyPropertiesAtIndex` for metadata;
  `CGImageSourceCreateThumbnailAtIndex` (transform applied) for the tiers,
  written with `CGImageDestination`. Tiers are v1's: 256 px wide, 1600 px
  wide, and the on-demand "full" JPEG capped at 4096 px wide.
- **RAW stills use the embedded JPEG, never a RAW decode, as in v1.** v1
  measured ~60 s per X-T5 RAF for a full ImageIO decode against ~0.5 s for
  the embedded JPEG, and the embedded JPEG carries the in-camera look that
  the feature print and the quality score are computed from. For RAF the embedded JPEG is
  read straight from the container (a fixed header gives its offset and
  length; a few lines of Go, read-only), then downscaled through ImageIO.
  For DNG the largest embedded preview is used. A full Apple RAW decode is
  the last resort only, for a file with no usable embedded preview. This
  also settles the reported width: it is the embedded JPEG's, as in v1.
- Accepted formats are v1's: RAF, DNG, HEIC, JPEG, TIFF, PNG stills and
  mov / mp4 / m4v video.
- **Video** — AVFoundation: `AVAsset` metadata (creation date, make, model,
  ISO 6709 location, duration, dimensions) and `AVAssetImageGenerator` for
  the five evenly spaced keyframes.
- Output contract is unchanged: same tiers, same cache paths
  (`.choix/thumbs/<bucket>/<id>-<tier>.jpg`), JPEG output, originals opened
  read-only.
- **Parity is a requirement, not a hope.** The native extractor must produce
  the same `captured_at`, `device_key` (make, model **and serial number**),
  GPS and Info-panel fields as v1 for the formats in `testdata/` and in the
  owner's own photos. `device_key` matters most: a folder scanned by
  v1 and extended under v2 must not split one camera into two devices. A
  dev-only comparison test (skipped when `exiftool` or the corpus is absent)
  reports any difference.
- **One parser.** The native extractor turns the ImageIO / AVFoundation
  dictionaries into a JSON record with the *same keys exiftool's `-j -G -n`
  output uses* (`EXIF:Make`, `EXIF:DateTimeOriginal`,
  `MakerNotes:SerialNumber`, `QuickTime:CreateDate`, `Composite:GPSLatitude`
  …) for every field `meta.Parse` reads. That record goes through the
  existing `meta.Parse` and is what is stored in `raw_exif` (gzip JSON). So
  v1 rows and v2 rows are indistinguishable to `BackfillGPS` /
  `BackfillDeviceKey`, and parity is a comparison of two JSON records.
- Serial number priority stays `MakerNotes:SerialNumber`, then
  `EXIF:SerialNumber`, then `InternalSerialNumber` (spike S4 finds where
  ImageIO exposes each).
- Unsupported or corrupt file: that file is `scan_status='failed'` with the
  error text, as today.
- `internal/deps` (tool resolution) and the `sips`/`ffmpeg`/`exiftool`
  fallback chain are deleted.

## 8. Visual grouping and quality rating

Specified in `2026-11-12-builtin-grouping-and-quality-design.md`; in short:

- **Visual grouping** uses Apple's Vision image feature print (revision 2,
  scale fill, 768 values) of each file's preview tier, made by the analyze
  stage and stored in `ai_signals.vision_print`, and sub-clusters each time
  bucket by complete-link at the `visual_similarity` setting (default
  0.90). Off, folders cluster by time only.
- **The quality rating** is a sharpness, noise and exposure formula in pure
  Go on the preview tier, shown as the 0–10 badge. It tells a damaged frame
  from a good one of the same scene; it does not rank different scenes.
- Nothing is downloaded or bundled for either, and the app makes no network
  request.

## 9. Where data lives

| Data | Location |
|---|---|
| Settings (`config.toml`) | `~/Library/Application Support/Choix/` — inside `~/Library/Containers/<bundle-id>/Data/…` when sandboxed |
| Library (folders, bookmarks, cached stats, preview strips) | same directory: `library.db` + `previews/<folder-id>/<n>.jpg` |
| Logs | `~/Library/Logs/Choix/` (container-relative when sandboxed) |
| Per-folder state | `<folder>/.choix/state.db`, `.choix/thumbs/` (unchanged schema; v1 folders open without a rescan) |
| Picks | `<folder>/picks/` |

- `library.db` is a second small SQLite database with its own migrations.
  It caches what the Library page shows (counts, date range, month, preview
  files) so the page renders instantly and offline folders still have a
  face. The per-folder `state.db` stays the source of truth; the cache is
  refreshed when a scan ends and when picks change.
- Paths come from `NSSearchPathForDirectoriesInDomains` so the same code is
  right in and out of the sandbox. `internal/appdir` is rewritten on that.

## 10. Build and distribution

- Wails v3.1 (stable), Go ≥ 1.26, React + TypeScript + Tailwind kept. The
  frontend moves to `frontend/` with Vite (the Wails default, needed for
  typed events and `wails3 dev`); bun stays the package manager **[call]**.
- `frontend/src/design/` holds the design system and every screen as a
  presentational component; `frontend/src/app/` holds the containers that
  feed them from bindings and events; `frontend/prototype/` is the second
  build entry (§15).
- Bundle identifier: `tech.inkhorn.choix` (from 2.2.0; 2.0.0 and 2.1.0 shipped as `com.github.volodymyrsmirnov.choix`)
  **[call]**, to be confirmed in the signing conversation. Product name
  `Choix`. Deployment target macOS 14 (the Vision feature print revision the
  app pins, revision 2, needs it; the first choice was macOS 13).
- `Taskfile.yml` replaces the Makefile: `dev`, `build`, `package` (the
  `.app`, arm64), `test`, `test:e2e`, `lint`, `fmt`, `bindings`, `icons`,
  `sign:dev`, `sign:developer-id`, `notarize`, `dmg`,
  `package:mas` (`.pkg` with provisioning profile), `clean`. Signing tasks
  read identities and profiles from environment variables and fail with a
  clear message when they are missing; none are needed for `build`/`test`.
- Icons: `assets/icon/choix.icns`, the Icon Composer layers and
  `favicon.svg` feed the bundle; `task icons` regenerates them.
- Homebrew: a cask that installs the notarized `.app`; the v1 formula and
  `install.sh` are retired.
- CI (`.github/workflows`): lint, Go tests, frontend build and unit tests,
  unsigned `.app` build. Release workflow is updated but signing secrets are
  wired only after the owner provides them.
- `CLAUDE.md` and `README.md` are rewritten for the new shape, including the
  amended hard rules 5 and 6.

## 11. Testing

- **Go unit tests** for every service and engine package, race detector on,
  no network, no window. Platform fakes for bookmarks, volumes, media.
- **Platform tests** (cgo) against `testdata/` fixtures: metadata values,
  thumbnail dimensions, video frames, bookmark round-trip.
- **Frontend unit tests** (Vitest) for the Library page states and the
  event-driven stores, with bindings mocked.
- **E2E, two layers:**
  1. Playwright against the Wails `server` build tag of the same app (same
     services and assets over HTTP, test-only, never shipped): add folder
     (path injected through a test-only binding, since native dialogs cannot
     be automated), scan to completion, open, pick/unpick, undo, recluster,
     settings, remove.
  2. A smoke of the real `.app`, **sandboxed** (ad-hoc signed with the
     release entitlements, so the sandbox, bookmarks and Vision are all
     exercised). A sandboxed app only gets folder access
     through the picker or a drop, so this layer is driven through the real
     UI (the open panel, by UI scripting) and is also written down as a
     checklist in `docs/`: launch, add a copy of a folder from the owner's photo folder (`$CHOIX_UAT_ROOT`), scan,
     pick, verify the copy in `picks/`, Open Picks, delete the test folder
     in Finder and see it leave the Library, relaunch and see state and
     folder access restored.
- `make test`'s contract carries over: `task test` and `task lint` must be
  clean before any commit.

## 12. Risks and the spikes that retire them

| # | Question | Fallback | Result (2026-11-11) |
|---|---|---|---|
| S1 | Does dropping a folder on the window give a usable, bookmarkable path in the sandbox? | Picker only | **Folder access: pass** — grant, bookmark, relaunch, rename, Trash and deletion detection work in the sandbox (measured with a Launch Services grant); the two `openURL:` calls return success and no Finder call logs a sandbox denial, but what Finder showed was not looked at. **Drop itself: not run** — it needs a hand on the window, as does a pick through the Wails open panel; both are left for the owner with a runbook, and file drop stays in the design until then |
| S2 | Can single-key shortcuts be shown in the menu without stealing typing? | Listed in the shortcut sheet only | **Not run** — needs key presses by hand (runbook left for the owner). Single-key accelerators do become native key equivalents; until the presses are made the fallback is the default: single keys stay with the webview and are listed in the shortcut sheet |
| S3 | Does `<video>` play and seek over `wails://` with Range? | Poster + keyframes, "Open in QuickTime" | **Pass.** In-app video stays; the media route sets `Content-Type` itself |
| S4 | Does ImageIO expose the Fuji body serial exiftool reports? | Read the maker note directly, or normalise `device_key` with a backfill | **Pass** for the bodies tested (`{Exif}.BodySerialNumber`); no fallback. iPhone and DNG files were not available to test |
| S5 | Retired: it measured how the models the app used to download ran in the sandbox | | **No longer applies:** the app runs no model (§8). The measured answer is in the spike results |
| S6 | Does a hardened, sandboxed build load the bundled dylib? | Sign it with the app identity (expected to be enough) | **Sandboxed: pass. Hardened: only with a real identity** — under ad-hoc signing library validation rejects the dylib (different Team IDs), so development builds were signed without the hardened runtime and the hardened build is verified in the signing conversation. **No longer applies:** the app bundles no library (§8); development builds are still signed without the hardened runtime, and turning it on is a follow-up |
| S7 | How fast is the embedded-JPEG path for RAF and DNG, and does DNG always carry a usable preview? | RAW decode for the files that do not, with the cost shown in scan progress | **RAF: pass** (well under 0.5 s). DNG could not be measured (no sample); both DNG paths are built as planned |

Measured results are in `2026-11-11-choix-2-spike-results.md`.

Spikes run first; a failed spike changes the affected section here before
the dependent work starts.

## 13. Left to the builder

- Exact Go type and method names, event payload shapes, SQL for
  `library.db`.
- How the scan queue is represented, as long as it is one-at-a-time, FIFO,
  cancellable within ~1 s, and survives a folder disappearing mid-scan.
- Component breakdown of the Library page.
- Vite and Vitest configuration.

## 14. Hard rules carried from v1 (amended)

1. Originals are sacred — unchanged.
2. Never lose a pick — unchanged.
3. Per-file failures are isolated — unchanged.
4. Cancellation within ~1 s — unchanged, with no exception: a Vision
   feature-print request is a native call like an ImageIO call, run
   detached and holding a native slot until it really returns.
5. **macOS on Apple Silicon only.** No universal binary. Deployment target
   macOS 14 (the Vision feature print revision the app pins needs it).
6. **The app makes no network request.** Nothing is downloaded at run time
   or fetched at build time for the app's own function.
7. No duplicate files at the schema level — unchanged.
8. **New: the app never reaches outside the sandbox.** No private APIs, no
   temporary-exception entitlements, no helper executables.

## 15. Design gate (owner's process, 2026-11-11)

No app UI code is written, committed or merged for a screen state the owner
has not approved, and an approval goes stale when the design changes. The
agent builds and edits the prototype freely and can never approve its own
work. The checks are tripwires that catch honest mistakes and make bypasses
visible in the diff; they are not a sandbox.

- **Prototype:** a second Vite entry that shows every screen in every state
  as a numbered scenario (`#sN`), on mock data, with a fixed clock.
- **Screens are shared:** the real app mounts the same presentational
  components the prototype shows.
- **Native surfaces** (menu bar, context menus, alerts) are reviewed as HTML
  replicas. The menu structure lives in one manifest
  (`frontend/src/design/menus.json`) that the prototype renders and
  `internal/app/menu` builds the native menu from; a Go test asserts the
  built menu equals the manifest, so the approved menu is the shipped one.
- **Fingerprints, ledger, hooks, CI, parity:** as specified in the plan
  `docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md`.
- One rule: app UI code can be edited only while every state is approved
  at its current hash. The owner approves all states before any screen is
  wired; the Go side is not gated.
- Approver: the repository owner (git email in `docs/design/approvers.txt`).
- Design source: none (no Figma). The reference is the approved mockup and
  the v1 UI.

