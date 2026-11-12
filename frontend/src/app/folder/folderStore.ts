// The open folder's cluster grid, its pick state and the Folder page's own
// status (notice, re-cluster in flight, files with a pick in flight). The
// Folder container renders it; the Focus container (Task 58) uses the same
// instance (createFolderStores) for its picks, so an optimistic mark made in
// Focus is what the grid shows on the way back.
//
// The data part is a shell store (../shell/store.ts) whose load is
// Catalog.Clusters. The folder session owns Open and Close: this store never
// calls them. Its first load waits for the session to settle and, when the
// session has just opened the folder, takes the view Open returned instead of
// asking again; a view the session already held when the store was made may
// be stale (picks made in Focus), so then Clusters runs. A reopen (`moved`,
// back `online`) is a state change carried by the session: reconciled.
//
// Events (store.ts kinds):
//   folder:changed clusters   reload (Clusters)
//   folder:changed ratings    Ratings(), then reconcile: only the members
//                             whose rating moved get a new object; the
//                             others, and every cluster without such a
//                             member, keep theirs
//
// Undo patches the member when it is shown and nothing can hide it; when
// the undone file is not in the grid, or hide_rejected_photos may add or
// remove it, the grid is fetched again (its membership changed).
//
// Offline, a reload keeps the value on screen (the latest picks, ratings
// and clusters); the session's Open view is used only when nothing was
// loaded yet.
//
// Picks are optimistic: the mark shows at once (also over any load that lands
// meanwhile), Go's answer replaces it, an error rolls it back and rejects
// with the call's error. Every pick and undo of the open folder goes through
// here, and the store keeps the latest mark of each file it touched (`marks`,
// with a sequence number): the one record of what the session changed, which
// Focus lays over its own cluster. A load overlays a mark that was in flight
// or arrived after the load began, so an answer older than the mutation
// never undoes it on screen.
//
// The store belongs to one folder session (UndoTarget, shell/toasts.ts): it
// is open from the moment the session has its folder until the session
// leaves it (Library, another folder, gone), and then closed for good; a
// reopened folder gets a new store. A pick toast's Undo calls this store's
// undo only while it is open, wherever the user is. An `offline` error sets the page's offline notice,
// `read_only` the read-only notice. A folder whose picks folder cannot be used
// (`picks_blocked`) refuses `pick` without a call, rejecting with the invalid
// picks_dir error the shell's errorText turns into the design's copy.
import type { PickAction } from '../../design/screens/focus/props'
import type { Cluster, FolderSummary, Member } from '../../design/types'
import type { FolderSummaryModel, FolderView, PickResult } from '../shell/api'
import { appError, fieldCopy } from '../shell/errors'
import type { On } from '../shell/events'
import type { SessionState } from '../shell/folderSession'
import { createStore, type Setter, type Store, type StoreSnapshot } from '../shell/store'
import type { UndoTarget } from '../shell/toasts'

export type { PickAction }

/** The cluster grid in the design's shapes. */
export interface FolderData {
  /** The folder's name (FolderView.folder). */
  name: string
  picked: number
  /** Members shown (rejected ones left out when hide_rejected_photos is on). */
  total: number
  clusters: Cluster[]
  devices: string[]
  failed: number
  picksDir: string
  /** Why the picks folder cannot be used (a picks_dir reason code); null when it can. */
  picksBlocked: string | null
  picksMissing: number
}

export type FolderNotice = 'offline' | 'read-only' | null

export interface FolderSnapshot extends StoreSnapshot<FolderData> {
  /** The page's notice: the session is offline, or a call answered offline / read_only. */
  notice: FolderNotice
  reclustering: boolean
  /** Files with a pick in flight. */
  busyFileIds: number[]
  /** The latest Ratings() answer by file id (empty until one arrived). */
  ratings: Record<number, number>
  /** The design's copy for why picking is unavailable; null when it is available. */
  pickBlocked: string | null
  /** The latest mark of every file a pick or undo of this session touched (a new map on every change). */
  marks: ReadonlyMap<number, MarkEntry>
}

