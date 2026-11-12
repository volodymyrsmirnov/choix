import { expect, test } from 'bun:test'
import { summarize, type Result } from './results'

const r = (over: Partial<Result>): Result => ({ browser: 'chromium', group: 'library', size: 'min', states: [100], renders: 2, failures: [], ...over })

test('check-states: failures, distinct states, sizes, browsers and the render count', () => {
  const line = summarize('check-states', [
    r({ states: [100, 101], renders: 4 }),
    r({ size: 'large', states: [100, 101], renders: 4 }),
    r({ browser: 'webkit', states: [100, 101], renders: 4, failures: [{ check: 'clip', state: 100, size: 'min', browser: 'webkit', message: 'm' }] }),
  ])
  expect(line).toBe('check-states: 1 failure · 2 states × 2 sizes × 2 browsers, 12 renders')
})

test('flows: failures, groups and states', () => {
  expect(summarize('flows', [r({ group: 'welcome', states: [11] }), r({ states: [100, 102] })])).toBe(
    'flows: 0 failures · 2 groups (library, welcome), 3 states',
  )
})
