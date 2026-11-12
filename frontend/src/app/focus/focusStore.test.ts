import { describe, expect, test } from 'bun:test'
import { MENUS } from '../../design/menus'
import { MAX_ZOOM, MIN_ZOOM, PEEP_ZOOM } from '../../design/screens/focus/props'
import { createFolderStore, type FolderStore } from '../folder/folderStore'
import { createActions } from '../shell/actions'
import type { FocusResponse as FocusResponseModel, FolderView, PickResult } from '../shell/api'
import { appError, errorText } from '../shell/errors'
import type { AppEvents, On } from '../shell/events'
import { createFolderSession, type SessionState } from '../shell/folderSession'
import { createKeyHandler } from '../shell/keys'
import type { Route } from '../shell/router'
import { focusActionHandlers } from './focusActions'
import { createFocusStore, type FocusStore, type FocusStoreDeps } from './focusStore'

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
const callError = (code: string, message: string) => Object.assign(new Error(code), { cause: { code, message } })

type State = 'picked' | 'rejected' | 'unmarked'
interface FakeFile {
  id: number
  cluster: number
  kind?: 'photo' | 'video'
  hash?: string
  quality?: number
}

/**
 * Go's catalog for one folder: the marks, the undo stack (per open folder,
 * as Go keeps it), Cluster, Clusters, SetPick, Undo and Ratings.
 */
function fakeGo(files: FakeFile[]) {
  const marks = new Map<number, State>()
  const stack: { fileId: number; prev: State }[] = []
  const calls = { cluster: [] as number[], clusters: 0, setPick: [] as string[], undo: [] as string[], ratings: 0 }
  const scores: Record<string, number> = {}
  /** The cluster ids in order, as the files say now (a rebuild renumbers them). */
  const clusterIdsOf = () => [...new Set(go.files.map((f) => f.cluster))]
  const member = (f: FakeFile) => ({
    file_id: f.id,
    path: `Trip/IMG_${f.id}.${f.kind === 'video' ? 'mov' : 'jpg'}`,
    content_hash: f.hash ?? `h${f.id}`,
    kind: f.kind ?? 'photo',
    format: f.kind === 'video' ? 'mov' : 'jpeg',
    picked: marks.get(f.id) === 'picked',
    rejected: marks.get(f.id) === 'rejected',
    ...(f.quality !== undefined ? { quality: f.quality } : {}),
  })
  const go = {
    files,
    marks,
    calls,
    scores,
    clusterError: null as unknown,
    /** Clusters that Cluster fails for (an internal error). */
    clusterFails: new Set<number>(),
    /** The next Cluster call fails with this (once). */
    clusterFailOnce: null as unknown,
    /** Cluster calls for these ids wait for their gate. */
    clusterGates: new Map<number, Deferred<void>>(),
    /** Clusters (the grid) fails with this while set. */
    clustersError: null as unknown,
    /** Called as Cluster is asked (a rebuild that lands between two calls). */
    beforeCluster: null as ((clusterId: number) => void) | null,
    /** hide_rejected_photos: Clusters leaves rejected photos out (Cluster does not). */
    hideRejected: false,
    clusterGate: null as Deferred<void> | null,
    setPickGate: null as Deferred<void> | null,
    async cluster(folderId: string, clusterId: number): Promise<FocusResponseModel> {
      expect(folderId).toBe('f1')
      calls.cluster.push(clusterId)
      go.beforeCluster?.(clusterId)
      if (go.clusterGate) await go.clusterGate.promise
      const own = go.clusterGates.get(clusterId)
      if (own) await own.promise
      if (go.clusterError) throw go.clusterError
      if (go.clusterFailOnce) {
        const e = go.clusterFailOnce
        go.clusterFailOnce = null
        throw e
      }
      if (go.clusterFails.has(clusterId)) throw callError('internal', `cluster ${clusterId} could not be read`)
      const clusterIds = clusterIdsOf()
      const i = clusterIds.indexOf(clusterId)
      if (i < 0) throw callError('not_found', `cluster ${clusterId} not found`)
      const mine = go.files.filter((f) => f.cluster === clusterId)
      return {
        cluster: { id: clusterId, device: 'Camera A', time: 'Mar 1 · 10:00', members: mine.map(member) },
        cluster_index: i + 1,
        cluster_count: clusterIds.length,
        exif: Object.fromEntries(mine.map((f) => [String(f.id), { camera: 'Camera A', gps_lat: null, gps_lon: null }])),
        next_cluster_id: clusterIds[i + 1] ?? 0,
        prev_cluster_id: clusterIds[i - 1] ?? 0,
      } as unknown as FocusResponseModel
    },
    clusters(folderId: string): Promise<FolderView> {
      expect(folderId).toBe('f1')
      calls.clusters++
      if (go.clustersError) return Promise.reject(go.clustersError)
      return Promise.resolve({
        folder: 'Trip',
        picked: [...marks.values()].filter((s) => s === 'picked').length,
        total: go.files.length,
        clusters: clusterIdsOf().map((id) => ({
          id,
          device: 'Camera A',
          time: 'Mar 1 · 10:00',
          members: go.files.filter((f) => f.cluster === id && !(go.hideRejected && marks.get(f.id) === 'rejected')).map(member),
        })),
        devices: ['Camera A'],
        failed: 0,
        picks_dir: 'picks',
        picks_missing: 0,
      } as unknown as FolderView)
    },
    async setPick(folderId: string, fileId: number, action: string): Promise<PickResult> {
      calls.setPick.push(`${folderId} ${action} ${fileId}`)
      if (go.setPickGate) await go.setPickGate.promise
      const cur = marks.get(fileId) ?? 'unmarked'
      const next: State =
        action === 'pick' ? 'picked' : action === 'reject' ? 'rejected' : action === 'unpick' && cur === 'picked' ? 'unmarked' : action === 'unreject' && cur === 'rejected' ? 'unmarked' : cur
      if (next !== cur) stack.push({ fileId, prev: cur })
      marks.set(fileId, next)
      return { file_id: fileId, state: next } as PickResult
    },
    undo(folderId: string): Promise<PickResult> {
      calls.undo.push(folderId)
      const top = stack.pop()
      if (!top) return Promise.reject(callError('not_found', 'nothing to undo'))
      marks.set(top.fileId, top.prev)
      return Promise.resolve({ file_id: top.fileId, state: top.prev } as PickResult)
    },
    ratings(): Promise<Record<string, number>> {
      calls.ratings++
      return Promise.resolve({ ...scores })
    },
  }
  return go
}

