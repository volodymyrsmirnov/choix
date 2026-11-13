#!/bin/sh
# Smoke test of the sandboxed app bundle. Run with `task smoke:sandbox`
# (or `sh build/smoke/sandbox-smoke.sh`) from anywhere in the repository:
#
#   sandbox-smoke.sh [--head-only]
#
#   1. `task package` builds and ad hoc signs bin/Choix.app.
#   2. Static checks on bin/Choix.app: exactly the three sandbox
#      entitlements; the executable links nothing outside /usr/lib, /System,
#      @rpath and @executable_path; it was built with -tags=production and
#      neither private_mac_apis nor server; none of the private-API
#      selectors of Wails' mac_private_api_darwin.go is in it. A tool that
#      fails or prints nothing fails its check.
#   3. The sandboxed self-check (CHOIX_SELFCHECK=1, run inside the app's
#      container) reports "sandboxed":true, a Vision feature print
#      ("featurePrint":"ok", "length":768, "revision":2), "quality":"ok",
#      "bookmarkRoundTrip":true and "trashCheck":true.
#   --head-only stops here and needs no photo folder.
#   4. The source folder is chosen by rule — the smallest folder (in bytes)
#      under $CHOIX_UAT_ROOT with at least 50 media files, counted
#      recursively, hidden folders and picks folders skipped — and is only
#      ever called folder-1. A manifest (type, size, mtime, path) of it is
#      taken and it is cloned with `cp -cR` to folder-1 in a new
#      $TMPDIR/choix-smoke.XXXXXX directory (the clone's own .choix/,
#      inherited from an earlier scan of the original, is removed so the
#      scan starts fresh). The manifest stays in that directory and is never
#      printed; a manifest that cannot be read completely is an error, never
#      "unchanged".
#   5. The app is launched and the clone is added through the open panel
#      with osascript (System Events: ⌘O, ⇧⌘G, the path, Return, Return).
#      Without Accessibility permission for the terminal it prints the
#      manual steps and waits for Enter.
#   6. The container's library.db is polled read-only (sqlite3 -readonly)
#      until folder-1's job_pending is 0; then its counts must be > 0, its
#      job error empty, <clone>/.choix/state.db must exist and the
#      original's manifest must be unchanged. From the launch until the end
#      every Choix process is sampled once a second with `pgrep -P`: Choix
#      must never have a child process.
#   7. It prints the manual checks of docs/release/sandbox-smoke.md and
#      waits for Enter; then it checks the children, the manifest and
#      ~/.choix once more.
#
# Without --head-only, CHOIX_UAT_ROOT must name an existing folder; there is
# no default. The script prints no folder or file name from that folder, no
# count and no size. Originals are only read. The app's container is only
# read (library.db, read-only); nothing under ~/Library is changed by this
# script.
#
# Before anything is written, the photo folder (whenever CHOIX_UAT_ROOT is
# set, --head-only included), the source folder, the repository checkout,
# the temporary directory and the app's container are resolved (pwd -P) and
# the script refuses to run when the temporary directory or the checkout
# (where `task package` writes) and the photo or source folder contain one
# another, when the temporary directory is in the container, or when the
# photo or source folder and the container overlap. With --head-only a set
# CHOIX_UAT_ROOT must still name an existing folder. The
# script writes only into directories it made with mktemp -d, and removes
# only those (or things inside them), never "/", $HOME or anything that
# overlaps the photo folder.
#
# CHOIX_SMOKE_LIB=1 defines the functions without running anything (for
# build/smoke/sandbox-smoke_test.sh).
set -eu

prog=sandbox-smoke
bundle_id=tech.inkhorn.choix
min_media=50
scan_timeout=3600

# The resolved paths the overlap and removal guards compare against; set by
# main (and by the tests).
corpus_canon=''
src_canon=''
home_canon=''

# The three entitlements of build/darwin/entitlements.plist, all true.
want_entitlements='com.apple.security.app-sandbox
com.apple.security.files.bookmarks.app-scope
com.apple.security.files.user-selected.read-write'

