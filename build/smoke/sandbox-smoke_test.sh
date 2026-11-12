#!/bin/sh
# Tests sandbox-smoke.sh without building, launching or signing anything and
# without reading a real photo folder or the app's container. Run with
# `sh build/smoke/sandbox-smoke_test.sh`; prints `ok` on success.
#
#  1. The script parses (`sh -n`) and rejects an unknown argument.
#  2. Without --head-only, an unset, empty or missing CHOIX_UAT_ROOT, or one
#     with no folder of at least 50 media files, exits 1 with one line
#     before anything is built (a fake `task` on PATH records every call),
#     and the output names no folder.
#  3. --head-only needs no CHOIX_UAT_ROOT and starts with `task package`; a
#     failed package stops it, with $HOME in the log shown as "~".
#  4. The checks, called one by one (CHOIX_SMOKE_LIB=1 loads the functions
#     without running anything): the folder rule, the entitlements, the
#     linked libraries, the build tags, the private-API selectors, the
#     self-check report, the manifest, the manual-check titles printed at
#     step 7 (from docs/release/sandbox-smoke.md) and the $HOME redaction.
#
# Works in a temporary directory whose name contains a space, with HOME and
# TMPDIR pointed inside it, and removes it on exit.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
script="$here/sandbox-smoke.sh"
entitlements="$here/../darwin/entitlements.plist"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

# expect_eq <what> <got> <want>
expect_eq() {
	[ "$2" = "$3" ] || fail "$1: got '$2', want '$3'"
}

# expect_contains <what> <text> <fragment>
expect_contains() {
	case $2 in
	*"$3"*) ;;
	*) fail "$1: '$3' not in:
$2" ;;
	esac
}

# expect_lacks <what> <text> <fragment>
expect_lacks() {
	case $2 in
	*"$3"*) fail "$1: '$3' should not be in:
$2" ;;
	esac
}

[ -f "$script" ] || fail "missing $script"
[ -f "$entitlements" ] || fail "missing $entitlements"

