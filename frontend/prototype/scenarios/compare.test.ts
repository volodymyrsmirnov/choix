import { expect, test } from 'bun:test'
import { scenarios } from './compare'
import { designRefProblem } from './designRef'

test('compare holds exactly the inventory states 400–409', () => {
  const want = Array.from({ length: 10 }, (_, i) => 400 + i)
  expect(scenarios.map((s) => s.n).sort((a, b) => a - b)).toEqual(want)
  expect(scenarios.every((s) => s.group === 'compare')).toBe(true)
})

test('every compare state names its design reference in the plan format', () => {
  for (const s of scenarios) expect(designRefProblem(s.design)).toBeNull()
})
