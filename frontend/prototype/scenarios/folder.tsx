// Folder states 200–229 (spec §4.3): v1's Library cluster grid as the page of
// one folder, wired to the prototype store so it is clickable.
//
// Store keys: `query`, `device`, `sort` (the filter), `selectedClusterId`,
// the overlay ids `folder-camera-menu` and `folder-sort-menu` (true while
// open), and `toast:<id>`.
import { filterClusters } from '../../src/design/screens/folder/filter'
import { FolderScreen } from '../../src/design/screens/folder/FolderScreen'
import { FOLDER_OVERLAYS, type FolderProps } from '../../src/design/screens/folder/props'
import type { Cluster, FolderFilter, FolderSummary, QualityStatus, ScanProgress, ToastItem } from '../../src/design/types'
import { quality as qualityStatus, scanAt } from '../mock/data'
import {
  BURST_CLUSTER,
  CLUSTERS,
  devicesOf,
  DONE_CLUSTER,
  hideRejected,
  IPHONE,
  MEDIA,
  MERGED_CLUSTER,
  ON_CARD,
  ON_DRIVE,
  REVIEWED,
  summaryOf,
  TOAST_RECLUSTER_FAILED,
  TOAST_RECLUSTERED,
  unrated,
  withSecond,
} from '../mock/folder'
import { CLUSTER_IDS } from '../mock/focus'
import type { Scenario, ScenarioCtx } from '../registry'
import { read, toastsOf } from '../store'

const GRID = 'v1:components/Library.tsx'
const EMPTY = 'v1:components/Empty.tsx'
const TOASTS = 'v1:components/Toaster.tsx'
const NEW = 'missing design'

// Where a click leads in the prototype. A cluster opens its Focus state; a
// cluster without one says so in a toast.
const GO = { library: 102, recluster: 215, settings: 500, shortcuts: 600 }
const FOCUS_OF: Record<number, number> = {
  [CLUSTER_IDS.ridge]: 301,
  [CLUSTER_IDS.single]: 311,
  [CLUSTER_IDS.long]: 318,
  [CLUSTER_IDS.video]: 306,
}

export interface FolderState {
  clusters: Cluster[] | null
  /** Defaults to the summary of `clusters` on the Dolomites folder. */
  folder?: FolderSummary
  scan?: ScanProgress | null
  filter?: Partial<FolderFilter>
  openMenu?: FolderProps['openMenu']
  /** Index into the visible clusters; v1 starts on the first. */
  selected?: number
  hideRejected?: boolean
  quality?: boolean
  /** The quality worker's status, for the title bar's indicator. */
  qualityStatus?: QualityStatus | null
  reclustering?: boolean
  failedFiles?: number
  picksMissing?: number
  notice?: FolderProps['notice']
  toasts?: ToastItem[]
}

export function folderProps(ctx: ScenarioCtx, s: FolderState): FolderProps {
  const { state, set, go, toast } = ctx.store
  const clusters = s.clusters
  const folder = s.folder ?? summaryOf(clusters ?? [])
  const filter: FolderFilter = {
    query: read(state, 'query', s.filter?.query ?? ''),
    device: read(state, 'device', s.filter?.device ?? ''),
    sort: read(state, 'sort', s.filter?.sort ?? 'time-asc'),
  }
  const visible = clusters ? filterClusters(clusters, filter) : []
  const seeded = visible[s.selected ?? 0]?.id ?? null
  const chosen = read<number | null>(state, 'selectedClusterId', seeded)
  // As v1: the cursor stays on the list when a filter hides its cluster.
  const selectedClusterId = visible.some((c) => c.id === chosen) ? chosen : (visible[0]?.id ?? null)
  const open = (id: string, dflt: boolean) => read<boolean>(state, id, dflt)
  const openMenu = open(FOLDER_OVERLAYS.device, s.openMenu === 'device') ? 'device' : open(FOLDER_OVERLAYS.sort, s.openMenu === 'sort') ? 'sort' : null

  return {
    folder,
    clusters,
    scan: s.scan ?? null,
    filter,
    devices: devicesOf(clusters ?? []),
    openMenu,
    selectedClusterId,
    settings: {
      hide_rejected_photos: s.hideRejected ?? false,
      quality_rating: s.quality ?? true,
    },
    quality: s.qualityStatus ?? null,
    reclustering: s.reclustering ?? false,
    failedFiles: s.failedFiles ?? 0,
    picksMissing: s.picksMissing ?? 0,
    notice: s.notice ?? null,
    picksDir: 'picks',
    toasts: toastsOf(state, s.toasts),
    media: MEDIA,
    onBack: () => go(GO.library),
    onOpenPicks: () =>
      toast({ id: 'picks', tone: 'info', title: `Finder shows the picks of “${folder.name}”`, body: 'The picks folder opens in a Finder window.' }),
    onOpenCluster: (id) =>
      FOCUS_OF[id] !== undefined
        ? go(FOCUS_OF[id])
        : toast({
            id: 'no-focus',
            tone: 'info',
            title: 'This cluster has no Focus state in the prototype',
            body: 'The first two clusters, the 40-shot burst and the last cluster open in Focus.',
          }),
    onSelectCluster: (id) => set({ selectedClusterId: id }),
    onFilter: (f) => set({ query: f.query, device: f.device, sort: f.sort }),
    onOpenMenu: (m) => set({ [FOLDER_OVERLAYS.device]: m === 'device', [FOLDER_OVERLAYS.sort]: m === 'sort' }),
    onRecluster: () => go(GO.recluster),
    onPick: () => {},
    onSettings: () => go(GO.settings),
    onHelp: () => go(GO.shortcuts),
    onUndoToast: (id) => set({ [`toast:${id}`]: false }),
    onDismissToast: (id) => set({ [`toast:${id}`]: false }),
  }
}

