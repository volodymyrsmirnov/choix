import { describe, expect, test } from 'bun:test'
import type { FolderSummary, ModelStatus, ScanProgress } from '../../design/types'
import type { AppEvents, On } from '../shell/events'
import { createScanStore } from '../shell/scanStore'
import type { StoreSnapshot } from '../shell/store'
import { createToasts } from '../shell/toasts'
import {
  createLibraryHandlers,
  createLibraryStore,
  libraryView,
  reportError,
  type FolderRowData,
  type LibraryHandlerDeps,
  type LibraryScreenKind,
} from './libraryStore'

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

/** A Library row as Library.List answers (invented folders only). */
const row = (id: string, name: string, over: Partial<FolderRowData> = {}): FolderRowData => ({
  id,
  name,
  parentPath: '~/Pictures',
  state: 'ready',
  month: '2026-05',
  firstCaptured: '2026-05-02T10:00:00Z',
  lastCaptured: '2026-05-03T10:00:00Z',
  photos: 12,
  videos: 0,
  clusters: 4,
  picked: 0,
  rejected: 0,
  previews: [],
  scan: null,
  added_at: 1_700_000_000,
  ...over,
})

const model = (kind: ModelStatus['kind'], state: ModelStatus['state']): ModelStatus => ({ kind, state, received: 0, size: 0 })

const ready = <T,>(value: T): StoreSnapshot<T> => ({ status: 'ready', value, error: null })
const loading = <T,>(): StoreSnapshot<T> => ({ status: 'loading', value: undefined, error: null })

const progress = (stage: ScanProgress['stage'], done: number, total: number): ScanProgress => ({
  stage,
  done,
  total,
  failed: 0,
  state: 'running',
})

