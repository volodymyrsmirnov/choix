import { describe, expect, test } from 'bun:test'
import type { AppEvents, On } from './events'
import { createModelsStore } from './modelsStore'
import { createQualityStore } from './qualityStore'

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

describe('modelsStore', () => {
  test('models:progress replaces the entry of that kind', async () => {
    const bus = fakeBus()
    const store = createModelsStore({
      status: () =>
        Promise.resolve([
          { kind: 'clip', state: 'downloading', received: 10, size: 100 },
          { kind: 'quality', state: 'queued', received: 0, size: 0 },
        ]),
      on: bus.on,
    })
    store.start()
    await flush()
    const quality = store.get().value![1]

    bus.emit('models:progress', { kind: 'clip', state: 'downloading', received: 60, size: 100 })
    const after = store.get().value!
    expect(after).toEqual([
      { kind: 'clip', state: 'downloading', received: 60, size: 100 },
      { kind: 'quality', state: 'queued', received: 0, size: 0 },
    ])
    expect(after[1]).toBe(quality)

    bus.emit('models:progress', { kind: 'clip', state: 'failed', received: 60, size: 100, error: 'offline' })
    expect(store.get().value![0]).toEqual({ kind: 'clip', state: 'failed', received: 60, size: 100, error: 'offline' })
  })
})

describe('modelsStore during a load', () => {
  test('a status transition during a load reconciles; byte progress does not', async () => {
    const bus = fakeBus()
    const answers: Array<() => void> = []
    let server = [
      { kind: 'clip', state: 'downloading', received: 10, size: 100 },
      { kind: 'quality', state: 'queued', received: 0, size: 0 },
    ]
    const store = createModelsStore({
      status: () =>
        new Promise((resolve) => {
          answers.push(() => resolve(server.map((m) => ({ ...m }))))
        }),
      on: bus.on,
    })
    store.start()
    answers[0]()
    await flush()

    // Byte progress during a load: shown, no follow-up.
    void store.reload()
    bus.emit('models:progress', { kind: 'clip', state: 'downloading', received: 50, size: 100 })
    expect(store.get().value![0].received).toBe(50)
    answers[1]()
    await flush()
    expect(answers).toHaveLength(2)

    // A transition (clip ready) during a load: one follow-up reload.
    void store.reload()
    bus.emit('models:progress', { kind: 'clip', state: 'ready', received: 100, size: 100 })
    server = [
      { kind: 'clip', state: 'ready', received: 100, size: 100 },
      { kind: 'quality', state: 'queued', received: 0, size: 0 },
    ]
    answers[2]()
    await flush()
    expect(answers).toHaveLength(4)
    answers[3]()
    await flush()
    expect(answers).toHaveLength(4)
    expect(store.get().value![0]).toEqual({ kind: 'clip', state: 'ready', received: 100, size: 100 })
  })
})

describe('qualityStore', () => {
  test('quality:status replaces the snapshot', async () => {
    const bus = fakeBus()
    const base = { enabled: true, state: 'idle', active: false, received: 0, size: 0, done: 0, total: 0, rated: 0 }
    const store = createQualityStore({ quality: () => Promise.resolve(base), on: bus.on })
    store.start()
    await flush()
    expect(store.get().value).toEqual(base as never)
    bus.emit('quality:status', { ...base, state: 'scoring', active: true, done: 2, total: 9 })
    expect(store.get().value).toMatchObject({ state: 'scoring', active: true, done: 2, total: 9 })
  })

  test('a status change during a load reconciles; scoring progress does not', async () => {
    const bus = fakeBus()
    const base = { enabled: true, state: 'scoring', active: true, received: 0, size: 0, done: 1, total: 9, rated: 1 }
    const answers: Array<() => void> = []
    const store = createQualityStore({
      quality: () => new Promise((resolve) => answers.push(() => resolve({ ...base }))),
      on: bus.on,
    })
    store.start()
    answers[0]()
    await flush()
    void store.reload()
    bus.emit('quality:status', { ...base, done: 2, rated: 2 })
    answers[1]()
    await flush()
    expect(answers).toHaveLength(2)
    void store.reload()
    bus.emit('quality:status', { ...base, state: 'idle', active: false, done: 9, rated: 9 })
    answers[2]()
    await flush()
    expect(answers).toHaveLength(4)
  })
})
