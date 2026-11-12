#!/usr/bin/env python3
"""The design gate (docs/design/gate.json). Standard library only, Python >= 3.9.

    gate.py digest          the source digest of the prototype: sha256 over the
                            sorted files matching gate.json's digestInputs
    gate.py check           the branch check (exit 0 pass, 1 findings, 2 error):
                            [--mode edit|merge] [--branch B] [--base R]
                            [--assume-changed P]... [--json]
    gate.py status          the merge-mode findings and every state as a
                            Markdown link; never exits non-zero
    gate.py restore-violations
                            puts every gated path the Claude Code hooks hold an
                            open violation for back to the content it had
                            before (working tree and index)
    gate.py init | approve | confirm-no-ui
                            the owner's commands, in tools/design/gate_owner.py

This module only reads, except `restore-violations`, which writes gated
working-tree and index paths back to blobs the hooks recorded, and never a
ledger file. The owner's commands and the only writers of approvals.json /
approvers.txt live in gate_owner.py, which is imported only when one of those
subcommands runs, so a module that imports gate (the hooks) never loads a
writer.

The repository root is `git rev-parse --show-toplevel` of the current
directory, so every command works from any subdirectory and inside a linked
worktree. A missing approvals.json / approvers.txt is an empty ledger, and a
ledger entry whose `by` is not in approvers.txt counts for nothing.

Paths are classified (gated, exempt, parity groups) case-insensitively,
because the default macOS filesystem (APFS) is case-insensitive: a hook that
names `Frontend/src/App.tsx` edits the gated `frontend/src/App.tsx`. The
digest keeps git's exact-case paths.
"""
import argparse
import dataclasses
import hashlib
import json
import os
import re
import subprocess
import sys
import unicodedata
from typing import Callable, Dict, Iterable, List, Optional, Sequence, Set, Tuple

GATE_JSON = os.path.join("docs", "design", "gate.json")
SNAPSHOT = "docs/design/state-hashes.json"
RETIRED = "docs/design/retired.json"
LEDGER = "docs/design/approvals.json"
APPROVERS = "docs/design/approvers.txt"
CHANGES = "docs/design/changes"
SESSION_DIR = "choix-design"  # under `git rev-parse --git-path`: the hooks' per-worktree session state
PARITY_ENABLED = "frontend/e2e/parity/enabled.json"
TEST_FILES = ["**/*.test.ts", "**/*.test.tsx", "**/*.pw.ts", "**/*_test.go"]
LEDGER_KEYS = ("states", "retired", "noUi")
OWNER_COMMANDS = {"init": "cmd_init", "approve": "cmd_approve", "confirm-no-ui": "cmd_confirm_no_ui"}

# frontend/prototype/registry.ts GROUPS (gate_test.py keeps the two equal):
# (id, title, first, last).
GROUPS = [
    ("welcome", "Welcome and shell", 10, 49),
    ("library", "Library", 100, 199),
    ("folder", "Folder", 200, 299),
    ("focus", "Focus", 300, 399),
    ("compare", "Compare", 400, 449),
    ("settings", "Settings", 500, 599),
    ("overlays", "Overlays", 600, 699),
    ("native", "Native replicas", 700, 799),
    ("gallery", "Gallery", 800, 899),
]
PARITY_ALWAYS_SATISFIED = {"native"}  # native surfaces: skip entries in parity
PARITY_NEVER_IMPLIED = {"gallery"}  # the gallery has no app screens


class GateError(Exception):
    """An error the user can act on: printed without a traceback, exit 2."""


class ChangeFileError(ValueError):
    """A change file that does not parse or does not belong to this branch."""


def git(root: str, *args: str) -> str:
    try:
        out = subprocess.run(
            ["git", "-C", root, *args],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    except FileNotFoundError:
        raise GateError("git is not installed")
    except subprocess.CalledProcessError as e:
        raise GateError("git {} failed: {}".format(" ".join(args), e.stderr.decode(errors="replace").strip()))
    return out.stdout.decode()


def git_try(root: str, *args: str) -> Optional[str]:
    """git's output, or None when it exits non-zero."""
    try:
        return git(root, *args)
    except GateError:
        return None


def repo_root(cwd: Optional[str] = None) -> str:
    return git(cwd or os.getcwd(), "rev-parse", "--show-toplevel").strip()


def load_gate(root: str) -> dict:
    path = os.path.join(root, GATE_JSON)
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        raise GateError("{} is missing".format(GATE_JSON))
    except json.JSONDecodeError as e:
        raise GateError("{} is not valid JSON: {}".format(GATE_JSON, e))


def glob_regex(pattern: str, ignore_case: bool = False) -> "re.Pattern[str]":
    """A gate.json glob as a regular expression: `**` crosses directories, `*` and `?` do not."""
    out = []
    i = 0
    while i < len(pattern):
        if pattern.startswith("**/", i):
            out.append("(?:.*/)?")
            i += 3
        elif pattern.startswith("**", i):
            out.append(".*")
            i += 2
        elif pattern[i] == "*":
            out.append("[^/]*")
            i += 1
        elif pattern[i] == "?":
            out.append("[^/]")
            i += 1
        else:
            out.append(re.escape(pattern[i]))
            i += 1
    return re.compile("".join(out) + r"\Z", re.IGNORECASE if ignore_case else 0)


def matches(path: str, patterns: Sequence[str], ignore_case: bool = False) -> bool:
    return any(glob_regex(p, ignore_case).match(path) for p in patterns)


def classify(path: str, patterns: Sequence[str]) -> bool:
    """Gate classification (gated, exempt, digestInputs, parity, tests): case-insensitive, as on APFS."""
    return matches(path, patterns, ignore_case=True)


def listed_files(root: str) -> List[str]:
    """Tracked and untracked files that git does not ignore (`git ls-files -co --exclude-standard`)."""
    out = git(root, "ls-files", "-z", "-co", "--exclude-standard")
    return sorted({p for p in out.split("\0") if p})


def sha256_file(path: str) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 16), b""):
            h.update(chunk)
    return h.hexdigest()


