// Focus states 300–317 (v1 Focus.tsx) and the wiring Compare (compare.tsx)
// shares. There is no state 309: photo navigation wraps, as in v1.
//
// Store keys: `activeIndex`, `view` ({ zoom, pan }), `comparing`,
// `compareIdx`, `marks` (file id → pick mark), `undo` (the last action, for
// the toast's Undo), `mediaState`, the overlay id `focus-info`, and
// `toast:<id>`.
import { applyAction, actionTitle, compareSelect, enterCompare, type Mark } from '../../src/design/screens/focus/logic'
import { FocusScreen } from '../../src/design/screens/focus/FocusScreen'
import { FOCUS_OVERLAYS, type FocusProps, type MediaState } from '../../src/design/screens/focus/props'
import type { FocusResponse, MediaURLs, ToastItem } from '../../src/design/types'
import {
  BROKEN_FILE,
  CLUSTER_IDS,
  FOLDER_NAME,
  LOAD_ERROR,
  LONG,
  MEDIA,
  MEDIA_BROKEN,
  ONLY,
  ONLY_FOLDER,
  ONLY_RATINGS,
  RATINGS,
  RIDGE_CLUSTER,
  RIDGE_REVIEWED,
  RIDGE_WITH_GPS,
  SINGLE,
  WITH_VIDEO,
} from '../mock/focus'
import type { Scenario, ScenarioCtx } from '../registry'
import { read, toastsOf } from '../store'

export interface FocusState {
  data: FocusResponse | null
  /** The folder Back returns to; the Dolomites folder unless given. */
  folder?: string
  loadError?: string
  activeIndex?: number
  zoom?: number
  pan?: { x: number; y: number }
  showInfo?: boolean
  comparing?: boolean
  compareIdx?: number[]
  busy?: number[]
  mediaState?: Record<number, MediaState>
  ratings?: Record<number, number>
  qualityOn?: boolean
  media?: MediaURLs
  toasts?: ToastItem[]
  /** Media loads are not reported (s313 keeps its thumbnail-only look). */
  frozenMedia?: boolean
}

// Where a click leads in the prototype.
const GO = { folder: 203, settings: 500, shortcuts: 600, reload: 301 }
/** The state each cluster id opens (previous / next cluster, the done pill).
 *  The other clusters of the folder have no Focus state: their buttons work
 *  in the app and do nothing here. */
const CLUSTER_STATE: Record<number, number> = {
  [CLUSTER_IDS.ridge]: 301,
  [CLUSTER_IDS.single]: 311,
  [CLUSTER_IDS.long]: 318,
  [CLUSTER_IDS.video]: 306,
}

/** v1 reuses one toast for every Pick / Reject / Clear and its Undo. */
const UNDO_TOAST = 'focus-undo'

export function focusProps(ctx: ScenarioCtx, s: FocusState): FocusProps {
  const { state, set, go, toast } = ctx.store
  const marks = read<Record<number, Mark>>(state, 'marks', {})
  const data: FocusResponse | null = s.data && {
    ...s.data,
    cluster: {
      ...s.data.cluster,
      members: s.data.cluster.members.map((m) => (marks[m.file_id] ? { ...m, ...marks[m.file_id] } : m)),
    },
  }
  const members = data?.cluster.members ?? []
  const activeIndex = read(state, 'activeIndex', s.activeIndex ?? 0)
  const view = read(state, 'view', { zoom: s.zoom ?? 1, pan: s.pan ?? { x: 0, y: 0 } })
  const comparing = read(state, 'comparing', s.comparing ?? false)
  const compareIdx = read(state, 'compareIdx', s.compareIdx ?? [])
  const mediaState = read(state, 'mediaState', s.mediaState ?? {})
  const undo = read<{ fileId: number; prev: Mark } | null>(state, 'undo', null)
  const markOf = (fileId: number): Mark => {
    const m = members.find((x) => x.file_id === fileId)
    return { picked: !!m?.picked, rejected: !!m?.rejected }
  }

  return {
    folder: s.folder ?? FOLDER_NAME,
    data,
    loadError: s.loadError ?? null,
    activeIndex,
    zoom: view.zoom,
    pan: view.pan,
    showInfo: read(state, FOCUS_OVERLAYS.info, s.showInfo ?? false),
    comparing,
    compareIdx,
    busyFileIds: s.busy ?? [],
    mediaState,
    ratings: s.ratings ?? RATINGS,
    qualityOn: s.qualityOn ?? true,
    media: s.media ?? MEDIA,
    toasts: toastsOf(state, s.toasts),
    onBack: () => go(GO.folder),
    onCluster: (id) => {
      const to = CLUSTER_STATE[id]
      if (to !== undefined) go(to)
    },
    onSelect: (i) => set({ activeIndex: i, ...(comparing ? {} : { view: { zoom: 1, pan: { x: 0, y: 0 } } }) }),
    onCompareSelect: (i) => {
      const next = compareSelect(compareIdx, i, activeIndex)
      if (next) set(next)
    },
    onCompare: (on) => set(on ? { comparing: true, ...enterCompare(compareIdx, activeIndex) } : { comparing: false }),
    onPick: (fileId, action) => {
      const prev = markOf(fileId)
      set({ marks: { ...marks, [fileId]: applyAction(prev, action) }, undo: { fileId, prev } })
      toast({ id: UNDO_TOAST, tone: 'pick', title: actionTitle(action), undo: true })
    },
    onView: (v) => set({ view: v }),
    onInfo: (open) => set({ [FOCUS_OVERLAYS.info]: open }),
    onMediaState: (fileId, st) => {
      if (s.frozenMedia) return
      if ((mediaState[fileId] ?? 'ready') !== st) set({ mediaState: { ...mediaState, [fileId]: st } })
    },
    onRetry: () => go(GO.reload),
    onHelp: () => go(GO.shortcuts),
    onSettings: () => go(GO.settings),
    onUndoToast: (id) => {
      if (id !== UNDO_TOAST || !undo) return set({ [`toast:${id}`]: false })
      set({ marks: { ...marks, [undo.fileId]: undo.prev }, undo: null })
      toast({ id: UNDO_TOAST, tone: 'pick', title: 'Undone' })
    },
    onDismissToast: (id) => set({ [`toast:${id}`]: false }),
  }
}