const folder = (n: number, label: string, s: FolderState, design = GRID, overlays: string[] = []): Scenario => ({
  n,
  group: 'folder',
  label,
  design,
  overlays,
  subject: SUBJECT[n],
  render: (ctx) => <FolderScreen {...folderProps(ctx, s)} />,
})

const ANALYZING = scanAt('analyze', 52, 96)
const EMPTY_FOLDER: FolderSummary = summaryOf([])
const LONG_FOLDER_NAME = '2026-09-02 Dolomites, Alta Via 1 from Lago di Braies to Belluno, every hut on the way (final selects)'

const FIRST = '[data-testid="clusters"] [data-testid="cluster"]:first-child'
const SECOND = '[data-testid="clusters"] [data-testid="cluster"]:nth-child(2)'
const COUNTS = '[data-testid="cluster-counts"]'

/** What each state's label names, which must be in view at every size. */
const SUBJECT: Record<number, string> = {
  200: '[aria-label="Loading the folder"]',
  201: '[data-empty-state]',
  202: FIRST,
  203: FIRST,
  204: `[data-testid="filter-input"], ${FIRST}`,
  205: '[data-testid="clusters-empty"]',
  206: '[role="menu"]',
  207: '[data-active-cluster="true"]',
  208: '[data-testid="scan-progress"]',
  209: '[data-testid="scan-progress"]',
  210: FIRST,
  211: SECOND,
  212: FIRST,
  213: FIRST,
  214: '[data-empty-state]',
  215: '[data-testid="recluster"]',
  217: SECOND,
  218: '[role="menu"]',
  219: `[data-testid="device-filter"], ${FIRST}`,
  220: `${SECOND} [data-testid="strip-more"]`,
  221: '[data-testid="failed-count"]',
  222: '[data-overlay="toast:reclustered"]',
  223: '[data-overlay="toast:recluster-failed"]',
  224: '[data-testid="notice-offline"]',
  225: '[data-testid="notice-read-only"]',
  226: '[data-testid="notice-picks-missing"]',
  227: FIRST,
  229: '[data-testid="quality-progress"]',
  230: '[data-testid="scan-checklist"]',
  231: '[data-testid="scan-progress"]',
}