tmproot=${TMPDIR:-/tmp}
tmp=$(mktemp -d "${tmproot%/}/choix smoke test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

# A fake `task` first on PATH: it records its arguments and fails, printing
# a line with $HOME in it (the script must redact it).
mkdir "$tmp/bin" "$tmp/home" "$tmp/tmp"
calls="$tmp/task-calls"
cat >"$tmp/bin/task" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"$calls"
echo "building in \$HOME/secret-build-dir"
exit 1
EOF
chmod 755 "$tmp/bin/task"

# run_smoke <args…>: runs the script with the fake task, HOME in the test
# directory, TMPDIR=$smoke_tmp and CHOIX_UAT_ROOT=$uat ("-" for unset);
# sets out (stdout and stderr) and code.
uat=- smoke_tmp="$tmp/tmp" smoke_script=$script
run_smoke() {
	: >"$calls"
	set +e
	if [ "$uat" = - ]; then
		out=$(env -u CHOIX_UAT_ROOT PATH="$tmp/bin:$PATH" HOME="$tmp/home" TMPDIR="$smoke_tmp" \
			sh "$smoke_script" "$@" 2>&1)
	else
		out=$(env CHOIX_UAT_ROOT="$uat" PATH="$tmp/bin:$PATH" HOME="$tmp/home" TMPDIR="$smoke_tmp" \
			sh "$smoke_script" "$@" 2>&1)
	fi
	code=$?
	set -e
}

# listing <dir>: every path below dir, sorted (to show nothing was written).
listing() {
	find "$1" | LC_ALL=C sort
}

# media <dir> <n> <bytes>: n JPEG-named files of the given size in dir.
media() {
	mkdir -p "$1"
	i=0
	while [ "$i" -lt "$2" ]; do
		head -c "$3" /dev/zero >"$1/IMG_$i.JPG"
		i=$((i + 1))
	done
}

# --- 1. Syntax and arguments.
sh -n "$script" || fail "sh -n failed"
run_smoke --bogus
expect_eq "unknown argument: exit" "$code" 2
[ ! -s "$calls" ] || fail "unknown argument: task was run"

# --- 2. CHOIX_UAT_ROOT, before anything is built.
run_smoke
expect_eq "unset root: exit" "$code" 1
expect_eq "unset root: lines" "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" 1
expect_contains "unset root" "$out" "CHOIX_UAT_ROOT is not set"
[ ! -s "$calls" ] || fail "unset root: task was run"

uat=''
run_smoke
expect_eq "empty root: exit" "$code" 1
expect_contains "empty root" "$out" "CHOIX_UAT_ROOT is not set"
[ ! -s "$calls" ] || fail "empty root: task was run"

uat="$tmp/no such folder"
run_smoke
expect_eq "missing root: exit" "$code" 1
expect_eq "missing root: lines" "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" 1
expect_contains "missing root" "$out" "CHOIX_UAT_ROOT is not an existing folder"
expect_lacks "missing root" "$out" "no such folder"
[ ! -s "$calls" ] || fail "missing root: task was run"

few="$tmp/corpus few"
media "$few/Holiday Alpha" 49 10
uat=$few
run_smoke
expect_eq "no candidate: exit" "$code" 1
expect_contains "no candidate" "$out" "no folder under CHOIX_UAT_ROOT has at least 50 media files"
expect_lacks "no candidate" "$out" "Holiday"
[ ! -s "$calls" ] || fail "no candidate: task was run"

# --- 2b. A temporary directory that overlaps the photo folder or lies in
# the container is refused before anything is written or built: one line,
# no path, no task call, nothing new in the photo folder.
ovl="$tmp/ovl corpus"
media "$ovl/Trip Alpha" 50 10
mkdir -p "$ovl/scratch" "$ovl/Trip Alpha/scratch"
ln -s "$ovl/scratch" "$tmp/link to scratch"
mkdir -p "$tmp/outer tmp/photos"
media "$tmp/outer tmp/photos/Trip Beta" 50 10
before=$(listing "$ovl")
# expect_refused <what> <message>: the last run_smoke was refused that way.
expect_refused() {
	expect_eq "$1: exit" "$code" 1
	expect_eq "$1: lines" "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" 1
	expect_contains "$1" "$out" "$2"
	expect_lacks "$1" "$out" "$tmp"
	expect_lacks "$1" "$out" Trip
	[ ! -s "$calls" ] || fail "$1: task was run"
	expect_eq "$1: photo folder untouched" "$(listing "$ovl")" "$before"
}
overlap='the work directory overlaps the photo folder'
uat=$ovl smoke_tmp="$ovl/scratch"
run_smoke
expect_refused "TMPDIR inside the photo folder" "$overlap"
uat=$ovl smoke_tmp="$ovl/Trip Alpha/scratch"
run_smoke
expect_refused "TMPDIR inside the source folder" "$overlap"
uat=$ovl smoke_tmp="$tmp/link to scratch"
run_smoke
expect_refused "TMPDIR a symlink into the photo folder" "$overlap"
if [ -d "$tmp/OVL CORPUS/scratch" ]; then # a case-insensitive volume
	uat=$ovl smoke_tmp="$tmp/OVL CORPUS/scratch"
	run_smoke
	expect_refused "TMPDIR inside the photo folder, other case" "$overlap"
fi
uat="$tmp/outer tmp/photos" smoke_tmp="$tmp/outer tmp"
run_smoke
expect_refused "photo folder inside TMPDIR" "$overlap"
uat="$ovl/scratch/.." smoke_tmp="$ovl"
run_smoke
expect_refused "TMPDIR is the photo folder" "$overlap"
in_container="$tmp/home/Library/Containers/com.github.volodymyrsmirnov.choix/Data/tmp"
mkdir -p "$in_container"
uat=- smoke_tmp=$in_container
run_smoke --head-only
expect_refused "TMPDIR inside the container (head only)" "the work directory is inside Choix's container"
uat=$ovl
run_smoke
expect_refused "TMPDIR inside the container" "the work directory is inside Choix's container"

# --head-only protects a supplied CHOIX_UAT_ROOT the same way.
uat=$ovl smoke_tmp="$ovl/scratch"
run_smoke --head-only
expect_refused "head only: TMPDIR inside the photo folder" "$overlap"
uat=$ovl smoke_tmp="$ovl/Trip Alpha/scratch"
run_smoke --head-only
expect_refused "head only: TMPDIR inside the source folder" "$overlap"
uat=$ovl smoke_tmp="$tmp/link to scratch"
run_smoke --head-only
expect_refused "head only: TMPDIR a symlink into the photo folder" "$overlap"
uat="$tmp/outer tmp/photos" smoke_tmp="$tmp/outer tmp"
run_smoke --head-only
expect_refused "head only: photo folder inside TMPDIR" "$overlap"
uat="$tmp/no such folder" smoke_tmp="$tmp/tmp"
run_smoke --head-only
expect_refused "head only: CHOIX_UAT_ROOT set but missing" "CHOIX_UAT_ROOT is not an existing folder"
# The photo folder and the container overlap.
photos_in_container="$tmp/home/Library/Containers/com.github.volodymyrsmirnov.choix/Data/photos"
media "$photos_in_container/Trip Gamma" 50 10
uat=$photos_in_container smoke_tmp="$tmp/tmp"
run_smoke --head-only
expect_refused "head only: photo folder inside the container" "the photo folder overlaps Choix's container"
run_smoke
expect_refused "photo folder inside the container" "the photo folder overlaps Choix's container"
uat="$tmp/home" smoke_tmp="$tmp/tmp"
run_smoke --head-only
expect_refused "head only: container inside the photo folder" "the photo folder overlaps Choix's container"
# The checkout (where task package writes) inside the photo folder: a copy
# of the script in a fake checkout there.
mkdir -p "$ovl/checkout/build/smoke"
cp "$script" "$ovl/checkout/build/smoke/sandbox-smoke.sh"
before=$(listing "$ovl")
uat=$ovl smoke_tmp="$tmp/tmp" smoke_script="$ovl/checkout/build/smoke/sandbox-smoke.sh"
run_smoke --head-only
expect_refused "head only: checkout inside the photo folder" "the repository checkout overlaps the photo folder"
run_smoke
expect_refused "checkout inside the photo folder" "the repository checkout overlaps the photo folder"
smoke_script=$script

# --head-only with a supplied, separate photo folder, and without one,
# proceeds to task package (the stub fails it).
uat=$ovl smoke_tmp="$tmp/tmp"
run_smoke --head-only
expect_eq "head only, separate photo folder: exit" "$code" 1
expect_eq "head only, separate photo folder: task calls" "$(cat "$calls")" "-d $(cd "$here/../.." && pwd) package"
expect_lacks "head only, separate photo folder" "$out" Trip
expect_eq "head only, separate photo folder: untouched" "$(listing "$ovl")" "$before"
uat=-
run_smoke --head-only
expect_eq "head only, no photo folder: task calls" "$(cat "$calls")" "-d $(cd "$here/../.." && pwd) package"
uat=- smoke_tmp="$tmp/tmp"

# --- 3. --head-only: no folder needed; task package runs first.
run_smoke --head-only
expect_eq "head-only, failed package: exit" "$code" 1
expect_eq "head-only: task calls" "$(cat "$calls")" "-d $(cd "$here/../.." && pwd) package"
expect_contains "head-only" "$out" "task package failed"
expect_contains "head-only: redacted log" "$out" "building in ~/secret-build-dir"
expect_lacks "head-only: redacted log" "$out" "$tmp/home"
expect_eq "head-only: work directory removed" "$(listing "$tmp/tmp")" "$tmp/tmp"

# --- 4. The checks, one by one.
# shellcheck source=build/smoke/sandbox-smoke.sh
CHOIX_SMOKE_LIB=1 . "$script"

# check <what> <want: pass|fail> <command…>: runs the check in a subshell.
check() {
	what=$1 want=$2
	shift 2
	set +e
	msg=$( ("$@") 2>&1)
	got=$?
	set -e
	case $want in
	pass) [ "$got" = 0 ] || fail "$what: want pass, failed with: $msg" ;;
	fail) [ "$got" != 0 ] || fail "$what: want fail, passed" ;;
	esac
}

