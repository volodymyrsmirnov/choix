import { describe, expect, test } from 'bun:test'
import type { ScanProgress } from '../../design/types'
import type { AppEvents, On } from './events'
import { createScanStore } from './scanStore'

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

const progress = (stage: ScanProgress['stage'], done: number, total: number): ScanProgress => ({
  stage,
  done,
  total,
  failed: 0,
  state: 'running',
})

async function setup() {
  const bus = fakeBus()
  let lists = 0
  const store = createScanStore({
    list: () => {
      lists++
      return Promise.resolve([
        { id: 'a', state: 'scanning', scan: progress('thumb', 3, 10) },
        { id: 'b', state: 'ready', scan: null },
      ])
    },
    on: bus.on,
  })
  store.start()
  await flush()
  return { bus, store, lists: () => lists }
}

describe('scanStore', () => {
  test('scan:progress updates only its folder', async () => {
    const { bus, store } = await setup()
    const before = store.get().value!
    expect(before.a).toEqual({ state: 'scanning', progress: progress('thumb', 3, 10) })

    bus.emit('scan:progress', { folderId: 'a', phase: 'running', progress: progress('analyze', 4, 10) })
    const after = store.get().value!
    expect(after.a.progress).toEqual(progress('analyze', 4, 10))
    expect(after.b).toBe(before.b)
  })

  test('scan:state ready clears progress', async () => {
    const { bus, store } = await setup()
    bus.emit('scan:state', { folderId: 'a', state: 'ready' })
    expect(store.get().value!.a).toEqual({ state: 'ready', progress: null })
  })

  test('library:changed reloads, except for stats', async () => {
    const { bus, lists } = await setup()
    bus.emit('library:changed', { reason: 'stats', folderId: 'a' })
    expect(lists()).toBe(1)
    bus.emit('library:changed', { reason: 'added', folderId: 'c' })
    expect(lists()).toBe(2)
  })

  test('progress alone during a load schedules no reload', async () => {
    const bus = fakeBus()
    const answers: Array<() => void> = []
    const store = createScanStore({
      list: () =>
        new Promise((resolve) => answers.push(() => resolve([{ id: 'a', state: 'scanning', scan: progress('thumb', 1, 10) }]))),
      on: bus.on,
    })
    store.start()
    answers[0]()
    await flush()
    void store.reload()
    for (let done = 2; done < 8; done++) {
      bus.emit('scan:progress', { folderId: 'a', phase: 'running', progress: progress('thumb', done, 10) })
    }
    expect(store.get().value!.a.progress).toEqual(progress('thumb', 7, 10))
    answers[1]()
    await flush()
    expect(answers).toHaveLength(2)
    // A state change during a load reconciles once.
    void store.reload()
    bus.emit('scan:state', { folderId: 'a', state: 'ready' })
    answers[2]()
    await flush()
    expect(answers).toHaveLength(4)
  })

  test('progress after a loaded ready state does not resurrect the scan', async () => {
    const bus = fakeBus()
    const store = createScanStore({
      list: () => Promise.resolve([{ id: 'a', state: 'ready', scan: null }]),
      on: bus.on,
    })
    store.start()
    await flush()
    const before = store.get().value
    bus.emit('scan:progress', { folderId: 'a', phase: 'running', progress: progress('cluster', 9, 10) })
    bus.emit('scan:progress', { folderId: 'zz', phase: 'running', progress: progress('thumb', 1, 10) })
    expect(store.get().value).toEqual({ a: { state: 'ready', progress: null } })
    expect(store.get().value!.a).toBe(before!.a)
  })
})