const OPEN: SessionState = { folderId: 'f1', status: 'open', view: null, error: null }

/** Files of cluster 1 (three photos), cluster 2 (one photo) and cluster 3 (a photo and a video). */
const FILES: FakeFile[] = [
  { id: 11, cluster: 1 },
  { id: 12, cluster: 1, quality: 6.1 },
  { id: 13, cluster: 1 },
  { id: 21, cluster: 2 },
  { id: 31, cluster: 3 },
  { id: 32, cluster: 3, kind: 'video' },
]

function setup(
  opts: {
    files?: FakeFile[]
    clusterId?: number
    startFileId?: number
    advance?: boolean
    session?: ReturnType<typeof fakeSession>
    showInfo?: boolean
  } = {},
) {
  const go = fakeGo(opts.files ?? FILES)
  const bus = fakeBus()
  const session = opts.session ?? fakeSession(OPEN)
  let advance = opts.advance ?? false
  const saved: boolean[] = []
  /** What the store asked the container to do with the route. */
  const routed: string[] = []
  const folder: FolderStore = createFolderStore({
    folderId: 'f1',
    session,
    on: bus.on,
    clusters: go.clusters,
    ratings: go.ratings,
    setPick: go.setPick,
    undo: go.undo,
    recluster: () => Promise.resolve(),
    hideRejected: () => false,
  })
  const deps: FocusStoreDeps = {
    folderId: 'f1',
    clusterId: opts.clusterId ?? 1,
    session,
    cluster: go.cluster,
    folder,
    on: bus.on,
    advanceOnAction: () => advance,
    prefs: { showInfo: () => opts.showInfo ?? false, setShowInfo: (v) => saved.push(v) },
    moved: (id) => routed.push(`moved ${id}`),
    lost: () => routed.push('lost'),
  }
  if (opts.startFileId !== undefined) deps.startFileId = opts.startFileId
  const store = createFocusStore(deps)
  return {
    /** Another Focus on the same folder store (Focus left and opened again). */
    reopen: (over: Partial<FocusStoreDeps> = {}) => createFocusStore({ ...deps, ...over }),
    go,
    bus,
    session,
    folder,
    store,
    saved,
    routed,
    setAdvance: (v: boolean) => {
      advance = v
    },
  }
}

async function started(s: { folder: FolderStore; store: FocusStore }) {
  const stops = [s.folder.start(), s.store.start()]
  await flush()
  await flush()
  return () => {
    for (const stop of stops) stop()
  }
}

const ids = (store: FocusStore) => store.get().data?.cluster.members.map((m) => m.file_id)
const activeId = (store: FocusStore) => store.get().data?.cluster.members[store.get().activeIndex]?.file_id
const markOfId = (store: FocusStore, id: number) => {
  const m = store.get().data?.cluster.members.find((x) => x.file_id === id)
  return m ? (m.picked ? 'picked' : m.rejected ? 'rejected' : 'unmarked') : undefined
}

