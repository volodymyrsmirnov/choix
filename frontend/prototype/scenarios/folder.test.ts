import { expect, test } from 'bun:test'
import { designRefProblem } from './designRef'
import { scenarios } from './folder'

test('folder holds exactly the inventory states 200–231', () => {
  const want = Array.from({ length: 32 }, (_, i) => 200 + i)
  expect(scenarios.map((s) => s.n).sort((a, b) => a - b)).toEqual(want)
  expect(scenarios.every((s) => s.group === 'folder')).toBe(true)
})

test('every folder state names its design reference in the plan format', () => {
  for (const s of scenarios) expect(designRefProblem(s.design)).toBeNull()
})

test('the menu and toast states declare the overlays they show open', () => {
  const overlays = (n: number) => scenarios.find((s) => s.n === n)?.overlays ?? []
  expect(overlays(206)).toEqual(['folder-camera-menu'])
  expect(overlays(218)).toEqual(['folder-sort-menu'])
  expect(overlays(222)).toEqual(['toast:reclustered'])
  expect(overlays(223)).toEqual(['toast:recluster-failed'])
  expect(overlays(202)).toEqual([])
})