export const scenarios: Scenario[] = [
  folder(200, 'Loading', { clusters: null, folder: summaryOf(REVIEWED) }, NEW),
  folder(201, 'First scan, stage checklist', { clusters: [], folder: EMPTY_FOLDER, scan: scanAt('thumb', 34, 96) }, EMPTY),
  folder(202, 'Filled', { clusters: CLUSTERS }),
  folder(203, 'With picks and rejects', { clusters: REVIEWED }),
  folder(204, 'Filter text with results', { clusters: REVIEWED, filter: { query: 'Sep 3' } }),
  folder(205, 'Filter, no match', { clusters: REVIEWED, filter: { query: 'harbour' } }),
  folder(206, 'Camera menu open', { clusters: REVIEWED, openMenu: 'device' }, GRID, [FOLDER_OVERLAYS.device]),
  folder(207, 'Cluster keyboard-selected', { clusters: REVIEWED, selected: 1 }),
  folder(208, 'Scan running over content', { clusters: REVIEWED, scan: ANALYZING }),
  folder(209, 'Scan stopped', { clusters: REVIEWED, scan: { ...ANALYZING, failed: 2, state: 'stopped' } }),
  folder(210, 'Hide rejected on', {
    // The server leaves rejected photos out; the title bar still counts the
    // folder (one story with the Library row), the filter bar what is shown.
    clusters: hideRejected(REVIEWED),
    folder: summaryOf(REVIEWED),
    hideRejected: true,
  }),
  folder(211, 'Cross-device merged cluster', { clusters: withSecond(REVIEWED, MERGED_CLUSTER) }),
  folder(212, 'Quality badges off', { clusters: unrated(REVIEWED), quality: false }),
  // The folder's last cluster is a clip and two stills: latest first puts it on top.
  folder(213, 'Video cluster (S3)', { clusters: REVIEWED, filter: { sort: 'time-desc' } }),
  folder(214, 'No photos or videos found', { clusters: [], folder: EMPTY_FOLDER }, EMPTY),
  folder(215, 'Re-clustering busy', { clusters: REVIEWED, reclustering: true }),
  folder(217, 'Cluster fully reviewed', { clusters: withSecond(REVIEWED, DONE_CLUSTER) }),
  folder(218, 'Sort menu open', { clusters: REVIEWED, openMenu: 'sort' }, GRID, [FOLDER_OVERLAYS.sort]),
  folder(219, 'Camera filter applied', { clusters: REVIEWED, filter: { device: IPHONE } }),
  folder(220, 'Strip with +N overflow', { clusters: withSecond(REVIEWED, BURST_CLUSTER) }),
  folder(221, 'Failed-files count', { clusters: REVIEWED, failedFiles: 3 }),
  folder(222, 'Re-cluster done toast', { clusters: REVIEWED, toasts: [TOAST_RECLUSTERED] }, TOASTS, [`toast:${TOAST_RECLUSTERED.id}`]),
  folder(223, 'Re-cluster failed toast', { clusters: REVIEWED, toasts: [TOAST_RECLUSTER_FAILED] }, TOASTS, [
    `toast:${TOAST_RECLUSTER_FAILED.id}`,
  ]),
  folder(224, 'Folder went offline while open', { clusters: REVIEWED, folder: summaryOf(REVIEWED, ON_DRIVE), notice: 'offline' }, NEW),
  folder(225, 'Read-only volume while open', { clusters: REVIEWED, folder: summaryOf(REVIEWED, ON_CARD), notice: 'read-only' }, NEW),
  folder(226, 'Picks missing their copies (notice)', { clusters: REVIEWED, picksMissing: 4 }, NEW),
  // Visual grouping off is the user's choice: the page shows no notice for
  // it, so it draws as 203 does (kept as a state so parity proves the app
  // shows none either: its fixture turns visual grouping off). 216 and 228,
  // the notices of a model download, are retired.
  folder(227, 'Visual grouping off (no notice)', { clusters: REVIEWED }, NEW),
  folder(
    229,
    'Title bar with quality indicator',
    { clusters: REVIEWED, qualityStatus: qualityStatus({
        state: 'scoring',
        done: 41,
        total: 95,
        folder_id: EMPTY_FOLDER.id,
        passes: [{ folder_id: EMPTY_FOLDER.id, done: 41, total: 95 }],
      }) },
    'v1:components/primitives.tsx',
  ),
  // The first scan's last stage, counted like the others, while another
  // folder's rating pass runs: the checklist is the progress, the bar shows
  // no other folder's work.
  folder(
    230,
    'First scan, clustering, another folder rating',
    {
      clusters: [],
      folder: EMPTY_FOLDER,
      scan: scanAt('cluster', 52, 96),
      qualityStatus: qualityStatus({
        state: 'scoring',
        done: 108,
        total: 248,
        folder_id: 'lisbon',
        passes: [{ folder_id: 'lisbon', done: 108, total: 248 }],
      }),
    },
    EMPTY,
  ),
  // A name too long for the bar: the title yields, the indicator keeps its label.
  folder(231, 'Long name, scan running (indicator keeps its label)', {
    clusters: REVIEWED,
    folder: { ...summaryOf(REVIEWED), name: LONG_FOLDER_NAME },
    scan: ANALYZING,
  }),
]
