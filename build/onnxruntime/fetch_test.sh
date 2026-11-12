#!/bin/sh
# Tests fetch.sh without the network: it is pointed (ORT_PIN_FILE, ORT_URL,
# ORT_DEST) at a synthetic archive behind a file:// URL, laid out like the
# official one, and an output directory whose name contains a space. Run with
# `sh build/onnxruntime/fetch_test.sh`; prints `ok` on success.
set -eu

# Tool output is compared below: no colour, whatever the caller's CI,
# FORCE_COLOR or CLICOLOR_FORCE (see build/taskfile_test.sh).
NO_COLOR=1
export NO_COLOR
unset FORCE_COLOR CLICOLOR_FORCE

here=$(cd "$(dirname "$0")" && pwd)
fetch="$here/fetch.sh"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

# expect_eq <what> <got> <want>
expect_eq() {
	[ "$2" = "$3" ] || fail "$1: got '$2', want '$3'"
}

for f in "$fetch" "$here/pin.env"; do
	[ -f "$f" ] || fail "missing $f"
done

# --- The committed pin: the official 1.25.0 arm64 archive and its digest.
pin_value() {
	sed -n "s/^$1=//p" "$here/pin.env"
}
expect_eq "pinned version" "$(pin_value ORT_VERSION)" 1.25.0
expect_eq "pinned url" "$(pin_value ORT_URL)" \
	https://github.com/microsoft/onnxruntime/releases/download/v1.25.0/onnxruntime-osx-arm64-1.25.0.tgz
expect_eq "pinned sha256" "$(pin_value ORT_SHA256)" \
	65405dc8793c86cadb98b5e07f6d3bdde84f8300f1b030d4736b41c17610d6c1

tmproot=${TMPDIR:-/tmp}
tmp=$(mktemp -d "${tmproot%/}/choix-ort-fetch-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

# --- A synthetic archive: the versioned library plus a different unversioned
# copy, so the test sees which one is installed.
v=9.9.9
top="onnxruntime-osx-arm64-$v"
mkdir -p "$tmp/src/$top/lib"
printf 'versioned library %s\n' "$v" >"$tmp/src/$top/lib/libonnxruntime.$v.dylib"
printf 'unversioned copy\n' >"$tmp/src/$top/lib/libonnxruntime.dylib"
printf 'licence\n' >"$tmp/src/$top/LICENSE"
tar -czf "$tmp/ort.tgz" -C "$tmp/src" "$top"
sum=$(shasum -a 256 "$tmp/ort.tgz" | awk '{print $1}')
url="file://$tmp/ort.tgz"

# write_pin <file> <sha256>: a pin whose own URL must never be used (the
# environment's ORT_URL overrides it).
write_pin() {
	printf 'ORT_VERSION=%s\nORT_URL=https://invalid.invalid/never-fetched.tgz\nORT_SHA256=%s\n' "$v" "$2" >"$1"
}
write_pin "$tmp/pin-wrong.env" 0000000000000000000000000000000000000000000000000000000000000000
write_pin "$tmp/pin-right.env" "$sum"

dest="$tmp/out dir/onnxruntime"
lib="$dest/lib/libonnxruntime.dylib"

# run_fetch <pin file>: runs fetch.sh offline against the synthetic archive.
run_fetch() {
	ORT_PIN_FILE="$1" ORT_URL="$url" ORT_DEST="$dest" sh "$fetch"
}

# --- Wrong digest: exit 1, no lib/, no archive or staging directory left.
if run_fetch "$tmp/pin-wrong.env" >/dev/null 2>"$tmp/err"; then
	fail "fetch.sh accepted an archive with the wrong digest"
fi
grep -q 'SHA-256 mismatch' "$tmp/err" || fail "no mismatch message: $(cat "$tmp/err")"
[ ! -e "$dest/lib" ] || fail "lib/ exists after a digest mismatch"
[ -z "$(find "$tmp/out dir" -name '*.tgz' -o -name '.onnxruntime.*')" ] ||
	fail "the rejected archive or a staging directory was left behind"

# --- Right digest: the versioned library is installed as libonnxruntime.dylib.
run_fetch "$tmp/pin-right.env" >/dev/null || fail "fetch.sh failed with the right digest"
[ -f "$lib" ] && [ ! -L "$lib" ] || fail "$lib is not a regular file"
cmp -s "$lib" "$tmp/src/$top/lib/libonnxruntime.$v.dylib" || fail "installed library is not the versioned one"
expect_eq "stamp" "$(cat "$dest/.sha256")" "$sum"
expect_eq "lib/ contents" "$(ls "$dest/lib")" libonnxruntime.dylib
[ -z "$(find "$tmp/out dir" -name '*.tgz' -o -name '.onnxruntime.*')" ] ||
	fail "the archive or a staging directory was left behind"

# --- Second run: up to date, downloads nothing (the archive is gone).
mv "$tmp/ort.tgz" "$tmp/ort.tgz.away"
out=$(run_fetch "$tmp/pin-right.env") || fail "second run failed: it tried to download again"
case $out in
*'up to date'*) ;;
*) fail "second run did not report up to date: $out" ;;
esac
cmp -s "$lib" "$tmp/src/$top/lib/libonnxruntime.$v.dylib" || fail "second run changed the library"
mv "$tmp/ort.tgz.away" "$tmp/ort.tgz"

# --- A pin change with a bad download leaves no stale library behind.
if run_fetch "$tmp/pin-wrong.env" >/dev/null 2>&1; then
	fail "fetch.sh accepted the wrong digest over an installed library"
fi
[ ! -e "$dest/lib" ] || fail "a stale lib/ survived a failed re-fetch"
[ ! -e "$dest/.sha256" ] || fail "a stale stamp survived a failed re-fetch"

# --- An unreachable URL fails cleanly.
if ORT_PIN_FILE="$tmp/pin-right.env" ORT_URL="file://$tmp/absent.tgz" ORT_DEST="$dest" \
	sh "$fetch" >/dev/null 2>&1; then
	fail "fetch.sh succeeded without an archive"
fi
[ ! -e "$dest/lib" ] || fail "lib/ exists after a failed download"

echo ok
