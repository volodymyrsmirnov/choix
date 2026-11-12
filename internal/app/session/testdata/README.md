# internal/app/session test fixtures

A folder last scanned by Choix v1 (Review Focus 4), written by
`_make_v1_state.go`. Every value is synthetic: camera TESTCAM D1, serial
TESTSERIAL01, capture date 2026-01-02, no GPS, flat-colour 64×48 JPEGs.

| Path | What it is | Where the tests put it |
|---|---|---|
| `v1-state.db` | the folder's `state.db` at `user_version` 5: 6 analyzed photos with exiftool-keyed `raw_exif`, their `thumbs` and `ai_signals` rows, clusters 5 and 6, 2 picks exported under `exports/out/`, no `cluster_params` key | `<folder>/.choix/state.db` |
| `v1-folder/` | the six originals (`DCIM/100TEST/`) and the two exported copies (`exports/out/DCIM/100TEST/`) | `<folder>/` |
| `v1-thumbs/` | v1's thumbnail cache, `<file_id % 256 as %02x>/<id>-thumb.jpg` and `-preview.jpg` | `<folder>/.choix/thumbs/` |

The cache lives in `v1-thumbs/` rather than under a `.choix/` directory
because the repository's `.gitignore` ignores every `.choix/`.

Every file's modification time is 2026-01-02 12:00:00 UTC (`fixedMtime` in the
generator, `sessionTestV1Mtime` in `manager_test.go`; keep them equal). Git
does not store modification times, so the tests set them again after copying.

## Regenerating

The go tool ignores files whose names start with `_`, and the generator also
carries `//go:build ignore`. Run a copy without that line from a temporary
directory inside the module, from the module root:

```sh
tmp=$(mktemp -d ./gen-v1-state.XXXXXX)
grep -v '^//go:build ignore$' internal/app/session/testdata/_make_v1_state.go > "$tmp/main.go"
go run "./$tmp"; rm -r "$tmp"
```

It replaces `v1-state.db`, `v1-folder/` and `v1-thumbs/`, and its output is
byte-for-byte deterministic. It refuses to run if `store.Open` migrates past
`user_version` 5, so a later migration never silently changes this v1 fixture.
