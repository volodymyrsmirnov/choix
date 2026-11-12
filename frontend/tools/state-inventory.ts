// The state inventory: docs/design/inventory.md, generated from the
// prototype's scenarios (prototype/scenarios/*.tsx, checked by the registry's
// defineScenarios against docs/design/retired.json), the retired list, and
// NOT_STATES below.
//
//   bun tools/state-inventory.ts           write docs/design/inventory.md (task design:inventory)
//   bun tools/state-inventory.ts --check   exit 1 when the file differs from what this prints
//
// The output depends only on those inputs: rows in number order, no clock.
// tools/state-inventory.test.ts fails when the committed file is out of date.
import { readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineScenarios, GROUPS, type GroupId, type Scenario } from '../prototype/registry'

const FRONTEND = join(dirname(fileURLToPath(import.meta.url)), '..')
const ROOT = join(FRONTEND, '..')
const SCENARIOS = join(FRONTEND, 'prototype', 'scenarios')
const GATE = join(ROOT, 'docs', 'design', 'gate.json')
const RETIRED = join(ROOT, 'docs', 'design', 'retired.json')
export const INVENTORY = join(ROOT, 'docs', 'design', 'inventory.md')

/** One registered state, as the inventory lists it. */
export interface InventoryState {
  n: number
  group: GroupId
  label: string
  /** The scenario's `design` field: `mockup-2026-11-11#<screen>`, `v1:<path>` or `missing design`. */
  design: string
  /** The scenario file under prototype/scenarios/ that registers it. */
  file: string
}

/** Something deliberately not given a state, and why. */
export interface NotState {
  what: string
  reason: string
  /** Where the decision is written down. */
  source: string
}

/**
 * Deliberately not states. The registry has no machine-readable list of
 * these, so it lives here; each entry names where the decision is recorded.
 */
export const NOT_STATES: NotState[] = [
  {
    what: 'No. 309',
    reason: 'There is no "last photo" state: photo navigation in Focus wraps around, as in v1.',
    source: 'frontend/prototype/scenarios/focus.tsx (header)',
  },
  {
    what: 'The folder picker',
    reason: 'A macOS system panel: the app opens it, the system draws it.',
    source: 'docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md, item 9 of "How this plan reads the prompt"',
  },
  {
    what: 'The About panel',
    reason: 'A macOS system panel, as the folder picker.',
    source: 'docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md, item 9 of "How this plan reads the prompt"',
  },
  {
    what: 'Scroll positions',
    reason:
      'Not states of their own. Where code sets a scroll position (the Focus rail, the Folder cluster list, Settings), the scroller carries a data-measured-by id, so the code that computes the position is part of the fingerprint.',
    source: 'frontend/src/design/measured.ts',
  },
  {
    what: 'Timers: toast auto-dismiss, delayed indicators',
    reason:
      'How long something stays on screen belongs to the app\'s containers, not to the design: each toast and indicator look is a state, its lifetime is not. Design code may not use setTimeout or setInterval (lint-design: timer).',
    source: 'docs/superpowers/plans/2026-11-11-design-prototype-and-gate.md (Global Constraints); frontend/tools/lint-design.ts',
  },
  {
    what: 'Motion in progress (a transition or animation mid-way)',
    reason:
      'Every scripted render freezes motion (html[data-freeze]); the motion rules themselves are part of every fingerprint (Tree.motionRules, Tree.atRules).',
    source: 'frontend/tools/extract.ts',
  },
  {
    what: 'A light theme, another OS look',
    reason: 'docs/design/gate.json lists one theme (dark) and one OS (macos); the navigator\'s toggles exist with that one option each.',
    source: 'docs/design/gate.json',
  },
  {
    what: 'Nos. 870–899',
    reason:
      'Reserved for the test-only fixture page of the check and parity self-tests (broken states 880–899, clean ones 870–879); never part of the prototype build.',
    source: 'frontend/tools/fixtures/kinds.ts',
  },
]

/** docs/design/retired.json as numbers (an entry may also be `{ "n": N }`, as scenarios/index.ts reads it). */
export function readRetired(): number[] {
  const raw = JSON.parse(readFileSync(RETIRED, 'utf8')) as Array<number | { n: number }>
  return raw.map((r) => (typeof r === 'number' ? r : r.n))
}

