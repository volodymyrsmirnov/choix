// The error screen's case (#40) against its scenario, without a browser:
// the fixture hands the app the scenario's error text through the parity
// channel, and the case only loads it.
import { expect, test } from 'bun:test'
import { scenarios } from '../../prototype/scenarios/welcome'
import type { ErrorScreenProps } from '../../src/design/screens/error/props'
import { CASES, ERROR_SCREEN_STATE, RENDER_ERROR_KEY, WELCOME_STATES } from './cases'
import { WELCOME } from './fixtures/welcome'

const CTX = { size: 'default', store: { state: {}, set() {}, go() {}, toast() {} }, now: new Date(0) } as const

test('#40 is the welcome group’s error screen, driven by its fixture alone, not a Library state', () => {
  expect(ERROR_SCREEN_STATE).toBe(40)
  expect(WELCOME_STATES).not.toContain(ERROR_SCREEN_STATE)
  const c = CASES.find((x) => x.state === ERROR_SCREEN_STATE)
  expect([c?.skip, typeof c?.drive, c?.interact]).toEqual([undefined, 'function', undefined])
})

test('#40’s fixture carries the scenario’s error text under shell.renderError, on the Library route', () => {
  const s = scenarios.find((x) => x.n === ERROR_SCREEN_STATE)!
  const p = s.render(CTX).props as ErrorScreenProps
  const f = WELCOME[ERROR_SCREEN_STATE]!
  expect(RENDER_ERROR_KEY).toBe('shell.renderError')
  expect(f.route).toBe('#/')
  expect(f.ui).toEqual({ [RENDER_ERROR_KEY]: p.details })
})