# The folder rule: the smallest (in bytes) folder with at least 50 media
# files, counted recursively; hidden folders and picks are skipped, other
# files do not count.
corpus="$tmp/corpus"
media "$corpus/Big Trip" 60 9000
media "$corpus/Small Trip" 50 5000
media "$corpus/Too Few" 49 1
media "$corpus/.hidden" 80 1
media "$corpus/Mixed/picks" 50 1
media "$corpus/Mixed" 10 1
mkdir -p "$corpus/Docs"
i=0
while [ "$i" -lt 60 ]; do
	printf x >"$corpus/Docs/note-$i.txt"
	i=$((i + 1))
done
expect_eq "choose_folder" "$(choose_folder "$corpus")" "$corpus/Small Trip"
media "$corpus/Nested/Inner" 50 1
media "$corpus/Nested" 1 50000
expect_eq "choose_folder, nested" "$(choose_folder "$corpus")" "$corpus/Nested/Inner"
check "choose_folder, none" fail choose_folder "$few"
expect_eq "media_count skips hidden and picks" "$(media_count "$corpus/Mixed")" 10

# Entitlements: exactly the four, all true.
check "entitlements.plist" pass check_entitlements "$entitlements"
sed -e 's#<key>com.apple.security.network.client</key>#<key>com.apple.security.network.server</key><true/>&#' \
	"$entitlements" >"$tmp/five.plist"
