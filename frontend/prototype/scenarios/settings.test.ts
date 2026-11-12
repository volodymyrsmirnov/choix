import { expect, test } from 'bun:test'
import { designRefProblem } from './designRef'
import { scenarios } from './settings'

test('settings holds exactly the inventory states 500–520, without the retired 505, 506 and 514–517', () => {
  const retired = [505, 506, 514, 515, 516, 517]
  const want = Array.from({ length: 21 }, (_, i) => 500 + i).filter((n) => !retired.includes(n))
  expect(scenarios.map((s) => s.n).sort((a, b) => a - b)).toEqual(want)
  expect(scenarios.every((s) => s.group === 'settings')).toBe(true)
})

test('every settings state names its design reference in the plan format', () => {
  for (const s of scenarios) expect(designRefProblem(s.design)).toBeNull()
})

test('the leave prompt is declared where it is open', () => {
  expect(scenarios.find((s) => s.n === 508)?.overlays).toContain('settings-leave')
})
