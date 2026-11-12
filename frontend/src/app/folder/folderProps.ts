// The Folder screen's props from what the container holds: the folder store's
// snapshot, the folder's Library row, the shared stores' values, the page's
// own filter, menu and cursor, and its handlers. Pure, so every prop's source
// is tested without a window.
import { filterClusters } from '../../design/screens/folder/filter'
import type { FolderMenu, FolderProps } from '../../design/screens/folder/props'
import type { Cluster, FolderFilter, FolderSummary, MediaURLs, QualityStatus, ScanProgress, SettingsBody } from '../../design/types'
import type { ToastProps } from '../shell/context'
import type { FolderScan } from '../shell/scanStore'
import type { FolderData, FolderSnapshot } from './folderStore'

/** A scan that has begun (or is about to) before its first progress event: the indicator is indeterminate. */
export const SCAN_STARTING: ScanProgress = { stage: 'discover', done: 0, total: 0, failed: 0, state: 'running' }

export const DEFAULT_FILTER: FolderFilter = { query: '', device: '', sort: 'time-asc' }

/** The defaults of the two settings the page reads, until Settings.Get answers (config defaults). */
const SETTINGS_DEFAULT = { hide_rejected_photos: false, quality_rating: true }

/**
 * The folder's job for the title bar and the first-scan checklist: its
 * progress (running, or stopped by a drive going away or an error), the
 * starting look while it scans without progress yet, and the same for a
 * folder queued before its first scan (no clusters), so a folder just added
 * shows the checklist instead of "No photos or videos found".
 */
export function scanOf(entry: FolderScan | undefined, clusters: Cluster[] | null): ScanProgress | null {
  if (!entry) return null
  if (entry.progress) return entry.progress
  if (entry.state === 'scanning') return SCAN_STARTING
  if (entry.state === 'queued' && clusters !== null && clusters.length === 0) return SCAN_STARTING
  return null
}

/**
 * The title bar's folder: its Library row; until that has loaded, what the
 * grid says (name and counts); before either, an unnamed folder.
 */
export function summaryOf(folderId: string, row: FolderSummary | null | undefined, data: FolderData | undefined): FolderSummary {
  if (row) return row
  let photos = 0
  let videos = 0
  let rejected = 0
  for (const c of data?.clusters ?? []) {
    for (const m of c.members) {
      if (m.kind === 'video') videos++
      else photos++
      if (m.rejected) rejected++
    }
  }
  return {
    id: folderId,
    name: data?.name ?? '',
    parentPath: '',
    state: 'ready',
    month: null,
    firstCaptured: null,
    lastCaptured: null,
    photos,
    videos,
    clusters: data?.clusters.length ?? 0,
    picked: data?.picked ?? 0,
    rejected,
    previews: [],
  }
}

/** v1: a remembered camera that no longer has clusters is dropped, rather than showing "No clusters match". */
export function usableFilter(f: FolderFilter, devices: string[]): FolderFilter {
  return f.device === '' || devices.includes(f.device) ? f : { ...f, device: '' }
}

/** v1: the cursor stays on the list; when its cluster is not shown, the first shown one is active. */
export function selectionOf(visible: Cluster[], chosen: number | null): number | null {
  return visible.some((c) => c.id === chosen) ? chosen : (visible[0]?.id ?? null)
}

export type FolderHandlers = Pick<
  FolderProps,
  | 'onBack'
  | 'onOpenPicks'
  | 'onOpenCluster'
  | 'onSelectCluster'
  | 'onFilter'
  | 'onOpenMenu'
  | 'onRecluster'
  | 'onPick'
  | 'onSettings'
  | 'onHelp'
>

export interface FolderInputs {
  folderId: string
  snap: FolderSnapshot
  /** The folder's Library row; null when it is not in the Library, undefined before List answered. */
  row: FolderSummary | null | undefined
  /** The folder's entry in the shared scan store. */
  scan: FolderScan | undefined
  settings: SettingsBody | undefined
  quality: QualityStatus | null | undefined
  filter: FolderFilter
  chosen: number | null
  openMenu: FolderMenu | null
  toasts: ToastProps
  media: MediaURLs
  handlers: FolderHandlers
}

/** Every FolderProps field, from its source. */
export function folderScreenProps(i: FolderInputs): FolderProps {
  const data = i.snap.value
  const clusters = data?.clusters ?? null
  const devices = data?.devices ?? []
  const filter = usableFilter(i.filter, devices)
  return {
    folder: summaryOf(i.folderId, i.row, data),
    clusters,
    scan: scanOf(i.scan, clusters),
    filter,
    devices,
    openMenu: i.openMenu,
    selectedClusterId: clusters ? selectionOf(filterClusters(clusters, filter), i.chosen) : null,
    settings: {
      hide_rejected_photos: i.settings?.hide_rejected_photos ?? SETTINGS_DEFAULT.hide_rejected_photos,
      quality_rating: i.settings?.quality_rating ?? SETTINGS_DEFAULT.quality_rating,
    },
    quality: i.quality ?? null,
    reclustering: i.snap.reclustering,
    failedFiles: data?.failed ?? 0,
    picksMissing: data?.picksMissing ?? 0,
    // The drive went away under a running job (scan:state offline) before the session heard of it.
    notice: i.scan?.state === 'offline' ? 'offline' : i.snap.notice,
    picksDir: data?.picksDir ?? i.settings?.picks_dir ?? '',
    toasts: i.toasts.toasts,
    onUndoToast: i.toasts.onUndoToast,
    onDismissToast: i.toasts.onDismissToast,
    media: i.media,
    ...i.handlers,
  }
}
