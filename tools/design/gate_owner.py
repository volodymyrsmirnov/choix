"""The owner's commands of the design gate: init, approve (and approve --retire), confirm-no-ui.

gate.py imports this module only when one of those subcommands runs, so a
module that imports gate (the hooks) never loads a writer. _write is the only
function that writes docs/design/approvals.json and docs/design/approvers.txt
(write_ledger and write_approvers call it), and it refuses, before anything
else, unless stdin and stdout are terminals and neither CLAUDECODE nor
CLAUDE_CODE_ENTRYPOINT is set: an agent never writes the ledger, even by
importing this module and calling a helper.
"""
import argparse
import datetime
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request
from typing import Dict, List, Optional, Tuple

import gate
from gate import APPROVERS, LEDGER, LEDGER_KEYS, RETIRED, SNAPSHOT, GateError

AGENT_VARS = ("CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT")
REBUILD = "rebuild with `task design:build` and restart `task design:serve`"


def is_terminal() -> bool:
    try:
        return sys.stdin.isatty() and sys.stdout.isatty()
    except (AttributeError, ValueError):
        return False


def ask(prompt: str) -> str:
    try:
        return input(prompt + " ")
    except EOFError:
        return ""


def confirm(prompt: str) -> bool:
    return ask(prompt).strip().lower() in ("y", "yes")


def fetch_json(url: str):
    """GET a JSON document, never through a proxy (the review build is on localhost)."""
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(url, timeout=10) as r:
        return json.loads(r.read().decode())


def run_snapshot_check(root: str, spec: str) -> int:
    try:
        return subprocess.run(["node", "tools/snapshot.mjs", "--check", "--states", spec],
                              cwd=os.path.join(root, "frontend")).returncode
    except FileNotFoundError:
        raise GateError("node is not installed")


def now() -> str:
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


# --- Is this process a descendant of the Claude Code CLI? ---------------------------------------
#
# An agent's shell descends from the Claude Code CLI: an executable named exactly `claude` (the native build,
# e.g. ~/Library/Application Support/Claude/claude-code/<version>/…/MacOS/claude, or ~/.local/bin/claude), or
# node running @anthropic-ai/claude-code's cli.js. The desktop app's Terminal panel, where the owner may run
# `task design:approve`, descends from `Claude Helper` → `Claude` (Claude.app), never from the CLI; those names
# differ in case and are not matched. Anything unexpected (no ps, a parse error) means "unknown": no refusal.

CLI_ARGV = re.compile(r"@anthropic-ai/claude-code/|/claude-code/cli\.m?js(\s|$)")

ProcessTable = Dict[int, Tuple[int, str, str]]  # pid -> (ppid, executable, argv)


def process_table() -> Optional[ProcessTable]:
    """Every process's parent, executable and argv from `ps`; None when ps fails."""
    try:
        comm = subprocess.run(["ps", "-axo", "pid=,ppid=,comm="], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                              timeout=5, check=True).stdout.decode(errors="replace")
        args = subprocess.run(["ps", "-axo", "pid=,args="], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                              timeout=5, check=True).stdout.decode(errors="replace")
    except (OSError, subprocess.SubprocessError):
        return None
    argv: Dict[int, str] = {}
    for line in args.splitlines():
        parts = line.strip().split(None, 1)
        if parts and parts[0].isdigit():
            argv[int(parts[0])] = parts[1] if len(parts) > 1 else ""
    table: ProcessTable = {}
    for line in comm.splitlines():
        parts = line.strip().split(None, 2)
        if len(parts) == 3 and parts[0].isdigit() and parts[1].isdigit():
            pid = int(parts[0])
            table[pid] = (int(parts[1]), parts[2], argv.get(pid, ""))
    return table


def ancestor_chain(pid: int, table: ProcessTable) -> List[Tuple[str, str]]:
    """(executable, argv) of pid and each of its ancestors, up to pid 1."""
    out = []
    seen = set()
    while pid > 1 and pid in table and pid not in seen and len(out) < 64:
        seen.add(pid)
        ppid, comm, args = table[pid]
        out.append((comm, args))
        pid = ppid
    return out