def digest(root: str) -> str:
    """sha256 over each digestInputs file, sorted by path, as `path\\0sha256(content)\\n`.

    A tracked file deleted from the working tree is left out, like any file
    that does not exist.
    """
    inputs = load_gate(root).get("digestInputs", [])
    h = hashlib.sha256()
    for path in listed_files(root):
        if not matches(path, inputs):
            continue
        full = os.path.join(root, path)
        if not os.path.isfile(full):
            continue
        h.update(path.encode() + b"\0" + sha256_file(full).encode() + b"\n")
    return h.hexdigest()


def cmd_digest(args: argparse.Namespace) -> int:
    print(digest(repo_root()))
    return 0


# --- Pure helpers ------------------------------------------------------------


def slug(branch: str) -> str:
    """The change file's name: lower-cased, every run outside [a-z0-9] replaced by `-`."""
    return re.sub(r"[^a-z0-9]+", "-", branch.lower())


def parse_numbers(text: str) -> List[int]:
    """`12 13 20-25,30` → [12, 13, 20, …, 25, 30]. ValueError on anything else."""
    out = set()
    for part in re.split(r"[\s,]+", text.strip()):
        if not part:
            continue
        m = re.match(r"\A(\d+)(?:-(\d+))?\Z", part)
        if not m:
            raise ValueError("{!r} is not a state number or range".format(part))
        a = int(m.group(1))
        b = int(m.group(2)) if m.group(2) else a
        if b < a:
            raise ValueError("{!r} is an empty range".format(part))
        out.update(range(a, b + 1))
    return sorted(out)


def format_ranges(numbers: Iterable[int], sep: str = ", ") -> str:
    """[11, 12, 13, 30] → `11-13, 30`."""
    runs: List[List[int]] = []
    for n in sorted(set(numbers)):
        if runs and n == runs[-1][1] + 1:
            runs[-1][1] = n
        else:
            runs.append([n, n])
    return sep.join(str(a) if a == b else "{}-{}".format(a, b) for a, b in runs)


CHANGE_KEYS = ("branch", "states", "retired", "ui")
LIST_RE = re.compile(r"\A\[\s*(?:\d+(?:-\d+)?(?:\s*,\s*\d+(?:-\d+)?)*)?\s*\]\Z")


def parse_change_file(text: str) -> dict:
    """The front matter of docs/design/changes/<slug>.md: exactly branch, states, retired, ui."""
    m = re.match(r"\A---\n(.*?)\n---(?:\n|\Z)", text.replace("\r\n", "\n"), re.S)
    if not m:
        raise ChangeFileError("no front matter between `---` lines")
    fields: Dict[str, str] = {}
    for line in m.group(1).split("\n"):
        if not line.strip():
            continue
        kv = re.match(r"\A([A-Za-z]+):[ \t]*(.*?)[ \t]*\Z", line)
        if not kv:
            raise ChangeFileError("{!r} is not a `key: value` line".format(line))
        if kv.group(1) in fields:
            raise ChangeFileError("{} appears twice".format(kv.group(1)))
        fields[kv.group(1)] = kv.group(2)
    if set(fields) != set(CHANGE_KEYS):
        raise ChangeFileError("the front matter needs exactly {}; it has {}".format(
            ", ".join(CHANGE_KEYS), ", ".join(fields) or "nothing"))
    if not re.match(r"\A\S+\Z", fields["branch"]):
        raise ChangeFileError("branch: is empty or has spaces")
    out: dict = {"branch": fields["branch"]}
    for key in ("states", "retired"):
        if not LIST_RE.match(fields[key]):
            raise ChangeFileError("{}: is not a list like [100, 101, 200-225]".format(key))
        try:
            out[key] = parse_numbers(fields[key][1:-1])
        except ValueError as e:
            raise ChangeFileError("{}: {}".format(key, e))
    if fields["ui"] not in ("true", "false"):
        raise ChangeFileError("ui: must be true or false")
    out["ui"] = fields["ui"] == "true"
    return out


def group_of(n: int) -> Optional[tuple]:
    return next((g for g in GROUPS if g[2] <= n <= g[3]), None)


def full_label(n: int, label: str) -> str:
    """`Library · Filled, six folders`: the group title, then the state's label."""
    g = group_of(n)
    return "{} · {}".format(g[1], label) if g else label


def state_statuses(hashes: Dict[int, str], ledger: dict, retired: Iterable[int]) -> Dict[int, str]:
    """approved / stale / new for each registered state; retired for a retired number not registered.

    Ledger entries for numbers that are not registered are ignored.
    """
    approved = {int(e["state"]): e.get("hash") for e in ledger.get("states", [])}
    out = {}
    for n, h in hashes.items():
        out[n] = "new" if n not in approved else ("approved" if approved[n] == h else "stale")
    for n in retired:
        if n not in hashes:
            out[n] = "retired"
    return dict(sorted(out.items()))


def parity_missing(files: Iterable[str], parity_groups: Dict[str, List[str]], enabled: Iterable[str]) -> Dict[str, List[str]]:
    """{group: [files needing it]} for each group a file needs that enabled.json lacks.

    A file that no parityGroups glob names maps to every group except the
    gallery; native is always satisfied.
    """
    every = [g[0] for g in GROUPS if g[0] not in PARITY_NEVER_IMPLIED]
    have = set(enabled) | PARITY_ALWAYS_SATISFIED
    need: Dict[str, List[str]] = {}
    for f in sorted(files):
        named = [gs for pat, gs in parity_groups.items() if classify(f, [pat])]
        groups = {g for gs in named for g in gs} if named else set(every)
        for g in sorted(groups - have):
            need.setdefault(g, []).append(f)
    return need


