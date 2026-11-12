# The design gate

This folder holds Choix 2.0's design approvals and the rules that tie the app's UI code to them. Read this before you change anything under `frontend/`, `internal/app/menu/`, `internal/app/dialogs/` or `internal/app/window/`.

In one paragraph: every screen state of the desktop app exists as a numbered scenario in a clickable prototype (`frontend/prototype/`). A snapshot fingerprints each state's rendered tree into `state-hashes.json`. The owner looks at each state and approves it at a terminal, which records the state's fingerprint in `approvals.json`. App UI code may be edited, committed and merged only while every state is approved at its current fingerprint. Claude Code hooks enforce this while an agent works; the `design` CI job enforces it at merge time. Parity tests later check that the app renders exactly what was approved.

Contents:

1. [What the gate guarantees, and what it does not](#1-what-the-gate-guarantees-and-what-it-does-not)
2. [The workflow](#2-the-workflow)
3. [Files](#3-files)
4. [Commands](#4-commands)
5. [Checks and the tests that pin them](#5-checks-and-the-tests-that-pin-them)
6. [Gate findings (reason codes)](#6-gate-findings-reason-codes)
7. [Fingerprints](#7-fingerprints)
8. [Hooks](#8-hooks)
9. [Parity](#9-parity)
10. [The owner's one-time setup](#10-the-owners-one-time-setup)
11. [Troubleshooting](#11-troubleshooting)

The state list itself, with numbers, labels and design references, is in [inventory.md](inventory.md).

---

## 1. What the gate guarantees, and what it does not

The gate is a set of tripwires. It catches honest mistakes and makes a bypass visible in the diff. It is not a sandbox: anyone with a shell can write any file.

What it does:

- **One rule for app UI edits.** A *gated* path (see `gated` and `exempt` in [gate.json](gate.json)) may change only while `python3 tools/design/gate.py check --mode edit --assume-changed <the gated path you are about to edit>` passes (the form the hooks run; without `--assume-changed`, a branch with no gated change yet is not checked for `unapproved`): every state approved at its current hash, the change file present and valid, the snapshot current. `task design:status` shows the same for the whole branch: every state's status and the findings. One new or stale state blocks every gated edit, not just the screen it belongs to.
- **An approval goes stale when the design changes.** An approval records a state's hash. Any change to what the state renders changes the hash, and the state reads `stale` until the owner approves it again.
- **Only the owner approves.** The approve command refuses unless it runs in a real terminal, outside Claude Code, under a git email listed in `approvers.txt`. The hooks refuse every agent write to the two ledger files and every run of the owner's commands.
- **The approved build is the one reviewed.** Approving checks that the build being served for review was made from the current source, and re-renders the states to confirm their hashes first.
- **CI repeats the branch check** in merge mode and runs every check and self-test. The `design` job (Linux) runs `bun test`, the type check (`bun run check`), the check and flow self-tests (`bun run design:selftest`, Chromium and WebKit), `bun run lint:design`, the state checks, the flows, the extractor tests, a fresh snapshot compared byte for byte with the committed `state-hashes.json`, the gate and hook Python tests, and the merge check. The `ci` job (macOS) writes the snapshot again and compares it the same way. Parity runs in its own `parity` job (Linux, Chromium) as `task parity`.

What it does not do:

- It does not judge the design. A state that passes every check can still look wrong; the owner's eyes are the review.
- It does not stop a determined bypass. Shell indirection, a program that writes files itself, or a commit made in another worktree can get past the hooks; post-command detection and CI catch most of it afterwards (see [What the hooks cannot see](#what-the-hooks-cannot-see-and-what-catches-it-instead)).
- CI is a hard gate only if the repository requires the `design` job before merging. This repository does not configure that; see [the owner's setup](#10-the-owners-one-time-setup).
- CI cannot tell a ledger entry the owner made from one forged with the owner's own git identity. The owner reviewing `approvals.json` diffs is the control for that.
- The Go side is not gated, apart from the three native-UI packages listed above.

### What the prototype does not do

Single-key shortcuts for Focus and Compare (pick, reject, compare, zoom, arrow navigation, 1–4) are shown in the shortcut sheet and in the menu replicas, but they are not live in the prototype. In the app they come from the native menu's accelerators (`frontend/src/design/menus.json`, `frontend/src/design/keys.ts`) and the app's own key handler; the flows click the buttons instead. The keyboard operation the prototype does check is Tab order, a visible focus ring, Enter and Space on controls, and Escape closing the top overlay.

### Not built: figma-compare

The owner's prompt asks for a comparison against Figma only when a Figma file exists. None does: the prototype is the design source (as [inventory.md](inventory.md) says), so `figma-compare` is not built.

## 2. The workflow

Check where the branch stands first: `task design:status` prints every state with its review link and status, and the merge-check findings. It never fails and writes nothing.

### a. Change the prototype

Screens and components live in `frontend/src/design/`; scenarios, mock data and the navigator in `frontend/prototype/`. Both are exempt from the gate, so an agent may edit them at any time.

1. Edit the design, or add a scenario to `frontend/prototype/scenarios/<group>.tsx` with a new number inside its group's range (ranges: [inventory.md](inventory.md)). Never renumber or reuse a number. To remove a state, delete its scenario and add its number to `retired.json` (see [Files](#3-files)).
2. As soon as a digest input changes, and before ending the turn, write or update the change file `docs/design/changes/<branch-slug>.md` (format: [Files](#3-files)): every state this branch adds or changes in `states:`, every number it retires in `retired:`, and `ui: true`. The Stop hook blocks on `no-change-file` and `bad-change-file`, and the merge check reports `unlisted-state` for a changed state missing from `states:`.
3. Run `cd frontend && bun test && bun run check && bun run lint:design`.
4. Run `task design:check-states` and `task design:flows`. Both must report 0 failures.
5. Run `task design:inventory` if you added, removed or relabelled a state.
6. Run `task design:snapshot`. It rebuilds the prototype and rewrites `state-hashes.json`. A state whose rendered tree changed gets a new hash, and its approval goes stale.

The Stop hook refuses to end a turn while this session has changed a digest input and the snapshot is out of date, or has changed a gated path or a digest input and the change file is missing or bad.

### b. Ask for review, then stop

1. Build and serve the review build in the background: `task design:build && task design:serve` (port 8811). Use the static build, never `task design:dev`: the approve command checks the served build's digest, and the dev server does not report one.
2. Ask the owner to review with **one Markdown link per new or changed state**, grouped by screen, in the form `task design:status` prints:

   ```markdown
   - [#102 Library · Filled, six folders](http://localhost:8811/#s102)
   - [#103 Library · Row hovered (actions shown)](http://localhost:8811/#s103)
   ```

   Never a bare number.
3. Stop. Do not continue with app UI work. The owner either approves or comments, and on a comment you go back to step a.

### c. The owner approves, at a terminal

Only the owner does this, in their own shell (a terminal app, or the desktop app's Terminal panel). Never through an agent.

```sh
task design:build && task design:serve     # if the review build is not already served
task design:approve -- 100-122 300         # or: -- all, -- 102 --note "copy agreed"
git add docs/design/approvals.json && git commit -m "docs(design): approve Library states"
```

`approve` runs, in order:

1. Refuses unless stdin and stdout are terminals, `CLAUDECODE` and `CLAUDE_CODE_ENTRYPOINT` are unset, and no ancestor process is the Claude Code CLI.
2. Checks that `git config user.email` is in `approvers.txt`.
3. Checks that `state-hashes.json`'s `sourceDigest` equals the current source digest.
4. Checks that every number is registered.
5. Fetches `<reviewUrl>/__build.json` and checks that the served build was made from this source.
6. Re-renders the named states (`node tools/snapshot.mjs --check --states <spec>`) and refuses if any differs from `state-hashes.json`.
7. Prints each state as `#N Group · Label — <link>` with its hash, and asks `Approve these N states? [y/N]`.
8. Writes one entry per state into `approvals.json` (an earlier approval of the same state is replaced).

To retire a removed state: add the number to `retired.json` and the change file's `retired:`, then the owner runs `task design:approve -- --retire N`.

### d. Implement in the app

Once every state the branch relies on is approved (the change file was written in step a):

1. Implement the screens in the app (`frontend/src/app/**`, created by the engineering plan), mounting the components and screens from `frontend/src/design/`. Do not copy markup out of them.
2. For each screen group the app now covers, add its parity cases to `frontend/e2e/parity/cases.ts` and the group id to `enabled.json` (see [Parity](#9-parity)).
3. Fill the PR template's design line: `Design approved: [#N Label](http://localhost:8811/#sN)` for each state.

Any later prototype edit blocks app UI edits again until the next snapshot and the owner's re-approval of the states whose hash changed.

### e. A branch with no visible UI change

A branch that touches gated files without changing what the user sees (a refactor, a renamed prop, a test helper outside the exempt globs):

1. Write the change file with `states: []`, `retired: []` and `ui: false`. With `ui: false` no digest input may change; if one does, the gate reports `ui-false-but-prototype-changed`.
2. Every state must still be approved at its current hash while you edit: `ui: false` does not lift the one rule.
3. Commit the gated changes. The owner then runs `task design:confirm-no-ui`, which lists the gated files changed between the base and `HEAD`, asks for confirmation, and records a hash of their contents in `approvals.json` under `noUi`. Any later change to one of those files invalidates the confirmation (`no-ui-unconfirmed` again).
4. In the PR template, keep the line `No visible UI change`.

## 3. Files

Who may write each file: **owner command** (only the owner's terminal commands write it; hooks block every agent write), **generated** (a task writes it; never edit by hand), **contributors** (edited by hand, by humans or agents, and reviewed in the PR).

| File | Written by | Read by |
|---|---|---|
| `docs/design/gate.json` | contributors; scope changes are the owner's decision (`gate-changed`) | gate.py, the hooks, `designPaths.ts`, `snapshot.mjs`, `prototype/location.ts`, `digestInputs.test.ts` |
| `docs/design/retired.json` | contributors | the registry (`prototype/scenarios/index.ts`), gate.py, `tools/state-inventory.ts` |
| `docs/design/state-hashes.json` | generated: `task design:snapshot` | gate.py, `approve`, `parity.ts`, the hooks |
| `docs/design/approvals.json` | owner command: `init`, `approve`, `confirm-no-ui` | gate.py, `parity.ts`, the hooks |
| `docs/design/approvers.txt` | owner command: `init` | gate.py, `approve`, `confirm-no-ui` |
| `docs/design/changes/<branch-slug>.md` | contributors | gate.py (the branch check), CI |
| `docs/design/inventory.md` | generated: `task design:inventory` | people; `tools/state-inventory.test.ts` |
| `frontend/e2e/parity/enabled.json`, `cases.ts` | contributors | `parity.pw.ts`; gate.py reads `enabled.json` |
| `frontend/dist-prototype/build.json` | generated: `task design:build` (and every task that builds) | `serve.mjs` (`/__build.json`), `approve` |
| `<git dir>/choix-design/session-<id>.json` | generated: the hooks | the hooks, `gate.py restore-violations` |

### gate.json

The gate's configuration. Fields:

| Field | Meaning |
|---|---|
| `reviewUrl` | Where the review build is served (`http://localhost:8811`). Links are `<reviewUrl>/#s<N>`; `approve` fetches `<reviewUrl>/__build.json`. |
| `sizes` | Review window sizes, `{ "min": [960, 600], "default": [1280, 800], "large": [1680, 1050] }`. |
| `themes`, `os` | Variant axes: `["dark"]` and `["macos"]` today. A state is rendered at every size × theme × os. |
| `gated` | Globs of app UI paths: `frontend/**` and the three `internal/app/` packages. |
| `exempt` | Globs taken out of `gated`: the design folder, the prototype, the tools, the e2e tests, build output, config files, and every test file (`**/*.test.ts`, `**/*.pw.ts`, `**/*_test.go`, …). |
| `prototypeOwned` | The design folder and the prototype. Only the `gate-changed` finding reads it. |
| `digestInputs` | Every file that can change the prototype build or how a state is rendered and hashed. Their digest is the *source digest*. |
| `parityGroups` | App folder glob → the parity groups that folder needs enabled. |

Globs: `**` crosses directories, `*` and `?` do not. Gated, exempt, digest-input and parity classification is case-insensitive (the macOS file system is). A change to `gated`, `exempt`, `prototypeOwned` or `parityGroups` against the base is reported as `gate-changed` in merge mode, because a branch could otherwise switch the gate off for itself.

### retired.json

A JSON list of state numbers that were removed and may never be reused: `[]` today, for example `[118, 309]`. The registry refuses to load a scenario with a retired number. A removed state also needs its number in the change file's `retired:` and the owner's retirement in the ledger (`unretired` otherwise).

### state-hashes.json

Written by `task design:snapshot`. Header fields, then one entry per state:

```json
{
  "browser": "chromium 158.0.8127.18",
  "extractor": "16c3cecaf9db247f",
  "sourceDigest": "44e786b6739e72d1fb5740031b0057ef3e62c55bdfddeacc6a22e7fed7ba2436",
  "states": {
    "102": {
      "hash": "ef67175f…",
      "label": "Filled, six folders",
      "variants": {
        "default/dark/macos": "636adb00…",
        "large/dark/macos": "cdd29ad2…",
        "min/dark/macos": "68b7163b…"
      }
    }
  },
  "version": 1
}
```

Keys sorted in code-unit order (so `"100"` comes before `"11"`), two-space indent, trailing newline. `sourceDigest` is `gate.py digest` at the time of the snapshot; `extractor` and the state `hash` are explained under [Fingerprints](#7-fingerprints). Two branches that both change it conflict; resolve by re-running the snapshot, never by hand.

### approvals.json (the ledger)

Written only by the owner's commands, one entry per line, each list sorted:

```json
{
  "states": [
    {"state": 102, "label": "Filled, six folders", "hash": "ef67175f…", "by": "owner@example.com", "at": "2026-11-11T18:40:40Z", "note": ""}
  ],
  "retired": [
    {"state": 118, "by": "owner@example.com", "at": "2026-12-01T09:00:00Z"}
  ],
  "noUi": [
    {"branch": "fix/rename-props", "files": "<sha256 of the gated files' paths and blob ids>", "by": "owner@example.com", "at": "2026-12-02T10:00:00Z"}
  ]
}
```

A state is `approved` when its entry's `hash` equals the current hash in `state-hashes.json`, `stale` when it differs, `new` when there is no entry. An entry whose `by` is not in `approvers.txt` counts for nothing and is reported as `bad-ledger`. A missing file is an empty ledger. Conflicts between branches are resolved by the owner. Agents may read it and may commit the owner's changes to it as they are; they never edit it.

### approvers.txt

Created by `task design:init` with the owner's git email:

```text
# Design approvers: one git email per line.
owner@example.com
```

Lines starting with `#` are comments; emails compare case-insensitively. A branch that changes it is reported as `approvers-changed` in merge mode.

### changes/&lt;branch-slug&gt;.md (the change file)

One per branch that touches a gated path or a digest input. The name is the branch name lower-cased, with every run of characters outside `a-z0-9` replaced by `-`: branch `feat/choix-2-desktop-design-463af0` → `docs/design/changes/feat-choix-2-desktop-design-463af0.md`. Front matter with exactly these four keys, then free text describing the change:

```markdown
---
branch: feat/library-rows
states: [102-104, 117]
retired: [118]
ui: true
---

Library rows show the scan state inline. 118 is folded into 117.
```

`states:` and `retired:` are lists of numbers and ranges (`[]` when empty). `branch:` must equal the current branch, and the file must be added or modified on this branch.

### inventory.md

Generated by `task design:inventory` from the scenarios and `retired.json`: one row per state (number, label, group, design source, reference), the retired numbers, and the things deliberately not given a state. `bun test` fails when it is out of date.

### frontend/e2e/parity/enabled.json and cases.ts

`enabled.json` is a JSON array of group ids whose parity tests run; today it lists `["welcome", "library", "folder", "focus", "compare", "overlays"]`. `cases.ts` exports `CASES: ParityCase[]`, one per state. See [Parity](#9-parity).

### frontend/dist-prototype/build.json

`{ "sourceDigest": "<64 hex>" }`, written by `tools/build-prototype.mjs` after a build whose source did not move while it ran. `serve.mjs` answers `/__build.json` from it (empty string when missing). Git-ignored, like the rest of `dist-prototype/`.

### The hooks' session state

`$(git rev-parse --git-path choix-design)/session-<session id>.json`, with a `.lock` next to it: per worktree, inside the git directory, never in the working tree. It holds the before-state of each running shell command (keyed by `tool_use_id`), the open violations (`path → {before, after, head_before, commit, seq, opened}`), which gated and digest-input paths this session changed, the Stop hook's count of identical blocks, and a pending ledger note. Files untouched for 14 days are deleted; a before-state with no matching post-command hook is dropped after a day. Do not edit it.

## 4. Commands

Run `task` from the repository root. Every `design:*` task that builds rewrites `frontend/dist-prototype/`, so run one at a time, and not while the owner is approving from it.

| Command | What it does | When |
|---|---|---|
| `task design:status` | `gate.py status`: merge-mode findings, every state as a Markdown link with its status, next steps. Never fails. | First, and before asking for review. |
| `task design:check` | `gate.py check --mode merge`: exit 1 on findings. Takes `-- --branch B --base R --json`. | Before opening a PR (CI runs the same). |
| `task design:dev` | `bun run proto:dev`: Vite dev server with hot reload on port 8811. | While editing the design. Not for review. |
| `task design:build` | `bun run proto:build` → `node tools/build-prototype.mjs`: digest, Vite build into `dist-prototype/`, `build.json`, digest again (fails and removes the stamp if the source moved). | Before serving for review. |
| `task design:serve` | `bun run proto:serve` → `node tools/serve.mjs`: serves `dist-prototype/` on `127.0.0.1:8811` (`-- --port N`) with `/__build.json`. Reads files on each request. | During review and approval, in the background. |
| `task design:check-states` | `bun run design:check-states`: builds, then every state × size × Chromium and WebKit through the state checks; screenshots and contact sheets under `frontend/.design/screenshots/`. `-- --grep <group>`. | After every design change. |
| `task design:flows` | `bun run design:flows`: builds, then each group's flow (`frontend/tools/flows/<group>.ts`) in Chromium, with overlay coverage. | After every design change. |
| `task design:selftest` | `bun run design:selftest`: the checks and the flow runner against the broken fixture page, in both engines. | After changing a check. |
| `task design:snapshot` | `bun run design:snapshot` → `node tools/snapshot.mjs`: builds, renders every state at every variant, writes `state-hashes.json`. `-- --check` compares only (exit 1 on a difference); `-- --states 100-122,300` renders some; `-- --workers N`; `-- --trees DIR` dumps the trees. | After every design change, before asking for review. |
| `task design:snapshot:verify` | `bun run design:snapshot`, then `git diff --exit-code` on `state-hashes.json`: fails if a second write changes the committed file by a byte (what CI runs). | Before committing a snapshot, to prove it is reproducible. |
| `task design:snapshot:linux` | `snapshot --check` of the committed `HEAD` in the pinned Playwright Linux Docker image. | After a change that may render differently on Linux (fonts, sizes). |
| `task design:inventory` | `bun tools/state-inventory.ts`: writes `inventory.md`. | After adding, removing or relabelling a state. |
| `task design:inventory:check` | `bun tools/state-inventory.ts --check`: exit 1 when `inventory.md` is out of date. | Optional; `bun test` checks the same. |
| `task test:frontend` | `bun run test`, `bun run check`, `bun run lint:design`. | Before every commit under `frontend/`. |
| `task design:init` | **Owner only.** Creates `approvers.txt` and an empty ledger. | Once. |
| `task design:approve` | **Owner only.** Approves states, or retires them with `-- --retire N`. | After review. |
| `task design:confirm-no-ui` | **Owner only.** Confirms a `ui: false` branch's committed gated changes. | On `ui: false` branches. |

Package scripts and tools behind them (run in `frontend/`): `bun test` (unit tests), `bun run check` (`tsc --noEmit`), `bun run lint:design` (`bun tools/lint-design.ts`), `bun run mock:photos` (regenerates the synthetic mock photos, which are digest inputs), and the Playwright projects of `playwright.design.config.ts`: `bunx playwright test -c playwright.design.config.ts --project <checks-chromium | checks-webkit | selftest-chromium | selftest-webkit | flows | extract | snapshot | parity>`. CI runs the extractor tests as `bunx playwright test -c playwright.design.config.ts tools/extract.pw.ts` and parity as `task parity` (in the `parity` job), which builds and serves both sides and runs `--project parity`.

`gate.py` (Python 3.9+, standard library only; run from anywhere in the repository):

| Command | What it does |
|---|---|
| `python3 tools/design/gate.py digest` | Prints the source digest. |
| `python3 tools/design/gate.py check [--mode edit\|merge] [--branch B] [--base R] [--assume-changed PATH]… [--json]` | The branch check. Exit 0 pass, 1 findings, 2 error. Default mode `edit`. `--assume-changed` treats a path as changed (the hooks use it to judge an edit before it happens). |
| `python3 tools/design/gate.py status [--branch B] [--base R]` | What `task design:status` runs. |
| `python3 tools/design/gate.py restore-violations` | Puts every gated path the hooks hold an open violation for back to its earlier content, in the working tree and the index (see [Hooks](#restore-violations)). |
| `gate.py init`, `approve`, `confirm-no-ui` | The owner's commands, implemented in `tools/design/gate_owner.py`, which `gate.py` imports only for these. |

The base is `git merge-base HEAD <base>`, with `<base>` from `--base`, else `origin/main`, else `main`. The branch is `--branch`, else `GITHUB_HEAD_REF`, else the checked-out branch, else the branch being rebased.

## 5. Checks and the tests that pin them

Test locations: bun tests run with `cd frontend && bun test`; Playwright files (`*.pw.ts`) with the project named; Python tests with `python3 -m unittest tools/design/gate_test.py` and `python3 -m unittest discover -s .claude/hooks -p '*_test.py'`. "Fixture s880" means a state of the test-only broken-fixture page (`frontend/tools/fixtures/`, numbers in `kinds.ts`), which the self-test requires to be reported by exactly that check and no other.

### State checks (`task design:check-states`)

Every state, at every size, in Chromium and WebKit. The geometry checks (root-overflow, clip, occlusion, orphan, align, centred, subject) run twice: once as rendered and once with `text-wrap` switched off (`nowrapfx`); focus, Escape, pointer-only, forced-look fidelity and the screenshots run once. Code in `frontend/tools/checks/`, orchestration in `frontend/tools/stateRunner.ts`. Self-tests: `frontend/tools/checks.pw.ts` (`task design:selftest`), test "each check reports exactly its own broken state, and the clean states pass", plus the pure judges in `frontend/tools/checks/judges.test.ts`.

| Check | What it catches | Pinned by |
|---|---|---|
| root-overflow | `[data-app-root]` or the document scrolls horizontally. | fixture s884 |
| clip | An element with `overflow: hidden`/`clip` whose content exceeds its box by more than 1 px, unless it truncates by design (ellipsis, line clamp) or is marked `data-scroll` / `data-clip="intended"`. | fixture s885 |
| occlusion | Text or a control whose centre is covered by another element (except by an open overlay it is not part of). | fixtures s886, s895, s896; "a row scrolled out of a scroller is not an occlusion" |
| orphan | A text block of two or more lines whose last line is one word narrower than 30 % of the widest line. | fixture s887; `judges.test.ts` › judgeOrphans |
| align | Elements sharing `data-align-x`/`data-align-y` whose left edges / vertical centres differ by more than 0.5 px. | fixture s888; judgeAlign |
| centred | An element marked `data-centered` more than 0.5 px off the centre of its box. | fixture s899, clean s872; judgeCentred; "switch thumbs sit 1 px inside the track" |
| focus (reach, ring) | Chromium only: an enabled control Tab cannot reach (directly or by arrows inside a menu, listbox, toolbar, tablist, radiogroup or grid); focus that changes nothing visible; the platform ring (`outline-style: auto`). | fixtures s889, s890, s897; judgeRing |
| focus (Escape) | In a state with overlays, one Escape must close exactly one overlay, and the top one. | fixtures s891, s898 |
| pointer-only | An element marked `data-pointer-only` must give a reason, not be focusable and be `aria-hidden`. | fixture s894, clean s873; judgePointerOnly |
| subject | What the state's label names (`subject` selector, `data-state-subject`) is fully in view at every size and visible. | fixture s893; judgeSubject |
| forced-look fidelity | Gallery states: a `data-force` look must equal the real hover / focus / press. | fixture s892; `extract.pw.ts` "a real hover hashes as `data-force="hover"`"; `postcss-force.test.ts` (every twin selector) |
| console, pageerror, request, external | A console error, an uncaught exception, a failed or ≥ 400 request, or any request off localhost (aborted before it leaves). | fixtures s880–s883; judges › isLocal |
| harness | A state that does not open, a check that throws, or a wrap-mode probe that proves nothing becomes a finding; the run never aborts. | `stateRunner.ts` (not separately pinned) |

Also in `checks.pw.ts`: "the prototype build holds no fixture code", "s102 passes every check at every size", "a contact sheet holds every screenshot, or the run fails". `frontend/tools/fixtures/fixtures.test.ts`: fixture numbers stay at 870–899 and no prototype state uses one.

### Flows (`task design:flows`)

| Check | What it catches | Pinned by |
|---|---|---|
| flows coverage | Every overlay a group declares or registers must be both opened and closed by the group's flow. | `frontend/tools/flowCoverage.test.ts`; `checks.pw.ts` "a flow that opens an overlay and never closes it fails naming it", "navigating away from an open overlay does not count as closing it" |
| dead links | A click that leads to a state the prototype does not have. | `checks.pw.ts` "a click that leads to a state the prototype does not have fails the flow" |
| flow errors | A missing flow file, a flow that throws, console errors and failed or off-localhost requests during a flow. | `frontend/tools/flows.pw.ts` and `flowRunner.ts`; not pinned beyond the self-tests above |

### Design lint (`bun run lint:design`)

`frontend/tools/lint-design.ts`; each rule has fixtures in `frontend/tools/lint-design.test.ts` ("lintDesign: one fixture per rule yields exactly one finding of that rule › <rule>: <case>"), and "the real tree yields no findings".

| Rule | Where | Forbids |
|---|---|---|
| `token` | `src/`, `prototype/`, `index.html`, `tailwind.config.ts` (not `tokens.css`) | Literal colours, px font sizes, radii, durations outside `src/design/tokens.css`. |
| `import` | `src/design/` | `fetch(`, and any import (static, dynamic, `require`, re-export, side-effect) except a relative path that resolves inside `src/design/`, `react`, and in test files `bun:test`, `node:fs`, `node:path` and `react-dom/server`. The finding names the specifier. |
| `raw-control` | `src/design/screens/` | Raw `<button>`, `<input>`, `<select>`, `<textarea>`. |
| `pseudo` | `src/design/` | `::before` / `::after` and `before:` / `after:` variants. |
| `clock`, `timer` | `src/design/`, `prototype/` (not `harness.ts`) | `Date.now()`, `new Date()` without an argument, `Math.random()`, `setTimeout`, `setInterval`. |
| `lazy` | `src/`, `prototype/`, `index.html` | `loading="lazy"`. |
| `isolation` | `src/`, `index.html`, `vite.config.ts` | Any reference to `prototype/` from the app. |
| `inert` | `src/design/` except `NativeBackdrop.tsx` | Setting `inert`. |
| `font-unit` | `src/design/`, `prototype/`, `tailwind.config.ts` | `ch`, `ex`, `ic`, `cap`, `lh` lengths: they compute differently on Linux. |
| `inline-motion` | code under `src/design/` | Transition or animation in an inline style (keys, computed keys, `style.*` and `style['…']` assignments, `setProperty`, `Object.assign(el.style, …)`), vendor-prefixed forms of each (`WebkitTransition`, `MozAnimation`, `'-webkit-transition'`, `setProperty('-webkit-…')`), the Web Animations API (`el.animate(…)`), and any `style.cssText =` or `setAttribute('style', …)`. |
| `url-fragment` | `src/design/`, `prototype/` | `url(#…)` SVG references: the extractor cannot fingerprint them and throws. |

### Privacy

The repository is public. `frontend/prototype/mock/privacy.test.ts` (run by `bun test`) scans every file git lists in the repository (tracked, plus untracked files that are not ignored), whatever its name or extension (`Makefile`, `.gitignore`, `go.mod`, SQL migrations included), skipping only binary files (a NUL byte in the first 8 KB), files over 2 MB, and dependency lock and integrity files (`*.lock` such as `bun.lock`, `go.sum`, `package-lock.json`), whose base64 integrity strings split into random short tokens and would give false hits, for a list of terms that must never appear, and fails naming the file and line. The list is stored as salted SHA-256 hashes only, so it discloses nothing: each line is split into runs of letters and digits, NFC and lower-cased, and every token and every pair of adjacent tokens joined by one space is hashed, which matches one- and two-word terms as whole words in any case. To add a term, write it NFC, lower-cased, with single spaces, and add the output of `printf '%s' 'choix-privacy-v1:<term>' | shasum -a 256` (the hex only) to `DENY_HASHES`; never commit the term itself, and never put it in a commit message. Mock data in `frontend/prototype/mock/` is invented, and the mock photos are generated (`bun run mock:photos`), not taken from anyone's library. The approval ledger (`approvals.json`) records the approver's git email in each entry by design: that is what makes an approval attributable.

### Fingerprints, digest and gate

| Check | What it catches | Pinned by |
|---|---|---|
| a design change changes the hash | Each kind of design edit (style, text, attribute, SVG geometry, a resource's bytes, pseudo-element, motion and at-rules, fonts, layers, `<html>`) moves the hash. | `frontend/tools/extract.pw.ts` (project `extract`): "a design change changes the hash", "pseudo-element rules and font faces (document-wide)", "what no element's computed style shows under freeze…", "attributes that change rendering…", "denylist: …" |
| what is not design does not move it | Measured values, the server port, render order, generated ids, a real hover vs `data-force`, an indeterminate progress bar under freeze. | `extract.pw.ts` "what is not design does not change the hash" |
| what the extractor refuses | `outline-style: auto`, pseudo-element content, a resource on another host, `@import`, a `FontFace` added from script, an unregistered measured id, an unreadable declaration. | `extract.pw.ts` "what the tree leaves out, and what it refuses" and the throw tests |
| determinism of the snapshot | Same file from two runs, any worker count, macOS and Linux. | `frontend/tools/snapshot.test.ts` (file format, the state-hash formula); `extract.pw.ts` "the order states were rendered", "generated element ids", "s220 … extracts the same in five fresh pages, at every size"; `frontend/tools/extract.test.ts` (canonical JSON). No unit test runs the whole snapshot twice; CI writes it again on Linux (job `design`) and macOS (job `ci`) and fails on any byte of difference from the committed file (`git diff --exit-code`), and `task design:snapshot:verify` does the same locally. |
| digest-input coverage | A module that can change the build or a hash but is not in `digestInputs`. | `frontend/tools/digestInputs.test.ts` (walks the import graph from the build and snapshot entry points; also Tailwind's content globs) |
| the two PostCSS chains agree | The prototype's CSS chain differs from the app's in anything but Tailwind's `content` (the probe covers the base layer, `@apply` and the data-force twins); the app's `content` is `./index.html` and `./src/**/*.{ts,tsx}` without test files, never the prototype. | `frontend/tools/postcssChain.test.ts` |
| build stamp | A build stamped with a digest its source no longer has. | `frontend/tools/build-prototype.test.ts`; `extract.pw.ts` "the prototype build is stamped with the current source digest" |
| measured registry | A `data-measured-by` id with no registered computing files. | `frontend/prototype/measured.test.ts` |
| registry | Duplicate, out-of-range or retired numbers. | `frontend/prototype/registry.test.ts` |
| inventory | `inventory.md` out of date; a retired number missing from it. | `frontend/tools/state-inventory.test.ts` |
| the digest | Which files count, from any directory or worktree. | `tools/design/gate_test.py` `DigestTest`, `GlobTest` |
| gate findings | One test per reason code: see the table in [section 6](#6-gate-findings-reason-codes). | `tools/design/gate_test.py` |
| owner commands | Refusal without a terminal, under `CLAUDECODE`, under the CLI; unknown email; mismatched or unreachable build; failed re-render; ledger format. | `gate_test.py` `HumanTest`, `AgentAncestorTest`, `CarryOverTest` |
| hook modes | See the table in [section 8](#8-hooks). | `.claude/hooks/design_gate_test.py` |
| parity | See [section 9](#9-parity). | `frontend/e2e/parity/parity.test.ts`, `cases.test.ts`, `selftest.pw.ts` |

## 6. Gate findings (reason codes)

`gate.py check` collects findings in this order. **Edit** mode is what the hooks and a plain `gate.py check` run; **merge** mode is `task design:check`, `task design:status` and CI. In both, when the branch changes no gated path and no digest input and leaves `state-hashes.json` as the base has it, only the first six rows are checked (so a branch that changes only `approvals.json` still gets `bad-ledger`).

| Code | Mode | Meaning | How to resolve | Test (`gate_test.py`) |
|---|---|---|---|---|
| `bad-snapshot` | both | `state-hashes.json` (here or at the base) is not valid. | `task design:snapshot`. | `test_a_malformed_snapshot_is_a_finding_not_a_traceback`, `test_a_malformed_base_snapshot_is_a_finding` |
| `bad-retired` | both | `retired.json`, here or at the base, is not a JSON list of numbers. | Fix the file. | `test_a_malformed_retired_file_is_a_finding_not_a_traceback` |
| `no-base` | both | No merge base with `origin/main` or `main` (or `--base`). The check stops here. | `git fetch origin main`, or pass `--base`. | `test_no_base` |
| `approvers-changed` | merge | `approvers.txt` differs from the base's (only when the base has one). | Only the owner changes it. This finding does not clear: the owner merges the pull request themselves, bypassing the required check, after reading the diff of `approvers.txt`. | `test_approvers_changed_is_a_merge_finding_when_the_base_has_the_file`, `test_an_approvers_only_branch_is_still_reported` |
| `gate-changed` | merge | `gated`, `exempt`, `prototypeOwned` or `parityGroups` differ from the base's `gate.json`. | The owner decides. This finding does not clear: the owner merges the pull request themselves, bypassing the required check, after reading the diff of `gate.json`. | `test_gate_changed_when_the_scope_of_the_gate_changes` |
| `bad-ledger` | both | `approvals.json` is malformed (every approval ignored), or holds entries whose `by` is not in `approvers.txt` (those ignored). Checked even on a branch that changes nothing else. | Tell the owner; only they edit the ledger. | `test_bad_ledger_entries_are_ignored_and_reported`, `test_malformed_ledger_is_a_finding`, `test_a_ledger_only_branch_with_a_malformed_ledger_is_a_finding`, `test_a_ledger_only_branch_with_an_entry_by_a_non_approver_is_a_finding`, `test_a_ledger_only_branch_with_valid_owner_entries_has_no_finding` |
| `no-branch` | both | Detached HEAD with no `GITHUB_HEAD_REF` and no rebase in progress. | Check out a branch, or pass `--branch`. | `test_no_branch_on_detached_head_and_branch_flag` |
| `no-change-file` | both | `docs/design/changes/<branch-slug>.md` is missing. | Write it ([format](#changesbranch-slugmd-the-change-file)). | `test_no_change_file` |
| `bad-change-file` | both | It does not parse, its `branch:` is another branch, or it is not added or modified on this branch. | Fix the front matter; touch it on this branch. | `test_bad_change_file`, `test_change_file_not_touched_on_this_branch_is_bad` |
| `stale-snapshot` | both | `state-hashes.json`'s `sourceDigest` is not the current source digest. | `task design:snapshot`. | `test_stale_snapshot` |
| `ui-false-but-prototype-changed` | both | The change file says `ui: false` but a digest input or `state-hashes.json` changed. | Set `ui: true`, or undo the prototype change. | `test_ui_false_but_prototype_changed`, `test_ui_false_names_the_changed_digest_inputs` |
| `unapproved` | both | Edit: a gated path changed while some state is `new` or `stale` (every pending state is listed). Merge: also any state in the change file's `states:` that is not approved. | The owner reviews and approves. Leave gated files alone until then. | `test_assumed_app_change_needs_every_state_approved`, `test_prototype_only_branch_passes_edit_and_fails_merge_unapproved`, `test_unapproved_holds_under_ui_false` |
| `unretired` | both | A state that was in the base's snapshot or the ledger is gone, and is missing from `retired.json`, the change file's `retired:` (unless the base already retired it), or the ledger's retirements. | Add it to all three; the owner runs `task design:approve -- --retire N`. | `test_removing_the_only_unapproved_state_fails_unretired`, `test_removed_state_passes_once_retired_everywhere` |
| `unlisted-state` | merge | A state is new or changed against the base but not in the change file's `states:`. | Add it. | `test_unlisted_state` |
| `retired-reused` | merge | A number retired at the base is registered again. | Use a new number. | `test_retired_reused` |
| `no-ui-unconfirmed` | merge | `ui: false`, gated files changed, and no `noUi` entry for this branch matches their current contents. | Commit, then the owner runs `task design:confirm-no-ui`. | `test_no_ui_confirmation_survives_main_moving_and_dies_on_a_gated_change` |
| `parity-not-enabled` | merge | App files exist under `frontend/` whose parity groups are not in `enabled.json`. | Add the cases and enable the group ([Parity](#9-parity)). | `test_parity_not_enabled` |

On this branch the `design` CI job fails with `unapproved` until the owner has approved every state. That is the gate working.

## 7. Fingerprints

### What a state hash is made of

For each variant (`<size>/<theme>/<os>`, three today), `frontend/tools/extract.ts` extracts the rendered tree under `[data-app-root]` in a fresh page (`?bare&freeze`, scale 1, UTC, `en-US`, reduced motion, clock fixed at 2026-11-11T10:00:00Z, external requests blocked) and hashes it: the variant hash is the sha256 of the tree's canonical JSON.

```text
state hash = sha256( min hash + default hash + large hash + browser + extractor )
browser    = "chromium <version>" of the Playwright Chromium that rendered it
extractor  = first 16 hex of sha256(frontend/tools/extract.ts)
```

The tree holds, per element: tag, sorted classes, own text, the rendering attributes, live form state, SVG attributes, and computed style values (not used pixel values). Every URL becomes `res:<hash of the bytes>`, so a swapped image or font under the same name changes the hash. Document-wide, every tree also holds the `@font-face` rules, the rules that style pseudo-elements (scrollbars, slider parts, `::placeholder`), the animation and transition declarations of every rule, `@keyframes` and other defining at-rules, the cascade layer order (when one of those rules sits in a layer), and the `<html>` element's own style. The source digest is **not** part of the state hash.

### What re-stales approvals

| Change | Effect |
|---|---|
| Any edit to `frontend/tools/extract.ts`, even a comment | The extractor id changes: **every** state is stale and needs the owner's re-approval. |
| A Playwright / Chromium upgrade | The browser string changes: **every** state is stale. (`package.json` and `bun.lock` are digest inputs too.) |
| A change to a document-wide record: a pseudo-element rule, any transition or animation rule, a `@keyframes`, `@property` or other at-rule, the order of layers holding such rules, an `@font-face`, the `<html>` style | It is in every tree: **every** state is stale. |
| An edit to a component, screen or scenario | The states whose trees change are stale. |
| An edit to any other digest input that changes no tree (a tool on the snapshot path, a config) | `stale-snapshot` until `task design:snapshot`; the state hashes come out the same and approvals stay valid. |

So batch extractor and toolchain changes, and tell the owner beforehand that a full re-approval follows.

### What is left out of a hash, and why

- **Values computed from layout at run time.** An element marked `data-measured="<props>" data-measured-by="<id>"` leaves those properties out (only the ones `frontend/src/design/measured.ts` allows for the id), and its tree node carries `<id>:<sha256 of the files that compute it>` instead, from `measured.ts` and `frontend/prototype/measured.ts`. Changing the computing code still changes the hash. Today: the cluster strip's mask, Focus zoom and pan, and three scroll positions.
- **Logical CSS aliases** (`inline-size`, `margin-block-start`, …): 52 entries whose physical property is kept (`DENYLIST` in `extract.ts`, each with a test that a change to it still moves the hash).
- **Custom properties**, except those a pseudo-element, motion or keyframe rule reads.
- **Prototype chrome** (`data-proto-chrome`), scripts, styles, templates, the children of `display: none`, shadow roots.
- **Generated ids**, which become the referenced node's position; the server's origin and port.

### macOS and Linux

Hashes use computed values, so sub-pixel layout differences between machines do not count. Font-metric units (`ch`, `ex`, `lh`, …) are banned by lint because Linux rounds glyph advances and they compute differently. CI writes the snapshot again on Linux (`design` job) and macOS (`ci` job) and fails if the committed file changes by a byte. Locally, `task design:snapshot:verify` does the same, and `task design:snapshot:linux` runs `snapshot --check` against the committed `HEAD` in the Playwright Docker image of the pinned version.

## 8. Hooks

Configured in `.claude/settings.json`; code in `.claude/hooks/design_gate.py`; logic shared with `tools/design/gate.py`. They load when a Claude Code session starts in this repository, in sub-agents too. Each command is guarded with `test ! -f …/design_gate.py ||`, so a checkout without the script is unaffected. `permissions.deny` also denies `Edit` on the two ledger files.

| Mode | Event (matcher) | Blocks | Tests (`design_gate_test.py`) |
|---|---|---|---|
| `pre-edit` | PreToolUse (`Edit\|Write\|MultiEdit\|NotebookEdit`) | Any write to `approvals.json` or `approvers.txt`. A gated path while `check --mode edit` with that path assumed changed has findings. Records which gated and digest-input paths the session touched. | `CoreTests.test_edit_of_app_ui_is_blocked_until_every_state_is_approved`, `test_edit_of_a_design_component_is_allowed`, `test_edit_of_the_ledger_is_blocked`; `HookTest.test_notebook_edit_uses_notebook_path`, `test_a_pre_edit_hook_is_fast` |
| `pre-bash` | PreToolUse (`Bash`, `mcp__terminal__.*`) | The owner's commands in any parsed form (`task design:init\|approve\|confirm-no-ui`, `gate.py` with those subcommands, `gate_owner` run or imported, behind wrappers, `bash -c`, `eval`, `$(…)`). Writes to the ledger (redirects, `tee`, `cp`, `mv`, `sed -i`, `rm`, `git rm`/`mv`, …). Writes to gated paths while the check fails. `git checkout`/`restore`/`reset`/`stash`/`switch` that would discard the owner's uncommitted ledger changes. A `git commit` that carries gated paths differing from the base while the check fails, or while the index and working tree disagree on a digest input, `state-hashes.json`, `approvals.json` or the change file. Then records the before-state. | `HookTest.test_shell_write_to_app_ui_on_a_clean_branch_is_blocked`, `test_shell_writes_to_the_ledger_are_blocked`, `test_owner_commands_are_blocked_in_every_shape`, `test_git_commands_that_would_lose_the_owners_uncommitted_approvals_are_blocked`, `test_commit_of_a_staged_gated_file_with_a_failing_check_is_blocked`, `test_terminal_tool_shape_is_checked_too`; `CoreTests.test_commit_is_refused_while_the_staged_snapshot_differs_from_the_working_tree`; `FixRoundTest.test_finishing_a_conflicted_merge_with_git_commit`; `FixRound2Test.test_running_gate_owner_is_blocked` |
| `post-bash` | PostToolUse and PostToolUseFailure (`Bash`, `mcp__terminal__.*`) | Cannot undo (the command ran). Compares every gated path's content and the commits the command made with the before-state; a change is a **violation** when the edit check fails and the new content is in no earlier commit or `origin/main`. Exit 2 names the files and the fix. Also runs the ledger tripwire, and catches up on earlier commands whose post-command hook never came. A command that was still running when the user sent a new prompt gets the ledger tripwire only, since the human may have edited gated files meanwhile. | `CoreTests.test_git_apply_over_a_gated_file_is_a_violation_and_restore_brings_back_the_exact_content`; `HookTest.test_generated_untracked_file_is_a_violation_restored_by_deletion_or_rm`, `test_generate_add_commit_in_one_command_is_a_violation_and_reset_soft_does_not_close_it`, `test_merge_rebase_and_amend_of_existing_gated_code_open_no_violation`, `test_playwright_output_under_frontend_design_opens_no_violation`, `test_interleaved_commands_keep_the_earliest_before`, `test_a_violation_closes_when_the_owner_approves`; `FixRoundTest.test_a_failed_command_runs_post_bash_through_post_tool_use_failure`, `test_a_missing_post_bash_is_reconciled_by_the_next_pre_bash`, `test_a_ledger_change_during_a_command_is_reported_once`, `test_a_ledger_change_in_a_failed_command_is_reported`; `FixRound3Test.test_a_post_bash_after_a_new_prompt_does_not_blame_the_owners_edit`, `test_a_post_bash_after_a_new_prompt_still_runs_the_ledger_tripwire` |
| `prompt` | UserPromptSubmit | Never blocks, prints nothing. Marks every command still waiting for its post-command hook as checked: the human has had the keyboard since, so the agent is not blamed for what the tree holds now. | `FixRound2Test.test_the_prompt_hook_never_blocks_or_speaks`, `test_a_denied_command_then_a_new_prompt_does_not_blame_the_agent_for_the_owners_edit` |
| `denied` | PermissionDenied (`Bash`, `mcp__terminal__.*`) | Never blocks. Drops the before-state of a command auto mode denied. | `FixRound2Test.test_a_permission_denied_event_drops_the_record` |
| `stop` | Stop | Ending the turn while a violation is open; while the snapshot is stale *and this session changed a digest input*; while the change file is missing or bad *and this session changed a gated or digest-input path*; once, after the ledger tripwire fired. States awaiting approval are listed as a note and never block. | `CoreTests.test_stop_blocks_on_an_unsnapshotted_design_edit`; `HookTest.test_stop_blocks_on_an_open_violation`, `test_stop_allows_a_tree_that_was_already_stale`, `test_stop_with_only_unapproved_states_prints_links_and_allows`, `test_stop_blocks_on_a_missing_change_file_after_a_gated_change`, `test_stop_safety_valve_allows_the_fourth_identical_block`; `FixRoundTest.test_stop_reconciles_a_command_without_post_bash` |
| `subagent-stop` | SubagentStop | Open violations only. | `HookTest.test_subagent_stop_blocks_only_on_violations`; `FixRoundTest.test_a_ledger_change_does_not_block_a_subagent_stop` |

Always allowed: reading anything (including the ledger and `gate_owner.py`), `git add` and `git commit` of the owner's ledger changes as they are, `gate.py restore-violations`, `rm` of a file whose violation says it did not exist before, and every edit outside gated paths (`HookTest.test_reading_and_committing_the_owners_ledger_is_allowed`, `test_restore_violations_is_always_allowed`, `test_reading_the_owner_tooling_is_allowed`, `FixRound2Test.test_naming_gate_owner_without_running_it_is_allowed`). `task --list` runs nothing and is allowed, and so is `task --summary design:approve` when nothing but owner targets and the inspection flags stand before the first `--`: exactly the tokens `--summary`, `--list`, `-l`, `--list-all`, `-a`, `--dry` and `-n`, nothing else (`FixRound3Test.test_task_inspection_is_allowed_only_in_its_exact_shapes`).

**Safety valve.** After three identical Stop blocks in a row, the fourth stop is allowed, with a warning that names what is still wrong; the count then restarts. A session can therefore never be trapped by something only the owner can resolve.

**Fail-open.** Malformed input, an unknown mode or an internal error: exit 0, with a warning on stderr and as a `systemMessage` the user sees (`HookTest.test_malformed_stdin_is_allowed_with_a_warning`). A repository without `docs/design/gate.json` is left alone (`test_a_repository_without_the_gate_is_left_alone`). One exception: when `gate.json` cannot be read, writes under `frontend/` and `internal/app/` are blocked with the error and everything else is allowed (`FixRoundTest.test_a_malformed_gate_json_blocks_app_ui_writes_only`).

**Timing.** Timeouts in `settings.json`: 10 s (`prompt`, `denied`), 20 s (`pre-edit`), 30 s (`pre-bash`), 60 s (`post-bash`, `stop`, `subagent-stop`). Measured on this repository: 0.06–0.3 s per hook run.

**Linked worktrees.** The repository a hook acts on is the one holding the edited path, so a worktree under `.claude/worktrees/` is judged on its own branch and files (`HookTest.test_nested_worktree_resolves_to_its_own_root`; the `CoreTests` also run in a linked worktree, `LinkedWorktreeTest`). `settings.json` wiring is pinned by `SettingsTest`.

### restore-violations

`python3 tools/design/gate.py restore-violations` puts each gated path with an open violation back to the content it had before the command that opened it, in the working tree and the index; a file that did not exist before is deleted. It restores nothing while the gate check passes, skips a path that changed since the violation (and prints the three blob ids to compare with `git show <id>`), saves the content it overwrites as a blob first and prints its id, and never touches a non-gated path or a ledger file. When the violating content is also committed, it names the commit to amend (`git commit --amend --no-edit`) or undo (`git reset --soft <commit>^`). The hooks close a violation at the next command once the working tree, the index and `HEAD` hold the earlier content, or once the check passes (the owner approved). Tests: `gate_test.py` `RestoreViolationsTest`; `design_gate_test.py` `FixRoundTest.test_restore_*`, `test_an_abandoned_sessions_violation_does_not_destroy_later_work`.

### The ledger tripwire

Before each shell command the hook records the blob ids of `approvals.json` and `approvers.txt`; after it (success or failure) it compares. If either changed, the hook reports it with the command that was running, says that this is fine if the owner approved at their terminal at that moment, and otherwise asks the agent to stop and tell the owner, who can `git checkout -- <file>`. Nothing is restored. The main agent's next Stop is blocked once with the same text.

### What the hooks cannot see, and what catches it instead

- Shell indirection the parser cannot resolve: `t=design:approve; task $t`, `xargs task` reading targets from stdin, aliases, functions, and scripts that run owner commands. The owner commands still refuse without a terminal and under the Claude Code CLI.
- Ledger writes pre-bash cannot parse (`python -c`, `git apply`, `patch`, an editor, any program writing files itself) are caught only after the fact, by the ledger tripwire (reported, not restored). CI's `bad-ledger` finding and the owner's review of `approvals.json` diffs remain.
- Gated writes pre-bash cannot parse are caught after the command by post-bash as violations, and at merge by CI.
- Generated gated code hidden in the stash.
- Commits on a branch other than HEAD's (`git commit-tree`, or commits in another worktree outside the parsed forms) are not checked for new gated blobs. CI's merge check sees them.
- A ledger entry forged with the owner's own identity cannot be told apart in CI; the owner's review of `approvals.json` diffs is the control.
- `mcp__terminal__run_in_terminal` returns once the command is typed, so post-bash sees only what happened by then. The commit check and CI still stop the code.
- A cancelled or denied command followed by another agent command in the same turn: cancelling fires no hook, and a denial outside auto mode fires none either, so the next command's catch-up compares against that record. This is right if the cancelled command wrote files. It is wrong if the human edited gated files during that same turn; then `restore-violations` skips anything changed since, and the violation closes when the owner approves.
- Human edits made while an agent turn is running (no new prompt in between) can be attributed to the agent command that was running then. The `after` check in `restore-violations` keeps a later human edit from being overwritten.
- The Claude Code CLI is recognised by an executable named `claude`, a node `claude-code` entry point, or node titled `claude`. A renamed binary, or a session that detaches its children from the CLI, is not recognised; an unknown process tree never refuses.
- Work done outside a hooked session (a terminal, another tool, a session started before the hooks existed) is not seen by the hooks at all. The commit check in a later hooked session, and CI's merge check, catch it.

## 9. Parity

Parity proves that the app renders what the owner approved. For each approved state of each enabled group, `frontend/e2e/parity/parity.pw.ts` drives the real app into the state with mocked data, opens the same `#sN` in the prototype, extracts both trees with the snapshot's own extractor at every review size, and fails on the first difference (node path, field, both values).

### Writing a case

`frontend/e2e/parity/cases.ts` exports `CASES: ParityCase[]`:

```ts
export interface ParityCase {
  state: number
  drive?(page: Page): Promise<void>     // put the app into the state; the page is loaded and its mock control installed
  interact?(page: Page): Promise<void>  // reproduce a forced look (data-force) with a real hover, focus or press
  skip?: string                         // why the state has no app counterpart; the test is skipped with it
}

{ state: 102, drive: (page) => page.evaluate((f) => window.__choixParity.load(f), fixtureFor(102)) }
```

Native replicas (700–799) and the gallery (800–899) are already listed as skips. A real hover also hovers every ancestor, so an ancestor's `:hover` look is a real difference: the prototype state must force the same ancestors.

### Enabling a group

Add the group id to `frontend/e2e/parity/enabled.json`. Then:

- Every approved state of the group (a ledger entry whose hash equals the current hash and whose `by` is listed in `approvers.txt`, the same trust rule as the gate) gets a test. One without a case fails with `no parity case for #N`. A group with nothing to compare (and not made only of skip entries, like `native`) gets a failing test named `group <id>`, so `--pass-with-no-tests` cannot pass quietly. Stale states are never parity-tested; `unapproved` blocks them.
- The merge check reports `parity-not-enabled` while app files exist (non-test files under `frontend/` that are gated) whose groups are missing. `parityGroups` in `gate.json` maps app folders to groups (`frontend/src/app/library/**` → welcome and library, `…/focus/**` → focus and compare, …); an app file no glob names needs every group except the gallery; `native` is always satisfied.
- `PARITY_APP_URL` must point at the served app parity build (`bun run build:parity` in the engineering plan); without it an enabled group fails with a message saying so. The page must install `window.__choixParity` within 15 s.

CI runs parity in its own `parity` job of `.github/workflows/ci.yml` (Linux, Chromium): it builds the prototype (`task design:build`), then runs `task parity`, which builds the prototype and the app's parity build (`bun run build:parity`: the app on the mock bindings), serves them on 127.0.0.1 (ports 8811 and 8801), runs `bunx playwright test -c playwright.design.config.ts --project parity` with `PARITY_APP_URL` set to the app, and stops both servers. That runs the self-test (`frontend/e2e/parity/selftest.pw.ts`) and one test per approved state of each group in `enabled.json`. Run `task parity` locally the same way (`-- --grep <group>` for one group).

### Rules the app must follow

- **Scripted-render mode.** The prototype is opened with `?freeze`, which sets `html[data-freeze]` (no animation and no transition on any element). Parity sets the same attribute on the app's `<html>` before driving it, so both sides compute the same frozen values.
- **One root, filling the window.** Both sides mount the shared `AppRoot` (`[data-app-root]`); only its subtree is compared. The prototype's frame pins the root to the review size; for the root only, width and height are compared as the laid-out size, plus its position and whether the document scrolls. The app's root must fill the window exactly.
- **Document-wide lists.** `pseudoRules`, `motionRules`, `atRules` and `layers` are compared as an ordered subsequence: every rule in the app's list must be in the prototype's, in the same relative order. The prototype may carry extra rules (its stylesheet also covers the navigator and mock screens); the app may not. So app code adds no pseudo-element or motion utilities of its own: put them in the design folder, where the prototype shows them and the owner approves them. Fonts and the `<html>` record must be equal.
- **Forced looks.** The `data-force` attribute never reaches a tree, and its CSS twins are stripped from the rule lists on both sides; the look itself is compared through the case's `interact`.

### Integrating the app

- The app entry gives `html`, `body` and `#root` the full window height and mounts `AppRoot` without a size of its own: parity compares the root's laid-out size and position with the window the prototype draws.
- Only `Sheet`, `Prompt` and `Dropdown` close themselves on Escape. Toasts and panels are closed by their host, so the app needs its own `OverlayContext` provider (to know which overlay is on top) and a key handler that closes it.
- Design code owns no timers and no clock (lint rules `timer` and `clock`), so a toast's expiry, a delayed indicator and the current time belong to the app's containers.
- Never pass the `force`, `forceItem` or `forceTitle` props in the app: they exist only to draw a look in the prototype.
- The first gated app file (`frontend/index.html`, `frontend/src/main.tsx`) is named by no `parityGroups` glob, so it maps to every parity group: from then on the merge check requires every group except the gallery in `enabled.json`, with a case or a skip for each approved state, and CI must build and serve the app's parity build and pass it as `PARITY_APP_URL`.
- A branch that reaches `main` together with, or before, the design branch must list all 144 states in its change file's `states:`, because `unlisted-state` compares with the base, which has none of them.
- A change to dependencies only (`frontend/package.json`, `frontend/bun.lock`, both digest inputs) needs a change file with `ui: true` and `states: []`, and a re-snapshot (`sourceDigest` moves, the state hashes do not).

### What parity cannot catch

- Properties left out by `data-measured` (the strip mask, Focus zoom and pan, scroll positions): compared on neither side.
- A prototype-only rule that reaches a design element's motion or pseudo-element from outside the app root: the subsequence rule lets the prototype carry it.
- Themes and OS looks: only sizes vary (one theme, one OS today).
- Behaviour over time: timers, transitions, what happens between states.
- Native surfaces and the gallery (skipped), and any state not approved.

## 10. The owner's one-time setup

1. In your own terminal, at the repository root: `task design:init`. It writes `docs/design/approvers.txt` with your `git config user.email` and an empty `docs/design/approvals.json`, after a `[y/N]` question. It never overwrites: it creates whichever file is missing, and if a ledger with entries exists but no approver list does, it asks a second time before adopting those entries. Commit both files.
2. Recommended GitHub settings for `main` (**not configured by this repository**; set them in the repository settings, branch protection or a ruleset):
   - Require status checks to pass before merging, including the `design` job (and `ci`), and require branches to be up to date before merging (two branches changing `state-hashes.json` or `approvals.json` conflict).
   - `gate-changed` and `approvers-changed` do not clear: nothing a branch can do makes them pass. A pull request that legitimately changes `gate.json`'s scope or `approvers.txt` is merged by the owner themselves, bypassing the required check (an administrator's merge), after reading the diff of that file. There is no other mechanism, by design.
   - Require signed commits, so a ledger commit is attributable.
   - Restrict who can push to `main`, and require a pull request.
3. Optionally add a CODEOWNERS entry for `docs/design/approvals.json`, `docs/design/approvers.txt` and `docs/design/gate.json`, so changes to them need your review (also not configured here).

## 11. Troubleshooting

**`task design:approve` refuses**

| Message | Cause and fix |
|---|---|
| `approve needs a terminal: only the owner runs …` | stdin or stdout is not a terminal, or `CLAUDECODE` / `CLAUDE_CODE_ENTRYPOINT` is set. Run it in your own terminal, not through an agent. |
| `… this process runs under the Claude Code CLI (<path>), so an agent started it …` | An ancestor process is the Claude Code CLI. Use a separate terminal (the desktop app's Terminal panel is fine). |
| `<email> is not in docs/design/approvers.txt` / `… does not exist: the owner runs task design:init first` | Your `git config user.email` is not listed, or `init` has not run. |
| `docs/design/state-hashes.json is not current …: run task design:snapshot first` | The source changed since the snapshot. Snapshot, then approve. |
| `cannot reach http://localhost:8811/__build.json (…)` | Nothing is served. `task design:build && task design:serve`. |
| `the server at http://localhost:8811 is not serving a prototype build (…)` | Something else answers on 8811 (often `task design:dev`, which has no `/__build.json`). Stop it and serve the static build. |
| `the build being served at … has no digest` / `… was not built from this source (it reports …, the source is …)` | The served build is older than the source, or was built from another worktree. Run `task design:build` here; restart `task design:serve` if it serves another folder. |
| `snapshot --check --states … failed: the rendered states differ from docs/design/state-hashes.json` | A fresh render of those states does not match the snapshot. The output above it lists each differing state and variant. Run `task design:snapshot`; if hashes change, review those states again before approving. |

**Stop is blocked: "this session changed the prototype's source and docs/design/state-hashes.json no longer matches it".** Run `task design:snapshot`. If the snapshot was already stale before your session, the hook only notes it.

**A violation after a generator or script ran.** The message lists each gated file and its earlier content. Run `python3 tools/design/gate.py restore-violations`, then amend or undo a commit if it names one. Do not try to approve; ask the owner if the change is needed.

**Committing the owner's ledger changes.** `approvals.json` shows as modified after the owner approves. Commit it as it is, naming the files: `git add docs/design/approvals.json && git commit -m "docs(design): the owner's approvals"`. Never `git add -A` across unrelated work, and never edit the file.

**`git stash` (or `checkout`, `switch`, `reset --hard`, `restore`) is blocked: "would discard the owner's uncommitted approvals".** The ledger has uncommitted changes. Commit them first as above, then run the command again.

**`no-change-file` in a throwaway worktree although the feature branch is approved.** The gate identifies a branch by name: the change file is `docs/design/changes/<branch-slug>.md`, and `gate.py` takes the branch from `--branch`, else `GITHUB_HEAD_REF`, else the checked-out branch (`git symbolic-ref HEAD`), else the branch being rebased (`rebase-merge/head-name` or `rebase-apply/head-name`); a detached HEAD with none of these is `no-branch`. The hooks pass no `--branch`, so an agent in a worktree on its own branch (for example `worktree-agent-…`) is judged as that branch, which has no change file, and every gated edit there fails with `no-change-file` even when the feature branch is fully approved. Two workable options: run gated work on the feature branch itself, one writer at a time; or give the worktree branch its own change file with the same content (its `branch:` set to the worktree branch) and delete that file before merging the work back. `gate.py` is not changed for this.

**`design` CI job red with `unapproved`.** Expected until the owner approves; the job log lists the states and the approve command.

**Two design commands at once fail with 404s or harness findings.** Every task that builds empties and rewrites `frontend/dist-prototype/`. Run them one at a time.