def claude_code_in(chain: List[Tuple[str, str]]) -> Optional[str]:
    """The first process in the chain that is the Claude Code CLI, or None."""
    for comm, args in chain:
        if os.path.basename(comm) == "claude" or CLI_ARGV.search(args):
            return comm
        # A node install may set process.title to `claude`: ps then shows `claude …` as the argv. Only for node,
        # only an exact `claude` first word (never `Claude`, `Claude Helper` or a path through Claude.app).
        argv0 = args.split(" ", 1)[0] if args else ""
        if os.path.basename(comm) == "node" and os.path.basename(argv0) == "claude":
            return "{} ({})".format(comm, argv0)
    return None


def agent_ancestor() -> Optional[str]:
    try:
        table = process_table()
        return claude_code_in(ancestor_chain(os.getpid(), table)) if table else None
    except Exception:  # unknown: never refuse the owner on a failure of this check
        return None


def require_human(what: str) -> None:
    if not is_terminal() or any(v in os.environ for v in AGENT_VARS):
        raise GateError("{} needs a terminal: only the owner runs `task design:init`, `task design:approve` and "
                        "`task design:confirm-no-ui`, in their own shell; agents never do".format(what))
    cli = agent_ancestor()
    if cli:
        raise GateError("{} needs a terminal: this process runs under the Claude Code CLI ({}), so an agent started "
                        "it; only the owner runs these commands, in their own shell (the desktop app's Terminal panel "
                        "is fine)".format(what, cli))


def format_ledger(ledger: dict) -> str:
    """approvals.json: one entry per line, each list sorted."""
    def block(key: str, keys, sort_key) -> str:
        entries = ledger[key]
        if not entries:
            return '  "{}": []'.format(key)
        rows = [json.dumps({k: e.get(k) for k in keys}, ensure_ascii=False) for e in sorted(entries, key=sort_key)]
        return '  "{}": [\n    {}\n  ]'.format(key, ",\n    ".join(rows))
    return "{\n" + ",\n".join([
        block("states", ("state", "label", "hash", "by", "at", "note"), lambda e: e["state"]),
        block("retired", ("state", "by", "at"), lambda e: e["state"]),
        block("noUi", ("branch", "files", "by", "at"), lambda e: (e["branch"], e["files"])),
    ]) + "\n}\n"


