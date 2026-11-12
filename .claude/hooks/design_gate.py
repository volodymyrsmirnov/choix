#!/usr/bin/env python3
"""Claude Code hooks of the design gate. Standard library only, Python >= 3.9.

    design_gate.py pre-edit       PreToolUse  Edit|Write|MultiEdit|NotebookEdit
    design_gate.py pre-bash       PreToolUse  Bash, mcp__terminal__.*
    design_gate.py post-bash      PostToolUse Bash, mcp__terminal__.*
    design_gate.py stop           Stop
    design_gate.py subagent-stop  SubagentStop

Each reads the hook's JSON on stdin. Exit 0 allows; exit 2 blocks, with the
reason on stderr (Claude reads it). Any internal error allows: exit 0 with a
warning on stderr and as a `systemMessage` the user sees. A warning that goes
with an allowed stop is a `systemMessage` too.

The logic of the gate is tools/design/gate.py, imported from the checkout this
hook lives in; the repository a hook acts on is the one holding the edited
path (`git -C <its directory> rev-parse --show-toplevel`), else the hook's
`cwd`, so a nested worktree under .claude/worktrees/ is its own repository. A
repository without docs/design/gate.json is left alone.

Session state lives in `<git rev-parse --git-path choix-design>/session-<id>.json`
(per worktree, outside the working tree). Sub-agents and parallel tool calls
share a session, so every read-modify-write holds an exclusive flock. It holds
the before-state of each running shell command (keyed by tool_use_id), the
open violations, which gated and digest-input paths this session changed, and
the stop hook's count of identical blocks.
"""
import datetime
import fcntl
import glob
import hashlib
import json
import os
import re
import subprocess
import sys
import time
from typing import Dict, Iterable, List, Optional, Set, Tuple

HERE = os.path.dirname(os.path.abspath(__file__))
TOOLS = os.path.normpath(os.path.join(HERE, "..", "..", "tools", "design"))

LINK_LIMIT = 30          # states listed as links in one message
VALVE = 3                # identical consecutive stop blocks before the stop is allowed
PENDING_TTL = 24 * 3600  # a before-state without its post-bash (a denied command) is dropped after a day
SESSION_TTL = 14 * 24 * 3600  # session files untouched this long are deleted
APP_PREFIXES = ("frontend/", "internal/app/")  # blocked when gate.json cannot be read
HUMAN_TASKS = ("design:init", "design:approve", "design:confirm-no-ui")
HUMAN_GATE_COMMANDS = ("init", "approve", "confirm-no-ui")

gate = None  # tools/design/gate.py, loaded in main()
messages: List[str] = []  # shown to the user as one systemMessage when the hook allows


class Block(Exception):
    """Exit 2 with this message on stderr."""


def warn(text: str) -> None:
    print("design gate: warning: " + text, file=sys.stderr)
    messages.append("design gate: " + text)


# --- git and repositories -------------------------------------------------------