check "five entitlements" fail check_entitlements "$tmp/five.plist"
sed -e '/network.client/{N;d;}' "$entitlements" >"$tmp/three.plist"
check "three entitlements" fail check_entitlements "$tmp/three.plist"
awk '/app-sandbox/ { print; getline; print "\t<false/>"; next } { print }' \
	"$entitlements" >"$tmp/false.plist"
check "a false entitlement" fail check_entitlements "$tmp/false.plist"
: >"$tmp/empty.plist"
check "no entitlements" fail check_entitlements "$tmp/empty.plist"

# Linked libraries.
cat >"$tmp/otool-good" <<'EOF'
/x/bin/Choix.app/Contents/MacOS/Choix:
	/usr/lib/libSystem.B.dylib (compatibility version 1.0.0, current version 1351.0.0)
	/System/Library/Frameworks/WebKit.framework/Versions/A/WebKit (compatibility version 1.0.0, current version 621.1.0)
	@rpath/libfoo.dylib (compatibility version 1.0.0, current version 1.0.0)
	@executable_path/../Frameworks/libbar.dylib (compatibility version 1.0.0, current version 1.0.0)
EOF
check "otool: allowed paths" pass check_linked_libs "$tmp/otool-good"
{
	cat "$tmp/otool-good"
	printf '\t/opt/homebrew/lib/libvips.dylib (compatibility version 1.0.0, current version 1.0.0)\n'
} >"$tmp/otool-bad"
check "otool: homebrew path" fail check_linked_libs "$tmp/otool-bad"
printf '/x/Choix:\n\t/usr/libexec/x.dylib (compatibility version 1.0.0)\n' >"$tmp/otool-bad2"
check "otool: /usr/libexec" fail check_linked_libs "$tmp/otool-bad2"
printf '/x/Choix:\n' >"$tmp/otool-none"
check "otool: no libraries" fail check_linked_libs "$tmp/otool-none"

