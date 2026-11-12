// The prototype holds exactly the plan's state inventory, in its wording.
import { expect, test } from 'bun:test'
import { readdirSync } from 'node:fs'
import { join } from 'node:path'
import retired from '../../../docs/design/retired.json'
import type { Scenario } from '../registry'
import { inventory, labelOf } from './inventory'

async function allScenarios(): Promise<Scenario[]> {
  const out: Scenario[] = []
  for (const f of readdirSync(import.meta.dir).filter((f) => f.endsWith('.tsx'))) {
    out.push(...((await import(join(import.meta.dir, f))) as { scenarios: Scenario[] }).scenarios)
  }
  return out
}

test('labels are written as the inventory writes them', () => {
  expect(labelOf('13 quality failed'.slice(3))).toBe('Quality failed')
  expect(labelOf('"folder removed" toast')).toBe('“Folder removed” toast')
  expect(labelOf('213 video cluster *(S3)*'.slice(4))).toBe('Video cluster (S3)')
  expect(labelOf('strip with `+N` overflow')).toBe('Strip with +N overflow')
})

test('every state is in the inventory, under the inventory’s label, and every inventory state exists unless retired', async () => {
  const inv = inventory()
  const states = await allScenarios()
  // The plan's inventory is a historical document: a state retired since
  // (docs/design/retired.json) stays listed there.
  const gone = new Set((retired as Array<number | { n: number }>).map((r) => (typeof r === 'number' ? r : r.n)))
  expect(states.length).toBeGreaterThan(140)
  for (const s of states) expect(`${s.n} ${s.label}`).toBe(`${s.n} ${inv.get(s.n)}`)
  for (const n of gone) expect([n, inv.has(n)]).toEqual([n, true])
  expect([...inv.keys()].filter((n) => !gone.has(n)).sort((a, b) => a - b)).toEqual(states.map((s) => s.n).sort((a, b) => a - b))
})
