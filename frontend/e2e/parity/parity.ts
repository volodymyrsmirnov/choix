// Parity: the real app, driven into a state with mocked data, must render the
// same Tree as the prototype's #sN. Both pages go through the snapshot's own
// extractor (tools/extract.ts, extractPage) and the same normalisation, at
// every review size; compareTrees reports the first difference.
//
// What is compared: the subtree of the one `[data-app-root]` element (the
// shared AppRoot both sides mount; the prototype's Frame and navigator and
// the app's body/#root sit outside it), plus the document-level fields:
//   - every @font-face, in full and in order (fonts[i]);
//   - the <html> element: its color-scheme <meta> and its own style record
//     (html.colorScheme, html.style.<property>);
//   - the document-wide rule lists (pseudoRules, motionRules, atRules,
//     layers), each as an ordered subsequence: every rule of the APP's list
//     must be in the PROTOTYPE's list, in the same relative order (the cascade
//     decides which rule wins, so order counts, and a rule the app has twice
//     and the prototype once is a difference); the prototype may carry
//     extra rules anywhere.
// Why extras are allowed on the prototype side only: each build has its own
// stylesheet, made from its own Tailwind content. The app's is the content
// of tailwind.config.ts (through postcss.config.cjs): `./index.html` and
// `./src/**/*.{ts,tsx}` with test files (`**/*.test.ts`, `**/*.test.tsx`)
// left out, which is the app and the design folder; it does not scan
// prototype/. The prototype's is PROTOTYPE_CONTENT (vite.prototype.config.ts):
// the design folder and the prototype's own files (mock screens, the
// navigator, the harness), test files left out. So the prototype's
// stylesheet legitimately holds rules no app file uses. The reverse is not
// allowed: a rule only the app has is styling the prototype never showed,
// which only app code can bring in. What this
// cannot catch: a prototype-only rule that nonetheless reaches a design
// element's motion or pseudo-element from outside the app root (say a
// prototype file's `.x::-webkit-scrollbar` or `.x { transition }` whose
// selector also matches a design component); the prototype shows it, the
// app lacks it, and the subsequence test passes. Element styles are not
// affected (they are compared node by node as computed values); only what
// no computed style shows is.
// TREE_FIELDS and NODE_FIELDS name how each field is compared; they are
// typed over every key of Tree and TreeNode, so a new field must be handled.
//
// Prototype-only machinery, and how it is kept from counting:
//   - `data-force` (the prototype's forced hover / focus / press looks): the
//     attribute itself never reaches a Tree, because the extractor records
//     only its ATTRS allowlist. The look it produces does: the app shows it
//     through the case's `interact` (a real hover, focus or press), and the
//     computed styles must match.
//   - The data-force CSS twins (tools/postcss-force.cjs appends
//     `[data-force~="…"]` selectors to every interaction rule): a twin is
//     dropped from each pseudo-element and motion rule's selector list
//     (motion rules carry :hover / :active selectors too) on both sides
//     (located through the page's own selectorText values, see
//     stripForceTwins), so the comparison holds whether or not the app build
//     carries them.
//     Only selectors inside a rule are removed; a rule keeps its place in
//     the list, and is dropped only when every one of its selectors is a twin.
//   - The AppRoot's size: the prototype's Frame pins it to the review size
//     (`style="width: 1280px; height: 800px"`), the app's fills the window.
//     For the root node only, `width` and `height` are compared as the
//     laid-out size instead of the computed value, so the app root must
//     still fill exactly the window the prototype draws. The root's own
//     record also carries its laid-out position (`root:x`, `root:y`) and
//     whether the document scrolls (`root:document-scrolls`), so a root that
//     is shifted, clipped or overflowing the window still differs.
//   - The scripted-render mode: the prototype is opened with ?freeze, which
//     sets html[data-freeze] (base.css: no animation, no transition on any
//     element, so those computed values read `none`). The app page gets the
//     same attribute (scriptedRender) before it is driven; nothing in the
//     tree is dropped.
//   - data-proto-chrome, data-state-subject, data-measured*: handled by the
//     extractor as in every snapshot (skipped / not recorded / measuredBy).
// Nothing else is ignored.
//
// The app: PARITY_APP_URL serves the parity build (`bun run build:parity`,
// mock bindings, the prototype's fixtures). The page installs
// window.__choixParity (the engineering plan's mock control); a case's
// `drive` loads the state's fixture through it.
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Browser, ElementHandle, Page } from '@playwright/test'
import { MEASURED } from '../../src/design/measured'
import { PROTO_MEASURED } from '../../prototype/measured'
import { GROUPS, type GroupId } from '../../prototype/registry'
import { FRONTEND, type Size } from '../../tools/designPaths'
import { canonicalJson, collectResources, extractPage, measuredDigests, resolveResources, type Tree, type TreeNode } from '../../tools/extract'
import { newDesignContext, openState, waitForHarness } from '../../tools/stateRunner'