# Build tags.
gover() {
	printf '/x/Choix: go1.26.5\n\tpath\tgithub.com/volodymyrsmirnov/choix/cmd/choix\n'
	[ -z "$1" ] || printf '\tbuild\t-tags=%s\n' "$1"
	printf '\tbuild\t-trimpath=true\n\tbuild\tCGO_ENABLED=1\n'
}
gover production >"$tmp/gover"
check "tags: production" pass check_build_tags "$tmp/gover"
gover production,server >"$tmp/gover"
check "tags: production,server" fail check_build_tags "$tmp/gover"
gover private_mac_apis,production >"$tmp/gover"
check "tags: private_mac_apis" fail check_build_tags "$tmp/gover"
gover '' >"$tmp/gover"
check "tags: none" fail check_build_tags "$tmp/gover"
gover productionx >"$tmp/gover"
check "tags: productionx" fail check_build_tags "$tmp/gover"

check "tags: no go version output" fail check_build_tags "$tmp/no such file"
check "otool: no otool output" fail check_linked_libs "$tmp/no such file"

# run_tool: a failing tool or an empty output fails; output is kept.
mkdir "$tmp/tool"
check "run_tool: failing tool" fail run_tool "$tmp/tool/out" "$tmp/tool/err" false
check "run_tool: empty output" fail run_tool "$tmp/tool/out" "$tmp/tool/err" true
check "run_tool: output" pass run_tool "$tmp/tool/out" "$tmp/tool/err" echo x
expect_eq "run_tool: kept output" "$(cat "$tmp/tool/out")" x

# Private-API selectors: strings must run and print something.
printf 'abc\0initWithFrame:\0setTitle:\0' >"$tmp/clean-bin"
check "selectors: clean" pass check_private_selectors "$tmp/clean-bin" "$tmp/tool"
for sel in setGroupIdentifier: setGroupSpacing: developerExtrasEnabled; do
	printf 'abc\0%s\0xyz' "$sel" >"$tmp/dirty-bin"
	check "selector $sel" fail check_private_selectors "$tmp/dirty-bin" "$tmp/tool"
done
check "selectors: no such executable" fail check_private_selectors "$tmp/no such file" "$tmp/tool"
mkdir "$tmp/stub-fail" "$tmp/stub-empty"
printf '#!/bin/sh\necho "strings: boom" >&2\nexit 1\n' >"$tmp/stub-fail/strings"
printf '#!/bin/sh\nexit 0\n' >"$tmp/stub-empty/strings"
chmod 755 "$tmp/stub-fail/strings" "$tmp/stub-empty/strings"
check "selectors: strings fails" fail eval \
	'PATH="$tmp/stub-fail:$PATH"; check_private_selectors "$tmp/clean-bin" "$tmp/tool"'
expect_contains "selectors: strings fails" "$msg" "strings failed"
check "selectors: strings prints nothing" fail eval \
	'PATH="$tmp/stub-empty:$PATH"; check_private_selectors "$tmp/clean-bin" "$tmp/tool"'
expect_contains "selectors: strings prints nothing" "$msg" "strings printed nothing"