describe('libraryStore', () => {
  test('library:changed reloads the list', async () => {
    const bus = fakeBus()
    let lists: FolderRowData[][] = [[row('a', 'Harbour walk')], [row('a', 'Harbour walk'), row('b', 'Orchard')]]
    let calls = 0
    const store = createLibraryStore({
      list: () => Promise.resolve(lists[Math.min(calls++, lists.length - 1)]!),
      on: bus.on,
      toasts: createToasts(),
    })
    store.start()
    await flush()
    expect(store.get().value?.map((f) => f.id)).toEqual(['a'])

    bus.emit('library:changed', { reason: 'added', folderId: 'b', name: 'Orchard' })
    await flush()
    expect(calls).toBe(2)
    expect(store.get().value?.map((f) => f.id)).toEqual(['a', 'b'])

    // Counts changed (a pick, a finished job): the rows carry them, so the list loads again too.
    lists = [[row('a', 'Harbour walk', { picked: 3 }), row('b', 'Orchard')]]
    calls = 0
    bus.emit('library:changed', { reason: 'stats', folderId: 'a' })
    await flush()
    expect(calls).toBe(1)
    expect(store.get().value?.[0]?.picked).toBe(3)
  })

  test('gone shows the removed toast once', async () => {
    const bus = fakeBus()
    const toasts = createToasts()
    const store = createLibraryStore({
      list: () => Promise.resolve([row('b', 'Orchard')]),
      on: bus.on,
      toasts,
    })
    store.start()
    await flush()

    bus.emit('library:changed', { reason: 'gone', folderId: 'a', name: 'Harbour walk' })
    // The same folder reported again (a second presence pass) replaces it in place.
    bus.emit('library:changed', { reason: 'gone', folderId: 'a', name: 'Harbour walk' })
    await flush()
    expect(toasts.items()).toHaveLength(1)
    expect(toasts.items()[0]).toMatchObject({
      tone: 'info',
      title: '“Harbour walk” left the Library',
      body: 'The folder was deleted or moved to the Trash in Finder.',
    })

    // The user's own removal (the native alert) says nothing.
    bus.emit('library:changed', { reason: 'removed', folderId: 'b', name: 'Orchard' })
    await flush()
    expect(toasts.items()).toHaveLength(1)
  })

  test('AddFolders existing result selects the row and shows the already-in-Library toast', async () => {
    const toasts = createToasts()
    const selected: (string | null)[] = []
    const h = createLibraryHandlers({
      library: {
        AddFolders: () => Promise.resolve([{ id: 'b', name: 'Orchard', existing: true, relation: 'same' }]),
      },
      system: { ShowFolderMenu: () => Promise.resolve(), OpenPicksFolder: () => Promise.resolve() },
      models: { Retry: () => Promise.resolve() },
      settings: { Save: () => Promise.resolve({ saved: true, reclustered: false }) },
      toasts,
      select: (id) => selected.push(id),
      folderName: () => undefined,
      onSaved: () => {},
      onError: (e) => {
        throw e
      },
    })
    await h.add()
    expect(selected).toEqual(['b'])
    expect(toasts.items()).toEqual([
      { id: expect.any(String), tone: 'info', title: '“Orchard” is already in the Library', body: 'Choix selected it for you.' },
    ])

    // A cancelled picker changes nothing.
    const h2 = createLibraryHandlers({
      library: { AddFolders: () => Promise.resolve([]) },
      system: { ShowFolderMenu: () => Promise.resolve(), OpenPicksFolder: () => Promise.resolve() },
      models: { Retry: () => Promise.resolve() },
      settings: { Save: () => Promise.resolve({ saved: true, reclustered: false }) },
      toasts,
      select: (id) => selected.push(id),
      folderName: () => undefined,
      onSaved: () => {},
      onError: () => {},
    })
    await h2.add()
    expect(selected).toEqual(['b'])
    expect(toasts.items()).toHaveLength(1)
  })

  test('welcome vs library decision', () => {
    const folders = [row('a', 'Harbour walk')]
    const clipDownloading = [model('clip', 'downloading'), model('quality', 'queued')]
    const allReady = [model('clip', 'ready'), model('quality', 'ready')]
    const oneOff = [model('clip', 'disabled'), model('quality', 'ready')]
    const failed = [model('clip', 'failed'), model('quality', 'ready')]
    const cases: [string, StoreSnapshot<FolderRowData[]>, StoreSnapshot<ModelStatus[]>, LibraryScreenKind][] = [
      ['list loading', loading(), ready(allReady), 'loading'],
      // Models not known yet: no drop zone that turns into the welcome panel a moment later.
      ['models loading', ready([]), loading(), 'loading'],
      ['no folders, CLIP downloading', ready([]), ready(clipDownloading), 'welcome'],
      ['no folders, a download failed', ready([]), ready(failed), 'welcome'],
      ['no folders, every enabled model ready', ready([]), ready(allReady), 'empty'],
      ['no folders, one model off, the other ready', ready([]), ready(oneOff), 'empty'],
      ['folders, CLIP downloading (compact banner)', ready(folders), ready(clipDownloading), 'timeline'],
      ['folders, all ready', ready(folders), ready(allReady), 'timeline'],
      // Models.Status failed: nothing to wait for, so the Library is not held on the spinner.
      ['no folders, models unknown after an error', ready([]), { status: 'error', value: undefined, error: new Error('x') }, 'empty'],
    ]
    for (const [name, list, models, screen] of cases) {
      const v = libraryView({ list, models, scan: {} })
      expect({ name, screen: v.screen }).toEqual({ name, screen })
      expect({ name, loading: v.folders === null }).toEqual({ name, loading: screen === 'loading' })
    }
  })

  test('scan row state follows scan:state', async () => {
    const bus = fakeBus()
    const rows = [row('a', 'Harbour walk'), row('b', 'Orchard')]
    const list = () => Promise.resolve(rows)
    const scan = createScanStore({ list, on: bus.on })
    const store = createLibraryStore({ list, on: bus.on, toasts: createToasts() })
    scan.start()
    store.start()
    await flush()
    const view = () =>
      libraryView({ list: store.get(), models: ready([model('clip', 'ready'), model('quality', 'ready')]), scan: scan.get().value })

    expect(view().folders?.map((f) => f.state)).toEqual(['ready', 'ready'])

    bus.emit('scan:state', { folderId: 'a', state: 'scanning' })
    // A job that has sent no progress yet is looking for photos (indeterminate).
    expect(view().folders?.[0]).toMatchObject({ state: 'scanning', scan: progress('discover', 0, 0) })

    bus.emit('scan:progress', { folderId: 'a', progress: progress('thumb', 5, 12), phase: 'running' })
    expect(view().folders?.[0]).toMatchObject({ state: 'scanning', scan: progress('thumb', 5, 12) })

    bus.emit('scan:state', { folderId: 'b', state: 'queued' })
    expect(view().folders?.[1]?.state).toBe('queued')

    bus.emit('scan:state', { folderId: 'a', state: 'error', error: 'Scan failed: the folder could not be read' })
    expect(view().folders?.[0]).toMatchObject({ state: 'error', error: 'Scan failed: the folder could not be read' })
    expect(view().folders?.[0]?.scan).toBeUndefined()

    bus.emit('scan:state', { folderId: 'a', state: 'ready' })
    expect(view().folders?.[0]?.state).toBe('ready')
    expect(view().folders?.[0]?.error).toBeUndefined()
  })

  test('onMenu passes coordinates to ShowFolderMenu', async () => {
    const calls: [string, number, number][] = []
    const h = createLibraryHandlers({
      library: { AddFolders: () => Promise.resolve([]) },
      system: {
        ShowFolderMenu: (id, x, y) => {
          calls.push([id, x, y])
          return Promise.resolve()
        },
        OpenPicksFolder: () => Promise.resolve(),
      },
      models: { Retry: () => Promise.resolve() },
      settings: { Save: () => Promise.resolve({ saved: true, reclustered: false }) },
      toasts: createToasts(),
      select: () => {},
      folderName: () => undefined,
      onSaved: () => {},
      onError: (e) => {
        throw e
      },
    })
    h.menu('a', { x: 412, y: 96 })
    // The replica's "close" (null) and a call without a position show nothing.
    h.menu(null)
    h.menu('a')
    // The ⋯ button's position comes from getBoundingClientRect (fractional);
    // Go takes whole window points (x, y int).
    h.menu('b', { x: 300.6, y: 140.2 })
    await flush()
    expect(calls).toEqual([
      ['a', 412, 96],
      ['b', 301, 140],
    ])
  })
})