/** One approved prototype state's counterpart in the app. */
export interface ParityCase {
  state: number
  /** Puts the real app into the state (the app page is loaded and its mock control installed). */
  drive?(page: Page): Promise<void>
  /** Reproduces a forced look (data-force in the prototype) with a real hover, focus or press. */
  interact?(page: Page): Promise<void>
  /** Why the state has no app counterpart to compare; the test is skipped with it. */
  skip?: string
  /**
   * Image URLs (as the app sets them, resolved against the page) the case
   * holds deliberately unanswered, because the state is "still loading"
   * (313: the preview shown while the full-size image loads). Settling does
   * not wait for these images to decode, since decode() of an image whose
   * request never completes never settles; every other image is waited for
   * as before. Nothing about what is compared changes: the extractor still
   * records these images (their URL's bytes, fetched by Node) like any other.
   */
  pendingImages?: string[]
}

export interface TreeDiff {
  /** The node, as `tag.class > tag.class:nth-child(k) > …` from the root. */
  path: string
  /** `tag`, `class`, `text`, `attr.<name>`, `prop.<name>`, `svg.<name>`, `measuredBy`, `style.<property>`, `children.length`, or a document field (`fonts[i]`, `fonts.length`, `html.colorScheme`, `html.style.<property>`, `pseudoRules[i]`, `motionRules[i]`, `atRules[i]`, `layers[i]`). */
  field: string
  app: string
  prototype: string
}

const ABSENT = '(absent)'
/** An element child's place in a node's text (extract.ts). */
const CHILD_MARK = /\u001f/g

// ---------------------------------------------------------------------------
// The comparison (pure).

/** A node as one path step: its tag and up to three of its (sorted) classes. */
function step(node: TreeNode): string {
  const cls = node.cls.slice(0, 3).map((c) => `.${c}`).join('')
  return `${node.tag}${cls}${node.cls.length > 3 ? '…' : ''}`
}

const shown = (text: string) => text.replace(CHILD_MARK, '␟')

/** The first key (sorted) whose value differs between two records. */
function recordDiff(a: Record<string, string> | undefined, p: Record<string, string> | undefined): [string, string, string] | null {
  const app = a ?? {}
  const proto = p ?? {}
  const keys = [...new Set([...Object.keys(app), ...Object.keys(proto)])].sort()
  for (const k of keys) {
    if (app[k] !== proto[k]) return [k, app[k] ?? ABSENT, proto[k] ?? ABSENT]
  }
  return null
}

/**
 * Two ordered lists, entry by entry: `<name>[i]` at the first index where
 * they differ (an entry past the shorter list's end is `(absent)`), and
 * `<name>.length` when one list is the other plus a tail. Order and
 * duplicates count: in cascade order they decide which rule wins.
 */
function orderedDiff(name: string, app: string[], proto: string[]): TreeDiff | null {
  const shared = Math.min(app.length, proto.length)
  for (let i = 0; i < shared; i++) {
    if (app[i] !== proto[i]) return { path: 'document', field: `${name}[${i}]`, app: app[i], prototype: proto[i] }
  }
  if (app.length === proto.length) return null
  // The longer side names its first entry the other lacks.
  const side = (list: string[]) => (list.length > shared ? `${list.length} (then ${list[shared]})` : String(list.length))
  return { path: 'document', field: `${name}.length`, app: side(app), prototype: side(proto) }
}

/** How nodeDiff treats each field of a TreeNode (see TREE_FIELDS). */
export const NODE_FIELDS: Record<keyof TreeNode, string> = {
  tag: 'tag',
  cls: 'class (sorted, joined)',
  attrs: 'attr.<name>, every key',
  props: 'prop.<name>, every key',
  svg: 'svg.<name>, every key',
  measuredBy: 'measuredBy',
  text: 'text',
  style: 'style.<property>, the record by content',
  children: 'children.length, then each child in order',
}