def git_blob_sha(data: bytes) -> str:
    return hashlib.sha1(b"blob %d\0" % len(data) + data).hexdigest()


def no_ui_files_hash(root: str, paths: Iterable[str]) -> str:
    """sha256 of the sorted (path, blob sha) pairs; a deleted file's blob is `deleted`."""
    h = hashlib.sha256()
    for p in sorted(set(paths)):
        full = rel_file(root, p)
        blob = "deleted"
        if os.path.isfile(full):
            with open(full, "rb") as f:
                blob = git_blob_sha(f.read())
        h.update(p.encode() + b"\0" + blob.encode() + b"\n")
    return h.hexdigest()


# --- Files -------------------------------------------------------------------


def rel_file(root: str, rel: str) -> str:
    return os.path.join(root, *rel.split("/"))


def read_text(root: str, rel: str) -> Optional[str]:
    try:
        with open(rel_file(root, rel), encoding="utf-8") as f:
            return f.read()
    except FileNotFoundError:
        return None


def show_at(root: str, rev: str, rel: str) -> Optional[str]:
    """The file at a revision; None when it does not exist there."""
    return git_try(root, "show", "{}:{}".format(rev, rel))


def parse_json(text: Optional[str], what: str, default):
    if text is None or not text.strip():
        return default
    try:
        return json.loads(text)
    except json.JSONDecodeError as e:
        raise GateError("{} is not valid JSON: {}".format(what, e))


def parse_snapshot(text: Optional[str], what: str) -> Tuple[Dict[int, dict], Optional[str]]:
    """state-hashes.json, checked: ({state number: entry with a string hash}, sourceDigest)."""
    data = parse_json(text, what, {})
    if not isinstance(data, dict):
        raise GateError("{} is malformed: not a JSON object".format(what))
    states = data.get("states", {})
    if not isinstance(states, dict):
        raise GateError("{} is malformed: states is not an object".format(what))
    out: Dict[int, dict] = {}
    for k, v in states.items():
        if not re.match(r"\A\d+\Z", k):
            raise GateError("{} is malformed: state key {!r} is not a number".format(what, k))
        if not isinstance(v, dict) or not isinstance(v.get("hash"), str):
            raise GateError("{} is malformed: states[{}].hash is not a string".format(what, k))
        out[int(k)] = v
    source = data.get("sourceDigest")
    if source is not None and not isinstance(source, str):
        raise GateError("{} is malformed: sourceDigest is not a string".format(what))
    return out, source


def snapshot_states(text: Optional[str], what: str) -> Dict[int, dict]:
    return parse_snapshot(text, what)[0]


def parse_retired(text: Optional[str], what: str) -> Set[int]:
    """retired.json, checked: a list of state numbers."""
    data = parse_json(text, what, [])
    if not isinstance(data, list) or not all(isinstance(n, int) and not isinstance(n, bool) for n in data):
        raise GateError("{} is malformed: not a list of state numbers".format(what))
    return set(data)


LEDGER_FIELDS = {"states": ("state", "hash", "by"), "retired": ("state", "by"), "noUi": ("branch", "files", "by")}


def parse_ledger(text: Optional[str], what: str = LEDGER) -> dict:
    """approvals.json, checked: an object of three lists of entries with the fields each needs."""
    data = parse_json(text, what, {})
    if not isinstance(data, dict):
        raise GateError("{} is malformed: not a JSON object".format(what))
    out = {}
    for key in LEDGER_KEYS:
        entries = data.get(key, [])
        if not isinstance(entries, list):
            raise GateError("{} is malformed: {} is not a list".format(what, key))
        for i, e in enumerate(entries):
            if not isinstance(e, dict):
                raise GateError("{} is malformed: {}[{}] is not an object".format(what, key, i))
            for field in LEDGER_FIELDS[key]:
                v = e.get(field)
                ok = (isinstance(v, int) and not isinstance(v, bool)) if field == "state" else isinstance(v, str)
                if not ok:
                    raise GateError("{} is malformed: {}[{}].{} is not {}".format(
                        what, key, i, field, "an integer" if field == "state" else "a string"))
        out[key] = list(entries)
    return out


def load_ledger(root: str) -> dict:
    return parse_ledger(read_text(root, LEDGER))


def read_approvers(root: str) -> Optional[Set[str]]:
    """The lower-cased emails in approvers.txt; None when the file does not exist."""
    text = read_text(root, APPROVERS)
    if text is None:
        return None
    return {ln.strip().lower() for ln in text.splitlines() if ln.strip() and not ln.strip().startswith("#")}


def trusted_ledger(ledger: dict, approvers: Optional[Set[str]]) -> Tuple[dict, List[str], List[int]]:
    """(the entries made by an approver, a description of each other entry, their state numbers)."""
    ok: dict = {k: [] for k in LEDGER_KEYS}
    ignored: List[str] = []
    numbers: List[int] = []
    for key in LEDGER_KEYS:
        for e in ledger[key]:
            if approvers is not None and e["by"].strip().lower() in approvers:
                ok[key].append(e)
                continue
            if key == "noUi":
                ignored.append("noUi for {} by {}".format(e["branch"], e["by"]))
            else:
                ignored.append("#{}{} by {}".format(e["state"], " (retired)" if key == "retired" else "", e["by"]))
                numbers.append(e["state"])
    return ok, ignored, sorted(set(numbers))


# --- Identity and changed paths ---------------------------------------------