/** One file's latest mark: in flight (optimistic), or Go's answer, numbered in the order it arrived. */
export interface MarkEntry {
  mark: Mark
  /** Order of arrival among this store's answers (markSeq); 0 for a mark still in flight. */
  seq: number
  pending: boolean
}

/**
 * Whether `e` belongs over a load that began when markSeq() was `since`:
 * still in flight, or Go answered after the load began (the load may predate it).
 */
export const markApplies = (e: MarkEntry, since: number): boolean => e.pending || e.seq > since

export interface Mark {
  picked: boolean
  rejected: boolean
}

/** A pick that went through: the file's mark before and after. */
export interface PickOutcome {
  fileId: number
  prev: Mark
  next: Mark
  /** Go's answer. */
  result: PickResult
}

/** The filter, sort and active cluster the Folder page last showed (v1 kept them for the page lifetime). */
export interface FolderMemory {
  filter: { query: string; device: string; sort: 'time-asc' | 'time-desc' }
  selectedClusterId: number | null
  /** The page was shown before on this store: restore the memory and the router's scroll. */
  visited: boolean
}

export interface FolderStoreDeps {
  folderId: string
  session: { get(): SessionState; subscribe(listener: () => void): () => void }
  clusters(folderId: string): PromiseLike<FolderView>
  ratings(folderId: string): PromiseLike<Record<string, number | undefined>>
  setPick(folderId: string, fileId: number, action: PickAction): PromiseLike<PickResult>
  undo(folderId: string): PromiseLike<PickResult>
  recluster(folderId: string): PromiseLike<void>
  /** hide_rejected_photos is on: Go leaves rejected photos (and clusters left empty) out of the grid. */
  hideRejected(): boolean
  on: On
}

export interface FolderStore extends UndoTarget {
  readonly folderId: string
  /** Subscribes, then loads; returns stop. May be started again. */
  start(): () => void
  get(): FolderSnapshot
  /** For useSyncExternalStore. */
  subscribe(listener: () => void): () => void
  /** Loads the grid again (Clusters); resolves once it is set. */
  reload(): Promise<void>
  /** Optimistic pick / unpick / reject / unreject; null when the file already has one in flight. */
  pick(fileId: number, action: PickAction): Promise<PickOutcome | null>
  /** Catalog.Undo on the folder's undo stack; the file's new state is applied. */
  undo(): Promise<PickResult>
  /** The number of the latest answer in `marks` (a load records it when it begins). */
  markSeq(): number
  /** Catalog.Recluster, then a reload; false (and nothing done) while one already runs. */
  recluster(): Promise<boolean>
  /** The clusters, or null before the first load. */
  clusters(): Cluster[] | null
  cluster(id: number): Cluster | undefined
  member(fileId: number): Member | undefined
  /** The Folder page's view memory (mutable; not observed). */
  readonly memory: FolderMemory
  /** The Folder page opened Focus at a thumbnail: remember the file for Focus. */
  setFocusEntry(clusterId: number, fileId: number | undefined): void
  /** Focus's starting file for the cluster, once (then forgotten). */
  takeFocusEntry(clusterId: number): number | undefined
}

/** v1's state machine for one mark. */
export function applyAction(m: Mark, action: PickAction): Mark {
  switch (action) {
    case 'pick':
      return { picked: true, rejected: false }
    case 'reject':
      return { picked: false, rejected: true }
    case 'unpick':
      return { picked: false, rejected: m.rejected }
    case 'unreject':
      return { picked: m.picked, rejected: false }
  }
}

/** The mark of Go's PickResult state (picked | rejected | unmarked). */
export function markOf(state: string): Mark {
  return { picked: state === 'picked', rejected: state === 'rejected' }
}

type ViewMember = FolderView['clusters'][number]['members'][number]

