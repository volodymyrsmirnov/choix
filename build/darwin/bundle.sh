#!/bin/sh
# Assembles Choix.app from a built binary and the ONNX Runtime library.
# It does not sign: the Taskfile's sign:* tasks do that afterwards.
#
#   sh build/darwin/bundle.sh --binary <path> --out <Choix.app> \
#       --version <v> --build <n> --dylib <libonnxruntime.dylib>
#
# <out> is removed first (it must end in .app), then created with:
#   Contents/MacOS/Choix
#   Contents/Frameworks/libonnxruntime.dylib
#   Contents/Resources/choix.icns   (from assets/icon/choix.icns)
#   Contents/Info.plist             (Info.plist.tmpl with VERSION and BUILD)
#   Contents/PkgInfo                (APPL????)
# and its extended attributes are cleared.
set -eu

prog=bundle.sh
here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
template="$here/Info.plist.tmpl"
icon="$here/../../assets/icon/choix.icns"

die() {
	printf '%s: %s\n' "$prog" "$*" >&2
	exit 1
}

usage() {
	die "usage: $prog --binary <path> --out <Choix.app> --version <v> --build <n> --dylib <path>"
}

binary='' out='' version='' build='' dylib=''
while [ $# -gt 0 ]; do
	case "$1" in
	--binary | --out | --version | --build | --dylib)
		[ $# -ge 2 ] || die "$1 needs a value"
		case "$1" in
		--binary) binary=$2 ;;
		--out) out=$2 ;;
		--version) version=$2 ;;
		--build) build=$2 ;;
		--dylib) dylib=$2 ;;
		esac
		shift 2
		;;
	-h | --help) usage ;;
	*) die "unknown argument: $1" ;;
	esac
done

[ -n "$binary" ] || die "missing --binary"
[ -n "$out" ] || die "missing --out"
[ -n "$version" ] || die "missing --version"
[ -n "$build" ] || die "missing --build"
[ -n "$dylib" ] || die "missing --dylib"

# The values are substituted into XML with sed, so only plain version
# characters are accepted.
case "$version" in
*[!A-Za-z0-9._+-]*) die "--version '$version' may only contain letters, digits and . _ + -" ;;
esac
case "$build" in
.* | *[!0-9.]*) die "--build '$build' must be digits separated by dots" ;;
esac

# Refuse to remove anything that is not an app bundle path.
while :; do
	case "$out" in
	?*/) out=${out%/} ;;
	*) break ;;
	esac
done
case "${out##*/}" in
?*.app) ;;
*) die "--out '$out' must be a <name>.app path (refusing to remove it)" ;;
esac

[ -f "$binary" ] || die "binary not found: $binary"
[ -f "$dylib" ] || die "dylib not found: $dylib"
[ -f "$icon" ] || die "icon not found: $icon"
[ -f "$template" ] || die "Info.plist template not found: $template"

rm -rf -- "$out"
mkdir -p -- "$out/Contents/MacOS" "$out/Contents/Frameworks" "$out/Contents/Resources"

cp -- "$binary" "$out/Contents/MacOS/Choix"
chmod 755 "$out/Contents/MacOS/Choix"
cp -- "$dylib" "$out/Contents/Frameworks/libonnxruntime.dylib"
cp -- "$icon" "$out/Contents/Resources/choix.icns"

plist="$out/Contents/Info.plist"
sed -e "s/{{VERSION}}/$version/g" -e "s/{{BUILD}}/$build/g" "$template" >"$plist"
if grep -q '{{' "$plist"; then
	die "unrendered placeholder left in $plist"
fi
plutil -lint -s "$plist" >/dev/null || die "generated Info.plist does not lint: $plist"

printf 'APPL????' >"$out/Contents/PkgInfo"

xattr -cr "$out"

printf '%s: wrote %s (version %s, build %s)\n' "$prog" "$out" "$version" "$build"