/** Handler deps with every call answering, overridable per test. */
function handlerDeps(over: Partial<LibraryHandlerDeps> = {}): LibraryHandlerDeps {
  return {
    library: { AddFolders: () => Promise.resolve([]) },
    system: { ShowFolderMenu: () => Promise.resolve(), OpenPicksFolder: () => Promise.resolve() },
    models: { Retry: () => Promise.resolve() },
    settings: { Save: () => Promise.resolve({ saved: true, reclustered: false }) },
    toasts: createToasts(),
    select: () => {},
    folderName: () => undefined,
    onSaved: () => {},
    onError: (e) => {
      throw e
    },
    ...over,
  }
}

/** A rejected binding call carrying an app error, as Wails delivers it. */
const rejected = (code: string, message: string) => Promise.reject(Object.assign(new Error(message), { cause: { code, message } }))

describe('library handlers', () => {
  test('a new folder from AddFolders is selected without a toast', async () => {
    const toasts = createToasts()
    const selected: (string | null)[] = []
    const h = createLibraryHandlers(
      handlerDeps({
        library: {
          AddFolders: () =>
            Promise.resolve([
              { id: 'c', name: 'Dunes', existing: false },
              { id: 'b', name: 'Orchard', existing: true, relation: 'inside' },
            ]),
        },
        toasts,
        select: (id) => selected.push(id),
      }),
    )
    await h.add()
    expect(selected).toEqual(['c'])
    // Only the folder that was already there says so.
    expect(toasts.items().map((t) => t.title)).toEqual(['“Orchard” is already in the Library'])
  })

  test('AddFolders failing goes to onError', async () => {
    const errors: unknown[] = []
    const h = createLibraryHandlers(
      handlerDeps({ library: { AddFolders: () => rejected('internal', 'the picker failed') }, onError: (e) => errors.push(e) }),
    )
    await h.add()
    expect(errors).toHaveLength(1)
  })

  test('open picks on a read-only drive shows the read-only toast', async () => {
    const toasts = createToasts()
    const errors: unknown[] = []
    const h = createLibraryHandlers(
      handlerDeps({
        system: {
          ShowFolderMenu: () => Promise.resolve(),
          OpenPicksFolder: (id) => (id === 'card' ? rejected('read_only', 'Choix cannot write here') : rejected('offline', 'not mounted')),
        },
        toasts,
        folderName: (id) => (id === 'card' ? 'Card Z' : undefined),
        onError: (e) => errors.push(e),
      }),
    )
    await h.openPicks('card')
    expect(toasts.items()).toEqual([
      {
        id: expect.any(String),
        tone: 'warn',
        title: '“Card Z” is on a read-only drive',
        body: 'Choix can show and review it, but cannot copy picks into it.',
      },
    ])
    // Again: the same toast, replaced in place.
    await h.openPicks('card')
    expect(toasts.items()).toHaveLength(1)
    // Any other refusal is an ordinary error.
    await h.openPicks('other')
    expect(errors).toHaveLength(1)
    expect(toasts.items()).toHaveLength(1)
  })

  test('a model switch saves its setting', async () => {
    const saved: unknown[] = []
    const patches: unknown[] = []
    const h = createLibraryHandlers(
      handlerDeps({
        settings: {
          Save: (p) => {
            saved.push(p)
            return Promise.resolve({ saved: true, reclustered: false })
          },
        },
        onSaved: (p) => patches.push(p),
      }),
    )
    await h.toggleModel('clip', false)
    await h.toggleModel('quality', true)
    expect(saved).toEqual([{ visual_grouping: false }, { quality_rating: true }])
    expect(patches).toEqual([{ visual_grouping: false }, { quality_rating: true }])
  })

  test('a failed model switch reports the error and asks for the settings again', async () => {
    const errors: [unknown, string | undefined][] = []
    const patches: unknown[] = []
    const h = createLibraryHandlers(
      handlerDeps({
        settings: { Save: () => rejected('internal', 'the settings file could not be written') },
        onSaved: (p) => patches.push(p),
        onError: (e, title) => errors.push([e, title]),
      }),
    )
    await h.toggleModel('quality', false)
    expect(errors.map(([, title]) => title)).toEqual(['Save failed'])
    // The save may have happened before the failure (a re-cluster after it): reload.
    expect(patches).toEqual([null])
  })

  test('retry asks Go for the model kind', async () => {
    const kinds: string[] = []
    const h = createLibraryHandlers(
      handlerDeps({
        models: {
          Retry: (kind) => {
            kinds.push(kind)
            return Promise.resolve()
          },
        },
      }),
    )
    await h.retryModel('quality')
    expect(kinds).toEqual(['quality'])
  })
})

