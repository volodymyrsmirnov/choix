#!/bin/sh
# Tests the Taskfile. Run with `sh build/taskfile_test.sh` (from anywhere);
# prints `ok` on success.
#
#  1. `task --list-all` lists every task of the desktop build, and the UI
#     track's tasks are still there; the retired runtime-library fetch task
#     is gone.
#  2. Nothing in Taskfile.yml builds for another architecture, fetches,
#     bundles or signs the retired runtime library, or passes --dylib to
#     bundle.sh.
#  3. sign:developer-id, notarize, notarize:dmg and package:mas, run
#     without their variables, exit 1 with "<task> needs <VAR> (<what>). See
#     docs/release/signing-checklist.md." before running any command.
#  4. build, test, lint and package — with every task they call — never
#     read a signing variable, and build, test and lint compile
#     (`task --dry`) without one.
#  5. verify:release, notarize and notarize:dmg, run against a fake bin/
#     with stubs of spctl, xcrun and codesign first on PATH that print, at
#     each verbosity and output format, only what the real tools print
#     there: the Gatekeeper check fails closed (only "assessments enabled"
#     with an accepted, notarized Developer ID verdict, or an explicit
#     "assessments disabled" with a valid stapled ticket, passes);
#     notarize and notarize:dmg accept a Developer ID signed file and
#     refuse an ad hoc, unsigned or otherwise signed one before submitting
#     anything; and no line of their output carries the stubbed signer name
#     or Team ID.
#  6. In a fresh `git worktree` of HEAD (no frontend/dist) `task lint`,
#     `task bindings:check` and `task test -- -run TestNone` succeed: the
#     frontend:bootstrap dependency works, and nothing writes v1's
#     internal/ui/web/dist. The working tree's Taskfile.yml is copied into that
#     checkout, so an uncommitted Taskfile is what is tested. The checkout
#     is removed on exit.
#
# Needs task, go, golangci-lint and wails3 (task tools). Runs no real
# signing, notarization or Gatekeeper command and uses no network beyond
# what `go` itself may need.
set -eu

# This script parses Task's output. Task colours it when CI is set (GitHub
# Actions sets CI=true) or FORCE_COLOR is, even into a pipe, and then no
# line matches. NO_COLOR=1 turns colour off for every task run below
# (`task --help`: "Set flag to false or use NO_COLOR=1 to disable") and wins
# over FORCE_COLOR; FORCE_COLOR and CLICOLOR_FORCE are dropped for the other
# tools.
NO_COLOR=1
export NO_COLOR
unset FORCE_COLOR CLICOLOR_FORCE

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
taskfile="$root/Taskfile.yml"
checklist=docs/release/signing-checklist.md

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

command -v task >/dev/null 2>&1 || fail "task is not installed (brew install go-task)"
[ -f "$taskfile" ] || fail "missing $taskfile"
[ -f "$root/$checklist" ] || fail "missing $root/$checklist"

# Every signing variable. Unset for every task run below.
signing_vars='CHOIX_SIGN_IDENTITY CHOIX_NOTARY_PROFILE CHOIX_MAS_APP_IDENTITY CHOIX_MAS_INSTALLER_IDENTITY CHOIX_MAS_PROFILE CHOIX_TEAM_ID'
for v in $signing_vars; do
	unset "$v"
done

# --- 1. The task list.
list=$(task -d "$root" --list-all) || fail "task --list-all failed"
# Lines look like "* design:snapshot:linux:   <desc>"; names have no spaces.
names=$(printf '%s\n' "$list" | sed -n 's/^\* \([^ ]*\):\( .*\)\{0,1\}$/\1/p')
for t in tools frontend:bootstrap frontend:dist \
	bindings bindings:check dev dev:frontend dev:run run:server uat \
	build package sign:dev sign:developer-id notarize dmg notarize:dmg \
	verify:release package:mas \
	test test:smoke test:e2e lint fmt icons clean smoke:sandbox \
	frontend:install test:frontend design:status; do
	printf '%s\n' "$names" | grep -Fqx -- "$t" || fail "task --list-all does not list $t"