function nodeDiff(app: Tree, proto: Tree, a: TreeNode, p: TreeNode, path: string): TreeDiff | null {
  const at = (field: string, av: string, pv: string): TreeDiff => ({ path, field, app: av, prototype: pv })
  if (a.tag !== p.tag) return at('tag', a.tag, p.tag)
  const ac = a.cls.join(' ')
  const pc = p.cls.join(' ')
  if (ac !== pc) return at('class', ac, pc)
  for (const [field, av, pv] of [
    ['attr', a.attrs, p.attrs],
    ['prop', a.props, p.props],
    ['svg', a.svg, p.svg],
  ] as const) {
    const d = recordDiff(av, pv)
    if (d) return at(`${field}.${d[0]}`, d[1], d[2])
  }
  if ((a.measuredBy ?? '') !== (p.measuredBy ?? '')) return at('measuredBy', a.measuredBy ?? ABSENT, p.measuredBy ?? ABSENT)
  if (a.text !== p.text) return at('text', shown(a.text), shown(p.text))
  const sd = recordDiff(app.styles[a.style], proto.styles[p.style])
  if (sd) return at(`style.${sd[0]}`, sd[1], sd[2])
  if (a.children.length !== p.children.length) {
    const list = (n: TreeNode) => `${n.children.length} (${n.children.map(step).join(', ')})`
    return at('children.length', list(a), list(p))
  }
  const steps = p.children.map(step)
  for (let i = 0; i < p.children.length; i++) {
    const child = p.children[i]
    // :nth-child(k) (k counts the node's children in the tree) only where a sibling shares the step.
    const ambiguous = steps.filter((s) => s === steps[i]).length > 1
    const childPath = `${path} > ${steps[i]}${ambiguous ? `:nth-child(${i + 1})` : ''}`
    const d = nodeDiff(app, proto, a.children[i], child, childPath)
    if (d) return d
  }
  return null
}

/**
 * A document-wide rule list of the app against the prototype's: the app's
 * list must be an ordered subsequence of the prototype's (every app rule is
 * in the prototype, in the same relative order; the prototype may hold
 * extras anywhere). The first app rule that cannot be matched after the
 * previous match is reported as `<name>[i]`, with the prototype's next
 * unmatched rule (the nearest one), or `(absent)` past its end. A rule the
 * app has twice and the prototype once, an app-only rule, and two rules in
 * the other order all fail this way. See the header for why.
 */
export function subsequenceDiff(name: string, app: string[], proto: string[]): TreeDiff | null {
  let j = 0
  for (let i = 0; i < app.length; i++) {
    let k = j
    while (k < proto.length && proto[k] !== app[i]) k++
    if (k === proto.length) return { path: 'document', field: `${name}[${i}]`, app: app[i], prototype: proto[j] ?? ABSENT }
    j = k + 1
  }
  return null
}

/** The <html> element: its color-scheme <meta>, then its style record by content. */
function htmlDiff(app: Tree, proto: Tree): TreeDiff | null {
  if (app.html.colorScheme !== proto.html.colorScheme) return { path: 'html', field: 'html.colorScheme', app: app.html.colorScheme, prototype: proto.html.colorScheme }
  const d = recordDiff(app.styles[app.html.style], proto.styles[proto.html.style])
  return d ? { path: 'html', field: `html.style.${d[0]}`, app: d[1], prototype: d[2] } : null
}

/**
 * How compareTrees treats each field of a Tree. A `Record` over every key, so
 * a field added to Tree without a line here fails the type check
 * (parity.test.ts also changes each field and expects a difference).
 */
export const TREE_FIELDS: Record<keyof Tree, string> = {
  fonts: 'in full, in order (fonts[i], fonts.length)',
  html: 'colorScheme, then the style record by content (html.colorScheme, html.style.<property>)',
  pseudoRules: 'the app’s list an ordered subsequence of the prototype’s, twins dropped (pseudoRules[i])',
  motionRules: 'the app’s list an ordered subsequence of the prototype’s, twins dropped (motionRules[i])',
  atRules: 'the app’s list an ordered subsequence of the prototype’s (atRules[i])',
  layers: 'the app’s list an ordered subsequence of the prototype’s (layers[i])',
  styles: 'through each node’s and the html’s style index, by content',
  root: 'node by node (nodeDiff)',
}

/**
 * The first difference between the app's tree and the prototype's: the
 * document fields first (fonts in full and in order; the <html> element;
 * then pseudoRules, motionRules, atRules and layers, each as an ordered
 * subsequence), then the nodes depth-first, each node's own fields before
 * its children. Style records are compared by content (their indexes differ
 * between trees). null when equal.
 */
