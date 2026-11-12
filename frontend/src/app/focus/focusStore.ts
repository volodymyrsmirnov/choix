// Focus and its compare mode (#/folder/<id>/focus/<clusterId>): the cluster
// on screen and everything v1's Focus.tsx kept in useState — the photo on
// screen, zoom and pan, compare panes, the Info panel, each member's media
// state. The rules are the design's (screens/focus/logic.ts), applied here
// so the screen's buttons, the keys and the native menu all end in the same
// call.
//
// The cluster is Catalog.Cluster, asked once the folder session has the
// folder open (the session opens and closes it; this store never does).
// `folder:changed` clusters for this folder loads it again (a state change
// the event does not carry); one in flight is followed by at most one more.
// A refresh of the cluster on screen that fails keeps it; only a cluster
// that never loaded shows loadError.
//
// Focus follows the photo, not the cluster id: a rebuild (WriteClusters)
// deletes every cluster and inserts them again under reused ids, so after
// one the id on screen may name another cluster. The photo Focus follows is
// the one on screen, or, before its cluster has landed, the starting photo
// (pinned once a clusters change arrived during that load). Membership is
// what Catalog.Cluster says (it lists rejected photos; the grid may not).
// When an answer does not hold the photo (or its id is gone), the store
// loads the folder's grid again and asks Catalog.Cluster, at most
// MAX_PROBES times, about the cluster the grid lists the photo in, else the
// clusters of its former cluster-mates (a rejected photo the grid leaves
// out). The cluster that holds it lands, keeping the photo and the compare
// panes still in it; only then does the cluster id change (deps.moved: the
// container puts it in the route). Until then what is on screen stays, and
// a follow that could not finish (the grid or a cluster could not be read)
// is tried again by the next load for the same photo. When no candidate
// holds it, the photo is in no cluster any more and Focus ends (deps.lost).
// Focus never shows another photo in its place.
//
// Picks and undo go through the open folder's store (folder/folderStore.ts),
// so the grid shows them on the way back and ⌘Z there undoes them (Go keeps
// one undo stack per open folder). That store's mark ledger is the one
// record of what the session changed: the members shown here are the
// cluster as loaded with every ledger mark laid over it that was in flight
// or answered after the load began, recomputed whenever the ledger changes.
// So a pick started by an earlier Focus, in the grid, or before another
// cluster was shown appears here when it lands, and an older load never
// hides a newer answer. Files with a pick in flight and the ratings (the id
// map) are the folder store's too.
import {
  actionTarget,
  compareSelect,
  enterCompare,
  paneIndex,
  stepIndex,
  stepZoom,
  toggleAction,
  viewAt,
} from '../../design/screens/focus/logic'
import { MIN_ZOOM, PEEP_ZOOM, type PickAction } from '../../design/screens/focus/props'
import type { ExifPayload, FocusResponse, FocusView, MediaState, Member } from '../../design/types'
import { markApplies, type MarkEntry, type PickOutcome } from '../folder/folderStore'
import type { FocusResponse as FocusResponseModel } from '../shell/api'
import { appError, errorText } from '../shell/errors'
import type { On } from '../shell/events'
import type { SessionState } from '../shell/folderSession'

/** What Focus uses of the open folder's store. */
export interface FocusFolder {
  pick(fileId: number, action: PickAction): Promise<PickOutcome | null>
  get(): {
    status: 'loading' | 'ready' | 'error'
    /** What the latest grid load failed with. */
    error: unknown
    busyFileIds: number[]
    ratings: Record<number, number>
    marks: ReadonlyMap<number, MarkEntry>
  }
  /** Loads the grid again (Catalog.Clusters); resolves once it is set (or failed). */
  reload(): Promise<void>
  /** The grid's clusters, or null before the first load. */
  clusters(): readonly { id: number; members: readonly { file_id: number }[] }[] | null
  /** The ledger's latest answer number (a load records it when it begins). */
  markSeq(): number
  subscribe(listener: () => void): () => void
}

/** Where the Info panel's open state is kept between visits. */
export interface FocusPrefs {
  showInfo(): boolean
  setShowInfo(open: boolean): void
}