done
# The retired library's name, split so this file does not contain it.
retired_lib="on""nxruntime"
if printf '%s\n' "$names" | grep -Fqx -- "deps:$retired_lib"; then
	fail "task --list-all still lists deps:$retired_lib"
fi

# --- 2. arm64 only.
n=$(grep -c 'universal\|amd64\|x86_64' "$taskfile" || true)
[ "$n" = 0 ] || fail "Taskfile.yml mentions another architecture on $n line(s)"
# No retired runtime library: no fetch, no ORT_DYLIB, no environment
# variable naming it, no --dylib, no Contents/Frameworks.
n=$(grep -ci "${retired_lib%runtime}\\|ORT_DYLIB\\|--dylib\\|Contents/Frameworks" "$taskfile" || true)
[ "$n" = 0 ] || fail "Taskfile.yml still mentions the retired runtime library on $n line(s)"

# --- 3. The signing tasks' variable checks.

# expect_needs <task> <VAR> [NAME=value…]: `task <task>`, run with the given
# variables set and every other signing variable unset, exits 1, prints the
# line "<task> needs <VAR> (<what>). See docs/release/signing-checklist.md."
# and runs no command (Task echoes each command it runs as "task: [<name>] …").
expect_needs() {
	t=$1 var=$2
	shift 2
	set +e
	out=$(env "$@" task -d "$root" "$t" 2>&1)
	code=$?
	set -e
	[ "$code" = 1 ] || fail "task $t without $var: exit status $code, want 1; output:
$out"
	found=no
	while IFS= read -r line; do
		case $line in
		"task: ["*) fail "task $t without $var ran a command: $line" ;;
		"$t needs $var ("?*"). See $checklist.") found=yes ;;
		esac
	done <<EOF
$out
EOF
	[ "$found" = yes ] || fail "task $t without $var: no line '$t needs $var (…). See $checklist.' in:
$out"
}

expect_needs sign:developer-id CHOIX_SIGN_IDENTITY
expect_needs sign:developer-id CHOIX_SIGN_IDENTITY CHOIX_SIGN_IDENTITY=
expect_needs notarize CHOIX_NOTARY_PROFILE
expect_needs notarize CHOIX_NOTARY_PROFILE CHOIX_SIGN_IDENTITY=test
expect_needs notarize:dmg CHOIX_NOTARY_PROFILE
expect_needs notarize:dmg CHOIX_NOTARY_PROFILE CHOIX_SIGN_IDENTITY=test
expect_needs package:mas CHOIX_MAS_APP_IDENTITY
mas_app=CHOIX_MAS_APP_IDENTITY=test-app
mas_inst=CHOIX_MAS_INSTALLER_IDENTITY=test-installer
mas_prof=CHOIX_MAS_PROFILE=/nonexistent/test.provisionprofile
mas_team=CHOIX_TEAM_ID=ABCDE12345
expect_needs package:mas CHOIX_MAS_APP_IDENTITY "$mas_inst" "$mas_prof" "$mas_team"
expect_needs package:mas CHOIX_MAS_INSTALLER_IDENTITY "$mas_app" "$mas_prof" "$mas_team"
expect_needs package:mas CHOIX_MAS_PROFILE "$mas_app" "$mas_inst" "$mas_team"
expect_needs package:mas CHOIX_TEAM_ID "$mas_app" "$mas_inst" "$mas_prof"

# --- 4. No signing variable on the build path.

