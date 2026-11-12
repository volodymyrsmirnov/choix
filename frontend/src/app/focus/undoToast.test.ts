// The pick toast's Undo is bound to the folder session the pick was made in
// (shell/toasts.ts pushPickToast / undoWithToast, with the folder store as
// the session's UndoTarget): it undoes there from any route while that
// session is open, leaves when the session closes, and never reaches another
// folder. Driven through the real session, folder stores and toast list.
import { describe, expect, test } from 'bun:test'
import { createFolderStore, createFolderStores, type FolderStore } from '../folder/folderStore'
import type { FolderView, PickResult } from '../shell/api'
import type { AppEvents, On } from '../shell/events'
import { createFolderSession } from '../shell/folderSession'
import type { Route } from '../shell/router'
import { createToasts, pushPickToast, undoWithToast, UNDONE_TITLE } from '../shell/toasts'

const flush = async () => {
  for (let i = 0; i < 3; i++) await new Promise<void>((resolve) => setTimeout(resolve, 0))
}

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

type State = 'picked' | 'rejected' | 'unmarked'

/** Go with two folders, f1 (file 11) and f2 (file 21), each with its own marks and undo stack. */
function world() {
  const files: Record<string, number> = { f1: 11, f2: 21 }
  const marks: Record<string, Map<number, State>> = { f1: new Map(), f2: new Map() }
  const stacks: Record<string, { fileId: number; prev: State }[]> = { f1: [], f2: [] }
  const calls = { open: [] as string[], close: [] as string[], undo: [] as string[] }
  const view = (folderId: string): FolderView =>
    ({
      folder: folderId,
      picked: 0,
      total: 1,
      clusters: [
        {
          id: 1,
          device: 'Camera A',
          time: 'Mar 1 · 10:00',
          members: [
            {
              file_id: files[folderId],
              path: `IMG_${files[folderId]}.jpg`,
              content_hash: 'h',
              kind: 'photo',
              format: 'jpeg',
              picked: marks[folderId]!.get(files[folderId]!) === 'picked',
              rejected: marks[folderId]!.get(files[folderId]!) === 'rejected',
            },
          ],
        },
      ],
      devices: ['Camera A'],
      failed: 0,
      picks_dir: 'picks',
      picks_missing: 0,
    }) as unknown as FolderView
  const bus = fakeBus()
  const session = createFolderSession({
    open: (id) => {
      calls.open.push(id)
      return Promise.resolve(view(id))
    },
    close: (id) => {
      calls.close.push(id)
      return Promise.resolve()
    },
    on: bus.on,
    toLibrary: () => {},
    settingsToLibrary: () => {},
  })
  session.start()
  const stores = createFolderStores(session, (folderId) =>
    createFolderStore({
      folderId,
      session,
      on: bus.on,
      clusters: (id) => Promise.resolve(view(id)),
      ratings: () => Promise.resolve({}),
      setPick: (id, fileId, action) => {
        const cur = marks[id]!.get(fileId) ?? 'unmarked'
        const next: State = action === 'pick' ? 'picked' : action === 'reject' ? 'rejected' : 'unmarked'
        stacks[id]!.push({ fileId, prev: cur })
        marks[id]!.set(fileId, next)
        return Promise.resolve({ file_id: fileId, state: next } as PickResult)
      },
      undo: (id) => {
        calls.undo.push(id)
        const top = stacks[id]!.pop()
        if (!top) return Promise.reject(Object.assign(new Error('nothing'), { cause: { code: 'not_found', message: 'Nothing to undo' } }))
        marks[id]!.set(top.fileId, top.prev)
        return Promise.resolve({ file_id: top.fileId, state: top.prev } as PickResult)
      },
      recluster: () => Promise.resolve(),
      hideRejected: () => false,
    }),
  )
  const toasts = createToasts()
  const route = async (r: Route) => {
    session.route(r)
    await flush()
  }
  /** Opens `folderId`, starts its store and picks its file; returns the store and the pick toast's id. */
  const pickIn = async (folderId: string): Promise<{ store: FolderStore; id: string }> => {
    await route({ name: 'folder', folderId })
    const store = stores.get(folderId)
    store.start()
    await flush()
    await store.pick(files[folderId]!, 'pick')
    const id = pushPickToast(toasts, 'Picked', store)
    if (id === null) throw new Error('a live session shows its pick toast')
    return { store, id }
  }
  const titles = () => toasts.items().map((t) => t.title)
  return { files, marks, calls, stores, toasts, route, pickIn, titles }
}