# The self-check report.
report() {
	printf '{"version":"v","dataDir":"%s/Library/Containers/x/Data","dataDirWritable":true,"sandboxed":%s,"onnxRuntime":"%s","providers":{"clip":"%s","quality":"%s"},"bookmarkRoundTrip":%s,"trashCheck":%s}\n' \
		"$HOME" "$1" "$2" "$3" "$4" "$5" "$6"
}
miss='skipped: model missing'
report true 1.25.0 "$miss" "$miss" true true >"$tmp/sc.json"
check "self-check: pass" pass check_selfcheck "$tmp/sc.json" 1.25.0
report true 1.25.0 coreml cpu true true >"$tmp/sc.json"
check "self-check: providers ran" pass check_selfcheck "$tmp/sc.json" 1.25.0
report false 1.25.0 "$miss" "$miss" true true >"$tmp/sc.json"
check "self-check: not sandboxed" fail check_selfcheck "$tmp/sc.json" 1.25.0
report true 1.24.0 "$miss" "$miss" true true >"$tmp/sc.json"
check "self-check: other runtime" fail check_selfcheck "$tmp/sc.json" 1.25.0
report true 1.25.0 "error: boom" "$miss" true true >"$tmp/sc.json"
check "self-check: provider error" fail check_selfcheck "$tmp/sc.json" 1.25.0
report true 1.25.0 "$miss" "" true true >"$tmp/sc.json"
check "self-check: empty provider" fail check_selfcheck "$tmp/sc.json" 1.25.0
report true 1.25.0 "$miss" "$miss" "\"error: resolve: $HOME/x\"" true >"$tmp/sc.json"
check "self-check: bookmark error" fail check_selfcheck "$tmp/sc.json" 1.25.0
expect_contains "self-check: failure names the key" "$msg" bookmarkRoundTrip
expect_lacks "self-check: failure redacts HOME" "$msg" "$HOME"
report true 1.25.0 "$miss" "$miss" true '"error: x"' >"$tmp/sc.json"
check "self-check: trash error" fail check_selfcheck "$tmp/sc.json" 1.25.0
: >"$tmp/sc.json"
check "self-check: no report" fail check_selfcheck "$tmp/sc.json" 1.25.0

# The manifest: equal for an untouched folder, different after a change;
# never "unchanged" when it cannot be read completely.
src="$tmp/orig folder"
media "$src/a" 3 10
mkdir "$tmp/mf"
manifest "$src" "$tmp/mf/m1" "$tmp/mf/err" 3 || fail "manifest: failed on a readable folder"
manifest "$src" "$tmp/mf/m2" "$tmp/mf/err" 3 || fail "manifest: failed on a readable folder"
cmp -s "$tmp/mf/m1" "$tmp/mf/m2" || fail "manifest: differs for an untouched folder"
expect_eq "manifest entries (2 dirs, 3 files)" "$(wc -l <"$tmp/mf/m1" | tr -d ' ')" 5
check "manifest: fewer files than the minimum" fail manifest "$src" "$tmp/mf/m4" "$tmp/mf/err" 4
check "check_unchanged: unchanged" pass eval \
	'min_media=3; check_unchanged "$src" "$tmp/mf/m1" "$tmp/mf/m3" "$tmp/mf/err"'
expect_eq "check_unchanged: silent when unchanged" "$msg" ""
printf 'y' >>"$src/a/IMG_1.JPG"
check "check_unchanged: a grown file" fail eval \
	'min_media=3; check_unchanged "$src" "$tmp/mf/m1" "$tmp/mf/m3" "$tmp/mf/err"'
expect_contains "check_unchanged: a grown file" "$msg" "the original of folder-1 changed"

# expect_unreadable <what>: the last check failed with only the "cannot
# prove" line: no path, no "unchanged: ok".
expect_unreadable() {
	expect_eq "$1: lines" "$(printf '%s\n' "$msg" | wc -l | tr -d ' ')" 1
	expect_contains "$1" "$msg" "could not read the original folder; cannot prove it is unchanged"
	expect_lacks "$1" "$msg" "unchanged: ok"
	expect_lacks "$1" "$msg" "$src"
	expect_lacks "$1" "$msg" "/secret"
}
# An unreadable folder inside the source.
mkdir "$src/locked"
chmod 000 "$src/locked"
check "manifest: unreadable folder" fail manifest "$src" "$tmp/mf/m5" "$tmp/mf/err" 1
expect_eq "manifest: unreadable folder prints nothing" "$msg" ""
[ ! -s "$tmp/mf/m5" ] || fail "manifest: unreadable folder left a manifest"
grep -q locked "$tmp/mf/err" || fail "manifest: the walk's errors were not kept in the error file"
check "check_unchanged: unreadable folder" fail eval \
	'min_media=1; check_unchanged "$src" "$tmp/mf/m1" "$tmp/mf/m5" "$tmp/mf/err"'