export function compareTrees(app: Tree, proto: Tree): TreeDiff | null {
  return (
    orderedDiff('fonts', app.fonts.map(canonicalJson), proto.fonts.map(canonicalJson)) ??
    htmlDiff(app, proto) ??
    subsequenceDiff('pseudoRules', app.pseudoRules, proto.pseudoRules) ??
    subsequenceDiff('motionRules', app.motionRules, proto.motionRules) ??
    subsequenceDiff('atRules', app.atRules, proto.atRules) ??
    subsequenceDiff('layers', app.layers ?? [], proto.layers ?? []) ??
    nodeDiff(app, proto, app.root, proto.root, step(proto.root))
  )
}

export function formatDiff(d: TreeDiff): string {
  return `${d.path}\n  ${d.field}: app ${JSON.stringify(d.app)}, prototype ${JSON.stringify(d.prototype)}`
}

// ---------------------------------------------------------------------------
// Normalisation of prototype-only machinery (pure; see the header).

const FORCE_TWIN = /\[data-force[~|^$*]?=/

/** Splits at top-level commas (outside (), [] and strings). */
function splitSelectors(text: string): string[] {
  const out: string[] = []
  let depth = 0
  let quote = ''
  let start = 0
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (quote) {
      if (c === '\\') i++
      else if (c === quote) quote = ''
    } else if (c === '"' || c === "'") quote = c
    else if (c === '(' || c === '[') depth++
    else if (c === ')' || c === ']') depth--
    else if (c === ',' && depth === 0) {
      out.push(text.slice(start, i))
      start = i + 1
    }
  }
  out.push(text.slice(start))
  return out
}

/**
 * A pseudo-element or motion rule without its data-force twin selectors; null when
 * every selector was a twin. It only removes selectors inside one rule: the
 * caller keeps every rule at its place in the list, and drops a rule only
 * when this returns null.
 *
 * extract.ts writes a rule as `[...context, rule.selectorText].join(' ')`,
 * then ` { <declarations> }`, where the context entries (`@media <cond>`,
 * `@supports <cond>`, `@layer <name>`, …) may themselves hold spaces and
 * commas (`@media (min-width: 1px), print`). So the string alone cannot say
 * where the selector list starts. `twinSelectors` are the exact
 * selectorText values of the page's style rules that hold a twin
 * (forceTwinSelectors, read from the same CSSOM the extractor read): the
 * head must end with one of them, at a space, and only that selector list is
 * split. The longest such value wins (a context entry is never a selector
 * list unless the CSS nests style rules, which no build here does). A rule
 * whose head ends with none of them, and an entry without a declaration
 * block (an `@layer` statement), is returned as it is.
 */
export function stripForceTwins(rule: string, twinSelectors: string[]): string | null {
  const brace = rule.indexOf(' { ')
  if (brace === -1) return rule
  const head = rule.slice(0, brace)
  let selectors: string | null = null
  for (const s of twinSelectors) {
    if ((head === s || head.endsWith(` ${s}`)) && (selectors === null || s.length > selectors.length)) selectors = s
  }
  if (selectors === null) return rule
  const context = head.slice(0, head.length - selectors.length)
  const kept = splitSelectors(selectors).filter((s) => !FORCE_TWIN.test(s))
  return kept.length === 0 ? null : context + kept.join(',') + rule.slice(brace)
}

/**
 * Page side: the selectorText of every style rule, at any depth, whose
 * selector list holds a data-force twin. Self-contained.
 */
