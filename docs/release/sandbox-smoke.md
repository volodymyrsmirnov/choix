# Sandboxed app smoke

The release-candidate check of the sandboxed `bin/Choix.app` (spec §11): a script for what can be checked from a
terminal, then a manual checklist in the app. The script is `build/smoke/sandbox-smoke.sh`, run through
`task smoke:sandbox`.

## Running it

```sh
task smoke:sandbox -- --head-only    # steps 1–3: package, static checks, sandboxed self-check
CHOIX_UAT_ROOT=<your photo folder> task smoke:sandbox    # steps 1–7
```

- **Steps 1–3** need no photo folder. `--head-only` stops after them.
- **The source folder** is chosen by rule: the smallest folder (in bytes) under `$CHOIX_UAT_ROOT` with at least 50
  media files, counted recursively, hidden and `picks` folders skipped. There is no default; without `--head-only`,
  an unset or missing `CHOIX_UAT_ROOT` stops the script before anything is built. The script calls the folder
  `folder-1` and prints no folder or file name, count or size from it.
- **Originals are only read.** The folder is cloned with `cp -cR` to `folder-1` in a new directory made with
  `mktemp -d` (`$TMPDIR/choix-smoke.XXXXXX`, below called the smoke directory), and the app only ever sees the clone.
  A manifest (type, size, mtime, path) of the original is taken before and compared after; it stays in the smoke
  directory and is never printed. A manifest that cannot be read completely (an unreadable folder, a failed `stat`,
  fewer than 50 files) stops the script with "could not read the original folder; cannot prove it is unchanged" —
  never "unchanged".