expect_unreadable "check_unchanged: unreadable folder"
chmod 755 "$src/locked"
# A find that fails after printing part of the walk, and one that prints
# nothing.
mkdir "$tmp/find-fail" "$tmp/find-empty"
cat >"$tmp/find-fail/find" <<'EOF'
#!/bin/sh
printf 'Regular File|1|1|./IMG_0.JPG\n'
echo "find: /secret/corpus/x: Permission denied" >&2
exit 1
EOF
printf '#!/bin/sh\nexit 0\n' >"$tmp/find-empty/find"
chmod 755 "$tmp/find-fail/find" "$tmp/find-empty/find"
check "check_unchanged: find fails" fail eval \
	'PATH="$tmp/find-fail:$PATH"; min_media=1; check_unchanged "$src" "$tmp/mf/m1" "$tmp/mf/m6" "$tmp/mf/err"'
expect_unreadable "check_unchanged: find fails"
check "check_unchanged: find prints nothing" fail eval \
	'PATH="$tmp/find-empty:$PATH"; min_media=1; check_unchanged "$src" "$tmp/mf/m1" "$tmp/mf/m7" "$tmp/mf/err"'
expect_unreadable "check_unchanged: find prints nothing"

# canon_path resolves links, keeps a missing tail and handles "/".
mkdir -p "$tmp/real dir/sub"
ln -s "$tmp/real dir" "$tmp/link dir"
real=$(cd -P "$tmp/real dir" && pwd -P)
expect_eq "canon_path: link" "$(canon_path "$tmp/link dir/sub")" "$real/sub"
expect_eq "canon_path: missing tail" "$(canon_path "$tmp/link dir/no/such")" "$real/no/such"
expect_eq "canon_path: trailing slash" "$(canon_path "$tmp/link dir/")" "$real"
expect_eq "canon_path: root" "$(canon_path /)" /
check "canon_path: relative" fail canon_path "rel/path"

# check_overlaps <dir> <corpus> <source> <container>.
P=/v/Photos S=/v/Photos/Trip C=/u/Library/Containers/x
check "overlaps: disjoint" pass check_overlaps /t/w "$P" "$S" "$C"
check "overlaps: no corpus (head only)" pass check_overlaps /t/w '' '' "$C"
check "overlaps: dir inside corpus" fail check_overlaps /v/Photos/w "$P" "$S" "$C"
expect_eq "overlaps: message" "$msg" "sandbox-smoke: $overlap"
check "overlaps: dir is the corpus" fail check_overlaps /v/Photos "$P" "$S" "$C"
check "overlaps: corpus inside dir" fail check_overlaps /v "$P" "$S" "$C"
check "overlaps: dir inside source" fail check_overlaps /v/Photos/Trip/w '' "$S" "$C"
check "overlaps: source inside dir" fail check_overlaps /v/Photos '' "$S" "$C"
check "overlaps: other case" fail check_overlaps /V/PHOTOS/w "$P" "$S" "$C"
check "overlaps: dir inside container" fail check_overlaps "$C/Data/tmp" "$P" "$S" "$C"
expect_contains "overlaps: container message" "$msg" "the work directory is inside Choix's container"
check "overlaps: source inside container" fail check_overlaps /t/w '' "$C/Data/p" "$C"
expect_contains "overlaps: source/container message" "$msg" "the photo folder overlaps Choix's container"
check "overlaps: container inside source" fail check_overlaps /t/w '' /u "$C"
check "overlaps: name prefix is not inside" pass check_overlaps /v/Photos2/w "$P" "$S" "$C"