def git_out(root: str, *args: str, data: Optional[bytes] = None) -> Optional[str]:
    try:
        out = subprocess.run(["git", "-C", root, *args], input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    except OSError:
        return None
    return out.stdout.decode(errors="surrogateescape") if out.returncode == 0 else None


_roots: Dict[str, Optional[str]] = {}


def root_of(path: str) -> Optional[str]:
    """The top level of the repository holding path (a file or directory that may not exist yet)."""
    d = path if os.path.isdir(path) else os.path.dirname(path)
    while d and not os.path.isdir(d):
        parent = os.path.dirname(d)
        if parent == d:
            return None
        d = parent
    if not d:
        return None
    if d not in _roots:
        out = git_out(d, "rev-parse", "--show-toplevel")
        _roots[d] = out.strip() if out and out.strip() else None
    return _roots[d]


def has_gate(root: Optional[str]) -> bool:
    return bool(root) and os.path.isfile(os.path.join(root, "docs", "design", "gate.json"))


def gated_root(path: str, fallback: Optional[str] = None) -> Optional[str]:
    root = root_of(path) or (root_of(fallback) if fallback else None)
    return root if has_gate(root) else None


def ledger_files() -> Tuple[str, str]:
    return gate.LEDGER, gate.APPROVERS


def is_ledger(rel: str, is_dir: bool = False) -> bool:
    low = rel.lower().strip("/")
    for f in ledger_files():
        if low == f.lower() or (is_dir and (not low or f.lower().startswith(low + "/"))):
            return True
    return False


def worktree_blobs(root: str, paths: Iterable[str], write: bool = False) -> Dict[str, str]:
    """{path: blob id of the working-tree file, or `absent`}; with write, the blobs go into the object store."""
    paths = sorted(set(paths))
    out = {p: "absent" for p in paths}
    present = [p for p in paths if os.path.isfile(gate.rel_file(root, p))]
    if present:
        args = ["hash-object"] + (["-w"] if write else []) + ["--stdin-paths"]
        res = git_out(root, *args, data="".join(p + "\n" for p in present).encode())
        if res is None:
            raise gate.GateError("git hash-object failed")
        for p, sha in zip(present, res.split()):
            out[p] = sha
    return out


def index_blob(root: str, path: str) -> str:
    entry = gate.index_entry(root, path)
    return entry[1] if entry else "absent"


def head_sha(root: str) -> Optional[str]:
    out = git_out(root, "rev-parse", "-q", "--verify", "HEAD^{commit}")
    return out.strip() if out else None


def ref_tips(root: str) -> List[str]:
    out = git_out(root, "for-each-ref", "--format=%(objectname)") or ""
    tips = {t for t in out.split() if t}
    h = head_sha(root)
    if h:
        tips.add(h)
    return sorted(tips)


def origin_main(root: str) -> List[str]:
    out = git_out(root, "rev-parse", "-q", "--verify", "refs/remotes/origin/main^{commit}")
    return [out.strip()] if out else []


def blob_known(root: str, path: str, blob: str, revs: List[str]) -> bool:
    """The path held this blob in some commit reachable from revs (commits that existed before the
    command) or from origin/main: a checkout, merge, rebase or amend of committed code."""
    revs = sorted(set(revs) | set(origin_main(root)))
    if not revs:
        return False
    out = git_out(root, "log", "--format=", "--raw", "--no-abbrev", "--no-renames", "-m", *revs, "--",
                  gate.literal(path)) or ""
    for line in out.splitlines():
        if line.startswith(":"):
            parts = line.split("\t", 1)[0].split()
            if len(parts) >= 4 and blob in (parts[2], parts[3]):
                return True
    return False


def existed_before(root: str, commit: Optional[str], tips: List[str]) -> bool:
    if not commit:
        return True
    if not tips:
        return False
    out = git_out(root, "rev-list", "-n1", commit, "--not", *tips)
    return out is not None and not out.strip()


def new_commits(root: str, head: Optional[str], tips: List[str]) -> List[str]:
    if not head:
        return []
    out = git_out(root, "rev-list", "--max-count=500", head, *(["--not", *tips] if tips else [])) or ""
    return out.split()


def commit_blobs(root: str, commit: str) -> List[Tuple[str, str]]:
    """(path, new blob) for each file the commit adds or modifies against its first parent."""
    parents = (git_out(root, "rev-list", "--parents", "-n1", commit) or "").split()[1:]
    args = ["diff-tree", "-r", "-z", "--no-renames", "--no-commit-id"]
    args += [parents[0], commit] if parents else ["--root", commit]
    toks = (git_out(root, *args) or "").split("\0")
    out = []
    i = 0
    while i + 1 < len(toks):
        meta, path = toks[i], toks[i + 1]
        i += 2
        parts = meta.lstrip(":").split()
        if len(parts) >= 5 and parts[4][0] in "AMT":
            out.append((path, parts[3]))
    return out


# --- session state --------------------------------------------------------------


def now() -> str:
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")


class Session:
    """The session's state for one worktree, read and written under an exclusive lock."""

    def __init__(self, root: str, session_id: str = "", path: Optional[str] = None):
        self.root = root
        d = gate.session_dir(root)
        os.makedirs(d, exist_ok=True)
        if path is None:
            path = os.path.join(d, Session.file_name(session_id))
        self.path = path
        self.lock_path = path[:-len(".json")] + ".lock"
        prune_sessions(d, keep=(self.path, self.lock_path))

    @staticmethod
    def file_name(session_id: str) -> str:
        sid = re.sub(r"[^A-Za-z0-9_.-]", "_", session_id or "") or "unknown"
        if len(sid) > 100:
            sid = hashlib.sha256(sid.encode()).hexdigest()
        return "session-{}.json".format(sid)

    def __enter__(self) -> dict:
        self.lock = open(self.lock_path, "a")
        fcntl.flock(self.lock, fcntl.LOCK_EX)
        try:
            with open(self.path, encoding="utf-8") as f:
                state = json.load(f)
            if not isinstance(state, dict):
                state = {}
        except (OSError, ValueError):
            state = {}
        state.setdefault("version", 1)
        state.setdefault("seq", 0)
        for key in ("pending", "violations", "stops", "notes"):
            if not isinstance(state.get(key), dict):
                state[key] = {}
        if not isinstance(state.get("seq"), int) or isinstance(state.get("seq"), bool):
            state["seq"] = 0
        state["pending"] = {k: v for k, v in state["pending"].items() if valid_record(v)}
        changed = state.get("changed")
        if not isinstance(changed, dict):
            changed = state["changed"] = {}
        for key in ("gated", "digest"):
            if not isinstance(changed.get(key), list):
                changed[key] = []
        self.state = state
        return state

    def __exit__(self, exc_type, exc, tb) -> None:
        try:
            if exc_type is None or issubclass(exc_type, Block):
                tmp = self.path + ".tmp"
                with open(tmp, "w", encoding="utf-8") as f:
                    json.dump(self.state, f, indent=1, sort_keys=True)
                os.replace(tmp, self.path)
        finally:
            fcntl.flock(self.lock, fcntl.LOCK_UN)
            self.lock.close()


def valid_record(rec) -> bool:
    """A before-state the hooks can use; anything else (a hand-edited or older file) is dropped."""
    def number(x) -> bool:
        return isinstance(x, (int, float)) and not isinstance(x, bool)
    return (isinstance(rec, dict) and isinstance(rec.get("seq"), int) and not isinstance(rec.get("seq"), bool)
            and number(rec.get("t", 0)) and isinstance(rec.get("tips", []), list)
            and isinstance(rec.get("dirty", {}), dict) and isinstance(rec.get("head"), (str, type(None))))


def prune_sessions(d: str, keep: Iterable[str] = ()) -> None:
    cutoff = time.time() - SESSION_TTL
    keep = set(keep)
    try:
        names = os.listdir(d)
    except OSError:
        return
    for name in names:
        path = os.path.join(d, name)
        if name.startswith("session-") and path not in keep:
            try:
                if os.path.getmtime(path) < cutoff:
                    os.remove(path)
            except OSError:
                pass


def other_sessions(root: str, own: str) -> List[str]:
    """Session files of this worktree other than own that hold open violations."""
    out = []
    for path, data in gate.read_sessions(root):
        if os.path.abspath(path) != os.path.abspath(own) and data.get("violations"):
            out.append(path)
    return out


def snapshot_current(root: str, current_digest: str) -> bool:
    try:
        return gate.parse_snapshot(gate.read_text(root, gate.SNAPSHOT), gate.SNAPSHOT)[1] == current_digest
    except gate.GateError:
        return False


def note_changed(state: dict, key: str, paths: Iterable[str]) -> None:
    have = state["changed"][key]
    for p in paths:
        if p not in have:
            have.append(p)
    del have[200:]


# --- messages -------------------------------------------------------------------

ADVICE = {
    "unapproved": "ask the owner to review the states listed above in the review build (`task design:build && task "
                  "design:serve`) and approve them in their own terminal; until then, leave the gated files alone",
    "stale-snapshot": "run `task design:snapshot`: docs/design/state-hashes.json no longer matches the prototype's "
                      "source",
    "no-change-file": "add docs/design/changes/<branch-slug>.md with `branch:`, `states:`, `retired:` and `ui:` "
                      "front matter (see docs/design/changes/ for an example)",
    "bad-change-file": "fix the change file's front matter as the finding says",
    "unretired": "add each removed number to docs/design/retired.json and to the change file's `retired:`; the owner "
                 "records the retirement with `task design:approve -- --retire N`",
    "bad-ledger": "tell the owner: the ledger holds entries the gate ignores, and only the owner edits it",
    "no-base": "fetch the base: `git fetch origin main`",
    "no-branch": "check out a branch: the gate finds the change file by the branch's name",
    "ui-false-but-prototype-changed": "set `ui: true` in the change file, or undo the prototype change",
    "bad-snapshot": "fix docs/design/state-hashes.json (`task design:snapshot` writes it)",
    "bad-retired": "fix docs/design/retired.json: a JSON list of retired state numbers",
    "approvers-changed": "tell the owner: only they change docs/design/approvers.txt",
}
NOT_OWNER = ("Do not try to approve states, and do not write docs/design/approvals.json or docs/design/approvers.txt: "
             "only the owner does, in their own terminal.")


def state_links(root: str, numbers: Iterable[int], statuses: Dict[str, str]) -> List[str]:
    numbers = sorted(set(numbers))
    if not numbers:
        return []
    try:
        labels = {n: str(s.get("label", "")) for n, s in
                  gate.snapshot_states(gate.read_text(root, gate.SNAPSHOT), gate.SNAPSHOT).items()}
    except gate.GateError:
        labels = {}
    try:
        review = gate.load_gate(root).get("reviewUrl", "").rstrip("/")
    except gate.GateError:
        review = ""
    lines = ["- {} — {}".format(gate.md_link(n, labels.get(n, ""), review), statuses.get(str(n), "new"))
             for n in numbers[:LINK_LIMIT]]
    if len(numbers) > LINK_LIMIT:
        rest = numbers[LINK_LIMIT:]
        lines.append("- … and {} more: {} (`python3 tools/design/gate.py status` lists every link)".format(
            len(rest), gate.format_ranges(rest)))
    return lines


def pending_numbers(result: dict) -> List[int]:
    return sorted(int(n) for n, s in result.get("states", {}).items() if s in ("new", "stale"))


def findings_text(root: str, result: dict) -> List[str]:
    lines = ["Findings of `gate.py check --mode edit`:"]
    lines += ["  - {}: {}".format(f["reason"], f["message"]) for f in result["findings"]]
    unapproved = [n for f in result["findings"] if f["reason"] == "unapproved" for n in f.get("states", [])]
    if unapproved:
        lines += ["", "States awaiting the owner's approval (open each in the review build):"]
        lines += state_links(root, unapproved, result.get("states", {}))
    lines += ["", "What you can do (you are not the owner):"]
    seen = set()
    for f in result["findings"]:
        if f["reason"] not in seen:
            seen.add(f["reason"])
            lines.append("  - {}: {}".format(f["reason"], ADVICE.get(f["reason"], "see the finding above")))
    lines.append("  - {}".format(NOT_OWNER))
    lines.append("  - Meanwhile you may change files the gate does not cover (frontend/src/design/**, "
                 "frontend/prototype/**, tests, the engine), or stop and tell the owner what is waiting.")
    return lines


def gated_block(root: str, what: str, rels: List[str], result: dict) -> str:
    head = ("design gate: blocked. {} would change {}: app UI that the owner's design approvals cover, and the "
            "gate check fails in {}.".format(what, ", ".join(rels), root))
    return "\n".join([head, ""] + findings_text(root, result))


def ledger_block(what: str, rel: str) -> str:
    return ("design gate: blocked. {} would write {}, the owner's approval ledger. Only the owner writes "
            "docs/design/approvals.json and docs/design/approvers.txt, with `task design:init`, `task design:approve` "
            "or `task design:confirm-no-ui` in their own terminal; an agent never does, with any tool or command.\n\n"
            "What you can do: leave the file as it is. If states need approval, tell the owner which "
            "(`python3 tools/design/gate.py status` lists them with review links). Reading the ledger (`cat`, "
            "`git diff`, `git log`) and committing the owner's changes to it (`git add`, `git commit`) are "
            "allowed.".format(what, rel))


def human_block(which: str) -> str:
    return ("design gate: blocked. This command runs `{}`, one of the owner's commands (`task design:init`, "
            "`task design:approve`, `task design:confirm-no-ui`). Creating the approver list, approving states and "
            "confirming that a branch changes no UI are the owner's decisions: they run these in their own "
            "terminal, never through an agent, and never through the Terminal panel on their behalf.\n\n"
            "What you can do: tell the owner which states await approval (`python3 tools/design/gate.py status` "
            "lists them with review links) and ask them to run `task design:approve -- <numbers>` "
            "themselves.".format(which))


def violations_text(root: str, violations: Dict[str, dict], new: Set[str]) -> str:
    lines = []
    committed = False
    for p in sorted(violations):
        v = violations[p]
        before = "did not exist" if v["before"] == "absent" else "content {}".format(v["before"][:12])
        extra = ""
        if v.get("commit"):
            committed = True
            extra = "; committed in {}".format(gate.describe_commit(root, v["commit"]))
        lines.append("  - {} (before: {}{}){}".format(p, before, extra, "" if p in new else " — still open"))
    fix = ("Put them back: `python3 tools/design/gate.py restore-violations` restores each path to its content from "
           "before, in the working tree and the index (a file that did not exist is deleted; a plain `rm` of such a "
           "file is allowed too).")
    if committed:
        fix += " It then names the commit to amend (`git commit --amend --no-edit`) or undo (`git reset --soft`)."
    return "\n".join([
        "design gate: gated app UI was changed while the gate check fails (the owner has not approved what it "
        "relies on), in {}:".format(root)] + lines + [
        fix,
        "A violation closes once the working tree, the index and HEAD hold the earlier content again, or once the "
        "gate check passes (the owner approves). " + NOT_OWNER])


# --- the shell parser -----------------------------------------------------------

REDIR = re.compile(r"&>>|&>|>>|>\||>&|<<<|<<-|<<|<>|<&|>|<")
OPS = ("&&", "||", ";;", "|&", ";", "&", "|", "(", ")")
WRAPPERS = {"sudo", "env", "command", "nohup", "time", "exec", "nice", "builtin", "caffeinate", "xargs", "timeout",
            "script", "stdbuf"}
WRAPPER_VALUES = {"sudo": {"-u", "-g", "-C", "-D", "-h", "-p", "-U", "-r", "-t"}, "env": {"-u", "-C", "-S", "-P"},
                  "nice": {"-n"}, "timeout": {"-s", "-k", "--signal", "--kill-after"}, "time": {"-f", "-o"},
                  "xargs": {"-I", "-n", "-P", "-L", "-d", "-E", "-s", "-J", "-R"}, "caffeinate": {"-t", "-w"},
                  "script": {"-t", "-T"}, "stdbuf": {"-i", "-o", "-e"}, "exec": {"-a"}}
WRAPPER_POSITIONAL = {"timeout": 1, "script": 1}  # the duration; the typescript file
SHELLS = {"bash", "sh", "zsh", "fish", "dash", "ksh"}


def match_close(s: str, i: int, open_ch: str = "(", close_ch: str = ")") -> int:
    """The index of the bracket closing the one at s[i] (quotes respected); len(s) when unclosed."""
    depth = 0
    q = None
    j = i
    while j < len(s):
        c = s[j]
        if q:
            if c == "\\" and q == '"':
                j += 2
                continue
            if c == q:
                q = None
        elif c in "'\"":
            q = c
        elif c == "\\":
            j += 2
            continue
        elif c == open_ch:
            depth += 1
        elif c == close_ch:
            depth -= 1
            if depth == 0:
                return j
        j += 1
    return len(s)


def lex(s: str) -> Tuple[List[tuple], List[str]]:
    """Shell tokens: ("word", text), ("op", op), ("redir", op, fd); plus the bodies of $(…), `…`, <(…) and >(…)
    to analyse as commands of their own. Quotes are removed; heredoc bodies are skipped."""
    tokens: List[tuple] = []
    subs: List[str] = []
    heredocs: List[Tuple[str, bool]] = []
    word: Optional[List[str]] = None
    expect_delim: Optional[str] = None
    i, n = 0, len(s)

    def flush() -> None:
        nonlocal word, expect_delim
        if word is not None:
            text = "".join(word)
            if expect_delim:
                heredocs.append((text, expect_delim == "<<-"))
                expect_delim = None
            tokens.append(("word", text))
            word = None

    def add(text: str) -> None:
        nonlocal word
        if word is None:
            word = []
        word.append(text)

    while i < n:
        c = s[i]
        if c == "\n":
            flush()
            tokens.append(("op", "\n"))
            i += 1
            while heredocs:
                delim, strip = heredocs.pop(0)
                while i < n:
                    j = s.find("\n", i)
                    line = s[i:] if j < 0 else s[i:j]
                    i = n if j < 0 else j + 1
                    if (line.lstrip("\t") if strip else line) == delim:
                        break
            continue
        if c in " \t\r":
            flush()
            i += 1
            continue
        if c == "#" and word is None:
            j = s.find("\n", i)
            i = n if j < 0 else j
            continue
        if c == "\\":
            if i + 1 < n and s[i + 1] != "\n":
                add(s[i + 1])
            i += 2
            continue
        if c == "'":
            j = s.find("'", i + 1)
            j = n if j < 0 else j
            add(s[i + 1:j])
            i = j + 1
            continue
        if c == '"':
            j = i + 1
            buf = []
            while j < n and s[j] != '"':
                if s[j] == "\\" and j + 1 < n:
                    buf.append(s[j + 1])
                    j += 2
                    continue
                if s.startswith("$(", j) and not s.startswith("$((", j):
                    k = match_close(s, j + 1)
                    subs.append(s[j + 2:k])
                    buf.append("$(…)")
                    j = k + 1
                    continue
                if s[j] == "`":
                    k = s.find("`", j + 1)
                    k = n if k < 0 else k
                    subs.append(s[j + 1:k])
                    j = k + 1
                    continue
                buf.append(s[j])
                j += 1
            add("".join(buf))
            i = j + 1
            continue
        if c == "$" and s.startswith("$(", i) and not s.startswith("$((", i):
            k = match_close(s, i + 1)
            subs.append(s[i + 2:k])
            add("$(…)")
            i = k + 1
            continue
        if c == "`":
            k = s.find("`", i + 1)
            k = n if k < 0 else k
            subs.append(s[i + 1:k])
            add("`…`")
            i = k + 1
            continue
        if c in "<>" and s.startswith("(", i + 1):
            k = match_close(s, i + 1)
            subs.append(s[i + 2:k])
            add("<(…)")
            i = k + 1
            continue
        m = REDIR.match(s, i) if c in "<>&" else None
        if m and (c != "&" or m.group().startswith("&>")):
            fd = None
            if word is not None and "".join(word).isdigit():
                fd = "".join(word)
                word = None
            else:
                flush()
            tokens.append(("redir", m.group(), fd))
            if m.group() in ("<<", "<<-"):
                expect_delim = m.group()
            i = m.end()
            continue
        op = next((o for o in OPS if s.startswith(o, i)), None)
        if op:
            flush()
            tokens.append(("op", op))
            i += len(op)
            continue
        add(c)
        i += 1
    flush()
    return tokens, subs


def segments(tokens: List[tuple]) -> List[dict]:
    segs: List[dict] = []
    cur: dict = {"words": [], "redirs": []}
    pending = None
    for t in tokens:
        if t[0] == "op":
            pending = None
            if cur["words"] or cur["redirs"]:
                segs.append(cur)
            cur = {"words": [], "redirs": []}
        elif t[0] == "redir":
            pending = t
        elif pending is not None:
            cur["redirs"].append((pending[1], pending[2], t[1]))
            pending = None
        else:
            cur["words"].append(t[1])
    if cur["words"] or cur["redirs"]:
        segs.append(cur)
    return segs


class Analysis:
    def __init__(self) -> None:
        self.human: List[str] = []
        self.writes: List[Tuple[str, str, bool]] = []  # (absolute path, kind: write | rm | git, is a directory)
        self.commits: List[Tuple[str, List[str], List[List[str]]]] = []  # (cwd, commit args, preceding `git add` args)
        self.dirs: Set[str] = set()  # directories the command works in
        self.adds: Dict[str, List[List[str]]] = {}
        self.git_ops: List[Tuple[str, str, List[str]]] = []  # (cwd, subcommand, args) that rewrite tree or index


def expand(word: str, cwd: str) -> str:
    """~ and $VARS; $PWD is the command's own directory (not the hook's), $CLAUDE_PROJECT_DIR the session's."""
    project = os.environ.get("CLAUDE_PROJECT_DIR") or cwd
    word = re.sub(r"\$(\{PWD\}|PWD\b)", lambda m: cwd, word)
    word = re.sub(r"\$(\{CLAUDE_PROJECT_DIR\}|CLAUDE_PROJECT_DIR\b)", lambda m: project, word)
    return os.path.expandvars(os.path.expanduser(word))


def resolve(cwd: str, word: str) -> str:
    return os.path.normpath(os.path.join(cwd, expand(word, cwd)))


def paths_of(cwd: str, word: str) -> List[str]:
    full = resolve(cwd, word)
    if re.search(r"[*?\[]", word):
        found = glob.glob(full)
        if found:
            return sorted(found)
    return [full]


def non_options(args: List[str], with_value: Iterable[str] = ()) -> List[str]:
    out = []
    skip = False
    after = False
    takes = set(with_value)
    for a in args:
        if skip:
            skip = False
            continue
        if after:
            out.append(a)
        elif a == "--":
            after = True
        elif a.startswith("-") and len(a) > 1:
            if a in takes:
                skip = True
        else:
            out.append(a)
    return out


def analyze(command: str, cwd: str, out: Optional[Analysis] = None, depth: int = 0) -> Analysis:
    out = out or Analysis()
    if depth > 4:
        return out
    out.dirs.add(cwd)
    tokens, subs = lex(command)
    for sub in subs:
        analyze(sub, cwd, out, depth + 1)
    for seg in segments(tokens):
        for op, fd, target in seg["redirs"]:
            if op in (">", ">>", ">|", "&>", "&>>", "<>") or (op == ">&" and not re.match(r"\A(\d+|-)\Z", target)):
                if not target.startswith("/dev/"):
                    for p in paths_of(cwd, target):
                        out.writes.append((p, "write", False))
        words = seg["words"]
        out.human += human_in(words)
        names = [os.path.basename(w) for w in words]
        for k, n in enumerate(names):
            if n in SHELLS and k + 2 < len(words) and re.match(r"\A-[a-zA-Z]*c[a-zA-Z]*\Z", words[k + 1]):
                analyze(words[k + 2], cwd, out, depth + 1)
        cwd = analyze_words(words, cwd, out, depth)
        out.dirs.add(cwd)
    return out


TASK_INSPECT = {"--summary", "--list", "-l", "--list-all", "-a", "--dry", "-n"}  # task options that run nothing
PY_VALUE_OPTS = {"-W", "-X", "-Q"}
PY_RUNNERS = {("uv", "run"), ("poetry", "run"), ("pipenv", "run"), ("pipx", "run")}
OWNER_IMPORT = re.compile(r"\b(import\s+gate_owner\b|from\s+gate_owner\s+import|import_module\(\s*['\"]gate_owner"
                          r"|__import__\(\s*['\"]gate_owner|run_module\(\s*['\"]gate_owner|run_path\([^)]*gate_owner)")
OWNER_MAIN = re.compile(r"main\(\s*\[\s*['\"](init|approve|confirm-no-ui)['\"]")


def task_inspects_only(args: List[str]) -> bool:
    """`task --summary design:approve` and the like run nothing. Fails towards blocking: before the first `--`,
    the words must be owner targets and at least one standalone token from TASK_INSPECT, and nothing else (no
    other flag, no `=value`, no other task), since task's own option grammar is not modelled here."""
    head = args[:args.index("--")] if "--" in args else args
    return (any(w in TASK_INSPECT for w in head) and any(w in HUMAN_TASKS for w in head)
            and all(w in TASK_INSPECT or w in HUMAN_TASKS for w in head))


def owner_script(path: str, rest: List[str]) -> Optional[str]:
    """`gate_owner.py …` or `gate.py <owner subcommand> …` run as a script."""
    base = os.path.basename(path)
    if base == "gate_owner.py":
        return "gate_owner"
    if base == "gate.py":
        plain = non_options(rest)
        if plain and plain[0] in HUMAN_GATE_COMMANDS:
            return "gate.py " + plain[0]
    return None


def python_owner(args: List[str]) -> Optional[str]:
    """What a python command line runs, when it is an owner command: a script, `-m gate_owner`,
    `-m gate <owner subcommand>`, or `-c` code importing gate_owner or calling gate.main with one."""
    k = 0
    while k < len(args):
        a = args[k]
        if a in ("-m",) or (a.startswith("-m") and len(a) > 2):
            module = a[2:] if len(a) > 2 else (args[k + 1] if k + 1 < len(args) else "")
            rest = args[k + 1:] if len(a) > 2 else args[k + 2:]
            if module == "gate_owner":
                return "gate_owner"
            if module == "gate":
                plain = non_options(rest)
                if plain and plain[0] in HUMAN_GATE_COMMANDS:
                    return "gate.py " + plain[0]
            return None
        if a == "-c":
            code = args[k + 1] if k + 1 < len(args) else ""
            m = OWNER_MAIN.search(code)
            if m:
                return "gate.main([{!r}])".format(m.group(1))
            return "gate_owner" if OWNER_IMPORT.search(code) else None
        if a in PY_VALUE_OPTS:
            k += 2
            continue
        if a.startswith("-"):
            k += 1
            continue
        return owner_script(a, args[k + 1:])
    return None


def human_in(words: List[str]) -> List[str]:
    """The owner's commands in one simple command, behind any wrapper (env, script, timeout, sudo, nice, xargs …):
    `task design:init|approve|confirm-no-ui` (not with --summary, --list or --dry), a python command line that
    runs gate_owner or gate.py with an owner subcommand, and gate_owner.py / gate.py run directly."""
    found = []
    names = [os.path.basename(w) for w in words]
    for k, n in enumerate(names):
        if n in ("task", "go-task"):
            later = words[k + 1:]
            hit = next((w for w in later if w in HUMAN_TASKS), None)
            if hit and not task_inspects_only(later):
                found.append("task " + hit)
        elif n.startswith("python") and re.match(r"\Apython[0-9.]*\Z", n):
            what = python_owner(words[k + 1:])
            if what:
                found.append(what)
        elif k + 1 < len(words) and (n, words[k + 1]) in PY_RUNNERS:
            rest = words[k + 2:]
            what = python_owner(rest[1:]) if rest and re.match(r"\Apython[0-9.]*\Z", os.path.basename(rest[0])) \
                else python_owner(rest)
            if what:
                found.append(what)
    plain = [w for w in words if not re.match(r"\A[A-Za-z_][A-Za-z0-9_]*=", w)]
    if plain:
        what = owner_script(plain[0], plain[1:])
        if what:
            found.append(what)
    return found


def analyze_words(words: List[str], cwd: str, out: Analysis, depth: int) -> str:
    while words and re.match(r"\A[A-Za-z_][A-Za-z0-9_]*=", words[0]):
        words = words[1:]
    while words and os.path.basename(words[0]) in WRAPPERS:
        wrapper = os.path.basename(words[0])
        words = words[1:]
        takes = WRAPPER_VALUES.get(wrapper, set())
        while words and (words[0].startswith("-") or re.match(r"\A[A-Za-z_][A-Za-z0-9_]*=", words[0])):
            words = words[2:] if words[0] in takes else words[1:]
        words = words[WRAPPER_POSITIONAL.get(wrapper, 0):]
    if not words:
        return cwd
    name, args = os.path.basename(words[0]), words[1:]

    def write(paths: Iterable[str], kind: str = "write") -> None:
        for w in paths:
            for p in paths_of(cwd, w):
                out.writes.append((p, kind, w.endswith("/") or os.path.isdir(p)))

    if name in ("cd", "pushd"):
        target = next((a for a in args if not a.startswith("-")), os.path.expanduser("~"))
        new = resolve(cwd, target)
        return new if os.path.isdir(new) else cwd
    if name in SHELLS:
        return cwd  # `sh -c …` is analysed by analyze(), wherever it stands
    if name == "eval":
        analyze(" ".join(args), cwd, out, depth + 1)
        return cwd
    # Writes.
    if name == "tee":
        write(non_options(args))
    elif name in ("cp", "install", "ln", "mv", "rsync"):
        target_dir = None
        rest: List[str] = []
        skip = False
        for k, a in enumerate(args):
            if skip:
                skip = False
                continue
            if a in ("-t", "--target-directory") and k + 1 < len(args):
                target_dir = args[k + 1]
                skip = True
            elif a.startswith("--target-directory="):
                target_dir = a.split("=", 1)[1]
            elif a in ("-S", "--suffix", "-m", "--mode", "-o", "-g", "--owner", "--group"):
                skip = True
            elif not a.startswith("-") or a == "-":
                rest.append(a)
        if target_dir is None and len(rest) >= 2:
            target_dir, rest = rest[-1], rest[:-1]
            dest = resolve(cwd, target_dir)
            if not (target_dir.endswith("/") or os.path.isdir(dest)) and len(rest) == 1:
                write([target_dir])
                target_dir = None
        if target_dir is not None:
            for src in rest:
                write([os.path.join(target_dir, os.path.basename(src.rstrip("/")))])
        if name == "mv":
            write(rest, "rm")
    elif name in ("rm", "unlink", "rmdir", "shred"):
        write(non_options(args), "rm")
    elif name in ("touch", "truncate"):
        write(non_options(args, ("-s", "--size", "-r", "--reference", "-d", "-t")))
    elif name == "sed" and any(a == "-i" or a.startswith("-i") or a.startswith("--in-place") or
                               (re.match(r"\A-[a-zA-Z]+\Z", a) and "i" in a) for a in args):
        files = non_options(args, ("-e", "--expression", "-f", "--file", "-l"))
        has_script = any(a in ("-e", "--expression", "-f", "--file") or a.startswith(("--expression=", "--file="))
                         for a in args)
        if "-i" in args:  # BSD sed: `-i ''` or `-i .bak` takes the next word as the suffix when it is not a script
            k = args.index("-i")
            if k + 1 < len(args) and args[k + 1] in files and (args[k + 1] == "" or args[k + 1].startswith(".")):
                files.remove(args[k + 1])
        files = [f for f in files if f]
        write(files if has_script else files[1:])
    elif name == "perl" and any(re.match(r"\A-[a-zA-Z]*i", a) for a in args):
        files = non_options(args, ("-e", "-E", "-M", "-I"))
        has_script = any(a in ("-e", "-E") or re.match(r"\A-[a-zA-Z]*[eE]\Z", a) for a in args)
        write(files if has_script else files[1:])
    elif name == "dd":
        write([a[3:] for a in args if a.startswith("of=")])
    elif name == "git":
        git_cwd = cwd
        k = 0
        while k < len(args) and args[k].startswith("-"):
            if args[k] == "-C" and k + 1 < len(args):
                git_cwd = resolve(git_cwd, args[k + 1])
                k += 2
            elif args[k] in ("-c", "--git-dir", "--work-tree", "--namespace"):
                k += 2
            else:
                k += 1
        out.dirs.add(git_cwd)
        sub, rest = (args[k], args[k + 1:]) if k < len(args) else ("", [])
        saved = cwd
        cwd = git_cwd
        if sub == "commit":
            out.commits.append((git_cwd, rest, list(out.adds.get(git_cwd, []))))
        elif sub in ("merge", "cherry-pick", "revert", "rebase", "am") and "--continue" in rest:
            out.commits.append((git_cwd, [], list(out.adds.get(git_cwd, []))))
        elif sub == "add":
            out.adds.setdefault(git_cwd, []).append(rest)
        elif sub in ("rm", "mv"):
            write(non_options(rest), "git")
        elif sub in ("checkout", "restore", "reset", "stash", "switch"):
            out.git_ops.append((git_cwd, sub, rest))
        cwd = saved
    return cwd


def write_targets(a: Analysis, cwd: str) -> List[Tuple[str, str, str, bool]]:
    """(root, repository path or "" for the root, kind, is a directory) for each write in a repository with a gate."""
    out = []
    for path, kind, is_dir in a.writes:
        root = gated_root(path)
        if not root:
            continue
        if gate.is_root(root, path):
            out.append((root, "", kind, True))
            continue
        rel = gate.repo_relative(root, path)
        if rel is not None:
            out.append((root, rel, kind, is_dir))
    return out


def target_gated(root: str, rel: str, is_dir: bool, g: dict) -> bool:
    """A write target is gated: a directory holding gated paths, an existing gated file, or a path that does
    not exist yet and is gated both as a file and as a directory (`frontend/.design` is an exempt directory,
    though the bare name matches `frontend/**`)."""
    if is_dir:
        return gate.dir_is_gated(rel, g)
    if os.path.lexists(gate.rel_file(root, rel)):
        return gate.is_gated(rel, g)
    return gate.is_gated(rel, g) and gate.dir_is_gated(rel, g)


def involved_roots(a: Analysis, cwd: str) -> List[str]:
    roots = {gated_root(d) for d in a.dirs} | {gated_root(p) for p, _, _ in a.writes}
    roots.add(gated_root(cwd))
    return sorted(r for r in roots if r)


# --- git commands and the owner's uncommitted ledger ------------------------------


def ledger_dirty(root: str) -> bool:
    out = git_out(root, "status", "--porcelain", "-z", "--", *(gate.literal(f) for f in ledger_files()))
    return bool(out and out.strip("\0"))


def covers_ledger(root: str, git_cwd: str, specs: List[str]) -> bool:
    for spec in specs:
        if spec in (":/", ":(top)"):
            return True
        full = resolve(git_cwd, spec)
        if gate.is_root(root, full):
            return True
        rel = gate.repo_relative(root, full)
        if rel is not None and is_ledger(rel, is_dir=True):
            return True
    return False


def discards_ledger(root: str, git_cwd: str, sub: str, args: List[str]) -> bool:
    """The git command would overwrite or drop the working-tree ledger files (only asked when they are dirty)."""
    if sub == "checkout":
        if "--" in args:
            return covers_ledger(root, git_cwd, args[args.index("--") + 1:])
        if any(a in ("-f", "--force") for a in args):
            return True
        plain = non_options(args, ("-b", "-B", "--orphan"))
        if any(a in ("-b", "-B", "--orphan") for a in args):
            return False
        return bool(plain)  # a ref (or paths): switching branches or overwriting files
    if sub == "switch":
        return not any(a in ("-c", "-C", "--create", "--force-create") for a in args) or \
            any(a in ("-f", "--force", "--discard-changes") for a in args)
    if sub == "restore":
        staged = any(a in ("--staged", "-S") for a in args)
        worktree = any(a in ("--worktree", "-W") for a in args)
        if staged and not worktree:
            return False
        return covers_ledger(root, git_cwd, non_options(args, ("-s", "--source")))
    if sub == "reset":
        return any(a in ("--hard", "--merge", "--keep") for a in args)
    if sub == "stash":
        plain = non_options(args, ("-m", "--message"))
        verb = plain[0] if plain else ""
        if verb in ("list", "show", "apply", "pop", "drop", "clear", "branch", "create", "store"):
            return False
        specs = args[args.index("--") + 1:] if "--" in args else [a for a in plain if a not in ("push", "save")]
        return covers_ledger(root, git_cwd, specs) if specs else True
    return False


def ledger_loss_block(sub: str) -> str:
    return ("design gate: blocked. `git {}` here would discard the owner's uncommitted approvals: "
            "docs/design/approvals.json or docs/design/approvers.txt differs from the index or HEAD. Commit them "
            "first, as they are (`git add docs/design/approvals.json docs/design/approvers.txt && git commit -m "
            "\"docs(design): the owner's approvals\"`), then run the command again.".format(sub))


# --- the commit check -----------------------------------------------------------

COMMIT_VALUE_SHORT = set("mFCct")
COMMIT_VALUE_LONG = {"--message", "--file", "--reuse-message", "--reedit-message", "--template", "--author",
                     "--date", "--fixup", "--squash", "--cleanup", "--trailer", "--pathspec-from-file"}


def parse_commit(args: List[str]) -> Tuple[bool, List[str]]:
    """(-a/--all given, pathspecs)."""
    all_ = False
    paths: List[str] = []
    k = 0
    ddash = False
    while k < len(args):
        a = args[k]
        if ddash:
            paths.append(a)
        elif a == "--":
            ddash = True
        elif a.startswith("--"):
            name = a.split("=", 1)[0]
            if name == "--all":
                all_ = True
            if "=" not in a and name in COMMIT_VALUE_LONG:
                k += 1
        elif a.startswith("-") and len(a) > 1:
            for j, ch in enumerate(a[1:]):
                if ch == "a":
                    all_ = True
                if ch in COMMIT_VALUE_SHORT:
                    if j == len(a) - 2:
                        k += 1
                    break
        else:
            paths.append(a)
        k += 1
    return all_, paths


def lines0(text: Optional[str]) -> List[str]:
    return [p for p in (text or "").split("\0") if p]


def commit_problems(root: str, git_cwd: str, args: List[str], adds: List[List[str]]) -> List[str]:
    """The commit (with the `git add`s before it in the same command) would carry gated paths that differ from
    the base: block when the edit check fails, or when the index and the working tree disagree on what the
    approval relies on."""
    g = gate.load_gate(root)
    all_, pathspecs = parse_commit(args)
    cached = set(lines0(git_out(root, "diff", "--cached", "--name-only", "--no-renames", "-z")))
    from_tree: Set[str] = set()  # paths the commit takes from the working tree
    if all_:
        from_tree |= set(lines0(git_out(root, "diff", "--name-only", "--no-renames", "-z")))
    if pathspecs:
        from_tree |= set(lines0(git_out(git_cwd, "diff", "--name-only", "--no-renames", "-z", "HEAD", "--", *pathspecs)))
    for spec in adds:
        specs = non_options(spec, ("--chmod", "--pathspec-from-file"))
        if not specs and any(a in ("-A", "--all", "-u", "--update") for a in spec):
            specs = ["."]
        if specs:
            from_tree |= set(lines0(git_out(git_cwd, "ls-files", "-z", "-om", "--exclude-standard", "--full-name",
                                            "--", *specs)))
    cands = sorted(p for p in cached | from_tree if gate.is_gated(p, g))
    if not cands:
        return []
    base = gate.resolve_base(root)
    tree_blobs = worktree_blobs(root, [p for p in cands if p in from_tree])

    def now_blob(p: str) -> Optional[str]:
        b = tree_blobs[p] if p in from_tree else index_blob(root, p)
        return None if b == "absent" else b

    tips = ref_tips(root)
    for pseudo in ("MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"):
        sha = git_out(root, "rev-parse", "-q", "--verify", pseudo + "^{commit}")
        if sha and sha.strip():
            tips.append(sha.strip())
    gated = [p for p in cands if base is None or now_blob(p) != gate.blob_at(root, base, p)]
    # Content some commit already holds (a merge, cherry-pick or revert being finished) is not new UI.
    gated = [p for p in gated if not (now_blob(p) and blob_known(root, p, now_blob(p), tips))]
    if not gated:
        return []
    problems = []
    result = gate.evaluate(root, "edit")
    if result["findings"]:
        problems.append(gated_block(root, "This commit", gated, result))
    branch = gate.current_branch(root) or ""
    change = "{}/{}.md".format(gate.CHANGES, gate.slug(branch)) if branch else None
    watched = {gate.SNAPSHOT.lower(), gate.LEDGER.lower()} | ({change.lower()} if change else set())

    def watched_path(p: str) -> bool:
        return p.lower() in watched or gate.classify(p, g.get("digestInputs", []))

    differ = [] if all_ else lines0(git_out(root, "diff", "--name-only", "--no-renames", "-z"))
    differ += lines0(git_out(root, "ls-files", "-z", "-o", "--exclude-standard"))
    differ = sorted({p for p in differ if watched_path(p)})
    if differ:
        problems.append(
            "design gate: blocked. This commit carries gated files ({}) while the index and the working tree "
            "disagree on {}: the approval the commit relies on must be the one it carries. Stage them too "
            "(`git add {}`) or put the working tree back, then commit again.".format(
                ", ".join(gated), ", ".join(differ), " ".join(differ)))
    return problems


# --- the hooks ------------------------------------------------------------------


def tool_command(data: dict) -> Tuple[str, str]:
    ti = data.get("tool_input") if isinstance(data.get("tool_input"), dict) else {}
    command = ti.get("command") if isinstance(ti.get("command"), str) else ""
    cwd = ti.get("cwd") if isinstance(ti.get("cwd"), str) and ti.get("cwd") else None
    base = data.get("cwd") if isinstance(data.get("cwd"), str) and data.get("cwd") else os.getcwd()
    return command, os.path.join(base, os.path.expanduser(cwd)) if cwd else base


def session_id(data: dict) -> str:
    sid = data.get("session_id")
    return sid if isinstance(sid, str) else ""


def broken_gate_block(what: str, rels: List[str], root: str, e: Exception) -> str:
    return ("design gate: blocked. {} would change {}, which the design approvals cover, and the gate cannot run in "
            "{}: {}. Fix that first (docs/design/gate.json must parse; `python3 tools/design/gate.py check` shows the "
            "same error). Files outside frontend/ and internal/app/ are not affected.".format(
                what, ", ".join(rels), root, e))


def evaluate_or_block(root: str, assumed: List[str], what: str, rels: List[str]) -> dict:
    try:
        return gate.evaluate(root, "edit", assumed=assumed)
    except gate.GateError as e:
        raise Block(broken_gate_block(what, rels, root, e))


def pre_edit(data: dict) -> int:
    ti = data.get("tool_input") if isinstance(data.get("tool_input"), dict) else {}
    target = ti.get("file_path") or ti.get("notebook_path")
    if not isinstance(target, str) or not target:
        return 0
    cwd = data.get("cwd") if isinstance(data.get("cwd"), str) and data.get("cwd") else os.getcwd()
    full = os.path.normpath(os.path.join(cwd, os.path.expanduser(target)))
    root = gated_root(full)
    if root is None:
        return 0
    rel = gate.repo_relative(root, full)
    if rel is None:
        return 0
    tool = data.get("tool_name") if isinstance(data.get("tool_name"), str) and data.get("tool_name") else "edit"
    what = "This {}".format(tool)
    if is_ledger(rel):
        raise Block(ledger_block(what, rel))
    try:
        g = gate.load_gate(root)
    except gate.GateError as e:
        if rel.lower().startswith(APP_PREFIXES):
            raise Block(broken_gate_block(what, [rel], root, e))
        warn("{} in {}; allowed, since {} is not app UI".format(e, root, rel))
        return 0
    gated = gate.is_gated(rel, g)
    digest_input = gate.classify(rel, g.get("digestInputs", []))
    if gated:
        result = evaluate_or_block(root, [full], what, [rel])
        if result["findings"]:
            raise Block(gated_block(root, what, [rel], result))
    if gated or digest_input:
        with Session(root, session_id(data)) as st:
            note_changed(st, "gated" if gated else "digest", [rel])
    return 0


def pre_bash(data: dict) -> int:
    command, cwd = tool_command(data)
    if not command.strip():
        return 0
    a = analyze(command, cwd)
    problems: List[str] = []
    for which in dict.fromkeys(a.human):
        problems.append(human_block(which))
        break
    targets = write_targets(a, cwd)
    for root, rel, kind, is_dir in targets:
        if is_ledger(rel, is_dir):
            problems.append(ledger_block("This command", rel or "{} (the repository root)".format(root)))
            break
    by_root: Dict[str, List[Tuple[str, bool]]] = {}
    for root, rel, kind, is_dir in targets:
        if kind == "git":
            continue  # git rm/mv: the ledger check above; post-bash judges the rest
        try:
            g = gate.load_gate(root)
        except gate.GateError as e:
            if rel.lower().startswith(APP_PREFIXES) or (is_dir and not rel):
                problems.append(broken_gate_block("This command", [rel or "(the repository root)"], root, e))
            continue
        if not target_gated(root, rel, is_dir, g):
            continue
        if kind == "rm" and not is_dir:
            v = gate.open_violations(root).get(rel)
            if v and v.get("before") == "absent":
                continue  # removing a file that did not exist before is always allowed
        by_root.setdefault(root, []).append((rel, is_dir))
    for root, rels in sorted(by_root.items()):
        names = [r + ("/" if d and r else "") or "(the repository root)" for r, d in rels]
        assumed = [os.path.join(root, r) + ("/" if d else "") for r, d in rels]
        result = evaluate_or_block(root, assumed, "This command", names)
        if result["findings"]:
            problems.append(gated_block(root, "This command", names, result))
    for git_cwd, sub, args in a.git_ops:
        root = gated_root(git_cwd)
        if root and ledger_dirty(root) and discards_ledger(root, git_cwd, sub, args):
            problems.append(ledger_loss_block(sub))
            break
    for git_cwd, args, adds in a.commits:
        root = gated_root(git_cwd)
        if root:
            try:
                problems += commit_problems(root, git_cwd, args, adds)
            except gate.GateError as e:
                staged = lines0(git_out(root, "diff", "--cached", "--name-only", "-z"))
                if a.adds or any(p.lower().startswith(APP_PREFIXES) for p in staged):
                    problems.append("design gate: blocked. The gate cannot check this commit in {}: {}".format(root, e))
    if problems:
        raise Block("\n\n".join(problems))
    tool_id = data.get("tool_use_id") if isinstance(data.get("tool_use_id"), str) else ""
    for root in involved_roots(a, cwd):
        record_before(root, session_id(data), tool_id, command)
    return 0


def catch_up(root: str, st: dict, skip: str = "") -> List[str]:
    """Runs the post-bash detection for each recorded command whose post-bash never came (a failed tool call
    without PostToolUseFailure, a crash) — once each; its own post-bash, if it still comes, runs it again."""
    texts = []
    for tid, rec in sorted(st["pending"].items(), key=lambda kv: kv[1].get("seq", 0) if isinstance(kv[1], dict) else 0):
        if tid == skip or not isinstance(rec, dict) or rec.get("checked"):
            continue
        rec["checked"] = True
        found, ledger_text = detect(root, rec, st)
        open_violations(st, found, int(rec.get("seq", 0)), str(rec.get("at", now())))
        if ledger_text:
            texts.append(ledger_text)
    return texts


def record_before(root: str, sid: str, tool_id: str, command: str = "") -> None:
    try:
        g = gate.load_gate(root)
    except gate.GateError:
        return
    dirty_paths = [p for p in gate.uncommitted_paths(root) if gate.is_gated(p, g)]
    dirty = worktree_blobs(root, dirty_paths, write=True)
    rec = {"head": head_sha(root), "tips": ref_tips(root), "dirty": dirty, "digest": gate.digest(root),
           "ledger": worktree_blobs(root, ledger_files()), "command": command[:300], "at": now(), "t": time.time()}
    with Session(root, sid) as st:
        catch_up(root, st, skip=tool_id)
        st["seq"] += 1
        rec["seq"] = st["seq"]
        cutoff = time.time() - PENDING_TTL
        st["pending"] = {k: v for k, v in st["pending"].items() if isinstance(v, dict) and v.get("t", 0) > cutoff}
        if tool_id in st["pending"]:
            return  # the earliest before-state of this tool call wins
        st["pending"][tool_id] = rec


def ledger_tripwire(root: str, rec: dict, st: dict) -> Optional[str]:
    before = rec.get("ledger")
    if not isinstance(before, dict):
        return None
    now_blobs = worktree_blobs(root, ledger_files())
    changed = [f for f in ledger_files() if before.get(f) != now_blobs[f]]
    if not changed:
        return None
    text = "\n".join(
        ["design gate: {} changed while an agent command was running (`{}`), in {}.".format(
            f, rec.get("command") or "a shell command", root) for f in changed] + [
            "If the owner approved at their terminal at that moment, this is fine. Otherwise stop and tell the owner: "
            "only they write the ledger, and they can undo the change with {}. Nothing was restored.".format(
                " and ".join("`git checkout -- {}`".format(f) for f in changed))])
    st["notes"]["ledger-changed"] = text
    return text


def detect(root: str, rec: dict, st: dict) -> Tuple[Dict[str, dict], Optional[str]]:
    """The violations this command opened ({path: violation}), and the ledger tripwire's message."""
    ledger_text = ledger_tripwire(root, rec, st)
    return detect_gated(root, rec, st), ledger_text


def detect_gated(root: str, rec: dict, st: dict) -> Dict[str, dict]:
    g = gate.load_gate(root)
    tips = [t for t in rec.get("tips", []) if isinstance(t, str)]
    old_head = rec.get("head")
    head = head_sha(root)
    dirty = rec.get("dirty", {}) if isinstance(rec.get("dirty"), dict) else {}
    cands = set(dirty) | {p for p in gate.uncommitted_paths(root) if gate.is_gated(p, g)}
    if head and old_head and head != old_head:
        cands |= {p for p in gate.diff_names(root, old_head, head) if gate.is_gated(p, g)}
    created = new_commits(root, head, tips)
    blobs_in_commits: List[Tuple[str, str, str]] = []
    for c in created:
        for p, blob in commit_blobs(root, c):
            if gate.is_gated(p, g):
                blobs_in_commits.append((c, p, blob))
                cands.add(p)
    current_digest = gate.digest(root)
    if current_digest != rec.get("digest"):
        note_changed(st, "digest", ["(a shell command)"])
    if snapshot_current(root, current_digest):
        st["changed"]["digest"] = []
    cands = {p for p in cands if gate.is_gated(p, g)}
    if not cands:
        return {}
    now_blobs = worktree_blobs(root, cands, write=True)  # `after`: recoverable, and what restore expects to find

    def before_of(p: str) -> str:
        if p in dirty and isinstance(dirty[p], str):
            return dirty[p]
        return (gate.blob_at(root, old_head, p) if old_head else None) or "absent"

    found: Dict[str, dict] = {}
    for p in sorted(cands):
        before, now_blob = before_of(p), now_blobs[p]
        if now_blob == before:
            continue
        note_changed(st, "gated", [p])
        if now_blob == "absent":
            known = existed_before(root, head, tips) and gate.blob_at(root, head, p) is None if head else False
        else:
            known = blob_known(root, p, now_blob, tips)
        if not known:
            found[p] = {"before": before, "after": now_blob, "commit": None,
                        "head_before": (gate.blob_at(root, old_head, p) if old_head else None) or "absent"}
    for c, p, blob in blobs_in_commits:
        if blob_known(root, p, blob, tips):
            continue
        v = found.setdefault(p, {"before": before_of(p), "after": now_blobs[p],
                                 "head_before": (gate.blob_at(root, old_head, p) if old_head else None) or "absent"})
        v["commit"] = v.get("commit") or c
        if head and c == head:
            v["commit"] = c
    if not found:
        return {}
    if not gated_check(root, found)["findings"]:
        return {}
    return found


_checks: Dict[str, dict] = {}


def gated_check(root: str, paths: Iterable[str]) -> dict:
    """`check --mode edit` with gated paths assumed changed. With any gated path assumed, the findings do not
    depend on which one, so one evaluation per repository serves the whole hook run."""
    if root not in _checks:
        _checks[root] = gate.evaluate(root, "edit", assumed=[os.path.join(root, p) for p in paths])
    return _checks[root]


def open_violations(st: dict, found: Dict[str, dict], seq: int, at: str) -> Set[str]:
    """Opens or updates violations: the earliest `before` wins; `after` is always the latest agent content."""
    opened = set()
    for p, v in found.items():
        ex = st["violations"].get(p)
        if not isinstance(ex, dict) or seq < ex.get("seq", float("inf")):
            merged = dict(v, seq=seq, opened=at)
            if isinstance(ex, dict):
                merged["commit"] = v.get("commit") or ex.get("commit")
            st["violations"][p] = merged
            opened.add(p)
        else:
            ex["after"] = v.get("after", ex.get("after"))
            if v.get("commit"):
                ex["commit"] = v["commit"]
            opened.add(p)
    return opened


def reconcile(root: str, st: dict) -> None:
    """Closes each violation whose working tree, index and HEAD hold the earlier content, or all of them once
    the gate check passes."""
    violations = st["violations"]
    if not violations:
        return
    wt = worktree_blobs(root, violations)
    for p in sorted(violations):
        v = violations[p]
        if not isinstance(v, dict) or not isinstance(v.get("before"), str):
            del violations[p]
            continue
        head = gate.blob_at(root, "HEAD", p) or "absent"
        if wt[p] == v["before"] and index_blob(root, p) == v["before"] and head in (v["before"], v.get("head_before")):
            del violations[p]
    if violations:
        try:
            if not gated_check(root, violations)["findings"]:
                violations.clear()
        except gate.GateError:
            return


def post_bash(data: dict) -> int:
    command, cwd = tool_command(data)
    if not command.strip():
        return 0
    a = analyze(command, cwd)
    tool_id = data.get("tool_use_id") if isinstance(data.get("tool_use_id"), str) else ""
    texts = []
    for root in involved_roots(a, cwd):
        with Session(root, session_id(data)) as st:
            rec = st["pending"].pop(tool_id, None)
            new: Set[str] = set()
            if isinstance(rec, dict):
                if rec.get("closed_by") == "prompt":
                    # A user prompt came while this command ran: the human may have edited gated files since, so
                    # only the ledger tripwire runs; gated writes by this command are not attributed.
                    found, ledger_text = {}, ledger_tripwire(root, rec, st)
                else:
                    found, ledger_text = detect(root, rec, st)
                new = open_violations(st, found, int(rec.get("seq", 0)), str(rec.get("at", now())))
                if ledger_text:
                    texts.append(ledger_text)
            texts += catch_up(root, st)
            reconcile(root, st)
            if st["violations"]:
                texts.append(violations_text(root, st["violations"], new & set(st["violations"])))
        reconcile_others(root, Session(root, session_id(data)).path)
    if texts:
        raise Block("\n\n".join(texts))
    return 0


def reconcile_others(root: str, own: str) -> None:
    """Closes what can be closed in this worktree's other sessions (an abandoned session's violations)."""
    for path in other_sessions(root, own):
        try:
            with Session(root, path=path) as st:
                reconcile(root, st)
        except (OSError, gate.GateError):
            continue


def stop(data: dict, sub: bool = False) -> int:
    cwd = data.get("cwd") if isinstance(data.get("cwd"), str) and data.get("cwd") else os.getcwd()
    root = gated_root(cwd)
    if root is None:
        return 0
    key = "subagent:" + str(data.get("agent_id") or "") if sub else "main"
    with Session(root, session_id(data)) as st:
        catch_up(root, st)
        reconcile(root, st)
        own_path = Session(root, session_id(data)).path
        reasons: List[str] = []
        notes: List[str] = []
        once: List[str] = []
        if not sub:  # a sub-agent's stop blocks only on violations; the main agent's stop shows the ledger note
            ledger_note = st["notes"].pop("ledger-changed", None)
            once = [ledger_note] if ledger_note else []
        if st["violations"]:
            reasons.append(violations_text(root, st["violations"], set()))
        touched = bool(st["changed"]["gated"] or st["changed"]["digest"])
        if not sub:
            try:
                result = gate.evaluate(root, "edit")
            except gate.GateError as e:
                result = None
                notes.append("the gate check could not run in {}: {}".format(root, e))
            if result is not None:
                for f in result["findings"]:
                    if f["reason"] == "stale-snapshot":
                        if st["changed"]["digest"]:
                            reasons.append("design gate: this session changed the prototype's source and "
                                           "docs/design/state-hashes.json no longer matches it: run "
                                           "`task design:snapshot` before you finish (the owner approves "
                                           "fingerprints, so they must describe this source).")
                        else:
                            notes.append("docs/design/state-hashes.json is stale in {}, but not from this "
                                         "session's changes (`task design:snapshot` refreshes it).".format(root))
                    elif f["reason"] in ("no-change-file", "bad-change-file") and touched:
                        reasons.append("design gate: this session changed gated or prototype files, and {}: {} — "
                                       "{}".format(f["reason"], f["message"], ADVICE[f["reason"]]))
                if touched:
                    pending = pending_numbers(result)
                    if pending:
                        notes.append("\n".join(["{} state(s) await the owner's approval (this does not block):"
                                                .format(len(pending))] + state_links(root, pending, result["states"])))
                if not any(f["reason"] in ("stale-snapshot", "bad-snapshot") for f in result["findings"]):
                    st["changed"]["digest"] = []  # the snapshot is current: later staleness is not this session's
        if reasons:
            digest_key = hashlib.sha256("\n".join(reasons).encode()).hexdigest()
            prev = st["stops"].get(key) if isinstance(st["stops"].get(key), dict) else {}
            count = prev.get("count", 0) + 1 if prev.get("hash") == digest_key else 1
            if count > VALVE:
                st["stops"][key] = {"hash": digest_key, "count": 0}
                warn("the stop was blocked three times in a row for the same reasons, so it is allowed now. "
                     "Still wrong:\n" + "\n\n".join(once + reasons))
                for note in notes:
                    messages.append(note)
                return 0
            st["stops"][key] = {"hash": digest_key, "count": count}
            raise Block("\n\n".join(once + reasons + notes))
        st["stops"].pop(key, None)
        if once:
            raise Block("\n\n".join(once + notes))  # shown once: the note is gone now
    reconcile_others(root, own_path)
    messages.extend(notes)
    return 0


def session_files(root: str, sid: str) -> List[Tuple[str, str]]:
    """(worktree root, session file) of this session in every worktree of the repository holding root."""
    out = []
    listing = git_out(root, "worktree", "list", "--porcelain") or ""
    for line in listing.splitlines():
        if not line.startswith("worktree "):
            continue
        wt = line[len("worktree "):]
        dot_git = os.path.join(wt, ".git")
        git_dir = None
        if os.path.isdir(dot_git):
            git_dir = dot_git
        elif os.path.isfile(dot_git):
            try:
                with open(dot_git, encoding="utf-8") as f:
                    text = f.read().strip()
            except OSError:
                continue
            if text.startswith("gitdir:"):
                git_dir = os.path.join(wt, text[len("gitdir:"):].strip())
        if git_dir is None:
            continue
        path = os.path.join(git_dir, gate.SESSION_DIR, Session.file_name(sid))
        if os.path.isfile(path):
            out.append((wt, path))
    return out


def prompt(data: dict) -> int:
    """UserPromptSubmit: the human has had the keyboard, so no earlier command whose post-bash never came can be
    blamed for what the tree holds now; those records are marked checked (their own post-bash, if it ever comes,
    still runs). Never blocks, never prints to stdout (that would become model context)."""
    cwd = data.get("cwd") if isinstance(data.get("cwd"), str) and data.get("cwd") else os.getcwd()
    root = gated_root(cwd)
    if root is None:
        return 0
    for wt, path in session_files(root, session_id(data)):
        with Session(wt, path=path) as st:
            for rec in st["pending"].values():
                if not rec.get("checked"):
                    rec["checked"] = True
                    rec["closed_by"] = "prompt"
    return 0


def denied(data: dict) -> int:
    """PermissionDenied (auto mode): the command never ran; drop its before-state."""
    tool_id = data.get("tool_use_id")
    cwd = data.get("cwd") if isinstance(data.get("cwd"), str) and data.get("cwd") else os.getcwd()
    root = gated_root(cwd)
    if root is None or not isinstance(tool_id, str):
        return 0
    for wt, path in session_files(root, session_id(data)):
        with Session(wt, path=path) as st:
            st["pending"].pop(tool_id, None)
    return 0


QUIET = ("prompt", "denied")  # modes that always exit 0 and print nothing on stdout


HANDLERS = {
    "prompt": prompt,
    "denied": denied,
    "pre-edit": pre_edit,
    "pre-bash": pre_bash,
    "post-bash": post_bash,
    "stop": lambda d: stop(d, sub=False),
    "subagent-stop": lambda d: stop(d, sub=True),
}


def main(argv: List[str]) -> int:
    global gate
    mode = argv[1] if len(argv) > 1 else ""
    code = 0
    try:
        handler = HANDLERS.get(mode)
        if handler is None:
            warn("unknown hook mode {!r}; allowed".format(mode))
        else:
            try:
                data = json.loads(sys.stdin.read())
                if not isinstance(data, dict):
                    raise ValueError("not a JSON object")
            except ValueError as e:
                data = None
                warn("{}: the hook input is not valid JSON ({}); allowed".format(mode, e))
            if data is not None:
                sys.path.insert(0, TOOLS)
                import gate as gate_module  # noqa: E402
                gate = gate_module
                code = handler(data) or 0
    except Block as b:
        if mode in QUIET:
            return 0
        print(str(b), file=sys.stderr)
        return 2
    except Exception as e:  # fail open: a broken hook never blocks work
        warn("the {} hook failed ({!r}); allowed".format(mode or "design", e))
        code = 0
    if mode in QUIET:
        return 0
    if messages:
        print(json.dumps({"systemMessage": "\n\n".join(messages)}))
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv))