- **No overlap.** Before anything is written, the script resolves (`pwd -P`) the photo folder (whenever
  `CHOIX_UAT_ROOT` is set, with `--head-only` too, where a set but missing folder is also refused), `folder-1`, the
  repository checkout, the temporary directory and the app's container. It refuses to run when the temporary
  directory or the checkout (where `task package` writes `bin/` and its caches) and the photo folder or `folder-1`
  contain one another, when the temporary directory is in the container, or when the photo folder or `folder-1` and
  the container overlap. It removes only what it made with `mktemp -d` (and the clone's inherited `.choix/` inside it),
  never `/`, `$HOME` or anything that overlaps the photo folder.
- **The app's container is only read**: the script polls `library.db` with `sqlite3 -readonly`. On macOS 15 and later
  the first read may make macOS ask whether the terminal may access data from other apps; allow it (or give the
  terminal Full Disk Access), or the script stops.
- **Accessibility** for the terminal lets step 5 add the clone through the open panel by itself (System Events: ⌘O,
  ⇧⌘G, the path, Return, Return). Without it, the script prints those steps, puts the clone's path on the clipboard
  and waits for Enter.
- Quit every running copy of Choix first. Each run uses a new smoke directory; afterwards remove `folder-1` from the
  Library and delete the smoke directories (`$TMPDIR/choix-smoke.*`).
- A static check whose tool (`codesign`, `otool`, `go version -m`, `strings`) fails or prints nothing fails.

What `bin/Choix.app` is: built with `-tags production`, arm64, minimum macOS 14.0, the ONNX Runtime 1.25.0 library in
`Contents/Frameworks`, signed ad hoc by `sign:dev` **without** the hardened runtime (ad hoc plus the hardened runtime's
library validation refuses the bundled library; see `docs/release/signing-checklist.md`), with exactly the four
entitlements of `build/darwin/entitlements.plist`.

### What the script checks

| Step | What | Passes when |
|---|---|---|
| 1 | `task package` | it succeeds |
| 2 | Static checks on `bin/Choix.app` | `codesign -d --entitlements -` lists exactly the four entitlements, each true; `otool -L` on the executable names nothing outside `/usr/lib`, `/System`, `@rpath`, `@executable_path`; `go version -m` shows `-tags=production` without `private_mac_apis` or `server`; `strings` finds none of `setGroupIdentifier:`, `setGroupSpacing:`, `developerExtrasEnabled` (the selectors of Wails' `mac_private_api_darwin.go`) |
| 3 | `CHOIX_SELFCHECK=1` run of the executable (sandboxed, in the app's container) | the report has `"sandboxed":true`, `"onnxRuntime":"1.25.0"`, `"bookmarkRoundTrip":true`, `"trashCheck":true`, and neither provider is an `error` (`skipped: model missing` is fine) |
| 4 | Manifest of the original, `cp -cR` clone | the clone exists |
| 5 | Launch, add the clone through the open panel | `folder-1` appears in `library.db` |
| 6 | Wait for the scan | `job_pending` is 0; photos + videos > 0 and clusters > 0; no job error; `<clone>/.choix/state.db` exists; the original's manifest is unchanged; `pgrep -P` never found a child of Choix |
| 7 | Manual checks | below; afterwards still no child process, the manifest unchanged and `~/.choix` neither created nor changed |

Wails' `open` helper is linked into the binary but never called (Task 44's test bans `app.Env` and `app.Browser`).
Whether a child process is ever started is checked at run time: from the launch until you press Enter at step 7, every
Choix process is sampled once a second and must never have a child.

## Checks

Do them in the app while the script waits at step 7. Use `folder-1` (the clone) unless a line says otherwise; never
an original folder.

1. **Launch** — Choix opens one window with the Library; no alert, no crash report.
2. **Add a clone of a corpus folder** — `folder-1` appears as one row in the Library, queued or scanning.
3. **Scan completes with thumbnails** — the row's progress ends, it shows its photo and video counts, and opening it
   shows a grid of thumbnails (no grey placeholders left once the row is idle).
4. **Pick a photo and find the copy in `picks/`** — after picking one photo, Finder shows the copy in
   `folder-1/picks/`; the original file is still in its place.
5. **Open Picks opens Finder there** — Finder comes to the front showing the contents of `folder-1/picks/`.
6. **Reveal selects the folder in Finder** — Finder comes to the front with `folder-1` selected in its parent folder.
7. **The ⋯ button and right-click both open the native row menu** — both show the same native macOS menu with the
   same items, at the row.
8. **⌘O opens the picker exactly once** — one open panel appears (not two, not one per window); Cancel closes it.
9. **Delete the test folder in Finder and watch the row leave with a toast** — make a throwaway copy first, in the
   smoke directory (`cp -cR folder-1 folder-2` there), add `folder-2`, then move it to the Trash in Finder: its row
   leaves the Library and a toast says so; `folder-1` stays.
10. **Eject a test volume mid-scan; the row goes offline, then resumes when it is back** — make a disk image in the
    smoke directory (`hdiutil create -size 2g -fs APFS -volname ChoixSmoke volume.dmg` there), mount it, copy
    `folder-1` onto it with `ditto`, add that copy and eject the volume in Finder while it scans (Force Eject if asked):
    the row turns offline and stays in the Library; mount the image again and the row turns present and the scan
    continues to the end.
11. **Relaunch: the Library and folder access are restored** — quit and start Choix again: every row is back, opens
    and shows its thumbnails; no row says Choix lost access to the folder.
12. **Quit during a scan returns within a few seconds** — start a rescan of `folder-1` and quit with ⌘Q while it runs:
    the process is gone within about 5 seconds (`pgrep -x Choix` prints nothing).
13. **Drop a folder on the window (if the drop grant works)** — spike S1's drop grant has not been run yet; until it
    has, mark this line n/a. Otherwise drop `folder-2` (or another clone) on the window: it is added like one chosen in
    the open panel, and opens.
14. **Play and seek a video** — open a video in `folder-1` (or any clone with one): it plays with sound and seeking
    to another position shows the frame there.
15. **No `deny` lines from `sandboxd` for Choix** — `log show --last 10m --predicate 'process == "sandboxd"'` prints
    no line that mentions both `deny` and `Choix`. (Run it within 10 minutes of the checks above.)
16. **`~/.choix` was not created** — the script checks it at the end of step 7; it says `not created: ok`, or, when
    the folder existed before the run, `unchanged: ok`.

## Result record

One row per run: the date, the build (`git describe` of the commit packaged), the macOS version (`sw_vers
-productVersion`), and pass, fail or n/a for each check above. Do not write folder or file names here.

| Date | Build | macOS | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 | 16 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| | | | | | | | | | | | | | | | | | | |
