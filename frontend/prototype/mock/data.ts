// Shared mock data for every scenario group: the synthetic photos
// (tools/make-mock-photos.ts), six invented shoots, and model states. Names,
// counts and dates are invented (the repository is public); nothing here
// may come from the owner's mockup or library (mock/privacy.test.ts).
import type { FolderSummary, ModelStatus, QualityStatus, ScanProgress } from '../../src/design/types'
import times from './photos.json'

const CAPTURED = times as Record<string, string>

/** Mock photo `i` (1–36), a 480×320 JPEG. */
export function photo(i: number): string {
  const name = String(((i - 1) % 36) + 1).padStart(2, '0')
  return new URL(`./photos/p${name}.jpg`, import.meta.url).href
}
/** A URL that loads but does not decode (image-error states). */
export const BROKEN_PHOTO = new URL('./photos/broken.jpg', import.meta.url).href

/** When mock photo `i` was taken. */
export const capturedAt = (i: number): string => CAPTURED[`p${String(i).padStart(2, '0')}.jpg`] ?? ''

// ── Folders ─────────────────────────────────────────────────────────────────

/** Six photos of shoot `k` (0–5), evenly spread over it. */
export const previewsOf = (k: number): string[] => Array.from({ length: 6 }, (_, j) => photo(k * 6 + j + 1))

/**
 * When mock folder `n` was added to the Library (`added_at`, unix seconds):
 * a day apart from 2026-11-01 09:00 UTC, in an order unlike the capture
 * dates', so "Recently added" visibly differs from the timeline.
 */
export const addedAt = (n: number): number => 1793523600 + n * 86400

function shoot(k: number, f: Omit<FolderSummary, 'month' | 'firstCaptured' | 'lastCaptured' | 'previews' | 'added_at'>): FolderSummary {
  const first = capturedAt(k * 6 + 1)
  // Shoots were added oldest trip first, then the Faroe drive's first.
  const added = [3, 1, 5, 0, 4, 2][k] ?? k
  return { ...f, month: first.slice(0, 7), firstCaptured: first, lastCaptured: capturedAt(k * 6 + 6), previews: previewsOf(k), added_at: addedAt(added) }
}

const base = { state: 'ready' as const, videos: 0, picked: 0, rejected: 0 }

export const LISBON = shoot(0, { ...base, id: 'lisbon', name: 'Lisbon', parentPath: '~/Pictures/Trips', photos: 417, videos: 3, clusters: 96 })
/** The one folder with a Folder page and Focus states: its counts are those
 *  of dolomites.ts's clusters (dolomites.test.ts holds them equal). */
export const DOLOMITES = shoot(1, {
  ...base,
  id: 'dolomites',
  name: 'Dolomites',
  parentPath: '~/Pictures/Trips',
  photos: 95,
  videos: 1,
  clusters: 14,
  picked: 8,
  rejected: 7,
})
/** A single-day shoot. */
export const KYOTO = shoot(2, { ...base, id: 'kyoto', name: 'Kyoto', parentPath: '~/Pictures/Trips', photos: 186, clusters: 44, picked: 23, rejected: 15 })
export const MARRAKECH = shoot(3, {
  ...base,
  id: 'marrakech',
  name: 'Marrakech',
  parentPath: '~/Pictures/Trips',
  photos: 74,
  clusters: 19,
  picked: 11,
})
/** The large shoot: a few thousand files, with videos (2,849 in all). */
export const PATAGONIA = shoot(4, {
  ...base,
  id: 'patagonia',
  name: 'Patagonia',
  parentPath: '~/Pictures/Trips',
  photos: 2768,
  videos: 81,
  clusters: 517,
  picked: 142,
  rejected: 203,
})
/** On an external drive. */
export const FAROE = shoot(5, {
  ...base,
  id: 'faroe',
  name: 'Faroe Islands',
  parentPath: '/Volumes/Atlas/Shoots',
  volumeName: 'Atlas',
  photos: 638,
  videos: 22,
  clusters: 133,
  picked: 71,
  rejected: 29,
})

/** The filled Library of state 102: six folders, all ready. */
export const SIX = [LISBON, DOLOMITES, KYOTO, PATAGONIA, MARRAKECH, FAROE]

export const scanAt = (stage: ScanProgress['stage'], done: number, total: number, extra: Partial<ScanProgress> = {}): ScanProgress => ({
  stage,
  done,
  total,
  failed: 0,
  state: 'running',
  ...extra,
})

/** `f` in the middle of a scan: dimmed strip, stage and progress in the row. */
export const scanning = (f: FolderSummary, progress: ScanProgress): FolderSummary => ({ ...f, state: 'scanning', scan: progress })

/** Replaces the folders with these ids by `next` (keeping order). */
export const replace = (list: FolderSummary[], ...next: FolderSummary[]): FolderSummary[] =>
  list.map((f) => next.find((n) => n.id === f.id) ?? f)

// ── Models ──────────────────────────────────────────────────────────────────

export const CLIP_SIZE = 352_000_000
export const QUALITY_SIZE = 177_000_000

export function model(kind: ModelStatus['kind'], state: ModelStatus['state'], received = 0, error?: string): ModelStatus {
  const size = kind === 'clip' ? CLIP_SIZE : QUALITY_SIZE
  return { kind, state, received: state === 'ready' ? size : received, size, ...(error ? { error } : {}) }
}

export const MODELS_READY = [model('clip', 'ready'), model('quality', 'ready')]

export const quality = (q: Partial<QualityStatus>): QualityStatus => ({
  enabled: true,
  state: 'idle',
  active: true,
  received: 0,
  size: 0,
  done: 0,
  total: 0,
  rated: 0,
  ...q,
})
