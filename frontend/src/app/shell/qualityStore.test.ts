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