# Selectors only Wails' mac_private_api_darwin.go (build tag
# private_mac_apis) sends.
private_selectors='setGroupIdentifier:
setGroupSpacing:
developerExtrasEnabled'

# --- Output.

say() {
	printf '%s\n' "$*"
}

# problem <text>: one line on stderr, with $HOME shown as "~".
problem() {
	printf '%s: %s\n' "$prog" "$*" | redact >&2
}

die() {
	problem "$*"
	exit 1
}

# redact: copies stdin to stdout with every $HOME replaced by "~".
redact() {
	awk -v h="${HOME:-}" '
		h == "" || h == "/" { print; next }
		{
			out = ""
			s = $0
			while ((i = index(s, h)) > 0) {
				out = out substr(s, 1, i - 1) "~"
				s = substr(s, i + length(h))
			}
			print out s
		}'
}

# --- Paths.

# canon_path <absolute path>: the path with every symbolic link resolved
# (pwd -P). When it does not exist, its nearest existing ancestor is
# resolved and the missing elements are appended unchanged.
canon_path() {
	cn_p=$1
	case $cn_p in
	/*) ;;
	*) return 1 ;;
	esac
	while :; do
		case $cn_p in
		?*/) cn_p=${cn_p%/} ;;
		*) break ;;
		esac
	done
	cn_rest=''
	while [ ! -d "$cn_p" ]; do
		cn_rest="/${cn_p##*/}$cn_rest"
		cn_p=${cn_p%/*}
		[ -n "$cn_p" ] || cn_p=/
	done
	cn_c=$(CDPATH='' cd -P -- "$cn_p" && pwd -P) || return 1
	[ "$cn_c" != / ] || cn_c=''
	cn_c="$cn_c$cn_rest"
	printf '%s\n' "${cn_c:-/}"
}

# lower <text>: text in lower case (APFS and HFS+ compare names that way).
lower() {
	printf '%s' "$1" | tr '[:upper:]' '[:lower:]'
}

# path_within <a> <b>: a is b or lies below b (resolved paths, compared
# case-insensitively).
path_within() {
	pw_a=$(lower "$1")
	pw_b=$(lower "$2")
	[ "$pw_b" != / ] || return 0
	case $pw_a in
	"$pw_b" | "$pw_b"/*) return 0 ;;
	esac
	return 1
}

# path_overlaps <a> <b>: one of a and b contains the other.
path_overlaps() {
	path_within "$1" "$2" || path_within "$2" "$1"
}

# check_overlaps <dir> <corpus> <source> <container>: refuses (one line,
# return 1) when dir — a resolved directory the script writes into — and the
# corpus or source folder contain one another, when dir is in the
# container, or when the corpus or source folder and the container overlap.
# An empty corpus or source is not checked.
check_overlaps() {
	for co_x in "$2" "$3"; do
		[ -n "$co_x" ] || continue
		if path_overlaps "$1" "$co_x"; then
			problem "the work directory overlaps the photo folder"
			return 1
		fi
	done
	if path_within "$1" "$4"; then
		problem "the work directory is inside Choix's container"
		return 1
	fi
	for co_x in "$2" "$3"; do
		[ -n "$co_x" ] || continue
		if path_overlaps "$co_x" "$4"; then
			problem "the photo folder overlaps Choix's container"
			return 1
		fi
	done
}

# safe_remove <path> <owner>: rm -rf path, but only when owner (a directory
# the script made with mktemp -d, resolved) is set, is not "/" or $HOME,
# path is owner or lies inside it, and path overlaps neither $HOME nor the
# photo or source folder. Refuses with one line and returns 1 otherwise.
safe_remove() {
	if [ -z "$1" ] || [ -z "$2" ] || [ "$2" = / ]; then
		problem "refusing to remove: no work directory"
		return 1
	fi
	sr_parent=$(canon_path "$(dirname -- "$1")") || {
		problem "refusing to remove: unresolvable path"
		return 1
	}
	sr_p="${sr_parent%/}/$(basename -- "$1")"
	if [ -n "$home_canon" ] && { path_within "$home_canon" "$2" || path_within "$home_canon" "$sr_p"; }; then
		problem "refusing to remove: it contains the home folder"
		return 1
	fi
	if ! path_within "$sr_p" "$2"; then
		problem "refusing to remove: outside the work directory"
		return 1
	fi
	for sr_x in "$corpus_canon" "$src_canon"; do
		[ -n "$sr_x" ] || continue
		if path_overlaps "$sr_p" "$sr_x"; then
			problem "refusing to remove: overlaps the photo folder"
			return 1
		fi
	done
	rm -rf -- "$sr_p"
}

# --- The source folder.

# media_find <dir>: every media file below dir (the scanner's extensions),
# one per line; hidden files, hidden folders and picks folders are skipped.
media_find() {
	find "$1" -mindepth 1 \( -type d \( -name '.*' -o -iname picks \) -prune \) -o \
		-type f ! -name '.*' \( -iname '*.raf' -o -iname '*.dng' -o -iname '*.heic' \
		-o -iname '*.heif' -o -iname '*.jpg' -o -iname '*.jpeg' -o -iname '*.tif' \
		-o -iname '*.tiff' -o -iname '*.png' -o -iname '*.mov' -o -iname '*.mp4' \
		-o -iname '*.m4v' \) -print 2>/dev/null
}

# media_count <dir>: the number of media files below dir.
media_count() {
	media_find "$1" | wc -l | tr -d ' '
}

# choose_folder <root>: prints the smallest folder (du -sk) below root with
# at least min_media media files, counted recursively; ties go to the first
# in byte order. Returns 1 when there is none. Prints nothing else.
choose_folder() {
	cf_root=$1
	while :; do
		case $cf_root in
		?*/) cf_root=${cf_root%/} ;;
		*) break ;;
		esac
	done
	cf_dirs=$(media_find "$cf_root" | awk -v root="$cf_root" -v min="$min_media" '
		{
			n = split(substr($0, length(root) + 2), part, "/")
			d = root
			for (i = 1; i < n; i++) {
				d = d "/" part[i]
				count[d]++
			}
		}
		END { for (d in count) if (count[d] >= min) print d }' | LC_ALL=C sort)
	[ -n "$cf_dirs" ] || return 1
	cf_best='' cf_best_kb=''
	while IFS= read -r cf_dir; do
		cf_kb=$(du -sk "$cf_dir" 2>/dev/null | cut -f1)
		[ -n "$cf_kb" ] || continue
		if [ -z "$cf_best" ] || [ "$cf_kb" -lt "$cf_best_kb" ]; then
			cf_best=$cf_dir cf_best_kb=$cf_kb
		fi
	done <<EOF
$cf_dirs
EOF
	[ -n "$cf_best" ] || return 1
	printf '%s\n' "$cf_best"
}

