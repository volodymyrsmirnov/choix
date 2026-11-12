"""Tests of tools/design/gate.py. Standard library only; no network.

    python3 -m unittest tools/design/gate_test.py -v

Each test builds a temporary git repository with a small gate.json.
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
GATE = os.path.join(HERE, "gate.py")
sys.path.insert(0, HERE)

import gate  # noqa: E402
import gate_owner  # noqa: E402

GATE_JSON = {
    "digestInputs": ["frontend/src/design/**", "frontend/package.json", "docs/design/gate.json"],
}


def sh(cwd, *args):
    return subprocess.run(args, cwd=cwd, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout.decode()


def write(root, rel, text):
    path = os.path.join(root, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)


class Repo:
    """A temporary repository with a design source, an ignored folder and one commit."""

    def __init__(self):
        self.base = tempfile.mkdtemp(prefix="gate-test-")
        self.root = os.path.join(self.base, "repo")
        os.makedirs(self.root)
        sh(self.root, "git", "init", "-q", "-b", "main")
        sh(self.root, "git", "config", "user.email", "t@example.com")
        sh(self.root, "git", "config", "user.name", "T")
        sh(self.root, "git", "config", "commit.gpgsign", "false")
        write(self.root, "docs/design/gate.json", json.dumps(GATE_JSON))
        write(self.root, "frontend/src/design/tokens.css", ":root { --a: 1px; }\n")
        write(self.root, "frontend/src/design/components/A.tsx", "export const A = 1\n")
        write(self.root, "frontend/package.json", "{}\n")
        write(self.root, "frontend/src/app/App.tsx", "export {}\n")
        write(self.root, "internal/engine.go", "package engine\n")
        write(self.root, ".gitignore", "frontend/src/design/ignored/\n")
        sh(self.root, "git", "add", "-A")
        sh(self.root, "git", "commit", "-q", "-m", "init")

    def digest(self, cwd=None):
        out = subprocess.run(
            [sys.executable, GATE, "digest"],
            cwd=cwd or self.root,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        return out.stdout.decode().strip()

    def close(self):
        shutil.rmtree(self.base, ignore_errors=True)


class DigestTest(unittest.TestCase):
    def setUp(self):
        self.repo = Repo()
        self.addCleanup(self.repo.close)

    def test_is_a_sha256(self):
        d = self.repo.digest()
        self.assertRegex(d, r"^[0-9a-f]{64}$")
        self.assertEqual(d, self.repo.digest())

    def test_format(self):
        import hashlib

        h = hashlib.sha256()
        for rel in ["docs/design/gate.json", "frontend/package.json",
                    "frontend/src/design/components/A.tsx", "frontend/src/design/tokens.css"]:
            with open(os.path.join(self.repo.root, rel), "rb") as f:
                h.update(rel.encode() + b"\0" + hashlib.sha256(f.read()).hexdigest().encode() + b"\n")
        self.assertEqual(self.repo.digest(), h.hexdigest())

    def test_changes_when_an_input_changes(self):
        before = self.repo.digest()
        write(self.repo.root, "frontend/src/design/tokens.css", ":root { --a: 2px; }\n")
        self.assertNotEqual(self.repo.digest(), before)

    def test_changes_when_an_untracked_input_appears(self):
        before = self.repo.digest()
        write(self.repo.root, "frontend/src/design/components/B.tsx", "export const B = 2\n")
        self.assertNotEqual(self.repo.digest(), before)

    def test_changes_when_an_input_is_deleted(self):
        before = self.repo.digest()
        os.remove(os.path.join(self.repo.root, "frontend/src/design/components/A.tsx"))
        self.assertNotEqual(self.repo.digest(), before)

    def test_ignores_an_ignored_file(self):
        before = self.repo.digest()
        write(self.repo.root, "frontend/src/design/ignored/cache.json", "{}\n")
        self.assertEqual(self.repo.digest(), before)

    def test_ignores_files_outside_the_inputs(self):
        before = self.repo.digest()
        write(self.repo.root, "frontend/src/app/App.tsx", "export const changed = 1\n")
        write(self.repo.root, "internal/engine.go", "package engine // changed\n")
        write(self.repo.root, "frontend/src/designer.ts", "x\n")
        self.assertEqual(self.repo.digest(), before)

    def test_same_from_root_subdirectory_and_linked_worktree(self):
        at_root = self.repo.digest()
        self.assertEqual(self.repo.digest(cwd=os.path.join(self.repo.root, "frontend", "src")), at_root)
        wt = os.path.join(self.repo.base, "wt")
        sh(self.repo.root, "git", "worktree", "add", "-q", "-b", "feat/x", wt)
        self.assertEqual(self.repo.digest(cwd=wt), at_root)
        self.assertEqual(self.repo.digest(cwd=os.path.join(wt, "frontend")), at_root)
        # The worktree's own files count, not the main checkout's.
        write(wt, "frontend/src/design/tokens.css", ":root { --a: 3px; }\n")
        self.assertNotEqual(self.repo.digest(cwd=wt), at_root)
        self.assertEqual(self.repo.digest(), at_root)

    def test_outside_a_repository_fails_cleanly(self):
        empty = tempfile.mkdtemp(prefix="gate-test-empty-")
        self.addCleanup(shutil.rmtree, empty, True)
        out = subprocess.run([sys.executable, GATE, "digest"], cwd=empty, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(out.returncode, 2)
        self.assertIn("gate:", out.stderr.decode())


class GlobTest(unittest.TestCase):
    def test_double_star_crosses_directories_single_star_does_not(self):
        self.assertTrue(gate.matches("frontend/src/design/a/b/c.ts", ["frontend/src/design/**"]))
        self.assertTrue(gate.matches("x/y/z.test.ts", ["**/*.test.ts"]))
        self.assertTrue(gate.matches("z.test.ts", ["**/*.test.ts"]))
        self.assertFalse(gate.matches("frontend/src/a/b.ts", ["frontend/src/*.ts"]))
        self.assertTrue(gate.matches("frontend/src/b.ts", ["frontend/src/*.ts"]))
        self.assertFalse(gate.matches("frontend/src/designer.ts", ["frontend/src/design/**"]))


# --- The ledger, the branch check and the human commands -------------------

import contextlib  # noqa: E402
import http.server  # noqa: E402
import io  # noqa: E402
import re  # noqa: E402
import threading  # noqa: E402
from unittest import mock  # noqa: E402

CHECK_GATE = {
    "reviewUrl": "http://127.0.0.1:9",
    "gated": ["frontend/**", "internal/app/menu/**"],
    "exempt": ["frontend/src/design/**", "frontend/prototype/**", "frontend/tools/**", "frontend/e2e/**",
               "frontend/package.json", "**/*.test.ts", "**/*.test.tsx", "**/*.pw.ts", "**/*_test.go"],
    "prototypeOwned": ["frontend/src/design/**", "frontend/prototype/**"],
    "digestInputs": ["frontend/src/design/**", "frontend/prototype/**", "frontend/package.json",
                     "docs/design/gate.json", "docs/design/retired.json"],
    "parityGroups": {"frontend/src/app/library/**": ["welcome", "library"],
                     "frontend/src/app/focus/**": ["focus", "compare"]},
}
H = {100: "a" * 64, 101: "b" * 64, 300: "c" * 64}
BRANCH = "feat/x"
EMAIL = "t@example.com"
ALL_PARITY = ["welcome", "library", "folder", "focus", "compare", "settings", "overlays"]
APP = "frontend/src/app/App.tsx"


def clean_env(**extra):
    drop = ("GITHUB_HEAD_REF", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT")
    env = {k: v for k, v in os.environ.items() if k not in drop}
    env.update(extra)
    return env


class GateRepo:
    """main with a three-state snapshot (100, 101, 300) and origin/main on it; feat/x checked out."""

    def __init__(self, base_ledger=None, gate_json=None):
        self.base = tempfile.mkdtemp(prefix="gate-check-")
        self.root = os.path.join(self.base, "repo")
        os.makedirs(self.root)
        sh(self.root, "git", "init", "-q", "-b", "main")
        sh(self.root, "git", "config", "user.email", EMAIL)
        sh(self.root, "git", "config", "user.name", "T")
        sh(self.root, "git", "config", "commit.gpgsign", "false")
        self.write("docs/design/gate.json", json.dumps(gate_json or CHECK_GATE))
        self.write("docs/design/retired.json", "[]\n")
        self.write("frontend/src/design/A.tsx", "export const A = 1\n")
        self.write("frontend/package.json", "{}\n")
        self.write("internal/engine.go", "package engine\n")
        self.snapshot(H)
        if base_ledger is not None:
            self.ledger(states=base_ledger)
        self.commit("init")
        sh(self.root, "git", "update-ref", "refs/remotes/origin/main", "HEAD")
        sh(self.root, "git", "checkout", "-q", "-b", BRANCH)

    def path(self, rel):
        return os.path.join(self.root, rel)

    def write(self, rel, text):
        write(self.root, rel, text)

    def read(self, rel):
        with open(self.path(rel), encoding="utf-8") as f:
            return f.read()

    def remove(self, rel):
        os.remove(self.path(rel))

    def git(self, *args):
        return sh(self.root, "git", *args)

    def commit(self, msg="change"):
        self.git("add", "-A")
        self.git("commit", "-q", "--allow-empty", "-m", msg)

    def on_main(self, fn):
        """Commits fn() on main, moves origin/main there and merges main into feat/x."""
        self.git("checkout", "-q", "main")
        fn()
        self.commit("main moves")
        self.git("update-ref", "refs/remotes/origin/main", "HEAD")
        self.git("checkout", "-q", BRANCH)
        self.git("merge", "-q", "--no-edit", "main")

    def snapshot(self, states):
        data = {
            "version": 1, "browser": "b", "extractor": "e", "sourceDigest": gate.digest(self.root),
            "states": {str(n): {"label": "State {}".format(n), "hash": h, "variants": {}} for n, h in states.items()},
        }
        self.write("docs/design/state-hashes.json", json.dumps(data, indent=2, sort_keys=True) + "\n")

    def ledger(self, states=None, retired=(), no_ui=(), by=EMAIL):
        """Writes approvals.json (and approvers.txt listing EMAIL, when there is none)."""
        data = {
            "states": [{"state": n, "label": "State {}".format(n), "hash": h, "by": by,
                        "at": "2026-11-11T10:00:00Z", "note": ""} for n, h in sorted((states or {}).items())],
            "retired": [{"state": n, "by": by, "at": "2026-11-11T10:00:00Z"} for n in retired],
            "noUi": list(no_ui),
        }
        self.write("docs/design/approvals.json", json.dumps(data) + "\n")
        if not os.path.exists(self.path("docs/design/approvers.txt")):
            self.write("docs/design/approvers.txt", EMAIL + "\n")

    def change(self, states="[]", retired="[]", ui="true", branch=BRANCH):
        text = "---\nbranch: {}\nstates: {}\nretired: {}\nui: {}\n---\n\nWhat this branch does.\n".format(
            branch, states, retired, ui)
        self.write("docs/design/changes/{}.md".format(gate.slug(branch)), text)

    def check(self, *args, cwd=None, env=None):
        out = subprocess.run(
            [sys.executable, GATE, "check", "--json", *args],
            cwd=cwd or self.root, env=env or clean_env(), stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        if out.returncode not in (0, 1):
            raise AssertionError("gate.py check exited {}: {}".format(out.returncode, out.stderr.decode()))
        data = json.loads(out.stdout.decode())
        reasons = {f["reason"] for f in data["findings"]}
        if (out.returncode == 0) != (not reasons):
            raise AssertionError("exit {} with findings {}".format(out.returncode, reasons))
        return out.returncode, reasons, data

    def close(self):
        shutil.rmtree(self.base, ignore_errors=True)


class ChangeFileTest(unittest.TestCase):
    def test_slug(self):
        self.assertEqual(gate.slug("feat/choix-2-desktop-design-463af0"), "feat-choix-2-desktop-design-463af0")
        self.assertEqual(gate.slug("Feat/Some__Thing!"), "feat-some-thing-")

    def test_parses_the_four_keys(self):
        c = gate.parse_change_file("---\nbranch: feat/x\nstates: [100, 101, 200-202]\nretired: []\nui: false\n---\n\nbody\n")
        self.assertEqual(c["branch"], "feat/x")
        self.assertEqual(c["states"], [100, 101, 200, 201, 202])
        self.assertEqual(c["retired"], [])
        self.assertIs(c["ui"], False)

    def test_rejects_malformed_front_matter(self):
        bad = [
            "branch: feat/x\nstates: []\nretired: []\nui: true\n",  # no front matter
            "---\nbranch: feat/x\nstates: []\nretired: []\n---\n",  # missing ui
            "---\nbranch: feat/x\nstates: []\nretired: []\nui: true\nextra: 1\n---\n",
            "---\nbranch: feat/x\nbranch: feat/x\nstates: []\nretired: []\nui: true\n---\n",
            "---\nbranch: feat/x\nstates: [1-]\nretired: []\nui: true\n---\n",
            "---\nbranch: feat/x\nstates: [5-3]\nretired: []\nui: true\n---\n",
            "---\nbranch: feat/x\nstates: []\nretired: []\nui: maybe\n---\n",
            "---\nbranch: \nstates: []\nretired: []\nui: true\n---\n",
        ]
        for text in bad:
            with self.assertRaises(gate.ChangeFileError, msg=text):
                gate.parse_change_file(text)

    def test_ranges(self):
        self.assertEqual(gate.format_ranges([300, 11, 12, 13, 30, 100]), "11-13, 30, 100, 300")
        self.assertEqual(gate.parse_numbers("12 13 20-22,30"), [12, 13, 20, 21, 22, 30])


class StatusesTest(unittest.TestCase):
    def test_approved_stale_new_retired_and_unregistered_ignored(self):
        snap = {100: "a", 101: "b", 102: "c"}
        ledger = {"states": [{"state": 100, "hash": "a"}, {"state": 101, "hash": "x"}, {"state": 999, "hash": "z"}]}
        got = gate.state_statuses(snap, ledger, [150, 101])
        self.assertEqual(got, {100: "approved", 101: "stale", 102: "new", 150: "retired"})

    def test_groups_match_the_registry(self):
        reg = os.path.join(HERE, "..", "..", "frontend", "prototype", "registry.ts")
        if not os.path.exists(reg):
            self.skipTest("frontend/prototype/registry.ts is absent")
        with open(reg, encoding="utf-8") as f:
            text = f.read()
        found = [(m.group(1), m.group(2), int(m.group(3)), int(m.group(4))) for m in re.finditer(
            r"\{\s*id:\s*'(\w+)',\s*title:\s*'([^']*)',\s*range:\s*\[(\d+),\s*(\d+)\]\s*\}", text)]
        self.assertEqual(found, [tuple(g) for g in gate.GROUPS])

    def test_full_label(self):
        self.assertEqual(gate.full_label(102, "Filled, six folders"), "Library · Filled, six folders")
        self.assertEqual(gate.full_label(11, "x"), "Welcome and shell · x")


class CheckTest(unittest.TestCase):
    def repo(self, **kw):
        r = GateRepo(**kw)
        self.addCleanup(r.close)
        return r

    def test_engine_only_branch_passes(self):
        r = self.repo()
        r.write("internal/engine.go", "package engine // changed\n")
        r.commit()
        for mode in ("edit", "merge"):
            self.assertEqual(r.check("--mode", mode)[:2], (0, set()))

    def test_prototype_only_branch_passes_edit_and_fails_merge_unapproved(self):
        r = self.repo()
        r.write("frontend/src/design/A.tsx", "export const A = 2\n")
        r.snapshot({**H, 100: "d" * 64})
        r.change(states="[100]")
        r.commit()
        self.assertEqual(r.check("--mode", "edit")[:2], (0, set()))
        self.assertEqual(r.check("--mode", "merge")[1], {"unapproved"})

    def test_assumed_app_change_needs_every_state_approved(self):
        r = self.repo()
        r.change()
        r.ledger(states={100: H[100], 101: H[101]})
        code, reasons, data = r.check("--assume-changed", APP)
        self.assertEqual(reasons, {"unapproved"})
        self.assertEqual(data["states"]["300"], "new")
        r.ledger(states=H)
        self.assertEqual(r.check("--assume-changed", APP)[:2], (0, set()))
        r.snapshot({**H, 300: "d" * 64})
        code, reasons, data = r.check("--assume-changed", APP)
        self.assertEqual(reasons, {"unapproved"})
        self.assertEqual(data["states"]["300"], "stale")

    def test_unapproved_holds_under_ui_false(self):
        r = self.repo(base_ledger={100: H[100]})
        r.change(ui="false")
        r.write(APP, "export {}\n")
        self.assertEqual(r.check("--mode", "edit")[1], {"unapproved"})

    def test_removed_state_passes_once_retired_everywhere(self):
        r = self.repo(base_ledger=H)
        r.write("docs/design/retired.json", "[101]\n")
        r.snapshot({100: H[100], 300: H[300]})
        r.change(retired="[101]")
        r.ledger(states=H, retired=[101])  # 101's old approval stays in the ledger
        for mode in ("edit", "merge"):
            self.assertEqual(r.check("--mode", mode, "--assume-changed", APP)[:2], (0, set()), mode)
        r.ledger(states=H)
        self.assertEqual(r.check("--assume-changed", APP)[1], {"unretired"})
        r.ledger(states=H, retired=[101])
        r.change(retired="[]")
        self.assertEqual(r.check("--assume-changed", APP)[1], {"unretired"})
        r.change(retired="[101]")
        r.write("docs/design/retired.json", "[]\n")
        r.snapshot({100: H[100], 300: H[300]})
        self.assertEqual(r.check("--assume-changed", APP)[1], {"unretired"})

    def test_removing_the_only_unapproved_state_fails_unretired(self):
        r = self.repo(base_ledger={100: H[100], 300: H[300]})
        r.write("frontend/src/design/A.tsx", "export const A = 2\n")
        r.snapshot({100: H[100], 300: H[300]})
        r.change()
        self.assertEqual(r.check("--mode", "edit", "--assume-changed", APP)[1], {"unretired"})

    def test_no_branch_on_detached_head_and_branch_flag(self):
        r = self.repo(base_ledger=H)
        r.change()
        r.write(APP, "export {}\n")
        r.commit()
        r.git("checkout", "-q", "--detach")
        self.assertEqual(r.check()[1], {"no-branch"})
        self.assertEqual(r.check("--branch", BRANCH)[:2], (0, set()))

    def test_github_head_ref_names_the_branch(self):
        r = self.repo(base_ledger=H)
        r.change(branch="feat/y")
        r.write(APP, "export {}\n")
        self.assertEqual(r.check()[1], {"no-change-file"})
        self.assertEqual(r.check(env=clean_env(GITHUB_HEAD_REF="feat/y"))[:2], (0, set()))

    def test_rebase_head_name_names_the_branch(self):
        r = self.repo(base_ledger=H)
        r.change()
        r.write(APP, "export {}\n")
        r.commit()
        r.git("checkout", "-q", "--detach")
        rel = r.git("rev-parse", "--git-path", "rebase-merge/head-name").strip()
        head_name = rel if os.path.isabs(rel) else os.path.join(r.root, rel)
        os.makedirs(os.path.dirname(head_name), exist_ok=True)
        with open(head_name, "w") as f:
            f.write("refs/heads/{}\n".format(BRANCH))
        self.assertEqual(r.check()[:2], (0, set()))

    def test_no_change_file(self):
        r = self.repo(base_ledger=H)
        r.write(APP, "export {}\n")
        self.assertEqual(r.check()[1], {"no-change-file"})

    def test_bad_change_file(self):
        r = self.repo(base_ledger=H)
        r.write(APP, "export {}\n")
        r.write("docs/design/changes/feat-x.md", "no front matter\n")
        self.assertEqual(r.check()[1], {"bad-change-file"})
        r.change(branch="feat/X")  # same slug, another branch
        self.assertEqual(r.check()[1], {"bad-change-file"})

    def test_change_file_not_touched_on_this_branch_is_bad(self):
        r = self.repo(base_ledger=H)
        r.on_main(lambda: r.change())
        r.write(APP, "export {}\n")
        self.assertEqual(r.check()[1], {"bad-change-file"})
        with open(r.path("docs/design/changes/feat-x.md"), "a") as f:
            f.write("Touched here.\n")
        self.assertEqual(r.check()[:2], (0, set()))

    def test_stale_snapshot(self):
        r = self.repo()
        r.change()
        r.write("frontend/src/design/A.tsx", "export const A = 2\n")
        self.assertEqual(r.check()[1], {"stale-snapshot"})

    def test_ui_false_but_prototype_changed(self):
        r = self.repo()
        r.change(ui="false")
        r.write("frontend/src/design/A.tsx", "export const A = 2\n")
        r.snapshot(H)
        self.assertEqual(r.check()[1], {"ui-false-but-prototype-changed"})
        r.write("frontend/src/design/A.tsx", "export const A = 1\n")
        r.snapshot({**H, 100: "d" * 64})
        self.assertEqual(r.check()[1], {"ui-false-but-prototype-changed"})
        r.snapshot(H)
        self.assertEqual(r.check()[:2], (0, set()))

    def test_unlisted_state(self):
        r = self.repo()
        r.write("frontend/src/design/A.tsx", "export const A = 2\n")
        r.snapshot({**H, 100: "d" * 64, 102: "e" * 64})
        r.change(states="[100]")
        r.ledger(states={100: "d" * 64, 102: "e" * 64})
        code, reasons, data = r.check("--mode", "merge")
        self.assertEqual(reasons, {"unlisted-state"})
        finding = [f for f in data["findings"] if f["reason"] == "unlisted-state"][0]
        self.assertEqual(finding["states"], [102])
        self.assertEqual(r.check("--mode", "edit")[:2], (0, set()))

    def test_retired_reused(self):
        r = self.repo(base_ledger=H)

        def retire():
            r.write("docs/design/retired.json", "[150]\n")
            r.snapshot(H)
        r.on_main(retire)
        r.write("docs/design/retired.json", "[]\n")
        r.snapshot({**H, 150: "e" * 64})
        r.change(states="[150]")
        r.ledger(states={**H, 150: "e" * 64})
        self.assertEqual(r.check("--mode", "merge")[1], {"retired-reused"})
        self.assertEqual(r.check("--mode", "edit")[:2], (0, set()))

    def test_no_ui_confirmation_survives_main_moving_and_dies_on_a_gated_change(self):
        r = self.repo(base_ledger=H)
        r.change(ui="false")
        r.write(APP, "export {}\n")
        r.write("frontend/e2e/parity/enabled.json", json.dumps(ALL_PARITY))
        r.commit()
        self.assertEqual(r.check("--mode", "merge")[1], {"no-ui-unconfirmed"})
        self.assertEqual(r.check("--mode", "edit")[:2], (0, set()))
        files = gate.no_ui_files_hash(r.root, [APP])
        r.ledger(states=H, no_ui=[{"branch": BRANCH, "files": files, "by": EMAIL, "at": "2026-11-11T10:00:00Z"}])
        r.commit()
        self.assertEqual(r.check("--mode", "merge")[:2], (0, set()))
        r.on_main(lambda: r.write("internal/other.go", "package other\n"))
        self.assertEqual(r.check("--mode", "merge")[:2], (0, set()))
        r.write(APP, "export const changed = 1\n")
        self.assertEqual(r.check("--mode", "merge")[1], {"no-ui-unconfirmed"})

    def test_parity_not_enabled(self):
        r = self.repo(base_ledger=H)
        r.change()
        r.write("frontend/src/app/library/List.tsx", "export {}\n")
        r.write("frontend/src/app/library/List.test.ts", "export {}\n")
        self.assertEqual(r.check("--mode", "merge")[1], {"parity-not-enabled"})  # no enabled.json
        self.assertEqual(r.check("--mode", "edit")[:2], (0, set()))
        r.write("frontend/e2e/parity/enabled.json", '["welcome"]\n')
        self.assertEqual(r.check("--mode", "merge")[1], {"parity-not-enabled"})
        r.write("frontend/e2e/parity/enabled.json", '["welcome", "library"]\n')
        self.assertEqual(r.check("--mode", "merge")[:2], (0, set()))
        # A file no parityGroups glob names maps to every group but gallery; native is always satisfied.
        r.write("frontend/src/main.tsx", "export {}\n")
        code, reasons, data = r.check("--mode", "merge")
        self.assertEqual(reasons, {"parity-not-enabled"})
        finding = [f for f in data["findings"] if f["reason"] == "parity-not-enabled"][0]
        self.assertEqual(sorted(finding["groups"]), sorted(set(ALL_PARITY) - {"welcome", "library"}))
        r.write("frontend/e2e/parity/enabled.json", json.dumps(ALL_PARITY))
        self.assertEqual(r.check("--mode", "merge")[:2], (0, set()))

    def test_renamed_gated_file_is_seen_under_both_names(self):
        r = self.repo(base_ledger=H)
        r.on_main(lambda: r.write("frontend/src/app/library/List.tsx", "export const list = [1, 2, 3, 4, 5]\n"))
        r.change()
        os.makedirs(r.path("frontend/src/app/focus"))
        r.git("mv", "frontend/src/app/library/List.tsx", "frontend/src/app/focus/List.tsx")
        names = ["frontend/src/app/focus/List.tsx", "frontend/src/app/library/List.tsx"]
        for _ in range(2):
            gated = r.check()[2]["gated"]
            self.assertTrue(set(names) <= set(gated), gated)
            r.commit()

    def test_changed_paths_include_deleted_staged_unstaged_and_untracked(self):
        r = self.repo(base_ledger=H)
        r.on_main(lambda: [r.write("frontend/src/app/a.ts", "a\n"), r.write("frontend/src/app/b.ts", "b\n"),
                           r.write("frontend/src/app/c.ts", "c\n")])
        r.change()
        r.remove("frontend/src/app/a.ts")
        r.write("frontend/src/app/b.ts", "b2\n")
        r.git("add", "frontend/src/app/b.ts")
        r.write("frontend/src/app/c.ts", "c2\n")
        r.write("frontend/src/app/d.ts", "d\n")
        r.write("internal/app/menu/menu.go", "package menu\n")
        gated = r.check()[2]["gated"]
        for p in ["frontend/src/app/a.ts", "frontend/src/app/b.ts", "frontend/src/app/c.ts", "frontend/src/app/d.ts",
                  "internal/app/menu/menu.go"]:
            self.assertIn(p, gated)
        self.assertNotIn("frontend/package.json", gated)

    def test_same_answer_from_root_subdirectory_and_linked_worktree(self):
        r = self.repo(base_ledger={100: H[100]})
        r.change()
        r.write(APP, "export {}\n")
        r.commit()
        at_root = r.check("--mode", "merge")
        self.assertEqual(at_root[1], {"unapproved", "parity-not-enabled"})
        self.assertEqual(r.check("--mode", "merge", cwd=r.path("frontend")), at_root)
        r.git("checkout", "-q", "main")
        wt = os.path.join(r.base, "wt")
        r.git("worktree", "add", "-q", wt, BRANCH)
        self.assertEqual(r.check("--mode", "merge", cwd=wt), at_root)
        self.assertEqual(r.check("--mode", "merge", cwd=os.path.join(wt, "frontend")), at_root)

    def test_missing_ledger_is_empty(self):
        r = self.repo()
        r.change()
        code, reasons, data = r.check("--assume-changed", APP)
        self.assertEqual(reasons, {"unapproved"})
        self.assertEqual(set(data["states"].values()), {"new"})
        self.assertFalse(os.path.exists(r.path("docs/design/approvals.json")))

    def test_assumed_paths_are_resolved_against_the_repository(self):
        r = self.repo(base_ledger={100: H[100], 101: H[101]})  # 300 is unapproved
        r.change()
        r.commit()
        frontend = r.path("frontend")
        forms = [
            ("absolute", r.path(APP), r.root, APP),
            ("relative to a subdirectory", "src/app/App.tsx", frontend, APP),
            ("with ../", "../frontend/src/app/App.tsx", frontend, APP),
            ("wrong case", "Frontend/Src/app/App.tsx", r.root, "Frontend/Src/app/App.tsx"),
        ]
        for name, path, cwd, rel in forms:
            code, reasons, data = r.check("--assume-changed", path, cwd=cwd)
            self.assertEqual(reasons, {"unapproved"}, name)
            self.assertIn(rel, data["gated"], name)
        outside = os.path.join(r.base, "elsewhere", "frontend", "src", "app", "App.tsx")
        for path, cwd in ((outside, r.root), ("../../elsewhere/frontend/src/App.tsx", frontend)):
            code, reasons, data = r.check("--assume-changed", path, cwd=cwd)
            self.assertEqual((code, reasons, data["gated"]), (0, set(), []), path)

    def test_wrong_case_exempt_path_stays_exempt(self):
        r = self.repo()
        r.change()
        r.commit()
        self.assertEqual(r.check("--assume-changed", "Frontend/Src/Design/B.tsx")[2]["gated"], [])

    def test_bad_ledger_entries_are_ignored_and_reported(self):
        r = self.repo()
        r.change()
        r.ledger(states={100: H[100], 101: H[101]})
        with open(r.path("docs/design/approvals.json")) as f:
            data = json.load(f)
        data["states"].append({"state": 300, "label": "x", "hash": H[300], "by": "agent@example.com", "at": "", "note": ""})
        r.write("docs/design/approvals.json", json.dumps(data))
        code, reasons, data = r.check("--assume-changed", APP)
        self.assertEqual(reasons, {"bad-ledger", "unapproved"})
        self.assertEqual(data["states"]["300"], "new")
        bad = [f for f in data["findings"] if f["reason"] == "bad-ledger"][0]
        self.assertEqual(bad["states"], [300])
        self.assertIn("agent@example.com", bad["message"])
        r.remove("docs/design/approvers.txt")  # no approvers: every entry is ignored
        code, reasons, data = r.check("--assume-changed", APP)
        self.assertEqual(reasons, {"bad-ledger", "unapproved"})
        self.assertEqual(set(data["states"].values()), {"new"})
        self.assertIn("approvers.txt is missing", [f for f in data["findings"] if f["reason"] == "bad-ledger"][0]["message"])

    def test_malformed_ledger_is_a_finding(self):
        r = self.repo()
        r.change()
        for text in ("[]\n", '{"states": [{"state": "100", "hash": "a", "by": "t@example.com"}]}\n'):
            r.write("docs/design/approvals.json", text)
            code, reasons, data = r.check("--assume-changed", APP)
            self.assertEqual(reasons, {"bad-ledger", "unapproved"})

    def test_a_ledger_only_branch_with_a_malformed_ledger_is_a_finding(self):
        r = self.repo(base_ledger=H)
        r.write("docs/design/approvals.json", "{not json\n")
        r.commit()
        for mode in ("edit", "merge"):
            code, reasons, data = r.check("--mode", mode)
            self.assertEqual((code, reasons), (1, {"bad-ledger"}), mode)
            self.assertEqual(data["gated"], [])

    def test_a_ledger_only_branch_with_an_entry_by_a_non_approver_is_a_finding(self):
        r = self.repo(base_ledger=H)
        with open(r.path("docs/design/approvals.json")) as f:
            data = json.load(f)
        data["states"].append({"state": 300, "label": "x", "hash": H[300], "by": "agent@example.com", "at": "", "note": ""})
        r.write("docs/design/approvals.json", json.dumps(data))
        r.commit()
        code, reasons, data = r.check("--mode", "merge")
        self.assertEqual((code, reasons), (1, {"bad-ledger"}))
        bad = [f for f in data["findings"] if f["reason"] == "bad-ledger"][0]
        self.assertEqual(bad["states"], [300])
        self.assertIn("agent@example.com", bad["message"])

    def test_a_ledger_only_branch_with_valid_owner_entries_has_no_finding(self):
        r = self.repo(base_ledger={100: H[100]})
        r.ledger(states=H)
        r.commit()
        for mode in ("edit", "merge"):
            self.assertEqual(r.check("--mode", mode)[:2], (0, set()), mode)

    def test_no_base(self):
        r = self.repo(base_ledger=H)
        r.change()
        r.write(APP, "export {}\n")
        r.git("update-ref", "-d", "refs/remotes/origin/main")
        self.assertEqual(r.check()[:2], (0, set()))  # falls back to the local main
        r.git("branch", "-D", "main")
        code, reasons, data = r.check()
        self.assertEqual((code, reasons), (1, {"no-base"}))
        self.assertEqual(r.check("--base", "nonexistent")[1], {"no-base"})


class StatusTest(unittest.TestCase):
    def setUp(self):
        self.r = GateRepo()
        self.addCleanup(self.r.close)

    def status(self, *args, cwd=None):
        out = subprocess.run([sys.executable, GATE, "status", *args], cwd=cwd or self.r.root, env=clean_env(),
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(out.returncode, 0, out.stderr.decode())
        return out.stdout.decode()

    def test_lists_states_as_links_and_asks_for_init(self):
        self.r.write("docs/design/retired.json", "[150]\n")
        self.r.snapshot(H)
        self.r.change(states="[100, 101, 300]")
        text = self.status()
        self.assertIn("[#100 Library · State 100](http://127.0.0.1:9/#s100)", text)
        self.assertIn("[#300 Focus · State 300](http://127.0.0.1:9/#s300)", text)
        self.assertIn("unapproved", text)
        self.assertIn("task design:init", text)
        self.assertIn("#150", text)  # retired

    def test_never_fails(self):
        empty = tempfile.mkdtemp(prefix="gate-status-empty-")
        self.addCleanup(shutil.rmtree, empty, True)
        self.assertIn("gate", self.status(cwd=empty))
        self.r.git("checkout", "-q", "--detach")
        self.r.write(APP, "x\n")
        self.assertIn("no-branch", self.status())

    def test_header_names_the_branch_on_the_shortcut_path(self):
        self.r.write("internal/engine.go", "package engine // changed\n")
        text = self.status()
        self.assertTrue(text.startswith("# Design gate: feat/x\n"), text[:80])
        self.assertIn("## Merge check: pass", text)

    def test_reports_no_base(self):
        self.r.git("update-ref", "-d", "refs/remotes/origin/main")
        self.r.git("branch", "-D", "main")
        self.assertIn("no-base", self.status())


class _BuildHandler(http.server.BaseHTTPRequestHandler):
    body = b"{}"

    def do_GET(self):  # noqa: N802
        self.send_response(200 if self.path == "/__build.json" else 404)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(self.body)

    def log_message(self, *args):
        pass


@contextlib.contextmanager
def chdir(path):
    old = os.getcwd()
    os.chdir(path)
    try:
        yield
    finally:
        os.chdir(old)


class HumanTest(unittest.TestCase):
    def setUp(self):
        self.r = GateRepo()
        self.addCleanup(self.r.close)
        self.fetched = []
        self.snapshot_checks = []

    def approvers(self, *emails):
        self.r.write("docs/design/approvers.txt", "".join(e + "\n" for e in emails))

    def run_human(self, *argv, answer="y", terminal=True, env=None, build=None, snapshot_rc=0, real_fetch=False):
        """Runs gate.main in-process in the temporary repository. `answer` is one answer, a list of
        answers in order, or an exception to raise at the prompt; `build` may be an exception to raise."""
        answers = list(answer) if isinstance(answer, list) else None

        def fake_ask(prompt):
            print(prompt)
            if isinstance(answer, type) and issubclass(answer, BaseException):
                raise answer()
            return answers.pop(0) if answers is not None else answer

        def fake_fetch(url):
            self.fetched.append(url)
            if isinstance(build, BaseException):
                raise build
            return build if build is not None else {"sourceDigest": gate.digest(self.r.root)}

        def fake_snapshot(root, spec):
            self.snapshot_checks.append(spec)
            return snapshot_rc

        out, err = io.StringIO(), io.StringIO()
        patches = [
            mock.patch.dict(os.environ, env if env is not None else clean_env(), clear=True),
            mock.patch.object(gate_owner, "is_terminal", lambda: terminal),
            mock.patch.object(gate_owner, "agent_ancestor", lambda: None),  # these tests run inside an agent
            mock.patch.object(gate_owner, "ask", fake_ask),
            mock.patch.object(gate_owner, "run_snapshot_check", fake_snapshot),
        ]
        if not real_fetch:
            patches.append(mock.patch.object(gate_owner, "fetch_json", fake_fetch))
        with contextlib.ExitStack() as stack:
            for p in patches:
                stack.enter_context(p)
            stack.enter_context(chdir(self.r.root))
            stack.enter_context(contextlib.redirect_stdout(out))
            stack.enter_context(contextlib.redirect_stderr(err))
            code = gate.main(list(argv))
        return code, out.getvalue(), err.getvalue()

    def ledger(self):
        return json.loads(self.r.read("docs/design/approvals.json"))

    def assertNoLedgerFiles(self):
        self.assertFalse(os.path.exists(self.r.path("docs/design/approvals.json")))
        self.assertFalse(os.path.exists(self.r.path("docs/design/approvers.txt")))

    def test_refuse_on_a_pipe(self):
        for argv in (["init"], ["approve", "100"], ["confirm-no-ui"]):
            out = subprocess.run([sys.executable, GATE, *argv], cwd=self.r.root, env=clean_env(),
                                 stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.assertEqual(out.returncode, 2, argv)
            self.assertIn("needs a terminal", out.stderr.decode(), argv)
            self.assertNotIn("Traceback", out.stderr.decode(), argv)
        self.assertNoLedgerFiles()

    def test_refuse_under_claude_code(self):
        self.approvers(EMAIL)
        for var in ("CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"):
            for argv in (["init"], ["approve", "100"], ["confirm-no-ui"]):
                code, out, err = self.run_human(*argv, env=clean_env(**{var: "1"}))
                self.assertNotEqual(code, 0, (var, argv))
                self.assertIn("needs a terminal", err, (var, argv))
        self.assertFalse(os.path.exists(self.r.path("docs/design/approvals.json")))

    def test_writers_refuse_without_a_human(self):
        cases = [(False, clean_env()), (True, clean_env(CLAUDECODE="1")), (True, clean_env(CLAUDE_CODE_ENTRYPOINT="cli"))]
        for terminal, env in cases:
            with mock.patch.dict(os.environ, env, clear=True), \
                    mock.patch.object(gate_owner, "agent_ancestor", lambda: None), \
                    mock.patch.object(gate_owner, "is_terminal", lambda: terminal):
                with self.assertRaisesRegex(gate.GateError, "needs a terminal"):
                    gate_owner.write_ledger(self.r.root, {"states": [], "retired": [], "noUi": []})
                with self.assertRaisesRegex(gate.GateError, "needs a terminal"):
                    gate_owner.write_approvers(self.r.root, [EMAIL])
        self.assertNoLedgerFiles()

    def test_importing_gate_never_loads_the_writers(self):
        code = ("import sys; sys.path.insert(0, {!r}); import gate; gate.main(['status']); "
                "gate.main(['check', '--assume-changed', 'frontend/src/app/App.tsx']); "
                "print('LOADED' if 'gate_owner' in sys.modules else 'CLEAN')").format(HERE)
        out = subprocess.run([sys.executable, "-c", code], cwd=self.r.root, env=clean_env(),
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(out.stdout.decode().strip().splitlines()[-1], "CLEAN", out.stderr.decode())
        with open(GATE, encoding="utf-8") as f:
            top_level = [ln for ln in f if ln.startswith(("import ", "from "))]
        self.assertFalse([ln for ln in top_level if "gate_owner" in ln])

    def test_ctrl_c_at_a_prompt(self):
        code, out, err = self.run_human("init", answer=KeyboardInterrupt)
        self.assertEqual(code, 130)
        self.assertEqual(err.strip(), "gate: interrupted; nothing written")
        self.assertNoLedgerFiles()

    def test_init(self):
        code, out, err = self.run_human("init", answer="n")
        self.assertNotEqual(code, 0)
        self.assertNoLedgerFiles()
        code, out, err = self.run_human("init")
        self.assertEqual(code, 0, err)
        self.assertIn(EMAIL, self.r.read("docs/design/approvers.txt").split())
        self.assertEqual(self.ledger(), {"states": [], "retired": [], "noUi": []})
        written = self.r.read("docs/design/approvers.txt") + self.r.read("docs/design/approvals.json")
        self.assertNotIn(self.r.base, written)
        code, out, err = self.run_human("init")
        self.assertNotEqual(code, 0)
        self.assertIn("already", err)

    def test_init_when_only_the_ledger_is_missing(self):
        self.approvers("owner@example.com")
        code, out, err = self.run_human("init")
        self.assertEqual(code, 0, err)
        self.assertIn("approvers.txt exists. This writes an empty approval ledger", out)
        self.assertEqual(self.r.read("docs/design/approvers.txt"), "owner@example.com\n")  # untouched
        self.assertEqual(self.ledger(), {"states": [], "retired": [], "noUi": []})

    def test_init_adopts_an_existing_ledger_only_on_a_second_yes(self):
        self.r.ledger(states=H, retired=[150])
        self.r.remove("docs/design/approvers.txt")
        before = self.r.read("docs/design/approvals.json")
        code, out, err = self.run_human("init", answer=["y", "n"])
        self.assertEqual(code, 1)
        self.assertIn("holds 4 entries (3 approvals, 1 retirements, 0 no-UI confirmations)", out)
        self.assertIn("Adopt these 4 entries", out)
        self.assertFalse(os.path.exists(self.r.path("docs/design/approvers.txt")))
        code, out, err = self.run_human("init", answer=["y", "y"])
        self.assertEqual(code, 0, err)
        self.assertIn(EMAIL, self.r.read("docs/design/approvers.txt").split())
        self.assertEqual(self.r.read("docs/design/approvals.json"), before)

    def test_approve_refuses_an_unknown_email(self):
        self.approvers("owner@example.com")
        code, out, err = self.run_human("approve", "100")
        self.assertNotEqual(code, 0)
        self.assertIn("{} is not in docs/design/approvers.txt".format(EMAIL), err)
        self.assertFalse(os.path.exists(self.r.path("docs/design/approvals.json")))

    def test_approve_refuses_a_malformed_ledger_without_a_traceback(self):
        self.approvers(EMAIL)
        for text, why in (("[]\n", "not a JSON object"), ('{"states": [{"state": "100", "hash": "a", "by": "x"}]}\n',
                                                             "states[0].state is not an integer")):
            self.r.write("docs/design/approvals.json", text)
            code, out, err = self.run_human("approve", "100")
            self.assertEqual(code, 2)
            self.assertIn("gate: docs/design/approvals.json is malformed: " + why, err)
            self.assertNotIn("Traceback", err)

    def test_approve_refuses_a_build_that_does_not_match(self):
        handler = type("H", (_BuildHandler,), {"body": json.dumps({"sourceDigest": "0" * 64}).encode()})
        server = http.server.HTTPServer(("127.0.0.1", 0), handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        review = "http://127.0.0.1:{}".format(server.server_address[1])
        self.r.write("docs/design/gate.json", json.dumps(dict(CHECK_GATE, reviewUrl=review)))
        self.r.snapshot(H)
        self.approvers(EMAIL)
        code, out, err = self.run_human("approve", "100", real_fetch=True)
        self.assertEqual(code, 2)
        self.assertIn("the build being served at {} was not built from this source".format(review), err)
        self.assertIn("rebuild with `task design:build` and restart `task design:serve`", err)
        for body in (b'{"sourceDigest": ""}', b"{}"):
            handler.body = body
            code, out, err = self.run_human("approve", "100", real_fetch=True)
            self.assertEqual(code, 2)
            self.assertIn("the build being served at {} has no digest; rebuild with `task design:build` and "
                          "restart `task design:serve`".format(review), err)
        self.assertFalse(os.path.exists(self.r.path("docs/design/approvals.json")))
        handler.body = json.dumps({"sourceDigest": gate.digest(self.r.root)}).encode()
        code, out, err = self.run_human("approve", "100", real_fetch=True)
        self.assertEqual(code, 0, err)

    def test_approve_refuses_an_unreachable_build(self):
        import urllib.error
        self.approvers(EMAIL)
        code, out, err = self.run_human("approve", "100", build=urllib.error.URLError("connection refused"))
        self.assertEqual(code, 2)
        self.assertIn("cannot reach http://127.0.0.1:9/__build.json", err)
        self.assertIn("`task design:build && task design:serve`", err)

    def test_approve_refuses_unregistered_stale_snapshot_and_failed_check(self):
        self.approvers(EMAIL)
        code, out, err = self.run_human("approve", "100", "102")
        self.assertEqual(code, 2)
        self.assertIn("gate: not registered: #102", err)
        code, out, err = self.run_human("approve", "100", snapshot_rc=1)
        self.assertEqual(code, 2)
        self.assertIn("snapshot --check --states 100 failed", err)
        code, out, err = self.run_human("approve", "100", build={"sourceDigest": "f" * 64})
        self.assertEqual(code, 2)
        self.assertIn("was not built from this source", err)
        self.r.write("frontend/src/design/A.tsx", "export const A = 2\n")
        code, out, err = self.run_human("approve", "100")
        self.assertEqual(code, 2)
        self.assertIn("docs/design/state-hashes.json is not current", err)
        self.assertFalse(os.path.exists(self.r.path("docs/design/approvals.json")))

    def test_approve_writes_a_sorted_ledger_and_replaces(self):
        self.approvers(EMAIL)
        code, out, err = self.run_human("approve", "300", "100-101", "--note", "looks right")
        self.assertEqual(code, 0, err)
        self.assertEqual(self.snapshot_checks, ["100-101,300"])
        self.assertEqual(self.fetched, ["http://127.0.0.1:9/__build.json"])
        self.assertIn("#100 Library · State 100 — http://127.0.0.1:9/#s100", out)
        self.assertIn(H[100], out)
        self.assertIn("Approve these 3 states? [y/N]", out)
        led = self.ledger()
        self.assertEqual([e["state"] for e in led["states"]], [100, 101, 300])
        e = led["states"][0]
        self.assertEqual((e["hash"], e["by"], e["note"], e["label"]), (H[100], EMAIL, "looks right", "State 100"))
        self.assertRegex(e["at"], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")
        lines = self.r.read("docs/design/approvals.json").splitlines()
        self.assertEqual(len([ln for ln in lines if '"state"' in ln]), 3)  # one entry per line
        self.r.snapshot({**H, 100: "d" * 64})
        code, out, err = self.run_human("approve", "100")
        self.assertEqual(code, 0, err)
        led = self.ledger()
        self.assertEqual([(x["state"], x["hash"]) for x in led["states"]],
                         [(100, "d" * 64), (101, H[101]), (300, H[300])])
        code, out, err = self.run_human("approve", "all", answer="n")
        self.assertNotEqual(code, 0)
        self.assertEqual(self.ledger(), led)
        code, out, err = self.run_human("approve", "all")
        self.assertEqual(code, 0, err)
        self.assertEqual(self.snapshot_checks[-1], "100-101,300")

    def test_approve_retire(self):
        self.approvers(EMAIL)
        code, out, err = self.run_human("approve", "--retire", "100")
        self.assertEqual(code, 2)
        self.assertIn("still registered", err)
        self.r.write("docs/design/retired.json", "[101]\n")
        self.r.snapshot({100: H[100], 300: H[300]})
        code, out, err = self.run_human("approve", "--retire", "101")
        self.assertEqual(code, 0, err)
        self.assertEqual([x["state"] for x in self.ledger()["retired"]], [101])
        self.assertEqual(self.ledger()["retired"][0]["by"], EMAIL)

    def test_confirm_no_ui_hashes_committed_changes_only(self):
        self.r.ledger(states=H)
        self.approvers(EMAIL)
        self.r.change(ui="false")
        self.r.write(APP, "export {}\n")
        self.r.write("frontend/e2e/parity/enabled.json", json.dumps(ALL_PARITY))
        code, out, err = self.run_human("confirm-no-ui")
        self.assertEqual(code, 2)
        self.assertIn("commit the gated changes first; uncommitted or untracked: " + APP, err)
        self.r.commit()
        self.assertEqual(self.r.check("--mode", "merge")[1], {"no-ui-unconfirmed"})
        code, out, err = self.run_human("confirm-no-ui", answer="n")
        self.assertEqual(code, 1)
        code, out, err = self.run_human("confirm-no-ui")
        self.assertEqual(code, 0, err)
        self.assertIn(APP, out)
        entry = self.ledger()["noUi"][0]
        self.assertEqual((entry["branch"], entry["by"]), (BRANCH, EMAIL))
        self.assertEqual(self.r.check("--mode", "merge")[:2], (0, set()))
        self.r.commit()
        self.assertEqual(self.r.check("--mode", "merge")[:2], (0, set()))  # what CI sees
        self.approvers("owner@example.com")
        code, out, err = self.run_human("confirm-no-ui")
        self.assertEqual(code, 2)
        self.assertIn("is not in docs/design/approvers.txt", err)


class _StatusHandler(http.server.BaseHTTPRequestHandler):
    """Answers every GET with `status` and `body`."""
    status = 200
    body = b"{}"

    def do_GET(self):  # noqa: N802
        self.send_response(self.status)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(self.body)

    def log_message(self, *args):
        pass


class CarryOverTest(unittest.TestCase):
    """Items deferred from Task 7's review."""

    def repo(self, **kw):
        r = GateRepo(**kw)
        self.addCleanup(r.close)
        return r

    def test_a_directory_target_is_gated_when_a_gated_glob_lies_under_it(self):
        r = self.repo(base_ledger={100: H[100], 101: H[101]})  # 300 is unapproved
        r.change()
        r.commit()
        os.makedirs(r.path("internal/app/menu"))
        os.makedirs(r.path("frontend/src/app"))
        for target in ("frontend", "frontend/", "frontend/src/app", "internal/app/menu", "internal"):
            code, reasons, data = r.check("--assume-changed", target)
            self.assertEqual(reasons, {"unapproved"}, target)
            self.assertIn(target.rstrip("/"), data["gated"], target)
        for target in ("frontend/src/design", "docs", "frontend/e2e"):
            os.makedirs(r.path(target), exist_ok=True)
            code, reasons, data = r.check("--assume-changed", target)
            self.assertEqual((code, data["gated"]), (0, []), target)
        self.assertTrue(gate.dir_is_gated("internal/app", CHECK_GATE))
        self.assertTrue(gate.dir_is_gated("", CHECK_GATE))  # the repository root holds everything
        self.assertFalse(gate.dir_is_gated("internal/engine", CHECK_GATE))
        self.assertFalse(gate.dir_is_gated("frontend/prototype", CHECK_GATE))

    def test_unicode_paths_are_compared_in_nfc(self):
        import unicodedata
        base = tempfile.mkdtemp(prefix="gate-nfc-")
        self.addCleanup(shutil.rmtree, base, True)
        nfd = unicodedata.normalize("NFD", "café")
        nfc = unicodedata.normalize("NFC", nfd)
        self.assertNotEqual(nfd, nfc)
        for on_disk, given in ((nfd, nfc), (nfc, nfd)):
            root = os.path.join(base, on_disk + ("-a" if on_disk == nfd else "-b"))
            os.makedirs(os.path.join(root, "frontend"), exist_ok=True)
            given_root = os.path.join(base, given + ("-a" if on_disk == nfd else "-b"))
            rel = gate.repo_relative(root, os.path.join(given_root, "frontend", nfd + ".tsx"))
            self.assertEqual(rel, "frontend/" + nfc + ".tsx")
            self.assertEqual(rel, unicodedata.normalize("NFC", rel))

    def test_the_low_level_writer_needs_a_human(self):
        r = self.repo()
        with mock.patch.dict(os.environ, clean_env(CLAUDECODE="1"), clear=True), \
                mock.patch.object(gate_owner, "agent_ancestor", lambda: None), \
                mock.patch.object(gate_owner, "is_terminal", lambda: True):
            for rel in ("docs/design/approvals.json", "docs/design/approvers.txt"):
                with self.assertRaisesRegex(gate.GateError, "needs a terminal"):
                    gate_owner._write(r.root, rel, "{}\n")
        self.assertFalse(os.path.exists(r.path("docs/design/approvals.json")))
        self.assertFalse(os.path.exists(r.path("docs/design/approvers.txt")))

    def _serve(self, status, body):
        handler = type("S", (_StatusHandler,), {"status": status, "body": body})
        server = http.server.HTTPServer(("127.0.0.1", 0), handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return "http://127.0.0.1:{}".format(server.server_address[1])

    def test_a_server_that_is_not_a_prototype_build_is_named_as_such(self):
        for status, body in ((404, b"not found"), (200, b"<html>hello</html>"), (200, b"[1, 2]")):
            r = self.repo()
            review = self._serve(status, body)
            r.write("docs/design/gate.json", json.dumps(dict(CHECK_GATE, reviewUrl=review)))
            r.snapshot(H)
            r.write("docs/design/approvers.txt", EMAIL + "\n")
            t = HumanTest("test_init")
            t.r = r
            t.fetched, t.snapshot_checks = [], []
            code, out, err = t.run_human("approve", "100", real_fetch=True)
            self.assertEqual(code, 2, (status, body))
            self.assertIn("the server at {} is not serving a prototype build".format(review), err)
            self.assertNotIn("cannot reach", err)
            self.assertFalse(os.path.exists(r.path("docs/design/approvals.json")))

    def test_a_build_from_another_source_was_not_built_from_this_source(self):
        r = self.repo()
        r.write("docs/design/approvers.txt", EMAIL + "\n")
        t = HumanTest("test_init")
        t.r = r
        t.fetched, t.snapshot_checks = [], []
        code, out, err = t.run_human("approve", "100", build={"sourceDigest": "f" * 64})
        self.assertEqual(code, 2)
        self.assertIn("was not built from this source", err)
        self.assertNotIn("older", err)

    def test_approvers_changed_is_a_merge_finding_when_the_base_has_the_file(self):
        r = self.repo(base_ledger=H)  # the base has approvers.txt (t@example.com)
        r.change()
        r.write("frontend/e2e/parity/enabled.json", json.dumps(ALL_PARITY))
        r.commit()
        self.assertEqual(r.check("--mode", "merge")[:2], (0, set()))
        r.write("docs/design/approvers.txt", EMAIL + "\nother@example.com\n")
        code, reasons, data = r.check("--mode", "merge")
        self.assertEqual(reasons, {"approvers-changed"})
        message = [f for f in data["findings"]][0]["message"]
        self.assertIn("this finding does not clear: the owner merges this pull request themselves, bypassing the "
                      "required check, after reading the diff of docs/design/approvers.txt", message)
        self.assertEqual(r.check("--mode", "edit")[:2], (0, set()))
        r.commit()
        self.assertEqual(r.check("--mode", "merge")[1], {"approvers-changed"})
        r.remove("docs/design/approvers.txt")
        self.assertIn("approvers-changed", r.check("--mode", "merge")[1])

    def test_approvers_added_on_a_branch_whose_base_has_none_is_no_finding(self):
        r = self.repo()
        r.change()
        r.ledger(states=H)  # also writes approvers.txt, which the base lacks
        r.write("frontend/e2e/parity/enabled.json", json.dumps(ALL_PARITY))
        r.commit()
        self.assertNotIn("approvers-changed", r.check("--mode", "merge")[1])

    def test_an_approvers_only_branch_is_still_reported(self):
        r = self.repo(base_ledger=H)
        r.write("docs/design/approvers.txt", EMAIL + "\nother@example.com\n")
        r.commit()
        self.assertEqual(r.check("--mode", "merge")[1], {"approvers-changed"})

    MALFORMED_SNAPSHOTS = ("[]\n", '{"states": []}\n', '{"states": {"x": {"hash": "a"}}}\n',
                           '{"states": {"100": "a"}}\n', '{"states": {"100": {"hash": 5}}}\n',
                           '{"sourceDigest": 5, "states": {}}\n', "{not json\n")
    MALFORMED_RETIRED = ('{"a": 1}\n', '["100"]\n', "5\n", "[true]\n", "[1.5]\n", "{not json\n")

    def test_a_malformed_snapshot_is_a_finding_not_a_traceback(self):
        for text in self.MALFORMED_SNAPSHOTS:
            r = self.repo(base_ledger=H)
            r.change()
            r.write("docs/design/state-hashes.json", text)
            for mode in ("edit", "merge"):
                code, reasons, data = r.check("--mode", mode, "--assume-changed", APP)
                self.assertEqual(code, 1, text)
                self.assertIn("bad-snapshot", reasons, text)
            out = subprocess.run([sys.executable, GATE, "status"], cwd=r.root, env=clean_env(),
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.assertEqual(out.returncode, 0)
            self.assertIn("bad-snapshot", out.stdout.decode(), text)
            self.assertNotIn("Traceback", out.stderr.decode() + out.stdout.decode())

    def test_a_malformed_base_snapshot_is_a_finding(self):
        r = self.repo(base_ledger=H)
        r.on_main(lambda: r.write("docs/design/state-hashes.json", '{"states": []}\n'))
        r.snapshot(H)
        r.change()
        code, reasons, data = r.check("--mode", "merge", "--assume-changed", APP)
        self.assertIn("bad-snapshot", reasons)
        msg = [f["message"] for f in data["findings"] if f["reason"] == "bad-snapshot"][0]
        self.assertIn("the base's", msg)

    def test_a_malformed_retired_file_is_a_finding_not_a_traceback(self):
        for text in self.MALFORMED_RETIRED:
            r = self.repo(base_ledger=H)
            r.change()
            r.write("docs/design/retired.json", text)
            r.snapshot(H)
            for mode in ("edit", "merge"):
                code, reasons, data = r.check("--mode", mode, "--assume-changed", APP)
                self.assertEqual(code, 1, text)
                self.assertIn("bad-retired", reasons, text)

    def test_approve_reports_a_malformed_snapshot_or_retired_file_clearly(self):
        r = self.repo()
        r.write("docs/design/approvers.txt", EMAIL + "\n")
        t = HumanTest("test_init")
        t.r = r
        t.fetched, t.snapshot_checks = [], []
        r.write("docs/design/state-hashes.json", '{"states": []}\n')
        code, out, err = t.run_human("approve", "100")
        self.assertEqual(code, 2)
        self.assertIn("gate: docs/design/state-hashes.json is malformed", err)
        self.assertNotIn("Traceback", err)
        r.write("docs/design/retired.json", '["x"]\n')
        r.snapshot({100: H[100]})
        code, out, err = t.run_human("approve", "--retire", "101")
        self.assertEqual(code, 2)
        self.assertIn("gate: docs/design/retired.json is malformed", err)


# What `ps` showed on the owner's machine (2026-11-11): an agent's shell, and a shell in the desktop app's
# Terminal panel. (pid, ppid, executable, argv)
AGENT_TREE = [
    (89683, 21554, "/bin/zsh", "/bin/zsh -c source /Users/x/.claude/shell-snapshots/snapshot-zsh.sh"),
    (21554, 21553, "/Users/x/Library/Application Support/Claude/claude-code/2.1.321/ddbdf2b334e1/claude.app/Contents/"
                   "MacOS/claude", "/Users/x/Library/Application Support/Claude/claude-code/2.1.321/ddbdf2b334e1/"
                   "claude.app/Contents/MacOS/claude --output-format stream-json --verbose"),
    (21553, 54351, "/Applications/Claude.app/Contents/Helpers/disclaimer",
     "/Applications/Claude.app/Contents/Helpers/disclaimer --pgroup -- /Users/x/Library/Application Support/Claude/"
     "claude-code/2.1.321/ddbdf2b334e1/claude.app/Contents/MacOS/claude --output-format stream-json"),
    (54351, 1, "/Applications/Claude.app/Contents/MacOS/Claude", "/Applications/Claude.app/Contents/MacOS/Claude"),
]
PANEL_TREE = [
    (54828, 54822, "/opt/homebrew/bin/fish", "/opt/homebrew/bin/fish -l"),
    (54822, 54351, "/Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper",
     "/Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper --type=utility "
     "--utility-sub-type=node.mojom.NodeService --lang=en-US"),
    (54351, 1, "/Applications/Claude.app/Contents/MacOS/Claude", "/Applications/Claude.app/Contents/MacOS/Claude"),
]


class AgentAncestorTest(unittest.TestCase):
    def chain(self, tree, start):
        return gate_owner.ancestor_chain(start, {pid: (ppid, comm, args) for pid, ppid, comm, args in tree})

    def test_an_agent_shell_descends_from_the_claude_code_cli(self):
        found = gate_owner.claude_code_in(self.chain(AGENT_TREE, 89683))
        self.assertIsNotNone(found)
        self.assertTrue(found.endswith("/MacOS/claude"))

    def test_the_desktop_terminal_panel_does_not(self):
        self.assertIsNone(gate_owner.claude_code_in(self.chain(PANEL_TREE, 54828)))

    def test_other_cli_forms(self):
        node = [(10, 9, "/bin/bash", "bash"), (9, 1, "/usr/local/bin/node",
                                               "node /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js")]
        self.assertIsNotNone(gate_owner.claude_code_in(self.chain(node, 10)))
        local = [(10, 9, "/bin/zsh", "zsh"), (9, 1, "/Users/x/.local/bin/claude", "claude")]
        self.assertIsNotNone(gate_owner.claude_code_in(self.chain(local, 10)))
        terminal = [(10, 9, "/bin/zsh", "-zsh"), (9, 1, "/Applications/iTerm.app/Contents/MacOS/iTerm2", "iTerm2")]
        self.assertIsNone(gate_owner.claude_code_in(self.chain(terminal, 10)))

    def test_a_loop_or_unknown_process_ends_the_walk(self):
        loop = [(10, 11, "/bin/zsh", "zsh"), (11, 10, "/bin/zsh", "zsh")]
        self.assertIsNone(gate_owner.claude_code_in(self.chain(loop, 10)))
        self.assertIsNone(gate_owner.claude_code_in(self.chain([], 10)))

    def test_require_human_refuses_under_the_cli_and_names_it(self):
        with mock.patch.dict(os.environ, clean_env(), clear=True), \
                mock.patch.object(gate_owner, "is_terminal", lambda: True), \
                mock.patch.object(gate_owner, "agent_ancestor", lambda: "/x/MacOS/claude"):
            with self.assertRaisesRegex(gate.GateError, "Claude Code"):
                gate_owner.require_human("approve")
        with mock.patch.dict(os.environ, clean_env(), clear=True), \
                mock.patch.object(gate_owner, "is_terminal", lambda: True), \
                mock.patch.object(gate_owner, "agent_ancestor", lambda: None):
            gate_owner.require_human("approve")  # the owner's terminal, inside the desktop app or not

    def test_a_node_cli_that_renamed_itself_claude(self):
        titled = [(10, 9, "/bin/zsh", "zsh"), (9, 1, "/usr/local/bin/node", "claude --output-format stream-json")]
        self.assertIsNotNone(gate_owner.claude_code_in(self.chain(titled, 10)))
        for argv in ("Claude", "Claude Helper --type=utility", "/Applications/Claude.app/Contents/MacOS/Claude",
                     "/Applications/Claude.app/x", "node server.js", "claudette", "/opt/claude-tools/run"):
            other = [(10, 9, "/bin/zsh", "zsh"), (9, 1, "/usr/local/bin/node", argv)]
            self.assertIsNone(gate_owner.claude_code_in(self.chain(other, 10)), argv)
        not_node = [(10, 9, "/bin/zsh", "zsh"), (9, 1, "/usr/bin/python3", "claude")]
        self.assertIsNone(gate_owner.claude_code_in(self.chain(not_node, 10)))

    def test_an_unreadable_process_table_never_refuses(self):
        def boom():
            raise RuntimeError("ps exploded")
        for table in (lambda: None, boom):
            with mock.patch.object(gate_owner, "process_table", table):
                self.assertIsNone(gate_owner.agent_ancestor())
                with mock.patch.dict(os.environ, clean_env(), clear=True), \
                        mock.patch.object(gate_owner, "is_terminal", lambda: True):
                    gate_owner.require_human("approve")  # no refusal

    def test_the_real_process_table_reads_without_error(self):
        table = gate_owner.process_table()
        if table is None:
            self.skipTest("ps is not available")
        self.assertIn(os.getpid(), table)


class FixRoundGateTest(unittest.TestCase):
    def repo(self, **kw):
        r = GateRepo(**kw)
        self.addCleanup(r.close)
        return r

    def test_gate_changed_when_the_scope_of_the_gate_changes(self):
        for key, value in (("gated", ["frontend/src/app/**"]), ("exempt", CHECK_GATE["exempt"] + ["frontend/src/**"]),
                           ("prototypeOwned", []), ("parityGroups", {})):
            r = self.repo(base_ledger=H)
            r.change()
            r.write("frontend/e2e/parity/enabled.json", json.dumps(ALL_PARITY))
            r.write("docs/design/gate.json", json.dumps(dict(CHECK_GATE, **{key: value})))
            r.snapshot(H)
            r.commit()
            code, reasons, data = r.check("--mode", "merge")
            self.assertIn("gate-changed", reasons, key)
            message = [f for f in data["findings"] if f["reason"] == "gate-changed"][0]["message"]
            self.assertIn(key, message)
            self.assertIn("this finding does not clear: the owner merges this pull request themselves, bypassing the "
                          "required check, after reading the diff of docs/design/gate.json", message)
            self.assertNotIn("gate-changed", r.check("--mode", "edit")[1])

    def test_gate_unchanged_in_scope_is_no_finding(self):
        r = self.repo(base_ledger=H)
        r.change()
        r.write("frontend/e2e/parity/enabled.json", json.dumps(ALL_PARITY))
        r.write("docs/design/gate.json", json.dumps(dict(CHECK_GATE, reviewUrl="http://127.0.0.1:1",
                                                         digestInputs=CHECK_GATE["digestInputs"] + ["x/**"])))
        r.snapshot(H)
        r.commit()
        self.assertNotIn("gate-changed", r.check("--mode", "merge")[1])

    def test_no_gate_changed_when_the_base_has_no_gate_json(self):
        r = self.repo(base_ledger=H)
        r.on_main(lambda: r.remove("docs/design/gate.json"))
        r.write("docs/design/gate.json", json.dumps(CHECK_GATE))
        r.change()
        r.snapshot(H)
        r.commit()
        self.assertNotIn("gate-changed", r.check("--mode", "merge")[1])

    def test_ui_false_names_the_changed_digest_inputs(self):
        r = self.repo()
        r.change(ui="false")
        for i in range(7):
            r.write("frontend/prototype/t{}.ts".format(i), "x\n")
        r.snapshot(H)
        code, reasons, data = r.check()
        msg = [f["message"] for f in data["findings"] if f["reason"] == "ui-false-but-prototype-changed"][0]
        self.assertIn("a digest input changed", msg)
        self.assertNotIn("the prototype changed", msg)
        self.assertIn("frontend/prototype/t0.ts", msg)
        self.assertNotIn("frontend/prototype/t6.ts", msg)
        self.assertIn("…", msg)


class RestoreViolationsTest(unittest.TestCase):
    """restore-violations reads the hooks' session state (the hooks' tests cover the full flow)."""

    def setUp(self):
        self.r = GateRepo()
        self.addCleanup(self.r.close)

    def run_restore(self):
        return subprocess.run([sys.executable, GATE, "restore-violations"], cwd=self.r.root, env=clean_env(),
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def state(self, violations):
        d = gate.session_dir(self.r.root)
        os.makedirs(d, exist_ok=True)
        with open(os.path.join(d, "session-s1.json"), "w") as f:
            json.dump({"version": 1, "violations": violations}, f)

    def test_nothing_to_restore(self):
        out = self.run_restore()
        self.assertEqual(out.returncode, 0, out.stderr.decode())
        self.assertIn("no open violations", out.stdout.decode())

    def test_restores_only_gated_paths(self):
        self.r.write("docs/design/x.md", "agent\n")
        self.r.write("frontend/src/app/A.tsx", "agent\n")
        blob =subprocess.run(["git", "hash-object", "-w", "--stdin"], cwd=self.r.root, input=b"owner\n",
                              stdout=subprocess.PIPE, check=True).stdout.decode().strip()
        agent = subprocess.run(["git", "hash-object", "-w", "--stdin"], cwd=self.r.root, input=b"agent\n",
                               stdout=subprocess.PIPE, check=True).stdout.decode().strip()
        self.state({"docs/design/x.md": {"before": blob, "after": agent, "seq": 1},
                    "../outside.txt": {"before": blob, "after": agent, "seq": 2},
                    "frontend/src/app/A.tsx": {"before": blob, "after": agent, "seq": 3}})
        out = self.run_restore()
        self.assertEqual(out.returncode, 1, out.stdout.decode())
        self.assertEqual(self.r.read("docs/design/x.md"), "agent\n")
        self.assertFalse(os.path.exists(os.path.join(self.r.base, "outside.txt")))
        self.assertEqual(self.r.read("frontend/src/app/A.tsx"), "owner\n")
        self.assertIn("not a gated path", out.stdout.decode())
        self.assertEqual(self.r.git("ls-files", "-s", "frontend/src/app/A.tsx").split()[1], blob)


if __name__ == "__main__":
    unittest.main()