export function forceTwinSelectors(): string[] {
  const out = new Set<string>()
  const walk = (rules: CSSRuleList) => {
    for (const rule of Array.from(rules)) {
      if (rule instanceof CSSStyleRule && /\[data-force[~|^$*]?=/.test(rule.selectorText)) out.add(rule.selectorText)
      if ('cssRules' in rule && (rule as CSSGroupingRule).cssRules) walk((rule as CSSGroupingRule).cssRules)
    }
  }
  for (const sheet of Array.from(document.styleSheets)) walk(sheet.cssRules)
  return [...out]
}

/** The root's layout, measured in the page (parityTree). */
export interface RootLayout {
  /** The root's border box (getBoundingClientRect), px. */
  x: number
  y: number
  width: number
  height: number
  /** Whether the document scrolls: 'none', 'x', 'y' or 'both'. */
  documentScrolls: string
}

/**
 * The tree parity compares: twins dropped from pseudoRules and motionRules (order and
 * duplicates kept), and the root's `width` / `height` replaced by its
 * laid-out size, with `root:x`, `root:y` and `root:document-scrolls` added
 * to the root's record. `twinSelectors`: the page's forceTwinSelectors().
 * The input is not modified; the root gets a style record of its own.
 */
export function normaliseTree(tree: Tree, root: RootLayout, twinSelectors: string[] = []): Tree {
  const px = (v: number) => `${Math.round(v * 100) / 100}px`
  const strip = (list: string[]) => list.map((r) => stripForceTwins(r, twinSelectors)).filter((r): r is string => r !== null)
  const pseudoRules = strip(tree.pseudoRules)
  const motionRules = strip(tree.motionRules)
  const own = {
    ...tree.styles[tree.root.style],
    width: px(root.width),
    height: px(root.height),
    'root:x': px(root.x),
    'root:y': px(root.y),
    'root:document-scrolls': root.documentScrolls,
  }
  const styles = [...tree.styles, own]
  return { ...tree, pseudoRules, motionRules, styles, root: { ...tree.root, style: styles.length - 1 } }
}

// ---------------------------------------------------------------------------
// Which states run, and their cases (pure).

export const GROUP_IDS: GroupId[] = GROUPS.map((g) => g.id)

export function groupOf(n: number): GroupId | null {
  return GROUPS.find((g) => n >= g.range[0] && n <= g.range[1])?.id ?? null
}

/** enabled.json: the group ids whose approved states must each have a case. */
export function parseEnabled(text: string): GroupId[] {
  const data: unknown = JSON.parse(text)
  if (!Array.isArray(data) || data.some((g) => typeof g !== 'string')) throw new Error('enabled.json: expected an array of group ids')
  for (const g of data as string[]) {
    if (!GROUP_IDS.includes(g as GroupId)) throw new Error(`enabled.json: unknown group "${g}" (known: ${GROUP_IDS.join(', ')})`)
  }
  return data as GroupId[]
}

/**
 * docs/design/approvers.txt as `gate.read_approvers` reads it: the lower-cased
 * emails, one per line, `#` comments and blank lines skipped; null when the
 * file does not exist.
 */
export function parseApprovers(text: string | null): Set<string> | null {
  if (text === null) return null
  return new Set(
    text
      .split(/\r?\n/)
      .map((l) => l.trim())
      .filter((l) => l !== '' && !l.startsWith('#'))
      .map((l) => l.toLowerCase()),
  )
}

/**
 * The approved states, as `gate.py status` counts them: a ledger entry
 * (approvals.json) whose hash equals the state's current hash
 * (state-hashes.json) and whose `by` is listed in approvers.txt (the trust
 * rule of `gate.trusted_ledger`; with no approvers.txt no entry counts).
 */
export function approvedStates(snapshot: unknown, ledger: unknown, approvers: Set<string> | null): { n: number; label: string }[] {
  const states = ((snapshot as { states?: Record<string, { hash: string; label: string }> } | null)?.states ?? {}) as Record<string, { hash: string; label: string }>
  const entries = ((ledger as { states?: { state: number; hash?: string; by?: unknown }[] } | null)?.states ?? []) as { state: number; hash?: string; by?: unknown }[]
  const trusted = entries.filter((e) => approvers !== null && typeof e.by === 'string' && approvers.has(e.by.trim().toLowerCase()))
  const approved = new Map(trusted.map((e) => [Number(e.state), e.hash]))
  return Object.entries(states)
    .map(([k, v]) => ({ n: Number(k), label: v.label, hash: v.hash }))
    .filter((s) => approved.has(s.n) && approved.get(s.n) === s.hash)
    .map(({ n, label }) => ({ n, label }))
    .sort((a, b) => a.n - b.n)
}

/** Throws on a state with two cases, or a skipped case that also drives or interacts. */
export function validateCases(cases: ParityCase[]): void {
  const seen = new Set<number>()
  for (const c of cases) {
    if (seen.has(c.state)) throw new Error(`cases.ts: #${c.state} has two cases`)
    seen.add(c.state)
    if (c.skip !== undefined && (c.drive || c.interact || c.pendingImages)) throw new Error(`cases.ts: #${c.state} is skipped but also drives or interacts`)
    if (c.pendingImages !== undefined && (c.pendingImages.length === 0 || c.pendingImages.some((u) => u.trim() === ''))) {
      throw new Error(`cases.ts: #${c.state} declares pendingImages without a URL`)
    }
    if (c.skip !== undefined && c.skip.trim() === '') throw new Error(`cases.ts: #${c.state} is skipped without a reason`)
  }
}

export interface PlannedState {
  n: number
  label: string
  group: GroupId
  /** null: the state needs a case and has none. */
  case: ParityCase | null
}

/** One entry per approved state in an enabled group, in number order. */
export function planParity(approved: { n: number; label: string }[], enabled: GroupId[], cases: ParityCase[]): PlannedState[] {
  const on = new Set(enabled)
  const byState = new Map(cases.map((c) => [c.state, c]))
  const out: PlannedState[] = []
  for (const s of [...approved].sort((a, b) => a.n - b.n)) {
    const group = groupOf(s.n)
    if (group === null || !on.has(group)) continue
    out.push({ n: s.n, label: s.label, group, case: byState.get(s.n) ?? null })
  }
  return out
}

/**
 * The enabled groups that plan nothing to run: none of their planned states
 * (planParity) is compared or fails for a missing case, so parity.pw.ts
 * would stay quiet under --pass-with-no-tests. Not counted: a group whose
 * registered states are all skip entries (native). parity.pw.ts defines one
 * failing test for each group returned.
 */
export function silentGroups(registered: number[], planned: PlannedState[], enabled: GroupId[], cases: ParityCase[]): GroupId[] {
  const skipped = new Set(cases.filter((c) => c.skip !== undefined).map((c) => c.state))
  return [...new Set(enabled)].filter((g) => {
    const own = registered.filter((n) => groupOf(n) === g)
    if (own.length > 0 && own.every((n) => skipped.has(n))) return false
    return !planned.some((s) => s.group === g && (s.case === null || s.case.skip === undefined))
  })
}

/** The failure of a group silentGroups returns. */
export function silentGroupMessage(g: GroupId): string {
  return `parity group "${g}" is enabled but no approved state of it has a case to compare (none approved at its current hash, or every approved one skipped)`
}

/** The state's case, or the failure parity.pw.ts reports for it. */
export function requireCase(s: PlannedState): ParityCase {
  if (!s.case) throw new Error(`no parity case for #${s.n}`)
  return s.case
}

/** The registered state numbers (state-hashes.json). */
export function registeredStates(snapshot: unknown): number[] {
  const states = (snapshot as { states?: Record<string, unknown> } | null)?.states ?? {}
  return Object.keys(states)
    .map(Number)
    .sort((a, b) => a - b)
}

/** Reads enabled.json, the registered and the approved states from the repository. */
export function loadPlanInputs(): { enabled: GroupId[]; registered: number[]; approved: { n: number; label: string }[] } {
  const repo = join(FRONTEND, '..')
  const read = (path: string): unknown => {
    try {
      return JSON.parse(readFileSync(join(repo, path), 'utf8'))
    } catch (e) {
      if ((e as NodeJS.ErrnoException).code === 'ENOENT') return null
      throw e
    }
  }
  const snapshot = read('docs/design/state-hashes.json')
  let approvers: string | null = null
  try {
    approvers = readFileSync(join(repo, 'docs/design/approvers.txt'), 'utf8')
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code !== 'ENOENT') throw e
  }
  return {
    enabled: parseEnabled(readFileSync(join(FRONTEND, 'e2e', 'parity', 'enabled.json'), 'utf8')),
    registered: registeredStates(snapshot),
    approved: approvedStates(snapshot, read('docs/design/approvals.json'), parseApprovers(approvers)),
  }
}