# manifest <dir> <out> <errfile> <min files>: writes to out the type, size,
# mtime and relative path of every entry under dir (dir included), sorted.
# The tools' stderr goes to errfile (it can name files; never print it).
# Returns 1, printing nothing, when the walk or a stat fails or fewer than
# <min files> regular files were listed: an incomplete manifest proves
# nothing.
manifest() {
	[ -d "$1" ] || return 1
	mf_raw="$2.raw"
	rm -f "$mf_raw" "$2"
	if (CDPATH='' cd -- "$1" && find . -exec stat -f '%HT|%z|%m|%N' {} +) >"$mf_raw" 2>"$3"; then
		:
	else
		rm -f "$mf_raw"
		return 1
	fi
	mf_n=$(grep -c '^Regular File|' "$mf_raw" 2>>"$3") || mf_n=0
	if [ "$mf_n" -lt "$4" ]; then
		rm -f "$mf_raw"
		return 1
	fi
	if LC_ALL=C sort "$mf_raw" >"$2" 2>>"$3"; then
		rm -f "$mf_raw"
	else
		rm -f "$mf_raw" "$2"
		return 1
	fi
}

# check_unchanged <source> <before> <after> <errfile>: takes the manifest
# of source again into after and compares it with before. Prints one line
# and returns 1 when it cannot be read completely or differs; prints
# nothing when it is unchanged.
check_unchanged() {
	manifest "$1" "$3" "$4" "$min_media" || {
		problem "could not read the original folder; cannot prove it is unchanged"
		return 1
	}
	cmp -s "$2" "$3" || {
		problem "the original of folder-1 changed (the manifests are not printed)"
		return 1
	}
}

