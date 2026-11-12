# Choix 2.0 Design Prototype and Approval Gate — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** revision 5, after three review rounds by Fable and Codex gpt-6-astra (all findings folded in). Waiting for the owner's OK. No code is written before it.

**Goal:** A clickable prototype showing every Choix 2.0 screen in every state, plus a gate that keeps app UI code from being written or merged while any state lacks the owner's approval.

**Architecture:** One Vite project in `frontend/` with two build entries: the app and the prototype. Screens are presentational React components in `frontend/src/design/`, mounted by both. Browser-driving tools (checks, flows, fingerprints, parity) run on Node through Playwright. The gate's decision logic (digest, branch check, human commands, hooks) is Python standard library only, so hooks, CI and the command line share one implementation.

**Tech Stack:** React 18, TypeScript 5, Tailwind 3, PostCSS, Vite, bun (package manager; `bun test` for pure-logic unit tests), Playwright on Node ≥ 20 (Chromium for fingerprints; Chromium + WebKit for checks), Python ≥ 3.9 stdlib, Task (`Taskfile.yml`), Claude Code hooks, GitHub Actions, Docker (local Linux run).

**Spec:** `docs/superpowers/specs/2026-11-11-choix-2-desktop-design.md` (§4 product, §4.6 look, §15 design gate). The owner's process prompt of 2026-11-11 is the authority for the gate; its blanks are filled in below.

## Context (the prompt's blanks, filled in)