describe('pick toast undo', () => {
  test("undo from settings undoes the originating folder's action", async () => {
    const w = world()
    const { store, id } = await w.pickIn('f1')
    await w.route({ name: 'focus', folderId: 'f1', clusterId: 1 })
    await w.route({ name: 'settings' })
    // The session stays open on Settings, and so does the toast.
    expect(w.titles()).toEqual(['Picked'])
    expect(w.toasts.items()[0]?.undo).toBe(true)
    w.toasts.undo(id)
    await flush()
    expect(w.calls.undo).toEqual(['f1'])
    expect(w.marks.f1!.get(11)).toBe('unmarked')
    expect(store.member(11)?.picked).toBe(false)
    expect(w.titles()).toEqual([UNDONE_TITLE])
    expect(w.calls.close).toEqual([])
  })

  test('the undo toast is dismissed when its folder session closes', async () => {
    const w = world()
    await w.pickIn('f1')
    expect(w.titles()).toEqual(['Picked'])
    await w.route({ name: 'library' })
    expect(w.calls.close).toEqual(['f1'])
    expect(w.titles()).toEqual([])
    // Opening another folder closes the session too.
    await w.pickIn('f1')
    expect(w.titles()).toEqual(['Picked'])
    await w.route({ name: 'folder', folderId: 'f2' })
    expect(w.titles()).toEqual([])
  })

  test("a late pick from a closed session leaves the live session's toast and its undo intact", async () => {
    const w = world()
    const first = await w.pickIn('f1')
    const second = await w.pickIn('f2')
    expect(w.titles()).toEqual(['Picked'])
    const live = w.toasts.items()[0]!
    // f1's pick completes late, after its session closed: it must not touch f2's toast.
    expect(pushPickToast(w.toasts, 'Rejected', first.store)).toBeNull()
    expect(w.toasts.items()).toEqual([live])
    expect(w.toasts.items()[0]).toBe(live)
    // f2's toast still undoes f2's pick.
    w.toasts.undo(second.id)
    await flush()
    expect(w.calls.undo).toEqual(['f2'])
    expect(w.marks.f2!.get(21)).toBe('unmarked')
    expect(w.titles()).toEqual([UNDONE_TITLE])
  })

  test("a late undo from a closed session leaves the live session's toast intact", async () => {
    const w = world()
    const first = await w.pickIn('f1')
    // f1's undo is in flight when f1's session closes and f2 takes a pick.
    let release!: () => void
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    const realUndo = first.store.undo.bind(first.store)
    first.store.undo = async () => {
      const r = await realUndo()
      await held
      return r
    }
    const undoing = undoWithToast(w.toasts, first.store)
    await flush()
    await w.pickIn('f2')
    const live = w.toasts.items()
    expect(live.map((t) => t.title)).toEqual(['Picked'])
    release()
    await undoing
    await flush()
    // No "Undone" for f1 in f2's toast's place; f2's toast keeps its Undo.
    expect(w.toasts.items()).toEqual(live)
    expect(w.toasts.items()[0]?.undo).toBe(true)
  })

  test('undo never acts on another folder', async () => {
    const w = world()
    const first = await w.pickIn('f1')
    // Another folder: f1's session closes, f2's opens and takes a pick of its own.
    const second = await w.pickIn('f2')
    expect(w.calls.open).toEqual(['f1', 'f2'])
    // The old toast is gone; its Undo, and f1's store, do nothing now.
    w.toasts.undo(first.id === second.id ? 'none' : first.id)
    await undoWithToast(w.toasts, first.store)
    await flush()
    expect(w.calls.undo).toEqual([])
    expect(w.marks.f2!.get(21)).toBe('picked')
    expect(w.marks.f1!.get(11)).toBe('picked')
    // f1 opened again is a new session: the first one's store stays closed.
    await w.route({ name: 'folder', folderId: 'f1' })
    expect(w.stores.get('f1')).not.toBe(first.store)
    await undoWithToast(w.toasts, first.store)
    expect(w.calls.undo).toEqual([])
    // f2's own toast was dismissed with its session; f2's undo went nowhere either.
    expect(w.marks.f2!.get(21)).toBe('picked')
  })
})