# task_block <name>: the lines of task <name> in Taskfile.yml, from its
# "  <name>:" line to the line before the next task or top-level key.
task_block() {
	awk -v want="  $1:" '
		$0 == want { on = 1; print; next }
		on && (/^  [^ #]/ || /^[^ #]/) { exit }
		on { print }
	' "$taskfile"
}

# top_level <key>: the lines of a top-level mapping such as vars or env.
top_level() {
	awk -v want="$1:" '
		$0 == want { on = 1; print; next }
		on && /^[^ #]/ { exit }
		on { print }
	' "$taskfile"
}

# calls <name>: the tasks <name> runs — "- task: X" commands and
# "deps: [X, Y]" — one per line.
calls() {
	task_block "$1" | sed -n \
		-e 's/^ *- task: *\([^ ]*\) *$/\1/p' \
		-e 's/^ *deps: *\[\(.*\)\] *$/\1/p' | tr ',' '\n' | tr -d ' '
}

signing_re='CHOIX_(SIGN|NOTARY|MAS|TEAM)'
for key in vars env dotenv; do
	if top_level "$key" | grep -Eq "$signing_re"; then
		fail "the top-level $key of Taskfile.yml reads a signing variable"
	fi
done
seen=' '
todo='build test lint package'
while :; do
	# shellcheck disable=SC2086 # split the list of names
	set -- $todo
	[ $# -gt 0 ] || break
	t=$1
	shift
	todo=$*
	case $seen in *" $t "*) continue ;; esac
	seen="$seen$t "
	block=$(task_block "$t")
	[ -n "$block" ] || fail "no task $t in Taskfile.yml"
	if printf '%s\n' "$block" | grep -Eq "$signing_re"; then
		fail "task $t (on the path of build/test/lint/package) reads a signing variable"
	fi
	todo="$todo $(calls "$t" | tr '\n' ' ')"
done
for t in build package sign:dev frontend:bootstrap frontend:dist; do
	case $seen in *" $t "*) ;; *) fail "the call graph of build/package misses $t (calls() is broken)" ;; esac
done
# (package is left out of the dry run: sign:dev's precondition needs a
# bin/Choix.app, which a dry run never makes.)
task -d "$root" --dry build test lint >/dev/null 2>&1 ||
	fail "task --dry build test lint fails without the signing variables"

tmproot=${TMPDIR:-/tmp}
tmp=$(mktemp -d "${tmproot%/}/choix-taskfile-test.XXXXXX")
wt="$tmp/checkout"
cleanup() {
	git -C "$root" worktree remove --force "$wt" >/dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

# --- 5. verify:release and notarize:dmg with stubbed Apple tools.
# The working tree's Taskfile.yml runs in a scratch directory with an empty
# bin/Choix.app and bin/Choix-9.9.9-arm64.dmg. The stubs answer as the
# STUB_* variables say, and every answer that the real tools print with the
# signer carries it (spctl's origin=, codesign's Authority= and
# TeamIdentifier=, the notary submission and log).
#
# Each stub prints only what the real tool prints for the same flags
# (checked against real signed and notarized apps and disk images): a stub
# that printed more would hide a Taskfile reading a line its flags never
# get. The first release run failed on exactly that: notarize read
# Authority= from `codesign -dv`, which prints no certificate chain.
#   codesign -d: Executable= at any verbosity; Identifier=, Format=,
#     Signature=adhoc (ad hoc) and TeamIdentifier= from verbosity 1;
#     Authority= (the chain, leaf first) only from verbosity 2 (-dvv,
#     -d -vv, --verbose=2). Unsigned: "code object is not signed at all",
#     exit 1. STUB_SIGNATURE: devid (default), other (an Apple Development
#     leaf), adhoc or none.
#   spctl --assess: nothing without -v; "<file>: <verdict>" and source=
#     from -v; origin= (the signer) only from -vv.
#   xcrun notarytool submit: JSON only with --output-format json, and a
#     status only with --wait (STUB_NOTARY_STATUS, default Invalid).
st="$tmp/stubbed"
mkdir -p "$st/bin/Choix.app" "$st/stubs"
cp "$taskfile" "$st/Taskfile.yml"
: >"$st/bin/Choix-9.9.9-arm64.dmg"
ident_name='Stub Person'
ident_team=STUBTEAM01
ident="Developer ID Application: $ident_name ($ident_team)"

cat >"$st/stubs/spctl" <<'STUB'
#!/bin/sh
case $1 in
--status)
	[ -z "${STUB_GK_OUT:-}" ] || echo "$STUB_GK_OUT"
	exit "${STUB_GK_CODE:-0}"
	;;
--assess)
	v=0
	for a; do
		case $a in
		--verbose) v=$((v + 1)) ;;
		--verbose=*) v=${a#--verbose=} ;;
		--*) ;;
		-v*)
			n=$(printf '%s' "${a#-}" | tr -cd v)
			v=$((v + ${#n}))
			;;
		esac
		f=$a
	done
	if [ "$v" -ge 1 ]; then
		echo "$f: $STUB_VERDICT" >&2
		echo "source=$STUB_SOURCE" >&2
	fi
	if [ "$v" -ge 2 ]; then
		echo "origin=$STUB_IDENT" >&2
	fi
	[ "$STUB_VERDICT" = accepted ] || exit 3
	;;
esac
STUB
cat >"$st/stubs/xcrun" <<'STUB'
#!/bin/sh
case "$1 $2" in
"stapler validate")
	echo "Processing: $3"
	if [ "${STUB_STAPLER_CODE:-0}" = 0 ]; then
		echo "The validate action worked!"
	else
		echo "$3 does not have a ticket stapled to it."
		exit "$STUB_STAPLER_CODE"
	fi
	;;
"stapler staple") echo "The staple and validate action worked!" ;;
"notarytool submit")
	json=no wait=no prev=
	for a; do
		if [ "$prev" = --output-format ] && [ "$a" = json ]; then json=yes; fi
		if [ "$a" = --wait ]; then wait=yes; fi
		prev=$a
	done
	status=${STUB_NOTARY_STATUS:-Invalid}
	if [ "$json" = no ]; then
		echo "Conducting pre-submission checks and initiating connection to the Apple notary service..."
		echo "  id: stub-id"
		if [ "$wait" = yes ]; then echo "  status: $status"; fi
	elif [ "$wait" = yes ]; then
		echo "{\"id\":\"stub-id\",\"message\":\"signed by $STUB_IDENT\",\"status\":\"$status\"}"
	else
		echo "{\"id\":\"stub-id\",\"message\":\"Successfully uploaded file\"}"
	fi
	# Exits 0 whatever the status, so notarize:submit must read the
	# status: an exit status alone would let an Invalid submission pass.
	;;