function asMember(m: ViewMember): Member {
  const out: Member = {
    file_id: m.file_id,
    path: m.path,
    content_hash: m.content_hash,
    picked: m.picked,
    rejected: m.rejected,
    kind: m.kind === 'video' ? 'video' : 'photo',
    format: m.format,
  }
  if (typeof m.quality === 'number') out.quality = m.quality
  return out
}

/** A FolderView in the design's shapes. */
export function asFolderData(v: FolderView): FolderData {
  return {
    name: v.folder,
    picked: v.picked,
    total: v.total,
    clusters: (v.clusters ?? []).map((c) => ({ id: c.id, device: c.device, time: c.time, members: (c.members ?? []).map(asMember) })),
    devices: [...(v.devices ?? [])],
    failed: v.failed,
    picksDir: v.picks_dir,
    picksBlocked: v.picks_blocked ? v.picks_blocked : null,
    picksMissing: v.picks_missing,
  }
}

/**
 * `data` with `fn` applied to every member. A member `fn` returns unchanged
 * keeps its object, a cluster none of whose members changed keeps its own,
 * and `data` itself is returned when nothing changed.
 */
export function patchMembers(data: FolderData, fn: (m: Member) => Member): FolderData {
  let changed = false
  const clusters = data.clusters.map((c) => {
    let mine = false
    const members = c.members.map((m) => {
      const next = fn(m)
      if (next !== m) mine = true
      return next
    })
    if (!mine) return c
    changed = true
    return { ...c, members }
  })
  return changed ? { ...data, clusters } : data
}

const withMark = (m: Member, mark: Mark): Member =>
  m.picked === mark.picked && m.rejected === mark.rejected ? m : { ...m, picked: mark.picked, rejected: mark.rejected }

function withRating(m: Member, ratings: Record<number, number>): Member {
  const q = m.kind === 'photo' && Object.hasOwn(ratings, m.file_id) ? ratings[m.file_id] : undefined
  if (q === m.quality) return m
  const next: Member = { ...m }
  if (q === undefined) delete next.quality
  else next.quality = q
  return next
}

/** A value the updater leaves alone (no value yet: nothing to patch). */
const unchanged = <T>(prev: T | undefined): T => prev as T

const noticeOf = (code: string): FolderNotice => (code === 'offline' ? 'offline' : code === 'read_only' ? 'read-only' : null)

