# Choix

> 📝 Blog post: [A tool to pick the best photo out of 100, choix](https://volodymyr.smirnov.im/writing/choix/)

A photo-and-video culling app for macOS on Apple Silicon. Add your shoots to the
Library; Choix scans each folder in the background and groups its files by
**device → time bucket → visual cluster**, so you can pick favourites fast.
Picks are copied (never moved) into the folder's `picks/` subfolder for hand-off
to Luminar Neo, Lightroom, or whatever editor you use.

> Walk in with a 2 000-photo trip dump, walk out an hour later with a
> `picks/` folder ready to edit.

## Status

- **Choix 2.0** is a sandboxed desktop app built with [Wails v3](https://v3.wails.io).
  It replaces the 1.x command-line tool and its localhost web UI, which were removed.
- macOS 14 or later, Apple Silicon only. There is no Intel build and no universal binary.
- Signed and notarized releases are on
  [GitHub Releases](https://github.com/volodymyrsmirnov/choix/releases) and in the
  Homebrew tap (below); 2.0.0 was the first. Developers can also build it from source (below).
  See [`v2.md`](v2.md) for what is intentionally out of scope.

## Install

Each release tag `v2.x.y` makes the release workflow
(`.github/workflows/release.yml`) sign, notarize and publish the disk image on
GitHub Releases and update the cask
([`docs/release/signing-checklist.md`](docs/release/signing-checklist.md)).

- **Homebrew cask** — the notarized disk image from GitHub Releases, through the owner's tap:

  ```bash
  brew install --cask volodymyrsmirnov/tap/choix
  ```

  `brew upgrade --cask choix` installs a newer release.
- **Disk image** — download `Choix-<version>-arm64.dmg` from
  [GitHub Releases](https://github.com/volodymyrsmirnov/choix/releases), open it and drag
  Choix to Applications. It is signed with a Developer ID and notarized by Apple.
- **Mac App Store** — planned, not available yet: the same sandboxed app, free, listed as
  "Choix: Photo Culling". `task package:mas` builds the package.

**Upgrading from 2.0 or 2.1.** From 2.2.0 the bundle identifier is `tech.inkhorn.choix`
(it was `com.github.volodymyrsmirnov.choix`), and macOS gives the app a new sandbox
container. The Library and the settings therefore start empty after the update: add your
folders again. Each folder's clusters, thumbnails and picks are kept, because they live in the
folder's own `.choix` and `picks` folders, so nothing is scanned again.

## First launch

1. **Add folders.** The Library opens empty. Click **Add Folder** (⌘O) and
   choose one or more folders in the system picker, or drop folders on the
   window. There is nothing to download first: visual grouping and the quality
   rating are built in, and Choix makes no network request at all.
2. **Cull.** Folders scan in the background, up to two at a time; the one you
   open goes first. Open a folder to see its clusters, then pick (Space) or reject
   (X), move with the arrow keys, compare (C), pixel-peep (Z), undo with ⌘Z.
   Press `?` for every shortcut. **Open Picks** (⌘P) shows the copies in Finder.

Removing a folder from the Library only forgets it; nothing on disk is touched.
A folder you delete in Finder leaves the Library by itself; one on a drive that
is not connected stays, dimmed, until the drive is back.

## How it works

Each folder goes through a five-stage pipeline that resumes safely if
interrupted:

```
discover → metadata → thumb → analyze → cluster
```

1. **discover** — walks the folder, content-hashes new and changed files.
2. **metadata** — reads EXIF and video metadata through Apple's ImageIO and
   AVFoundation, and derives the device (make, model and serial number) and
   capture time.
3. **thumb** — makes 256 px (grid) and 1600 px (Focus) JPEGs. RAW files (RAF,
   DNG) use the JPEG preview embedded by the camera; videos get five keyframes.
4. **analyze** — computes an image feature print per file with Apple's Vision
   framework, on the device, from the 1600 px preview, or from the 256 px
   thumbnail of a file that has no preview (when visual grouping is on).
5. **cluster** — sorts by `(device, capture time)`, splits into time buckets at
   the gap threshold, then sub-clusters each bucket by how alike the feature
   prints are (complete-link: every shot in a cluster is close to every other).
   Picks survive re-clustering untouched.

The quality rating runs after that, in the same background job.
Supported files: RAF, DNG, HEIC, JPEG, TIFF and PNG photos; MOV, MP4 and M4V videos.
Choix runs no external tools: the exiftool, ffmpeg and sips dependencies of 1.x
are gone, replaced by Apple frameworks inside the app.

## Guarantees

- **Originals are sacred.** Originals are only ever opened read-only. Inside a
  folder Choix writes only its `.choix/` working directory and the picks
  folder, and refuses to write through a symbolic link.
- **Picks are durable.** A pick, reject or undo reaches the disk (with a full
  fsync) before the app shows it as done.
- **Per-file failures are isolated.** A corrupt RAF or a video without a video
  track fails just that file; the scan keeps going.
- **Nothing leaves your Mac.** Choix makes no network request: grouping and
  the quality rating run on the device, with nothing to download.
- **Sandboxed.** The app runs in the macOS App Sandbox. It reaches a folder
  only after you chose it, through a security-scoped bookmark.
- **Folders scanned by Choix 1.x open as they are**, with their clusters,
  thumbnails and picks, without a rescan.

## Quality rating

Each photo gets an advisory **technical-quality rating from 0 to 10**
(one decimal, 0 = worst), shown as a small badge on thumbnails, in Focus, in
every Compare pane and in the Info panel. It exists for one job: inside a
cluster of near-identical frames, showing which one is sharpest and cleanest.

- **What it measures:** sharpness (focus and motion blur), noise and exposure
  (a dark, blown-out or clipped frame), with a small formula built into the
  app and run on the 1600 px preview (a few tens of milliseconds a photo).
  Nothing is downloaded and there is no model.
- **What it does not measure:** composition, expression, exposure taste or
  whether the sharp part is your subject. A frame focused on the background
  rates like a sharp one.
- **How to read it:** compare numbers *within a cluster*. The rating tells a
  damaged frame from a good one of the same scene; it does not rank good
  photos of different scenes. Critical sharpness is still a job for
  pixel-peep (`Z`).
- **It never acts:** nothing is picked, rejected, hidden or reordered by it,
  and there is no shortcut tied to it.
- **Photos only.** Videos, images under 384 px on a side and extreme panoramas
  are left unrated.

It is **on by default**. Turn it off in Settings → Behaviour → Quality rating.
Off means no scoring and no badges.

## Settings

Settings are machine-wide and edited on the Settings page (⌘,). They are
stored in `config.toml` in the app's data directory (see below); there are no
command-line flags, environment variables or per-folder settings.

| Key                        | Default | What                                                         |
|----------------------------|---------|--------------------------------------------------------------|
| `bucket_size_sec`          | 600     | Time-bucket gap (seconds) within a device                    |
| `visual_similarity`        | 0.90    | How alike two shots of one time bucket must look to share a cluster |
| `visual_grouping`          | true    | Visual grouping with Apple's on-device image analysis; off means time-only clusters |
| `cross_device_merging`     | false   | Merge clusters across devices via clock-skew anchors         |
| `picks_dir`                | `picks` | Where picks are copied (a folder inside the photo folder)    |
| `advance_on_action`        | false   | Move to the next photo after Pick/Reject in Focus            |
| `hide_rejected_photos`     | false   | Hide rejected photos                                         |
| `quality_rating`           | true    | Advisory 0–10 technical-quality rating (see above)           |

A folder that already has picks keeps copying them where they are, whatever
`picks_dir` says. The `visual_cluster_threshold` key of earlier versions is
ignored: its scale was different, so the similarity starts from 0.90. Settings from Choix 1.x (`~/.choix/config.toml`) are not
imported: a sandboxed app cannot read them.

## Where data lives

| Data | Location |
|---|---|
| Settings (`config.toml`) | `~/Library/Application Support/Choix/` — inside `~/Library/Containers/tech.inkhorn.choix/Data/…` when sandboxed |
| Library (folders, bookmarks, cached stats, preview strips) | same directory: `library.db` + `previews/<folder-id>/<n>.jpg` |
| Logs | `~/Library/Logs/Choix/` (container-relative when sandboxed) |
| Per-folder state | `<folder>/.choix/state.db`, `<folder>/.choix/thumbs/` |
| Picks | `<folder>/picks/` |

## Building from source

You need macOS 14 or later on Apple Silicon, the Xcode command-line tools,
[Go](https://go.dev) 1.26.2 or later, [bun](https://bun.sh),
[golangci-lint](https://golangci-lint.run) and [Task](https://taskfile.dev).

```bash
git clone https://github.com/volodymyrsmirnov/choix.git
cd choix
task tools build package
open bin/Choix.app
```

- `task tools` installs the pinned `wails3` CLI and checks for golangci-lint and bun.
- `task build` builds the frontend and the arm64 binary `bin/Choix`.
- `task package` assembles `bin/Choix.app` and signs it ad hoc with the
  sandbox entitlements.

`task dev` runs the app with hot reload (unsigned, so not sandboxed).
`task --list` shows every task. Signing for distribution (`task sign:developer-id`,
`task notarize`, `task dmg`, `task package:mas`) needs the owner's Apple
Developer identities; see [`docs/release/signing-checklist.md`](docs/release/signing-checklist.md).

## Testing

| Command | What it proves | What it needs |
|---|---|---|
| `task test` | Every Go package, with the race detector | Nothing extra |
| `task lint` | `go vet` and golangci-lint, 0 issues | golangci-lint |
| `task test:smoke` | The pipeline end to end on a synthetic folder | Nothing: no network |
| `task test:e2e` | Playwright against the app's test-only server build: the bindings (`api`) and the real UI (`ui`) | Playwright Chromium: `cd frontend && bunx playwright install chromium` |
| `task parity` | The app renders exactly the approved design states | Playwright Chromium |
| `task test:frontend` | Frontend unit tests, type check and design lint | bun |
| `task design:*` | The design prototype and its approval gate ([`docs/design/README.md`](docs/design/README.md)) | bun, Playwright |
| `task smoke:sandbox` | The packaged, sandboxed `Choix.app`, then a manual checklist ([`docs/release/sandbox-smoke.md`](docs/release/sandbox-smoke.md)) | `CHOIX_UAT_ROOT` set to a photo folder (no default), unless run with `-- --head-only` |
| `task uat` | The whole app engine over clones of a real photo archive ([`docs/release/uat.md`](docs/release/uat.md)) | `CHOIX_UAT_ROOT` (no default; skips without it); hours on a large archive |

The architecture, hard rules and conventions are documented in
[`CLAUDE.md`](CLAUDE.md); read that before contributing.

## Tech stack

- **App:** Go, [Wails v3](https://v3.wails.io) (bindings and typed events, no
  HTTP server), [modernc.org/sqlite](https://modernc.org/sqlite) (pure Go).
- **Native media:** ImageIO and AVFoundation through cgo and Objective-C;
  security-scoped bookmarks and `NSWorkspace` for folder access and Finder.
- **Grouping and rating:** Apple's Vision image feature print for visual
  grouping; a sharpness, noise and exposure formula in Go for the quality
  rating. No model is downloaded or bundled.
- **Frontend:** React + TypeScript + Tailwind, built with Vite and `bun`.
