// The toast list and its timers (v1 Toaster.tsx's state, without the view:
// the design's Toaster is controlled, and each screen renders it with the
// list this keeps). v1's rules: a toast leaves after 4 s unless it says
// otherwise ("Undone" stays 1.5 s); a toast with a key replaces the one
// holding that key in place and restarts its timer (the pick toast, pushed
// on every pick). New here: a toast whose Undo was pressed stays until the
// undo has finished, then leaves, unless the undo put up its own toast in
// its place.
//
// The pick toast (pushPickToast) is bound to the folder session its pick was
// made in: its Undo undoes there (undoWithToast) from any route while that
// session is open — Folder, Focus, Settings — and the toast leaves as soon
// as the session closes, so it can never undo in another folder.
import type { ToastItem } from '../../design/types'
import { appError, errorText } from './errors'

/** v1's DEFAULT_DURATION. */
export const TOAST_MS = 4000
/** v1's "Undone" toast. */
export const UNDONE_MS = 1500
/** v1's UNDO_TOAST_KEY: the pick / reject toast and the "Undone" that replaces it share one place. */
export const PICK_TOAST_KEY = 'pick'

// v1's toast titles that more than one screen shows (Focus.tsx toggle and
// undo); the pick toast's own title is the design's actionTitle.
/** After an undo went through (UNDONE_MS, PICK_TOAST_KEY). */
export const UNDONE_TITLE = 'Undone'
/** A pick, reject or other action failed; the body says why. */
export const ACTION_FAILED_TITLE = 'Action failed'
/** An undo failed; the body says why. */
export const UNDO_FAILED_TITLE = 'Undo failed'

export interface ToastInput {
  tone: ToastItem['tone']
  title: string
  body?: string
  /** Replace the toast holding this key instead of adding one. */
  key?: string
  /** Milliseconds; TOAST_MS when absent. */
  duration?: number
  /** Shows Undo (⌘Z) on the toast and runs this when it is pressed. */
  onUndo?: () => unknown
}

export interface Toasts {
  /** The list as the Toaster shows it; a new array on every change. */
  items(): ToastItem[]
  /** Returns the toast's id (the replaced toast's id for a keyed toast). */
  push(t: ToastInput): string
  dismiss(id: string): void
  /** The toast's Undo was pressed. */
  undo(id: string): void
  /** For useSyncExternalStore. */
  subscribe(listener: () => void): () => void
}

interface Entry {
  input: ToastInput
  item: ToastItem
  timer: ReturnType<typeof setTimeout> | undefined
  undoing: boolean
}

export function createToasts(): Toasts {
  let entries: Entry[] = []
  let items: ToastItem[] = []
  let next = 1
  const listeners = new Set<() => void>()

  const publish = () => {
    items = entries.map((e) => e.item)
    for (const l of [...listeners]) l()
  }

  const remove = (entry: Entry) => {
    if (!entries.includes(entry)) return
    clearTimeout(entry.timer)
    entries = entries.filter((e) => e !== entry)
    publish()
  }

  const arm = (entry: Entry) => {
    clearTimeout(entry.timer)
    entry.timer = setTimeout(() => remove(entry), entry.input.duration ?? TOAST_MS)
  }

  const itemOf = (id: string, t: ToastInput): ToastItem => ({
    id,
    tone: t.tone,
    title: t.title,
    ...(t.body !== undefined ? { body: t.body } : {}),
    ...(t.onUndo ? { undo: true } : {}),
  })

  return {
    items: () => items,
    push(t) {
      const holder = t.key !== undefined ? entries.find((e) => e.input.key === t.key) : undefined
      const id = holder ? holder.item.id : `t${next++}`
      const entry: Entry = { input: t, item: itemOf(id, t), timer: undefined, undoing: false }
      if (holder) {
        clearTimeout(holder.timer)
        entries = entries.map((e) => (e === holder ? entry : e))
      } else {
        entries = [...entries, entry]
      }
      arm(entry)
      publish()
      return id
    },
    dismiss(id) {
      const entry = entries.find((e) => e.item.id === id)
      if (entry) remove(entry)
    },
    undo(id) {
      const entry = entries.find((e) => e.item.id === id)
      if (!entry?.input.onUndo || entry.undoing) return
      entry.undoing = true
      clearTimeout(entry.timer)
      entry.timer = undefined
      // The entry, not the id: an undo that pushes a toast with the same key
      // (its "Undone") replaces this entry, and that toast must stay.
      let done: unknown
      try {
        done = entry.input.onUndo()
      } catch {
        done = undefined
      }
      Promise.resolve(done)
        .catch(() => {}) // the undo reports its own failure
        .finally(() => remove(entry))
    },
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

/** One folder session's undo: the open folder's store (folder/folderStore.ts) is one. */
export interface UndoTarget {
  /** Catalog.Undo for this session's folder; rejects with the call's error. */
  undo(): Promise<unknown>
  /** The session is still the open one. */
  isOpen(): boolean
  /** Runs once when the session closes (at once if it has); returns unsubscribe. */
  onClose(listener: () => void): () => void
}

/**
 * Undo on `target` with v1's toasts: "Undone" in the pick toast's place, or
 * "Undo failed" with the reason. Does nothing once its session has closed.
 */
export function undoWithToast(toasts: Toasts, target: UndoTarget): Promise<void> {
  if (!target.isOpen()) return Promise.resolve()
  return target.undo().then(
    () => {
      // A session that closed meanwhile must not replace the current pick toast.
      if (target.isOpen()) toasts.push({ tone: 'pick', title: UNDONE_TITLE, key: PICK_TOAST_KEY, duration: UNDONE_MS })
    },
    (e: unknown) => {
      const err = appError(e)
      if (err.code === 'unavailable') return // quitting
      toasts.push({ tone: 'reject', title: UNDO_FAILED_TITLE, body: errorText(err) })
    },
  )
}

/**
 * v1's pick toast (`title`: the design's actionTitle) with Undo, bound to the
 * session the pick was made in; it is dismissed when that session closes.
 * Only a live session may put it up or replace it: a pick that completes
 * after its session closed shows nothing and leaves the current toast (and
 * its Undo) alone. Returns the toast's id, or null when nothing was shown.
 */
export function pushPickToast(toasts: Toasts, title: string, target: UndoTarget): string | null {
  if (!target.isOpen()) return null
  const id = toasts.push({ tone: 'pick', title, key: PICK_TOAST_KEY, onUndo: () => undoWithToast(toasts, target) })
  let perTarget = boundToasts.get(toasts)
  if (!perTarget) boundToasts.set(toasts, (perTarget = new WeakMap()))
  const ids = perTarget.get(target)
  if (ids) {
    ids.add(id)
    return id
  }
  const mine = new Set([id])
  perTarget.set(target, mine)
  // One listener per session: it dismisses every toast still bound to it.
  target.onClose(() => {
    for (const t of mine) toasts.dismiss(t)
    mine.clear()
  })
  return id
}

/** The pick toasts bound to each session, per toast list. */
const boundToasts = new WeakMap<Toasts, WeakMap<UndoTarget, Set<string>>>()