def current_branch(root: str, explicit: Optional[str] = None) -> Optional[str]:
    """--branch, else GITHUB_HEAD_REF, else HEAD's branch, else the branch being rebased."""
    if explicit:
        return explicit
    if os.environ.get("GITHUB_HEAD_REF"):
        return os.environ["GITHUB_HEAD_REF"]
    head = (git_try(root, "symbolic-ref", "--short", "-q", "HEAD") or "").strip()
    if head:
        return head
    for name in ("rebase-merge/head-name", "rebase-apply/head-name"):
        path = (git_try(root, "rev-parse", "--git-path", name) or "").strip()
        if not path:
            continue
        path = path if os.path.isabs(path) else os.path.join(root, path)
        if os.path.isfile(path):
            with open(path, encoding="utf-8") as f:
                ref = f.read().strip()
            if ref.startswith("refs/heads/"):
                return ref[len("refs/heads/"):]
    return None


def resolve_base(root: str, base: Optional[str] = None) -> Optional[str]:
    """The merge base of HEAD and --base, else of HEAD and origin/main, else of HEAD and main; None if none."""
    for ref in ([base] if base else ["origin/main", "main"]):
        out = git_try(root, "merge-base", "HEAD", ref)
        if out:
            return out.strip()
    return None


def diff_names(root: str, *revs: str) -> List[str]:
    """Paths in `git diff --name-status -M <revs>`: both sides of a rename or copy; deletions count."""
    toks = git(root, "diff", "--name-status", "-z", "-M", "--no-ext-diff", *revs).split("\0")
    paths = set()
    i = 0
    while i < len(toks):
        if not toks[i]:
            i += 1
            continue
        width = 2 if toks[i][0] in "RC" else 1
        paths.update(toks[i + 1:i + 1 + width])
        i += 1 + width
    return sorted(paths)


def uncommitted_paths(root: str) -> List[str]:
    """Staged, unstaged and untracked paths (both names of an uncommitted rename)."""
    paths = set()
    for args in (("diff", "--name-only", "--no-renames", "-z", "HEAD"), ("ls-files", "-z", "-o", "--exclude-standard")):
        paths.update(p for p in (git_try(root, *args) or "").split("\0") if p)
    return sorted(paths)


def nfc(text: str) -> str:
    return unicodedata.normalize("NFC", text)


def repo_relative(root: str, path: str, cwd: Optional[str] = None) -> Optional[str]:
    """A path given on the command line (absolute, or relative to cwd) as a repository path in NFC;
    None for a path outside the repository or for the root itself. The root prefix is compared
    case-insensitively (APFS) and both sides in Unicode NFC (APFS keeps either form; git reports NFC)."""
    full = nfc(os.path.realpath(os.path.join(cwd or os.getcwd(), path)))
    prefix = nfc(os.path.realpath(root)).rstrip(os.sep) + os.sep
    if not full.lower().startswith(prefix.lower()) or len(full) <= len(prefix):
        return None
    return full[len(prefix):].replace(os.sep, "/")


def is_root(root: str, path: str, cwd: Optional[str] = None) -> bool:
    full = nfc(os.path.realpath(os.path.join(cwd or os.getcwd(), path)))
    return full.lower() == nfc(os.path.realpath(root)).rstrip(os.sep).lower()


def assumed_targets(root: str, assumed: Iterable[str], cwd: Optional[str] = None) -> List[Tuple[str, bool]]:
    """Each assumed path inside the repository as (repository path, is a directory); the root is ("", True).

    A path is a directory when it is one on disk or is written with a trailing slash."""
    out = []
    for p in assumed:
        is_dir = p.endswith(("/", os.sep)) or os.path.isdir(os.path.join(cwd or os.getcwd(), p))
        if is_root(root, p, cwd):
            out.append(("", True))
            continue
        rel = repo_relative(root, p, cwd)
        if rel is not None:
            out.append((rel, is_dir))
    return out


def changed_paths(root: str, base: str, assumed: Iterable[str] = (), cwd: Optional[str] = None) -> List[str]:
    """Paths changed since base: committed, staged, unstaged, untracked, plus the assumed files.

    An assumed path outside the repository is dropped: it is not gated. An assumed directory is not a
    path here; evaluate() counts it as gated when dir_is_gated() says it holds gated paths.
    """
    paths = set(diff_names(root, base)) | set(uncommitted_paths(root))
    paths.update(rel for rel, is_dir in assumed_targets(root, assumed, cwd) if rel and not is_dir)
    return sorted(paths)


def is_gated(path: str, gate: dict) -> bool:
    return classify(path, gate.get("gated", [])) and not classify(path, gate.get("exempt", []))


GLOB_CHARS = re.compile(r"[*?\[]")
PROBE = "__design_gate_probe__/__design_gate_probe__.tsx"


def dir_is_gated(directory: str, gate: dict) -> bool:
    """A directory holds gated paths: a gated glob lies under it (`internal` holds
    `internal/app/menu/**`), or a file inside it would be gated (`frontend/src/app` under `frontend/**`).
    The repository root ("") holds every gated glob."""
    d = directory.strip("/")
    if not d:
        return bool(gate.get("gated"))
    for pattern in gate.get("gated", []):
        literal = GLOB_CHARS.split(pattern, 1)[0]
        if literal.lower().startswith(d.lower() + "/"):
            return True
    return is_gated(d + "/" + PROBE, gate)


def target_is_gated(rel: str, is_dir: bool, gate: dict) -> bool:
    return dir_is_gated(rel, gate) if is_dir else is_gated(rel, gate)


# --- The check ---------------------------------------------------------------


@dataclasses.dataclass
class Facts:
    """What the merge-only findings look at."""
    root: str
    gate: dict
    base: str
    branch: str
    change: Optional[dict]
    gated: List[str]
    hashes: Dict[int, str]
    base_hashes: Dict[int, str]
    base_retired: Set[int]
    ledger: dict