"notarytool log") printf '{\n  "teamId": "%s",\n  "issues": [{"message": "signed by %s"}]\n}\n' "$STUB_TEAM" "$STUB_IDENT" ;;
*)
	echo "xcrun stub: unexpected $*" >&2
	exit 1
	;;
esac
STUB
cat >"$st/stubs/codesign" <<'STUB'
#!/bin/sh
display=no v=0
for a; do
	case $a in
	--display) display=yes ;;
	--verbose) v=$((v + 1)) ;;
	--verbose=*) v=${a#--verbose=} ;;
	--* | -) ;;
	-*)
		case $a in *d*) display=yes ;; esac
		n=$(printf '%s' "${a#-}" | tr -cd v)
		v=$((v + ${#n}))
		;;
	esac
	f=$a
done
if [ "$display" = no ]; then
	echo "$f: valid on disk" >&2
	exit 0
fi
sig=${STUB_SIGNATURE:-devid}
if [ "$sig" = none ]; then
	echo "$f: code object is not signed at all" >&2
	exit 1
fi
echo "Executable=$f" >&2
[ "$v" -ge 1 ] || exit 0
echo "Identifier=stub.identifier" >&2
echo "Format=generic" >&2
if [ "$sig" = adhoc ]; then
	echo "Signature=adhoc" >&2
	echo "TeamIdentifier=not set" >&2
	exit 0
fi
if [ "$v" -ge 2 ]; then
	case $sig in
	devid)
		echo "Authority=$STUB_IDENT" >&2
		echo "Authority=Developer ID Certification Authority" >&2
		;;
	*)
		echo "Authority=Apple Development: ${STUB_IDENT#*: }" >&2
		echo "Authority=Apple Worldwide Developer Relations Certification Authority" >&2
		;;
	esac
	echo "Authority=Apple Root CA" >&2
fi
echo "TeamIdentifier=$STUB_TEAM" >&2
STUB
chmod +x "$st/stubs/spctl" "$st/stubs/xcrun" "$st/stubs/codesign"

# stubbed <task> [VAR=value…]: runs the task in $st with the stubs (by
# default Gatekeeper on, accepting a notarized Developer ID); sets $out and
# $code, and fails when the output names the stubbed signer or team.
stubbed() {
	t=$1
	shift
	set +e
	out=$(env PATH="$st/stubs:$PATH" STUB_IDENT="$ident" STUB_TEAM="$ident_team" \
		STUB_GK_OUT='assessments enabled' STUB_VERDICT=accepted STUB_SOURCE='Notarized Developer ID' \
		"$@" task -d "$st" "$t" VERSION=9.9.9 2>&1)
	code=$?
	set -e
	case $out in
	*"$ident_name"* | *"$ident_team"*) fail "task $t ($*) printed the signer's name or Team ID:
$out" ;;
	esac
}

