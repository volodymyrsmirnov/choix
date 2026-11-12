// The quality store starts from Catalog.QualityStatus and applies
// quality:status, passing the engine's snapshot through unchanged,
// including the rating passes of the folders rated at once (folder_id,
// passes): the design names, counts and filters them per folder.
import { describe, expect, test } from 'bun:test'
import type { AppEvents, On } from './events'
import { createQualityStore, type QualityData } from './qualityStore'

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

function fakeBus() {
  const handlers = new Map<string, Set<(data: unknown) => void>>()
  const on: On = (name, handler) => {
    const set = handlers.get(name) ?? new Set()
    set.add(handler as (data: unknown) => void)
    handlers.set(name, set)
    return () => set.delete(handler as (data: unknown) => void)
  }
  const emit = <K extends keyof AppEvents>(name: K, data: AppEvents[K]) => {
    for (const h of [...(handlers.get(name) ?? [])]) h(data)
  }
  return { on, emit }
}

const base: QualityData = { enabled: true, state: 'scoring', active: true, done: 3, total: 40, rated: 3 }

describe('quality store', () => {
  test('Catalog.QualityStatus, then quality:status, reach the store with folder_id and passes as Go sent them', async () => {
    const bus = fakeBus()
    const first: QualityData = { ...base, folder_id: 'a', passes: [{ folder_id: 'a', done: 3, total: 40 }] }
    let asked = 0
    const store = createQualityStore({
      qualityStatus: () => {
        asked++
        return Promise.resolve(first)
      },
      on: bus.on,
    })
    store.start()
    await flush()
    expect(asked).toBe(1)
    expect(store.get().value as unknown).toEqual(first)

    const two: QualityData = {
      ...base,
      done: 9,
      total: 70,
      folder_id: 'a',
      passes: [
        { folder_id: 'a', done: 5, total: 40 },
        { folder_id: 'b', done: 4, total: 30 },
      ],
    }
    bus.emit('quality:status', two as AppEvents['quality:status'])
    expect(store.get().value as unknown).toEqual(two)
  })

  test('a counter tick is applied without a reload; a state change is reconciled', async () => {
    const bus = fakeBus()
    let asked = 0
    const store = createQualityStore({
      qualityStatus: () => {
        asked++
        return Promise.resolve(base)
      },
      on: bus.on,
    })
    store.start()
    await flush()
    bus.emit('quality:status', { ...base, done: 4, rated: 4 } as AppEvents['quality:status'])
    expect(store.get().value).toMatchObject({ state: 'scoring', done: 4, rated: 4 })
    bus.emit('quality:status', { ...base, state: 'idle', active: false } as AppEvents['quality:status'])
    expect(store.get().value).toMatchObject({ state: 'idle', active: false })
    expect(asked).toBe(1)
  })
})

// Catalog.QualityStatus whose answers wait: each call takes Go's snapshot
// when it is made and answers when the test says, so an event can arrive
// while a load is outstanding and the load can then answer with a
// snapshot older than that event.
function deferredStatus(initial: QualityData) {
  let server = initial
  const answers: Array<() => void> = []
  return {
    answers,
    setServer(next: QualityData) {
      server = next
    },
    qualityStatus: () => {
      const snap = structuredClone(server)
      return new Promise<QualityData>((resolve) => answers.push(() => resolve(snap)))
    },
  }
}

const emitQuality = (bus: ReturnType<typeof fakeBus>, q: QualityData) =>
  bus.emit('quality:status', q as AppEvents['quality:status'])

