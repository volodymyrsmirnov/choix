// The prototype store: the current scenario's state, which its `render`
// reads and its callbacks write. That is what makes the prototype clickable.
// It is emptied whenever a state is opened, so every state starts from its
// own defaults.
//
// Conventions every scenario follows:
//   - a key that opens an overlay IS that overlay's id, and `false` closes it
//     (the prototype's Escape handler sets the top open overlay's key to
//     false);
//   - `toast(t)` appends to `toasts`; a toast's overlay id is `toast:<id>`,
//     so setting that key to false dismisses it (see toastsOf).
import type { ToastItem } from '../src/design/types'

export interface ProtoStore { state: Record<string, unknown>; set(patch: Record<string, unknown>): void; go(n: number): void; toast(t: ToastItem): void }

export interface StoreHost {
  snapshot(): Record<string, unknown>
  subscribe(listener: () => void): () => void
  set(patch: Record<string, unknown>): void
  toast(t: ToastItem): void
  reset(): void
}

/** A tiny external store (for useSyncExternalStore). Snapshots are immutable. */
export function createStoreHost(): StoreHost {
  let state: Record<string, unknown> = {}
  const listeners = new Set<() => void>()
  const emit = () => {
    for (const l of listeners) l()
  }
  return {
    snapshot: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    set(patch) {
      state = { ...state, ...patch }
      emit()
    },
    toast(t) {
      const list = Array.isArray(state.toasts) ? (state.toasts as ToastItem[]) : []
      state = { ...state, toasts: [...list.filter((x) => x.id !== t.id), t], [`toast:${t.id}`]: true }
      emit()
    },
    reset() {
      state = {}
      emit()
    },
  }
}

/**
 * The toasts a scenario shows: its seeded ones, then those added with
 * `toast()`, minus every toast whose `toast:<id>` key is false.
 */
export function toastsOf(state: Record<string, unknown>, seeded: ToastItem[] = []): ToastItem[] {
  const added = Array.isArray(state.toasts) ? (state.toasts as ToastItem[]) : []
  const ids = new Set(added.map((t) => t.id))
  return [...seeded.filter((t) => !ids.has(t.id)), ...added].filter((t) => state[`toast:${t.id}`] !== false)
}

/** A scenario-local value, or `dflt` until the scenario first sets it. */
export function read<T>(state: Record<string, unknown>, key: string, dflt: T): T {
  return key in state ? (state[key] as T) : dflt
}