| Blank | Value |
|---|---|
| App | Choix — macOS app for culling photo and video shoots |
| UI stack | Wails v3 + React + TypeScript + Tailwind + Vite, bun |
| UI source folder | `frontend/` (new; v1's `internal/ui/web/` is the source being ported and is removed by the engineering plan) |
| Window sizes | `min` 960×600, `default` 1280×800, `large` 1680×1050 |
| Themes | `dark` only (the toggle exists with one option) |
| OS variants | `macos` only (the toggle exists with one option) |
| Design source | none (no Figma), so `figma-compare` is not built. Each state names its reference: `mockup-2026-11-11#<screen>`, `v1:<file>`, or `missing design` |
| Approvers | the repository owner, who creates `docs/design/approvers.txt` with `task design:init` |

## Decided by the owner

- **Mock photos are synthetic** (2026-11-11). The repository is public, so none of the owner's own photos is committed.

## How this plan reads the prompt where it left room

Each is a call the owner can veto.

1. **Third review size** (`large`), because the Library's photo strip changes most on wide windows.
2. **Everything under `frontend/` is gated unless exempted**, so a file placed in an unexpected folder is still caught. Native UI built in Go (menus, alerts, window chrome) lives in three gated packages: `internal/app/menu/`, `internal/app/dialogs/`, `internal/app/window/`.
3. **One rule for app UI edits: every state is approved at its current hash.** This is workflow step 5 taken literally. A new or changed state blocks all app UI edits until the owner approves it. (Revision 2 unlocked by screen group; both reviewers found holes in that, and the prototype is complete before the approve command exists anyway.)
4. **Edit time and merge time differ in one respect.** Editing needs every state approved. Merging additionally needs the change file to list every state this branch changed, so the pull request says what moved.
5. **A branch that changes only the design folder still needs a change file**, because the app ships those components.
6. **`ui: false` needs a human confirmation.** The prompt says "a reviewer confirms it from the diff". Here that is `task design:confirm-no-ui`, humans only, which records the changed app files and their contents; a later change to any of them invalidates it. It is checked at merge, because it covers the finished diff; while editing, a `ui: false` branch still needs every state approved.
7. **Retiring a state is acknowledged by the owner** (`task design:approve -- --retire N`), since a removed state has no hash to approve.
8. **The Stop hook and "ask, then stop".** The prompt has Stop block "while the branch check fails", and also tells the agent to stop after asking for review, when states are necessarily unapproved. Reading used here: Stop blocks on what this session caused and can fix by itself — a snapshot it made stale, a change file it owes, or an app file it changed while the gate was failing (fixed by restoring it). States awaiting approval are printed and never block. After three identical blocks in a row the hook lets the session stop with a visible warning, so it cannot be trapped by something only the owner can resolve.
9. **Native surfaces** are reviewed as HTML replicas built from `frontend/src/design/menus.json`; Go builds the native menu from the same file. The folder picker and the About panel are system panels and are listed in the inventory without a state.
10. **Forced interaction looks.** A PostCSS step clones every `:hover`, `:focus`, `:focus-visible`, `:focus-within` and `:active` rule with a `[data-force~=…]` twin, so those looks can be shown and fingerprinted as static states. A test proves forced paint equals real paint.
11. **Orphans.** `text-wrap: pretty` exists only from Safari 26 and `balance` from Safari 17.5, and the app targets older macOS too. Copy goes through a helper that binds the last two words with a no-break space; `text-wrap` is an extra. The orphan check also runs with `text-wrap` switched off.
12. **Clipping, occlusion and alignment checks** are added to `check-states` (the owner's "everything lines up").
13. **Hashes include the browser revision and a digest of the extractor's code**, so a Playwright upgrade or an extractor change makes approvals visibly stale. The package is pinned to an exact version.
14. **Layout values are computed values, not used values.** The prompt calls them "authored"; `computedStyleMap()` returns computed values (`auto` and percentages survive, `em` resolves). That is the property the prompt is after: no laid-out pixel boxes.
15. **Parity cases arrive with the app.** This plan builds the parity harness and its self-test. A merge fails if app UI code exists while the parity cases of the groups it maps to are not enabled; app code outside a screen's own folder maps to all groups.

## Known consequences

- The owner approves all states (about 135) before any app screen is wired. The Go side is not gated and proceeds meanwhile.
- This branch's `design` CI job is red until then. That is the gate working; Task 8 reads the job log to confirm it fails for that reason and no other.
- `approvals.json` and `state-hashes.json` are single files. Two branches changing them will conflict: `state-hashes.json` is resolved by re-running the snapshot, `approvals.json` by the owner. The repository should require branches to be up to date and mark `design` as a required check (owner's settings, documented in the README).
- Hooks are read when a Claude Code session starts, so enforcement is live from the first session after Task 8 lands. Code already in the tree from another session is not a hook violation; it is caught at commit time and by CI.
- A full snapshot renders every state at three sizes. Task 6 sets a time budget; sub-agents are not asked to snapshot, only the coordinating session.

## Global Constraints

- Literal colours, font sizes, radii, shadows, masks and durations exist **only** in `frontend/src/design/tokens.css`. `tailwind.config.ts` contains only `var(--…)` references and is linted like any other file.
- Fonts are bundled `woff2` (Inter 400/500/600/700, JetBrains Mono 400/500). No request leaves `localhost`.
- Screens in `frontend/src/design/` import nothing from a store, router, bindings, `fetch` or Wails. **Everything visible that distinguishes one state from another is a prop** (open menu, selected row, active section, zoom, busy); screens keep no `useState` for it.
- Every component lives in `design/components/`; a file under `design/screens/` only composes components and lays them out. A missing component becomes a shared component, never a one-off inside a screen. A screen never renders a raw `<button>`, `<input>`, `<select>` or `<textarea>`.
- No `::before` / `::after` content (use a real element), no `loading="lazy"`, no `setTimeout` / `setInterval`, no `Date.now()`, argument-less `new Date()` or `Math.random()` in `src/design/` or `prototype/`. Timers (toast auto-dismiss, delayed indicators) belong to the app's containers.
- Every focusable control has an explicit focus ring (never `outline-style: auto`). Keyboard labels are macOS glyphs (⌘ ⇧ ⌥ ⌃), never derived from the user agent.
- Every dialog, menu, popover, sheet and collapsible panel registers through `useOverlay`.
- Scenario numbers become permanent at the first committed `state-hashes.json` and are never renumbered or reused afterwards.
- Fixed clock `2026-11-11T10:00:00Z`, timezone UTC, locale `en-US`, reduced motion, device scale 1, one fresh page per state and size, for every scripted render.
- Python tools: standard library only, Python ≥ 3.9. Hooks fail open on internal errors.
- An agent never runs `design:init`, `design:approve` or `design:confirm-no-ui`, and never writes `docs/design/approvals.json` or `docs/design/approvers.txt`.
- Each task ends with its tests green, and the real output of its final verification is pasted into the task report and relayed to the owner.
- Code is written by Opus sub-agents; Fable and Codex review.
- Commits: one topic each, prefixes `feat(design):`, `test(design):`, `chore:`, `docs:`.

## Review Focus

1. **A hash that differs between two runs or two machines.** Pinned by the two-run test and the Docker Linux run (Task 6) and by CI on both platforms (Task 8).
2. **A layout that breaks only at `min`.** Pinned by clipping, occlusion and alignment checks at all three sizes (Task 4).
3. **Unusual content**: an 80-character folder name, an RTL or emoji path, 0 and 1 counts, forty folders. Pinned by states s109, s110, s119, s121, s122 and the `plural` tests (Tasks 2–3).
4. **The first app UI edit on a fresh branch**, by an edit tool, a shell redirect, or a generator followed by a commit in one command. Pinned by the hook tests (Task 8).
5. **A swapped image or font under the same file name.** Pinned by the resource-identity tests (Task 6).

---

## File Structure

```
Taskfile.yml                              design:* and frontend tasks (the engineering plan extends it)
frontend/
  package.json  bun.lock  tsconfig.json  tailwind.config.ts  postcss.config.cjs
  vite.config.ts                          app entry (created by the engineering plan)
  vite.prototype.config.ts                prototype entry (prototype/index.html → dist-prototype/), port 8811
  playwright.design.config.ts             projects: checks, flows, extract, snapshot, parity
  index.html  src/main.tsx  src/app/**    the app (GATED; created by the engineering plan after approval)
  src/design/
    tokens.css  base.css  fonts/*.woff2
    types.ts  format.ts  overlay.ts  measured.ts  menus.json
    components/                           every component; existing files are coordinator-owned after Task 2
    screens/{welcome,library,folder,focus,settings,overlays,native}/   each with props.ts
  prototype/
    index.html  main.tsx  store.ts  clock.ts  harness.ts
    registry.ts                           pure: defineScenarios, GROUPS, RegistryError
    scenarios/index.ts                    Vite-only: collects scenarios/*.tsx with import.meta.glob
    scenarios/<group>.tsx  scenarios/<group>.test.ts
    mock/{data.ts,<group>.ts,photos/*.jpg,photos.json}
    navigator/{Navigator.tsx,search.ts}
  tools/
    serve.mjs                             static server for dist-prototype (+ /__build.json)
    postcss-force.cjs                     clones interaction selectors with data-force twins
    lint-design.ts  make-mock-photos.ts  inventory.ts
    checks/{clip,occlusion,orphans,align,focus}.ts
    check-states.pw.ts  flows.pw.ts  flows/<group>.ts  checks.pw.ts
    extract.ts  extract.pw.ts  snapshot.pw.ts  snapshot.mjs
    fixtures/broken-states.tsx
  e2e/parity/{parity.ts,cases.ts,enabled.json,parity.pw.ts,selftest.pw.ts}
tools/design/
  gate.py                                 digest | check | status | restore-violations | init | approve | confirm-no-ui
  gate_test.py
.claude/settings.json  .claude/hooks/design_gate.py  .claude/hooks/design_gate_test.py
.github/pull_request_template.md  .github/workflows/ci.yml (design job)
docs/design/
  README.md  inventory.md  gate.json  retired.json
  approvers.txt  approvals.json           created by the owner with `task design:init`
  state-hashes.json  changes/<branch-slug>.md
```

Runner split: `*.test.ts` files are pure logic and run under `bun test`. `*.pw.ts` files drive a browser and run on Node with `bunx playwright test -c playwright.design.config.ts --project <name>` (`testMatch: '**/*.pw.ts'`). Nothing that imports Playwright or uses a Vite-only API (`import.meta.glob`) is loaded by `bun test`.

## State inventory

Ranges: welcome and shell 10–49, library 100–199, folder 200–299, focus 300–399, compare 400–449, settings 500–599, overlays 600–699, native replicas 700–799, gallery 800–899. Before the first snapshot each group owner checks every listed state against v1 and the spec and drops any the code cannot reach; after the first committed snapshot the numbers are permanent.

| Group | States |
|---|---|
| Welcome, shell | 11 CLIP downloading, quality queued · 12 CLIP failed (Retry) · 13 quality failed · 14 offline, both failed · 15 visual grouping off · 16 quality rating off · 17 CLIP ready, quality downloading · 30 scan discovering (indeterminate), in its row · 31 rating quality, in its row · 32 scan stopped, in its row · 33 scan: reading metadata · 34 scan: thumbnails · 35 scan: analyzing · 36 scan: clustering · 37 two folders rating quality, in their rows · 40 error screen (a screen failed to render, and so did the Library) |
| Library | 100 empty (drop zone) · 101 drop target active *(needs spike S1)* · 102 filled, six folders · 103 row hovered (actions shown) · 104 row keyboard-focused · 105 one scanning, one queued · 106 scan error row · 107 offline row · 108 undated folder · 109 forty folders · 110 80-char name, long RTL/emoji path · 111 loading · 112 compact model-download banner · 113 "folder removed" toast · 114 "already in Library" toast · 115 read-only volume error · 116 folder spanning three months · 117 scanning, previews not ready · 118 single folder · 119 folder with 0 photos · 120 compact banner, download failed · 121 folder with 1 photo · 122 folder with 1 video · 123 two folders scanning, one queued · 124 search with results · 125 search, no match · 126 sort menu open · 127 show menu open · 128 sorted by name (one list, no month headers) · 129 showing the folders that need attention · 130 filter with nothing to show · 131 new folder processing, at the top |
| Folder | 200 loading · 201 first scan, stage checklist · 202 filled · 203 with picks and rejects · 204 filter text with results · 205 filter, no match · 206 camera menu open · 207 cluster keyboard-selected · 208 scan running over content · 209 scan stopped · 210 hide rejected on · 211 cross-device merged cluster · 212 quality badges off · 213 video cluster *(S3)* · 214 no photos or videos found · 215 re-clustering busy · 216 time-only notice (model downloading) · 217 cluster fully reviewed · 218 sort menu open · 219 camera filter applied · 220 strip with `+N` overflow · 221 failed-files count · 222 re-cluster done toast · 223 re-cluster failed toast · 224 folder went offline while open · 225 read-only volume while open · 226 picks missing their copies (notice) · 227 visual grouping off (no notice) · 228 time-only notice (model download failed) · 229 title bar with quality indicator · 230 first scan, clustering, another folder rating · 231 long name, scan running (indicator keeps its label) |
| Focus | 300 loading · 301 photo · 302 picked · 303 rejected · 304 Info panel open · 305 Info with GPS · 306 video *(S3)* · 307 zoom 2× · 308 zoom 8×, panned · 310 all reviewed, cluster-done pill · 311 single-photo cluster · 312 image failed to load · 313 preview shown while full loads · 314 quality badge absent · 315 pick in flight (buttons disabled) · 316 cluster failed to load · 317 only cluster in folder (cluster navigation disabled) · 318 long cluster, rail scrolled to the active photo |
| Compare | 400 two panes · 401 three panes · 402 four panes · 403 active pane picked · 404 zoomed, panes in lockstep · 405 pane with video *(S3)* · 406 one pane · 407 no panes selected · 408 active pane rejected · 409 pane with pick in flight |
| Settings | 500 saved values, Grouping section active · 501 unsaved changes (section marked) · 502 bucket validation error · 503 picks-folder validation error · 504 saving · 505 quality model downloading · 506 quality download failed (Retry) · 507 visual grouping off · 508 leave-without-saving prompt · 509 quality scoring progress · 510 loading · 511 failed to load · 512 save failed toast · 513 quality waiting for scan · 514 quality error · 515 retrying · 516 CLIP downloading row · 517 CLIP failed row · 518 at defaults · 519 Behaviour section active · 520 Output section active |
| Overlays | 600 shortcut sheet · 601 pick toast with Undo · 602 error toast · 603 three stacked toasts · 604 warning toast · 605 info toast |
| Native | 700 Choix menu · 701 File (Library, folder selected) · 702 File (Folder) · 703 Edit · 704 Photo (disabled, Library) · 705 Photo (Focus) · 706 View · 707 Window · 708 Help · 709 File (Library, nothing selected) · 710 folder-row context menu · 711 context menu, offline folder · 712 context menu, scanning folder · 720 "Remove from Library" alert |
| Gallery | 800 colours · 801 type scale · 802 buttons (rest, hover, focus, pressed, disabled, busy) · 803 inputs and switches · 804 thumbnails (rest, picked, rejected, current, current and picked or rejected) · 805 badges, dots, progress, spinner · 806 menu and alert replicas · 807 title bar variants, including full screen (no traffic-light inset) · 808 title bar in a macOS 27 window, centred on the traffic lights (drawn) · 809 the current item, one look everywhere (row, cluster, rail photo, compare pane; picked; keyboard focus) |

Not states, listed in the inventory with the reason: the folder picker and the About panel (system panels); scroll offsets (not part of a hash).

---

### Task 1: Frontend workspace, tokens, lints

**Files:**
- Create: `frontend/{package.json,tsconfig.json,tailwind.config.ts,postcss.config.cjs,vite.prototype.config.ts,playwright.design.config.ts}`, `frontend/prototype/{index.html,main.tsx}` (placeholder), `frontend/src/design/{tokens.css,base.css,fonts/*.woff2}`, `frontend/tools/{lint-design.ts,postcss-force.cjs,serve.mjs}`, `docs/design/gate.json`, `docs/design/retired.json` (`[]`), `Taskfile.yml`
- Test: `frontend/tools/lint-design.test.ts`, `frontend/tools/postcss-force.test.ts`
- Modify: `.gitignore` (`frontend/node_modules/`, `frontend/dist/`, `frontend/dist-prototype/`, `frontend/.design/`)

**Interfaces:**
- No app entry is created by this plan: `frontend/index.html`, `frontend/src/main.tsx`, `frontend/src/app/**` and `vite.config.ts` belong to the engineering plan, after approval.
- Package scripts created here: `proto:dev`, `proto:build`, `proto:serve` (`node tools/serve.mjs`), `check` (`tsc --noEmit`), `test` (`bun test`), `lint:design`. Task targets created here: `frontend:install`, `design:dev`, `design:build`, `design:serve`, `test:frontend`. Later tasks add their scripts and targets in the task that creates the tool.
- `playwright.design.config.ts` puts `outputDir` and every reporter's output under `frontend/.design/` (git-ignored), so a test run never creates a file in a gated path.
- Exact versions, no `^`. `vite.prototype.config.ts`: `root: 'prototype'`, `build.outDir: '../dist-prototype'`, `build.emptyOutDir: true`, `server.port: 8811`, `strictPort: true`, `server.fs.allow: ['..']`. Tailwind `content`: `./src/**/*.{ts,tsx}`, `./prototype/**/*.{ts,tsx,html}` (the engineering plan adds `./index.html`).
- `gate.json` (complete from the start; later tasks only read it):
  ```json
  { "reviewUrl": "http://localhost:8811",
    "sizes": { "min": [960, 600], "default": [1280, 800], "large": [1680, 1050] },
    "themes": ["dark"], "os": ["macos"],
    "gated": ["frontend/**", "internal/app/menu/**", "internal/app/dialogs/**", "internal/app/window/**"],
    "exempt": ["frontend/src/design/**", "frontend/prototype/**", "frontend/tools/**", "frontend/e2e/**",
               "frontend/bindings/**", "frontend/node_modules/**", "frontend/dist/**", "frontend/dist-prototype/**", "frontend/.design/**",
               "frontend/package.json", "frontend/bun.lock", "frontend/tsconfig.json", "frontend/tailwind.config.ts",
               "frontend/postcss.config.cjs", "frontend/vite.config.ts", "frontend/vite.prototype.config.ts",
               "frontend/playwright.design.config.ts", "**/*.test.ts", "**/*.test.tsx", "**/*.pw.ts", "**/*_test.go"],
    "prototypeOwned": ["frontend/src/design/**", "frontend/prototype/**"],
    "digestInputs": ["frontend/src/design/**", "frontend/prototype/**", "frontend/package.json", "frontend/bun.lock",
                     "frontend/tsconfig.json", "frontend/tailwind.config.ts", "frontend/postcss.config.cjs",
                     "frontend/vite.prototype.config.ts", "frontend/tools/postcss-force.cjs", "frontend/tools/extract.ts",
                     "frontend/tools/snapshot.pw.ts", "frontend/tools/snapshot.mjs", "docs/design/gate.json", "docs/design/retired.json"],
    "parityGroups": { "frontend/src/app/library/**": ["welcome", "library"], "frontend/src/app/folder/**": ["folder"],
                      "frontend/src/app/focus/**": ["focus", "compare"], "frontend/src/app/settings/**": ["settings"],
                      "frontend/src/app/shell/**": ["overlays"] } }
  ```
- `tokens.css`: colours, radii and shadows exactly as v1's `tailwind.config.js`; `--mask-opaque: #000`; durations `--dur-fast|base|slow` = 120/200/320 ms; font sizes named by size, `--text-9, -10, -11, -12, -13, -14, -15, -17, -18, -20, -22, -30`.
- Tailwind theme: `fontSize` is **replaced** with `text-9 … text-30`, so Tailwind's `text-xs`, `text-sm`… do not exist. Port mapping from v1: `text-[9px]`→`text-9`, `text-[11px]`→`text-11`, `text-[12px]` and `text-xs`→`text-12`, `text-[13px]`→`text-13`, `text-sm`→`text-14`, `text-[15px]`→`text-15`, `text-[18px]`→`text-18`, `text-[22px]`→`text-22`, `text-3xl`→`text-30`; the mockup's 10, 17 and 20 px are `text-10`, `text-17`, `text-20`. Built-in variants are left untouched.
- `postcss-force.cjs`: a PostCSS plugin run after Tailwind. For every rule with a selector containing `:hover`, `:focus-visible`, `:focus-within`, `:focus` or `:active`, it appends a copy of that selector with the pseudo-class replaced by `[data-force~="hover"]`, `[data-force~="focus"]` (for both focus forms), `[data-force~="focus-within"]`, `[data-force~="active"]`. Order and specificity of the original rule are kept.
- `base.css`: `@font-face` rules; v1's scrollbar rules; `font: inherit; color: inherit` on form controls; `h1,h2,h3,[data-wrap="balance"]{text-wrap:balance}`, `p,li,[data-wrap="pretty"]{text-wrap:pretty}`; `html[data-nowrapfx] *{text-wrap:wrap !important}`; the freeze rule `html[data-freeze] *{animation:none !important;transition:none !important}` last (v1's reduced-motion pulse on the indeterminate rail is ported without `!important`).
- `lintDesign(root: string): Finding[]`, `Finding = { rule: 'token'|'import'|'raw-control'|'pseudo'|'clock'|'timer'|'lazy'|'isolation'; file: string; line: number; text: string }`:
  - `token`: `#hex`, `oklch(`, `rgb(`, `hsl(`, `text-[…px]`, `rounded-[`, `duration-[`, or a literal `font-size`, `border-radius`, `transition-duration` outside `tokens.css`.
  - `import`: a file in `src/design/` importing `@wailsio/runtime`, a path containing `/app/` or `/bindings/`, or calling `fetch(`.
  - `raw-control`: `<button`, `<input`, `<select`, `<textarea` in `src/design/screens/`.
  - `pseudo`: `before:`, `after:`, `::before`, `::after` in `src/design/`.
  - `clock`: `Date.now(`, `new Date()`, `Math.random(`; `timer`: `setTimeout(`, `setInterval(` — both in `src/design/` and `prototype/`, except `prototype/harness.ts` (the readiness time-out).
  - `lazy`: `loading="lazy"`.
  - `isolation`: a file under `frontend/src/`, `frontend/index.html` or `vite.config.ts` referencing `prototype/`.
- `serve.mjs [--port N]`: serves `dist-prototype/` on `127.0.0.1` (default 8811) and `/__build.json` → `{ "sourceDigest": string }` from `dist-prototype/build.json` (written by Task 6's wrapper; `""` until then).

- [ ] **Step 1: Write the failing tests.** `lint-design.test.ts`: one fixture per rule yields exactly one finding of that rule; a clean fixture (`text-12 bg-bg-2 w-[132px]`, a `<Button>`) yields none; the real tree yields `[]`. `postcss-force.test.ts`: compiling a fixture with `hover:bg-bg-4`, `group-hover:opacity-100`, `group-hover/row:opacity-100`, `peer-focus:block`, `focus-visible:ring-2`, `disabled:opacity-40 hover:bg-bg-4` and a handwritten `.x:active{}` emits, for each, both the original selector and its `data-force` twin, and the `disabled:` rule still comes after the `hover:` rule.
- [ ] **Step 2: Run** `cd frontend && bun test tools/` — expected: FAIL, modules not found.
- [ ] **Step 3: Create the workspace, configs, the prototype placeholder, `gate.json`, `retired.json`, `Taskfile.yml`.**
- [ ] **Step 4: Write `tokens.css`, `base.css`, the Tailwind config; add the fonts.**
- [ ] **Step 5: Implement `lintDesign`, `postcss-force.cjs`, `serve.mjs`.**
- [ ] **Step 6: Run** `bun test && bun run check && bun run lint:design && bun run proto:build` — expected: all pass; `dist-prototype/index.html` exists.
- [ ] **Step 7: Commit** `chore: add frontend workspace with design tokens and lints`.

### Task 2: Shared components, types, menus, gallery

**Files:**
- Create: `frontend/src/design/{types.ts,format.ts,overlay.ts,measured.ts,menus.json}`, `frontend/src/design/components/*.tsx`, `frontend/prototype/scenarios/gallery.tsx`
- Test: `frontend/src/design/format.test.ts`, `frontend/src/design/menus.test.ts`

**Interfaces:**
- Components (interactive ones take `force?: string`, a space-separated list of `hover`, `focus`, `active`, `focus-within`, rendered as `data-force`; app code never passes it):
  - `Button({ variant: 'default'|'ghost'|'primary'|'pick'|'reject'; icon?; kbd?; disabled?; busy?; onClick; children })`, `IconButton({ icon; label; … })`
  - `TitleBar({ leading?; title; subtitle?; indicator?; trailing?; fullScreen?: boolean })` — 48 px high; 76 px left inset unless `fullScreen`; the bar is `--wails-draggable: drag`, controls `no-drag`; controls carry `data-align-y="titlebar"`
  - `ScanIndicator({ scan: ScanProgress })`, `QualityIndicator({ status: QualityStatus })` (ported from v1 `primitives.tsx`)
  - `ProgressRail({ value?: number; label })`, `Spinner`, `Dot`, `Kbd`, `Switch`, `Field`, `TextInput`, `Banner`, `EmptyState`, `QualityBadge`, `Icon({ name })`, `Prose({ children: string; wrap?: 'balance'|'pretty' })` (applies `noOrphan`)
  - `Thumb({ src; alt; state?: 'picked'|'rejected'|'selected'|'active'; quality?; video? })`
  - `Dropdown({ id; label; options; value; open: boolean; onOpenChange(open); onSelect(value) })` — controlled; v1's roving-focus keyboard handling kept
  - `Sheet({ id; title; onClose; children })`, `Panel({ id; open; children })` (collapsible side panel), `Prompt({ id; title; message; buttons })` (in-window confirmation), `Toaster({ items: ToastItem[]; onUndo(id); onDismiss(id) })`
  - `MenuBarReplica({ menus: MenuDef[]; open: string | null; screen: ScreenId; hasSelection: boolean })`, `ContextMenuReplica({ id; items: MenuItemDef[]; flags: string[] })`, `AlertReplica({ id; title; message; buttons: { label: string; role: 'default'|'cancel'|'destructive' }[] })`
- `overlay.ts`: `OverlayContext`, `useOverlay(id: string, open: boolean): void`. Called by `Sheet`, `Panel`, `Prompt`, `Dropdown`, `Toaster` and the three replicas. The default context is a no-op (the app); the prototype's provider records `registered`, `opened`, `closed` and routes Escape to the top overlay.
- `measured.ts`: `export const MEASURED: Record<string, { files: string[] }>` — each `data-measured-by` id with the files that compute it.
- `format.ts`: `plural(n, one, many)` (`'1 photo'`, `'0 photos'`, `'5,218 photos'`); `dateRange(first: string | null, last: string | null, timeZone = 'UTC')` (`'Mar 14 – 16'`, `'Apr 28 – May 9'`, `'Dec 28, 2025 – Jan 3, 2026'`, `'Sep 6'`, `''`); `abbreviatePath(p, home)`; `bytes(n)`; `noOrphan(text: string): string` (replaces the last space with U+00A0 when the text has ≥ 4 words; idempotent).
- `types.ts`: v1's `Member`, `Cluster`, `ExifPayload`, `FocusResponse`, `QualityStatus`, `ProgressEvent` moved unchanged from `internal/ui/web/src/api.ts`; `SettingsBody` plus `visual_grouping: boolean`; and
  ```ts
  export type FolderState = 'ready' | 'scanning' | 'queued' | 'offline' | 'error'
  export interface ScanProgress { stage: 'discover'|'metadata'|'thumb'|'analyze'|'cluster'; done: number; total: number; failed: number; state: 'running'|'stopped' }
  export interface FolderSummary { id: string; name: string; parentPath: string; state: FolderState; month: string | null /* 'YYYY-MM' */; firstCaptured: string | null; lastCaptured: string | null; photos: number; videos: number; clusters: number; picked: number; rejected: number; previews: string[] /* ≤ 6 URLs */; scan?: ScanProgress; volumeName?: string; error?: string }
  export interface ModelStatus { kind: 'clip' | 'quality'; state: 'disabled'|'queued'|'downloading'|'failed'|'ready'; received: number; size: number; error?: string }
  export type ScreenId = 'library' | 'folder' | 'focus' | 'compare' | 'settings'
  export interface MediaURLs { thumb(m: Member, tier?: 'thumb'|'preview'): string; full(m: Member): string }
  export interface ToastItem { id: string; tone: 'pick'|'reject'|'warn'|'info'; title: string; body?: string; undo?: boolean }
  export interface FolderFilter { query: string; device: string /* '' = all cameras */; sort: 'time-asc' | 'time-desc' }
  export type MenuItemDef = { id: string; label: string; accelerator?: string; display?: string; enabledIn: ScreenId[]; needsSelection?: boolean; hiddenWhen?: string[] } | { separator: true } | { role: string }
  export interface MenuDef { id: string; title: string; items: MenuItemDef[] }
  ```
- `menus.json`: `{ "menus": MenuDef[], "context": { "folder-row": MenuItemDef[] } }` with exactly the items and shortcuts of spec §4.4.

- [ ] **Step 1: Write the failing tests.** `format.test.ts` with the values above, plus `noOrphan('never leave it')` unchanged and `noOrphan('Your photos never leave it.')` ending in `leave it.`. `menus.test.ts`: item ids unique; accelerators unique; File has `add-folder` with `CmdOrCtrl+O` and `open-picks` with `CmdOrCtrl+Shift+P`; every `enabledIn` value is a `ScreenId`.
- [ ] **Step 2: Run** `bun test src/design` — expected: FAIL.
- [ ] **Step 3: Implement `format.ts`, `types.ts`, `overlay.ts`, `measured.ts`, `menus.json`.**
- [ ] **Step 4: Build the components** by porting v1's markup (`primitives.tsx`, the component classes in `styles.css`, `Toaster.tsx`, `Library.tsx`'s `Dropdown`) onto tokens.
- [ ] **Step 5: Write gallery scenarios 800–807** as an exported array.
- [ ] **Step 6: Run** `bun test && bun run check && bun run lint:design` — expected: pass.
- [ ] **Step 7: Commit** `feat(design): shared components, types and menu manifest`.

### Task 3: Prototype shell, Welcome and Library

**Files:**
- Create: `frontend/prototype/{store.ts,clock.ts,harness.ts,registry.ts,scenarios/index.ts}`, `frontend/prototype/navigator/{Navigator.tsx,search.ts}`, `frontend/prototype/mock/{data.ts,photos.json,photos/*.jpg}`, `frontend/tools/make-mock-photos.ts`, `frontend/src/design/screens/welcome/{props.ts,WelcomePanel.tsx}`, `frontend/src/design/screens/library/{props.ts,LibraryScreen.tsx}`, `frontend/src/design/components/{FolderRow,MonthHeader,DropZone,ModelRow}.tsx`, `frontend/prototype/scenarios/{welcome,library}.tsx`
- Modify: `frontend/prototype/main.tsx`
- Test: `frontend/prototype/registry.test.ts`, `frontend/prototype/scenarios/{gallery,welcome,library}.test.ts`, `frontend/prototype/navigator/search.test.ts`, `frontend/tools/make-mock-photos.test.ts`

**Interfaces:**
- ```ts
  // registry.ts (pure)
  export type GroupId = 'gallery'|'welcome'|'library'|'folder'|'focus'|'compare'|'settings'|'overlays'|'native'
  export interface Group { id: GroupId; title: string; range: [number, number] }
  export const GROUPS: Group[]
  export interface ScenarioCtx { size: 'min'|'default'|'large'; store: ProtoStore; now: Date }
  export interface Scenario { n: number; group: GroupId; label: string; design: string; overlays?: string[]; render(ctx: ScenarioCtx): React.ReactElement }
  export class RegistryError extends Error { code: 'duplicate'|'out-of-range'|'retired' }
  export function defineScenarios(all: Scenario[], retired: number[]): Scenario[]
  // scenarios/index.ts (Vite only): export const SCENARIOS = defineScenarios(<import.meta.glob('./*.tsx', { eager: true })>, retired)
  // store.ts
  export interface ProtoStore { state: Record<string, unknown>; set(patch: Record<string, unknown>): void; go(n: number): void; toast(t: ToastItem): void }
  // harness.ts → window.__choixProto
  interface Harness {
    states: { n: number; group: GroupId; label: string; overlays: string[] }[]
    setToggles(t: { size?: 'min'|'default'|'large'; theme?: 'dark'; os?: 'macos'; freeze?: boolean; nowrapfx?: boolean }): void
    open(n: number): Promise<void>
    overlays: { registered: string[]; opened: string[]; closed: string[] }
  }
  ```
- `open(n)` resolves when: the scenario has rendered; `document.fonts.ready`; `Promise.allSettled` over every image's `decode()`; with `freeze`, every running animation is cancelled; then two consecutive rendering updates pass with no DOM mutation (rejects after `OPEN_TIMEOUT_MS`). A rendering update is a fresh IntersectionObserver's first notification (it comes after that update's layout and ResizeObserver deliveries) followed by a posted message, not `requestAnimationFrame`, which the scripted renders' fixed clock turns into a timer that fires whether or not the page renders.
- URL contract: `/#s102`; query `?size=`, `?theme=`, `?os=`, `?freeze`, `?nowrapfx`, `?bare` (only the app surface at the exact size, element `[data-app-root]`, no navigator). Toggles live in the query string and survive a state change.
- Navigator: grouped sidebar; search by word and by number (`"102"`, `"s102"`); Alt+↓ / Alt+↑ next / previous; "Copy link"; size, theme and OS toggles. Everything it renders carries `data-proto-chrome`.
- `LibraryProps`: `{ folders: FolderSummary[] | null; models: ModelStatus[]; dropActive: boolean; toasts: ToastItem[]; selectedId: string | null; forceRow?: { id: string; force: string }; scan: { folder: string; progress: ScanProgress } | null; menuFor: string | null /* row whose context-menu replica is open */; removePrompt: string | null /* row whose Remove alert replica is open */; onAdd(); onOpen(id); onSelect(id); onOpenPicks(id); onMenu(id | null); onRemove(id); onConfirmRemove(id, confirmed: boolean); onSettings(); onHelp(); onRetryModel(kind); onToggleModel(kind, on: boolean); onUndoToast(id); onDismissToast(id) }`. `WelcomeProps`: `{ models: ModelStatus[]; online: boolean; onAdd(); onRetryModel(kind); onToggleModel(kind, on) }`.
- Mock photos: 36 JPEGs `p01.jpg…p36.jpg`, 480×320, ≤ 40 KB, procedural landscapes from a seeded generator; plus `broken.jpg` (the request succeeds, the bytes do not decode) for image-error states. `photos.json` gives each a capture time.
- Layout A (from the approved mockup): month header = mono eyebrow with a right-aligned count; row 116 px high, radius `lg`; text column 46 % of the row but never narrower than 480 px, `data-align-x="library-text"`; photo strip in the right 70 %, six equal images, 2 px gaps, mask `linear-gradient(90deg, transparent 0, var(--mask-opaque) 46%)`; opacity 0.5 and saturation 0.8 at rest, 0.95 and 1 on hover or focus, 0.3 while scanning, greyscale at 0.18 when offline; actions fade in at top-right; summary line above the list.

- [ ] **Step 1: Write the failing tests.** `registry.test.ts`: `defineScenarios` throws `RegistryError` with `duplicate`, `out-of-range` (a library scenario numbered 250) and `retired`. `scenarios/<group>.test.ts` (one per group, importing that group's file directly): the exported list contains exactly the inventory numbers. `search.test.ts`: `search('empty')` includes s100; `search('s102')` and `search('102')` return s102. `make-mock-photos.test.ts`: two runs into two temp dirs are byte-identical; each file ≤ 40 KB.
- [ ] **Step 2: Run** `bun test` — expected: FAIL.
- [ ] **Step 3: Implement registry, store, clock, harness, navigator, bare mode.** The store holds the current scenario's props; a scenario's `render` wires callbacks to `store.set` / `store.go`, which is what makes the prototype clickable. No scenario edits `store.ts`.
- [ ] **Step 4: Generate and commit the mock photos.**
- [ ] **Step 5: Build `WelcomePanel`, `LibraryScreen` and scenarios 10–36, 100–122.**
- [ ] **Step 6: Run** `bun test && bun run check && bun run lint:design && bun run proto:build`, then `task design:dev` and look at every state at all three sizes — expected: tests pass; nothing clipped, overlapping or misaligned.
- [ ] **Step 7: Commit** `feat(design): prototype shell with welcome and library states`.

### Task 4: Checks on the prototype

**Files:**
- Create: `frontend/tools/checks/{clip,occlusion,orphans,align,focus}.ts`, `frontend/tools/{check-states.pw.ts,flows.pw.ts,checks.pw.ts}`, `frontend/tools/flows/{welcome,library}.ts`, `frontend/tools/fixtures/broken-states.tsx`
- Modify: `frontend/package.json` (`design:check-states`, `design:flows`), `Taskfile.yml`

**Interfaces:**
- `design:check-states [--grep <group>]`: builds, serves on an ephemeral port, and for every state × size × browser (Chromium, WebKit) opens `?bare&freeze`, and again with `&nowrapfx`. A state fails on:
  - a console error or page error; a failed request; a request whose host is not `localhost`/`127.0.0.1`;
  - **root overflow**: `[data-app-root]` scrolls horizontally;
  - **clipping**: an element with `overflow` hidden or clip on an axis whose scroll size exceeds its client size by more than 1 px on that axis, unless it truncates by design (`text-overflow: ellipsis`, `-webkit-line-clamp`) or carries `data-scroll` or `data-clip="intended"`; `.sr-only` and zero-area elements are skipped;
  - **occlusion**: for every text-bearing or interactive element whose centre is inside the viewport and inside every clipping ancestor's box, `elementFromPoint` at that centre returns something that is neither the element, a descendant nor an ancestor — unless the covering element belongs to an open overlay;
  - **orphan** (left-to-right text): a text block of ≥ 2 lines whose last line is a single word and narrower than 30 % of the widest line; opt-out `data-wrap="any"`;
  - **alignment**: elements sharing a `data-align-y` value whose vertical centres differ by more than 0.5 px, or sharing `data-align-x` whose left edges differ by more than 0.5 px;
  - **focus** (Chromium only): every enabled control is tabbable, or sits in a `role` `menu`, `listbox`, `toolbar`, `tablist`, `radiogroup` or `grid` container that is tabbable and reaches it with arrow keys; a focused control's computed outline, box-shadow or border differs from its unfocused one; in a state with `overlays`, Escape closes the top one;
  - **forced-look fidelity** (gallery states): for each control, computed style under `data-force="hover"`, `"focus"`, `"active"` equals computed style under a real `hover()`, `focus()`, mouse-down.
  Screenshots: `frontend/.design/screenshots/<browser>/<size>/s<N>.png` and one contact sheet per group and size.
- `design:flows [--grep <group>]`: each `flows/<group>.ts` exports `default async function(page: Page, proto: ProtoHandle): Promise<void>`. After all flows of the run, every overlay id registered by the states of the selected groups must be in `opened` and `closed`.
- `fixtures/broken-states.tsx`: a test-only registry, never in the build, with one state per failure kind.

- [ ] **Step 1: Write the failing tests** in `checks.pw.ts`: against the broken fixture each check reports exactly its own state; against s102 none reports; an off-screen row inside a scroller is not an occlusion; a flow that opens and never closes a registered overlay fails naming it.
- [ ] **Step 2: Run** `bunx playwright test -c playwright.design.config.ts tools/checks.pw.ts` — expected: FAIL.
- [ ] **Step 3: Implement the checks and both runners.**
- [ ] **Step 4: Write the Welcome and Library flows**: first launch → add folder → scanning → filled → hover row → open the row menu → Escape → Remove → alert → Cancel; drag over → drop; toast → Undo.
- [ ] **Step 5: Run** `task design:check-states` and `task design:flows` — expected: `0 failures` for both. Look at every contact sheet.
- [ ] **Step 6: Commit** `test(design): state and flow checks`.

### Task 5: Remaining screen groups (parallel, one owner and one worktree each)

Four owners. Each may create or change only the paths in its row, and may **add new** component files under `design/components/` whose names start with its prefix (`Folder…`, `Cluster…`; `Focus…`, `Compare…`; `Settings…`; `Shortcut…`, `Native…`). Everything else is the coordinator's: `types.ts`, `store.ts`, `registry.ts`, `mock/data.ts`, `menus.json`, and every existing file under `design/components/`.

| Owner | Ports from v1 | Owned paths | States |
|---|---|---|---|
| 5a Folder | `Library.tsx`, `Empty.tsx` | `screens/folder/**`, `scenarios/folder.tsx`, `scenarios/folder.test.ts`, `flows/folder.ts`, `mock/folder.ts` | 200–229 |
| 5b Focus and Compare | `Focus.tsx` | `screens/focus/**`, `scenarios/{focus,compare}.tsx`, `scenarios/{focus,compare}.test.ts`, `flows/{focus,compare}.ts`, `mock/focus.ts` | 300–317, 400–409 |
| 5c Settings | `Settings.tsx` | `screens/settings/**`, `scenarios/settings.tsx`, `scenarios/settings.test.ts`, `flows/settings.ts`, `mock/settings.ts` | 500–520 |
| 5d Overlays and native | `Shortcuts.tsx` | `screens/overlays/**`, `screens/native/**`, `scenarios/{overlays,native}.tsx`, `scenarios/{overlays,native}.test.ts`, `flows/{overlays,native}.ts` | 600–605, 700–720 |

Round structure: (1) every owner writes its `props.ts` and a list of the shared changes it needs (types, a component change, mock data); (2) the coordinator reviews the props, lands all shared changes on the integration branch, and owners rebase; (3) owners build; (4) the coordinator merges the four worktrees and runs the full checks. A shared change discovered in (3) goes back through the coordinator.

Props fixed here (owners add what their states need, under the "everything visible is a prop" rule):

- `FolderProps`: `{ folder: FolderSummary; clusters: Cluster[] | null; scan: ScanProgress | null; filter: FolderFilter; devices: string[]; openMenu: 'device' | 'sort' | null; selectedClusterId: number | null; settings: Pick<SettingsBody,'hide_rejected_photos'|'quality_rating'|'visual_grouping'>; clipReady: boolean; reclustering: boolean; failedFiles: number; picksMissing: number; notice: 'offline' | 'read-only' | null; toasts: ToastItem[]; media: MediaURLs; onBack(); onOpenPicks(); onOpenCluster(id); onSelectCluster(id); onFilter(f: FolderFilter); onOpenMenu(m); onRecluster(); onPick(fileId, action); onSettings(); onHelp() }`
- `FocusProps`: `{ data: FocusResponse | null; loadError: string | null; activeIndex: number; zoom: number /* 1..8, continuous, as v1 */; pan: { x: number; y: number }; showInfo: boolean; comparing: boolean; compareIdx: number[] /* 0..4 entries */; busyFileIds: number[]; mediaState: Record<number, 'loading' | 'ready' | 'error'>; ratings: Record<number, number>; qualityOn: boolean; media: MediaURLs; toasts: ToastItem[]; on… }`. Photo navigation wraps, as in v1. The Info panel is a `Panel` (registered overlay). Video uses `preload="none"` with a poster and never autoplays.
- `SettingsProps`: `{ saved: SettingsBody | null; loadError: string | null; draft: SettingsDraft /* raw text for numeric fields */; errors: Partial<Record<keyof SettingsBody, string>>; activeSection: 'grouping' | 'behaviour' | 'output'; saving: boolean; quality: QualityStatus | null; retrying: boolean; models: ModelStatus[]; leavePrompt: boolean; toasts: ToastItem[]; onChange(patch); onSection(id); onSave(); onRevert(); onBack(); onRetryQuality(); onRetryModel(kind); onConfirmLeave(leave: boolean) }`. The leave prompt is a `Prompt` (registered overlay).

Porting rule: v1's markup, behaviour, keyboard handling, accessibility attributes and `data-testid` values are preserved. What changes: data arrives as props, effects leave as callbacks, media URLs come from `media`, design literals become tokens, state that defines a scenario becomes a prop, timers move out. v1's validation rules and messages are kept (`validatePicksDir` rejecting absolute paths included).

For each owner:

- [ ] **Step 1: Write `props.ts` and the shared-change list**; hand both to the coordinator.
- [ ] **Step 2: Write the failing tests**: `scenarios/<group>.test.ts` (exactly the inventory numbers) and unit tests for logic moved out of components (5a `filterClusters(clusters, filter)`; 5c `validateSettings(draft): errors` with v1's rules and messages).
- [ ] **Step 3: Run** `bun test` — expected: FAIL.
- [ ] **Step 4: Build the screens and scenarios** (after the coordinator has landed the shared changes).
- [ ] **Step 5: Write the group's flows** so every overlay its states register is opened and closed.
- [ ] **Step 6: Run** `bun test && bun run check && bun run lint:design && task design:check-states -- --grep <group> && task design:flows -- --grep <group>` — expected: 0 failures; contact sheets looked at, at all three sizes.
- [ ] **Step 7: Commit** `feat(design): <group> states`.

### Task 6: Fingerprints and the source digest

**Files:**
- Create: `frontend/tools/{extract.ts,extract.pw.ts,snapshot.pw.ts,snapshot.mjs}`, `tools/design/gate.py` (the `digest` command only), `tools/design/gate_test.py` (digest tests), `docs/design/state-hashes.json`
- Modify: `frontend/package.json` (`design:snapshot` = `node tools/snapshot.mjs`), `Taskfile.yml` (`design:snapshot`, `design:snapshot:linux`)

**Interfaces:**
- `python3 tools/design/gate.py digest` — sha256 over the sorted list of files matching `digestInputs` (`git ls-files -co --exclude-standard`), each as `path\0sha256(content)\n`. The repository root is `git rev-parse --show-toplevel` of the current directory, so it is correct inside a worktree.
- ```ts
  export interface TreeNode { tag: string; cls: string[]; text: string; attrs: Record<string,string>; props: Record<string,string>; svg?: Record<string,string>; style: number /* index into Tree.styles */; measuredBy?: string; children: TreeNode[] }
  export interface Tree { fonts: { family: string; weight: string; style: string; res: string }[]; styles: Record<string,string>[]; root: TreeNode }
  export function extractTree(root: Element, resources: Record<string, string>): Tree   // self-contained; runs in the page
  export function hashTree(t: Tree): string                                           // sha256 hex of canonical JSON (sorted keys)
  ```
  - `styles`: for each element, every longhand returned by `element.computedStyleMap()`, minus custom properties (`--*`) and minus a denylist kept in `extract.ts`; identical records are stored once. These are computed values, not used values.
  - **Denylist rule:** a property may be dropped only if it is a logical alias of a physical property that is kept, or its computed value differs by platform **and** a kept property still changes when the design changes. Each entry has a one-line reason and a test showing a change in the dropped property's design intent still changes the hash.
  - `attrs`: `role`, every `aria-*`, `type`, `disabled`, `readonly`, `required`, `placeholder`, `title`, `alt`, `href` (origin stripped; a navigation target, not a resource), `controls`, `open`, `hidden`, `tabindex`, `for`, `draggable`, `dir`, `lang`, `data-state`. `data-force` is left out; its effect is in the style.
  - `props` (live state): `value`, `checked`, `indeterminate`, `selectedIndex`, and the `::placeholder` colour.
  - `svg`: `d`, `points`, `x`, `y`, `x1`, `y1`, `x2`, `y2`, `cx`, `cy`, `r`, `rx`, `ry`, `width`, `height`, `viewBox`, `transform`.
  - **Resources**: every URL in `src`, `srcset`, `poster`, an SVG `<image href>` or a CSS `url()` becomes `res:<first 16 hex of the file's sha256>` (no origin, no path). `fonts` lists every **declared** `@font-face` rule (whether or not it has loaded) with the identity of its source file.
  - **Measured values**: an element declares `data-measured="width left"` and `data-measured-by="<id>"`; those style properties are dropped and `measuredBy` becomes `<id>:<sha256 of the files MEASURED[id] names>`. Measured text or DOM (such as the strip's `+N` count) is not exempt; fixtures make it deterministic.
  - Skipped: `script`, `style`, `template`, anything with `data-proto-chrome`, children of `display: none`.
  - The extractor throws on `outline-style: auto` and on a `::before` / `::after` whose `content` is not `none`.
- `node tools/snapshot.mjs [--check] [--states 100-122,300]`: digest → build (writes `dist-prototype/build.json`) → digest again (abort if it moved) → serve on an ephemeral port → run the `snapshot` Playwright project, which renders each selected state at each size in a fresh page (Chromium, `deviceScaleFactor: 1`, `timezoneId: 'UTC'`, `locale: 'en-US'`, `reducedMotion: 'reduce'`, `page.clock.setFixedTime(NOW)`, URL `?bare&freeze`) and writes one JSON fragment per state → the wrapper merges the fragments, sorted, into the file. The result does not depend on worker count or on which states were selected.
- `state-hashes.json`: `{ "version": 1, "sourceDigest": string, "browser": string, "extractor": string /* first 16 hex of sha256(extract.ts) */, "states": { "<n>": { "label": string, "variants": { "min/dark/macos": string, "default/dark/macos": string, "large/dark/macos": string }, "hash": string } } }`; `hash` = sha256 of the three variant hashes in that order, then `browser`, then `extractor`. Sorted keys, two-space indent, trailing newline. `--check` writes nothing and exits 1 listing each differing state; with `--states` it compares only those.
- `design:snapshot:linux`: clones the committed HEAD inside the pinned Playwright image and runs `--check` there, leaving the host's `node_modules` alone:
  `docker run --rm -v "$(git rev-parse --path-format=absolute --git-common-dir)":/src.git:ro mcr.microsoft.com/playwright:v<pinned>-noble bash -lc "apt-get update -qq && apt-get install -y -qq python3 git unzip && npm i -g bun && git clone -q /src.git /tmp/w && cd /tmp/w && git checkout -q <HEAD sha> && cd frontend && bun install --frozen-lockfile && bun run design:snapshot --check"`

- [ ] **Step 1: Measure before fixing the schema.** Extract s109 and s202 at `large` on macOS and in the Docker image; record the time per variant and every property whose value differs between the two. Target: ≤ 250 ms per variant. Seed the denylist from the differences, applying the denylist rule.
- [ ] **Step 2: Write the failing tests.** `extract.pw.ts` (inline fixture pages): trees differing in one text node, one class, one colour, `width: 50%` vs `51%`, `font-variant-numeric`, `box-shadow`, an SVG `d`, an input's live `value`, a `video` `poster`, the bytes behind an unchanged image URL, or the bytes behind an unchanged font URL each hash differently; trees differing only in a `data-measured` property, in the server port, or in the order states were rendered hash the same; a page with a real hover hashes the same as that page with `data-force="hover"`; an indeterminate `ProgressRail` extracted twice 300 ms apart under `freeze` hashes the same; `data-proto-chrome` is absent; `outline-style: auto` and pseudo-element content throw; one test per denylist entry; s109 extracts within the time budget. `gate_test.py`: the digest changes when a `digestInputs` file changes or an untracked one appears, not when an ignored file appears, and is the same from the repository root, a subdirectory and a linked worktree.
- [ ] **Step 3: Run** both suites — expected: FAIL.
- [ ] **Step 4: Implement `extract.ts`, `snapshot.pw.ts`, `snapshot.mjs`, `gate.py digest`.**
- [ ] **Step 5: Run** the tests, then `task design:snapshot` twice, then once with `--workers 1` — expected: pass; `git diff --stat docs/design/state-hashes.json` stays empty after the first run.
- [ ] **Step 6: Commit** `feat(design): state fingerprints and source digest`, then run `task design:snapshot:linux` — expected: exit 0 against the file written on macOS. A difference is fixed under the denylist rule or in the offending component, with a test, and this step is repeated.

### Task 7: Ledger, branch check, human commands

**Files:**
- Modify: `tools/design/gate.py` (add `check`, `status`, `init`, `approve`, `confirm-no-ui`; `restore-violations` is added in Task 8 with the hooks), `tools/design/gate_test.py`, `Taskfile.yml` (`design:check`, `design:status`, `design:init`, `design:approve`, `design:confirm-no-ui`)
- Create: `docs/design/changes/feat-choix-2-desktop-design-463af0.md`

**Interfaces:**
- Change file: front matter with exactly four keys, parsed with a regular expression (no YAML library): `branch: <exact branch name>`, `states: [100, 101, 200-225]`, `retired: []`, `ui: true|false`. Slug = branch name lower-cased, every run outside `[a-z0-9]` replaced by `-`. `branch` must equal the current branch, which settles slug collisions.
- `approvals.json` (written only by the human commands): `{ "states": [{ "state", "label", "hash", "by", "at", "note" }], "retired": [{ "state", "by", "at" }], "noUi": [{ "branch", "files": string /* sha256 of the sorted (path, blob sha) pairs of the changed gated paths */, "by", "at" }] }`, one entry per line, sorted. A new approval for a state replaces the old one.
- Identity: repository root from `git -C <dir> rev-parse --show-toplevel`; branch from `--branch`, else `GITHUB_HEAD_REF`, else `git symbolic-ref --short HEAD`, else (during a rebase) the name in `rebase-merge/head-name` or `rebase-apply/head-name`; a detached HEAD with none of these fails `no-branch`. Base = `--base` or `git merge-base HEAD origin/main`. Base versions of files come from `git show <base>:<path>` (absent means empty). Changed paths = `git diff --name-status -M <base>` (both sides of a rename; deletions count) + staged + unstaged + untracked + `--assume-changed`. Globs: `**` crosses directories, `*` does not; translated to regular expressions.
- State status: `approved` (ledger hash equals current), `stale` (ledger entry with another hash), `new` (no entry), `retired` (number in `retired.json` and absent from the snapshot). Ledger entries for numbers that are not registered are ignored.
- `gate.py check [--mode edit|merge] [--branch B] [--base R] [--assume-changed P]... [--json]`, exit 0 or 1:
  - Nothing gated and nothing in `digestInputs` changed (including assumed paths) → pass.
  - Both modes: `no-branch`; `no-change-file` / `bad-change-file` (missing, unparsable, wrong `branch`, or not added or modified on this branch); `stale-snapshot` (`digest` ≠ `sourceDigest`); `ui-false-but-prototype-changed` (`ui: false` while a `digestInputs` path changed or `state-hashes.json` differs from the base's).
  - Both modes, **when a gated path is changed or assumed**: `unapproved` — any registered state is `stale` or `new`. This holds under `ui: false` too.
  - Both modes: `unretired` — a number present at the base, or in the ledger, and absent from the snapshot is missing from `retired.json`, from the change file's `retired:`, or from the ledger's `retired`. (Deleting an unapproved state does not make the rest "all approved".)
  - Merge only: `unapproved` for any state listed in `states` even when no gated path changed; `unlisted-state` (a state whose hash differs from the base's, or is new, is not in `states`); `retired-reused` (a number in the base's `retired.json` is registered); `no-ui-unconfirmed` (`ui: false` with gated changes and no matching `noUi` entry); `parity-not-enabled` (a gated, non-test file under `frontend/` exists and a group it maps to in `parityGroups` is missing from `frontend/e2e/parity/enabled.json`; a file that matches no `parityGroups` glob — `App.tsx`, `main.tsx`, anything unexpected — maps to **every** group).
- `gate.py status`: prints the merge-mode findings, each state as a Markdown link `[#102 Library · Filled](http://localhost:8811/#s102)`; never exits non-zero.
- Human commands refuse unless stdin and stdout are terminals and neither `CLAUDECODE` nor `CLAUDE_CODE_ENTRYPOINT` is set:
  - `init`: writes `approvers.txt` with `git config user.email` and an empty ledger, after `y/N`.
  - `approve 12 13 20-25 [--note …]`: git email must be in `approvers.txt`; the digest must equal `sourceDigest`; `snapshot.mjs --check --states <those>` must pass; `<reviewUrl>/__build.json` must report the same digest (the owner is looking at this build, not at the dev server or an old one); prints each state as `#N Label — <reviewUrl>/#sN` with its hash; asks `Approve these N states? [y/N]`; writes the ledger. `approve --retire N…` records retirements the same way.
  - `confirm-no-ui`: git email check; prints the changed gated files; asks `y/N`; records their hash.

- [ ] **Step 1: Write the failing tests** in `gate_test.py` (temporary git repositories, one with a linked worktree; no network), one per reason above, and: a branch touching only Go engine files passes; a prototype-only branch with a current snapshot and a change file passes in edit mode and fails `unapproved` in merge mode; `--assume-changed frontend/src/App.tsx` with one state unapproved fails `unapproved`, and passes once all are approved; it fails again when one approved hash goes stale; a branch that removes a state passes once `retired.json`, `retired:` and the ledger agree, and the removed state's old ledger entry does not trip `unapproved`; removing the only unapproved state without retiring it fails `unretired` in edit mode; a detached HEAD fails `no-branch` and passes with `--branch`; a renamed gated file is seen under both names; the answer is the same from the worktree root and from `frontend/`; a `noUi` confirmation survives `main` moving and dies when a gated file changes; the human commands exit non-zero with "needs a terminal" on a pipe and with `CLAUDECODE=1`; `approve` refuses an unknown email and a mismatched `__build.json`.
- [ ] **Step 2: Run** `python3 -m unittest tools/design/gate_test.py -v` — expected: FAIL.
- [ ] **Step 3: Implement the commands.**
- [ ] **Step 4: Write this branch's change file** (`ui: true`, every state listed, `retired: []`).
- [ ] **Step 5: Run** the tests and `task design:snapshot && task design:status` — expected: tests pass; status lists every state as awaiting approval with its link and asks the owner to run `task design:init`.
- [ ] **Step 6: Commit** `feat(design): approval ledger and branch check`.

### Task 8: Enforcement — hooks, CI, PR template

**Files:**
- Create: `.claude/hooks/design_gate.py`, `.claude/hooks/design_gate_test.py`, `.claude/settings.json`, `.github/pull_request_template.md`
- Modify: `.github/workflows/ci.yml`, `tools/design/gate.py` (add `restore-violations`), `tools/design/gate_test.py`

**Interfaces:**
- First read the current Claude Code hooks reference (stdin fields, matcher names, exit codes, `permissions.deny` path syntax, `session_id`) and adjust the details below to it.
- `design_gate.py <pre-edit|pre-bash|post-bash|stop|subagent-stop>` reads the hook JSON on stdin; exit 0 allows; exit 2 blocks with the reason on stderr; any internal exception → exit 0 with a warning. It imports `tools/design/gate.py` and resolves the repository from the target file's directory (or the hook's `cwd`), so worktrees work.
- Session state: `<git rev-parse --git-path choix-design>/session-<session_id>.json` — outside the working tree, so `git clean` or deleting `frontend/.design` does not touch it. Sub-agents and parallel tool calls share a session, so every read-modify-write holds an exclusive `fcntl.flock`, before-state is keyed by `tool_use_id`, and the earliest `before` recorded for a path wins. It also records which digest-input and gated paths this session changed. Before each shell command it records, for every gated file that differs from `HEAD` or is untracked, a blob written with `git hash-object -w` (so the content itself is recoverable), plus the tip of every ref. It also holds the open **violations**: `{ path, before }`, where `before` is a blob id or `absent`.
  - `pre-edit` (Edit, Write, MultiEdit, NotebookEdit): block a ledger file or `approvers.txt`; block a gated target when `check --mode edit --assume-changed <target>` fails, printing the reasons and the review links.
  - `pre-bash` (Bash and `mcp__terminal__*`): block a command that runs a human command (`design:init`, `design:approve`, `design:confirm-no-ui`, `gate.py init|approve|confirm-no-ui`); block a command that names a ledger file with a write form; extract write destinations (`>`, `>>`, `tee`, `cp`/`mv` target, `sed -i`, `touch`, `rm`) and block when one is gated and its edit check fails; for `git commit`, when a gated path is staged (or would be, with `-a`): block if the edit check fails, and block if the index and the working tree disagree on any digest input, `state-hashes.json`, `approvals.json` or the change file (so the approval relied on is the one being committed). Two commands are always allowed: `gate.py restore-violations`, and `rm` of a path whose open violation has `before: absent`. Then record the before-state.
  - `post-bash`: for every gated file whose content changed during the command while the edit check fails, open a violation — unless the new content equals that path's blob in a commit that existed before the command or in `origin/main` (a checkout, merge or rebase bringing in committed code is not the agent writing UI). The same test applies to commits created by the command: a gated blob in a new commit is a violation only when that blob exists in no commit that predates the command and not in `origin/main` (so a merge, rebase, cherry-pick or amend of existing code passes, and a generator followed by a commit in one command does not). Only files git does not ignore are considered. Exit 2 naming the files and the fix: `python3 tools/design/gate.py restore-violations`, which puts every violated path back to its `before` blob in both the working tree and the index (deleting it when `before` is `absent`), and, when the content was committed, says which commit to amend or reset. A violation closes only when the path's content in the working tree, the index and `HEAD` equals `before`, or the edit check passes.
  - `stop`: block on `stale-snapshot` **when this session changed a digest input** (run `task design:snapshot`; a session that only found the tree stale is told so and not blocked), on a missing or bad change file when this session changed a gated or digest-input path, and on any open violation. States awaiting approval are printed as Markdown links and do not block. Safety valve: after three consecutive blocks with identical reasons the hook allows the stop and prints a visible warning naming what is still wrong, so a broken build or an unresolvable conflict cannot trap the session.
  - `subagent-stop`: block only on open violations.
- `.claude/settings.json`: PreToolUse (edit tools; Bash; `mcp__terminal__.*`), PostToolUse (Bash; `mcp__terminal__.*`), Stop, SubagentStop, all via `"$CLAUDE_PROJECT_DIR"/.claude/hooks/design_gate.py`; `permissions.deny` for Edit and Write on the two ledger files.
- CI job `design` on `ubuntu-latest`: `actions/checkout` with `fetch-depth: 0`; `oven-sh/setup-bun`; `actions/setup-node`; then, with `working-directory: frontend`: `bun install --frozen-lockfile`, `bunx playwright install --with-deps chromium webkit`, `bun test`, `bun run lint:design`, `bun run design:check-states`, `bun run design:flows`, `bunx playwright test -c playwright.design.config.ts tools/extract.pw.ts`, `bun run design:snapshot --check`; and at the repository root: `python3 -m unittest tools/design/gate_test.py`, `python3 -m unittest discover -s .claude/hooks -p '*_test.py'`, `python3 tools/design/gate.py check --mode merge --branch "${{ github.head_ref || github.ref_name }}" --base origin/main`. Every step after install has `if: always()` so one failure does not hide the others. The existing macOS job gains `setup-bun`, `bun install`, `bunx playwright install chromium` and `bun run design:snapshot --check`. With `ui: false` the job adds a notice annotation.
- PR template: `Design approved: [#N Label](http://localhost:8811/#sN)` or `No visible UI change`.

- [ ] **Step 1: Write the failing tests** in `design_gate_test.py` (subprocess, JSON on stdin, temporary repository, also run from a linked worktree): Edit of `frontend/src/app/library/X.tsx` and of `frontend/src/App.tsx` with a state unapproved exits 2 naming states, and 0 once all are approved; Edit of `frontend/src/design/components/Button.tsx` exits 0; Edit of `docs/design/approvals.json` exits 2; Bash `cat > frontend/src/app/library/X.tsx` on a clean branch exits 2; Bash `echo x > docs/design/approvals.json` exits 2; Bash `task design:approve -- 100` exits 2, also in the `mcp__terminal__run_in_terminal` shape; Bash `git add docs/design/approvals.json` exits 0; `post-bash` after a gated file was overwritten through `git apply` exits 2 and names it, and `gate.py restore-violations` brings back its exact previous uncommitted content and closes the violation; an untracked gated file created by a generator is a violation with `before: absent`, `restore-violations` deletes it, and a plain `rm` of it is allowed; a `git reset --soft` alone does not close a commit violation; `git commit` of a gated file is refused while the staged `state-hashes.json` differs from the working tree's; `post-bash` after `git merge origin/main` brought in gated files while a state is stale exits 0; `post-bash` after "generate && git add -A && git commit" exits 2; `git commit` with a staged gated file and a failing check exits 2; `stop` exits 2 when this session edited a design file and did not snapshot, 0 when the tree was already stale and this session changed nothing, 2 with an open violation, 0 with only unapproved states, and 0 with a warning on the fourth identical block; two interleaved pre/post pairs with different `tool_use_id`s keep the earliest `before`; a Playwright run that writes under `frontend/.design/` opens no violation; `git merge origin/main`, a rebase and an amend of existing gated code open no violation; during a rebase the branch is read from `rebase-merge/head-name`; a fresh session in a tree that already contains unapproved gated code has no violation, and a `git commit` of that code exits 2; `subagent-stop` with a stale snapshot exits 0; malformed stdin exits 0.
- [ ] **Step 2: Run** `python3 -m unittest discover -s .claude/hooks -p '*_test.py' -v` — expected: FAIL.
- [ ] **Step 3: Implement the hook script and settings.**
- [ ] **Step 4: Add the CI job and the PR template.**
- [ ] **Step 5: Run** the hook tests — expected: PASS. After the push, read the `design` job's log (`gh run view --log`) and confirm the only failing step is the merge check, with reason `unapproved`. In the next session (hooks load at session start), attempt an edit under `frontend/src/app/` and confirm it is refused with the list of states; paste both outputs into the README.
- [ ] **Step 6: Commit** `feat(design): enforce the approval gate in hooks and CI`.

### Task 9: Parity harness

**Files:**
- Create: `frontend/e2e/parity/{parity.ts,cases.ts,enabled.json,parity.pw.ts,selftest.pw.ts}`
- Modify: `.github/workflows/ci.yml` (add the parity step: `bunx playwright test -c playwright.design.config.ts --project parity --pass-with-no-tests`)

**Interfaces:**
- `interface ParityCase { state: number; drive?(page: Page): Promise<void>; interact?(page: Page): Promise<void>; skip?: string }`. `drive` puts the real app (built with its mock-bindings alias and the prototype's fixtures, supplied by the engineering plan) into the state. `interact` reproduces a forced look with a real hover, focus or press, since the app never sets `data-force`. `skip` carries a reason (every 700-range state: "native surface").
- `compareTrees(app: Tree, proto: Tree): { path: string; field: string; app: string; prototype: string } | null` — the first difference, with a DOM path.
- `enabled.json`: the group ids whose states must each have a case; starts as `[]`. `parity.pw.ts` runs one test per approved state in an enabled group and fails with "no parity case for #N" when one is missing. The merge check's `parity-not-enabled` reason (Task 7) forces a group in as soon as its app code exists.

- [ ] **Step 1: Write the failing self-test** `selftest.pw.ts`: two fixture pages differing in one padding return that node's path and `field: 'style.padding-left'`; identical pages return `null`; a page with a real hover equals the same page with `data-force="hover"`.
- [ ] **Step 2: Run** `bunx playwright test -c playwright.design.config.ts e2e/parity/selftest.pw.ts` — expected: FAIL.
- [ ] **Step 3: Implement `compareTrees`, the spec and the skip entries; add the CI step.**
- [ ] **Step 4: Run** the self-test and the parity project — expected: PASS (no groups enabled yet).
- [ ] **Step 5: Commit** `test(design): parity harness`.

### Task 10: Documentation

**Files:**
- Create: `docs/design/README.md`, `docs/design/inventory.md`, `frontend/tools/inventory.ts`
- Modify: `CLAUDE.md` (a "Design gate" section), `frontend/package.json` (`design:inventory`), `Taskfile.yml` (descriptions)
- Test: `frontend/tools/inventory.test.ts`

**Interfaces:**
- `inventory.md`: generated by `design:inventory` — one row per state (number, label, group, design reference), a "Retired" section, and a "Not states" section with reasons.
- `README.md`: the workflow; every file's format and who may write it; every check with the test that pins it; the owner's one-time setup (`task design:init`, branch protection settings); what a Playwright upgrade or an extractor change means for approvals; what the hooks cannot see (work done outside a hooked session) and what catches it instead.

- [ ] **Step 1: Write the failing test** `inventory.test.ts`: the committed `inventory.md` equals the generator's output; every number in `retired.json` appears under "Retired".
- [ ] **Step 2: Run** `bun test tools/inventory.test.ts` — expected: FAIL.
- [ ] **Step 3: Write the generator, both documents and the `CLAUDE.md` section.**
- [ ] **Step 4: Run everything**: `bun test`, `bun run lint:design`, both Python suites, `task design:check-states`, `task design:flows`, `task design:snapshot -- --check`, `task design:snapshot:linux` — expected: all green; paste the real output of each into the hand-off message.
- [ ] **Step 5: Commit** `docs: design gate workflow and state inventory`.

---

## After this plan: the review round

1. `task design:snapshot`, then `task design:serve` in the background (the static build on port 8811, never the dev server).
2. Ask the owner to review with one Markdown link per state, grouped by screen, e.g. `[#102 Library · Filled](http://localhost:8811/#s102)`. Then stop.
3. The owner runs `task design:init` once, then `task design:approve -- 100-122` and so on in a terminal, or comments and the loop repeats.
4. Once every state is approved, the engineering plan wires the screens into the app and enables each group's parity cases.

An informal look is available earlier: after Task 3 the Library and Welcome states can be opened at `http://localhost:8811` for comments, before the approve command exists.

## What runs while the owner reviews

The Go side is not gated: sandbox bookmarks, the native media layer, services, the library database, the Wails shell without screens, the build and packaging tasks. That is the separate engineering plan, written and validated next. It waits for design approval only where it touches `frontend/` outside the design folder, or the three native-UI Go packages. It also owns two tests: that the native menu built by `internal/app/menu/` equals `menus.json`, and that native UI constructors (menus, dialogs, window options) are called only from the three gated packages.