// ---------------------------------------------------------------------------
// Rendering (Playwright, on Node).

/** The app's parity build, served by the engineering plan's `task parity`. */
export function appUrl(): string {
  const url = process.env.PARITY_APP_URL
  if (!url) {
    throw new Error(
      'PARITY_APP_URL is not set: parity groups are enabled (e2e/parity/enabled.json), so the app build must be served — run `task parity`, or set PARITY_APP_URL to the served parity build (bun run build:parity)',
    )
  }
  return url
}

let measuredCache: Record<string, string> | null = null
const measured = () => (measuredCache ??= measuredDigests(FRONTEND, [MEASURED, PROTO_MEASURED]))
// One prototype build and one app build per run: a URL's bytes never change.
const resourceCache = new Map<string, string>()

// ---------------------------------------------------------------------------
// Warming the resource cache (Node side).
//
// extractPage resolves every resource URL of a page at once (extract.ts
// resolveResources: one Promise.all of fetches, no retry). An app page can
// hold ~100 distinct /media/ URLs (Go's URLs are per member), and several
// Playwright workers extract at the same moment: on macOS the burst
// overflows the listen queue (kern.ipc.somaxconn, 128) and a fetch fails
// with ECONNRESET. So before extracting, parityTree resolves the page's
// URLs into the shared cache itself, a few at a time, retrying a fetch whose
// connection was reset; extractPage then finds them cached and fetches
// nothing. The identities are resolveResources' own (the same function, one
// URL per call), so nothing compared changes; a URL that still fails, or
// answers an HTTP error, is left out of the cache and extractPage reports it
// exactly as before. extract.ts (a digest input) is not touched.