def evaluate(root: str, mode: str, branch_arg: Optional[str] = None, base_arg: Optional[str] = None,
             assumed: Iterable[str] = ()) -> dict:
    """The branch check's result: {mode, base, branch, gated, prototype, states, findings}."""
    gate = load_gate(root)
    snap_text = read_text(root, SNAPSHOT)
    malformed: List[Tuple[str, str]] = []
    try:
        snap_states, snap_digest = parse_snapshot(snap_text, SNAPSHOT)
    except GateError as e:
        snap_states, snap_digest = {}, None
        malformed.append(("bad-snapshot", "{}: fix it or run `task design:snapshot`".format(e)))
    hashes = {n: s["hash"] for n, s in snap_states.items()}
    try:
        retired = parse_retired(read_text(root, RETIRED), RETIRED)
    except GateError as e:
        retired = set()
        malformed.append(("bad-retired", "{}: it holds a JSON list of retired state numbers".format(e)))
    ledger_error = None
    try:
        raw = load_ledger(root)
    except GateError as e:
        raw, ledger_error = {k: [] for k in LEDGER_KEYS}, str(e)
    approvers = read_approvers(root)
    ledger, ignored, ignored_states = trusted_ledger(raw, approvers)
    statuses = state_statuses(hashes, ledger, retired)
    branch = current_branch(root, branch_arg)
    findings: List[dict] = []
    result = {"mode": mode, "base": None, "branch": branch, "gated": [], "prototype": [],
              "states": {str(n): s for n, s in statuses.items()}, "findings": findings}

    def find(reason: str, message: str, **extra) -> None:
        findings.append(dict(reason=reason, message=message, **extra))

    for reason, message in malformed:
        find(reason, message)
    base = resolve_base(root, base_arg)
    if base is None:
        find("no-base", "cannot find the base: no merge base of HEAD and {} (fetch origin/main, or pass --base)".format(
            base_arg or "origin/main or main"))
        return result
    if mode == "merge":
        approvers_finding(root, base, find)
        gate_scope_finding(root, base, gate, find)
    targets = assumed_targets(root, assumed)
    changed = changed_paths(root, base, assumed)
    gated = [p for p in changed if is_gated(p, gate)]
    gated += sorted({rel for rel, is_dir in targets if is_dir and dir_is_gated(rel, gate)} - set(gated))
    proto = [p for p in changed if classify(p, gate.get("digestInputs", []))]
    base_snap_text = show_at(root, base, SNAPSHOT)
    snapshot_changed = (snap_text or "") != (base_snap_text or "")
    result.update(base=base, gated=gated, prototype=proto)
    # Before the shortcut below: a branch that changes only approvals.json must still have its ledger judged.
    if ledger_error:
        find("bad-ledger", "{}; every approval is ignored".format(ledger_error))
    elif ignored:
        why = "{} is missing".format(APPROVERS) if approvers is None else "not made by an approver in " + APPROVERS
        find("bad-ledger", "{} ledger entr{} ignored ({}): {}".format(
            len(ignored), "y" if len(ignored) == 1 else "ies", why, "; ".join(ignored)), states=ignored_states)
    if not gated and not proto and not snapshot_changed:
        return result
    if not branch:
        find("no-branch", "cannot tell the branch (detached HEAD, no GITHUB_HEAD_REF, no rebase): pass --branch")
        return result

    rel = "{}/{}.md".format(CHANGES, slug(branch))
    text = read_text(root, rel)
    change = None
    if text is None:
        find("no-change-file", "{} is missing: add it with branch:, states:, retired: and ui: front matter".format(rel))
    else:
        try:
            change = parse_change_file(text)
            if change["branch"] != branch:
                raise ChangeFileError("branch: is {}, not {}".format(change["branch"], branch))
            if rel not in changed:
                raise ChangeFileError("not added or modified on this branch")
        except ChangeFileError as e:
            find("bad-change-file", "{}: {}".format(rel, e))
            change = None

    if not any(r == "bad-snapshot" for r, _ in malformed) and snap_digest != digest(root):
        find("stale-snapshot", "{} was not made from this source (sourceDigest is not `gate.py digest`): "
             "run `task design:snapshot`".format(SNAPSHOT))

    if change is not None and not change["ui"] and (proto or snapshot_changed):
        what = proto + ([SNAPSHOT] if snapshot_changed else [])
        shown = ", ".join(what[:5]) + (", …" if len(what) > 5 else "")
        find("ui-false-but-prototype-changed", "the change file says ui: false, but a digest input changed ({})".format(
            shown), paths=what)

    pending = {n for n, s in statuses.items() if s in ("new", "stale")}
    unapproved = set(pending) if gated else set()
    if mode == "merge" and change is not None:
        unapproved |= pending & set(change["states"])
    if unapproved:
        find("unapproved", "{} state(s) new or stale: {} — the owner reviews them (`task design:build && task "
             "design:serve`) and runs `task design:approve -- {}`".format(
                 len(unapproved), format_ranges(unapproved), format_ranges(unapproved, " ")), states=sorted(unapproved))

    try:
        base_hashes = {n: s["hash"] for n, s in snapshot_states(base_snap_text, "the base's " + SNAPSHOT).items()}
    except GateError as e:
        base_hashes = {}
        find("bad-snapshot", str(e))
    try:
        base_retired = parse_retired(show_at(root, base, RETIRED), "the base's " + RETIRED)
    except GateError as e:
        base_retired = set()
        find("bad-retired", str(e))
    ledger_retired = {e["state"] for e in ledger["retired"]}
    change_retired = set(change["retired"]) if change is not None else set()
    unretired: List[int] = []
    notes: List[str] = []
    for n in sorted((set(base_hashes) | {e["state"] for e in ledger["states"]}) - set(hashes)):
        lacks = []
        if n not in retired:
            lacks.append(RETIRED)
        if n not in base_retired and n not in change_retired:
            lacks.append("the change file's retired:")
        if n not in ledger_retired:
            lacks.append("the ledger's retired (`task design:approve -- --retire {}`)".format(n))
        if lacks:
            unretired.append(n)
            notes.append("#{} (missing from {})".format(n, ", ".join(lacks)))
    if unretired:
        find("unretired", "state(s) left the snapshot without being retired: {}".format("; ".join(notes)),
             states=unretired)

    if mode == "merge":
        merge_findings(Facts(root, gate, base, branch, change, gated, hashes, base_hashes, base_retired, ledger), find)
    return result


