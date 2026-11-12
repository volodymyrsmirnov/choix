#!/bin/sh
# Fetches the pinned official ONNX Runtime library that is bundled as
# Choix.app/Contents/Frameworks/libonnxruntime.dylib. Build time only: the
# app itself never downloads it.
#
#   sh build/onnxruntime/fetch.sh
#
# Result: build/.tools/onnxruntime/lib/libonnxruntime.dylib (git-ignored)
# plus build/.tools/onnxruntime/.sha256, the digest of the archive it came
# from. Nothing is downloaded when that library exists and the stamp equals
# the pinned digest. Otherwise the archive is downloaded, its SHA-256 is
# verified before anything is unpacked (a mismatch deletes it and exits 1),
# and the versioned library from its lib/ directory is installed.
#
# Overrides (for tests): ORT_PIN_FILE (default: pin.env beside this script),
# ORT_URL (wins over the pin file's URL; https:// or file://), ORT_DEST
# (default: build/.tools/onnxruntime).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
pin=${ORT_PIN_FILE:-$here/pin.env}

die() {
	printf 'fetch onnxruntime: %s\n' "$*" >&2
	exit 1
}

[ -f "$pin" ] || die "pin file $pin not found"

# pin_value <key>: the last KEY=value in the pin file. The file is read, not
# sourced, so it cannot run anything.
pin_value() {
	sed -n "s/^$1=//p" "$pin" | tail -n 1
}

version=$(pin_value ORT_VERSION)
sha=$(pin_value ORT_SHA256)
url=${ORT_URL:-$(pin_value ORT_URL)}
[ -n "$version" ] || die "ORT_VERSION missing in $pin"
[ -n "$url" ] || die "ORT_URL missing in $pin"
case $sha in
*[!0-9a-f]* | '') die "ORT_SHA256 in $pin is not a lower-case hex digest" ;;
esac
[ ${#sha} -eq 64 ] || die "ORT_SHA256 in $pin is not 64 hex digits"

dest=${ORT_DEST:-$(cd "$here/.." && pwd)/.tools/onnxruntime}
lib="$dest/lib/libonnxruntime.dylib"
stamp="$dest/.sha256"

if [ -f "$lib" ] && [ -f "$stamp" ] && [ "$(cat "$stamp")" = "$sha" ]; then
	printf 'onnxruntime %s: up to date (%s)\n' "$version" "$lib"
	exit 0
fi

# Whatever was there belongs to another pin or an interrupted run: remove it
# first, so a failed fetch never leaves a stale library to be bundled.
rm -rf "$dest/lib" "$stamp"
mkdir -p "$dest"
staging=$(mktemp -d "$dest/.onnxruntime.XXXXXX")
trap 'rm -rf "$staging"' EXIT
trap 'exit 1' HUP INT TERM

archive="$staging/onnxruntime.tgz"
printf 'onnxruntime %s: downloading %s\n' "$version" "$url"
curl --fail --silent --show-error --location --retry 3 \
	--proto '=https,file' --proto-redir '=https' \
	--output "$archive" "$url" || die "download failed: $url"

got=$(shasum -a 256 "$archive" | awk '{print $1}')
if [ "$got" != "$sha" ]; then
	rm -f "$archive"
	die "SHA-256 mismatch for $url: got $got, want $sha"
fi

member="onnxruntime-osx-arm64-$version/lib/libonnxruntime.$version.dylib"
tar -xzf "$archive" -C "$staging" "$member" || die "$member not found in the archive"
src="$staging/$member"
[ -f "$src" ] && [ ! -L "$src" ] || die "$member is not a regular file"

mkdir -p "$dest/lib"
cp "$src" "$lib.tmp"
mv -f "$lib.tmp" "$lib"
# The stamp goes last: an interrupted run is never mistaken for a complete one.
printf '%s\n' "$sha" >"$stamp.tmp"
mv -f "$stamp.tmp" "$stamp"
printf 'onnxruntime %s: installed %s\n' "$version" "$lib"
