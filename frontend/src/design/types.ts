// Data shapes the design components and screens render. The first block is
// moved unchanged from v1 (internal/ui/web/src/api.ts); the rest is new in
// Choix 2.0. Nothing here fetches: screens receive these as props.

// ── From v1 ────────────────────────────────────────────────────────────────

export interface Member {
  file_id: number
  path: string
  content_hash: string
  picked: boolean
  rejected: boolean
  kind: 'photo' | 'video'
  format: string
  // Advisory technical-quality rating, 0–10 with one decimal (0 = worst).
  // Absent for videos, unrated photos and when the feature is off.
  quality?: number
}

export interface Cluster {
  id: number
  device: string
  time: string
  members: Member[]
}

export interface ExifPayload {
  shutter?: string
  aperture?: string
  iso?: string
  focal_length?: string
  camera?: string
  captured?: string
  size?: string
  gps_lat?: number
  gps_lon?: number
}

export interface FocusResponse {
  cluster: Cluster
  cluster_index: number
  cluster_count: number
  exif: Record<string, ExifPayload>
  next_cluster_id: number
  prev_cluster_id: number
}

export interface SettingsBody {
  bucket_size: number
  similarity: number
  picks_dir: string
  advance_on_action: boolean
  hide_rejected_photos: boolean
  cross_device_merging: boolean
  quality_rating: boolean
  // New in 2.0: off means no feature prints are made and folders cluster by
  // time only.
  visual_grouping: boolean
}

// Snapshot of the background quality worker.
export type QualityState =
  | 'disabled'
  | 'waiting'
  | 'scoring'
  | 'idle'
export interface QualityStatus {
  enabled: boolean
  state: QualityState
  // Keep polling while true: work is in flight or queued, or a scan is
  // running that will hand the worker photos.
  active: boolean
  done: number     // photos rated in the current pass
  total: number
  // Photos dealt with since choix started; only grows.
  rated: number
  // The folder of the pass that done/total describe (the latest begun);
  // absent when no pass runs.
  folder_id?: string
  // Every rating pass in flight, oldest first (two folder jobs may run at
  // once); absent when none runs. A Folder page shows only its own pass,
  // the Library names one pass's folder and counts several.
  passes?: QualityPass[]
}

/** One folder's rating pass. */
export interface QualityPass {
  folder_id: string
  done: number // photos of this folder's pass dealt with
  total: number // photos pending when the pass began
}

export interface ProgressEvent {
  stage: string
  done: number
  total: number
  failed: number
  phase?: string
}

// ── New in 2.0 ─────────────────────────────────────────────────────────────

export type FolderState = 'ready' | 'scanning' | 'queued' | 'offline' | 'error'

export interface ScanProgress {
  stage: 'discover' | 'metadata' | 'thumb' | 'analyze' | 'cluster'
  done: number
  total: number
  failed: number
  state: 'running' | 'stopped'
}

export interface FolderSummary {
  id: string
  name: string
  parentPath: string
  state: FolderState
  month: string | null /* 'YYYY-MM' */
  firstCaptured: string | null
  lastCaptured: string | null
  photos: number
  videos: number
  clusters: number
  picked: number
  rejected: number
  previews: string[] /* ≤ 6 URLs */
  scan?: ScanProgress
  volumeName?: string
  error?: string
  /**
   * When the folder was added to the Library: library.db's `folders.added_at`,
   * unix seconds, sent by Go for every folder (json `added_at`). The
   * "Recently added" order sorts on it alone; a row without it sorts after
   * every row that has it, by name. Optional in the type only so the app's
   * own literals compile until the binding carries it.
   */
  added_at?: number
}

export type ScreenId = 'library' | 'folder' | 'focus' | 'compare' | 'settings'

export interface MediaURLs {
  thumb(m: Member, tier?: 'thumb' | 'preview'): string
  full(m: Member): string
}

export interface ToastItem {
  id: string
  tone: 'pick' | 'reject' | 'warn' | 'info'
  title: string
  body?: string
  undo?: boolean
}

export interface FolderFilter {
  query: string
  device: string /* '' = all cameras */
  sort: 'time-asc' | 'time-desc'
}

/** The Library toolbar: a search, which folders to show and their order (screens/library/filter.ts). */
export interface LibraryFilter {
  /** Matches the folder's name and where it is (its parent path), trimmed, ignoring case. */
  query: string
  /** `date-desc`: capture date, newest first (the month timeline); `date-asc`: oldest first; `name`: A to Z; `added`: recently added first. */
  order: 'date-desc' | 'date-asc' | 'name' | 'added'
  /** `unreviewed`: no pick or reject yet; `started`: at least one; `attention`: offline or in error. */
  show: 'all' | 'unreviewed' | 'started' | 'attention'
}

export type MenuItemDef =
  | {
      id: string
      label: string
      accelerator?: string
      display?: string
      enabledIn: ScreenId[]
      /** Needs a target folder: the selected row on the Library, the open
       *  folder elsewhere. */
      needsSelection?: boolean
      hiddenWhen?: string[]
    }
  | { separator: true }
  /** A native role item; `label` is the text macOS shows for it. */
  | { role: string; label: string }

export interface MenuDef {
  id: string
  title: string
  items: MenuItemDef[]
}

/**
 * Where one member's full-resolution media is, on the Focus stage or in a
 * compare pane: `loading` shows the thumb tier (over a spinner while nothing
 * has painted yet) and loads the full image unseen, `ready` the full image,
 * `error` the error look. A member missing from the map is `ready`.
 */
export type MediaState = 'loading' | 'ready' | 'error'

/** Focus's pixel-peep zoom and pan, shared by every compare pane. */
export interface FocusView {
  zoom: number
  pan: { x: number; y: number }
}