describe('reportError', () => {
  test('shows the design copy or Go’s message, and nothing while quitting', () => {
    const toasts = createToasts()
    const report = reportError(toasts)
    report(Object.assign(new Error('x'), { cause: { code: 'invalid', field: 'picks_dir', reason: 'absolute', message: 'go text' } }))
    report(Object.assign(new Error('x'), { cause: { code: 'internal', message: 'disk full' } }), 'Save failed')
    report(Object.assign(new Error('x'), { cause: { code: 'unavailable', message: 'quitting' } }))
    const items = toasts.items()
    expect(items).toHaveLength(2)
    expect(items[0]).toMatchObject({ tone: 'reject', title: 'Action failed' })
    expect(items[0]?.body).not.toBe('go text')
    expect(items[1]).toMatchObject({ tone: 'reject', title: 'Save failed', body: 'disk full' })
  })
})

describe('library view', () => {
  test('a folder that left the Library is reported to onLeft', async () => {
    const bus = fakeBus()
    const left: string[] = []
    const store = createLibraryStore({
      list: () => Promise.resolve([row('b', 'Orchard')]),
      on: bus.on,
      toasts: createToasts(),
      onLeft: (id) => left.push(id),
    })
    store.start()
    await flush()
    bus.emit('library:changed', { reason: 'gone', folderId: 'a', name: 'Harbour walk' })
    bus.emit('library:changed', { reason: 'removed', folderId: 'b', name: 'Orchard' })
    bus.emit('library:changed', { reason: 'offline', folderId: 'c' })
    expect(left).toEqual(['a', 'b'])
  })

  test('gone without a name uses the row’s name', async () => {
    const bus = fakeBus()
    const toasts = createToasts()
    const store = createLibraryStore({ list: () => Promise.resolve([row('a', 'Harbour walk')]), on: bus.on, toasts })
    store.start()
    await flush()
    bus.emit('library:changed', { reason: 'gone', folderId: 'a' })
    expect(toasts.items()[0]?.title).toBe('“Harbour walk” left the Library')
  })

  test('the view has no title-bar scan: each row shows its own scan, stopped run or several at once', () => {
    // The design draws no scan in the Library's title bar (no status shown twice).
    const rows = [row('a', 'Harbour walk', { state: 'error', error: 'Scan stopped. Rescan to finish it.' }), row('b', 'Orchard'), row('c', 'Card A')]
    const stopped = { ...progress('analyze', 9, 30), state: 'stopped' as const }
    const models = ready([model('clip', 'ready'), model('quality', 'ready')])
    const v = libraryView({
      list: ready(rows),
      models,
      scan: { a: { state: 'error', progress: stopped }, b: { state: 'scanning', progress: progress('thumb', 1, 4) }, c: { state: 'scanning', progress: null } },
    })
    expect('scan' in v).toBe(false)
    expect(v.folders?.[0]).toMatchObject({ state: 'error', error: 'Scan stopped. Rescan to finish it.' })
    expect(v.folders?.[1]).toMatchObject({ state: 'scanning', scan: progress('thumb', 1, 4) })
    expect(v.folders?.[2]).toMatchObject({ state: 'scanning', scan: progress('discover', 0, 0) })
    const loading = libraryView({ list: { status: 'loading', value: undefined, error: null }, models, scan: undefined })
    expect('scan' in loading).toBe(false)
  })

  test('rows without a scan map entry keep the state and progress Library.List gave', () => {
    const rows = [row('a', 'Harbour walk', { state: 'scanning', scan: progress('metadata', 3, 9) }), row('b', 'Orchard', { state: 'queued' })]
    const v = libraryView({ list: ready(rows), models: ready([]), scan: undefined })
    expect(v.folders?.[0]).toMatchObject({ state: 'scanning', scan: progress('metadata', 3, 9) })
    expect(v.folders?.[1]?.state).toBe('queued')
    expect(v.folders?.[1]?.scan).toBeUndefined()
    // An error text is only shown on a row in error.
    const stale = libraryView({
      list: ready([row('a', 'Harbour walk', { state: 'error', error: 'old' })]),
      models: ready([]),
      scan: { a: { state: 'ready', progress: null } },
    })
    expect(stale.folders?.[0]?.error).toBeUndefined()
  })

  test('each row’s added_at (library.db, unix seconds) reaches the screen as Library.List sent it', () => {
    const rows = [row('a', 'Harbour walk', { added_at: 1_700_000_111 }), row('b', 'Orchard', { state: 'scanning', added_at: 1_700_000_222 })]
    const v = libraryView({ list: ready(rows), models: ready([]), scan: { b: { state: 'error', progress: null } } })
    expect(v.folders?.map((f) => f.added_at)).toEqual([1_700_000_111, 1_700_000_222])
  })
})