export interface FocusStoreDeps {
  folderId: string
  clusterId: number
  /** The photo to show first (the clicked thumbnail); the first photo when absent or gone. */
  startFileId?: number
  session: { get(): SessionState; subscribe(listener: () => void): () => void }
  cluster(folderId: string, clusterId: number): PromiseLike<FocusResponseModel>
  folder: FocusFolder
  on: On
  /** Settings › Advance on action. */
  advanceOnAction(): boolean
  prefs: FocusPrefs
  /** A rebuild moved the photo on screen to cluster `clusterId`, which the store shows now: put it in the route. */
  moved?(clusterId: number): void
  /**
   * The photo on screen is in no cluster any more (deleted from disk, its
   * cluster gone): leave for the Folder page. A rejected photo the grid
   * leaves out is still in its cluster and stays in Focus.
   */
  lost?(): void
}

export interface FocusSnapshot {
  /** The cluster asked for (the route's). */
  clusterId: number
  /** The cluster on screen; null before the first answer or after a failed one. */
  data: FocusResponse | null
  loadError: string | null
  activeIndex: number
  view: FocusView
  comparing: boolean
  compareIdx: number[]
  showInfo: boolean
  mediaState: Record<number, MediaState>
  busyFileIds: number[]
  ratings: Record<number, number>
}

export interface FocusStore {
  /** Subscribes, then loads; returns stop. */
  start(): () => void
  get(): FocusSnapshot
  /** For useSyncExternalStore. */
  subscribe(listener: () => void): () => void
  /** Another cluster (the route changed): leaves compare and zoom, loads it. */
  open(clusterId: number, startFileId?: number): void
  /** Try Again after loadError. */
  retry(): void
  select(index: number): void
  /** ←/→ (and ↑/↓): through the cluster, or among the panes while comparing; both wrap. */
  step(dir: 1 | -1): void
  /** 1–4 while comparing. */
  pane(n: number): void
  compare(on: boolean): void
  toggleCompare(): void
  compareSelect(index: number): void
  setView(v: FocusView): void
  zoom(dir: 1 | -1): void
  zoomFit(): void
  /** Z: fit ↔ PEEP_ZOOM (not while comparing). */
  peep(): void
  setInfo(open: boolean): void
  toggleInfo(): void
  setMediaState(fileId: number, state: Exclude<MediaState, 'loading'>): void
  /** A pick action on a file; null when that file already has one in flight. Advances when Settings say so. */
  act(fileId: number, action: PickAction): Promise<PickOutcome | null>
  /** Space (`picked`) or X (`rejected`) on the photo on screen or the active pane. */
  mark(k: 'picked' | 'rejected'): Promise<{ action: PickAction; outcome: PickOutcome } | null>
  /** Esc / Back: un-zooms, else leaves compare (true); false when there was nothing to close. */
  escape(): boolean
  /** The next or previous cluster's id; null when there is none or the requested cluster has not loaded yet. */
  neighbour(dir: 'next' | 'prev'): number | null
}

const FIT: FocusView = { zoom: MIN_ZOOM, pan: { x: 0, y: 0 } }

/** How many clusters one follow may ask Catalog.Cluster about. */
const MAX_PROBES = 4

/** How many times one load may start a follow again for a photo the user chose meanwhile. */
const MAX_RESTARTS = 3

const holdsFile = (d: FocusResponse, fileId: number) => d.cluster.members.some((m) => m.file_id === fileId)

