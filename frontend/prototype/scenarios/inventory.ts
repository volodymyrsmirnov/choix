// The plan's state inventory (docs/superpowers/plans/…-design-prototype-and-gate.md,
// "State inventory"), parsed: number → the label a scenario must carry.
// Test-only (it reads the disk).
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

export const PLAN = join(import.meta.dir, '..', '..', '..', 'docs', 'superpowers', 'plans', '2026-11-11-design-prototype-and-gate.md')

/** An inventory entry as a scenario label: markdown removed, quotes curled, first letter capitalised. */
export function labelOf(entry: string): string {
  let s = entry.replace(/\*/g, '').replace(/`/g, '').trim()
  s = s.replace(/"([^"]*)"/g, '“$1”')
  const i = s.search(/[A-Za-z]/)
  return i < 0 ? s : s.slice(0, i) + s.charAt(i).toUpperCase() + s.slice(i + 1)
}

/** Every state the inventory lists, by number. */
export function inventory(text = readFileSync(PLAN, 'utf8')): Map<number, string> {
  const start = text.indexOf('## State inventory')
  const table = text.slice(start, text.indexOf('\n\n', text.indexOf('| Group | States |', start)))
  const out = new Map<number, string>()
  for (const row of table.split('\n').filter((l) => /^\| [A-Z]/.test(l) && !l.startsWith('| Group'))) {
    const cells = row.split('|').map((c) => c.trim())
    for (const entry of cells[2]!.split(' · ')) {
      const m = /^(\d+) (.+)$/.exec(entry.trim())
      if (m) out.set(Number(m[1]), labelOf(m[2]!))
    }
  }
  return out
}
