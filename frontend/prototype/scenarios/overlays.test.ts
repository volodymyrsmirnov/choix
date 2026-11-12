import { expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { NOW } from '../clock'
import type { ScenarioCtx } from '../registry'
import { designRefProblem } from './designRef'
import { scenarios } from './overlays'

const ctx = (): ScenarioCtx => ({
  size: 'default',
  now: NOW,
  store: { state: {}, set() {}, go() {}, toast() {} },
})
const render = (n: number) => renderToStaticMarkup(scenarios.find((s) => s.n === n)!.render(ctx()))

test('overlays holds exactly the inventory states 600–605', () => {
  expect(scenarios.map((s) => s.n).sort((a, b) => a - b)).toEqual([600, 601, 602, 603, 604, 605])
  expect(scenarios.every((s) => s.group === 'overlays')).toBe(true)
})

test('every overlays state names its design reference in the plan format', () => {
  for (const s of scenarios) expect(designRefProblem(s.design)).toBeNull()
})

test('600 shows the shortcut sheet over the Library, Library section first', () => {
  const html = render(600)
  expect(html).toContain('data-testid="shortcuts-overlay"')
  expect(html).toContain('Keyboard shortcuts')
  const first = html.indexOf('data-shortcuts-group="library"')
  expect(first).toBeGreaterThan(0)
  expect(first).toBeLessThan(html.indexOf('data-shortcuts-group="focus"'))
  expect(html).toContain('· this screen')
  expect(scenarios.find((s) => s.n === 600)!.overlays).toEqual(['shortcuts'])
})

test('each toast state shows its toasts and declares their overlays', () => {
  const tones: Record<number, string[]> = { 601: ['pick'], 602: ['reject'], 603: ['warn', 'info', 'pick'], 604: ['warn'], 605: ['info'] }
  for (const [n, want] of Object.entries(tones)) {
    const s = scenarios.find((x) => x.n === Number(n))!
    const html = render(s.n)
    expect([...html.matchAll(/data-tone="(\w+)" data-testid="toast"/g)].map((m) => m[1])).toEqual(want)
    expect(s.overlays?.length).toBe(want.length)
    expect(html).not.toContain('shortcuts-overlay')
  }
})

/** The toasts of a rendered state, in order, each with whether it offers Undo (the toasts are the page's last markup). */
function toastsWithUndo(html: string): { id: string; undo: boolean }[] {
  const parts = html.split(/(?=<div[^>]*data-overlay="toast:)/).slice(1)
  return parts.map((p) => ({
    id: /data-overlay="toast:([^"]+)"/.exec(p)![1]!,
    undo: />Undo</.test(p),
  }))
}

test('only the pick toast offers Undo', () => {
  expect(toastsWithUndo(render(601))).toEqual([{ id: 'picked', undo: true }])
  expect(toastsWithUndo(render(603))).toEqual([
    { id: 'unreadable', undo: false },
    { id: 'clip-ready', undo: false },
    { id: 'picked', undo: true },
  ])
  for (const n of [602, 604, 605]) expect(render(n)).not.toContain('>Undo<')
})