export function createFolderStore(deps: FolderStoreDeps): FolderStore {
  const { folderId } = deps
  const listeners = new Set<() => void>()
  /** The view the session held when this store was made: possibly stale, never used as is. */
  let seen: FolderView | null = (() => {
    const s = deps.session.get()
    return s.folderId === folderId ? s.view : null
  })()
  /** A first load is waiting for the session: it takes the view Open returns. */
  let waiting = false
  const waiters = new Set<() => void>()
  /** The latest mark per file this session changed (see MarkEntry); replaced, never mutated. */
  let marks: ReadonlyMap<number, MarkEntry> = new Map()
  let seq = 0
  const setMark = (fileId: number, e: MarkEntry | undefined) => {
    const next = new Map(marks)
    if (e) next.set(fileId, e)
    else next.delete(fileId)
    marks = next
  }
  const answered = (r: PickResult) => setMark(r.file_id, { mark: markOf(r.state), seq: ++seq, pending: false })

  // The session binding: open once the session has this folder, closed for good once it leaves it.
  let phase: 'waiting' | 'open' | 'closed' = 'waiting'
  const closeListeners = new Set<() => void>()
  const watchSession = () => {
    if (phase === 'closed') return
    if (deps.session.get().folderId === folderId) {
      phase = 'open'
      return
    }
    if (phase === 'waiting') return
    phase = 'closed'
    offWatch()
    const ls = [...closeListeners]
    closeListeners.clear()
    for (const l of ls) l()
  }
  const offWatch = deps.session.subscribe(watchSession)
  watchSession()
  let localNotice: FolderNotice = null
  let reclustering = false
  let ratings: Record<number, number> = {}
  let ratingsGen = 0
  let stopped = true
  let reconcile: Setter<FolderData> | null = null
  const focusEntries = new Map<number, number>()
  const memory: FolderMemory = { filter: { query: '', device: '', sort: 'time-asc' }, selectedClusterId: null, visited: false }

  /** `d` with the marks that belong over a load begun at `since` (0: every one this session made). */
  const overlay = (d: FolderData, since = 0): FolderData => {
    if (marks.size === 0) return d
    return patchMembers(d, (m) => {
      const e = marks.get(m.file_id)
      return e && markApplies(e, since) ? withMark(m, e.mark) : m
    })
  }

  /** The session for this folder once it is not opening. */
  const settled = () =>
    new Promise<SessionState>((resolve) => {
      const ready = () => {
        const s = deps.session.get()
        return s.folderId === folderId && s.status !== 'opening' ? s : null
      }
      const now = ready()
      if (now) {
        resolve(now)
        return
      }
      waiting = true
      const off = deps.session.subscribe(() => {
        const s = ready()
        if (!s) return
        waiting = false
        stopWaiting()
        resolve(s)
      })
      const stopWaiting = () => {
        off()
        waiters.delete(stopWaiting)
      }
      waiters.add(stopWaiting)
    })

  const load = async (): Promise<FolderData> => {
    const s = await settled()
    if (s.status === 'open' && s.view && s.view !== seen) {
      seen = s.view
      localNotice = null
      return overlay(asFolderData(s.view))
    }
    if (s.status === 'open') {
      const since = seq
      return overlay(asFolderData(await deps.clusters(folderId)), since)
    }
    // Offline: nothing can be fetched, and what is on screen stays. The
    // latest value (picks, ratings, re-clusters since Open) wins over the
    // view Open returned, which is only the fallback when nothing loaded yet.
    const latest = data.get().value
    if (latest !== undefined) return overlay(latest)
    if (s.view) return overlay(asFolderData(s.view))
    throw new Error('the folder is not open', { cause: s.error ?? { code: 'offline', message: 'the folder is offline' } })
  }

  const data: Store<FolderData> = createStore(load, (_set, reload, rec) => {
    reconcile = rec
    const offFolder = deps.on('folder:changed', (ev) => {
      if (ev.folderId !== folderId) return
      // Clusters: the grid changed in a way the event does not carry.
      if (ev.reason === 'clusters') reload()
      else if (ev.reason === 'ratings') void fetchRatings()
    })
    // A reopen (moved, back online) brings a new view: a state change it carries.
    const offSession = deps.session.subscribe(() => {
      const s = deps.session.get()
      if (waiting || s.folderId !== folderId || s.status !== 'open' || !s.view || s.view === seen) return
      seen = s.view
      localNotice = null
      const fresh = asFolderData(s.view)
      rec(() => overlay(fresh))
    })
    return () => {
      offFolder()
      offSession()
    }
  })

  const patch = (fn: (d: FolderData) => FolderData) => {
    const apply = reconcile ?? data.set
    apply((prev): FolderData => (prev === undefined ? unchanged<FolderData>(prev) : fn(prev)))
  }

  async function fetchRatings() {
    const mine = ++ratingsGen
    let answer: Record<string, number | undefined>
    try {
      answer = await deps.ratings(folderId)
    } catch {
      return // the next ratings event or load repairs it
    }
    if (mine !== ratingsGen || stopped) return
    const next: Record<number, number> = {}
    for (const [id, q] of Object.entries(answer)) if (typeof q === 'number') next[Number(id)] = q
    ratings = next
    recompute()
    patch((d) => patchMembers(d, (m) => withRating(m, next)))
  }

  const noteError = (e: unknown) => {
    const n = noticeOf(appError(e).code)
    if (n !== null && n !== localNotice) {
      localNotice = n
      recompute()
    }
  }
  /** A call went through: the drive is there. */
  const noteSuccess = () => {
    if (localNotice === 'offline') {
      localNotice = null
      recompute()
    }
  }

  let snap: FolderSnapshot
  const build = (): FolderSnapshot => {
    const d = data.get()
    const s = deps.session.get()
    const sessionOffline = s.folderId === folderId && s.status === 'offline'
    const blocked = d.value?.picksBlocked ?? null
    return {
      ...d,
      notice: sessionOffline ? 'offline' : localNotice,
      reclustering,
      busyFileIds: [...marks].flatMap(([id, e]) => (e.pending ? [id] : [])),
      ratings,
      pickBlocked: blocked === null ? null : (fieldCopy('picks_dir', blocked) ?? blocked),
      marks,
    }
  }
  const same = (a: FolderSnapshot, b: FolderSnapshot) =>
    a.status === b.status &&
    a.value === b.value &&
    a.error === b.error &&
    a.notice === b.notice &&
    a.reclustering === b.reclustering &&
    a.ratings === b.ratings &&
    a.pickBlocked === b.pickBlocked &&
    a.marks === b.marks &&
    a.busyFileIds.join() === b.busyFileIds.join()
  function recompute() {
    const next = build()
    if (snap && same(snap, next)) return
    snap = next
    for (const l of [...listeners]) l()
  }
  snap = build()
  data.subscribe(recompute)

  const findMember = (fileId: number): Member | undefined => {
    for (const c of data.get().value?.clusters ?? []) {
      const m = c.members.find((x) => x.file_id === fileId)
      if (m) return m
    }
    return undefined
  }

  const applyResult = (r: PickResult) => {
    const mark = markOf(r.state)
    patch((d) => patchMembers(d, (m) => (m.file_id === r.file_id ? withMark(m, mark) : m)))
  }

  return {
    folderId,
    start() {
      stopped = false
      // The session's offline status is part of the snapshot (only while started: the session outlives this store).
      const offSession = deps.session.subscribe(recompute)
      recompute()
      const stopData = data.start()
      return () => {
        stopped = true
        offSession()
        stopData()
        for (const w of [...waiters]) w()
        waiting = false
      }
    },
    get: () => snap,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    reload: () => data.reload(),
    async pick(fileId, action) {
      if (marks.get(fileId)?.pending) return null
      const blocked = data.get().value?.picksBlocked ?? null
      if (action === 'pick' && blocked !== null) {
        const message = fieldCopy('picks_dir', blocked) ?? blocked
        throw new Error(message, { cause: { code: 'invalid', field: 'picks_dir', reason: blocked, message } })
      }
      const before = marks.get(fileId)
      const cur = findMember(fileId)
      const prev: Mark = cur ? { picked: cur.picked, rejected: cur.rejected } : (before?.mark ?? { picked: false, rejected: false })
      const next = applyAction(prev, action)
      const mine: MarkEntry = { mark: next, seq: 0, pending: true }
      setMark(fileId, mine)
      patch((d) => patchMembers(d, (m) => (m.file_id === fileId ? withMark(m, next) : m)))
      recompute()
      try {
        const result = await deps.setPick(folderId, fileId, action)
        answered(result)
        applyResult(result)
        noteSuccess()
        recompute()
        return { fileId, prev, next: markOf(result.state), result }
      } catch (e) {
        if (marks.get(fileId) === mine) setMark(fileId, before)
        // Roll back, unless something newer (a load) already put another mark there.
        patch((d) =>
          patchMembers(d, (m) =>
            m.file_id === fileId && m.picked === next.picked && m.rejected === next.rejected ? withMark(m, prev) : m,
          ),
        )
        noteError(e)
        recompute()
        throw e
      }
    },
    async undo() {
      // Never on a session this store does not belong to (the folder may be open again, as another session).
      if (phase === 'closed') throw new Error('the folder session is closed', { cause: { code: 'not_open', message: 'The folder is not open' } })
      try {
        const r = await deps.undo(folderId)
        noteSuccess()
        // The fast path patches the member in place. When it is not in the
        // grid (hidden as rejected, its cluster with it) or the setting can
        // hide or show it, the grid's membership changed: fetch it again.
        const shown = findMember(r.file_id) !== undefined
        answered(r)
        applyResult(r)
        recompute()
        if (!shown || deps.hideRejected()) await data.reload()
        return r
      } catch (e) {
        noteError(e)
        throw e
      }
    },
    async recluster() {
      if (reclustering) return false
      reclustering = true
      recompute()
      try {
        await deps.recluster(folderId)
        noteSuccess()
        await data.reload()
        return true
      } catch (e) {
        noteError(e)
        throw e
      } finally {
        reclustering = false
        recompute()
      }
    },
    markSeq: () => seq,
    isOpen: () => phase === 'open' && deps.session.get().folderId === folderId,
    onClose(listener) {
      if (phase === 'closed') {
        listener()
        return () => {}
      }
      closeListeners.add(listener)
      return () => closeListeners.delete(listener)
    },
    clusters: () => data.get().value?.clusters ?? null,
    cluster: (id) => data.get().value?.clusters.find((c) => c.id === id),
    member: findMember,
    memory,
    setFocusEntry(clusterId, fileId) {
      focusEntries.clear()
      if (fileId !== undefined) focusEntries.set(clusterId, fileId)
    },
    takeFocusEntry(clusterId) {
      const fileId = focusEntries.get(clusterId)
      focusEntries.clear()
      return fileId
    },
  }
}

