import { expect, test } from 'bun:test'
import { designRefProblem } from './designRef'
import { scenarios } from './welcome'

test('welcome and shell hold exactly the inventory states 11–17, 30–37 and 40 (s10 dropped)', () => {
  expect(scenarios.map((s) => s.n).sort((a, b) => a - b)).toEqual([
    11, 12, 13, 14, 15, 16, 17, 30, 31, 32, 33, 34, 35, 36, 37, 40,
  ])
  expect(scenarios.every((s) => s.group === 'welcome')).toBe(true)
})

test('every welcome state names its design reference in the plan format', () => {
  for (const s of scenarios) expect(designRefProblem(s.design)).toBeNull()
})
