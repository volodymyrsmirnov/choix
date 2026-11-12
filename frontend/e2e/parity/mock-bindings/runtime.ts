// What `@wailsio/runtime` is in the parity build (vite.config.ts, mode
// `parity`): an in-page event bus with the runtime's Events API, so the
// app's `Events.On` subscribers (src/app/shell/events.ts) hear what a parity
// case emits (control.ts `emit`) as they would hear Go. Nothing else of the
// runtime is provided: an app import of anything more fails the parity
// build, which is the signal to add it here.
//
// A subscriber gets the runtime's WailsEvent shape, `{ name, data }`, with
// `data` as emitted (the real runtime runs the bindings' event creators on
// it; the app reads plain fields, so plain data is what Go would send).

export interface WailsEvent {
  name: string
  data: unknown
  sender?: string
}

type Callback = (ev: WailsEvent) => void

const listeners = new Map<string, Set<Callback>>()

function subscribe(name: string, callback: Callback, times: number): () => void {
  let left = times
  const set = listeners.get(name) ?? new Set<Callback>()
  listeners.set(name, set)
  const wrapped: Callback = (ev) => {
    if (left > 0 && --left === 0) set.delete(wrapped)
    callback(ev)
  }
  set.add(wrapped)
  return () => {
    set.delete(wrapped)
  }
}

/** Delivers an event to its subscribers, in subscription order. */
export function dispatch(name: string, data: unknown): void {
  const set = listeners.get(name)
  if (!set) return
  const ev: WailsEvent = { name, data: data ?? null }
  for (const cb of [...set]) cb(ev)
}

export const Events = {
  /** Subscribes to `name`; returns unsubscribe. */
  On: (name: string, callback: Callback): (() => void) => subscribe(name, callback, -1),
  /** Subscribes to the next `name` only; returns unsubscribe. */
  Once: (name: string, callback: Callback): (() => void) => subscribe(name, callback, 1),
  /** Drops every subscriber of each name. */
  Off: (...names: string[]): void => {
    for (const n of names) listeners.delete(n)
  },
}
