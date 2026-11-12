import { describe, expect, test } from 'bun:test'
import { createStore, type Setter } from './store'

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

describe('createStore', () => {
  test('subscribes before loading', async () => {
    const calls: string[] = []
    const store = createStore(
      () => {
        calls.push('load')
        return Promise.resolve(1)
      },
      () => {
        calls.push('subscribe')
        return () => calls.push('unsubscribe')
      },
    )
    const stop = store.start()
    expect(calls).toEqual(['subscribe', 'load'])
    await flush()
    expect(store.get()).toMatchObject({ status: 'ready', value: 1 })
    stop()
    expect(calls).toEqual(['subscribe', 'load', 'unsubscribe'])
  })

  test('reload after a missed event yields the same value as applying it', async () => {
    // The server's state; an event says how it changed.
    let server: Record<string, number> = { a: 1, b: 2 }
    const patch = (prev: Record<string, number> | undefined) => ({ ...prev, a: 10 })

    let emitA: (() => void) | null = null
    const live = createStore(
      () => Promise.resolve({ ...server }),
      (set: Setter<Record<string, number>>) => {
        emitA = () => set(patch)
        return () => {}
      },
    )
    // This one never hears the event (a missed event).
    const missed = createStore(
      () => Promise.resolve({ ...server }),
      () => () => {},
    )
    live.start()
    missed.start()
    await flush()

    server = patch(server)
    emitA!()
    await missed.reload()

    expect(missed.get().value).toEqual(live.get().value)
    expect(live.get().value).toEqual({ a: 10, b: 2 })
  })

  test('an event during a load does not regress the loaded value', async () => {
    // The reviewer's reproduction: Go's snapshot says `idle`; a `scoring`
    // event emitted before that snapshot arrives while the load is in flight.
    type Quality = { state: string; active: boolean }
    let server: Quality = { state: 'idle', active: false }
    const loads: Array<() => void> = []
    let reconcile: Setter<Quality> = () => {}
    const store = createStore(
      () =>
        new Promise<Quality>((resolve) => {
          const answer = { ...server }
          loads.push(() => resolve(answer))
        }),
      (_set, _reload, r) => {
        reconcile = r
        return () => {}
      },
    )
    store.start()
    reconcile({ state: 'scoring', active: true })
    loads[0]()
    await flush()
    expect(store.get().value).toEqual({ state: 'idle', active: false })
    // The follow-up reload answers with the authoritative value again.
    expect(loads).toHaveLength(2)
    server = { state: 'idle', active: false }
    loads[1]()
    await flush()
    expect(store.get()).toMatchObject({ status: 'ready', value: { state: 'idle', active: false } })
  })

  test('events during a load trigger exactly one follow-up reload', async () => {
    // State changes (`reconcile`); transient progress is the next test.
    const loads: Array<(v: number) => void> = []
    let set: Setter<number> = () => {}
    const store = createStore(
      () => new Promise<number>((resolve) => loads.push(resolve)),
      (_set, _reload, reconcile) => {
        set = reconcile
        return () => {}
      },
    )
    store.start()
    expect(loads).toHaveLength(1)
    set(5)
    set(6)
    set(7)
    expect(store.get().value).toBe(7) // shown at once
    loads[0](1)
    await flush()
    expect(store.get().value).toBe(1)
    expect(loads).toHaveLength(2) // one follow-up, however many events

    set(8) // during the follow-up: at most one more
    set(9)
    loads[1](2)
    await flush()
    expect(loads).toHaveLength(3)
    loads[2](3)
    await flush()
    expect(loads).toHaveLength(3) // quiet: no more reloads
    expect(store.get().value).toBe(3)

    set(4) // no load in flight: a plain patch, no reload
    await flush()
    expect(store.get().value).toBe(4)
    expect(loads).toHaveLength(3)
  })

  test('only the latest load sets the value; a failed load keeps the last value', async () => {
    const answers: Array<(v: string) => void> = []
    const fails: Array<(e: unknown) => void> = []
    const store = createStore(
      () =>
        new Promise<string>((resolve, reject) => {
          answers.push(resolve)
          fails.push(reject)
        }),
      () => () => {},
    )
    store.start()
    void store.reload()
    answers[1]('new')
    answers[0]('old')
    await flush()
    expect(store.get()).toMatchObject({ status: 'ready', value: 'new' })

    void store.reload()
    fails[2](new Error('boom'))
    await flush()
    expect(store.get().status).toBe('error')
    expect(store.get().value).toBe('new')
  })

  test('progress alone during a load schedules no reload', async () => {
    const loads: Array<(v: number) => void> = []
    let set: Setter<number> = () => {}
    const store = createStore(
      () => new Promise<number>((resolve) => loads.push(resolve)),
      (s) => {
        set = s
        return () => {}
      },
    )
    store.start()
    set(5)
    set(6)
    expect(store.get().value).toBe(6) // shown at once
    loads[0](1)
    await flush()
    expect(loads).toHaveLength(1) // no follow-up
    expect(store.get().value).toBe(1) // the result replaces it as is
    set(7) // the next progress patches on top
    expect(store.get().value).toBe(7)
  })

  test('a state change during a load still triggers exactly one follow-up reload', async () => {
    const loads: Array<(v: number) => void> = []
    let set: Setter<number> = () => {}
    let reload: () => void = () => {}
    const store = createStore(
      () => new Promise<number>((resolve) => loads.push(resolve)),
      (s, r) => {
        set = s
        reload = r
        return () => {}
      },
    )
    store.start()
    reload() // e.g. library:changed: coalesced into one follow-up
    set(9) // progress among them changes nothing
    reload()
    expect(loads).toHaveLength(1)
    loads[0](1)
    await flush()
    expect(loads).toHaveLength(2)
    loads[1](2)
    await flush()
    expect(loads).toHaveLength(2)
    expect(store.get().value).toBe(2)

    reload() // no load in flight: loads at once
    expect(loads).toHaveLength(3)
  })
})
