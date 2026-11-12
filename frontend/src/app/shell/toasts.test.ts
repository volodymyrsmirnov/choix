import { afterEach, beforeEach, describe, expect, jest, test } from 'bun:test'
import { createToasts, TOAST_MS } from './toasts'

/** Lets settled promises run their callbacks (the fake timers do not cover microtasks). */
async function settle() {
  for (let i = 0; i < 10; i++) await Promise.resolve()
}

beforeEach(() => {
  jest.useFakeTimers()
})
afterEach(() => {
  jest.useRealTimers()
})

describe('toasts', () => {
  test('dismisses after its duration', () => {
    const toasts = createToasts()
    let changes = 0
    toasts.subscribe(() => changes++)
    const id = toasts.push({ tone: 'info', title: '“Film scans” left the Library' })
    expect(toasts.items()).toEqual([{ id, tone: 'info', title: '“Film scans” left the Library' }])

    jest.advanceTimersByTime(TOAST_MS - 1)
    expect(toasts.items()).toHaveLength(1)
    jest.advanceTimersByTime(1)
    expect(toasts.items()).toEqual([])
    expect(changes).toBe(2)

    // An explicit duration (v1's "Undone": 1.5 s).
    toasts.push({ tone: 'pick', title: 'Undone', duration: 1500 })
    jest.advanceTimersByTime(1500)
    expect(toasts.items()).toEqual([])
  })

  test('undo keeps the toast until resolved', async () => {
    const toasts = createToasts()
    let resolveUndo: () => void = () => {}
    let undos = 0
    const id = toasts.push({
      tone: 'pick',
      title: 'Picked',
      onUndo: () => {
        undos++
        return new Promise<void>((r) => (resolveUndo = r))
      },
    })
    expect(toasts.items()[0]).toEqual({ id, tone: 'pick', title: 'Picked', undo: true })

    toasts.undo(id)
    toasts.undo(id) // a second click while the first is in flight does nothing
    expect(undos).toBe(1)
    jest.advanceTimersByTime(TOAST_MS * 3)
    expect(toasts.items().map((t) => t.id)).toEqual([id])

    resolveUndo()
    await settle()
    expect(toasts.items()).toEqual([])
  })

  test('a keyed toast replaces the one with its key in place and restarts its timer', () => {
    const toasts = createToasts()
    toasts.push({ tone: 'info', title: 'First' })
    const a = toasts.push({ tone: 'pick', title: 'Picked', key: 'pick' })
    jest.advanceTimersByTime(TOAST_MS - 100)
    const b = toasts.push({ tone: 'reject', title: 'Rejected', key: 'pick' })
    expect(b).toBe(a)
    expect(toasts.items().map((t) => t.title)).toEqual(['First', 'Rejected'])
    jest.advanceTimersByTime(200)
    expect(toasts.items().map((t) => t.title)).toEqual(['Rejected'])
    toasts.dismiss(b)
    expect(toasts.items()).toEqual([])
  })

  test('an undo that shows its own keyed toast leaves that toast up', async () => {
    const toasts = createToasts()
    const id = toasts.push({
      tone: 'pick',
      title: 'Picked',
      key: 'pick',
      onUndo: async () => {
        toasts.push({ tone: 'pick', title: 'Undone', key: 'pick', duration: 1500 })
      },
    })
    toasts.undo(id)
    await settle()
    expect(toasts.items().map((t) => t.title)).toEqual(['Undone'])
    jest.advanceTimersByTime(1500)
    expect(toasts.items()).toEqual([])
  })
})
