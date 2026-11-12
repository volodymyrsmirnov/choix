#!/bin/sh
# Tests the app bundle definition: bundle.sh, Info.plist.tmpl and the
# sandbox entitlements. Run with `sh build/darwin/bundle_test.sh`; prints
# `ok` on success. Works in a temporary directory (whose name contains a
# space, to exercise quoting) and removes it on exit.
set -eu

# Tool output is compared below: no colour, whatever the caller's CI,
# FORCE_COLOR or CLICOLOR_FORCE (see build/taskfile_test.sh).
NO_COLOR=1
export NO_COLOR
unset FORCE_COLOR CLICOLOR_FORCE

here=$(cd "$(dirname "$0")" && pwd)
bundle="$here/bundle.sh"
bundle_id=com.github.volodymyrsmirnov.choix

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

# expect_eq <what> <got> <want>
expect_eq() {
	[ "$2" = "$3" ] || fail "$1: got '$2', want '$3'"
}

# plist_value <file> <keypath>: the raw value at keypath, or fails the test.
plist_value() {
	plutil -extract "$2" raw -o - "$1" 2>/dev/null || fail "$1: no key $2"
}

# plist_key <file> <key>: the raw value of a top-level key that contains dots
# (plutil reads an unescaped dot as a keypath separator).
plist_key() {
	plist_value "$1" "$(printf '%s' "$2" | sed -e 's/\./\\./g')"
}

# plist_bool_key <file> <key>: like plist_key, but the value must be a real
# boolean (<true/> or <false/>); <string>true</string> fails the test.
plist_bool_key() {
	plutil -extract "$(printf '%s' "$2" | sed -e 's/\./\\./g')" raw -expect bool -o - "$1" 2>/dev/null ||
		fail "$1: $2 is missing or not a boolean"
}

# plist_keys <file>: the top-level keys of a dictionary plist, sorted, one per line.
# Every value in these plists is a scalar without commas, so splitting the
# JSON form on commas is enough.
plist_keys() {
	plutil -convert json -o - "$1" |
		tr -d '{}' | tr ',' '\n' | sed -e 's/^"\([^"]*\)":.*$/\1/' | LC_ALL=C sort
}

for f in "$bundle" "$here/Info.plist.tmpl" "$here/entitlements.plist" \
	"$here/entitlements.mas.plist.tmpl" "$here/../config.yml"; do
	[ -f "$f" ] || fail "missing $f"
done