describe('quality store during a load', () => {
  test('a counters-only event is applied directly and asks for nothing more', async () => {
    const bus = fakeBus()
    const one: QualityData = {
      ...base,
      folder_id: 'b',
      passes: [
        { folder_id: 'a', done: 1, total: 40 },
        { folder_id: 'b', done: 2, total: 30 },
      ],
    }
    const go = deferredStatus(one)
    const store = createQualityStore({ qualityStatus: go.qualityStatus, on: bus.on })
    store.start()
    go.answers[0]()
    await flush()

    void store.reload()
    const tick: QualityData = {
      ...one,
      done: 4,
      total: 30,
      rated: 6,
      passes: [
        { folder_id: 'a', done: 2, total: 40 },
        { folder_id: 'b', done: 4, total: 30 },
      ],
    }
    go.setServer(tick)
    emitQuality(bus, tick)
    expect(store.get().value as unknown).toEqual(tick)
    go.answers[1]()
    await flush()
    // Progress alone never makes the store load again.
    expect(go.answers).toHaveLength(2)
  })

  test('a state change during a load is reconciled: one more load follows it', async () => {
    const bus = fakeBus()
    const go = deferredStatus(base)
    const store = createQualityStore({ qualityStatus: go.qualityStatus, on: bus.on })
    store.start()
    go.answers[0]()
    await flush()

    void store.reload()
    const idle: QualityData = { ...base, state: 'idle', active: false, done: 40, rated: 40 }
    go.setServer(idle)
    emitQuality(bus, idle)
    go.answers[1]()
    await flush()
    expect(go.answers).toHaveLength(3)
    go.answers[2]()
    await flush()
    expect(go.answers).toHaveLength(3)
    expect(store.get().value as unknown).toEqual(idle)
  })

  test('a pass starting during a load: the older answer does not win', async () => {
    const bus = fakeBus()
    const one: QualityData = { ...base, folder_id: 'a', passes: [{ folder_id: 'a', done: 3, total: 40 }] }
    const go = deferredStatus(one)
    const store = createQualityStore({ qualityStatus: go.qualityStatus, on: bus.on })
    store.start()
    go.answers[0]()
    await flush()

    void store.reload() // takes Go's snapshot before the pass starts
    const two: QualityData = {
      ...base,
      done: 0,
      total: 30,
      folder_id: 'b',
      passes: [
        { folder_id: 'a', done: 3, total: 40 },
        { folder_id: 'b', done: 0, total: 30 },
      ],
    }
    go.setServer(two)
    emitQuality(bus, two)
    go.answers[1]() // the older snapshot
    await flush()
    expect(go.answers).toHaveLength(3)
    go.answers[2]()
    await flush()
    expect(store.get().value as unknown).toEqual(two)
  })

  test('a pass ending during a load: the older answer does not win', async () => {
    const bus = fakeBus()
    // Two passes; folder_id names the latest begun (b), so only the list changes when a ends.
    const two: QualityData = {
      ...base,
      folder_id: 'b',
      passes: [
        { folder_id: 'a', done: 39, total: 40 },
        { folder_id: 'b', done: 3, total: 30 },
      ],
    }
    const go = deferredStatus(two)
    const store = createQualityStore({ qualityStatus: go.qualityStatus, on: bus.on })
    store.start()
    go.answers[0]()
    await flush()

    void store.reload() // takes Go's snapshot while both passes run
    const one: QualityData = { ...two, passes: [{ folder_id: 'b', done: 3, total: 30 }] }
    go.setServer(one)
    emitQuality(bus, one)
    go.answers[1]() // the older snapshot, still with a's pass
    await flush()
    expect(go.answers).toHaveLength(3)
    go.answers[2]()
    await flush()
    expect(store.get().value as unknown).toEqual(one)
  })
})

describe('quality store on window focus', () => {
  test('a missed final event is repaired when the window gains focus', async () => {
    const bus = fakeBus()
    const go = deferredStatus(base)
    const store = createQualityStore({ qualityStatus: go.qualityStatus, on: bus.on })
    store.start()
    go.answers[0]()
    await flush()
    expect(store.get().value).toMatchObject({ state: 'scoring', active: true })

    // The pass ends, and its final idle event never reaches the store.
    const idle: QualityData = { ...base, state: 'idle', active: false, done: 40, rated: 40 }
    go.setServer(idle)
    bus.emit('common:WindowFocus', undefined)
    expect(go.answers).toHaveLength(2)
    go.answers[1]()
    await flush()
    expect(store.get().value as unknown).toEqual(idle)
  })

  test('a focus during a load asks once more after it', async () => {
    const bus = fakeBus()
    const go = deferredStatus(base)
    const store = createQualityStore({ qualityStatus: go.qualityStatus, on: bus.on })
    store.start()
    bus.emit('common:WindowFocus', undefined)
    expect(go.answers).toHaveLength(1)
    go.answers[0]()
    await flush()
    expect(go.answers).toHaveLength(2)
  })
})
