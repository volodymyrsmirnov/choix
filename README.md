# choix

A single-binary photo-and-video culling tool for macOS. Drop it into a folder
of mixed media, run it, and a localhost web UI groups everything by
**device → time bucket → visual cluster** so you can pick favorites fast.
Picks are copied (never moved) into `./picks/` for hand-off to Luminar Neo,
Lightroom, or whatever editor you use.

> Walk in with a 2 000-photo trip dump, walk out an hour later with a
> `picks/` folder ready to edit.

## Status

- macOS only (Apple Silicon first; the release binary is a universal build
  so x86_64 still works).
- v1 — actively developed. See [`v2.md`](v2.md) for what is intentionally
  out of scope.

## Install

### Homebrew (recommended)

```bash
brew tap volodymyrsmirnov/tap
brew install choix
```

This also pulls `exiftool` and `ffmpeg`, which choix needs for metadata and
video keyframes.

### Install script

```bash
curl -fsSL https://raw.githubusercontent.com/volodymyrsmirnov/choix/main/install.sh | bash
```

Downloads the latest universal macOS binary from GitHub Releases and drops
it in `/usr/local/bin/choix`. macOS only.

### From source

Requires Go ≥ 1.22, [bun](https://bun.sh) (for the embedded SPA), and the
macOS command-line tools (`sips`, `lipo`, `codesign`).

```bash
git clone https://github.com/volodymyrsmirnov/choix.git
cd choix
make build
./choix --help
```

`make build` rebuilds the SPA bundle and stamps the version from
`git describe`. The binary is fully self-contained — the React UI is
embedded via `go:embed`.

## Usage

```bash
# Scan a folder, serve the UI on localhost, and open the browser.
choix /path/to/photos

# Useful flags:
choix /path/to/photos --port 8080       # pin a specific port
choix /path/to/photos --no-open         # don't auto-open the browser
choix /path/to/photos --idle-after 60m  # auto-shutdown after no activity
```

There are no subcommands — the web UI is the only interface. Picks,
rejects, scans, settings, and CLIP-model installs all live there.

State for each scan root is kept under `.choix/` next to your photos:

```
/path/to/photos/
├── .choix/
│   ├── state.db          SQLite — files, clusters, picks
│   └── thumbs/           cached JPEG thumbnails
├── picks/                your selections (copied, never moved)
└── …your originals
```

User-level config and downloaded ML models live under
`~/Library/Application Support/choix/`.

## How it works

A 5-stage pipeline runs over every file and resumes safely if interrupted:

```
discover → metadata → thumb → analyze → cluster
```

1. **discover** — walks the scan root, content-hashes new/changed files.
2. **metadata** — pulls EXIF via `exiftool`, derives device + capture time.
3. **thumb** — generates 256w (Library) and 1600w (Focus) JPEGs via
   `sips` for HEIC/JPEG/PNG/TIFF and `ffmpeg` for video keyframes.
4. **analyze** — computes a CLIP embedding per file (optional; needs the
   ONNX model). CoreML execution provider for ANE/Metal acceleration.
5. **cluster** — sorts by `(device, captured_at)`, splits into time
   buckets at the gap threshold, then sub-clusters each bucket by
   CLIP cosine similarity. Picks survive re-clustering untouched.

## Hard guarantees

- **Originals are sacred.** No code path opens an original in anything
  other than `O_RDONLY`. Only `picks/` and `.choix/` are ever written.
- **Picks are durable.** Every pick is `fsync`'d to SQLite synchronously
  before the API returns success. The DB is the source of truth.
- **Per-file failures are isolated.** A corrupt RAF or a video without
  keyframes fails just that one file; the pipeline keeps going.
- **Idempotent.** Running `choix .` twice is a no-op for unchanged files.

## CLIP model (optional but recommended)

Without a CLIP model installed, the analyze stage skips embeddings and the
grouper falls back to time proximity alone — same-scene bursts won't
collapse into a single cluster. Install the model from the Settings page
(first-run wizard), and the model lands in
`~/Library/Application Support/choix/models/`.

## Configuration

Settings are machine-wide and live in
`~/Library/Application Support/choix/config.toml`. Most can be edited live
from the Settings page in the UI.

| Key                        | Default | What                                                  |
|----------------------------|---------|-------------------------------------------------------|
| `bucket_size_sec`          | 600     | Time-bucket gap (seconds) within a device             |
| `visual_cluster_threshold` | 0.92    | CLIP cosine similarity for same-scene clustering      |
| `picks_dir`                | `picks` | Where pick exports land (relative to scan root)       |
| `advance_on_action`        | false   | Auto-advance to next photo after Pick/Reject in Focus |
| `hide_rejected_photos`     | false   | Hide rejected photos in the Library view              |
| `cross_device_merging`     | false   | Merge clusters across devices via clock-skew anchors  |

Precedence: CLI flags > env vars > `config.toml` > built-in defaults.

## Development

```bash
make webui         # rebuild the embedded SPA bundle
make build         # rebuild SPA + Go binary
make test          # go test -race ./...
make lint          # golangci-lint run (must be 0 issues)
make fmt           # gofmt -s -w .
make release       # universal arm64 + x86_64 binary in dist/
make verify-signed # codesign + spctl assertions on dist/choix
```

The architecture, conventions, and SQLite specifics are documented in
[`CLAUDE.md`](CLAUDE.md) — read that before contributing.

## Tech stack

- **Backend:** Go, [chi](https://github.com/go-chi/chi),
  [modernc.org/sqlite](https://modernc.org/sqlite) (pure-Go),
  [r3labs/sse](https://github.com/r3labs/sse) for live progress.
- **AI:** [onnxruntime_go](https://github.com/yalue/onnxruntime_go) with
  the CoreML execution provider for CLIP embeddings.
- **External tools:** `exiftool`, `ffmpeg`, `sips` (macOS-native).
- **Frontend:** React + TypeScript + Tailwind v3, built with `bun`,
  embedded into the binary via `go:embed`.