function asMember(m: FocusResponseModel['cluster']['members'][number]): Member {
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

function asExif(e: NonNullable<FocusResponseModel['exif'][string]>): ExifPayload {
  const out: ExifPayload = {}
  for (const k of ['camera', 'aperture', 'shutter', 'iso', 'focal_length', 'captured', 'size'] as const) {
    const v = e[k]
    if (typeof v === 'string' && v !== '') out[k] = v
  }
  if (typeof e.gps_lat === 'number') out.gps_lat = e.gps_lat
  if (typeof e.gps_lon === 'number') out.gps_lon = e.gps_lon
  return out
}

/** Catalog.Cluster's answer in the design's shapes. */
export function asFocusResponse(r: FocusResponseModel): FocusResponse {
  const exif: Record<string, ExifPayload> = {}
  for (const [id, e] of Object.entries(r.exif ?? {})) if (e) exif[id] = asExif(e)
  return {
    cluster: { id: r.cluster.id, device: r.cluster.device, time: r.cluster.time, members: (r.cluster.members ?? []).map(asMember) },
    cluster_index: r.cluster_index,
    cluster_count: r.cluster_count,
    exif,
    next_cluster_id: r.next_cluster_id,
    prev_cluster_id: r.prev_cluster_id,
  }
}

/** `d` with the ledger's marks that belong over a load begun at `since`; `d` itself when none changes a member. */
function withLedger(d: FocusResponse, marks: ReadonlyMap<number, MarkEntry>, since: number): FocusResponse {
  if (marks.size === 0) return d
  let changed = false
  const members = d.cluster.members.map((m) => {
    const e = marks.get(m.file_id)
    if (!e || !markApplies(e, since) || (m.picked === e.mark.picked && m.rejected === e.mark.rejected)) return m
    changed = true
    return { ...m, picked: e.mark.picked, rejected: e.mark.rejected }
  })
  return changed ? { ...d, cluster: { ...d.cluster, members } } : d
}

/** Same files with the same marks, in the same order. */
const sameMarks = (a: Member[], b: Member[]) =>
  a.length === b.length && a.every((m, i) => m === b[i] || (m.file_id === b[i]!.file_id && m.picked === b[i]!.picked && m.rejected === b[i]!.rejected))

/** Every photo `loading` unless it is the same file (id and hash) as before, which keeps its state; videos are absent (ready). */
function mediaStateFor(members: Member[], prev: FocusResponse | null, prevState: Record<number, MediaState>): Record<number, MediaState> {
  const hashOf = new Map((prev?.cluster.members ?? []).map((m) => [m.file_id, m.content_hash]))
  const out: Record<number, MediaState> = {}
  for (const m of members) {
    if (m.kind !== 'photo') continue
    const kept = hashOf.get(m.file_id) === m.content_hash ? prevState[m.file_id] : undefined
    out[m.file_id] = kept ?? 'loading'
  }
  return out
}

interface UiState {
  clusterId: number
  data: FocusResponse | null
  loadError: string | null
  activeIndex: number
  view: FocusView
  comparing: boolean
  compareIdx: number[]
  showInfo: boolean
  mediaState: Record<number, MediaState>
}

export function createFocusStore(deps: FocusStoreDeps): FocusStore {
  const { folderId } = deps
  const listeners = new Set<() => void>()
  let ui: UiState = {
    clusterId: deps.clusterId,
    data: null,
    loadError: null,
    activeIndex: 0,
    view: FIT,
    comparing: false,
    compareIdx: [],
    showInfo: deps.prefs.showInfo(),
    mediaState: {},
  }
  /**
   * The starting photo until its cluster lands (open's, or the clicked
   * thumbnail). `strict` from the moment a clusters event arrives while it
   * is pending, through every failed load, follow or Try Again, until a
   * load lands it: it is then followed like the photo on screen, never
   * replaced by the cluster's first photo. A new open replaces it.
   */
  let pin: { fileId: number; strict: boolean } | null = deps.startFileId === undefined ? null : { fileId: deps.startFileId, strict: false }
  /** The cluster as Catalog.Cluster answered, and the ledger's number when that load began. */
  let base: FocusResponse | null = null
  let baseSince = 0
  /** The ledger `ui.data` was last derived with. */
  let ledger = deps.folder.get().marks
  let gen = 0
  let inFlight = false
  let dirty = false
  let stopped = true
  /** Stops waiting for the session. */
  let unwait: (() => void) | null = null

  let busy: number[] = []
  let snap: FocusSnapshot
  const build = (): FocusSnapshot => {
    const f = deps.folder.get()
    if (f.busyFileIds.join() !== busy.join()) busy = [...f.busyFileIds]
    return { ...ui, busyFileIds: busy, ratings: f.ratings }
  }
  const publish = () => {
    const next = build()
    if (
      snap &&
      Object.keys(next).every((k) => next[k as keyof FocusSnapshot] === snap[k as keyof FocusSnapshot])
    ) {
      return
    }
    snap = next
    for (const l of [...listeners]) l()
  }
  snap = build()
  const update = (patch: Partial<UiState>) => {
    ui = { ...ui, ...patch }
    publish()
  }

  const members = (): Member[] => ui.data?.cluster.members ?? []
  const activeMember = (): Member | undefined => members()[ui.activeIndex]
  const activeIsVideo = () => activeMember()?.kind === 'video'

  /** The loaded cluster with the ledger over it; the shown object when no mark on screen changed. */
  const derive = (b: FocusResponse): FocusResponse => {
    ledger = deps.folder.get().marks
    const out = withLedger(b, ledger, baseSince)
    const shown = ui.data
    if (shown && shown !== out && shown.cluster.id === out.cluster.id && shown.exif === out.exif && sameMarks(shown.cluster.members, out.cluster.members)) {
      return shown
    }
    return out
  }

  /** The folder store changed: a new ledger is laid over the loaded cluster again. */
  const onFolder = () => {
    if (base && deps.folder.get().marks !== ledger) ui = { ...ui, data: derive(base) }
    publish()
  }

  /**
   * Shows `loaded` with `show` on screen (the first photo when absent or not
   * in it). `carry`: the photo on screen and the panes are carried over by
   * file (a refresh, or the cluster the photo moved to); else a fresh start.
   * The cluster id becomes the answer's: a different one is reported (moved).
   */
  const land = (loaded: FocusResponse, since: number, show: number | undefined, carry: boolean) => {
    base = loaded
    baseSince = since
    pin = null
    const fresh = derive(loaded)
    const prev = ui.data
    const ms = fresh.cluster.members
    const mediaState = mediaStateFor(ms, prev, ui.mediaState)
    const at = Math.max(0, show === undefined ? -1 : ms.findIndex((m) => m.file_id === show))
    const clusterId = fresh.cluster.id
    const moved = clusterId !== ui.clusterId
    if (!carry || !prev) {
      update({ clusterId, data: fresh, loadError: null, activeIndex: at, view: FIT, comparing: false, compareIdx: [], mediaState })
    } else {
      const indexOf = new Map(ms.map((m, i) => [m.file_id, i]))
      const old = prev.cluster.members
      const panes = ui.compareIdx.flatMap((i) => {
        const id = old[i]?.file_id
        const j = id === undefined ? undefined : indexOf.get(id)
        return j === undefined ? [] : [j]
      })
      // Fewer than two panes left of a comparison: the photo on screen alone.
      const single = panes.length < ui.compareIdx.length && panes.length < 2
      update({
        clusterId,
        data: fresh,
        loadError: null,
        activeIndex: at,
        compareIdx: single ? [] : panes,
        comparing: single ? false : ui.comparing,
        mediaState,
        // Another cluster starts at fit (as open does).
        ...(prev.cluster.id !== clusterId ? { view: FIT } : {}),
      })
    }
    if (moved) deps.moved?.(clusterId)
  }

  /** True once the session has this folder open; false when it is offline or failed (the container leaves). */
  const settled = () =>
    new Promise<boolean>((resolve) => {
      const check = (): boolean | null => {
        const s = deps.session.get()
        if (s.folderId !== folderId || s.status === 'opening' || s.status === 'closed') return null
        return s.status === 'open'
      }
      const now = check()
      if (now !== null) {
        resolve(now)
        return
      }
      const off = deps.session.subscribe(() => {
        const v = check()
        if (v === null) return
        stop()
        resolve(v)
      })
      const stop = () => {
        off()
        if (unwait === stop) unwait = null
      }
      unwait?.()
      unwait = stop
    })

  /**
   * The photo this load must keep, when it has one: the starting photo
   * (strict once a clusters event arrived for it), else the photo on screen
   * when `want` is the cluster on screen. `carry`: it is the photo on screen.
   */
  const identity = (want: number): { fileId: number; strict: boolean; carry: boolean } | null => {
    if (pin) return { fileId: pin.fileId, strict: pin.strict, carry: false }
    const a = ui.data?.cluster.id === want ? activeMember() : undefined
    return a ? { fileId: a.file_id, strict: true, carry: true } : null
  }

  /**
   * Finds and lands the cluster that holds `id.fileId` after a rebuild.
   * 'landed', 'lost' (in no cluster any more), 'open' (could not finish:
   * what is on screen stays, the next load tries again; `followError` says
   * why when a call failed), 'changed' (the user chose another photo during
   * an await: nothing of this follow is applied) or 'stale'.
   */
  const follow = async (mine: number, id: { fileId: number; carry: boolean }, want: number, wantLacks: boolean) => {
    const { fileId } = id
    followError = null
    /** The photo to follow is no longer this one (a selection while waiting). */
    const changed = () => identity(want)?.fileId !== fileId
    // Its cluster-mates before the rebuild: the cluster on screen, else the grid as it was.
    const before = id.carry && ui.data ? ui.data.cluster.members : (deps.folder.clusters()?.find((c) => c.members.some((m) => m.file_id === fileId))?.members ?? [])
    const mates = before.map((m) => m.file_id).filter((f) => f !== fileId)
    await deps.folder.reload()
    if (mine !== gen || stopped) return 'stale'
    if (changed()) return 'changed'
    const f = deps.folder.get()
    const grid = f.status === 'error' ? null : deps.folder.clusters()
    if (!grid) {
      followError = f.error
      return 'open'
    }
    const clusterOf = (f: number) => grid.find((c) => c.members.some((m) => m.file_id === f))?.id
    const home = clusterOf(fileId)
    let candidates: number[]
    if (home !== undefined) candidates = [home]
    else {
      // Not in the grid (rejected while hide_rejected_photos is on): try where its former cluster-mates are now.
      candidates = []
      for (const m of mates) {
        const c = clusterOf(m)
        if (c !== undefined && !candidates.includes(c) && !(wantLacks && c === want)) candidates.push(c)
      }
    }
    let unsure = candidates.length > MAX_PROBES
    for (const c of candidates.slice(0, MAX_PROBES)) {
      const since = deps.folder.markSeq()
      let r: FocusResponse
      try {
        r = asFocusResponse(await deps.cluster(folderId, c))
      } catch (e) {
        if (mine !== gen || stopped) return 'stale'
        if (changed()) return 'changed'
        followError = e
        unsure = true
        continue
      }
      if (mine !== gen || stopped) return 'stale'
      if (changed()) return 'changed'
      if (holdsFile(r, fileId)) {
        land(r, since, fileId, id.carry)
        return 'landed'
      }
      // The grid listed it there and Go says otherwise: rebuilt again meanwhile, not gone.
      if (c === home) unsure = true
    }
    return unsure ? 'open' : 'lost'
  }

  /** Why the latest follow could not finish, when a call failed. */
  let followError: unknown = null

  const load = async (): Promise<void> => {
    const mine = ++gen
    inFlight = true
    dirty = false
    const open = await settled()
    if (mine !== gen || stopped) return
    if (!open) {
      inFlight = false
      return
    }
    const since = deps.folder.markSeq()
    const want = ui.clusterId
    let loaded: FocusResponse | null = null
    let failure: unknown = null
    try {
      loaded = asFocusResponse(await deps.cluster(folderId, want))
    } catch (e) {
      failure = e
    }
    if (mine !== gen || stopped) return
    const id = identity(want)
    const strict = id?.strict ?? false
    if (loaded && (!strict || holdsFile(loaded, id!.fileId))) {
      inFlight = false
      land(loaded, since, id?.fileId, id?.carry ?? false)
    } else if (!loaded && (!strict || appError(failure).code !== 'not_found')) {
      inFlight = false
      // A refresh of the cluster on screen that failed keeps it; another cluster cannot be shown.
      if (ui.data?.cluster.id !== want) {
        base = null
        update({ data: null, loadError: errorText(appError(failure)) })
      }
    } else {
      // The photo is not in the answer, or the id is gone: the clusters were rebuilt.
      let target = id!
      let wantLacks = loaded !== null
      for (let restarts = 0; ; restarts++) {
        const outcome = await follow(mine, target, want, wantLacks)
        if (outcome === 'stale') return
        if (outcome === 'changed') {
          // The user chose another photo meanwhile: follow that one instead (a newer follow wins).
          const next = identity(want)
          if (next && restarts < MAX_RESTARTS) {
            target = next
            wantLacks = false
            continue
          }
          // Out of restarts: what is on screen stays; the next load follows the photo then shown.
          inFlight = false
          break
        }
        inFlight = false
        if (outcome === 'lost') deps.lost?.()
        // Not finished, nothing of that cluster on screen: say why, as a failed load does (the pin stays strict).
        else if (outcome === 'open' && pin && followError !== null && ui.data?.cluster.id !== want) {
          base = null
          update({ data: null, loadError: errorText(appError(followError)) })
        }
        break
      }
    }
    if (dirty && !stopped) {
      dirty = false
      void load()
    }
  }
  const requestLoad = () => {
    if (inFlight) dirty = true
    else void load()
  }

  const select = (index: number) => {
    if (index < 0 || index >= members().length || index === ui.activeIndex) return
    // Another photo starts at fit; compare panes share one view and keep it.
    update(ui.comparing ? { activeIndex: index } : { activeIndex: index, view: FIT })
  }

  const compare = (on: boolean) => {
    if (on === ui.comparing || !ui.data) return
    update(on ? { comparing: true, ...enterCompare(ui.compareIdx, ui.activeIndex) } : { comparing: false })
  }

  const setInfo = (open: boolean) => {
    if (open === ui.showInfo) return
    deps.prefs.setShowInfo(open)
    update({ showInfo: open })
  }

  const act = async (fileId: number, action: PickAction): Promise<PickOutcome | null> => {
    // The folder store shows the mark at once, replaces it with Go's answer
    // or rolls it back; its ledger reaches this cluster through onFolder.
    const outcome = await deps.folder.pick(fileId, action)
    if (outcome === null) return null
    // Advance after Pick or Reject (not after clearing one), never while
    // comparing, and only if the photo acted on is still the one on screen.
    const ms = members()
    if (
      (action === 'pick' || action === 'reject') &&
      deps.advanceOnAction() &&
      !ui.comparing &&
      ms.length > 1 &&
      ms[ui.activeIndex]?.file_id === fileId
    ) {
      ui = { ...ui, activeIndex: (ui.activeIndex + 1) % ms.length, view: FIT }
    }
    publish()
    return outcome
  }

  return {
    start() {
      stopped = false
      const offFolder = deps.folder.subscribe(onFolder)
      const offEvents = deps.on('folder:changed', (ev) => {
        if (ev.folderId !== folderId) return
        if (ev.reason !== 'clusters') return
        // Recorded as it arrives: the starting photo must be followed from now on, whatever fails meanwhile.
        if (pin && !pin.strict) pin = { ...pin, strict: true }
        requestLoad()
      })
      // The folder reopened (moved, back online): load the cluster again.
      let wasOpen = deps.session.get().folderId === folderId && deps.session.get().status === 'open'
      const offSession = deps.session.subscribe(() => {
        const s = deps.session.get()
        const open = s.folderId === folderId && s.status === 'open'
        if (open && !wasOpen && !inFlight) requestLoad()
        wasOpen = open
      })
      onFolder()
      void load()
      return () => {
        stopped = true
        gen++
        inFlight = false
        dirty = false
        unwait?.()
        offFolder()
        offEvents()
        offSession()
      }
    },
    get: () => snap,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    open(clusterId, start) {
      if (clusterId === ui.clusterId) return
      pin = start === undefined ? null : { fileId: start, strict: false }
      // The panes and the zoom meant the previous cluster; it stays on screen until the new one lands.
      update({ clusterId, comparing: false, compareIdx: [], view: FIT })
      if (!stopped) void load()
    },
    retry() {
      update({ loadError: null })
      if (!stopped) void load()
    },
    select,
    step(dir) {
      const i = stepIndex(members().length, ui.activeIndex, dir, ui.comparing, ui.compareIdx)
      if (i !== null) select(i)
    },
    pane(n) {
      if (!ui.comparing) return
      const i = paneIndex(ui.compareIdx, members().length, n)
      if (i !== undefined) select(i)
    },
    compare,
    toggleCompare() {
      compare(!ui.comparing)
    },
    compareSelect(index) {
      if (!ui.comparing || index < 0 || index >= members().length) return
      const next = compareSelect(ui.compareIdx, index, ui.activeIndex)
      if (next) update(next)
    },
    setView(v) {
      update({ view: v })
    },
    zoom(dir) {
      if (!ui.data || activeIsVideo()) return
      update({ view: stepZoom(ui.view, dir) })
    },
    zoomFit() {
      if (!ui.data || activeIsVideo()) return
      update({ view: viewAt(MIN_ZOOM, ui.view.pan) })
    },
    peep() {
      if (!ui.data || ui.comparing || activeIsVideo()) return
      update({ view: ui.view.zoom > MIN_ZOOM ? viewAt(MIN_ZOOM, ui.view.pan) : viewAt(PEEP_ZOOM, ui.view.pan) })
    },
    setInfo,
    toggleInfo() {
      setInfo(!ui.showInfo)
    },
    setMediaState(fileId, state) {
      if ((ui.mediaState[fileId] ?? 'ready') === state) return
      update({ mediaState: { ...ui.mediaState, [fileId]: state } })
    },
    act,
    async mark(k) {
      const target = actionTarget(members(), ui.activeIndex, ui.comparing, ui.compareIdx)
      if (!target) return null
      const action = toggleAction(target, k)
      const outcome = await act(target.file_id, action)
      return outcome ? { action, outcome } : null
    },
    escape() {
      if (ui.view.zoom > MIN_ZOOM) {
        update({ view: FIT })
        return true
      }
      if (ui.comparing) {
        update({ comparing: false })
        return true
      }
      return false
    },
    neighbour(dir) {
      const d = ui.data
      if (!d || d.cluster.id !== ui.clusterId) return null
      const id = dir === 'next' ? d.next_cluster_id : d.prev_cluster_id
      return id ? id : null
    },
  }
}
