"""Tests of .claude/hooks/design_gate.py. Standard library only; no network.

    python3 -m unittest discover -s .claude/hooks -p '*_test.py' -v

Each test builds a temporary repository (main with an approved three-state
snapshot, origin/main on it, and the branch feat/x with its change file), runs
the hook as Claude Code does (JSON on stdin) and, for shell commands, runs the
command between the pre-bash and post-bash hooks. Nothing touches this
repository's ledger. LinkedWorktreeTest repeats the core tests in a linked
worktree.
"""
import json
import os
import shlex
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HOOK = os.path.join(HERE, "design_gate.py")
TOOLS = os.path.normpath(os.path.join(HERE, "..", "..", "tools", "design"))
GATE = os.path.join(TOOLS, "gate.py")
sys.path.insert(0, TOOLS)

import gate  # noqa: E402

EMAIL = "owner@example.com"
BRANCH = "feat/x"
REVIEW = "http://localhost:8811"
GATE_JSON = {
    "reviewUrl": REVIEW,
    "gated": ["frontend/**", "internal/app/menu/**"],
    "exempt": ["frontend/src/design/**", "frontend/prototype/**", "frontend/tools/**", "frontend/e2e/**",
               "frontend/node_modules/**", "frontend/.design/**", "frontend/package.json",
               "**/*.test.ts", "**/*.test.tsx", "**/*.pw.ts", "**/*_test.go"],
    "prototypeOwned": ["frontend/src/design/**", "frontend/prototype/**"],
    "digestInputs": ["frontend/src/design/**", "frontend/prototype/**", "frontend/package.json",
                     "docs/design/gate.json", "docs/design/retired.json"],
    "parityGroups": {},
}
H = {100: "a" * 64, 101: "b" * 64, 300: "c" * 64}
APP = "frontend/src/app/library/X.tsx"
ROOT_APP = "frontend/src/App.tsx"
BUTTON = "frontend/src/design/components/Button.tsx"
LEDGER = "docs/design/approvals.json"
APPROVERS = "docs/design/approvers.txt"
SNAPSHOT = "docs/design/state-hashes.json"


def sh(cwd, *args, input_bytes=None):
    return subprocess.run(args, cwd=cwd, check=True, input=input_bytes, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE).stdout.decode()