/** At most this many of one page's resource fetches run at once (per worker). */
export const WARM_CONCURRENCY = 6
/** Tries per resource when its connection is reset. */
export const WARM_ATTEMPTS = 5

/** `fn` over `items` with at most `limit` calls in flight; the results in input order. */
export async function mapLimit<T, R>(items: readonly T[], limit: number, fn: (item: T, index: number) => Promise<R>): Promise<R[]> {
  const out = new Array<R>(items.length)
  let next = 0
  const worker = async () => {
    while (next < items.length) {
      const i = next++
      out[i] = await fn(items[i] as T, i)
    }
  }
  await Promise.all(Array.from({ length: Math.min(Math.max(1, Math.floor(limit)), items.length) }, worker))
  return out
}

/** Connection-level failure codes (Node's fetch puts them on the error's `cause`). */
const RESET_CODES = new Set(['ECONNRESET', 'ECONNREFUSED', 'EPIPE', 'UND_ERR_SOCKET'])

/** Whether `e` is a fetch that failed because its connection was refused, reset or closed (not an HTTP status). */
export function isConnectionReset(e: unknown): boolean {
  let cur: unknown = e
  for (let depth = 0; depth < 5 && typeof cur === 'object' && cur !== null; depth++) {
    const code = (cur as { code?: unknown }).code
    if (typeof code === 'string' && RESET_CODES.has(code)) return true
    cur = (cur as { cause?: unknown }).cause
  }
  return false
}

export interface RetryOptions {
  /** Tries in all (at least 1). */
  attempts: number
  retryable(e: unknown): boolean
  /** The wait before try `attempt + 1` (attempt counts from 1). */
  delayMs?(attempt: number): number
}

/** `fn`, tried again while it fails with a retryable error, at most `attempts` times; the last error is thrown. */
export async function withRetry<T>(fn: () => Promise<T>, o: RetryOptions): Promise<T> {
  const attempts = Math.max(1, Math.floor(o.attempts))
  for (let attempt = 1; ; attempt++) {
    try {
      return await fn()
    } catch (e) {
      if (attempt >= attempts || !o.retryable(e)) throw e
      const wait = o.delayMs?.(attempt) ?? 0
      if (wait > 0) await new Promise((resolve) => setTimeout(resolve, wait))
    }
  }
}

export interface WarmOptions {
  limit?: number
  attempts?: number
  delayMs?(attempt: number): number
  /** resolveResources (extract.ts); replaceable in tests. */
  resolve?(urls: string[], cache: Map<string, string>): Promise<Record<string, string>>
}

/**
 * Puts the `res:` identity of each of `urls` not yet in `cache` into it,
 * through `resolve` (resolveResources) one URL per call, at most `limit`
 * at once, each retried on a reset connection. Never throws: a URL that
 * still fails stays uncached, for extractPage to report.
 */
export async function warmResources(urls: readonly string[], cache: Map<string, string>, o: WarmOptions = {}): Promise<void> {
  const resolve = o.resolve ?? resolveResources
  const todo = [...new Set(urls)].filter((u) => !cache.has(u))
  await mapLimit(todo, o.limit ?? WARM_CONCURRENCY, (url) =>
    withRetry(() => resolve([url], cache), {
      attempts: o.attempts ?? WARM_ATTEMPTS,
      retryable: isConnectionReset,
      delayMs: o.delayMs ?? ((attempt) => 50 * 2 ** (attempt - 1)),
    }).then(
      () => undefined,
      () => undefined,
    ),
  )
}

/** Every resource URL extractPage will meet under `root` (the extractor's own collectResources), warmed into the cache. */
async function warmPage(root: ElementHandle<Element>, cache: Map<string, string>): Promise<void> {
  const known = [...cache.keys()].filter((k) => k.startsWith('sheet:')).map((k) => k.slice(6))
  const found = await root.evaluate(collectResources, known)
  const fromSheets = known.flatMap((href) => JSON.parse(cache.get(`sheet:${href}`)!) as string[])
  await warmResources([...found.urls, ...Object.values(found.sheets).flat(), ...fromSheets], cache)
}

