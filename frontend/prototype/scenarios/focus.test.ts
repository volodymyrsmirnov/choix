import { expect, test } from 'bun:test'
import { designRefProblem } from './designRef'
import { scenarios } from './focus'

test('focus holds exactly the inventory states 300–318, without 309', () => {
  const want = Array.from({ length: 19 }, (_, i) => 300 + i).filter((n) => n !== 309)
  expect(scenarios.map((s) => s.n).sort((a, b) => a - b)).toEqual(want)
  expect(scenarios.every((s) => s.group === 'focus')).toBe(true)
})

test('every focus state names its design reference in the plan format', () => {
  for (const s of scenarios) expect(designRefProblem(s.design)).toBeNull()
})

test('314: the photo on screen has no quality readout (the stage controls carry none)', async () => {
  const { renderToStaticMarkup } = await import('react-dom/server')
  const { NOW } = await import('../clock')
  const s = scenarios.find((x) => x.n === 314)!
  const html = renderToStaticMarkup(s.render({ size: 'default', now: NOW, store: { state: {}, set() {}, go() {}, toast() {} } }))
  const controls = html.slice(html.indexOf('data-testid="stage-controls"'))
  expect(controls.length).toBeGreaterThan(0)
  expect(html).not.toContain('data-testid="quality-hero"')
  // 301, the same cluster on a rated photo, does show it.
  const rated = renderToStaticMarkup(scenarios.find((x) => x.n === 301)!.render({ size: 'default', now: NOW, store: { state: {}, set() {}, go() {}, toast() {} } }))
  expect(rated).toContain('data-testid="quality-hero"')
})
