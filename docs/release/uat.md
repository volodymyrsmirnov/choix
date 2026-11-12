# UAT on a real corpus

`TestUAT` (`internal/e2e/uat_test.go`, build tags `uat` and `darwin`) runs the whole headless runtime — the real
platform packages (ImageIO, AVFoundation, Vision, bookmarks, volumes) and the quality formula — over clones of the
owner's photo archive, and checks what a user relies on. It is the last check before a release; a failure is a bug to
fix, or, for a single unreadable source file, an entry in this document with the file's format and error.

## Privacy

The archive is private and this repository is public (R22). Nothing from it is written here, in a commit, or in the
test's output:

- Folders are named `folder-1` (the most media files) … `folder-N`; a file is `folder-K, <extension>, #<index>`.
- Folder names, file names, serial numbers, camera models, dates and locations are never printed. The device-key
  check reports how many camera models are split (and how many have more than one body), never which.
- Text from outside the test is redacted by what the test knows, never by what a path looks like:
  - An error that holds a path (`*fs.PathError`, `*os.LinkError`, `*os.SyscallError`) is printed as its operation
    and the system error only.
  - In any other text (an error, a stored error column, a tool's output), the archive root and the temporary, data
    and home directories are cut together with the rest of their path, to the end of the quoted string or the line.
  - Every name the test read from the archive's trees is then replaced, longest first. That covers each relative
    path, each directory prefix of one, and each component of two or more characters with and without its
    extension, raw, `%q`-escaped and with OS separators.
  - If a known name, or a path separator followed by a non-space character, is still there, the text is withheld:
    `<error text withheld> (<Go type>)`. The test logs how many texts were withheld.
  - The name set is kept in memory only. Names inside `.choix` directories are not in it: Choix names those itself.
- Expected counts are computed by the test from the tree itself; nothing about the archive is a literal in the code.
- Log records name files, so the test swallows them and reports only how many warnings and errors there were.

The per-folder table the test prints is not recorded here: its counts describe the archive. Results below are
verdicts and archive-independent measurements only.

## What it does

1. Takes every immediate sub-folder of `$CHOIX_UAT_ROOT` that holds media (by the scanner's rules), sorted by file
   count, and records each one's manifest (relative path, size, modification time; Finder's `.DS_Store` left out).
2. Clones each folder with `cp -cR` (APFS clones, no extra space) into a temporary directory; with a limit, only the
   first N media files, with `cp -c`. The archive is only read.
3. Points `CHOIX_DATA_DIR` at a temporary directory, writes a `config.toml` with visual grouping and the quality
   rating both on, and starts a runtime on the real platform (Vision's feature print included). Nothing needs to be
   downloaded or installed first.
4. For each folder in turn: adds the clone, waits for `Idle()`, then checks
   - every file is `analyzed` or `failed`, at most 0.5 % failed, each failure logged with its error;
   - photos + videos equals the media files the test counts by extension in the clone, outside what the scanner
     skips: hidden directories (`.choix` among them) and `node_modules` at any depth, and the folder's export
     directories exactly as the engine works them out — the effective picks directory and every other directory
     earlier picks were exported to (`picks.EffectiveDir` over the folder's `state.db` and the `picks_dir` setting),
     each validated, and left out when `state.db` holds scanned originals under it (then it is walked as originals);
   - every non-failed photo has `thumb` and `preview` rows whose files exist and match the rows. RAF/DNG: width
     `min(limit, displayed width of the embedded JPEG)` — the candidate the engine uses, read through the same ImageIO
     reader, its orientation applied — so neither a missing downscale nor an upscale passes. Other stills: long side
     `min(limit, the source's long side)`. Limits 256 and 1600. Every video has five keyframes;
   - the folder has clusters, and at least 99 % of non-failed files have a feature print (768 values, stamped
     `analyze.PrintID`);
   - no camera is split into two devices: a model never has a serial-less key beside exactly one serial'd key, and
     one serial never appears under two spellings of make and model; two bodies of one model (two serials) are two
     devices by design and are only logged; no DNG has device key `Unknown` or a capture time equal to its file time;
   - `Library.List()` shows six previews (fewer only when the folder has fewer files);
   - the first, middle and last file of the grid are picked, copied byte-identical into `picks/`, and the copies are
     removed by unpick.
5. Preemption: while a fresh clone of `folder-1` scans, a fresh clone of `folder-2` is added and opened; its job must
   start within 2 s (from the `Catalog.Open` call to its `scan:state` "scanning"), `folder-1` must be re-queued, and
   both must finish whole (the file checks of step 4 again).
6. Quit: `Shutdown` during the scan of a fresh clone of `folder-3` must return within `QuitDeadline` + 1 s.

   The clones of steps 5 and 6 hold at most 200 files each, whatever the limit: they exercise the queue, not the
   throughput, and a full run would otherwise scan the largest folders twice.
7. No more than `platform.NativeSlots` native calls ever run at once (`platform.NativeHighWater`, and
   `platform.NativeRunning` sampled every 5 ms).
8. After `Shutdown`, every original's manifest equals the recorded one, and no `.choix` or `picks` entry appeared in
   it. (An original that already held them from earlier v1 runs is checked to be unchanged; the test logs how many.)

The report — per folder: files by format, failed, clusters, wall time per stage (from each stage's `starting` to its
`done` progress event), the time after the pipeline (print backfill, preview strip, quality rating), the whole job
(`scanning` to `ready`), files per second, and the process's peak RSS so far — is printed to stdout and written to
`$TMPDIR/choix-uat/report.md`. That is the only file the test writes outside its temporary directories, and it is
written safely: the temporary directory is resolved first, a `choix-uat` that is a symbolic link (or lies inside the
archive) is refused, and the report goes to an exclusively created file that is renamed into place, so a stale
symbolic link at `report.md` is replaced, never followed.

## How to run it

```sh
# Quick: 200 files a folder (minutes).
CHOIX_UAT_ROOT=<archive> go test -tags uat -short -run TestUAT -v ./internal/e2e/

# Full: every file (hours). Pass -- -limit N for N files a folder.
CHOIX_UAT_ROOT=<archive> task uat

# One folder: every file of folder-K only, without the preemption and quit phase.
CHOIX_UAT_ROOT=<archive> task uat -- -folder K
```

`CHOIX_UAT_ROOT` has no default; without it the test skips. The other CPU-heavy work on the machine makes the timings slower; only the 2 s preemption start and the quit
deadline are asserted.

## Results

No full run of the current engine is recorded yet. Record each full run (`task uat`) here before a release:

- the date of the run and the commit tested;
- the scope (every media file of every immediate sub-folder of the corpus root, on APFS clones, or the limit used),
  and any check the run could not exercise (for example the DNG checks, when the archive has no DNG);
- a verdict for each check of steps 4 to 8, with the archive-independent measurements: the preemption start time,
  the `Shutdown` time, the native-call high-water mark against `platform.NativeSlots`, and how many texts were
  withheld.

A failed check is a bug to fix, or a defect of the assertion to correct and re-run; say which.

The per-folder table the test prints (`$TMPDIR/choix-uat/report.md`) is deliberately not recorded in the repository:
its counts describe the archive.

### Unreadable source files

None recorded.