# gatekeeper <pass|fail> <what> [VAR=value…]: verify:release with the
# stubs. A failure must come from the Gatekeeper helper, so no later check
# can be what caught it.
gatekeeper() {
	want=$1 what=$2
	shift 2
	stubbed verify:release "$@"
	if [ "$want" = pass ]; then
		[ "$code" = 0 ] || fail "verify:release, $what: exit status $code, want 0:
$out"
		return 0
	fi
	[ "$code" != 0 ] || fail "verify:release, $what: exit status 0, want a failure:
$out"
	case $out in
	*'Failed to run task "verify:gatekeeper"'*) ;;
	*) fail "verify:release, $what: did not fail in verify:gatekeeper:
$out" ;;
	esac
}

gatekeeper pass "enabled, notarized"
case $out in *"source=Notarized Developer ID"*) ;; *) fail "verify:release does not print the verdict's source:
$out" ;; esac
gatekeeper fail "enabled, Developer ID but not notarized" STUB_SOURCE='Developer ID'
gatekeeper fail "enabled, rejected" STUB_VERDICT=rejected STUB_SOURCE='Unnotarized Developer ID'
gatekeeper fail "enabled, but spctl --status exits 1" STUB_GK_CODE=1
gatekeeper pass "disabled, stapled ticket valid" STUB_GK_OUT='assessments disabled'
case $out in *"Gatekeeper assessments are disabled"*) ;; *) fail "verify:release with assessments disabled does not warn:
$out" ;; esac
gatekeeper pass "disabled (spctl --status exits 1), stapled ticket valid" STUB_GK_OUT='assessments disabled' STUB_GK_CODE=1
gatekeeper fail "disabled, no stapled ticket" STUB_GK_OUT='assessments disabled' STUB_STAPLER_CODE=65
gatekeeper fail "spctl --status error" STUB_GK_OUT='spctl: internal error' STUB_GK_CODE=1
gatekeeper fail "spctl --status fails silently" STUB_GK_OUT= STUB_GK_CODE=1
gatekeeper fail "unknown spctl --status answer" STUB_GK_OUT='assessments maybe'