describe('focusStore', () => {
  test('photo navigation wraps', async () => {
    const s = setup()
    await started(s)
    expect(ids(s.store)).toEqual([11, 12, 13])
    expect(s.store.get().activeIndex).toBe(0)
    s.store.step(1)
    s.store.step(1)
    expect(activeId(s.store)).toBe(13)
    s.store.step(1)
    expect(activeId(s.store)).toBe(11)
    s.store.step(-1)
    expect(activeId(s.store)).toBe(13)
    // In compare mode ←/→ move among the panes, also wrapping.
    s.store.select(0)
    s.store.compare(true)
    s.store.compareSelect(2)
    expect(s.store.get().compareIdx).toEqual([0, 2])
    s.store.step(1)
    expect(s.store.get().activeIndex).toBe(2)
    s.store.step(1)
    expect(s.store.get().activeIndex).toBe(0)
    s.store.step(-1)
    expect(s.store.get().activeIndex).toBe(2)
  })

  test('advance on action moves to the next unreviewed photo', async () => {
    const s = setup({ advance: true })
    await started(s)
    // Culling forward: each Pick or Reject moves on to the photo after it.
    await s.store.act(11, 'pick')
    expect(activeId(s.store)).toBe(12)
    expect(markOfId(s.store, 12)).toBe('unmarked')
    await s.store.mark('rejected')
    expect(activeId(s.store)).toBe(13)
    // From the last photo it wraps to the first (the Settings copy).
    await s.store.mark('picked')
    expect(activeId(s.store)).toBe(11)
    // Clearing a mark does not move.
    await s.store.mark('picked') // 11 was picked: this unpicks it
    expect(markOfId(s.store, 11)).toBe('unmarked')
    expect(activeId(s.store)).toBe(11)
    // Never while comparing: the panes are the user's working set.
    s.store.compare(true)
    await s.store.mark('picked')
    expect(s.store.get().activeIndex).toBe(0)
    s.store.compare(false)
    // Off in Settings: stays.
    s.setAdvance(false)
    await s.store.mark('rejected')
    expect(activeId(s.store)).toBe(11)
  })

  test('advance follows the Settings copy: the next photo, reviewed or not', async () => {
    const s = setup({ advance: true })
    s.go.marks.set(12, 'rejected')
    await started(s)
    await s.store.act(11, 'pick')
    // "Cycle to the next photo in the cluster": 12 is already reviewed and is still next.
    expect(activeId(s.store)).toBe(12)
  })

  test('advance does not move when the user moved on while the pick was in flight', async () => {
    const s = setup({ advance: true })
    await started(s)
    s.go.setPickGate = deferred()
    const pending = s.store.act(11, 'pick')
    s.store.step(1)
    s.store.step(1)
    s.go.setPickGate.resolve()
    await pending
    expect(activeId(s.store)).toBe(13)
  })

  test('busyFileIds covers the in-flight pick', async () => {
    const s = setup()
    await started(s)
    s.go.setPickGate = deferred()
    const pending = s.store.act(11, 'pick')
    await flush()
    expect(s.store.get().busyFileIds).toEqual([11])
    // The mark shows at once, here and on the folder's grid.
    expect(markOfId(s.store, 11)).toBe('picked')
    expect(s.folder.member(11)?.picked).toBe(true)
    // A second action on the same file waits for the first (v1).
    expect(await s.store.act(11, 'reject')).toBeNull()
    s.go.setPickGate.resolve()
    const outcome = await pending
    expect(outcome?.next).toEqual({ picked: true, rejected: false })
    expect(s.store.get().busyFileIds).toEqual([])
    expect(s.go.calls.setPick).toEqual(['f1 pick 11'])
  })

  test('a failed pick is rolled back and rethrown', async () => {
    const s = setup()
    await started(s)
    s.go.setPickGate = deferred()
    const pending = s.store.act(12, 'reject')
    expect(markOfId(s.store, 12)).toBe('rejected')
    s.go.setPickGate.reject(callError('offline', 'The folder is offline'))
    let err: unknown
    await pending.catch((e: unknown) => {
      err = e
    })
    expect(appError(err).code).toBe('offline')
    expect(markOfId(s.store, 12)).toBe('unmarked')
    expect(s.store.get().busyFileIds).toEqual([])
  })

  test('a load that lands during a pick keeps the pick', async () => {
    const s = setup()
    await started(s)
    s.go.setPickGate = deferred()
    const pending = s.store.act(11, 'pick')
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    expect(markOfId(s.store, 11)).toBe('picked')
    // A load that started before the pick finished and lands after it keeps Go's answer.
    s.go.clusterGate = deferred()
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    s.go.setPickGate.resolve()
    await pending
    s.go.marks.delete(11) // the load answers with the mark from before the pick
    s.go.clusterGate.resolve()
    await flush()
    await flush()
    expect(markOfId(s.store, 11)).toBe('picked')
  })

  test('a pick that completes after focus is reopened is shown as picked', async () => {
    const s = setup()
    await started(s)
    s.go.setPickGate = deferred()
    const pending = s.store.act(11, 'pick')
    await flush()
    // Leave Focus and open it again before Go has answered.
    const again = s.reopen()
    const stopAgain = again.start()
    await flush()
    expect(markOfId(again, 11)).toBe('picked') // in flight: shown at once
    expect(again.get().busyFileIds).toEqual([11])
    s.go.setPickGate.resolve()
    await pending
    await flush()
    expect(s.folder.member(11)?.picked).toBe(true)
    expect(markOfId(again, 11)).toBe('picked')
    expect(again.get().busyFileIds).toEqual([])
    // Also when the answer lands while the reopened Focus shows another cluster, and it comes back.
    s.go.setPickGate = deferred()
    const second = again.act(12, 'reject')
    await flush()
    again.open(2)
    await flush()
    s.go.setPickGate.resolve()
    await second
    again.open(1)
    await flush()
    expect(markOfId(again, 12)).toBe('rejected')
    stopAgain()
  })

  test('a mutation result survives a later cluster load', async () => {
    const s = setup()
    await started(s)
    // A load starts, then the Folder store applies a pick (the grid, or another Focus), then the load lands.
    s.go.clusterGate = deferred()
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await s.folder.pick(11, 'pick')
    await flush()
    expect(markOfId(s.store, 11)).toBe('picked')
    s.go.marks.delete(11) // the load read the mark from before the pick
    s.go.clusterGate.resolve()
    await flush()
    await flush()
    expect(markOfId(s.store, 11)).toBe('picked')
    // An undo through the Folder store reaches Focus too.
    await s.folder.undo()
    await flush()
    expect(markOfId(s.store, 11)).toBe('unmarked')
    // A load that starts after Go answered shows what Go says.
    s.go.marks.set(11, 'rejected')
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(markOfId(s.store, 11)).toBe('rejected')
  })

  test('cluster load error sets loadError', async () => {
    const s = setup()
    const err = callError('internal', 'The folder’s database could not be read: disk I/O error')
    s.go.clusterError = err
    await started(s)
    expect(s.store.get().data).toBeNull()
    expect(s.store.get().loadError).toBe(errorText(appError(err)))
    // Try Again loads it.
    s.go.clusterError = null
    s.store.retry()
    expect(s.store.get().loadError).toBeNull()
    await flush()
    expect(ids(s.store)).toEqual([11, 12, 13])
    expect(s.store.get().loadError).toBeNull()
    // A refresh of the cluster on screen that fails keeps the cluster.
    s.go.clusterError = err
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    expect(ids(s.store)).toEqual([11, 12, 13])
    expect(s.store.get().loadError).toBeNull()
  })

  test('opening a cluster that fails to load shows loadError instead of the previous cluster', async () => {
    const s = setup()
    await started(s)
    s.store.open(99)
    await flush()
    expect(s.store.get().data).toBeNull()
    expect(s.store.get().loadError).toBe('cluster 99 not found')
  })

  test('ratings come from the id map', async () => {
    const s = setup()
    await started(s)
    const before = s.store.get().data
    expect(s.store.get().ratings).toEqual({})
    s.go.scores['12'] = 7.5
    s.go.scores['13'] = 3.2
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'ratings' })
    await flush()
    await flush()
    expect(s.go.calls.ratings).toBe(1)
    expect(s.store.get().ratings).toEqual({ 12: 7.5, 13: 3.2 })
    // The members are not replaced (the hero's full image keeps loading).
    expect(s.store.get().data).toBe(before)
    expect(s.store.get().data?.cluster.members[1]?.quality).toBe(6.1)
  })

  test('undo works after returning from focus to the folder', async () => {
    const go = fakeGo(FILES)
    const bus = fakeBus()
    const sessionCalls = { open: [] as string[], close: [] as string[] }
    const session = createFolderSession({
      open: (id) => {
        sessionCalls.open.push(id)
        return go.clusters(id)
      },
      close: (id) => {
        sessionCalls.close.push(id)
        return Promise.resolve()
      },
      on: bus.on,
      toLibrary: () => {},
      settingsToLibrary: () => {},
    })
    const stopSession = session.start()
    const folder = createFolderStore({
      folderId: 'f1',
      session,
      on: bus.on,
      clusters: go.clusters,
      ratings: go.ratings,
      setPick: go.setPick,
      undo: go.undo,
      recluster: () => Promise.resolve(),
      hideRejected: () => false,
    })
    const route = (r: Route) => session.route(r)

    // The folder page.
    route({ name: 'folder', folderId: 'f1' })
    let stopFolder = folder.start()
    await flush()
    await flush()
    expect(folder.member(12)?.rejected).toBe(false)
    stopFolder()

    // Focus on cluster 1: reject photo 12.
    route({ name: 'focus', folderId: 'f1', clusterId: 1 })
    const focus = createFocusStore({
      folderId: 'f1',
      clusterId: 1,
      startFileId: 12,
      session,
      cluster: go.cluster,
      folder,
      on: bus.on,
      advanceOnAction: () => false,
      prefs: { showInfo: () => false, setShowInfo: () => {} },
      moved: () => {},
      lost: () => {},
    })
    stopFolder = folder.start()
    const stopFocus = focus.start()
    await flush()
    expect(activeId(focus)).toBe(12)
    await focus.mark('rejected')
    expect(markOfId(focus, 12)).toBe('rejected')
    stopFocus()
    stopFolder()

    // Back on the folder page: the grid shows the reject, and ⌘Z undoes it.
    route({ name: 'folder', folderId: 'f1' })
    stopFolder = folder.start()
    await flush()
    await flush()
    expect(folder.member(12)?.rejected).toBe(true)
    const r = await folder.undo()
    expect(r).toMatchObject({ file_id: 12, state: 'unmarked' })
    expect(go.calls.undo).toEqual(['f1'])
    expect(folder.member(12)?.rejected).toBe(false)
    // One session the whole way: opened once, never closed.
    expect(sessionCalls.open).toEqual(['f1'])
    expect(sessionCalls.close).toEqual([])
    stopFolder()
    stopSession()
  })

  test('undo in focus applies Go’s answer to the photo', async () => {
    const s = setup()
    await started(s)
    await s.store.act(13, 'pick')
    const r = await s.folder.undo()
    expect(r).toMatchObject({ file_id: 13, state: 'unmarked' })
    expect(markOfId(s.store, 13)).toBe('unmarked')
  })

  test('the clicked thumbnail is the first photo shown', async () => {
    const s = setup({ startFileId: 13 })
    await started(s)
    expect(activeId(s.store)).toBe(13)
    // A file that is not in the cluster: its first photo.
    s.store.open(3, 999)
    await flush()
    expect(activeId(s.store)).toBe(31)
  })

  test('waits for the session before asking for the cluster', async () => {
    const session = fakeSession({ folderId: null, status: 'closed', view: null, error: null })
    const s = setup({ session })
    const stop = await started(s)
    expect(s.go.calls.cluster).toEqual([])
    session.publish({ folderId: 'f1', status: 'opening', view: null, error: null })
    await flush()
    expect(s.go.calls.cluster).toEqual([])
    session.publish(OPEN)
    await flush()
    expect(s.go.calls.cluster).toEqual([1])
    expect(ids(s.store)).toEqual([11, 12, 13])
    stop()
  })

  test('an offline session loads nothing', async () => {
    const session = fakeSession({ folderId: 'f1', status: 'offline', view: null, error: null })
    const s = setup({ session })
    await started(s)
    expect(s.go.calls.cluster).toEqual([])
    expect(s.store.get().data).toBeNull()
    expect(s.store.get().loadError).toBeNull()
  })

  test('next and previous cluster follow the response, once it is the requested cluster', async () => {
    const s = setup({ clusterId: 2 })
    await started(s)
    expect(s.store.neighbour('next')).toBe(3)
    expect(s.store.neighbour('prev')).toBe(1)
    s.go.clusterGate = deferred()
    s.store.open(3)
    // Until cluster 3 has loaded, ↩ / ⇧↩ must not follow cluster 2's neighbours.
    expect(s.store.neighbour('next')).toBeNull()
    expect(s.store.neighbour('prev')).toBeNull()
    s.go.clusterGate.resolve()
    await flush()
    expect(s.store.neighbour('next')).toBeNull() // the last cluster
    expect(s.store.neighbour('prev')).toBe(2)
  })

  test('compare selection follows the design rules', async () => {
    const s = setup()
    await started(s)
    s.store.select(1)
    s.store.compare(true)
    // Entering: the photo on screen is the first and active pane.
    expect(s.store.get()).toMatchObject({ comparing: true, compareIdx: [1], activeIndex: 1 })
    s.store.compareSelect(0)
    s.store.compareSelect(2)
    expect(s.store.get().compareIdx).toEqual([1, 0, 2])
    // 1–4 choose a pane.
    s.store.pane(3)
    expect(s.store.get().activeIndex).toBe(2)
    s.store.pane(4) // no fourth pane
    expect(s.store.get().activeIndex).toBe(2)
    // Removing the active pane moves to the one that slid into its place.
    s.store.compareSelect(2)
    expect(s.store.get()).toMatchObject({ compareIdx: [1, 0], activeIndex: 0 })
    // Leaving and entering again keeps the selection.
    s.store.compare(false)
    expect(s.store.get().comparing).toBe(false)
    s.store.compare(true)
    expect(s.store.get().compareIdx).toEqual([1, 0])
    // Opening another cluster leaves compare (the indices meant this cluster).
    s.store.open(3)
    await flush()
    expect(s.store.get()).toMatchObject({ comparing: false, compareIdx: [], activeIndex: 0 })
  })

  test('zoom, pixel-peep and Esc', async () => {
    const s = setup()
    await started(s)
    expect(s.store.get().view).toEqual({ zoom: MIN_ZOOM, pan: { x: 0, y: 0 } })
    s.store.peep()
    expect(s.store.get().view.zoom).toBe(PEEP_ZOOM)
    s.store.setView({ zoom: PEEP_ZOOM, pan: { x: 40, y: -10 } })
    s.store.zoom(1)
    expect(s.store.get().view).toEqual({ zoom: PEEP_ZOOM * 1.5, pan: { x: 40, y: -10 } })
    for (let i = 0; i < 10; i++) s.store.zoom(1)
    expect(s.store.get().view.zoom).toBe(MAX_ZOOM)
    s.store.zoomFit()
    expect(s.store.get().view).toEqual({ zoom: MIN_ZOOM, pan: { x: 0, y: 0 } })
    s.store.peep()
    s.store.peep()
    expect(s.store.get().view.zoom).toBe(MIN_ZOOM)
    // Another photo starts at fit.
    s.store.peep()
    s.store.step(1)
    expect(s.store.get().view.zoom).toBe(MIN_ZOOM)
    // Esc: un-zoom first, then leave compare, then leave.
    s.store.compare(true)
    s.store.zoom(1)
    expect(s.store.escape()).toBe(true)
    expect(s.store.get().view.zoom).toBe(MIN_ZOOM)
    expect(s.store.get().comparing).toBe(true)
    // Compare panes share the zoom and keep it when the active pane changes.
    s.store.compareSelect(0)
    s.store.zoom(1)
    s.store.pane(2)
    expect(s.store.get().view.zoom).toBe(1.5)
    s.store.peep() // Pixel-peep is off while comparing
    expect(s.store.get().view.zoom).toBe(1.5)
    s.store.zoomFit()
    expect(s.store.escape()).toBe(true)
    expect(s.store.get().comparing).toBe(false)
    expect(s.store.escape()).toBe(false)
  })

  test('a video does not zoom', async () => {
    const s = setup({ clusterId: 3, startFileId: 32 })
    await started(s)
    expect(activeId(s.store)).toBe(32)
    s.store.zoom(1)
    s.store.peep()
    expect(s.store.get().view.zoom).toBe(MIN_ZOOM)
  })

  test('mediaState: photos load, videos are ready, an unchanged file keeps its state', async () => {
    const files: FakeFile[] = [
      { id: 31, cluster: 3 },
      { id: 32, cluster: 3, kind: 'video' },
      { id: 33, cluster: 3 },
    ]
    const s = setup({ files, clusterId: 3 })
    await started(s)
    expect(s.store.get().mediaState).toEqual({ 31: 'loading', 33: 'loading' })
    s.store.setMediaState(31, 'ready')
    s.store.setMediaState(33, 'error')
    expect(s.store.get().mediaState).toEqual({ 31: 'ready', 33: 'error' })
    // A refresh: 31 is the same file, 33 changed on disk (a new hash).
    s.go.files = [files[0]!, files[1]!, { id: 33, cluster: 3, hash: 'h33b' }]
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    expect(s.store.get().mediaState).toEqual({ 31: 'ready', 33: 'loading' })
  })

  test('a refresh keeps the photo on screen and the panes by file', async () => {
    const s = setup()
    await started(s)
    s.store.select(2)
    s.store.compare(true)
    s.store.compareSelect(0)
    expect(s.store.get().compareIdx).toEqual([2, 0])
    // A rescan put a new photo first.
    s.go.files = [{ id: 10, cluster: 1 }, ...FILES]
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    expect(ids(s.store)).toEqual([10, 11, 12, 13])
    expect(activeId(s.store)).toBe(13)
    expect(s.store.get().compareIdx).toEqual([3, 1])
  })

  test('a recluster that renumbers clusters keeps focus on the same photo', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    await started(s)
    expect(activeId(s.store)).toBe(12)
    s.store.peep()
    expect(s.store.get().view.zoom).toBe(PEEP_ZOOM)
    // The rebuild put a new cluster first: id 1 now names another cluster, and 11–13 are cluster 2.
    s.go.files = [
      { id: 21, cluster: 1 },
      { id: 11, cluster: 2 },
      { id: 12, cluster: 2 },
      { id: 13, cluster: 2 },
      { id: 31, cluster: 3 },
    ]
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(s.routed).toEqual(['moved 2'])
    expect(s.store.get().clusterId).toBe(2)
    expect(s.store.get().data?.cluster.id).toBe(2)
    expect(ids(s.store)).toEqual([11, 12, 13])
    expect(activeId(s.store)).toBe(12)
    expect(s.store.get().view).toEqual({ zoom: MIN_ZOOM, pan: { x: 0, y: 0 } })
    // ↩ / ⇧↩ follow the cluster it is in now.
    expect(s.store.neighbour('prev')).toBe(1)
    expect(s.store.neighbour('next')).toBe(3)
    // The route following the store (the container's open) asks for nothing more.
    const asked = s.go.calls.cluster.length
    s.store.open(2)
    await flush()
    expect(s.go.calls.cluster.length).toBe(asked)
    expect(activeId(s.store)).toBe(12)
  })

  test('a recluster that removes the active photo leaves focus for the folder', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    await started(s)
    // Deleted in a rescan that also renumbered: never another photo by index.
    s.go.files = [
      { id: 21, cluster: 1 },
      { id: 11, cluster: 2 },
      { id: 13, cluster: 2 },
    ]
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(s.routed).toEqual(['lost'])
    expect(s.store.get().clusterId).toBe(1)
    expect(activeId(s.store)).toBe(12)

  })

  test('a photo that is in no cluster any more leaves focus for the folder', async () => {
    // Gone from the same cluster, ids unchanged.
    const t = setup({ clusterId: 1, startFileId: 13 })
    await started(t)
    t.go.files = FILES.filter((f) => f.id !== 13)
    t.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(t.routed).toEqual(['lost'])
    expect(activeId(t.store)).toBe(13)

    // Gone with its whole cluster (the id is not found any more).
    const u = setup({ clusterId: 3, startFileId: 31 })
    await started(u)
    u.go.files = FILES.filter((f) => f.cluster !== 3)
    u.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(u.routed).toEqual(['lost'])
    expect(activeId(u.store)).toBe(31)
  })

  test('a rejected photo hidden from the grid stays in focus across a clusters event', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    s.go.hideRejected = true
    await started(s)
    await s.store.mark('rejected')
    expect(markOfId(s.store, 12)).toBe('rejected')
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    // The grid leaves it out; Catalog.Cluster still holds it: Focus stays on it (Undo is still at hand).
    expect(s.folder.member(12)).toBeUndefined()
    expect(s.routed).toEqual([])
    expect(s.go.calls.cluster).toEqual([1, 1])
    expect(activeId(s.store)).toBe(12)
    expect(markOfId(s.store, 12)).toBe('rejected')
  })

  test('a rejected hidden photo is followed to its renumbered cluster through its cluster-mates', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    s.go.hideRejected = true
    s.go.marks.set(12, 'rejected')
    await started(s)
    expect(activeId(s.store)).toBe(12)
    s.go.files = [
      { id: 21, cluster: 1 },
      { id: 11, cluster: 2 },
      { id: 12, cluster: 2 },
      { id: 13, cluster: 2 },
      { id: 31, cluster: 3 },
    ]
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(s.folder.member(12)).toBeUndefined()
    expect(s.routed).toEqual(['moved 2'])
    expect(s.go.calls.cluster).toEqual([1, 1, 2])
    expect(activeId(s.store)).toBe(12)
    expect(s.store.get().data?.cluster.id).toBe(2)
  })

  test('a failed folder refresh during a follow keeps the photo and the next event completes the follow', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    await started(s)
    // 12 moved to cluster 2 ...
    s.go.files = [
      { id: 21, cluster: 1 },
      { id: 11, cluster: 2 },
      { id: 12, cluster: 2 },
      { id: 13, cluster: 2 },
    ]
    // ... and, as cluster 2 is asked for, a second rebuild moves it to 3 (2 now holds an unrelated
    // photo) while the grid cannot be read.
    let rebuilt = false
    s.go.beforeCluster = (id) => {
      if (id !== 2 || rebuilt) return
      rebuilt = true
      s.go.files = [
        { id: 21, cluster: 1 },
        { id: 31, cluster: 2 },
        { id: 11, cluster: 3 },
        { id: 12, cluster: 3 },
        { id: 13, cluster: 3 },
      ]
      s.go.clustersError = callError('internal', 'the grid could not be read')
    }
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    // Unfinished: the photo stays on screen with its cluster; nothing by index.
    expect(rebuilt).toBe(true)
    expect(s.routed).toEqual([])
    expect(s.store.get()).toMatchObject({ clusterId: 1, loadError: null })
    expect(activeId(s.store)).toBe(12)
    // The grid can be read again: the next event completes the follow for the same photo.
    s.go.clustersError = null
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(s.routed).toEqual(['moved 3'])
    expect(s.store.get().clusterId).toBe(3)
    expect(ids(s.store)).toEqual([11, 12, 13])
    expect(activeId(s.store)).toBe(12)
  })

  test('a failed target-cluster load during a follow keeps the photo', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    await started(s)
    s.go.files = [
      { id: 21, cluster: 1 },
      { id: 31, cluster: 2 },
      { id: 11, cluster: 3 },
      { id: 12, cluster: 3 },
      { id: 13, cluster: 3 },
    ]
    s.go.clusterFails.add(3)
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(s.routed).toEqual([])
    expect(s.store.get()).toMatchObject({ clusterId: 1, loadError: null })
    expect(activeId(s.store)).toBe(12)
    expect(s.store.neighbour('next')).toBe(2) // the cluster on screen's, as loaded
    // The next refresh finishes the follow for the same photo.
    s.go.clusterFails.clear()
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(s.routed).toEqual(['moved 3'])
    expect(activeId(s.store)).toBe(12)
  })

  test('a recluster during the initial load still lands on the starting photo', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    s.go.clusterGate = deferred()
    const stop = await started(s)
    expect(s.store.get().data).toBeNull()
    // Rebuilt while Catalog.Cluster(1) is in flight: id 1 now names another cluster.
    s.go.files = [
      { id: 21, cluster: 1 },
      { id: 11, cluster: 2 },
      { id: 12, cluster: 2 },
      { id: 13, cluster: 2 },
    ]
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    s.go.clusterGate.resolve()
    await flush()
    await flush()
    await flush()
    expect(s.routed).toEqual(['moved 2'])
    expect(s.store.get().clusterId).toBe(2)
    expect(activeId(s.store)).toBe(12)
    // The refresh queued by the event keeps it.
    expect(s.go.calls.cluster.at(-1)).toBe(2)
    expect(activeId(s.store)).toBe(12)
    stop()

    // The same, but the grid cannot be read: nothing is shown in the photo's place; Try Again finds it.
    const u = setup({ clusterId: 1, startFileId: 12 })
    u.go.clusterGate = deferred()
    const stopU = await started(u)
    u.go.files = [
      { id: 21, cluster: 1 },
      { id: 11, cluster: 2 },
      { id: 12, cluster: 2 },
      { id: 13, cluster: 2 },
    ]
    u.go.clustersError = callError('internal', 'the grid could not be read')
    u.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    u.go.clusterGate.resolve()
    await flush()
    await flush()
    await flush()
    expect(u.store.get().data).toBeNull()
    expect(u.store.get().loadError).toBe('the grid could not be read')
    expect(u.routed).toEqual([])
    u.go.clustersError = null
    u.store.retry()
    await flush()
    await flush()
    expect(u.routed).toEqual(['moved 2'])
    expect(activeId(u.store)).toBe(12)
    stopU()

    // Without a change during the load, a starting photo not in the cluster still shows its first photo.
    const t = setup({ clusterId: 3, startFileId: 999 })
    await started(t)
    expect(activeId(t.store)).toBe(31)
    expect(t.routed).toEqual([])
  })

  test('compare keeps the panes that survived a recluster', async () => {
    const s = setup({ clusterId: 1, startFileId: 11 })
    await started(s)
    s.store.compare(true)
    s.store.compareSelect(1)
    s.store.compareSelect(2)
    expect(s.store.get()).toMatchObject({ comparing: true, compareIdx: [0, 1, 2], activeIndex: 0 })
    // Renumbered, a new photo first in 11's cluster, and 12 moved to another one.
    s.go.files = [
      { id: 21, cluster: 1 },
      { id: 10, cluster: 2 },
      { id: 11, cluster: 2 },
      { id: 13, cluster: 2 },
      { id: 12, cluster: 3 },
    ]
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(s.routed).toEqual(['moved 2'])
    expect(ids(s.store)).toEqual([10, 11, 13])
    expect(activeId(s.store)).toBe(11)
    // The panes that survived, by file, in pane order.
    expect(s.store.get()).toMatchObject({ comparing: true, compareIdx: [1, 2] })

    // 13 leaves too (cluster 2 keeps its id): one pane is not a comparison.
    s.go.files = [
      { id: 21, cluster: 1 },
      { id: 10, cluster: 2 },
      { id: 11, cluster: 2 },
      { id: 13, cluster: 3 },
      { id: 12, cluster: 3 },
    ]
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(s.routed).toEqual(['moved 2'])
    expect(ids(s.store)).toEqual([10, 11])
    expect(activeId(s.store)).toBe(11)
    expect(s.store.get()).toMatchObject({ comparing: false, compareIdx: [] })
  })

  /** 12 moved from cluster 1 (now another photo) to cluster 2. */
  const MOVED_TO_2: FakeFile[] = [
    { id: 21, cluster: 1 },
    { id: 11, cluster: 2 },
    { id: 12, cluster: 2 },
    { id: 13, cluster: 2 },
  ]

  test('a failed initial load after a recluster still lands on the starting photo when retried', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    s.go.clusterGate = deferred()
    await started(s)
    // Rebuilt while the first Catalog.Cluster(1) is in flight, and that call then fails once.
    s.go.files = MOVED_TO_2
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    s.go.clusterFailOnce = callError('internal', 'disk I/O error')
    s.go.clusterGate.resolve()
    await flush()
    await flush()
    await flush()
    // The refresh the event queued succeeds: the starting photo, not cluster 1's first.
    expect(s.go.calls.cluster.length).toBeGreaterThan(1)
    expect(s.routed).toEqual(['moved 2'])
    expect(activeId(s.store)).toBe(12)
  })

  test('a failed initial load after a recluster still lands on the starting photo through Try Again', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    s.go.clusterGate = deferred()
    await started(s)
    s.go.files = MOVED_TO_2
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    s.go.clusterError = callError('internal', 'disk I/O error')
    s.go.clusterGate.resolve()
    await flush()
    await flush()
    await flush()
    expect(s.store.get().data).toBeNull()
    expect(s.store.get().loadError).toBe('disk I/O error')
    s.go.clusterError = null
    s.store.retry()
    await flush()
    await flush()
    expect(s.routed).toEqual(['moved 2'])
    expect(activeId(s.store)).toBe(12)
  })

  test('a selection made during a follow is kept', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    await started(s)
    s.go.files = MOVED_TO_2
    const probe = deferred<void>()
    s.go.clusterGates.set(2, probe)
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    // The follow for 12 waits on Catalog.Cluster(2); the user moves on to 13 (still cluster 1 on screen).
    expect(s.go.calls.cluster.at(-1)).toBe(2)
    s.store.step(1)
    expect(activeId(s.store)).toBe(13)
    s.go.clusterGates.delete(2)
    probe.resolve()
    await flush()
    await flush()
    await flush()
    expect(s.routed).toEqual(['moved 2'])
    expect(s.store.get().data?.cluster.id).toBe(2)
    expect(activeId(s.store)).toBe(13)
  })

  test('navigating to another cluster during a follow is not undone by it', async () => {
    const s = setup({ clusterId: 1, startFileId: 12 })
    await started(s)
    // 12 moved to cluster 3; cluster 2 is another photo now.
    s.go.files = [
      { id: 21, cluster: 1 },
      { id: 31, cluster: 2 },
      { id: 11, cluster: 3 },
      { id: 12, cluster: 3 },
      { id: 13, cluster: 3 },
    ]
    const probe = deferred<void>()
    s.go.clusterGates.set(3, probe)
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'clusters' })
    await flush()
    await flush()
    expect(s.go.calls.cluster.at(-1)).toBe(3)
    // ↩ while the follow waits: the next cluster of the one on screen.
    const next = s.store.neighbour('next')
    expect(next).toBe(2)
    s.store.open(next!)
    await flush()
    expect(activeId(s.store)).toBe(31)
    s.go.clusterGates.delete(3)
    probe.resolve()
    await flush()
    await flush()
    expect(s.routed).toEqual([])
    expect(s.store.get().clusterId).toBe(2)
    expect(activeId(s.store)).toBe(31)
  })

  test('showInfo comes from and goes to the prefs', async () => {
    const s = setup({ showInfo: true })
    await started(s)
    expect(s.store.get().showInfo).toBe(true)
    s.store.toggleInfo()
    expect(s.store.get().showInfo).toBe(false)
    s.store.setInfo(true)
    expect(s.saved).toEqual([false, true])
  })

  test('a folder:changed for another folder is ignored', async () => {
    const s = setup()
    await started(s)
    s.bus.emit('folder:changed', { folderId: 'f2', reason: 'clusters' })
    await flush()
    expect(s.go.calls.cluster).toEqual([1])
    // Only clusters loads the cluster again (nothing sends another reason that changes it).
    s.bus.emit('folder:changed', { folderId: 'f1', reason: 'settings' })
    await flush()
    expect(s.go.calls.cluster).toEqual([1])
  })
})