# --- Static checks. Each prints what is wrong on stderr and returns 1.

# run_tool <out> <errfile> <command…>: runs the command with stdout to out
# and stderr to errfile; returns 1 when it fails or prints nothing.
run_tool() {
	rt_out=$1 rt_err=$2
	shift 2
	if "$@" >"$rt_out" 2>"$rt_err"; then
		:
	else
		problem "$1 failed"
		return 1
	fi
	[ -s "$rt_out" ] || {
		problem "$1 printed nothing"
		return 1
	}
}

# check_entitlements <plist>: exactly the three entitlements, each true.
check_entitlements() {
	ce_json=$(plutil -convert json -o - "$1" 2>/dev/null) || {
		problem "entitlements: not a property list"
		return 1
	}
	ce_got=$(printf '%s' "$ce_json" | tr -d '{}\n' | tr ',' '\n' | LC_ALL=C sort)
	ce_want=$(printf '%s\n' "$want_entitlements" | sed -e 's/^.*$/"&":true/' | LC_ALL=C sort)
	[ "$ce_got" = "$ce_want" ] || {
		problem "entitlements: got $(printf '%s' "$ce_got" | tr '\n' ' '); want the three sandbox entitlements, each true"
		return 1
	}
}

# check_linked_libs <otool -L output>: every library is under /usr/lib or
# /System, or @rpath- or @executable_path-relative.
check_linked_libs() {
	cl_libs=$(sed -e '1d' -e 's/^[[:space:]]*//' -e 's/ (compatibility version.*$//' "$1") || {
		problem "cannot read the otool -L output"
		return 1
	}
	[ -n "$cl_libs" ] || {
		problem "otool -L lists no library"
		return 1
	}
	cl_bad=$(printf '%s\n' "$cl_libs" | grep -Ev '^(/usr/lib/|/System/|@rpath/|@executable_path/)') && {
		problem "the executable links outside the system: $(printf '%s' "$cl_bad" | tr '\n' ' ')"
		return 1
	}
	return 0
}

# check_build_tags <go version -m output>: -tags includes production and
# neither private_mac_apis nor server.
check_build_tags() {
	bt_tags=$(sed -n 's/^[[:space:]]*build[[:space:]][[:space:]]*-tags=//p' "$1") || {
		problem "cannot read the go version -m output"
		return 1
	}
	bt_prod=no
	for bt_tag in $(printf '%s' "$bt_tags" | tr ',' ' '); do
		case $bt_tag in
		production) bt_prod=yes ;;
		private_mac_apis | server)
			problem "the executable was built with -tags $bt_tag"
			return 1
			;;
		esac
	done
	[ "$bt_prod" = yes ] || {
		problem "the executable was not built with -tags=production (got '-tags=$bt_tags')"
		return 1
	}
}

# check_private_selectors <file> <dir>: `strings -a` of file (kept in dir)
# contains none of private_selectors. A strings failure or empty output
# fails the check.
check_private_selectors() {
	run_tool "$2/strings.txt" "$2/strings.err" strings -a "$1" || return 1
	if grep -F "$private_selectors" "$2/strings.txt" >"$2/selectors.txt" 2>"$2/grep.err"; then
		problem "private-API selector(s) in the executable: $(LC_ALL=C sort -u "$2/selectors.txt" | tr '\n' ' ')"
		return 1
	else
		ps_rc=$?
	fi
	[ "$ps_rc" = 1 ] || {
		problem "cannot search the strings of the executable"
		return 1
	}
}

