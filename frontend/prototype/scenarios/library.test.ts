import { expect, test } from 'bun:test'
import { designRefProblem } from './designRef'
import { scenarios } from './library'

test('library holds exactly the inventory states 100–131, without the retired 112 and 120', () => {
  const want = Array.from({ length: 32 }, (_, i) => 100 + i).filter((n) => n !== 112 && n !== 120)
  expect(scenarios.map((s) => s.n).sort((a, b) => a - b)).toEqual(want)
  expect(scenarios.every((s) => s.group === 'library')).toBe(true)
})

test('every library state names its design reference in the plan format', () => {
  for (const s of scenarios) expect(designRefProblem(s.design)).toBeNull()
})

test('the toolbar menu states declare the overlay they show open', () => {
  const overlays = (n: number) => scenarios.find((s) => s.n === n)?.overlays ?? []
  expect(overlays(126)).toEqual(['library-order-menu'])
  expect(overlays(127)).toEqual(['library-show-menu'])
})

test('every folder the Library and welcome states list carries added_at (unix seconds), as Go sends it, and no two share one', async () => {
  const { scenarios: welcome } = await import('./welcome')
  const { scenarioProps } = await import('../../e2e/parity/fixtures/library')
  for (const s of [...scenarios, ...welcome.filter((w) => w.n !== 40)]) {
    const folders = scenarioProps([s], s.n).folders ?? []
    for (const f of folders) expect([s.n, f.id, Number.isInteger(f.added_at)]).toEqual([s.n, f.id, true])
    const byId = new Map(folders.map((f) => [f.added_at, f.id]))
    expect([s.n, byId.size]).toEqual([s.n, new Set(folders.map((f) => f.id)).size])
  }
})
