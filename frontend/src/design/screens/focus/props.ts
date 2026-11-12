import type { FocusResponse, FocusView, MediaState, MediaURLs, ToastItem } from '../../types'

/** Overlay ids Focus registers; each is also the key that opens it. */
export const FOCUS_OVERLAYS = {
  /** The Info panel (EXIF, GPS, quality). Not rendered while comparing. */
  info: 'focus-info',
} as const

/** Pixel-peep zoom range and the multiplicative step of − / + (v1). */
export const MIN_ZOOM = 1
export const MAX_ZOOM = 8
export const ZOOM_STEP = 1.5
/** Z toggles between fit and this zoom (v1). */
export const PEEP_ZOOM = 2

/** The server's pick actions (v1 internal/picks state machine). */
export type PickAction = 'pick' | 'unpick' | 'reject' | 'unreject'

export type { FocusView, MediaState } from '../../types'

/**
 * Focus (v1 Focus.tsx) and its compare mode: one cluster, a rail of its
 * members, the hero (or up to four compare panes), the Info panel and the
 * Pick / Reject / Compare / Pixel-peep controls. Everything v1 kept in
 * useState arrives here as a prop; every effect leaves as a callback. The
 * keyboard map (Space, X, ←/→, C, Z, I, + − 0, 1–4, ↵, ⇧↵, Esc, ⌘Z) is the
 * app's: it calls the same callbacks.
 */
export interface FocusProps {
  /** The open folder's name, on the Back button. */
  folder: string
  /** null while the cluster loads (or failed to: see loadError). */
  data: FocusResponse | null
  /** The cluster could not be loaded; shown instead of the stage. */
  loadError: string | null
  /** Index into data.cluster.members of the photo on screen; in compare
   *  mode, of the active pane (which may not be among the panes). */
  activeIndex: number
  /** MIN_ZOOM..MAX_ZOOM, continuous (pinch and ⌘-wheel). Shared by every
   *  compare pane. */
  zoom: number
  pan: { x: number; y: number }
  showInfo: boolean
  comparing: boolean
  /** Member indices shown as compare panes, in pane order (0–4 entries). */
  compareIdx: number[]
  /** Files with a pick mutation in flight: their Pick / Reject are disabled. */
  busyFileIds: number[]
  mediaState: Record<number, MediaState>
  /** Advisory ratings by file id (App's map), over each member's own. */
  ratings: Record<number, number>
  /** The quality rating is switched on: badges show and their room is kept. */
  qualityOn: boolean
  media: MediaURLs
  toasts: ToastItem[]
  /** Back to the folder (Esc with nothing to close, the Back buttons). */
  onBack(): void
  /** Open another cluster (previous / next, the cluster-done pill). */
  onCluster(id: number): void
  /** Show this member: a rail click outside compare, ←/→, a pane click or 1–4. */
  onSelect(index: number): void
  /** In compare mode, a rail click adds or removes a pane (at most four). */
  onCompareSelect(index: number): void
  onCompare(on: boolean): void
  onPick(fileId: number, action: PickAction): void
  /** Zoom and pan changed (− / +, Pixel-peep, ⌘-wheel, pinch, a pan drag). */
  onView(v: FocusView): void
  onInfo(open: boolean): void
  /** A member's full-resolution media finished loading or failed to decode. */
  onMediaState(fileId: number, state: Exclude<MediaState, 'loading'>): void
  /** Load the cluster again after loadError. */
  onRetry(): void
  onHelp(): void
  onSettings(): void
  onUndoToast(id: string): void
  onDismissToast(id: string): void
}