GATE_SCOPE_KEYS = ("gated", "exempt", "prototypeOwned", "parityGroups")


def gate_scope_finding(root: str, base: str, gate: dict, find: Callable[..., None]) -> None:
    """A branch that changes what the gate covers says so in review: the merge check reads the branch's own
    gate.json, so narrowing `gated` or widening `exempt` would otherwise switch the gate off for that branch.
    A base without gate.json (or with one that does not parse): no finding."""
    try:
        base_gate = json.loads(show_at(root, base, GATE_JSON.replace(os.sep, "/")) or "null")
    except ValueError:
        return
    if not isinstance(base_gate, dict):
        return
    changed = [k for k in GATE_SCOPE_KEYS if base_gate.get(k) != gate.get(k)]
    if changed:
        find("gate-changed", "docs/design/gate.json changes what the gate covers ({}) against the base, and this "
             "finding does not clear: the owner merges this pull request themselves, bypassing the required check, "
             "after reading the diff of docs/design/gate.json".format(", ".join(changed)), keys=changed)


def approvers_finding(root: str, base: str, find: Callable[..., None]) -> None:
    """approvers.txt is created by `task design:init`; changing it later is the owner's decision, so a
    branch that changes the base's file says so in review. A base without the file: no finding."""
    base_text = show_at(root, base, APPROVERS)
    if base_text is None:
        return
    if read_text(root, APPROVERS) != base_text:
        find("approvers-changed", "{0} differs from the base's (who may approve changes with this branch), and "
             "this finding does not clear: the owner merges this pull request themselves, bypassing the required "
             "check, after reading the diff of {0}".format(APPROVERS))


def merge_findings(f: Facts, find: Callable[..., None]) -> None:
    if f.change is not None:
        unlisted = sorted(n for n, h in f.hashes.items() if f.base_hashes.get(n) != h and n not in f.change["states"])
        if unlisted:
            find("unlisted-state", "state(s) new or changed on this branch but not in the change file's states: "
                 "{}".format(format_ranges(unlisted)), states=unlisted)
    reused = sorted(f.base_retired & set(f.hashes))
    if reused:
        find("retired-reused", "retired number(s) registered again: {}".format(format_ranges(reused)), states=reused)
    if f.change is not None and not f.change["ui"] and f.gated:
        files = no_ui_files_hash(f.root, f.gated)
        if not any(e["branch"] == f.branch and e["files"] == files for e in f.ledger["noUi"]):
            find("no-ui-unconfirmed", "ui: false with gated changes ({}) and no matching confirmation: commit them, "
                 "then the owner runs `task design:confirm-no-ui`".format(", ".join(f.gated)))
    app_files = [p for p in listed_files(f.root) if p.lower().startswith("frontend/") and is_gated(p, f.gate)
                 and not classify(p, TEST_FILES) and os.path.isfile(rel_file(f.root, p))]
    enabled = parse_json(read_text(f.root, PARITY_ENABLED), PARITY_ENABLED, [])
    need = parity_missing(app_files, f.gate.get("parityGroups", {}), enabled)
    if need:
        find("parity-not-enabled", "parity group(s) missing from {}: {}".format(PARITY_ENABLED, "; ".join(
            "{} (for {})".format(g, ", ".join(fs[:3]) + (", …" if len(fs) > 3 else "")) for g, fs in need.items())),
            groups=sorted(need))


def render_check(result: dict) -> str:
    head = "design gate ({} mode{}):".format(result["mode"], ", " + result["branch"] if result["branch"] else "")
    if not result["findings"]:
        return head + " pass"
    lines = ["{} {} finding(s)".format(head, len(result["findings"]))]
    lines += ["  {}: {}".format(f["reason"], f["message"]) for f in result["findings"]]
    return "\n".join(lines)


def cmd_check(args: argparse.Namespace) -> int:
    result = evaluate(repo_root(), args.mode, args.branch, args.base, args.assume_changed)
    print(json.dumps(result, indent=2, ensure_ascii=False) if args.json else render_check(result))
    return 1 if result["findings"] else 0


def md_link(n: int, label: str, review: str) -> str:
    text = "#{} {}".format(n, full_label(n, label)).replace("[", "\\[").replace("]", "\\]")
    return "[{}]({}/#s{})".format(text, review, n)


def render_status(root: str, result: dict) -> str:
    review = load_gate(root).get("reviewUrl", "").rstrip("/")
    try:
        labels = {n: str(s.get("label", "")) for n, s in snapshot_states(read_text(root, SNAPSHOT), SNAPSHOT).items()}
    except GateError:
        labels = {}
    statuses = {int(n): s for n, s in result["states"].items()}
    by = {k: [n for n, s in statuses.items() if s == k] for k in ("new", "stale", "approved", "retired")}
    lines = ["# Design gate: {}".format(result["branch"] or "(no branch)"), "",
             "{} registered states: {} approved, {} stale, {} new. {} retired. Review build: {} "
             "(`task design:build && task design:serve`).".format(
                 len(labels), len(by["approved"]), len(by["stale"]), len(by["new"]), len(by["retired"]), review), ""]
    if result["findings"]:
        lines.append("## Merge check: {} finding(s)".format(len(result["findings"])))
        lines += ["- **{}**: {}".format(f["reason"], f["message"]) for f in result["findings"]]
    else:
        lines.append("## Merge check: pass")
    pending = sorted(by["new"] + by["stale"])
    for title, numbers in (("Awaiting approval", pending), ("Approved", by["approved"])):
        if numbers:
            lines += ["", "## {} ({})".format(title, len(numbers))]
            lines += ["- {} — {}".format(md_link(n, labels.get(n, ""), review), statuses[n]) for n in numbers]
    if by["retired"]:
        lines += ["", "## Retired ({})".format(len(by["retired"])), "- " + ", ".join("#{}".format(n) for n in by["retired"])]
    lines.append("")
    if read_approvers(root) is None:
        lines.append("No approvers yet: the owner runs `task design:init` once, in their own terminal.")
    if pending:
        lines.append("To approve, the owner builds and serves the prototype (`task design:build && task design:serve`), "
                     "reviews each link and runs `task design:approve -- {}`.".format(format_ranges(pending, " ")))
    return "\n".join(lines)