/** A keydown without a DOM. */
function key(init: Partial<KeyboardEvent> & { key: string }): KeyboardEvent {
  const e = {
    code: '',
    metaKey: false,
    ctrlKey: false,
    altKey: false,
    shiftKey: false,
    repeat: false,
    isComposing: false,
    defaultPrevented: false,
    target: null,
    getModifierState: () => false,
    preventDefault() {
      e.defaultPrevented = true
    },
    ...init,
  }
  return e as unknown as KeyboardEvent
}

/** The shell's defaults (actions.ts): Focus leaves these to the shell. */
const SHELL_DEFAULTS = ['add-folder', 'open-folder', 'open-picks', 'reveal-folder', 'rescan-folder', 'remove-folder', 'github', 'settings', 'shortcuts', 'find']

describe('focus keys and menu', () => {
  /** The shell's real dispatcher and key table, with Focus's handlers registered as the container does. */
  async function wired(opts: Parameters<typeof setup>[0] = {}) {
    const s = setup(opts)
    await started(s)
    const nav: string[] = []
    const shellCalls: string[] = []
    const rec = (name: string) => () => {
      shellCalls.push(name)
      return Promise.resolve()
    }
    const actions = createActions({
      catalog: { Undo: rec('Catalog.Undo') },
      library: { AddFolders: rec('AddFolders'), Rescan: rec('Rescan'), Remove: rec('Remove') },
      system: { OpenPicksFolder: rec('OpenPicksFolder'), RevealFolder: rec('RevealFolder'), OpenHelp: rec('OpenHelp'), SetMenuContext: () => Promise.resolve() },
      router: { current: () => ({ name: 'focus', folderId: 'f1', clusterId: 1 }), navigate: () => true, back: () => nav.push('router.back') },
      toggleShortcuts: () => shellCalls.push('shortcuts'),
      document: { activeElement: null, execCommand: () => true },
      onError: (e) => {
        throw e
      },
    })
    const handlers = focusActionHandlers(s.store, {
      mark: (k) => s.store.mark(k).then(() => {}),
      undo: () => s.folder.undo().then(() => {}),
      toCluster: (id) => {
        if (id !== null) nav.push(`cluster ${id}`)
      },
      toFolder: () => nav.push('folder'),
    })
    for (const [id, h] of Object.entries(handlers)) actions.register(id, h)
    const handle = createKeyHandler({
      run: actions.runAction,
      screen: () => (s.store.get().comparing ? 'compare' : 'focus'),
      singleKeysNative: () => false,
    })
    const press = async (init: Partial<KeyboardEvent> & { key: string }) => {
      handle(key(init))
      await flush()
    }
    return { ...s, actions, handlers, press, nav, shellCalls }
  }

  test('every Photo, View and Edit item enabled in Focus or Compare has a handler', async () => {
    const w = await wired()
    const ids = MENUS.flatMap((m) =>
      m.items.flatMap((it) => ('id' in it && (it.enabledIn.includes('focus') || it.enabledIn.includes('compare')) ? [it.id] : [])),
    )
    expect(ids.length).toBeGreaterThan(15)
    for (const id of ids) expect(Object.hasOwn(w.handlers, id) || SHELL_DEFAULTS.includes(id)).toBe(true)
    // v1's keys that no menu item lists (the key table's webview bindings).
    for (const id of ['back', 'prev-photo', 'next-photo', 'zoom-in', 'zoom-out', 'zoom-fit', 'pane']) expect(Object.hasOwn(w.handlers, id)).toBe(true)
  })

  test('the Focus keys of the shortcut sheet', async () => {
    const w = await wired({ clusterId: 1 })
    const s = w.store
    // ← → ↑ ↓
    await w.press({ key: 'ArrowRight' })
    expect(activeId(s)).toBe(12)
    await w.press({ key: 'ArrowLeft' })
    expect(activeId(s)).toBe(11)
    await w.press({ key: 'ArrowDown' })
    expect(activeId(s)).toBe(12)
    await w.press({ key: 'ArrowUp' })
    expect(activeId(s)).toBe(11)
    // Space, X (again: un-marks)
    await w.press({ key: ' ', code: 'Space' })
    expect(markOfId(s, 11)).toBe('picked')
    await w.press({ key: ' ', code: 'Space' })
    expect(markOfId(s, 11)).toBe('unmarked')
    await w.press({ key: 'x', code: 'KeyX' })
    expect(markOfId(s, 11)).toBe('rejected')
    await w.press({ key: 'x', code: 'KeyX' })
    expect(markOfId(s, 11)).toBe('unmarked')
    // ⌘Z is the native menu's: the webview ignores the keydown; the menu's action undoes.
    await w.press({ key: 'x', code: 'KeyX' })
    await w.press({ key: 'z', code: 'KeyZ', metaKey: true })
    expect(markOfId(s, 11)).toBe('rejected')
    w.actions.runAction('undo')
    await flush()
    expect(markOfId(s, 11)).toBe('unmarked')
    // Z, + − 0
    await w.press({ key: 'z', code: 'KeyZ' })
    expect(s.get().view.zoom).toBe(PEEP_ZOOM)
    await w.press({ key: '+' })
    expect(s.get().view.zoom).toBe(PEEP_ZOOM * 1.5)
    await w.press({ key: '-' })
    expect(s.get().view.zoom).toBe(PEEP_ZOOM)
    await w.press({ key: '0' })
    expect(s.get().view.zoom).toBe(MIN_ZOOM)
    // ⌘+ ⌘− ⌘0 come from the native menu.
    await w.press({ key: '=', metaKey: true })
    expect(s.get().view.zoom).toBe(MIN_ZOOM)
    w.actions.runAction('zoom-in')
    expect(s.get().view.zoom).toBe(1.5)
    w.actions.runAction('zoom-out')
    expect(s.get().view.zoom).toBe(MIN_ZOOM)
    w.actions.runAction('zoom-in')
    w.actions.runAction('zoom-fit')
    expect(s.get().view.zoom).toBe(MIN_ZOOM)
    // I
    const info = s.get().showInfo
    await w.press({ key: 'i', code: 'KeyI' })
    expect(s.get().showInfo).toBe(!info)
    // ↩ ⇧↩ (cluster 1 is the first: no previous)
    await w.press({ key: 'Enter' })
    await w.press({ key: 'Enter', shiftKey: true })
    expect(w.nav).toEqual(['cluster 2'])
    // Esc (and ⌘[ from the menu): un-zoom, then leave compare, then back.
    await w.press({ key: 'z', code: 'KeyZ' })
    await w.press({ key: 'Escape' })
    expect(s.get().view.zoom).toBe(MIN_ZOOM)
    expect(w.nav).toEqual(['cluster 2'])
    await w.press({ key: 'c', code: 'KeyC' })
    expect(s.get().comparing).toBe(true)
    w.actions.runAction('back')
    expect(s.get().comparing).toBe(false)
    await w.press({ key: 'Escape' })
    expect(w.nav).toEqual(['cluster 2', 'folder'])
    // ? and ⌘, are the shell's.
    await w.press({ key: '?' })
    expect(w.shellCalls).toEqual(['shortcuts'])
  })

  test('the Compare keys of the shortcut sheet', async () => {
    const w = await wired({ clusterId: 1 })
    const s = w.store
    await w.press({ key: 'c', code: 'KeyC' })
    s.compareSelect(1)
    s.compareSelect(2)
    expect(s.get()).toMatchObject({ comparing: true, compareIdx: [0, 1, 2], activeIndex: 0 })
    // 1–4 choose a pane (also the physical digit row).
    await w.press({ key: '3', code: 'Digit3' })
    expect(s.get().activeIndex).toBe(2)
    await w.press({ key: '"', code: 'Digit2' })
    expect(s.get().activeIndex).toBe(1)
    // ← → among the panes, wrapping.
    await w.press({ key: 'ArrowRight' })
    expect(s.get().activeIndex).toBe(2)
    await w.press({ key: 'ArrowRight' })
    expect(s.get().activeIndex).toBe(0)
    await w.press({ key: 'ArrowLeft' })
    expect(s.get().activeIndex).toBe(2)
    // Space / X mark the active pane.
    await w.press({ key: ' ', code: 'Space' })
    expect(markOfId(s, 13)).toBe('picked')
    await w.press({ key: 'x', code: 'KeyX' })
    expect(markOfId(s, 13)).toBe('rejected')
    // Z and I are Focus's only (the menu disables them in Compare).
    const info = s.get().showInfo
    await w.press({ key: 'z', code: 'KeyZ' })
    await w.press({ key: 'i', code: 'KeyI' })
    expect(s.get().view.zoom).toBe(MIN_ZOOM)
    expect(s.get().showInfo).toBe(info)
    // + zooms every pane.
    await w.press({ key: '+' })
    expect(s.get().view.zoom).toBe(1.5)
    // C leaves.
    await w.press({ key: 'c', code: 'KeyC' })
    expect(s.get().comparing).toBe(false)
  })
})