# safe_remove <path> <owner>: only inside the mktemp directory, never "/",
# $HOME, or anything overlapping the photo folder.
g="$tmp/guard"
mkdir -p "$g/home/work/x" "$g/photos/Trip/w" "$g/outside" "$g/work/a/b" "$g/work/c"
printf keep >"$g/outside/file"
ln -s "$g/outside" "$g/work/link"
gh=$(canon_path "$g/home") gp=$(canon_path "$g/photos") gs=$(canon_path "$g/photos/Trip")
gw=$(canon_path "$g/work")
# remove <want: pass|fail> <path> <owner>: safe_remove with the guard's
# home, photo and source folders.
remove() {
	rm_path=$2 rm_owner=$3
	check "safe_remove $2 (owner $3)" "$1" eval \
		'home_canon=$gh corpus_canon=$gp src_canon=$gs; safe_remove "$rm_path" "$rm_owner"'
	# A refusal is one "refusing to remove" line, not a shell error.
	if [ "$1" = fail ]; then
		expect_contains "safe_remove $2 (owner $3)" "$msg" "refusing to remove"
	fi
}
remove fail "$gw/a" ''
remove fail / /
remove fail "$gw/a" /
remove fail "$g/outside" "$gw"
[ -f "$g/outside/file" ] || fail "safe_remove removed a path outside the work directory"
remove fail "$gh/work" "$gh"
remove fail "$gh" "$gh"
remove fail "$g" "$g"
[ -d "$gh/work/x" ] || fail "safe_remove removed inside the home folder"
# A work directory below $HOME (a TMPDIR there) is fine.
remove pass "$gh/work/x" "$gh/work"
remove fail "$gs/w" "$gp"
remove fail "$gs" "$gs"
[ -d "$gs/w" ] || fail "safe_remove removed inside the photo folder"
remove pass "$gw/link" "$gw"
[ -f "$g/outside/file" ] || fail "safe_remove followed a symlink out of the work directory"
[ ! -e "$gw/link" ] && [ ! -L "$gw/link" ] || fail "safe_remove did not remove the symlink"
remove pass "$gw/a" "$gw"
[ ! -e "$gw/a" ] || fail "safe_remove did not remove a directory inside the work directory"
remove pass "$gw" "$gw"
[ ! -e "$gw" ] || fail "safe_remove did not remove the work directory"

# folder_row on a synthetic library.db (the columns it reads): matched
# case-insensitively, a quote in the path is safe, an unknown path is empty.
db="$tmp/library.db"
sqlite3 "$db" "CREATE TABLE folders (id TEXT PRIMARY KEY, path TEXT NOT NULL,
	presence TEXT NOT NULL DEFAULT 'present', job_pending INTEGER NOT NULL DEFAULT 0,
	job_error TEXT NOT NULL DEFAULT '', photos INTEGER NOT NULL DEFAULT 0,
	videos INTEGER NOT NULL DEFAULT 0, clusters INTEGER NOT NULL DEFAULT 0);
	INSERT INTO folders VALUES ('a', '/private/var/T/choix-smoke/folder-1', 'present', 0, '', 7, 2, 3);
	INSERT INTO folders VALUES ('b', '/x/it''s', 'offline', 1, 'boom', 0, 0, 0);"
expect_eq "folder_row" "$(folder_row "$db" /PRIVATE/var/T/choix-smoke/folder-1)" "0 present 9 3 1"
expect_eq "folder_row, quote" "$(folder_row "$db" "/x/it's")" "1 offline 0 0 0"
expect_eq "folder_row, unknown" "$(folder_row "$db" /nope)" ""

# The manual checks printed at step 7: the sixteen titles of the checklist.
doc="$here/../../docs/release/sandbox-smoke.md"
[ -f "$doc" ] || fail "missing $doc"
expect_eq "manual checks" "$(manual_checks "$doc" | wc -l | tr -d ' ')" 16
expect_eq "first manual check" "$(manual_checks "$doc" | head -n 1)" "1. Launch"
expect_eq "last manual check" "$(manual_checks "$doc" | tail -n 1)" '16. `~/.choix` was not created'

# redact: $HOME becomes "~", wherever it appears.
expect_eq redact "$(printf 'a %s/x and %s\n' "$HOME" "$HOME" | redact)" "a ~/x and ~"

echo ok
