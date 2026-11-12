// Pure rules of Focus and Compare, moved out of v1's Focus.tsx so the screen
// stays presentational and the app's container can apply the same rules to
// keys, clicks and menu items.
import { formatQuality } from '../../format'
import type { ExifPayload, Member } from '../../types'
import { MAX_ZOOM, MIN_ZOOM, ZOOM_STEP, type FocusView, type PickAction } from './props'

export type Mark = { picked: boolean; rejected: boolean }

/** Mirrors the server's picks state machine (v1 applyAction). */
export function applyAction(s: Mark, action: PickAction): Mark {
  switch (action) {
    case 'pick':
      return { picked: true, rejected: false }
    case 'reject':
      return { picked: false, rejected: true }
    case 'unpick':
      return { picked: false, rejected: s.rejected }
    case 'unreject':
      return { picked: s.picked, rejected: false }
  }
}

/** The one action that takes a file from `cur` back to `prev`, or null (v1 undoActionFor). */
export function undoActionFor(prev: Mark, cur: Mark): PickAction | null {
  if (prev.picked) return cur.picked ? null : 'pick'
  if (prev.rejected) return cur.rejected ? null : 'reject'
  if (cur.picked) return 'unpick'
  if (cur.rejected) return 'unreject'
  return null
}

/** What Pick (`picked`) or Reject (`rejected`) sends for this file. */
export function toggleAction(m: Mark, k: 'picked' | 'rejected'): PickAction {
  if (k === 'picked') return m.picked ? 'unpick' : 'pick'
  return m.rejected ? 'unreject' : 'reject'
}

/** The toast title after a successful action (v1). */
export const actionTitle = (a: PickAction): string => (a === 'pick' ? 'Picked' : a === 'reject' ? 'Rejected' : 'Cleared')

/**
 * What Space / X / Pick / Reject act on: the photo on screen, or in compare
 * mode the active pane (nothing when the active index is not a pane).
 */
export function actionTarget(members: Member[], active: number, comparing: boolean, compareIdx: number[]): Member | undefined {
  if (comparing && !compareIdx.includes(active)) return undefined
  return members[active]
}

/**
 * ←/→: through the cluster normally, or among the panes in compare mode.
 * Both wrap. Null when there is nowhere to go.
 */
export function stepIndex(n: number, cur: number, dir: 1 | -1, comparing: boolean, compareIdx: number[]): number | null {
  if (n === 0) return null
  if (comparing) {
    if (compareIdx.length === 0) return null
    const pos = compareIdx.indexOf(cur)
    return pos < 0 ? compareIdx[0]! : compareIdx[(pos + dir + compareIdx.length) % compareIdx.length]!
  }
  return (cur + dir + n) % n
}

/** At most this many compare panes, so the grid stays readable. */
export const MAX_PANES = 4

/**
 * Entering compare keeps the previous selection; the first time, the photo
 * on screen becomes the first (and active) pane.
 */
export function enterCompare(compareIdx: number[], cur: number): { compareIdx: number[]; activeIndex: number } {
  const sel = compareIdx.length > 0 ? compareIdx : [cur]
  return { compareIdx: sel, activeIndex: sel.includes(cur) ? cur : sel[0]! }
}

/**
 * A rail click in compare mode adds or removes member `i`. Null when the
 * selection is full. The active pane moves to the pane that slid into the
 * removed one's place, or to the first one added to an empty selection.
 */
export function compareSelect(prev: number[], i: number, cur: number): { compareIdx: number[]; activeIndex: number } | null {
  let next: number[]
  if (prev.includes(i)) next = prev.filter((x) => x !== i)
  else if (prev.length >= MAX_PANES) return null
  else next = [...prev, i]
  let activeIndex = cur
  if (next.length > 0 && !next.includes(cur)) {
    const removedAt = prev.indexOf(cur)
    activeIndex = removedAt >= 0 ? next[Math.min(removedAt, next.length - 1)]! : next[0]!
  }
  return { compareIdx: next, activeIndex }
}

/** The member index of compare pane `n` (1-based), for the 1–4 keys. */
export const paneIndex = (compareIdx: number[], members: number, n: number): number | undefined =>
  compareIdx.filter((i) => i < members)[n - 1]

/** Columns and rows of the compare grid (v1). */
export function compareGrid(panes: number): { cols: number; rows: number } {
  return { cols: panes <= 1 ? 1 : 2, rows: panes <= 2 ? 1 : 2 }
}

export const clampZoom = (z: number): number => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z))

/** Zoom set to `z`, clamped; at fit the pan resets. */
export function viewAt(z: number, pan: FocusView['pan']): FocusView {
  const zoom = clampZoom(z)
  return zoom <= MIN_ZOOM + 0.001 ? { zoom: MIN_ZOOM, pan: { x: 0, y: 0 } } : { zoom, pan }
}

/** − / + (and the − + = _ keys). */
export const stepZoom = (v: FocusView, dir: 1 | -1): FocusView => viewAt(dir === 1 ? v.zoom * ZOOM_STEP : v.zoom / ZOOM_STEP, v.pan)

/** Zoom scaled by `factor` (a pinch or ⌘-wheel step), clamped. */
export const zoomBy = (v: FocusView, factor: number): FocusView => viewAt(v.zoom * factor, v.pan)

/** `120%`, as the zoom control shows it. */
export { zoomLabel } from '../../format'

/** The rating to show for a member, or undefined. Videos are never rated. */
export function ratingOf(m: Member, ratings: Record<number, number>, qualityOn: boolean): number | undefined {
  if (!qualityOn || m.kind !== 'photo') return undefined
  return ratings[m.file_id] ?? m.quality
}

/** The Info panel's rows, in v1's order; Quality last when rated. */
export function exifRows(exif: ExifPayload, rating: number | undefined): [string, string][] {
  const rows: [string, string][] = []
  if (exif.camera) rows.push(['Camera', exif.camera])
  if (exif.aperture) rows.push(['Aperture', exif.aperture])
  if (exif.shutter) rows.push(['Shutter', exif.shutter])
  if (exif.iso) rows.push(['ISO', exif.iso])
  if (exif.focal_length) rows.push(['Focal', exif.focal_length])
  if (exif.captured) rows.push(['Captured', exif.captured])
  if (exif.size) rows.push(['Size', exif.size])
  if (rating !== undefined) rows.push(['Quality', `${formatQuality(rating)} / 10`])
  return rows
}

/** GPS coordinates as the Info panel shows and links them (v1), or null. */
export function gpsOf(exif: ExifPayload): { label: string; href: string } | null {
  if (typeof exif.gps_lat !== 'number' || typeof exif.gps_lon !== 'number') return null
  return {
    label: `${exif.gps_lat.toFixed(5)}, ${exif.gps_lon.toFixed(5)}`,
    href: `https://www.google.com/maps/search/?api=1&query=${exif.gps_lat},${exif.gps_lon}`,
  }
}

export const reviewedCount = (members: Member[]): number => members.reduce((n, m) => n + (m.picked || m.rejected ? 1 : 0), 0)

/** `07 / 12`: a member's position, as the rail, the hero and the panes show it. */
export const position = (i: number, n: number): string => `${String(i + 1).padStart(2, '0')} / ${String(n).padStart(2, '0')}`

export { baseName, imageLabel } from '../../format'