# The stubs themselves: no certificate chain below verbosity 2, no origin=
# below -vv, so a Taskfile that asks for too little cannot pass.
stub_out=$(env STUB_IDENT="$ident" STUB_TEAM="$ident_team" "$st/stubs/codesign" -dv "$st/bin/Choix.app" 2>&1)
case $stub_out in *Authority=*) fail "the codesign stub prints Authority= at -dv, which the real codesign does not" ;; esac
stub_out=$(env STUB_IDENT="$ident" STUB_TEAM="$ident_team" "$st/stubs/codesign" -dvv "$st/bin/Choix.app" 2>&1)
case $stub_out in *"Authority=$ident"*) ;; *) fail "the codesign stub prints no Authority= at -dvv" ;; esac
stub_out=$(env STUB_IDENT="$ident" STUB_VERDICT=accepted STUB_SOURCE=x "$st/stubs/spctl" --assess --type exec -v "$st/bin/Choix.app" 2>&1)
case $stub_out in *origin=*) fail "the spctl stub prints origin= at -v, which the real spctl does not" ;; esac

# notarize and notarize:dmg: a Developer ID signed file passes the
# precondition and, with an accepted submission, is notarized and stapled.
for t in notarize notarize:dmg; do
	stubbed "$t" CHOIX_NOTARY_PROFILE=stub-profile STUB_NOTARY_STATUS=Accepted
	[ "$code" = 0 ] || fail "$t of a Developer ID signed file with an accepted submission: exit status $code, want 0:
$out"
	case $out in *'The staple and validate action worked!'*) ;; *) fail "$t did not staple:
$out" ;; esac
done

# refused <task> <file> <msg> <signature>: the precondition refuses a file
# without a Developer ID authority, before any command (nothing submitted).
refused() {
	t=$1 file=$2 msg=$3 sig=$4
	stubbed "$t" CHOIX_NOTARY_PROFILE=stub-profile STUB_NOTARY_STATUS=Accepted STUB_SIGNATURE="$sig"
	[ "$code" != 0 ] || fail "$t of a $sig signed $file exited 0:
$out"
	case $out in *"$file is not signed with a Developer ID; $msg"*) ;; *) fail "$t of a $sig signed $file: no refusal message:
$out" ;; esac
	while IFS= read -r line; do
		case $line in "task: ["*) fail "$t of a $sig signed $file ran a command: $line" ;; esac
	done <<EOF
$out
EOF
}
for sig in adhoc none other; do
	refused notarize bin/Choix.app 'run task sign:developer-id first' "$sig"
	refused notarize:dmg bin/Choix-9.9.9-arm64.dmg 'run task dmg with CHOIX_SIGN_IDENTITY set' "$sig"
done

# A rejected notarization prints the submission and the notary log, which
# both carry the signer in the stubs: stubbed() checks they are redacted.
stubbed notarize:dmg CHOIX_NOTARY_PROFILE=stub-profile
[ "$code" != 0 ] || fail "notarize:dmg with an Invalid submission exited 0:
$out"
case $out in *'"issues"'*) ;; *) fail "notarize:dmg did not print the notary log:
$out" ;; esac

# --- 6. A fresh checkout: the frontend:bootstrap dependency.
command -v go >/dev/null 2>&1 || fail "go is not installed"
command -v golangci-lint >/dev/null 2>&1 || fail "golangci-lint is not installed"
command -v wails3 >/dev/null 2>&1 || fail "wails3 is not on PATH (task tools)"

git -C "$root" worktree add --detach --quiet "$wt" HEAD || fail "git worktree add failed"
cp "$taskfile" "$wt/Taskfile.yml"
[ ! -e "$wt/frontend/dist" ] || fail "fresh checkout already has frontend/dist"

task -d "$wt" lint || fail "task lint failed in a fresh checkout"
task -d "$wt" bindings:check || fail "task bindings:check failed in a fresh checkout"
task -d "$wt" test -- -run TestNone || fail "task test -- -run TestNone failed in a fresh checkout"
cmp -s "$wt/frontend/dist/index.html" "$wt/build/frontend-stub/index.html" ||
	fail "frontend:bootstrap did not copy the stub page to frontend/dist/index.html"
[ ! -e "$wt/internal/ui" ] || fail "a task wrote under v1's internal/ui, removed with the v1 shell"

echo ok
