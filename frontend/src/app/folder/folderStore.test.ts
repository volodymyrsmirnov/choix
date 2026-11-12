import { describe, expect, test } from 'bun:test'
import { FIELD_COPY } from '../../design/screens/settings/validate'
import type { FolderView, PickResult } from '../shell/api'
import { appError, errorText } from '../shell/errors'
import type { AppEvents, On } from '../shell/events'
import type { SessionState } from '../shell/folderSession'
import { createFolderStore, createFolderStores, createSummaryStore, type FolderStoreDeps } from './folderStore'

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

function fakeSession(initial: SessionState) {
  let state = initial
  const listeners = new Set<() => void>()
  return {
    get: () => state,
    subscribe(l: () => void) {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    publish(next: SessionState) {
      state = next
      for (const l of [...listeners]) l()
    },
  }
}

interface Deferred<T> {
  promise: Promise<T>
  resolve(v: T): void
  reject(e: unknown): void
}
function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

/** A rejected binding call as Wails delivers it: the app error in `cause`. */
const callError = (code: string, extra: Record<string, string> = {}) =>
  Object.assign(new Error(code), { cause: { code, message: `${code} happened`, ...extra } })

const member = (file_id: number, over: Partial<FolderView['clusters'][number]['members'][number]> = {}) => ({
  file_id,
  path: `Trip/IMG_${file_id}.jpg`,
  content_hash: `h${file_id}`,
  kind: 'photo',
  format: 'jpeg',
  picked: false,
  rejected: false,
  ...over,
})

const view = (over: Partial<FolderView> = {}): FolderView => ({
  folder: 'Trip',
  picked: 0,
  total: 3,
  clusters: [
    { id: 1, device: 'Camera A', time: 'Mar 1, 10:00', members: [member(11), member(12, { quality: 6.1 })] },
    { id: 2, device: 'Camera B', time: 'Mar 1, 11:00', members: [member(21, { quality: 4 })] },
  ],
  devices: ['Camera A', 'Camera B'],
  failed: 0,
  picks_dir: 'picks',
  picks_missing: 0,
  ...over,
})

const OPEN = (v: FolderView | null = view()): SessionState => ({ folderId: 'f1', status: 'open', view: v, error: null })

function setup(
  opts: {
    session?: SessionState
    view?: FolderView
    /** Clusters() answers these in turn (the last one from then on). */
    views?: FolderView[]
    setPick?: FolderStoreDeps['setPick']
    ratings?: Record<string, number>
    undo?: PickResult
    hideRejected?: boolean
  } = {},
) {
  const bus = fakeBus()
  const session = fakeSession(opts.session ?? OPEN())
  const calls = { clusters: 0, ratings: 0, setPick: [] as string[], recluster: 0, undo: 0 }
  const store = createFolderStore({
    folderId: 'f1',
    session,
    on: bus.on,
    clusters: () => {
      calls.clusters++
      if (opts.views) return Promise.resolve(opts.views[Math.min(calls.clusters, opts.views.length) - 1])
      return Promise.resolve(opts.view ?? view())
    },
    ratings: () => {
      calls.ratings++
      return Promise.resolve(opts.ratings ?? {})
    },
    setPick:
      opts.setPick ??
      ((_f, fileId, action) => {
        calls.setPick.push(`${action} ${fileId}`)
        const state = action === 'pick' ? 'picked' : action === 'reject' ? 'rejected' : 'unmarked'
        return Promise.resolve({ file_id: fileId, state } as PickResult)
      }),
    undo: () => {
      calls.undo++
      return Promise.resolve(opts.undo ?? ({ file_id: 21, state: 'rejected' } as PickResult))
    },
    hideRejected: () => opts.hideRejected ?? false,
    recluster: () => {
      calls.recluster++
      return Promise.resolve()
    },
  })
  const stop = store.start()
  return { bus, session, calls, store, stop }
}

const memberOf = (store: ReturnType<typeof createFolderStore>, fileId: number) => store.member(fileId)

describe('folderStore', () => {
  test('optimistic pick is rolled back on error', async () => {
    const pending = deferred<PickResult>()
    const { store } = setup({ setPick: () => pending.promise })
    await flush()
    const before = memberOf(store, 11)
    expect(before).toMatchObject({ picked: false, rejected: false })

    const done = store.pick(11, 'pick')
    // Shown at once, and the file is busy while the call is in flight.
    expect(memberOf(store, 11)).toMatchObject({ picked: true, rejected: false })
    expect(store.get().busyFileIds).toEqual([11])

    pending.reject(callError('internal'))
    const err = await done.then(
      () => null,
      (e: unknown) => e,
    )
    expect(appError(err).message).toBe('internal happened')
    expect(memberOf(store, 11)).toMatchObject({ picked: false, rejected: false })
    expect(store.get().busyFileIds).toEqual([])
    expect(store.get().notice).toBeNull()
  })

  test('a pick that succeeds keeps the state Go answered', async () => {
    const { store, calls } = setup()
    await flush()
    const r = await store.pick(21, 'reject')
    expect(calls.setPick).toEqual(['reject 21'])
    expect(r).toMatchObject({ fileId: 21, prev: { picked: false, rejected: false }, next: { picked: false, rejected: true } })
    expect(memberOf(store, 21)).toMatchObject({ picked: false, rejected: true })
    // A second pick on a busy file is ignored (v1 mutate).
    const slow = deferred<PickResult>()
    const s2 = setup({ setPick: () => slow.promise })
    await flush()
    const first = s2.store.pick(11, 'pick')
    expect(await s2.store.pick(11, 'reject')).toBeNull()
    slow.resolve({ file_id: 11, state: 'picked' } as PickResult)
    await first
  })

  test('a load during a pick keeps the optimistic mark', async () => {
    const pending = deferred<PickResult>()
    const { store, bus } = setup({ setPick: () => pending.promise })
    await flush()
    const done = store.pick(11, 'pick')
    bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    expect(memberOf(store, 11)).toMatchObject({ picked: true })
    pending.resolve({ file_id: 11, state: 'picked' } as PickResult)
    await done
    expect(memberOf(store, 11)).toMatchObject({ picked: true })
  })

  test('folder:changed clusters refetches', async () => {
    const { calls, bus } = setup()
    await flush()
    // Mounted on an open folder: its view may be stale, so Clusters runs.
    expect(calls.clusters).toBe(1)
    bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    expect(calls.clusters).toBe(2)
    bus.emit('folder:changed', { folderId: 'other', reason: 'clusters' })
    bus.emit('folder:changed', { folderId: 'f1', reason: 'ratings' })
    await flush()
    expect(calls.clusters).toBe(2)
    expect(calls.ratings).toBe(1)
    // No other reason is sent (a settings save reaches the grid as clusters): one would load nothing.
    bus.emit('folder:changed', { folderId: 'f1', reason: 'settings' })
    await flush()
    expect(calls.clusters).toBe(2)
    expect(calls.ratings).toBe(1)
  })

  test('the first open uses the view Open returned', async () => {
    const opening: SessionState = { folderId: 'f1', status: 'opening', view: null, error: null }
    const { calls, session, store } = setup({ session: opening })
    await flush()
    expect(store.get().status).toBe('loading')
    session.publish(OPEN(view({ folder: 'Fresh' })))
    await flush()
    expect(calls.clusters).toBe(0)
    expect(store.get().value?.name).toBe('Fresh')
  })

  test('a reopened folder (moved, back online) takes the new view and clears the offline notice', async () => {
    const { session, store } = setup({ setPick: () => Promise.reject(callError('offline')) })
    await flush()
    await store.pick(11, 'pick').catch(() => {})
    expect(store.get().notice).toBe('offline')
    session.publish({ ...OPEN(), status: 'opening' })
    session.publish(OPEN(view({ folder: 'Moved' })))
    await flush()
    expect(store.get().value?.name).toBe('Moved')
    expect(store.get().notice).toBeNull()
  })

  test('ratings patch members without replacing them', async () => {
    const { store, bus, calls } = setup({ ratings: { '11': 7.5, '12': 6.1 } })
    await flush()
    const before = store.get().value!
    const [c1, c2] = before.clusters
    const m11 = c1.members[0]
    const m12 = c1.members[1]
    bus.emit('folder:changed', { folderId: 'f1', reason: 'ratings' })
    await flush()
    const after = store.get().value!
    expect(calls.clusters).toBe(1)
    // The member whose rating changed carries it, with everything else as it was.
    expect(after.clusters[0].members[0]).toEqual({ ...m11, quality: 7.5 })
    // Unchanged members keep their objects; a cluster with no change keeps its own.
    expect(after.clusters[0].members[1]).toBe(m12)
    // 21 lost its rating (absent from the map): only its cluster is new.
    expect(after.clusters[1]).not.toBe(c2)
    expect(after.clusters[1].members[0].quality).toBeUndefined()
    expect(store.get().ratings).toEqual({ 11: 7.5, 12: 6.1 })
    // Nothing changed: the same value.
    bus.emit('folder:changed', { folderId: 'f1', reason: 'ratings' })
    await flush()
    expect(store.get().value).toBe(after)
  })

  test("offline error sets notice 'offline'", async () => {
    const { store } = setup({ setPick: () => Promise.reject(callError('offline')) })
    await flush()
    await expect(store.pick(11, 'pick')).rejects.toBeDefined()
    expect(store.get().notice).toBe('offline')
    expect(memberOf(store, 11)).toMatchObject({ picked: false })
  })

  test("read_only error sets notice 'read-only'", async () => {
    const { store } = setup({ setPick: () => Promise.reject(callError('read_only')) })
    await flush()
    await expect(store.pick(12, 'reject')).rejects.toBeDefined()
    expect(store.get().notice).toBe('read-only')
    expect(memberOf(store, 12)).toMatchObject({ rejected: false })
  })

  test('the session going offline sets the notice', async () => {
    const { session, store } = setup()
    await flush()
    session.publish({ ...OPEN(), status: 'offline' })
    expect(store.get().notice).toBe('offline')
  })

  test('picks_blocked disables picking with the mapped copy', async () => {
    const { store, calls } = setup({ view: view({ picks_blocked: 'has_originals' }) })
    await flush()
    expect(store.get().pickBlocked).toBe(FIELD_COPY.picks_dir.has_originals)
    const err = await store.pick(11, 'pick').then(
      () => null,
      (e: unknown) => e,
    )
    expect(calls.setPick).toEqual([])
    expect(memberOf(store, 11)).toMatchObject({ picked: false })
    expect(errorText(appError(err))).toBe(FIELD_COPY.picks_dir.has_originals)
    // Only picking copies into the picks folder: a reject still goes through.
    await store.pick(11, 'reject')
    expect(calls.setPick).toEqual(['reject 11'])
  })

  test('an offline reload keeps the latest picks and ratings', async () => {
    const { store, session, bus, stop } = setup({ ratings: { '11': 7.5 } })
    await flush()
    await store.pick(11, 'pick')
    bus.emit('folder:changed', { folderId: 'f1', reason: 'ratings' })
    await flush()
    expect(memberOf(store, 11)).toMatchObject({ picked: true, quality: 7.5 })
    // The drive goes; the session keeps the view Open returned long ago.
    session.publish({ ...OPEN(), status: 'offline' })
    bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    expect(memberOf(store, 11)).toMatchObject({ picked: true, quality: 7.5 })
    // Leaving for Focus and coming back (a remount) while offline: the same.
    stop()
    store.start()
    await flush()
    expect(store.get().status).toBe('ready')
    expect(memberOf(store, 11)).toMatchObject({ picked: true, quality: 7.5 })
    expect(store.get().notice).toBe('offline')
  })

  test('an offline folder with nothing loaded yet shows the view Open returned', async () => {
    const { store } = setup({ session: { ...OPEN(view({ folder: 'Kept' })), status: 'offline' } })
    await flush()
    expect(store.get().value?.name).toBe('Kept')
    expect(store.get().notice).toBe('offline')
  })

  test('undo after returning from focus restores a hidden rejected photo', async () => {
    // hide_rejected_photos on: 21 was rejected in Focus, so the grid Go sends
    // leaves it (and its whole cluster) out; Undo brings it back.
    const hidden = view({ clusters: view().clusters.slice(0, 1) })
    const { store, calls, stop } = setup({ hideRejected: true, views: [view(), hidden, view()], undo: { file_id: 21, state: 'unmarked' } as PickResult })
    await flush()
    // To Focus and back: the remount loads the grid without 21.
    stop()
    store.start()
    await flush()
    expect(store.member(21)).toBeUndefined()
    expect(calls.clusters).toBe(2)
    await store.undo()
    expect(calls.clusters).toBe(3)
    expect(store.member(21)).toMatchObject({ picked: false, rejected: false })
    expect(store.cluster(2)).toBeDefined()
  })

  test('undo with the member shown and nothing hidden is a plain patch', async () => {
    const { store, calls } = setup()
    await flush()
    await store.undo()
    expect(calls.clusters).toBe(1)
    expect(memberOf(store, 21)).toMatchObject({ rejected: true })
  })

  test('undo applies the state Go answered', async () => {
    const { store, calls } = setup()
    await flush()
    const r = await store.undo()
    expect(calls.undo).toBe(1)
    expect(r).toMatchObject({ file_id: 21, state: 'rejected' })
    expect(memberOf(store, 21)).toMatchObject({ rejected: true })
  })

  test('recluster is busy while it runs, then reloads', async () => {
    const { store, calls } = setup()
    await flush()
    const done = store.recluster()
    expect(store.get().reclustering).toBe(true)
    await done
    expect(store.get().reclustering).toBe(false)
    expect(calls.recluster).toBe(1)
    expect(calls.clusters).toBe(2)
  })

  test('accessors find clusters and members', async () => {
    const { store } = setup()
    await flush()
    expect(store.clusters()?.map((c) => c.id)).toEqual([1, 2])
    expect(store.cluster(2)?.device).toBe('Camera B')
    expect(store.member(12)?.quality).toBe(6.1)
    expect(store.member(99)).toBeUndefined()
  })

  test('an open that failed ends in error with its reason', async () => {
    const opening: SessionState = { folderId: 'f1', status: 'opening', view: null, error: null }
    const { session, store } = setup({ session: opening })
    session.publish({ folderId: 'f1', status: 'error', view: null, error: { code: 'access_lost', message: 'lost' } })
    await flush()
    expect(store.get().status).toBe('error')
    expect(appError(store.get().error).code).toBe('access_lost')
  })
})

describe('createFolderStores', () => {
  test('one store per open folder, dropped when the session leaves it', () => {
    const session = fakeSession(OPEN())
    let made = 0
    const stores = createFolderStores(session, (folderId) => {
      made++
      return createFolderStore({
        folderId,
        session,
        on: fakeBus().on,
        clusters: () => Promise.resolve(view()),
        ratings: () => Promise.resolve({}),
        setPick: () => Promise.reject(new Error('unused')),
        undo: () => Promise.reject(new Error('unused')),
        recluster: () => Promise.resolve(),
        hideRejected: () => false,
      })
    })
    const a = stores.get('f1')
    expect(stores.get('f1')).toBe(a)
    session.publish({ folderId: null, status: 'closed', view: null, error: null })
    expect(stores.get('f1')).not.toBe(a)
    expect(made).toBe(2)
  })
})

describe('createSummaryStore', () => {
  test('follows library:changed for its folder', async () => {
    const bus = fakeBus()
    let lists = 0
    const store = createSummaryStore({
      folderId: 'f1',
      on: bus.on,
      list: () => {
        lists++
        return Promise.resolve([
          { id: 'f0', name: 'Other' },
          { id: 'f1', name: 'Trip', picked: lists },
        ] as never)
      },
    })
    store.start()
    await flush()
    expect(store.get().value).toMatchObject({ name: 'Trip', picked: 1 })
    bus.emit('library:changed', { reason: 'stats', folderId: 'f1' })
    await flush()
    expect(store.get().value).toMatchObject({ picked: 2 })
    bus.emit('library:changed', { reason: 'stats', folderId: 'f0' })
    await flush()
    expect(lists).toBe(2)
  })
})
