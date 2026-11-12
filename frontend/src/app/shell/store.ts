// A value the app keeps in step with Go: loaded with a binding call, then
// kept current by events. start() subscribes first and loads second, so no
// event can fall between the two; every event either patches the value or
// reloads it, so an event that was missed anyway (the page reloaded, a
// listener came late) is repaired by the next reload. This replaces v1's
// SSE replay.
//
// A load result is authoritative for everything Go did before producing
// it, but the frontend cannot order an event against a load in flight. So
// the subscription says, at each call site, which kind of event it has:
//
//   set(patch)        TRANSIENT: continuous progress (bytes, counts) that
//                     the stream's next event supersedes within a moment.
//                     Applied at once; never makes an in-flight load stale.
//                     When the load lands its result replaces the value as
//                     is and the next progress event patches on top. Its
//                     patch function must not bring back what the value
//                     says is over (progress for a folder that is `ready`).
//   reconcile(patch)  STATE CHANGE carried by the event: applied at once;
//                     if a load is in flight, ONE follow-up reload runs
//                     after it (the event may be older or newer than the
//                     result).
//   reload()          STATE CHANGE the event does not carry (the list
//                     changed): loads now, or, if a load is in flight, once
//                     after it.
//
// Follow-ups coalesce: however many state changes arrive during a load,
// one reload follows it, and state changes during that reload allow at
// most one more. So the value always converges on Go's, and progress alone
// never makes the store load again. Only the latest load may set the value.
import { useSyncExternalStore } from 'react'

export type Updater<T> = (prev: T | undefined) => T
export type Setter<T> = (next: T | Updater<T>) => void

export interface StoreSnapshot<T> {
  /** `loading` until the first load answers; `error` when the latest load failed. */
  status: 'loading' | 'ready' | 'error'
  /** The last good value (kept across a failed reload); undefined before the first. */
  value: T | undefined
  /** What the latest load failed with; null otherwise. */
  error: unknown
}

export interface Store<T> {
  /** Subscribes, then loads. Returns stop, which unsubscribes and drops loads in flight. */
  start(): () => void
  /** Loads again now (superseding a load in flight); resolves once the value (or the error) is set. */
  reload(): Promise<void>
  /** Puts a value (the app's own write, e.g. the settings it saved); transient like `set` above. */
  set: Setter<T>
  get(): StoreSnapshot<T>
  /** For useSyncExternalStore. */
  subscribe(listener: () => void): () => void
}

export function createStore<T>(
  load: () => Promise<T>,
  subscribe: (set: Setter<T>, reload: () => void, reconcile: Setter<T>) => () => void,
): Store<T> {
  let snap: StoreSnapshot<T> = { status: 'loading', value: undefined, error: null }
  const listeners = new Set<() => void>()
  let gen = 0
  let inFlight = false
  /** A state change arrived while the latest load was in flight: reload once more after it. */
  let dirty = false
  let stopped = false

  const publish = (next: StoreSnapshot<T>) => {
    snap = next
    for (const l of [...listeners]) l()
  }

  const apply = (next: T | Updater<T>) => {
    const update: Updater<T> = typeof next === 'function' ? (next as Updater<T>) : () => next
    publish({ ...snap, value: update(snap.value) })
  }
  const set: Setter<T> = apply
  const reconcile: Setter<T> = (next) => {
    if (inFlight) dirty = true
    apply(next)
  }
  const requestReload = () => {
    if (inFlight) dirty = true
    else void reload()
  }

  const reload = async (): Promise<void> => {
    const mine = ++gen
    inFlight = true
    dirty = false
    try {
      const loaded = await load()
      if (mine !== gen) return
      inFlight = false
      publish({ status: 'ready', value: loaded, error: null })
    } catch (error) {
      if (mine !== gen) return
      inFlight = false
      publish({ ...snap, status: 'error', error })
    }
    // A state change came while that load was in flight: it may or may
    // not be in the result, so ask again (once, however many came).
    if (dirty && !stopped) {
      dirty = false
      void reload()
    }
  }

  return {
    start() {
      stopped = false
      const unsubscribe = subscribe(set, requestReload, reconcile)
      void reload()
      return () => {
        unsubscribe()
        stopped = true
        gen++
        inFlight = false
        dirty = false
      }
    },
    reload,
    set,
    get: () => snap,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

/** The store's snapshot, re-rendering on every change. */
export function useStore<T>(store: Store<T>): StoreSnapshot<T> {
  return useSyncExternalStore(store.subscribe, store.get)
}
