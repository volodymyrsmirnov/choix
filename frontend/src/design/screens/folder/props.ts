import type { Cluster, FolderFilter, FolderSummary, MediaURLs, QualityStatus, ScanProgress, SettingsBody, ToastItem } from '../../types'

/** Overlay ids the Folder page registers; each is also the key that opens it. */
export const FOLDER_OVERLAYS = {
  /** The camera filter menu (`openMenu === 'device'`). */
  device: 'folder-camera-menu',
  /** The sort menu (`openMenu === 'sort'`). */
  sort: 'folder-sort-menu',
} as const

export type FolderMenu = 'device' | 'sort'

/**
 * How the folder's clusters were formed: by look (`visual`), or by capture
 * time only because visual grouping is off in Settings (`time-off`). The
 * page shows no notice for either: grouping by time is the user's choice.
 */
export type FolderGrouping =
  | 'visual'
  | 'time-off'
  /** @deprecated removed in phase B: nothing is downloaded. */
  | 'time-downloading'
  /** @deprecated removed in phase B: nothing is downloaded. */
  | 'time-failed'

/**
 * The Folder page (spec §4.3): v1's Library cluster grid inside one folder,
 * under a title bar with Back, the folder's name and counts, the scan
 * indicator and Open Picks.
 */
export interface FolderProps {
  /** The Library's cached summary of this folder (name, counts, drive). */
  folder: FolderSummary
  /** The clusters as the backend sends them (rejected photos already left
   *  out when `hide_rejected_photos` is on); null while loading. Filtering
   *  and sorting by `filter` happen in the screen (`filterClusters`). */
  clusters: Cluster[] | null
  /** This folder's job, while it runs or after it stopped; null otherwise. */
  scan: ScanProgress | null
  filter: FolderFilter
  /** Sorted distinct single-camera labels, for the camera menu. */
  devices: string[]
  openMenu: FolderMenu | null
  /** The active cluster (v1's keyboard cursor); null when there is none. */
  selectedClusterId: number | null
  settings: Pick<SettingsBody, 'hide_rejected_photos' | 'quality_rating'>
  grouping: FolderGrouping
  /**
   * The quality worker: shown in the title bar when no scan is (v1 AppBar),
   * and only this folder's work: a rating pass whose folder_id is another
   * folder's is not shown, and nothing is during the first scan (the
   * checklist is the progress).
   */
  quality: QualityStatus | null
  /** A re-cluster is in flight. */
  reclustering: boolean
  /** Files of this folder whose scan failed (left out of the clusters). */
  failedFiles: number
  /** Picked photos whose copy is no longer in the picks folder (reported,
   *  never copied back). */
  picksMissing: number
  /** The folder's drive went away (opening Focus is then unavailable: the
   *  thumbnails, Open Focus, F and ↩), or turned out read-only, while open. */
  notice: 'offline' | 'read-only' | null
  /** The picks folder, relative to the folder (Settings › Output): named by the "picks missing" notice. */
  picksDir: string
  toasts: ToastItem[]
  media: MediaURLs
  onBack(): void
  onOpenPicks(): void
  /** Opens Focus on the cluster, at `fileId` when a thumbnail was clicked. */
  onOpenCluster(id: number, fileId?: number): void
  /** Moves the active cluster (a click on the cluster, ↑/↓, J/K). */
  onSelectCluster(id: number): void
  onFilter(f: FolderFilter): void
  onOpenMenu(m: FolderMenu | null): void
  onRecluster(): void
  /** Reserved for the app container (Edit › Undo, a toast's Undo, a menu
   *  pick on the active cluster); nothing in the screen calls it — the grid
   *  has no pick control, as in v1. */
  onPick(fileId: number, action: 'pick' | 'unpick' | 'reject' | 'unreject'): void
  onSettings(): void
  onHelp(): void
  onUndoToast(id: string): void
  onDismissToast(id: string): void
}