# check_selfcheck <report.json>: the report reads "sandboxed":true, a
# vision object with "featurePrint":"ok", "length":768 and "revision":2,
# "quality":"ok", "bookmarkRoundTrip":true and "trashCheck":true. Each
# check is "<key path>|<compact JSON the report must contain>".
check_selfcheck() {
	[ -s "$1" ] || {
		problem "self-check printed no report"
		return 1
	}
	sc_status=0
	for sc_check in 'sandboxed|"sandboxed":true' \
		'vision.featurePrint|"featurePrint":"ok"' 'vision.length|"length":768' 'vision.revision|"revision":2' \
		'quality|"quality":"ok"' 'bookmarkRoundTrip|"bookmarkRoundTrip":true' 'trashCheck|"trashCheck":true'; do
		sc_key=${sc_check%%|*}
		sc_want=${sc_check#*|}
		if grep -Fq -- "$sc_want" "$1"; then
			continue
		fi
		sc_val=$(plutil -extract "$sc_key" raw -o - "$1" 2>/dev/null) || sc_val='(missing)'
		problem "self-check: $sc_key is '$sc_val', want ${sc_want#*:}"
		sc_status=1
	done
	return "$sc_status"
}

# manual_checks <doc>: "N. <title>" for each numbered check in the doc's
# "## Checks" section.
manual_checks() {
	sed -n '/^## Checks/,/^## Result record/p' "$1" |
		sed -n 's/^\([0-9][0-9]*\. \)\*\*\([^*]*\)\*\*.*$/\1\2/p'
}

# --- The running app.

# choix_pids: the pids of every process named Choix, one per line.
choix_pids() {
	pgrep -x Choix 2>/dev/null || true
}

# sample_children <file>: until <file>.stop exists, once a second appends
# the command of every child process of every Choix process to <file>.
sample_children() {
	while [ ! -e "$1.stop" ]; do
		for sc_pid in $(choix_pids); do
			for sc_kid in $(pgrep -P "$sc_pid" 2>/dev/null || true); do
				sc_cmd=$(ps -o comm= -p "$sc_kid" 2>/dev/null || true)
				printf '%s\n' "${sc_cmd:-pid $sc_kid}" >>"$1"
			done
		done
		sleep 1
	done
}

# check_children <file>: the sampler saw no child process.
check_children() {
	[ -s "$1" ] || return 0
	problem "Choix started child process(es): $(LC_ALL=C sort -u "$1" | tr '\n' ' ')"
	return 1
}

# folder_row <db> <path>: "job_pending presence media clusters no_error"
# for the Library folder at path (case-insensitive), or nothing. Read-only.
folder_row() {
	fr_path=$(printf '%s' "$2" | sed "s/'/''/g")
	sqlite3 -readonly -cmd '.timeout 5000' -separator ' ' "$1" \
		"SELECT job_pending, presence, photos + videos, clusters, job_error = '' FROM folders WHERE lower(path) = lower('$fr_path');"
}

# wait_row <seconds>: waits up to that long for folder-1 ($canon) to appear
# in $db; returns 1 when it does not.
wait_row() {
	wr_n=0
	while [ ! -f "$db" ] || [ -z "$(folder_row "$db" "$canon" 2>/dev/null || true)" ]; do
		wr_n=$((wr_n + 1))
		[ "$wr_n" -le "$1" ] || return 1
		sleep 1
	done
}

# wait_enter <prompt>: prints the prompt and waits for Enter on the terminal.
wait_enter() {
	printf '%s ' "$1"
	read -r _ </dev/tty || die "no terminal to read Enter from"
}

# drive_open_panel <path>: adds path through Choix's open panel with
# System Events; fails when the terminal has no Accessibility permission.
drive_open_panel() {
	ax=$(osascript -e 'tell application "System Events" to get UI elements enabled' 2>/dev/null || true)
	[ "$ax" = true ] || return 1
	osascript - "$1" >/dev/null 2>&1 <<'EOF'
on run argv
	set thePath to item 1 of argv
	tell application "System Events"
		repeat 60 times
			if exists process "Choix" then
				if (count of windows of process "Choix") > 0 then exit repeat
			end if
			delay 0.5
		end repeat
		tell process "Choix" to set frontmost to true
		delay 1
		keystroke "o" using command down
		delay 1.5
		keystroke "g" using {command down, shift down}
		delay 1
		keystroke thePath
		delay 0.5
		key code 36
		delay 1
		key code 36
	end tell
end run
EOF
}

# --- Main.

usage() {
	printf 'usage: sh build/smoke/sandbox-smoke.sh [--head-only]\n' >&2
	exit 2
}

cleanup() {
	if [ -n "${children:-}" ]; then
		: >"$children.stop" 2>/dev/null || true
	fi
	if [ -n "${sampler:-}" ]; then
		kill "$sampler" 2>/dev/null || true
		wait "$sampler" 2>/dev/null || true
	fi
	if [ -n "${work:-}" ]; then
		safe_remove "$work" "$work" || true
	fi
}

# make_dir <prefix>: mktemp -d "$tmp_canon/<prefix>.XXXXXX", resolved and
# checked against the photo folder and the container; prints its path.
make_dir() {
	md_d=$(mktemp -d "$tmp_canon/$1.XXXXXX") || {
		problem "cannot create a work directory"
		return 1
	}
	md_c=$(canon_path "$md_d") || md_c=''
	if [ -z "$md_c" ] || ! check_overlaps "$md_c" "$corpus_canon" "$src_canon" "$container_canon"; then
		rmdir -- "$md_d" 2>/dev/null || true
		return 1
	fi
	printf '%s\n' "$md_c"
}

main() {
	head_only=no
	for arg in "$@"; do
		case $arg in
		--head-only) head_only=yes ;;
		-h | --help) usage ;;
		*) usage ;;
		esac
	done

	here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
	root=$(CDPATH='' cd -- "$here/../.." && pwd)
	app="$root/bin/Choix.app"
	exe="$app/Contents/MacOS/Choix"
	doc="$root/docs/release/sandbox-smoke.md"
	container="$HOME/Library/Containers/$bundle_id/Data/Library/Application Support/Choix"
	db="$container/library.db"

	# Resolve every path the guards compare, before anything is written.
	home_canon=$(canon_path "$HOME") || die "HOME is not an absolute path"
	tmp_canon=$(canon_path "${TMPDIR:-/tmp}") || die "TMPDIR is not an absolute path"
	[ -d "$tmp_canon" ] || die "the temporary directory does not exist"
	container_canon=$(canon_path "$HOME/Library/Containers/$bundle_id") ||
		die "cannot resolve Choix's container"

	root_canon=$(canon_path "$root") || die "cannot resolve the repository checkout"

	# A supplied photo folder is protected in both modes; only the full run
	# needs one.
	if [ -n "${CHOIX_UAT_ROOT:-}" ]; then
		[ -d "$CHOIX_UAT_ROOT" ] || die "CHOIX_UAT_ROOT is not an existing folder"
		corpus_canon=$(canon_path "$(CDPATH='' cd -- "$CHOIX_UAT_ROOT" && pwd)") ||
			die "CHOIX_UAT_ROOT cannot be resolved"
	elif [ "$head_only" = no ]; then
		die "CHOIX_UAT_ROOT is not set: set it to your photo folder, or pass --head-only"
	fi
	# task package writes into the checkout (bin/, frontend/dist, build/.tools).
	if [ -n "$corpus_canon" ] && path_overlaps "$root_canon" "$corpus_canon"; then
		die "the repository checkout overlaps the photo folder"
	fi
	# The work directories are made below the temporary directory: refuse
	# while it overlaps the photo folder or lies in the container.
	check_overlaps "$tmp_canon" "$corpus_canon" '' "$container_canon" || exit 1

	if [ "$head_only" = no ]; then
		say "0. choosing folder-1…"
		src=$(choose_folder "$corpus_canon") ||
			die "no folder under CHOIX_UAT_ROOT has at least $min_media media files"
		src_canon=$(canon_path "$src") || die "folder-1 cannot be resolved"
		check_overlaps "$tmp_canon" "$corpus_canon" "$src_canon" "$container_canon" || exit 1
		say "0. folder-1: chosen (the smallest folder with at least $min_media media files)"
		for c in osascript sqlite3 pgrep pbcopy open; do
			command -v "$c" >/dev/null 2>&1 || die "$c is not installed"
		done
		[ -z "$(choix_pids)" ] || die "Choix is running: quit every copy of it first"
		if [ -f "$db" ]; then
			folder_row "$db" / >/dev/null 2>&1 ||
				die "cannot read Choix's library.db: allow this terminal to read other apps' data when macOS asks (or give it Full Disk Access), then run again"
		fi
	fi

	work=$(make_dir choix-sandbox-smoke) || exit 1
	children="$work/children"
	sampler=''
	trap cleanup EXIT
	trap 'exit 1' HUP INT TERM
	: >"$work/start"
	home_choix=no
	[ ! -e "$HOME/.choix" ] || home_choix=yes

	# --- 1. Package.
	say "1. task package…"
	task -d "$root" package >"$work/package.log" 2>&1 || {
		tail -n 40 "$work/package.log" | redact >&2
		die "1. task package failed"
	}
	say "1. task package: ok"

	# --- 2. Static checks. A tool that fails or prints nothing fails.
	[ -x "$exe" ] || die "2. no executable in bin/Choix.app"
	run_tool "$work/entitlements.plist" "$work/codesign.err" codesign -d --xml --entitlements - "$app" ||
		die "2. codesign cannot read the entitlements of bin/Choix.app"
	check_entitlements "$work/entitlements.plist" || die "2. entitlements: FAIL"
	run_tool "$work/otool.txt" "$work/otool.err" otool -L "$exe" || die "2. otool -L: FAIL"
	check_linked_libs "$work/otool.txt" || die "2. linked libraries: FAIL"
	run_tool "$work/goversion.txt" "$work/goversion.err" go version -m "$exe" || die "2. go version -m: FAIL"
	check_build_tags "$work/goversion.txt" || die "2. build tags: FAIL"
	check_private_selectors "$exe" "$work" || die "2. private-API selectors: FAIL"
	say "2. static checks: ok (three entitlements; system libraries only; -tags=production; no private-API selector)"

	# --- 3. The sandboxed self-check.
	set +e
	CHOIX_SELFCHECK=1 "$exe" >"$work/selfcheck.json" 2>"$work/selfcheck.err"
	code=$?
	set -e
	check_selfcheck "$work/selfcheck.json" || {
		tail -n 20 "$work/selfcheck.err" | redact >&2
		die "3. self-check: FAIL"
	}
	[ "$code" = 0 ] || {
		tail -n 20 "$work/selfcheck.err" | redact >&2
		die "3. self-check exited $code"
	}
	say "3. self-check: ok (sandboxed; Vision feature print; quality formula; bookmark round trip; trash check)"

	if [ "$head_only" = yes ]; then
		check_home_choix
		say "head only: steps 1-3 passed"
		return 0
	fi

	# --- 4. Manifest and clone, in a new directory of their own.
	smoke=$(make_dir choix-smoke) || exit 1
	clone="$smoke/folder-1"
	canon=$clone
	manifest "$src_canon" "$smoke/manifest.before" "$work/manifest.err" "$min_media" ||
		die "4. could not read the original folder; cannot take its manifest"
	cp -cR "$src_canon" "$clone" 2>"$work/cp.err" || {
		problem "4. cp -cR failed ($(wc -l <"$work/cp.err" | tr -d ' ') error line(s), not printed)"
		exit 1
	}
	if [ -e "$clone/.choix" ] || [ -L "$clone/.choix" ]; then
		safe_remove "$clone/.choix" "$smoke" || die "4. cannot remove the clone's .choix"
	fi
	say "4. folder-1 cloned to \$TMPDIR/choix-smoke.XXXXXX/folder-1; manifest of the original taken: ok"

	# --- 5. Launch and add the clone.
	open "$app" || die "5. open could not launch bin/Choix.app"
	n=0
	while [ -z "$(choix_pids)" ]; do
		n=$((n + 1))
		[ "$n" -le 30 ] || die "5. Choix did not start within 30 s"
		sleep 1
	done
	sample_children "$children" &
	sampler=$!
	printf '%s' "$clone" | pbcopy
	if drive_open_panel "$clone" && wait_row 30; then
		say "5. Choix launched; folder-1 added through the open panel (osascript): ok"
	else
		say "5. Choix launched, but folder-1 could not be added by script (no Accessibility permission for this"
		say "   terminal?). Add it by hand: in Choix press ⌘O, then ⇧⌘G, paste with ⌘V (the path of"
		say "   folder-1 is on the clipboard), press Return, then Return again to choose it."
		wait_enter "   Press Enter here once folder-1 is in the Library."
		wait_row 60 ||
			die "5. folder-1 did not appear in Choix's Library within 60 s (or this terminal may not read its library.db)"
		say "5. folder-1 added by hand: ok"
	fi

	# --- 6. The scan.
	say "6. waiting for folder-1's scan…"
	seen_pending=no
	start=$(date +%s)
	while :; do
		[ -n "$(choix_pids)" ] || die "6. Choix quit during the scan"
		row=$(folder_row "$db" "$canon") || die "6. cannot read Choix's library.db"
		[ -n "$row" ] || die "6. folder-1 left Choix's Library during the scan"
		# shellcheck disable=SC2086 # split the row into its five fields
		set -- $row
		if [ "$1" = 1 ]; then
			seen_pending=yes
		elif [ "$seen_pending" = yes ] || [ "$3" -gt 0 ]; then
			break
		fi
		[ $(($(date +%s) - start)) -le "$scan_timeout" ] ||
			die "6. folder-1's scan did not finish within $scan_timeout s"
		sleep 2
	done
	# The statistics are stored after the job is acknowledged: give them up
	# to a minute.
	n=0
	while [ "$3" -le 0 ] || [ "$4" -le 0 ]; do
		n=$((n + 1))
		[ "$n" -le 30 ] || break
		sleep 2
		row=$(folder_row "$db" "$canon") || die "6. cannot read Choix's library.db"
		[ -n "$row" ] || die "6. folder-1 left Choix's Library"
		# shellcheck disable=SC2086 # split the row into its five fields
		set -- $row
	done
	[ "$2" = present ] || die "6. folder-1 is '$2', want present"
	[ "$5" = 1 ] || die "6. folder-1's job ended with an error"
	[ "$3" -gt 0 ] || die "6. folder-1 has no photos or videos"
	[ "$4" -gt 0 ] || die "6. folder-1 has no clusters"
	[ -f "$clone/.choix/state.db" ] || die "6. folder-1/.choix/state.db does not exist"
	check_unchanged "$src_canon" "$smoke/manifest.before" "$smoke/manifest.after" "$work/manifest.err" ||
		die "6. original: FAIL"
	check_children "$children" || die "6. child process: FAIL"
	say "6. scan finished: ok (counts > 0, no job error, state.db in the clone, original unchanged, no child process)"

	# --- 7. The manual checks.
	say "7. Manual checks (docs/release/sandbox-smoke.md); Choix is watched for child processes until you press Enter:"
	manual_checks "$doc" | sed -e 's/^/   /'
	wait_enter "   Press Enter when the manual checks are done."
	: >"$children.stop"
	wait "$sampler" 2>/dev/null || true
	sampler=''
	check_children "$children" || die "7. child process: FAIL"
	check_unchanged "$src_canon" "$smoke/manifest.before" "$smoke/manifest.end" "$work/manifest.err" ||
		die "7. original: FAIL"
	check_home_choix
	say "7. no child process, original unchanged: ok. Record the manual results at the bottom of docs/release/sandbox-smoke.md."
	say "   When you are done, remove folder-1 from Choix's Library, then delete \$TMPDIR/choix-smoke.*."
}

# check_home_choix: ~/.choix was not created, nor changed if it existed.
check_home_choix() {
	if [ "$home_choix" = no ]; then
		[ ! -e "$HOME/.choix" ] || die "~/.choix was created during the run"
		say "   ~/.choix: not created: ok"
	elif [ -n "$(find "$HOME/.choix" -newer "$work/start" -print 2>/dev/null | head -n 1)" ]; then
		die "something under ~/.choix changed during the run"
	else
		say "   ~/.choix: existed before the run, unchanged: ok"
	fi
}

if [ "${CHOIX_SMOKE_LIB:-}" != 1 ]; then
	main "$@"
fi