def clean_env(**extra):
    drop = ("GITHUB_HEAD_REF", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_PROJECT_DIR")
    env = {k: v for k, v in os.environ.items() if k not in drop}
    env.update(extra)
    return env


class HookRepo:
    def __init__(self, linked=False):
        self.base = os.path.realpath(tempfile.mkdtemp(prefix="design-hook-"))
        self.main = os.path.join(self.base, "repo")
        self.root = self.main
        os.makedirs(self.main)
        self.sid = "session-1"
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.email", EMAIL)
        self.git("config", "user.name", "Owner")
        self.git("config", "commit.gpgsign", "false")
        self.write(".gitignore", "frontend/.design/\nfrontend/node_modules/\n")
        self.write("docs/design/gate.json", json.dumps(GATE_JSON))
        self.write("docs/design/retired.json", "[]\n")
        self.write(BUTTON, "export const Button = 1\n")
        self.write("frontend/package.json", "{}\n")
        self.write("internal/engine.go", "package engine\n")
        self.snapshot(H)
        self.approve()
        self.commit("init")
        self.git("update-ref", "refs/remotes/origin/main", "HEAD")
        self.git("checkout", "-q", "-b", BRANCH)
        self.change()
        self.commit("change file")
        if linked:
            self.git("checkout", "-q", "main")
            wt = os.path.join(self.base, "wt")
            self.git("worktree", "add", "-q", wt, BRANCH)
            self.root = wt

    # --- files and git ----------------------------------------------------------
    def path(self, rel):
        return os.path.join(self.root, *rel.split("/"))

    def write(self, rel, text):
        p = self.path(rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            f.write(text)

    def read(self, rel):
        with open(self.path(rel), encoding="utf-8") as f:
            return f.read()

    def exists(self, rel):
        return os.path.exists(self.path(rel))

    def git(self, *args):
        return sh(self.root, "git", *args)

    def commit(self, msg="change"):
        self.git("add", "-A")
        self.git("commit", "-q", "--allow-empty", "-m", msg)

    def blob(self, text):
        return sh(self.root, "git", "hash-object", "--stdin", input_bytes=text.encode()).strip()

    # --- the design files ---------------------------------------------------------
    def hashes(self):
        return {int(k): v["hash"] for k, v in json.loads(self.read(SNAPSHOT))["states"].items()}

    def snapshot(self, states=None, indent=2):
        states = states if states is not None else self.hashes()
        data = {"version": 1, "sourceDigest": gate.digest(self.root),
                "states": {str(n): {"label": "State {}".format(n), "hash": h} for n, h in states.items()}}
        self.write(SNAPSHOT, json.dumps(data, indent=indent, sort_keys=True) + "\n")

    def approve(self, states=None):
        """What the owner's `task design:approve -- all` writes (here written directly)."""
        states = states if states is not None else self.hashes()
        self.write(LEDGER, json.dumps({"states": [{"state": n, "label": "State {}".format(n), "hash": h, "by": EMAIL,
                                                   "at": "2026-11-11T10:00:00Z", "note": ""}
                                                  for n, h in sorted(states.items())],
                                       "retired": [], "noUi": []}) + "\n")
        self.write(APPROVERS, EMAIL + "\n")

    def unapprove(self, n=300, commit=True):
        """State n's fingerprint changes: it becomes stale."""
        self.snapshot({**self.hashes(), n: "d" * 64})
        if commit:
            self.commit("state {} changes".format(n))

    def change(self, branch=BRANCH):
        self.write("docs/design/changes/{}.md".format(gate.slug(branch)),
                   "---\nbranch: {}\nstates: []\nretired: []\nui: true\n---\n\nWhat it does.\n".format(branch))

    # --- the hook -------------------------------------------------------------------
    def hook(self, mode, payload, cwd=None):
        data = {"session_id": self.sid, "transcript_path": "/dev/null", "cwd": cwd or self.root,
                "permission_mode": "default"}
        data.update(payload)
        out = subprocess.run([sys.executable, HOOK, mode], input=json.dumps(data).encode(), cwd=cwd or self.root,
                             env=clean_env(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        return out.returncode, out.stdout.decode(), out.stderr.decode()

    def edit(self, rel, tool="Edit", cwd=None):
        target = rel if os.path.isabs(rel) else self.path(rel)
        key = "notebook_path" if tool == "NotebookEdit" else "file_path"
        return self.hook("pre-edit", {"hook_event_name": "PreToolUse", "tool_name": tool,
                                      "tool_input": {key: target}, "tool_use_id": "toolu_edit"}, cwd=cwd)

    def pre(self, command, tid="toolu_1", tool="Bash", cwd=None):
        return self.hook("pre-bash", {"hook_event_name": "PreToolUse", "tool_name": tool,
                                      "tool_input": {"command": command}, "tool_use_id": tid}, cwd=cwd)

    def post(self, command, tid="toolu_1", tool="Bash", cwd=None, failed=None):
        """PostToolUse, or PostToolUseFailure (with Claude Code's `error` text) when failed is an exit code."""
        payload = {"hook_event_name": "PostToolUse", "tool_name": tool, "tool_input": {"command": command},
                   "tool_response": {}, "tool_use_id": tid}
        if failed:
            payload = {"hook_event_name": "PostToolUseFailure", "tool_name": tool, "tool_input": {"command": command},
                       "tool_use_id": tid, "error": "Exit code {}\n".format(failed), "is_interrupt": False}
        return self.hook("post-bash", payload, cwd=cwd)

    def run(self, command):
        return subprocess.run(["/bin/bash", "-c", command], cwd=self.root, env=clean_env(),
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE).returncode

    def bash(self, command, tid="toolu_1"):
        """pre-bash, the command (when allowed), then post-bash as PostToolUse or, when the command exits non-zero,
        PostToolUseFailure: ((pre code, err), (post code, err) or None)."""
        code, out, err = self.pre(command, tid)
        if code != 0:
            return (code, err), None
        rc = self.run(command)
        pcode, pout, perr = self.post(command, tid, failed=rc or None)
        return (code, err), (pcode, perr)

    def restore(self):
        out = subprocess.run([sys.executable, GATE, "restore-violations"], cwd=self.root, env=clean_env(),
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        return out.returncode, out.stdout.decode() + out.stderr.decode()

    def prompt(self, text="go on"):
        return self.hook("prompt", {"hook_event_name": "UserPromptSubmit", "prompt": text})

    def denied(self, command, tid):
        return self.hook("denied", {"hook_event_name": "PermissionDenied", "tool_name": "Bash",
                                    "tool_input": {"command": command}, "tool_use_id": tid,
                                    "reason": "[Data Exfiltration]"})

    def stop(self, sub=False, agent="agent-1"):
        payload = {"hook_event_name": "SubagentStop" if sub else "Stop", "stop_hook_active": False,
                   "last_assistant_message": "done"}
        if sub:
            payload.update(agent_id=agent, agent_type="general-purpose")
        return self.hook("subagent-stop" if sub else "stop", payload)

    def violations(self):
        return gate.open_violations(self.root)

    def close(self):
        shutil.rmtree(self.base, ignore_errors=True)


RESTORE = "{} {} restore-violations".format(shlex.quote(sys.executable), shlex.quote(GATE))
GENERATE = "{} -c \"import os; os.makedirs('frontend/src/app', exist_ok=True); open('{}', 'w').write('gen')\"".format(
    shlex.quote(sys.executable), "frontend/src/app/Gen.tsx")


class Base(unittest.TestCase):
    linked = False

    def setUp(self):
        self.r = HookRepo(linked=self.linked)
        self.addCleanup(self.r.close)


class CoreTests:
    """Run in a plain repository and again in a linked worktree."""

    def test_edit_of_app_ui_is_blocked_until_every_state_is_approved(self):
        r = self.r
        r.unapprove(300)
        for rel in (APP, ROOT_APP):
            code, out, err = r.edit(rel)
            self.assertEqual(code, 2, err)
            self.assertIn("[#300 Focus · State 300]({}/#s300)".format(REVIEW), err)
            self.assertIn("unapproved", err)
            self.assertIn("only the owner", err)
            self.assertNotIn("Traceback", err)
        r.approve()
        r.commit("the owner approves")
        for rel in (APP, ROOT_APP):
            self.assertEqual(r.edit(rel)[0], 0, rel)
        self.assertEqual(r.edit(APP, tool="Write")[0], 0)

    def test_edit_of_a_design_component_is_allowed(self):
        self.r.unapprove(300)
        self.assertEqual(self.r.edit(BUTTON)[:2], (0, ""))

    def test_edit_of_the_ledger_is_blocked(self):
        for rel in (LEDGER, APPROVERS, "Docs/Design/Approvals.json"):
            for tool in ("Edit", "Write", "MultiEdit"):
                code, out, err = self.r.edit(rel, tool=tool)
                self.assertEqual(code, 2, (rel, tool))
                self.assertIn("approval ledger", err)
                self.assertIn("task design:approve", err)
        self.assertFalse(self.r.git("status", "--porcelain"))

    def test_git_apply_over_a_gated_file_is_a_violation_and_restore_brings_back_the_exact_content(self):
        r = self.r
        r.write(APP, "export const v = 1\n")
        r.commit("app")
        r.unapprove(300)
        owner = "export const v = 2 // the owner's uncommitted work\n"
        r.write(APP, owner)
        r.git("add", APP)
        r.write(APP, "export const v = 3 // the agent's\n")
        patch = os.path.join(r.base, "p.patch")
        with open(patch, "w") as f:
            f.write(r.git("diff", "--", APP))  # owner's content → the agent's
        r.git("reset", "-q", "--", APP)
        r.write(APP, owner)
        (pre, _), (post, err) = r.bash("git apply {}".format(shlex.quote(patch)))
        self.assertEqual(pre, 0)
        self.assertEqual(post, 2)
        self.assertIn(APP, err)
        self.assertIn("restore-violations", err)
        self.assertEqual(r.read(APP), "export const v = 3 // the agent's\n")
        self.assertEqual(r.violations()[APP]["before"], r.blob("export const v = 2 // the owner's uncommitted work\n"))
        (pre, _), (post, err) = r.bash(RESTORE, tid="toolu_2")
        self.assertEqual((pre, post), (0, 0), err)
        self.assertEqual(r.read(APP), "export const v = 2 // the owner's uncommitted work\n")
        self.assertEqual(r.violations(), {})
        self.assertEqual(r.stop()[0], 0)

    def test_commit_is_refused_while_the_staged_snapshot_differs_from_the_working_tree(self):
        r = self.r
        r.write(APP, "export {}\n")
        r.git("add", APP)
        r.snapshot(indent=4)  # same fingerprints, other bytes: the index holds the old file
        code, out, err = r.pre("git commit -qm app")
        self.assertEqual(code, 2)
        self.assertIn(SNAPSHOT, err)
        r.git("add", SNAPSHOT)
        (pre, _), (post, err) = r.bash("git commit -qm app")
        self.assertEqual((pre, post), (0, 0), err)

    def test_stop_blocks_on_an_unsnapshotted_design_edit(self):
        r = self.r
        self.assertEqual(r.edit(BUTTON)[0], 0)
        r.write(BUTTON, "export const Button = 2\n")
        code, out, err = r.stop()
        self.assertEqual(code, 2)
        self.assertIn("task design:snapshot", err)


class HookTest(CoreTests, Base):
    # --- pre-edit ---------------------------------------------------------------------
    def test_notebook_edit_uses_notebook_path(self):
        self.r.unapprove(300)
        self.assertEqual(self.r.edit("frontend/src/app/n.ipynb", tool="NotebookEdit")[0], 2)

    def test_edit_outside_the_gate_is_allowed_and_silent(self):
        self.r.unapprove(300)
        for rel in ("internal/engine.go", "docs/design/changes/feat-x.md", "frontend/e2e/a.ts", "frontend/src/a.test.tsx"):
            self.assertEqual(self.r.edit(rel)[:2], (0, ""), rel)
        outside = os.path.join(self.r.base, "elsewhere", "frontend", "src", "App.tsx")
        self.assertEqual(self.r.edit(outside)[0], 0)

    def test_nested_worktree_resolves_to_its_own_root(self):
        r = self.r
        nested = os.path.join(r.root, ".claude", "worktrees", "n")
        r.git("worktree", "add", "-q", "-b", "feat/n", nested)
        n = HookRepo.__new__(HookRepo)
        n.__dict__.update(r.__dict__)
        n.root = nested
        n.change("feat/n")
        n.unapprove(300)  # the nested worktree has a stale state; the outer checkout does not
        code, out, err = r.edit(os.path.join(nested, APP), cwd=r.root)
        self.assertEqual(code, 2, err)
        self.assertIn("#300", err)
        code, out, err = r.edit(r.path(APP), cwd=nested)
        self.assertEqual(code, 0, err)

    def test_branch_during_a_rebase_comes_from_head_name(self):
        r = self.r
        r.git("checkout", "-q", "--detach")
        code, out, err = r.edit(APP)
        self.assertEqual(code, 2)
        self.assertIn("no-branch", err)
        rel = r.git("rev-parse", "--git-path", "rebase-merge/head-name").strip()
        head_name = rel if os.path.isabs(rel) else os.path.join(r.root, rel)
        os.makedirs(os.path.dirname(head_name), exist_ok=True)
        with open(head_name, "w") as f:
            f.write("refs/heads/{}\n".format(BRANCH))
        self.assertEqual(r.edit(APP)[0], 0)

    def test_a_pre_edit_hook_is_fast(self):
        import time
        self.r.unapprove(300)
        t = time.monotonic()
        self.r.edit(APP)
        self.assertLess(time.monotonic() - t, 3.0)

    # --- pre-bash -----------------------------------------------------------------------
    def test_shell_write_to_app_ui_on_a_clean_branch_is_blocked(self):
        r = self.r
        r.git("checkout", "-q", "-b", "feat/clean", "main")  # no change file on this branch
        code, out, err = r.pre("cat > {}".format(APP))
        self.assertEqual(code, 2)
        self.assertIn(APP, err)
        self.assertIn("no-change-file", err)
        r.git("checkout", "-q", BRANCH)
        r.unapprove(300)
        for command in ("cat > {}".format(APP), "echo x >> {}".format(APP), "printf x | tee {}".format(APP),
                        "cp /etc/hosts {}".format(APP), "sed -i '' s/a/b/ {}".format(ROOT_APP),
                        "touch frontend/src/app/Y.tsx", "rm -rf frontend/src/app", "rm -rf frontend",
                        "cd frontend/src && echo x > App.tsx", "bash -c 'echo x > {}'".format(APP),
                        "echo $(echo x > {})".format(APP), "mv /tmp/x internal/app/menu/menu.go",
                        "dd if=/dev/zero of={} count=1".format(APP), "cp /etc/hosts frontend/src/app/"):
            code, out, err = r.pre(command)
            self.assertEqual(code, 2, command)
            self.assertIn("#300", err, command)

    def test_shell_writes_outside_the_gate_are_allowed(self):
        r = self.r
        r.unapprove(300)
        scratch = os.path.join(r.base, "out.txt")
        for command in ("echo x > {}".format(scratch), "echo x > {}".format(BUTTON), "echo 2>&1 x",
                        "rm -rf frontend/.design frontend/node_modules", "echo x > internal/engine.go",
                        "ls frontend > {}".format(scratch), "git commit -m 'echo > {}'".format(APP),
                        "cat <<'EOF' > {}\nrm -rf frontend\necho x > {}\nEOF".format(scratch, LEDGER)):
            self.assertEqual(r.pre(command)[0], 0, command)

    def test_shell_writes_to_the_ledger_are_blocked(self):
        r = self.r
        for command in ("echo x > {}".format(LEDGER), "echo x >> {}".format(APPROVERS), "tee {} < /dev/null".format(LEDGER),
                        "cp /tmp/a {}".format(LEDGER), "mv {} /tmp/".format(LEDGER), "sed -i.bak s/a/b/ {}".format(LEDGER),
                        "rm -rf docs/design", "cd docs/design && touch approvals.json", "truncate -s 0 {}".format(LEDGER),
                        "git rm --cached {}".format(LEDGER), "git mv {} x.json".format(APPROVERS),
                        "echo x > \"$PWD\"/{}".format(LEDGER), "echo x > $CLAUDE_PROJECT_DIR/{}".format(LEDGER),
                        "sh -c \"echo '{{}}' > {}\"".format(LEDGER), "cp /tmp/approvals.json docs/design/"):
            code, out, err = r.pre(command)
            self.assertEqual(code, 2, command)
            self.assertIn("approval ledger", err, command)

    def test_owner_commands_are_blocked_in_every_shape(self):
        r = self.r
        for command in ("task design:approve -- 100", "task design:init", "task design:confirm-no-ui",
                        "FOO=1 task design:approve -- all", "cd frontend && task design:approve -- 100",
                        "python3 tools/design/gate.py approve 100", "python3 tools/design/gate.py init",
                        "cd tools/design && ./gate.py confirm-no-ui", "python3 -c 'import gate_owner'",
                        "bash -lc 'task design:approve -- all'",
                        "env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT script -q /dev/null task design:approve -- all",
                        "timeout 60 task design:approve -- 100", "nice -n 5 task design:approve -- 100",
                        "sudo -u someone task design:init", "python3 -m gate approve all",
                        "cd tools/design && python3 -m gate approve all", "PYTHONPATH=tools/design python3 -m gate init",
                        "python3 -m gate_owner", "python3 -mgate confirm-no-ui", "uv run tools/design/gate_owner.py",
                        "script -q /dev/null bash -c 'task design:approve -- 1'", "xargs task design:approve < /dev/null",
                        "/usr/local/bin/task --dir . design:approve -- all"):
            code, out, err = r.pre(command)
            self.assertEqual(code, 2, command)
            self.assertIn("owner", err, command)
        code, out, err = r.pre("task design:approve -- 100", tool="mcp__terminal__run_in_terminal")
        self.assertEqual(code, 2)
        self.assertIn("owner", err)

    def test_reading_the_owner_tooling_is_allowed(self):
        for command in ("cat tools/design/gate_owner.py", "grep -n require_human tools/design/gate_owner.py",
                        "git log -- tools/design/gate_owner.py", "git add tools/design/gate_owner.py",
                        "python3 -m gate status", "python3 tools/design/gate.py check --mode merge",
                        "git -c commit.gpgsign=false commit -qm 'feat: gate_owner and task design:approve docs'",
                        "task design:status", "task design:snapshot -- --check", "echo 'task design:approve'"):
            self.assertEqual(self.r.pre(command)[0], 0, command)
        # A `task` word followed by an owner target blocks wherever it stands, even as echo's arguments.
        self.assertEqual(self.r.pre("echo task design:approve")[0], 2)

    def test_git_commands_and_a_clean_ledger(self):
        r = self.r
        for command in ("git checkout .", "git checkout -- .", "git restore .", "git restore --staged .",
                        "git restore --staged {}".format(LEDGER), "git reset --hard", "git stash",
                        "git checkout main", "git checkout -- {}".format(LEDGER)):
            self.assertEqual(r.pre(command)[0], 0, command)

    def test_git_commands_that_would_lose_the_owners_uncommitted_approvals_are_blocked(self):
        r = self.r
        r.unapprove(300)
        r.approve()  # the owner approved at their terminal: unstaged
        for command in ("git checkout .", "git checkout -- .", "git restore .", "git restore {}".format(LEDGER),
                        "git checkout -- {}".format(LEDGER), "git reset --hard", "git reset --hard HEAD~1",
                        "git stash", "git stash push", "git stash -- docs/design", "git checkout main",
                        "git switch -f main", "cd docs && git checkout -- design"):
            code, out, err = r.pre(command)
            self.assertEqual(code, 2, command)
            self.assertIn("uncommitted approvals", err, command)
            self.assertIn("git add docs/design/approvals.json docs/design/approvers.txt && git commit", err)
        for command in ("git restore --staged {}".format(LEDGER), "git stash list", "git checkout -- {}".format(APP),
                        "git reset -q", "git diff"):
            self.assertEqual(r.pre(command)[0], 0, command)

    def test_reading_and_committing_the_owners_ledger_is_allowed(self):
        r = self.r
        r.unapprove(300)
        r.approve()  # the owner approves in their terminal: an unstaged ledger change
        self.assertEqual(r.stop()[0], 0)
        for command in ("git add {}".format(LEDGER), "cat {}".format(LEDGER), "git diff {}".format(LEDGER),
                        "git log -- {}".format(LEDGER), "git diff --stat", "python3 tools/design/gate.py status",
                        "grep -c state {} {}".format(LEDGER, APPROVERS), "git status --short",
                        "git commit -qm 'docs(design): the owner approves' {} {}".format(LEDGER, APPROVERS)):
            (pre, err), post = r.bash(command)
            self.assertEqual(pre, 0, (command, err))
            self.assertEqual(post[0], 0, (command, post[1]))
        self.assertEqual(r.git("status", "--porcelain"), "")

    def test_restore_violations_is_always_allowed(self):
        self.r.unapprove(300)
        self.assertEqual(self.r.pre(RESTORE)[0], 0)

    # --- commits -------------------------------------------------------------------------
    def test_commit_of_a_staged_gated_file_with_a_failing_check_is_blocked(self):
        r = self.r
        r.unapprove(300)
        r.write(APP, "export {}\n")
        r.git("add", APP)
        for command in ("git commit -qm app", "git commit -qam app", "git -C {} commit -m app".format(r.root)):
            code, out, err = r.pre(command)
            self.assertEqual(code, 2, command)
            self.assertIn("#300", err)

    def test_fresh_session_with_unapproved_gated_code_has_no_violation_but_cannot_commit_it(self):
        r = self.r
        r.unapprove(300)
        r.write(APP, "export const existing = 1\n")  # there before this session
        (pre, _), (post, err) = r.bash("git status --short")
        self.assertEqual((pre, post), (0, 0), err)
        self.assertEqual(r.violations(), {})
        self.assertEqual(r.stop()[0], 0)
        for command in ("git add -A && git commit -qm app", "git add {} && git commit -qm app".format(APP)):
            code, out, err = r.pre(command)
            self.assertEqual(code, 2, command)
        r.git("add", APP)
        self.assertEqual(r.pre("git commit -qm app")[0], 2)

    # --- post-bash ---------------------------------------------------------------------------
    def test_generated_untracked_file_is_a_violation_restored_by_deletion_or_rm(self):
        r = self.r
        r.unapprove(300)
        (pre, _), (post, err) = r.bash(GENERATE)
        self.assertEqual((pre, post), (0, 2), err)
        self.assertIn("frontend/src/app/Gen.tsx", err)
        self.assertEqual(r.violations()["frontend/src/app/Gen.tsx"]["before"], "absent")
        code, out, err = r.pre("rm frontend/src/app/X.tsx")  # rm of a path without an `absent` violation
        self.assertEqual(code, 2)
        (pre, _), (post, err) = r.bash("rm frontend/src/app/Gen.tsx", tid="toolu_2")
        self.assertEqual((pre, post), (0, 0), err)
        self.assertEqual(r.violations(), {})
        (pre, _), (post, _) = r.bash(GENERATE, tid="toolu_3")
        self.assertEqual(post, 2)
        (pre, _), (post, err) = r.bash(RESTORE, tid="toolu_4")
        self.assertEqual((pre, post), (0, 0), err)
        self.assertFalse(r.exists("frontend/src/app/Gen.tsx"))
        self.assertEqual(r.violations(), {})

    def test_generate_add_commit_in_one_command_is_a_violation_and_reset_soft_does_not_close_it(self):
        r = self.r
        r.unapprove(300)
        (pre, _), (post, err) = r.bash(GENERATE + " && git add -A && git commit -qm gen")
        self.assertEqual((pre, post), (0, 2), err)
        self.assertIn("frontend/src/app/Gen.tsx", err)
        head = r.git("rev-parse", "HEAD").strip()
        self.assertEqual(r.violations()["frontend/src/app/Gen.tsx"]["commit"], head)
        (pre, _), (post, err) = r.bash("git reset -q --soft HEAD^", tid="toolu_2")
        self.assertEqual(post, 2, err)
        self.assertIn("frontend/src/app/Gen.tsx", r.violations())
        (pre, _), (post, err) = r.bash(RESTORE, tid="toolu_3")
        self.assertEqual(post, 0, err)
        self.assertEqual(r.violations(), {})
        self.assertFalse(r.exists("frontend/src/app/Gen.tsx"))

    def test_restore_after_a_commit_names_the_commit_to_amend(self):
        r = self.r
        r.unapprove(300)
        r.bash(GENERATE + " && git add -A && git commit -qm gen")
        out = subprocess.run([sys.executable, GATE, "restore-violations"], cwd=r.root, env=clean_env(),
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout.decode()
        self.assertIn("git commit --amend --no-edit", out)
        # The generated file was the commit's only change, so the amended commit is empty.
        (pre, _), (post, err) = r.bash("git commit -q --amend --no-edit --allow-empty", tid="toolu_2")
        self.assertEqual((pre, post), (0, 0), err)
        self.assertEqual(r.violations(), {})

    def test_merge_rebase_and_amend_of_existing_gated_code_open_no_violation(self):
        r = self.r
        # main gains a gated file; origin/main follows.
        r.git("checkout", "-q", "main")
        r.write("frontend/src/app/Main.tsx", "export const main = 1\n")
        r.commit("main: app file")
        r.git("update-ref", "refs/remotes/origin/main", "HEAD")
        r.git("checkout", "-q", BRANCH)
        r.write(APP, "export const mine = 1\n")
        r.commit("feat: app file")  # committed while everything was approved
        r.unapprove(300)
        (pre, _), (post, err) = r.bash("git merge -q --no-edit origin/main")
        self.assertEqual((pre, post), (0, 0), err)
        self.assertTrue(r.exists("frontend/src/app/Main.tsx"))
        r.git("reset", "-q", "--hard", "HEAD^")
        (pre, _), (post, err) = r.bash("git rebase -q origin/main", tid="toolu_2")
        self.assertEqual((pre, post), (0, 0), err)
        self.assertTrue(r.exists("frontend/src/app/Main.tsx"))
        (pre, _), (post, err) = r.bash("git commit -q --amend --no-edit", tid="toolu_3")
        self.assertEqual((pre, post), (0, 0), err)
        self.assertEqual(r.violations(), {})

    def test_playwright_output_under_frontend_design_opens_no_violation(self):
        r = self.r
        r.unapprove(300)
        command = "{} -c \"import os; os.makedirs('frontend/.design/run', exist_ok=True); " \
                  "open('frontend/.design/run/r.json', 'w').write('{{}}')\"".format(shlex.quote(sys.executable))
        (pre, _), (post, err) = r.bash(command)
        self.assertEqual((pre, post), (0, 0), err)
        self.assertEqual(r.violations(), {})

    def test_interleaved_commands_keep_the_earliest_before(self):
        r = self.r
        r.write(APP, "v1\n")
        r.commit("app")
        r.unapprove(300)
        r.write(APP, "owner\n")
        self.assertEqual(r.pre("true", tid="toolu_a")[0], 0)
        r.write(APP, "agent a\n")  # command a writes
        self.assertEqual(r.pre("true", tid="toolu_b")[0], 0)
        r.write(APP, "agent b\n")  # command b writes
        # b's pre-bash has already run a's detection (a's post-bash has not come yet), so the earliest
        # `before` is in place even before either post-bash.
        self.assertEqual(r.violations()[APP]["before"], r.blob("owner\n"))
        self.assertEqual(r.post("true", tid="toolu_b")[0], 2)
        self.assertEqual(r.violations()[APP]["before"], r.blob("owner\n"))
        self.assertEqual(r.violations()[APP]["after"], r.blob("agent b\n"))
        self.assertEqual(r.post("true", tid="toolu_a")[0], 2)
        self.assertEqual(r.violations()[APP]["before"], r.blob("owner\n"))
        (pre, _), (post, err) = r.bash(RESTORE, tid="toolu_c")
        self.assertEqual(post, 0, err)
        self.assertEqual(r.read(APP), "owner\n")

    def test_a_violation_closes_when_the_owner_approves(self):
        r = self.r
        r.unapprove(300)
        r.bash(GENERATE)
        self.assertIn("frontend/src/app/Gen.tsx", r.violations())
        r.approve()  # the owner, in their terminal
        (pre, _), (post, err) = r.bash("true", tid="toolu_2")
        self.assertEqual(post, 0, err)
        self.assertEqual(r.violations(), {})

    def test_terminal_tool_shape_is_checked_too(self):
        r = self.r
        r.unapprove(300)
        code, out, err = r.pre("echo x > {}".format(APP), tool="mcp__terminal__run_in_terminal")
        self.assertEqual(code, 2)
        self.assertEqual(r.hook("pre-bash", {"tool_name": "mcp__terminal__read_terminal", "tool_input": {},
                                             "tool_use_id": "t"})[0], 0)

    # --- stop and subagent-stop ----------------------------------------------------------------
    def test_stop_allows_a_tree_that_was_already_stale(self):
        r = self.r
        r.write(BUTTON, "export const Button = 2\n")  # not this session
        code, out, err = r.stop()
        self.assertEqual(code, 0, err)

    def test_stop_blocks_on_an_open_violation(self):
        r = self.r
        r.unapprove(300)
        r.bash(GENERATE)
        code, out, err = r.stop()
        self.assertEqual(code, 2)
        self.assertIn("frontend/src/app/Gen.tsx", err)
        self.assertIn("restore-violations", err)

    def test_stop_with_only_unapproved_states_prints_links_and_allows(self):
        r = self.r
        self.assertEqual(r.edit(BUTTON)[0], 0)
        r.write(BUTTON, "export const Button = 2\n")
        r.snapshot({**r.hashes(), 300: "d" * 64})  # the session refingerprints: 300 is stale
        code, out, err = r.stop()
        self.assertEqual(code, 0, err)
        self.assertIn("[#300 Focus · State 300]({}/#s300)".format(REVIEW), json.loads(out)["systemMessage"])

    def test_stop_blocks_on_a_missing_change_file_after_a_gated_change(self):
        r = self.r
        os.remove(r.path("docs/design/changes/feat-x.md"))
        r.commit("no change file")
        self.assertEqual(r.edit(BUTTON)[0], 0)
        r.write(BUTTON, "export const Button = 2\n")
        r.snapshot()
        code, out, err = r.stop()
        self.assertEqual(code, 2)
        self.assertIn("no-change-file", err)

    def test_stop_safety_valve_allows_the_fourth_identical_block(self):
        r = self.r
        r.unapprove(300)
        r.bash(GENERATE)
        for _ in range(3):
            self.assertEqual(r.stop()[0], 2)
        code, out, err = r.stop()
        self.assertEqual(code, 0)
        warning = json.loads(out)["systemMessage"]
        self.assertIn("frontend/src/app/Gen.tsx", warning)
        self.assertIn("three times", warning)
        self.assertEqual(r.stop()[0], 2)  # the count starts again

    def test_subagent_stop_blocks_only_on_violations(self):
        r = self.r
        self.assertEqual(r.edit(BUTTON)[0], 0)
        r.write(BUTTON, "export const Button = 2\n")  # stale snapshot, changed by this session
        self.assertEqual(r.stop(sub=True)[0], 0)
        r.unapprove(300)
        r.bash(GENERATE)
        code, out, err = r.stop(sub=True)
        self.assertEqual(code, 2)
        self.assertIn("frontend/src/app/Gen.tsx", err)

    # --- robustness ----------------------------------------------------------------------------
    def test_malformed_stdin_is_allowed_with_a_warning(self):
        for mode in ("pre-edit", "pre-bash", "post-bash", "stop", "subagent-stop", "bogus"):
            for stdin in (b"", b"not json", b"[1]"):
                out = subprocess.run([sys.executable, HOOK, mode], input=stdin, cwd=self.r.root, env=clean_env(),
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                self.assertEqual(out.returncode, 0, (mode, stdin))
                self.assertIn("design gate", out.stderr.decode())
                self.assertNotIn("Traceback", out.stderr.decode())

    def test_a_repository_without_the_gate_is_left_alone(self):
        other = tempfile.mkdtemp(prefix="design-hook-other-")
        self.addCleanup(shutil.rmtree, other, True)
        sh(other, "git", "init", "-q")
        target = os.path.join(other, "frontend", "src", "App.tsx")
        code, out, err = self.r.edit(target, cwd=other)
        self.assertEqual((code, err), (0, ""))
        self.assertEqual(self.r.hook("stop", {"hook_event_name": "Stop"}, cwd=other)[0], 0)

    def test_state_lives_outside_the_working_tree(self):
        r = self.r
        r.unapprove(300)
        r.bash(GENERATE)
        r.git("clean", "-qfdx")
        state_dir = os.path.realpath(gate.session_dir(r.root))
        git_dir = os.path.realpath(os.path.join(r.root, r.git("rev-parse", "--git-dir").strip()))
        self.assertTrue(state_dir.startswith(git_dir + os.sep), (state_dir, git_dir))
        self.assertTrue(any(n.startswith("session-") for n in os.listdir(state_dir)))
        self.assertIn("frontend/src/app/Gen.tsx", r.violations())


class FixRoundTest(Base):
    # --- I1: a failed command, or a post-bash that never came ----------------------------------
    def test_a_failed_command_runs_post_bash_through_post_tool_use_failure(self):
        r = self.r
        r.unapprove(300)
        (pre, _), (post, err) = r.bash(GENERATE + " && false")
        self.assertEqual((pre, post), (0, 2), err)
        self.assertIn("frontend/src/app/Gen.tsx", err)

    def test_a_missing_post_bash_is_reconciled_by_the_next_pre_bash(self):
        r = self.r
        r.unapprove(300)
        self.assertEqual(r.pre(GENERATE + " && false", tid="toolu_a")[0], 0)
        r.run(GENERATE + " && false")  # no post-bash arrives
        (pre, _), (post, err) = r.bash("ls", tid="toolu_b")
        self.assertEqual(pre, 0)
        self.assertIn("frontend/src/app/Gen.tsx", r.violations())
        self.assertEqual(r.violations()["frontend/src/app/Gen.tsx"]["before"], "absent")
        self.assertEqual(post, 2)
        self.assertEqual(r.stop()[0], 2)

    def test_stop_reconciles_a_command_without_post_bash(self):
        r = self.r
        r.unapprove(300)
        self.assertEqual(r.pre(GENERATE, tid="toolu_a")[0], 0)
        r.run(GENERATE)
        code, out, err = r.stop()
        self.assertEqual(code, 2)
        self.assertIn("frontend/src/app/Gen.tsx", err)

    # --- I2: restore-violations destroys nothing that is not a violation -----------------------
    def test_restore_restores_nothing_when_the_check_passes(self):
        r = self.r
        r.unapprove(300)
        r.bash(GENERATE)
        r.approve()  # the owner approves; no hook has run since
        code, out = r.restore()
        self.assertEqual(code, 0, out)
        self.assertIn("passes", out)
        self.assertEqual(r.read("frontend/src/app/Gen.tsx"), "gen")

    def test_restore_skips_a_path_changed_since_the_violation(self):
        r = self.r
        r.unapprove(300)
        r.bash(GENERATE)
        r.write("frontend/src/app/Gen.tsx", "the owner's own edit\n")  # outside the agent
        code, out = r.restore()
        self.assertEqual(code, 1, out)
        self.assertEqual(r.read("frontend/src/app/Gen.tsx"), "the owner's own edit\n")
        v = r.violations()["frontend/src/app/Gen.tsx"]
        self.assertIn(v["after"][:12], out)
        self.assertIn(r.blob("the owner's own edit\n")[:12], out)
        self.assertIn("changed since", out)

    def test_restore_with_a_missing_before_blob_touches_nothing(self):
        r = self.r
        r.write(APP, "v1\n")
        r.commit("app")
        r.unapprove(300)
        r.bash("echo ok", tid="toolu_0")
        r.bash(GENERATE)
        r.write(APP, "agent\n")
        r.pre("true", tid="toolu_x")
        # Forge a violation whose before blob does not exist, next to a valid one.
        path = os.path.join(gate.session_dir(r.root), "session-{}.json".format(r.sid))
        with open(path) as f:
            st = json.load(f)
        st["violations"][APP] = {"before": "0" * 40, "after": r.blob("agent\n"), "head_before": "absent",
                                 "seq": 0, "opened": "2026-01-01T00:00:00.000000Z"}
        with open(path, "w") as f:
            json.dump(st, f)
        code, out = r.restore()
        self.assertEqual(code, 1, out)
        self.assertIn("0" * 12, out)
        self.assertIn("not in the object store", out)
        self.assertEqual(r.read(APP), "agent\n")
        self.assertEqual(r.read("frontend/src/app/Gen.tsx"), "gen")  # the valid one is not touched either

    def test_an_abandoned_sessions_violation_does_not_destroy_later_work(self):
        r = self.r
        r.unapprove(300)
        r.sid = "session-A"
        r.bash(GENERATE)  # A leaves a violation behind and never runs again
        r.approve()
        r.commit("the owner approves")
        r.sid = "session-B"
        r.write("frontend/src/app/Gen.tsx", "legit\n")
        r.commit("legit Gen")  # B's work, approved at the time
        r.write("frontend/src/app/Gen.tsx", "legit, edited\n")
        r.unapprove(101, commit=False)  # a later state change makes the check fail again
        code, out = r.restore()
        self.assertEqual(r.read("frontend/src/app/Gen.tsx"), "legit, edited\n")
        self.assertIn("changed since", out)

    def test_restore_saves_the_current_content_before_overwriting(self):
        r = self.r
        r.unapprove(300)
        r.bash(GENERATE)
        code, out = r.restore()
        self.assertEqual(code, 0, out)
        gen = r.blob("gen")
        self.assertIn(gen, out)
        self.assertEqual(sh(r.root, "git", "cat-file", "-p", gen), "gen")

    # --- I3(b): the ledger tripwire ---------------------------------------------------------------
    def test_a_ledger_change_during_a_command_is_reported_once(self):
        r = self.r
        r.unapprove(300)
        self.assertEqual(r.pre("sleep 0", tid="toolu_a")[0], 0)
        r.approve()  # the ledger changes while the command runs (the owner, or not)
        code, out, err = r.post("sleep 0", tid="toolu_a")
        self.assertEqual(code, 2)
        self.assertIn("docs/design/approvals.json changed while an agent command was running", err)
        self.assertIn("git checkout -- docs/design/approvals.json", err)
        self.assertNotEqual(r.git("status", "--porcelain", LEDGER), "")  # nothing restored
        code, out, err = r.stop()
        self.assertEqual(code, 2)
        self.assertIn("changed while an agent command was running", err)
        self.assertEqual(r.stop()[0], 0)

    def test_a_ledger_change_does_not_block_a_subagent_stop(self):
        r = self.r
        r.unapprove(300)
        self.assertEqual(r.pre("sleep 0", tid="toolu_a")[0], 0)
        r.approve()  # new content: 300's new fingerprint
        r.run("sleep 0")  # no post-bash: the stop hooks find the change themselves
        self.assertEqual(r.stop(sub=True)[0], 0)
        code, out, err = r.stop()
        self.assertEqual(code, 2)
        self.assertIn("changed while an agent command was running", err)
        self.assertEqual(r.stop()[0], 0)

    def test_a_ledger_change_in_a_failed_command_is_reported(self):
        r = self.r
        (pre, _), (post, err) = r.bash("{} -c \"open('{}', 'a').write(' ')\" && false".format(
            shlex.quote(sys.executable), APPROVERS))
        self.assertEqual((pre, post), (0, 2), err)
        self.assertIn("docs/design/approvers.txt changed while an agent command was running", err)

    # --- I4: finishing a merge, cherry-pick or revert --------------------------------------------
    def _conflict_setup(self, branch):
        r = self.r
        r.write("internal/engine.go", "package engine // base\n")
        r.commit("engine base")
        r.git("checkout", "-q", "-b", branch)
        r.write("frontend/src/app/Main.tsx", "export const main = 1\n")
        r.write("internal/engine.go", "package engine // theirs\n")
        r.commit("theirs: gated file and a conflicting engine edit")
        r.git("checkout", "-q", BRANCH)
        r.write("internal/engine.go", "package engine // ours\n")
        r.commit("ours: conflicting engine edit")
        r.unapprove(300)

    def test_finishing_a_conflicted_merge_with_git_commit(self):
        r = self.r
        self._conflict_setup("other")
        self.assertNotEqual(r.run("git merge -q --no-edit other"), 0)
        r.write("internal/engine.go", "package engine // resolved\n")
        r.git("add", "internal/engine.go")
        (pre, err), (post, perr) = r.bash("git commit -q --no-edit")
        self.assertEqual((pre, post), (0, 0), (err, perr))

    def test_finishing_a_conflicted_cherry_pick(self):
        r = self.r
        self._conflict_setup("other")
        start = r.git("rev-parse", "HEAD").strip()
        for finish in ("GIT_EDITOR=true git cherry-pick --continue", "git commit -q --no-edit"):
            r.git("reset", "-q", "--hard", start)
            self.assertNotEqual(r.run("git cherry-pick other"), 0)
            r.write("internal/engine.go", "package engine // resolved\n")
            r.git("add", "internal/engine.go")
            (pre, err), (post, perr) = r.bash(finish)
            self.assertEqual((pre, post), (0, 0), (finish, err, perr))
            self.assertTrue(r.exists("frontend/src/app/Main.tsx"))

    def test_a_new_gated_blob_in_a_conflict_resolution_is_still_checked(self):
        r = self.r
        self._conflict_setup("other")
        r.run("git merge -q --no-edit other")
        r.write("internal/engine.go", "package engine // resolved\n")
        r.write("frontend/src/app/Main.tsx", "export const main = 2 // new\n")
        r.git("add", "-A")
        self.assertEqual(r.pre("git commit -q --no-edit")[0], 2)

    # --- M2: a broken gate.json is not an off switch -----------------------------------------------
    def test_a_malformed_gate_json_blocks_app_ui_writes_only(self):
        r = self.r
        r.write("docs/design/gate.json", "{not json")
        self.assertEqual(r.edit(APP)[0], 2)
        self.assertEqual(r.edit("internal/app/menu/menu.go")[0], 2)
        self.assertEqual(r.edit("internal/engine.go")[0], 0)
        code, out, err = r.pre("echo x > {}".format(APP))
        self.assertEqual(code, 2)
        self.assertIn("gate.json", err)
        self.assertEqual(r.pre("echo x > internal/engine.go")[0], 0)

    # --- M5: housekeeping -----------------------------------------------------------------------------
    def test_old_session_files_are_pruned(self):
        r = self.r
        d = gate.session_dir(r.root)
        os.makedirs(d, exist_ok=True)
        old = [os.path.join(d, "session-old.json"), os.path.join(d, "session-old.lock")]
        for p in old:
            with open(p, "w") as f:
                f.write("{}")
            t = __import__("time").time() - 15 * 86400
            os.utime(p, (t, t))
        self.assertEqual(r.bash("true")[1][0], 0)
        for p in old:
            self.assertFalse(os.path.exists(p), p)

    def test_a_snapshot_made_stale_by_someone_else_later_does_not_block(self):
        r = self.r
        self.assertEqual(r.edit(BUTTON)[0], 0)
        r.write(BUTTON, "export const Button = 2\n")
        r.snapshot()  # this session refingerprints
        self.assertEqual(r.stop()[0], 0)
        r.write(BUTTON, "export const Button = 3\n")  # someone else, later
        r.commit("someone else")
        code, out, err = r.stop()
        self.assertEqual(code, 0, err)


class FixRound2Test(Base):
    # --- a command that never ran, followed by the human's own edits ------------------------------
    def test_a_denied_command_then_a_new_prompt_does_not_blame_the_agent_for_the_owners_edit(self):
        r = self.r
        r.write(APP, "v1\n")
        r.commit("app")
        r.unapprove(300)
        self.assertEqual(r.pre("npm run codegen", tid="toolu_a")[0], 0)  # the user denies it: no post hook
        code, out, err = r.prompt()
        self.assertEqual((code, out), (0, ""))
        r.write(APP, "the owner's hand edit\n")
        (pre, _), (post, err) = r.bash("ls", tid="toolu_b")
        self.assertEqual((pre, post), (0, 0), err)
        self.assertEqual(r.violations(), {})
        self.assertEqual(r.stop()[0], 0)
        code, out = r.restore()
        self.assertEqual(code, 0, out)
        self.assertEqual(r.read(APP), "the owner's hand edit\n")

    def test_a_permission_denied_event_drops_the_record(self):
        r = self.r
        r.write(APP, "v1\n")
        r.commit("app")
        r.unapprove(300)
        self.assertEqual(r.pre("npm run codegen", tid="toolu_a")[0], 0)
        code, out, err = r.denied("npm run codegen", "toolu_a")
        self.assertEqual((code, out), (0, ""))
        r.write(APP, "the owner's hand edit\n")  # same turn, but the command never ran
        (pre, _), (post, err) = r.bash("ls", tid="toolu_b")
        self.assertEqual(post, 0, err)
        self.assertEqual(r.violations(), {})

    def test_no_ledger_alarm_after_a_denied_command_and_the_owners_approval(self):
        r = self.r
        r.unapprove(300)
        self.assertEqual(r.pre("npm run codegen", tid="toolu_a")[0], 0)
        r.prompt()
        r.approve()  # the owner, at their terminal
        (pre, _), (post, err) = r.bash("git add {} {} && git commit -qm 'docs(design): approvals'".format(
            LEDGER, APPROVERS), tid="toolu_b")
        self.assertEqual((pre, post), (0, 0), err)
        code, out, err = r.stop()
        self.assertEqual(code, 0, err)
        self.assertNotIn("changed while an agent command was running", err + out)

    def test_a_failing_command_then_another_in_the_same_turn_still_opens_the_violation(self):
        r = self.r
        r.unapprove(300)
        r.prompt()
        self.assertEqual(r.pre(GENERATE + " && false", tid="toolu_a")[0], 0)
        r.run(GENERATE + " && false")  # its post-bash never comes
        (pre, _), (post, err) = r.bash("ls", tid="toolu_b")
        self.assertEqual(post, 2, err)
        self.assertIn("frontend/src/app/Gen.tsx", r.violations())

    def test_a_subagents_commands_belong_to_the_same_turn(self):
        r = self.r
        r.unapprove(300)
        r.prompt()
        sub = {"hook_event_name": "PreToolUse", "tool_name": "Bash", "agent_id": "agent-7",
               "agent_type": "general-purpose", "tool_input": {"command": GENERATE + " && false"},
               "tool_use_id": "toolu_sub"}
        self.assertEqual(r.hook("pre-bash", sub)[0], 0)
        r.run(GENERATE + " && false")  # the sub-agent's command; no post-bash
        (pre, _), (post, err) = r.bash("ls", tid="toolu_main")
        self.assertEqual(post, 2, err)
        self.assertIn("frontend/src/app/Gen.tsx", r.violations())
        r.prompt()  # a later turn changes nothing about what was already found
        self.assertIn("frontend/src/app/Gen.tsx", r.violations())

    def test_the_prompt_hook_never_blocks_or_speaks(self):
        r = self.r
        r.unapprove(300)
        r.bash(GENERATE)  # an open violation
        for stdin in (b"", b"not json"):
            out = subprocess.run([sys.executable, HOOK, "prompt"], input=stdin, cwd=r.root, env=clean_env(),
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.assertEqual((out.returncode, out.stdout), (0, b""))
        self.assertEqual(r.prompt()[:2], (0, ""))
        self.assertEqual(r.denied("x", "toolu_none")[:2], (0, ""))

    # --- a bad record does not switch the hooks off ------------------------------------------------
    def test_a_malformed_pending_record_is_dropped(self):
        r = self.r
        r.unapprove(300)
        self.assertEqual(r.pre("true", tid="toolu_a")[0], 0)
        path = os.path.join(gate.session_dir(r.root), "session-{}.json".format(r.sid))
        with open(path) as f:
            st = json.load(f)
        st["pending"]["toolu_bad"] = {"seq": "x", "t": "never", "tips": 5}
        st["pending"]["toolu_worse"] = "nonsense"
        with open(path, "w") as f:
            json.dump(st, f)
        code, out, err = r.pre(GENERATE, tid="toolu_g")
        self.assertEqual(code, 0)
        self.assertNotIn("failed", err)
        r.run(GENERATE)
        code, out, err = r.post(GENERATE, tid="toolu_g")
        self.assertEqual(code, 2, err)
        with open(path) as f:
            pending = json.load(f)["pending"]
        self.assertNotIn("toolu_bad", pending)
        self.assertNotIn("toolu_worse", pending)

    # --- the gate_owner rule only matches executing forms -------------------------------------------
    def test_naming_gate_owner_without_running_it_is_allowed(self):
        for command in ("python3 -m pytest tools/design -k gate_owner", "ruff check tools/design/gate_owner.py",
                        "python3 -m py_compile tools/design/gate_owner.py",
                        "python3 -m unittest tools/design/gate_test.py tools/design/gate_owner.py",
                        "python3 -c \"print(open('tools/design/gate_owner.py').read()[:10])\"",
                        "task --summary design:approve", "go-task --summary design:approve", "task --list",
                        "task --list-all", "task --dry design:approve -- all", "task -n design:init",
                        "wc -l tools/design/gate_owner.py"):
            self.assertEqual(self.r.pre(command)[0], 0, command)

    def test_running_gate_owner_is_blocked(self):
        for command in ("python3 tools/design/gate_owner.py", "python3 -B tools/design/gate_owner.py approve",
                        "python3 -m gate_owner", "python -mgate_owner", "python3 -m gate approve all",
                        "python3 tools/design/gate.py confirm-no-ui", "uv run tools/design/gate_owner.py",
                        "python3 -c 'import gate_owner'", "python3 -c 'from gate_owner import cmd_approve'",
                        "./tools/design/gate_owner.py", "task design:approve -- all"):
            code, out, err = self.r.pre(command)
            self.assertEqual(code, 2, command)


class FixRound3Test(Base):
    def test_task_inspection_is_allowed_only_in_its_exact_shapes(self):
        r = self.r
        for command in ("task --summary design:approve", "task -n design:approve", "task design:approve --dry",
                        "task --list", "task design:status", "go-task --summary design:init",
                        "task --list-all design:confirm-no-ui -- x", "task -l -a design:approve"):
            self.assertEqual(r.pre(command)[0], 0, command)
        for command in ("task --summary=false design:approve -- all", "task --summary=0 design:approve -- all",
                        "task --sort -n design:approve -- all",
                        "task -o group --output-group-begin -n design:approve -- all",
                        "task --dry=false design:approve", "task -n=false design:approve",
                        "task --summary design:approve design:status", "task --summary --verbose design:approve",
                        "env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT script -q /dev/null task design:approve -- all",
                        "timeout 60 task design:approve -- 100", "nice -n 5 task design:init",
                        "sh -c 'task design:approve -- all'", "task design:approve -- --dry"):
            code, out, err = r.pre(command)
            self.assertEqual(code, 2, command)
            self.assertIn("owner", err, command)

    def test_a_post_bash_after_a_new_prompt_does_not_blame_the_owners_edit(self):
        r = self.r
        r.write(APP, "v1\n")
        r.commit("app")
        r.unapprove(300)
        self.assertEqual(r.pre("sleep 30", tid="toolu_a")[0], 0)
        self.assertEqual(r.prompt()[0], 0)  # the human has the keyboard while the command still runs
        r.write(APP, "the owner's edit\n")
        code, out, err = r.post("sleep 30", tid="toolu_a")
        self.assertEqual(code, 0, err)
        self.assertEqual(r.violations(), {})
        self.assertEqual(r.read(APP), "the owner's edit\n")

    def test_a_post_bash_after_a_new_prompt_still_runs_the_ledger_tripwire(self):
        r = self.r
        r.unapprove(300)
        self.assertEqual(r.pre("sleep 30", tid="toolu_a")[0], 0)
        r.prompt()
        r.approve()
        code, out, err = r.post("sleep 30", tid="toolu_a")
        self.assertEqual(code, 2)
        self.assertIn("changed while an agent command was running", err)


class SettingsTest(unittest.TestCase):
    def test_settings_wire_every_hook_and_deny_the_ledger(self):
        with open(os.path.join(HERE, "..", "settings.json"), encoding="utf-8") as f:
            settings = json.load(f)
        wired = {}
        for event, groups in settings["hooks"].items():
            for group in groups:
                for h in group["hooks"]:
                    self.assertEqual(h["type"], "command")
                    self.assertTrue(h["command"].startswith(
                        'test ! -f "$CLAUDE_PROJECT_DIR"/.claude/hooks/design_gate.py || '
                        'python3 "$CLAUDE_PROJECT_DIR"/.claude/hooks/design_gate.py '), h["command"])
                    self.assertLessEqual(h["timeout"], 60)
                    wired[(event, group.get("matcher", ""))] = h["command"].rsplit(" ", 1)[1]
        self.assertEqual(wired, {
            ("PreToolUse", "Edit|Write|MultiEdit|NotebookEdit"): "pre-edit",
            ("PreToolUse", "Bash"): "pre-bash",
            ("PreToolUse", "mcp__terminal__.*"): "pre-bash",
            ("PostToolUse", "Bash"): "post-bash",
            ("PostToolUse", "mcp__terminal__.*"): "post-bash",
            ("PostToolUseFailure", "Bash"): "post-bash",
            ("PostToolUseFailure", "mcp__terminal__.*"): "post-bash",
            ("UserPromptSubmit", ""): "prompt",
            ("PermissionDenied", "Bash"): "denied",
            ("PermissionDenied", "mcp__terminal__.*"): "denied",
            ("Stop", ""): "stop",
            ("SubagentStop", ""): "subagent-stop",
        })
        deny = settings["permissions"]["deny"]
        for f in (LEDGER, APPROVERS):
            self.assertIn("Edit(/{})".format(f), deny)

    def test_a_missing_hook_script_allows(self):
        with open(os.path.join(HERE, "..", "settings.json"), encoding="utf-8") as f:
            command = json.load(f)["hooks"]["PreToolUse"][0]["hooks"][0]["command"]
        empty = tempfile.mkdtemp(prefix="design-hook-missing-")
        self.addCleanup(shutil.rmtree, empty, True)
        out = subprocess.run(["/bin/sh", "-c", command], input=b"{}", env=clean_env(CLAUDE_PROJECT_DIR=empty),
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(out.returncode, 0, out.stderr.decode())


class LinkedWorktreeTest(CoreTests, Base):
    linked = True


if __name__ == "__main__":
    unittest.main()