def cmd_status(args: argparse.Namespace) -> int:
    try:
        root = repo_root()
        print(render_status(root, evaluate(root, "merge", args.branch, args.base)))
    except GateError as e:
        print("gate status: {}".format(e))
    except Exception as e:  # status is advisory: it never fails
        print("gate status: internal error: {!r}".format(e))
    return 0


# --- The hooks' session state and restore-violations --------------------------


def session_dir(root: str) -> str:
    """`git rev-parse --git-path choix-design`: per worktree, outside the working tree."""
    path = git(root, "rev-parse", "--git-path", SESSION_DIR).strip()
    return path if os.path.isabs(path) else os.path.join(root, path)


def read_sessions(root: str) -> List[Tuple[str, dict]]:
    """(file, state) for every session-*.json the hooks wrote in this worktree; unreadable ones are skipped."""
    d = session_dir(root)
    try:
        names = sorted(os.listdir(d))
    except OSError:
        return []
    out = []
    for name in names:
        if not (name.startswith("session-") and name.endswith(".json")):
            continue
        try:
            with open(os.path.join(d, name), encoding="utf-8") as f:
                data = json.load(f)
        except (OSError, ValueError):
            continue
        if isinstance(data, dict):
            out.append((os.path.join(d, name), data))
    return out


def open_violations(root: str) -> Dict[str, dict]:
    """{path: {before, head_before, commit, seq, opened}} over every session of this worktree; the earliest
    opened wins. `before` is the blob id the path held before the command that opened the violation, or
    `absent`; `head_before` is the path's blob at HEAD then (or `absent`); `commit` names a commit the
    command made with the path's new content."""
    out: Dict[str, dict] = {}
    for _, data in read_sessions(root):
        violations = data.get("violations")
        if not isinstance(violations, dict):
            continue
        for path, v in violations.items():
            if not isinstance(v, dict) or not isinstance(v.get("before"), str):
                continue
            if path not in out or str(v.get("opened", "")) < str(out[path].get("opened", "")):
                out[path] = v
    return out


def literal(path: str) -> str:
    """A pathspec that matches exactly this path."""
    return ":(literal)" + path


def blob_at(root: str, rev: str, path: str) -> Optional[str]:
    """The blob id of path at rev; None when it is not a file there (or rev does not exist)."""
    out = git_try(root, "ls-tree", "-z", "--full-tree", rev, "--", literal(path))
    for entry in (out or "").split("\0"):
        meta, _, name = entry.partition("\t")
        parts = meta.split()
        if name == path and len(parts) == 3 and parts[1] == "blob":
            return parts[2]
    return None


def index_entry(root: str, path: str) -> Optional[Tuple[str, str]]:
    """(mode, blob id) of path in the index (stage 0, else the first stage); None when it is not there."""
    out = git_try(root, "ls-files", "-s", "-z", "--", literal(path)) or ""
    for entry in out.split("\0"):
        meta, _, name = entry.partition("\t")
        parts = meta.split()
        if name == path and len(parts) == 3:
            return parts[0], parts[1]
    return None


def safe_relative(path: str) -> bool:
    parts = path.split("/")
    return bool(path) and not path.startswith("/") and "\\" not in path and all(p not in ("", ".", "..") for p in parts)


def describe_commit(root: str, rev: str) -> str:
    return (git_try(root, "log", "-1", "--format=%h %s", rev) or rev[:12]).strip()