def _write(root: str, rel: str, text: str) -> None:
    require_human("writing " + rel)
    path = gate.rel_file(root, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        f.write(text)
    os.replace(tmp, path)


def write_ledger(root: str, ledger: dict) -> None:
    require_human("writing " + LEDGER)
    _write(root, LEDGER, format_ledger(ledger))


def write_approvers(root: str, emails: List[str]) -> None:
    require_human("writing " + APPROVERS)
    _write(root, APPROVERS, "# Design approvers: one git email per line.\n" + "".join(e + "\n" for e in emails))


def git_email(root: str) -> str:
    email = (gate.git_try(root, "config", "user.email") or "").strip()
    if not email:
        raise GateError("git config user.email is not set")
    return email


def require_approver(root: str) -> str:
    approvers = gate.read_approvers(root)
    if approvers is None:
        raise GateError("{} does not exist: the owner runs `task design:init` first".format(APPROVERS))
    email = git_email(root)
    if email.lower() not in approvers:
        raise GateError("{} is not in {}".format(email, APPROVERS))
    return email


def cmd_init(args: argparse.Namespace) -> int:
    require_human("init")
    root = gate.repo_root()
    email = git_email(root)
    has_approvers = gate.read_approvers(root) is not None
    ledger_text = gate.read_text(root, LEDGER)
    if has_approvers and ledger_text is not None:
        raise GateError("already initialised: {} and {} exist".format(APPROVERS, LEDGER))
    if has_approvers:
        print("{} exists. This writes an empty approval ledger in {}.".format(APPROVERS, LEDGER))
    elif ledger_text is None:
        print("This writes {} with the approver {}, and an empty approval ledger in {}.".format(APPROVERS, email, LEDGER))
    else:
        print("This writes {} with the approver {}. {} already exists.".format(APPROVERS, email, LEDGER))
    if not confirm("Initialise the design approvals? [y/N]"):
        print("Nothing written.")
        return 1
    if not has_approvers and ledger_text is not None:
        existing = gate.load_ledger(root)
        count = sum(len(existing[k]) for k in LEDGER_KEYS)
        if count:
            print("{} holds {} entr{} ({} approvals, {} retirements, {} no-UI confirmations) that no listed approver "
                  "made.".format(LEDGER, count, "y" if count == 1 else "ies", len(existing["states"]),
                                 len(existing["retired"]), len(existing["noUi"])))
            if not confirm("Adopt these {} entries as valid under the approver {}? [y/N]".format(count, email)):
                print("Nothing written.")
                return 1
    if not has_approvers:
        write_approvers(root, [email])
    if ledger_text is None:
        write_ledger(root, {k: [] for k in LEDGER_KEYS})
    print("Done. Commit {} with the branch.".format(" and ".join(
        p for p, new in ((APPROVERS, not has_approvers), (LEDGER, ledger_text is None)) if new)))
    return 0


def check_build(root: str, current: str) -> None:
    """The build being served at reviewUrl reports this source's digest."""
    review = gate.load_gate(root).get("reviewUrl", "").rstrip("/")
    url = review + "/__build.json"
    not_a_build = ("the server at {} is not serving a prototype build ({}): build and serve the prototype with "
                   "`task design:build && task design:serve`")
    try:
        build = fetch_json(url)
    except urllib.error.HTTPError as e:
        raise GateError(not_a_build.format(review, "{} answered HTTP {}".format(url, e.code)))
    except ValueError as e:  # not JSON (json.JSONDecodeError, UnicodeDecodeError)
        raise GateError(not_a_build.format(review, "{} is not JSON: {}".format(url, e)))
    except Exception as e:
        raise GateError("cannot reach {} ({}): build and serve the prototype with "
                        "`task design:build && task design:serve`".format(url, e))
    if not isinstance(build, dict):
        raise GateError(not_a_build.format(review, "{} is not a build description".format(url)))
    served = build.get("sourceDigest")
    if not served:
        raise GateError("the build being served at {} has no digest; {}".format(review, REBUILD))
    if served != current:
        raise GateError("the build being served at {} was not built from this source (it reports {}…, the source "
                        "is {}…); {}".format(review, str(served)[:12], current[:12], REBUILD))


def cmd_approve(args: argparse.Namespace) -> int:
    require_human("approve")
    root = gate.repo_root()
    if args.retire is not None and args.states:
        raise GateError("pass either states to approve or --retire N…, not both")
    if args.retire is None and not args.states:
        raise GateError("name the states: `task design:approve -- 100-122 300` (or `all`)")
    email = require_approver(root)
    snap, source = gate.parse_snapshot(gate.read_text(root, SNAPSHOT), SNAPSHOT)
    current = gate.digest(root)
    if source != current:
        raise GateError("{} is not current (its sourceDigest is not this source's digest): run "
                        "`task design:snapshot` first".format(SNAPSHOT))
    try:
        if args.retire is not None:
            numbers = gate.parse_numbers(" ".join(args.retire))
        elif [t.lower() for t in args.states] == ["all"]:
            numbers = sorted(snap)
        else:
            numbers = gate.parse_numbers(" ".join(args.states))
    except ValueError as e:
        raise GateError(str(e))
    ledger = gate.load_ledger(root)
    if args.retire is not None:
        return approve_retire(root, email, numbers, snap, ledger)
    unknown = [n for n in numbers if n not in snap]
    if unknown:
        raise GateError("not registered: {}".format(", ".join("#{}".format(n) for n in unknown)))
    check_build(root, current)
    spec = gate.format_ranges(numbers, ",")
    print("Re-rendering {} state(s) to confirm their fingerprints (snapshot --check --states {}) …".format(len(numbers), spec))
    if run_snapshot_check(root, spec) != 0:
        raise GateError("snapshot --check --states {} failed: the rendered states differ from {}".format(spec, SNAPSHOT))
    review = gate.load_gate(root).get("reviewUrl", "").rstrip("/")
    for n in numbers:
        print("#{} {} — {}/#s{}\n    {}".format(n, gate.full_label(n, snap[n].get("label", "")), review, n, snap[n]["hash"]))
    if not confirm("Approve these {} states? [y/N]".format(len(numbers))):
        print("Nothing written.")
        return 1
    entries = {e["state"]: e for e in ledger["states"]}
    at = now()
    for n in numbers:
        entries[n] = {"state": n, "label": snap[n].get("label", ""), "hash": snap[n]["hash"], "by": email,
                      "at": at, "note": args.note or ""}
    ledger["states"] = list(entries.values())
    write_ledger(root, ledger)
    print("Recorded {} approval(s) in {}. Commit it with the branch.".format(len(numbers), LEDGER))
    return 0


def approve_retire(root: str, email: str, numbers: List[int], snap: Dict[int, dict], ledger: dict) -> int:
    registered = [n for n in numbers if n in snap]
    if registered:
        raise GateError("still registered, so not retired: {}".format(", ".join("#{}".format(n) for n in registered)))
    retired = gate.parse_retired(gate.read_text(root, RETIRED), RETIRED)
    missing = [n for n in numbers if n not in retired]
    if missing:
        raise GateError("add {} to {} first".format(", ".join("#{}".format(n) for n in missing), RETIRED))
    for n in numbers:
        print("#{} — retired".format(n))
    if not confirm("Record these {} retirements? [y/N]".format(len(numbers))):
        print("Nothing written.")
        return 1
    entries = {e["state"]: e for e in ledger["retired"]}
    at = now()
    for n in numbers:
        entries[n] = {"state": n, "by": email, "at": at}
    ledger["retired"] = list(entries.values())
    write_ledger(root, ledger)
    print("Recorded {} retirement(s) in {}. Commit it with the branch.".format(len(numbers), LEDGER))
    return 0


def cmd_confirm_no_ui(args: argparse.Namespace) -> int:
    """Hashes the gated paths that differ between the base and HEAD, so CI computes the same hash."""
    require_human("confirm-no-ui")
    root = gate.repo_root()
    email = require_approver(root)
    branch = gate.current_branch(root, args.branch)
    if not branch:
        raise GateError("cannot tell the branch: pass --branch")
    base: Optional[str] = gate.resolve_base(root, args.base)
    if base is None:
        raise GateError("cannot find the base (origin/main or main): pass --base")
    g = gate.load_gate(root)
    dirty = [p for p in gate.uncommitted_paths(root) if gate.is_gated(p, g)]
    if dirty:
        raise GateError("commit the gated changes first; uncommitted or untracked: {}".format(", ".join(dirty)))
    gated = [p for p in gate.diff_names(root, base, "HEAD") if gate.is_gated(p, g)]
    if not gated:
        raise GateError("no gated file changed on {}: nothing to confirm".format(branch))
    ledger = gate.load_ledger(root)
    print("{} changes these gated files:".format(branch))
    for p in gated:
        print("  " + p)
    if not confirm("Confirm that none of them changes the UI? [y/N]"):
        print("Nothing written.")
        return 1
    ledger["noUi"] = [e for e in ledger["noUi"] if e["branch"] != branch]
    ledger["noUi"].append({"branch": branch, "files": gate.no_ui_files_hash(root, gated), "by": email, "at": now()})
    write_ledger(root, ledger)
    print("Recorded the confirmation in {}. Commit it with the branch; it holds until a gated file changes.".format(LEDGER))
    return 0