/** Every registered state, checked by defineScenarios (duplicates, ranges, retired numbers) and ordered by number. */
export async function loadStates(): Promise<InventoryState[]> {
  const fileOf = new Map<Scenario, string>()
  const all: Scenario[] = []
  for (const file of readdirSync(SCENARIOS).filter((f) => f.endsWith('.tsx')).sort()) {
    const mod = (await import(join(SCENARIOS, file))) as { scenarios?: Scenario[] }
    for (const s of mod.scenarios ?? []) {
      fileOf.set(s, file)
      all.push(s)
    }
  }
  return defineScenarios(all, readRetired()).map((s) => ({ n: s.n, group: s.group, label: s.label, design: s.design, file: fileOf.get(s)! }))
}

const cell = (text: string) => text.replace(/\|/g, '\\|').replace(/\n/g, ' ')
const range = (r: [number, number]) => `${r[0]}–${r[1]}`

/** The inventory's Markdown. Pure: the same inputs give the same text, whatever their order. */
export function renderInventory(states: InventoryState[], retired: number[], notStates: NotState[], reviewUrl: string): string {
  const review = reviewUrl.replace(/\/+$/, '')
  const sorted = [...states].sort((a, b) => a.n - b.n)
  const title = (id: GroupId) => GROUPS.find((g) => g.id === id)?.title ?? id
  const files = (id: GroupId) => [...new Set(sorted.filter((s) => s.group === id).map((s) => s.file))].sort()
  const out: string[] = [
    '# State inventory',
    '',
    '<!-- Generated by `task design:inventory` (frontend/tools/state-inventory.ts). Do not edit by hand: `bun test` fails when this file differs from what the generator prints. -->',
    '',
    'There is no Figma file for this project: the prototype is the design source, and the owner approves each state in it. The Design source column names the scenario file that draws the state; the Reference column names what the state was built from: a screen of the owner\'s 2026-11-11 mockup (`mockup-2026-11-11#<screen>`, not committed), a v1 file under `internal/ui/web/src/` (`v1:<path>`), or `missing design`.',
    '',
    `${sorted.length} states. Each number opens its state at \`${review}/#s<N>\` while the review build is served (\`task design:build && task design:serve\`). Numbers are permanent: a removed state is retired, never renumbered or reused. How states are reviewed and approved: [README.md](README.md).`,
    '',
    '## Groups',
    '',
    '| Group | Range | States | Scenario file |',
    '|---|---|---|---|',
  ]
  for (const g of GROUPS) {
    const count = sorted.filter((s) => s.group === g.id).length
    const links = files(g.id).map((f) => `[scenarios/${f}](../../frontend/prototype/scenarios/${f})`)
    out.push(`| ${g.title} | ${range(g.range)} | ${count} | ${links.join(', ') || '—'} |`)
  }
  out.push('', '## States', '', '| # | State | Group | Design source | Reference |', '|---|---|---|---|---|')
  for (const s of sorted) {
    const source = `prototype: [scenarios/${s.file}](../../frontend/prototype/scenarios/${s.file})`
    const ref = s.design === 'missing design' ? 'missing design' : `\`${s.design}\``
    out.push(`| [${s.n}](${review}/#s${s.n}) | ${cell(s.label)} | ${title(s.group)} | ${source} | ${ref} |`)
  }
  out.push('', '## Retired', '')
  const gone = [...new Set(retired)].sort((a, b) => a - b)
  if (gone.length === 0) {
    out.push('No state is retired yet. A retired number is listed in `docs/design/retired.json`, and the registry refuses to register it again.')
  } else {
    out.push('From `docs/design/retired.json`. The registry refuses to register these numbers again.', '', '| # |', '|---|')
    for (const n of gone) out.push(`| ${n} |`)
  }
  out.push('', '## Not states', '', 'Deliberately not given a state.', '', '| What | Why | Recorded in |', '|---|---|---|')
  for (const e of notStates) out.push(`| ${cell(e.what)} | ${cell(e.reason)} | ${cell(e.source)} |`)
  out.push('')
  return out.join('\n')
}

/** What docs/design/inventory.md must hold. */
export async function inventoryText(): Promise<string> {
  const gate = JSON.parse(readFileSync(GATE, 'utf8')) as { reviewUrl?: string }
  return renderInventory(await loadStates(), readRetired(), NOT_STATES, gate.reviewUrl ?? '')
}

if (import.meta.main) {
  const text = await inventoryText()
  if (process.argv.includes('--check')) {
    let current = ''
    try {
      current = readFileSync(INVENTORY, 'utf8')
    } catch {}
    if (current !== text) {
      console.error('inventory: docs/design/inventory.md is out of date: run `task design:inventory`')
      process.exit(1)
    }
    console.log('inventory: docs/design/inventory.md is current')
  } else {
    writeFileSync(INVENTORY, text)
    console.log(`inventory: ${text.split('\n').filter((l) => l.startsWith('| [')).length} states → docs/design/inventory.md`)
  }
}