export interface FolderStores {
  /** The store of the open folder `folderId`, made on first use. */
  get(folderId: string): FolderStore
}

/**
 * One FolderStore per open folder, shared by the Folder and Focus routes. A
 * store is dropped (its memory with it) once the session no longer has its
 * folder: the next visit from the Library starts afresh, as v1 did on reload.
 */
export function createFolderStores(
  session: { get(): SessionState; subscribe(listener: () => void): () => void },
  make: (folderId: string) => FolderStore,
): FolderStores {
  let current: FolderStore | null = null
  session.subscribe(() => {
    if (current && session.get().folderId !== current.folderId) current = null
  })
  return {
    get(folderId) {
      if (!current || current.folderId !== folderId) current = make(folderId)
      return current
    },
  }
}

/** A Library row as Library.List returns it. */
export type SummaryRow = FolderSummaryModel

/** A Library row in the design's FolderSummary shape. */
export function asSummary(r: SummaryRow): FolderSummary {
  const out: FolderSummary = {
    id: r.id,
    name: r.name,
    parentPath: r.parentPath,
    state: r.state as FolderSummary['state'],
    month: r.month,
    firstCaptured: r.firstCaptured,
    lastCaptured: r.lastCaptured,
    photos: r.photos,
    videos: r.videos,
    clusters: r.clusters,
    picked: r.picked,
    rejected: r.rejected,
    previews: [...(r.previews ?? [])],
  }
  if (r.scan) out.scan = r.scan as FolderSummary['scan']
  if (r.volumeName) out.volumeName = r.volumeName
  if (r.error) out.error = r.error
  return out
}

export interface SummaryStoreDeps {
  folderId: string
  list(): PromiseLike<SummaryRow[]>
  on: On
}

/**
 * The folder's Library row (name, counts, where it is, its drive's name), for
 * the title bar and the offline notice. Loaded with Library.List; a
 * library:changed for this folder (or for no folder in particular) and the
 * folder's own folder:changed clusters reload it. null when the folder is
 * not in the Library.
 */
export function createSummaryStore(deps: SummaryStoreDeps): Store<FolderSummary | null> {
  const load = async () => {
    const row = (await deps.list()).find((r) => r.id === deps.folderId)
    return row ? asSummary(row) : null
  }
  return createStore(load, (_set, reload) => {
    // State changes the events do not carry: load the row again.
    const offLibrary = deps.on('library:changed', (ev) => {
      if (!ev.folderId || ev.folderId === deps.folderId) reload()
    })
    const offFolder = deps.on('folder:changed', (ev) => {
      if (ev.folderId === deps.folderId && ev.reason === 'clusters') reload()
    })
    return () => {
      offLibrary()
      offFolder()
    }
  })
}