tmproot=${TMPDIR:-/tmp}
tmp=$(mktemp -d "${tmproot%/}/choix bundle test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

# --- Inputs: a dummy binary and dylib, one carrying an extended attribute.
mkdir "$tmp/in put"
printf '#!/bin/sh\necho choix\n' >"$tmp/in put/choix bin"
chmod 755 "$tmp/in put/choix bin"
printf 'not really a dylib\n' >"$tmp/in put/libonnxruntime.dylib"
xattr -w com.apple.quarantine '0081;00000000;test;' "$tmp/in put/choix bin"

app="$tmp/out dir/Choix.app"
mkdir -p "$app/Contents/Stale"
printf 'stale\n' >"$app/Contents/Stale/file"

sh "$bundle" --binary "$tmp/in put/choix bin" --out "$app" \
	--version 2.0.0 --build 42 --dylib "$tmp/in put/libonnxruntime.dylib" >/dev/null ||
	fail "bundle.sh exited non-zero"

# --- Exactly the five files (the stale one is gone), no symlinks.
got=$(cd "$app" && find . -type f | LC_ALL=C sort)
want=$(printf '%s\n' ./Contents/Frameworks/libonnxruntime.dylib ./Contents/Info.plist \
	./Contents/MacOS/Choix ./Contents/PkgInfo ./Contents/Resources/choix.icns | LC_ALL=C sort)
expect_eq "bundle files" "$got" "$want"
[ -z "$(find "$app" -type l)" ] || fail "bundle contains a symlink"
[ -x "$app/Contents/MacOS/Choix" ] || fail "Contents/MacOS/Choix is not executable"
cmp -s "$app/Contents/MacOS/Choix" "$tmp/in put/choix bin" || fail "binary not copied verbatim"
cmp -s "$app/Contents/Frameworks/libonnxruntime.dylib" "$tmp/in put/libonnxruntime.dylib" ||
	fail "dylib not copied verbatim"
cmp -s "$app/Contents/Resources/choix.icns" "$here/../../assets/icon/choix.icns" ||
	fail "icon is not assets/icon/choix.icns"
expect_eq "PkgInfo" "$(cat "$app/Contents/PkgInfo")" 'APPL????'
expect_eq "PkgInfo size" "$(wc -c <"$app/Contents/PkgInfo" | tr -d ' ')" 8
# com.apple.provenance is kernel-managed: `xattr -c` cannot remove it and
# codesign ignores it, so it is the one attribute allowed to remain.
left=$(xattr -r "$app" | grep -v ': com\.apple\.provenance$' || true)
[ -z "$left" ] || fail "extended attributes left in the bundle: $left"

# --- Every plist lints.
plutil -lint -s "$here/Info.plist.tmpl" "$here/entitlements.plist" \
	"$here/entitlements.mas.plist.tmpl" "$app/Contents/Info.plist" >/dev/null ||
	fail "plutil -lint failed"

# --- Info.plist values.
info="$app/Contents/Info.plist"
expect_eq CFBundleIdentifier "$(plist_value "$info" CFBundleIdentifier)" "$bundle_id"
expect_eq CFBundleName "$(plist_value "$info" CFBundleName)" Choix
expect_eq CFBundleDisplayName "$(plist_value "$info" CFBundleDisplayName)" Choix
expect_eq CFBundleExecutable "$(plist_value "$info" CFBundleExecutable)" Choix
expect_eq CFBundleIconFile "$(plist_value "$info" CFBundleIconFile)" choix
expect_eq CFBundlePackageType "$(plist_value "$info" CFBundlePackageType)" APPL
expect_eq CFBundleShortVersionString "$(plist_value "$info" CFBundleShortVersionString)" 2.0.0
expect_eq CFBundleVersion "$(plist_value "$info" CFBundleVersion)" 42
expect_eq LSMinimumSystemVersion "$(plist_value "$info" LSMinimumSystemVersion)" 14.0
expect_eq LSApplicationCategoryType "$(plist_value "$info" LSApplicationCategoryType)" \
	public.app-category.photography
expect_eq "LSArchitecturePriority count" "$(plutil -extract LSArchitecturePriority raw -o - "$info")" 1
expect_eq "LSArchitecturePriority.0" "$(plist_value "$info" LSArchitecturePriority.0)" arm64
expect_eq NSHighResolutionCapable "$(plist_value "$info" NSHighResolutionCapable)" true
expect_eq ITSAppUsesNonExemptEncryption "$(plist_value "$info" ITSAppUsesNonExemptEncryption)" false
[ -n "$(plist_value "$info" NSHumanReadableCopyright)" ] || fail "NSHumanReadableCopyright is empty"
if grep -q '{{' "$info"; then fail "unrendered placeholder in Info.plist"; fi

# --- The boolean check itself rejects a string "true".
strent="$tmp/string-typed.plist"
printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' \
	'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
	'<plist version="1.0"><dict><key>com.apple.security.app-sandbox</key><string>true</string></dict></plist>' >"$strent"
if (plist_bool_key "$strent" com.apple.security.app-sandbox) >/dev/null 2>&1; then
	fail "plist_bool_key accepted <string>true</string>"
fi

# --- Entitlements: exactly the four sandbox keys, all boolean true.
ent_keys=$(printf '%s\n' com.apple.security.app-sandbox \
	com.apple.security.files.user-selected.read-write \
	com.apple.security.files.bookmarks.app-scope \
	com.apple.security.network.client | LC_ALL=C sort)
ent="$here/entitlements.plist"
expect_eq "entitlements.plist keys" "$(plist_keys "$ent")" "$ent_keys"
for k in $ent_keys; do
	expect_eq "entitlements.plist $k" "$(plist_bool_key "$ent" "$k")" true
done

# --- MAS entitlements: the same four plus the two team identifiers.
team=ABCDE12345
mas="$tmp/entitlements.mas.plist"
sed -e "s/{{TEAM_ID}}/$team/g" "$here/entitlements.mas.plist.tmpl" >"$mas"
plutil -lint -s "$mas" >/dev/null || fail "rendered MAS entitlements do not lint"
mas_keys=$(printf '%s\n%s\n%s\n' "$ent_keys" com.apple.application-identifier \
	com.apple.developer.team-identifier | LC_ALL=C sort)
expect_eq "entitlements.mas keys" "$(plist_keys "$mas")" "$mas_keys"
for k in $ent_keys; do
	expect_eq "entitlements.mas $k" "$(plist_bool_key "$mas" "$k")" true
done
expect_eq com.apple.application-identifier \
	"$(plist_key "$mas" com.apple.application-identifier)" "$team.$bundle_id"
expect_eq com.apple.developer.team-identifier \
	"$(plist_key "$mas" com.apple.developer.team-identifier)" "$team"

# --- No temporary-exception entitlement anywhere.
if grep -rl 'temporary-exception' "$here" --include='*.plist' --include='*.tmpl'; then
	fail "a temporary-exception key is present"
fi

# --- wails3 dev config: the three executes, in order, with their types.
got=$(awk '
	/^[^ #]/ { in_dev = ($0 ~ /^dev_mode:/) }
	in_dev && /^[ ]*-[ ]*cmd:/ { sub(/^[ ]*-[ ]*cmd:[ ]*/, ""); cmd = $0 }
	in_dev && /^[ ]*type:/ { sub(/^[ ]*type:[ ]*/, ""); print cmd " | " $0 }
' "$here/../config.yml")
want='task frontend:bootstrap | blocking
task dev:frontend | background
task dev:run | primary'
expect_eq "config.yml dev_mode.executes" "$got" "$want"

# --- bundle.sh refuses bad input and never removes a non-.app --out.
keep="$tmp/keep me"
mkdir "$keep"
printf 'precious\n' >"$keep/file"
if sh "$bundle" --binary "$tmp/in put/choix bin" --out "$keep" --version 2.0.0 \
	--build 42 --dylib "$tmp/in put/libonnxruntime.dylib" 2>/dev/null; then
	fail "bundle.sh accepted an --out that does not end in .app"
fi
[ -f "$keep/file" ] || fail "bundle.sh removed a non-.app --out"

if sh "$bundle" 2>/dev/null; then fail "bundle.sh accepted no arguments"; fi
if sh "$bundle" --binary "$tmp/in put/choix bin" --out "$tmp/x.app" --version 2.0.0 \
	--build 42 2>/dev/null; then
	fail "bundle.sh accepted a missing --dylib"
fi
if sh "$bundle" --binary "$tmp/nope" --out "$tmp/y.app" --version 2.0.0 --build 42 \
	--dylib "$tmp/in put/libonnxruntime.dylib" 2>/dev/null; then
	fail "bundle.sh accepted a missing binary"
fi
if sh "$bundle" --binary "$tmp/in put/choix bin" --out "$tmp/z.app" --version '2.0</string>' \
	--build 42 --dylib "$tmp/in put/libonnxruntime.dylib" 2>/dev/null; then
	fail "bundle.sh accepted a version that would break the plist"
fi
if sh "$bundle" --binary "$tmp/in put/choix bin" --out "$tmp/w.app" --version 2.0.0 \
	--build 42 --dylib "$tmp/in put/libonnxruntime.dylib" --bogus 1 2>/dev/null; then
	fail "bundle.sh accepted an unknown flag"
fi

echo ok
