// The parity channel (parityUi.ts): inert unless the bundle was built in
// mode `parity` — no global is read, nothing is seeded — and in that mode
// it reads the loaded fixture's `ui` record and seeds its toasts.
import { afterEach, expect, test } from 'bun:test'
import type { ToastItem } from '../../design/types'
import { createParityUi, seedParityToasts, uiOverride, type ParityUiHost } from './parityUi'
import { createToasts, type ToastInput } from './toasts'

function fakeHost(ui: Record<string, unknown>, toasts: ToastItem[] = []) {
  const listeners = new Set<() => void>()
  let reads = 0
  let loaded = false
  const host: ParityUiHost = {
    ui: () => {
      reads++
      return loaded ? ui : {}
    },
    toasts: () => {
      reads++
      return loaded ? toasts : []
    },
    subscribe(l) {
      reads++
      listeners.add(l)
      return () => listeners.delete(l)
    },
  }
  const fire = () => {
    loaded = true
    for (const l of [...listeners]) l()
  }
  return { host, reads: () => reads, fire, listeners }
}

const g = globalThis as { window?: unknown }
const hadWindow = 'window' in g
const savedWindow = g.window
afterEach(() => {
  if (hadWindow) g.window = savedWindow
  else delete g.window
})

test('production mode returns undefined and never reads the host', () => {
  const fake = fakeHost({ 'settings.reveal': 'models' })
  const channel = createParityUi('production', () => fake.host)
  expect(channel.uiOverride('settings.reveal')).toBeUndefined()
  expect(fake.reads()).toBe(0)
})

test('the module-level channel is inert outside a parity build, even with a host on the window', () => {
  // bun test is not a Vite build in mode `parity`.
  const fake = fakeHost({ 'settings.reveal': 'models' }, [{ id: 'x', tone: 'info', title: 'Hi' }])
  g.window = { __choixParityUi: fake.host }
  expect(uiOverride('settings.reveal')).toBeUndefined()
  const pushed: ToastInput[] = []
  const stop = seedParityToasts({ push: (t) => (pushed.push(t), 'id') })
  fake.fire()
  stop()
  expect(pushed).toEqual([])
  expect(fake.listeners.size).toBe(0)
  expect(fake.reads()).toBe(0)
})

test('parity mode reads the key from the loaded fixture', () => {
  const fake = fakeHost({ 'settings.reveal': 'models' })
  const channel = createParityUi('parity', () => fake.host)
  expect(channel.uiOverride('settings.reveal')).toBeUndefined()
  fake.fire()
  expect(channel.uiOverride<string>('settings.reveal')).toBe('models')
  expect(channel.uiOverride('nothing')).toBeUndefined()
})

test('parity mode without a host (the page has no mock control) returns undefined', () => {
  const channel = createParityUi('parity', () => undefined)
  expect(channel.uiOverride('settings.reveal')).toBeUndefined()
  expect(channel.seedToasts({ push: () => 'id' })()).toBeUndefined()
})

test('parity mode seeds each load’s toasts into the store, kept until dismissed, Undo shown where the fixture has it', () => {
  const fixtureToasts: ToastItem[] = [
    { id: 'warn', tone: 'warn', title: 'Three files could not be read', body: 'Left out.' },
    { id: 'picked', tone: 'pick', title: 'Picked', undo: true },
  ]
  const fake = fakeHost({}, fixtureToasts)
  const channel = createParityUi('parity', () => fake.host)
  const toasts = createToasts()
  const stop = channel.seedToasts(toasts)
  expect(toasts.items()).toEqual([])
  fake.fire()
  // The store's own ids; the rest as the fixture says.
  expect(toasts.items().map(({ id: _id, ...rest }) => rest)).toEqual([
    { tone: 'warn', title: 'Three files could not be read', body: 'Left out.' },
    { tone: 'pick', title: 'Picked', undo: true },
  ])
  stop()
  expect(fake.listeners.size).toBe(0)
  for (const t of toasts.items()) toasts.dismiss(t.id)
})

test('a fixture loaded before the shell started is seeded at once', () => {
  const fake = fakeHost({}, [{ id: 'info', tone: 'info', title: 'Ready' }])
  fake.fire()
  const toasts = createToasts()
  const stop = createParityUi('parity', () => fake.host).seedToasts(toasts)
  expect(toasts.items().map((t) => t.title)).toEqual(['Ready'])
  stop()
  for (const t of toasts.items()) toasts.dismiss(t.id)
})

test('parity mode tells a subscriber about each fixture load (an override read by the shell follows it)', () => {
  const fake = fakeHost({ 'shell.renderError': 'Invalid time value' })
  const channel = createParityUi('parity', () => fake.host)
  let calls = 0
  const stop = channel.subscribe(() => calls++)
  expect(channel.uiOverride('shell.renderError')).toBeUndefined()
  fake.fire()
  expect(calls).toBe(1)
  expect(channel.uiOverride<string>('shell.renderError')).toBe('Invalid time value')
  stop()
  fake.fire()
  expect(calls).toBe(1)
})

test('outside parity, subscribe does nothing and never reads the host', () => {
  const fake = fakeHost({})
  const channel = createParityUi('production', () => fake.host)
  channel.subscribe(() => {})()
  expect(fake.reads()).toBe(0)
})
