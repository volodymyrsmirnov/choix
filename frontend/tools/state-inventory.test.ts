// docs/design/inventory.md is what tools/state-inventory.ts prints, and it names
// every retired number under "Retired".
import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { GROUPS } from '../prototype/registry'
import { INVENTORY, inventoryText, loadStates, NOT_STATES, readRetired, renderInventory, type InventoryState } from './state-inventory'

const committed = () => readFileSync(INVENTORY, 'utf8')

/** The lines of one `## <title>` section, up to the next `## `. */
function section(text: string, title: string): string {
  const start = text.indexOf(`\n## ${title}\n`)
  expect(start, `no "## ${title}" section`).toBeGreaterThan(-1)
  const end = text.indexOf('\n## ', start + 1)
  return text.slice(start, end < 0 ? undefined : end)
}

describe('docs/design/inventory.md', () => {
  test('the committed file is what the generator prints (run `task design:inventory`)', async () => {
    expect(committed()).toBe(await inventoryText())
  })

  test('every number in retired.json appears under "Retired"', () => {
    const retired = section(committed(), 'Retired')
    for (const n of readRetired()) expect(retired).toContain(`| ${n} |`)
  })

  test('every registered state has one row with its number, label, group and a design source', async () => {
    const text = committed()
    const states = await loadStates()
    expect(states.length).toBeGreaterThan(140)
    for (const s of states) {
      const rows = text.split('\n').filter((l) => l.startsWith(`| [${s.n}](`))
      expect(rows.length, `#${s.n}`).toBe(1)
      expect(rows[0]).toContain(s.label.replace(/\|/g, '\\|'))
      expect(rows[0]).toContain(GROUPS.find((g) => g.id === s.group)!.title)
      expect(rows[0]).toContain(`prototype: [scenarios/${s.file}]`)
    }
  })
})

describe('renderInventory', () => {
  const states: InventoryState[] = [
    { n: 102, group: 'library', label: 'Filled, six folders', design: 'mockup-2026-11-11#s-a', file: 'library.tsx' },
    { n: 100, group: 'library', label: 'A | B', design: 'missing design', file: 'library.tsx' },
  ]
  const text = renderInventory(states, [10, 309], NOT_STATES, 'http://localhost:8811')

  test('retired numbers are listed under "Retired", and an empty list says so', () => {
    const retired = section(text, 'Retired')
    expect(retired).toContain('| 10 |')
    expect(retired).toContain('| 309 |')
    expect(section(renderInventory(states, [], NOT_STATES, 'http://localhost:8811'), 'Retired')).toContain('No state is retired yet')
  })

  test('rows are in number order, link to the review build, escape pipes and never leave the design source empty', () => {
    const rows = text.split('\n').filter((l) => l.startsWith('| [1'))
    expect(rows[0]).toStartWith('| [100](http://localhost:8811/#s100) | A \\| B | Library | prototype: [scenarios/library.tsx]')
    expect(rows[1]).toStartWith('| [102](http://localhost:8811/#s102) |')
    expect(rows[1]).toContain('`mockup-2026-11-11#s-a`')
  })

  test('every "Not states" entry has a reason', () => {
    expect(NOT_STATES.length).toBeGreaterThan(0)
    for (const e of NOT_STATES) {
      expect(e.what.trim().length).toBeGreaterThan(0)
      expect(e.reason.trim().length).toBeGreaterThan(0)
    }
    const notStates = section(text, 'Not states')
    for (const e of NOT_STATES) expect(notStates).toContain(e.what)
  })

  test('the output does not depend on the clock or on input order', () => {
    expect(renderInventory([...states].reverse(), [309, 10], NOT_STATES, 'http://localhost:8811')).toBe(text)
  })
})