def restore_one(root: str, path: str, before: str) -> str:
    """Puts path back to before (a blob id, or `absent`) in the working tree and the index."""
    full = rel_file(root, path)
    if os.path.isdir(full) and not os.path.islink(full):
        raise GateError("{} is a directory now; move it away first".format(path))
    if before == "absent":
        if os.path.lexists(full):
            os.remove(full)
        git(root, "rm", "--cached", "-q", "--ignore-unmatch", "--", literal(path))
        return "deleted (it did not exist before)"
    if (git_try(root, "cat-file", "-t", before) or "").strip() != "blob":
        raise GateError("the recorded content {} is not in the object store".format(before[:12]))
    try:
        data = subprocess.run(["git", "-C", root, "cat-file", "blob", before], check=True,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout
    except subprocess.CalledProcessError as e:
        raise GateError("git cat-file failed: {}".format(e.stderr.decode(errors="replace").strip()))
    entry = index_entry(root, path)
    mode = entry[0] if entry else None
    if mode is None:
        head = git_try(root, "ls-tree", "-z", "--full-tree", "HEAD", "--", literal(path)) or ""
        mode = head.split(" ", 1)[0] if head else ""
    mode = mode if mode in ("100644", "100755") else "100644"
    os.makedirs(os.path.dirname(full), exist_ok=True)
    tmp = full + ".design-gate-restore"
    with open(tmp, "wb") as f:
        f.write(data)
    os.chmod(tmp, 0o755 if mode == "100755" else 0o644)
    if os.path.islink(full):
        os.remove(full)
    os.replace(tmp, full)
    git(root, "update-index", "--add", "--cacheinfo", "{},{},{}".format(mode, before, path))
    return "restored to its earlier content {} (working tree and index)".format(before[:12])


def cmd_restore_violations(args: argparse.Namespace) -> int:
    """Writes only gated working-tree and index paths, each back to the blob the hooks recorded, and only
    while the gate check fails: a path that changed since the violation is left alone, and the content it
    replaces is saved as a blob first."""
    root = repo_root()
    g = load_gate(root)
    violations = open_violations(root)
    if not violations:
        print("restore-violations: no open violations in {}".format(root))
        return 0
    paths = sorted(violations)
    result = evaluate(root, "edit", assumed=[rel_file(root, p) for p in paths if safe_relative(p)])
    if not result["findings"]:
        print("restore-violations: the gate check passes now, so {} {} no longer a violation; nothing restored "
              "(the hooks close {} at the next command).".format(
                  ", ".join(paths), "is" if len(paths) == 1 else "are", "it" if len(paths) == 1 else "them"))
        return 0
    failed = 0
    todo = []
    for path in paths:
        if not safe_relative(path) or not is_gated(path, g):
            print("  {}: not a gated path; left alone".format(path))
            failed += 1
        else:
            todo.append(path)
    missing = [p for p in todo if violations[p]["before"] != "absent" and
               (git_try(root, "cat-file", "-t", violations[p]["before"]) or "").strip() != "blob"]
    if missing:
        for p in missing:
            print("  {}: the recorded earlier content {} is not in the object store".format(p, violations[p]["before"]))
        print("restore-violations: nothing restored. Restore those paths by hand (the owner decides what they "
              "should hold), or let the owner approve the states.")
        return 1
    head = (git_try(root, "rev-parse", "-q", "--verify", "HEAD") or "").strip()
    print("restore-violations: {} path(s)".format(len(todo)))
    for path in todo:
        v = violations[path]
        before = v["before"]
        after = v.get("after") if isinstance(v.get("after"), str) else None
        full = rel_file(root, path)
        current = "absent"
        if os.path.isfile(full):
            current = (git_try(root, "hash-object", "-w", "--", path) or "").strip() or "absent"
        if after is None or current != after:
            print("  {}: changed since the violation (it holds {}; the violation left {}; before it, {}). Not "
                  "restored: compare them with `git show <id>` and decide.".format(
                      path, current, after or "(not recorded)", before))
            failed += 1
            continue
        if current != "absent" and current != before:
            print("  {}: its current content is saved as blob {} (`git show {}` brings it back)".format(
                path, current, current))
        try:
            print("  {}: {}".format(path, restore_one(root, path, before)))
        except (OSError, GateError) as e:
            print("  {}: not restored: {}".format(path, e))
            failed += 1
            continue
        if (blob_at(root, "HEAD", path) or "absent") in (before, v.get("head_before")):
            continue
        commit = v.get("commit") if isinstance(v.get("commit"), str) else None
        if commit and commit == head:
            print("    It is also committed, in the last commit ({}): run `git commit --amend --no-edit` to take "
                  "the restored content into it.".format(describe_commit(root, commit)))
        elif commit:
            print("    It is also committed, in {}: undo that commit (`git reset --soft {}^` keeps the changes "
                  "after it staged) and commit the rest again.".format(describe_commit(root, commit), commit[:12]))
        else:
            last = (git_try(root, "log", "-1", "--format=%H", "--", literal(path)) or "").strip()
            print("    HEAD holds other content for it{}: amend or undo that commit.".format(
                " (last changed in {})".format(describe_commit(root, last)) if last else ""))
    print("The hooks close a violation at the next command once the working tree, the index and HEAD hold the "
          "content from before (or once the gate check passes).")
    return 1 if failed else 0


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = argparse.ArgumentParser(prog="gate.py", description="The design gate.")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("digest", help="print the prototype's source digest").set_defaults(func=cmd_digest)
    p = sub.add_parser("check", help="the branch check (exit 1 on findings)")
    p.add_argument("--mode", choices=("edit", "merge"), default="edit")
    p.add_argument("--branch")
    p.add_argument("--base")
    p.add_argument("--assume-changed", action="append", default=[], metavar="PATH")
    p.add_argument("--json", action="store_true")
    p.set_defaults(func=cmd_check)
    p = sub.add_parser("status", help="the merge-mode findings and every state's link")
    p.add_argument("--branch")
    p.add_argument("--base")
    p.set_defaults(func=cmd_status)
    sub.add_parser("restore-violations", help="put the gated paths the hooks hold violations for back to their "
                   "earlier content").set_defaults(func=cmd_restore_violations)
    sub.add_parser("init", help="(owner) create approvers.txt and an empty ledger")
    p = sub.add_parser("approve", help="(owner) approve states: 12 13 20-25, or all")
    p.add_argument("states", nargs="*")
    p.add_argument("--note")
    p.add_argument("--retire", nargs="+", metavar="N")
    p = sub.add_parser("confirm-no-ui", help="(owner) confirm the branch's committed gated changes do not change the UI")
    p.add_argument("--branch")
    p.add_argument("--base")
    args = parser.parse_args(argv)
    try:
        if args.command in OWNER_COMMANDS:
            import gate_owner  # only here: importing gate never loads a writer
            return getattr(gate_owner, OWNER_COMMANDS[args.command])(args)
        return args.func(args)
    except GateError as e:
        print("gate: {}".format(e), file=sys.stderr)
        return 2
    except KeyboardInterrupt:
        print("\ngate: interrupted; nothing written", file=sys.stderr)
        return 130


if __name__ == "__main__":
    # Run as the module `gate`, the one gate_owner imports, so both share GateError.
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    import gate as _gate
    sys.exit(_gate.main())
