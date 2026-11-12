import { expect, test } from 'bun:test'
import { createStoreHost, toastsOf } from './store'

test('set merges patches; reset empties the state', () => {
  const host = createStoreHost()
  host.set({ a: 1 })
  host.set({ b: 2, a: 3 })
  expect(host.snapshot()).toEqual({ a: 3, b: 2 })
  host.reset()
  expect(host.snapshot()).toEqual({})
})

test('every change gives a new snapshot and notifies subscribers', () => {
  const host = createStoreHost()
  let calls = 0
  const off = host.subscribe(() => calls++)
  const before = host.snapshot()
  host.set({ a: 1 })
  expect(host.snapshot()).not.toBe(before)
  host.reset()
  off()
  host.set({ b: 1 })
  expect(calls).toBe(2)
})

test('toast appends to `toasts`; a toast closes when its overlay id is set false', () => {
  const host = createStoreHost()
  host.toast({ id: 'a', tone: 'info', title: 'A' })
  host.toast({ id: 'b', tone: 'info', title: 'B' })
  const seeded = [{ id: 's', tone: 'warn' as const, title: 'S' }]
  expect(toastsOf(host.snapshot(), seeded).map((t) => t.id)).toEqual(['s', 'a', 'b'])
  host.set({ 'toast:s': false, 'toast:a': false })
  expect(toastsOf(host.snapshot(), seeded).map((t) => t.id)).toEqual(['b'])
})
