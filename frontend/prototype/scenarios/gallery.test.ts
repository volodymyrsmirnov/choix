import { expect, test } from 'bun:test'
import { designRefProblem } from './designRef'
import { scenarios } from './gallery'

test('gallery holds exactly the inventory states 800–809', () => {
  expect(scenarios.map((s) => s.n).sort((a, b) => a - b)).toEqual([800, 801, 802, 803, 804, 805, 806, 807, 808, 809])
  expect(scenarios.every((s) => s.group === 'gallery')).toBe(true)
})

test('every gallery state names its design reference in the plan format', () => {
  for (const s of scenarios) expect(designRefProblem(s.design)).toBeNull()
})