/**
 * Page side: fonts loaded, images decoded, every animation and transition
 * cancelled (what the prototype's `freeze` does), then two animation frames
 * without a DOM mutation (as the prototype harness's open()).
 *
 * `pending`: the case's pendingImages. An image whose `src` or `currentSrc`
 * is exactly one of these URLs (each resolved against the document's base
 * URL) is not waited for; every other image is, as without it. Self-contained
 * (it runs in the page through page.evaluate).
 */
export async function settlePage(pending: string[] = []): Promise<void> {
  await document.fonts.ready
  const skip = new Set(pending.map((u) => new URL(u, document.baseURI).href))
  const awaited = Array.from(document.images).filter((img) => !skip.has(img.src) && !skip.has(img.currentSrc))
  await Promise.allSettled(awaited.map((img) => img.decode()))
  for (const a of document.getAnimations()) a.cancel()
  const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
  let mutated = false
  const observer = new MutationObserver(() => {
    mutated = true
  })
  observer.observe(document, { subtree: true, childList: true, attributes: true, characterData: true })
  try {
    let still = 0
    while (still < 2) {
      mutated = false
      await frame()
      still = mutated ? 0 : still + 1
    }
  } finally {
    observer.disconnect()
  }
}

/** Settles the page (settlePage); `pending`: the case's pendingImages, not waited for. */
export async function settle(page: Page, pending: string[] = []): Promise<void> {
  await page.evaluate(settlePage, pending)
}

/** Drives a loaded page through a case: drive, settle, interact, settle. */
export async function runCase(page: Page, c: ParityCase): Promise<void> {
  const pending = c.pendingImages ?? []
  if (c.drive) await c.drive(page)
  await settle(page, pending)
  if (c.interact) {
    await c.interact(page)
    await settle(page, pending)
  }
}

/** The normalised tree of the page's one `[data-app-root]`. */
export async function parityTree(page: Page): Promise<Tree> {
  const roots = await page.$$('[data-app-root]')
  if (roots.length !== 1) throw new Error(`parity: the page has ${roots.length} [data-app-root] elements, not 1`)
  const root = roots[0] as ElementHandle<Element>
  const layout = await root.evaluate((el): RootLayout => {
    const r = el.getBoundingClientRect()
    const doc = document.scrollingElement ?? document.documentElement
    const x = doc.scrollWidth > doc.clientWidth
    const y = doc.scrollHeight > doc.clientHeight
    return { x: r.left, y: r.top, width: r.width, height: r.height, documentScrolls: x && y ? 'both' : x ? 'x' : y ? 'y' : 'none' }
  })
  const twins = await page.evaluate(forceTwinSelectors)
  await warmPage(root, resourceCache)
  const tree = await extractPage(page, { root, measured: measured(), cache: resourceCache })
  return normaliseTree(tree, layout, twins)
}

/**
 * The prototype's scripted-render mode on a page that does not set it
 * itself (the app): ?freeze sets html[data-freeze], and base.css then
 * computes `animation: none; transition: none` on every element.
 */
export async function scriptedRender(page: Page): Promise<void> {
  await page.evaluate(() => document.documentElement.toggleAttribute('data-freeze', true))
}

/** Prototype state `n` at `size`, served at `baseUrl`, in a fresh design context. */
export async function prototypeTree(browser: Browser, baseUrl: string, n: number, size: Size): Promise<Tree> {
  const context = await newDesignContext(browser, size)
  try {
    const page = await context.newPage()
    await page.goto(`${baseUrl}?bare&freeze&size=${size}#s${n}`)
    await waitForHarness(page)
    await openState(page, n)
    return await parityTree(page)
  } finally {
    await context.close()
  }
}

/** How long the app may take to install window.__choixParity. */
export const APP_TIMEOUT_MS = 15_000

/** The app, at `baseUrl`, driven through case `c` at `size`, in a fresh design context. */
export async function appTree(browser: Browser, baseUrl: string, c: ParityCase, size: Size): Promise<Tree> {
  const context = await newDesignContext(browser, size)
  try {
    const page = await context.newPage()
    await page.goto(baseUrl)
    await page.waitForFunction(() => '__choixParity' in window, undefined, { timeout: APP_TIMEOUT_MS }).catch(() => {
      throw new Error(`parity: the app at ${baseUrl} did not install window.__choixParity within ${APP_TIMEOUT_MS} ms (is it the parity build?)`)
    })
    // The same render mode as the prototype's ?freeze (base.css is shared).
    await scriptedRender(page)
    await runCase(page, c)
    return await parityTree(page)
  } finally {
    await context.close()
  }
}