const V1 = 'v1:components/Focus.tsx'

const HERO = '[data-testid="hero"]'
const PICK = '[data-testid="pick-btn"]'

/** What each state's label names, which must be in view at every size. */
const SUBJECT: Record<number, string> = {
  300: 'main [role="status"]',
  301: HERO,
  302: PICK,
  303: '[data-testid="reject-btn"]',
  304: '[data-overlay="focus-info"]',
  305: '[data-overlay="focus-info"] a',
  306: '[data-testid="hero-video"]',
  307: '[data-testid="zoom-level"]',
  308: '[data-testid="zoom-level"]',
  310: '[data-testid="cluster-done"]',
  311: '[data-rail-index="0"]',
  312: '[data-testid="media-error"] [role="status"]',
  313: '[data-testid="hero-thumb"]',
  314: '[data-testid="stage-controls"]',
  315: PICK,
  316: '[role="alert"]',
  317: '[data-testid="prev-cluster"], [data-testid="next-cluster"]',
  318: '[data-rail-index="31"]',
}

const focus = (n: number, label: string, s: FocusState, design = V1, overlays?: string[]): Scenario => ({
  n,
  group: 'focus',
  label,
  design,
  overlays,
  subject: SUBJECT[n],
  render: (ctx) => <FocusScreen {...focusProps(ctx, s)} />,
})

const INFO = [FOCUS_OVERLAYS.info]

export const scenarios: Scenario[] = [
  focus(300, 'Loading', { data: null }),
  focus(301, 'Photo', { data: RIDGE_CLUSTER }),
  focus(302, 'Picked', { data: RIDGE_CLUSTER, activeIndex: 1 }),
  focus(303, 'Rejected', { data: RIDGE_CLUSTER, activeIndex: 3 }),
  focus(304, 'Info panel open', { data: RIDGE_CLUSTER, showInfo: true }, V1, INFO),
  focus(305, 'Info with GPS', { data: RIDGE_WITH_GPS, activeIndex: 2, showInfo: true }, V1, INFO),
  focus(306, 'Video (S3)', { data: WITH_VIDEO }),
  focus(307, 'Zoom 2×', { data: RIDGE_CLUSTER, zoom: 2 }),
  focus(308, 'Zoom 8×, panned', { data: RIDGE_CLUSTER, zoom: 8, pan: { x: -960, y: 440 } }),
  focus(310, 'All reviewed, cluster-done pill', { data: RIDGE_REVIEWED, activeIndex: 7 }),
  focus(311, 'Single-photo cluster', { data: SINGLE }),
  focus(
    312,
    'Image failed to load',
    { data: RIDGE_CLUSTER, activeIndex: 2, media: MEDIA_BROKEN, mediaState: { [BROKEN_FILE]: 'error' } },
    'missing design',
  ),
  focus(313, 'Preview shown while full loads', { data: RIDGE_CLUSTER, activeIndex: 4, mediaState: { 4105: 'loading' }, frozenMedia: true }),
  focus(314, 'Quality badge absent', { data: RIDGE_CLUSTER, activeIndex: 5 }),
  focus(315, 'Pick in flight (buttons disabled)', { data: RIDGE_CLUSTER, busy: [4101] }),
  focus(316, 'Cluster failed to load', { data: null, loadError: LOAD_ERROR }, 'missing design'),
  focus(317, 'Only cluster in folder (cluster navigation disabled)', { data: ONLY, folder: ONLY_FOLDER, ratings: ONLY_RATINGS }),
  focus(318, 'Long cluster, rail scrolled to the active photo', { data: LONG, activeIndex: 31 }),
]
