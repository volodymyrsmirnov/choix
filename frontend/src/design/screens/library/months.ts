// The Library's month timeline (Layout A): a folder sits under the month of
// its earliest capture, months run newest first (or oldest first, from the
// toolbar's order), and folders without dated media go under "Undated" at
// the end either way. Folders still being processed for the first time go
// under "Processing" at the top (the owner: a new folder must not vanish
// at the bottom of a long Library).
import { monthLabel, plural } from '../../format'
import type { FolderSummary } from '../../types'

export interface MonthGroup { key: string; label: string; folders: FolderSummary[] }

/**
 * A folder whose job runs or waits (state `scanning` or `queued`) and that
 * has no capture month yet: a new folder before its first job ends (Go
 * writes a folder's dates when its job ends). A rescan of a dated folder is
 * not processing; it stays in its month.
 */
export function isProcessing(f: FolderSummary): boolean {
  return (f.state === 'scanning' || f.state === 'queued') && f.month === null
}

/** Most recently added first (`added_at`; a folder without it last), then by name. */
const byAddedThenName = (a: FolderSummary, b: FolderSummary) =>
  (b.added_at ?? -Infinity) - (a.added_at ?? -Infinity) || a.name.localeCompare(b.name)

export function monthGroups(folders: FolderSummary[], direction: 'newest' | 'oldest' = 'newest'): MonthGroup[] {
  const sign = direction === 'newest' ? -1 : 1
  const processing = folders.filter(isProcessing).sort(byAddedThenName)
  const byKey = new Map<string, FolderSummary[]>()
  for (const f of folders) {
    if (isProcessing(f)) continue
    const key = f.month ?? ''
    byKey.set(key, [...(byKey.get(key) ?? []), f])
  }
  const keys = [...byKey.keys()].sort((a, b) => (a === '' ? 1 : b === '' ? -1 : sign * a.localeCompare(b)))
  const months = keys.map((key) => ({
    key: key || 'undated',
    label: key ? monthLabel(key) : 'Undated',
    folders: [...(byKey.get(key) ?? [])].sort(
      (a, b) => sign * (a.firstCaptured ?? '').localeCompare(b.firstCaptured ?? '') || a.name.localeCompare(b.name),
    ),
  }))
  return processing.length > 0 ? [{ key: 'processing', label: 'Processing', folders: processing }, ...months] : months
}

/** The month header's right-aligned count: `2 folders · 2,768 photos · 81 videos` (Processing: `2 folders`). */
export function monthCount(folders: FolderSummary[]): string {
  const parts = [plural(folders.length, 'folder', 'folders')]
  // Processing folders' photos are not counted until their job ends.
  if (folders.length > 0 && folders.every(isProcessing)) return parts[0]!
  if (folders.every((f) => f.state === 'offline')) return `${parts[0]} · offline`
  const photos = folders.reduce((n, f) => n + f.photos, 0)
  const videos = folders.reduce((n, f) => n + f.videos, 0)
  if (photos > 0 || videos === 0) parts.push(plural(photos, 'photo', 'photos'))
  if (videos > 0) parts.push(plural(videos, 'video', 'videos'))
  return parts.join(' · ')
}

/** The summary line above the list: `6 folders` and `3,894 photos · 90 videos · 229 picked`. */
export function librarySummary(folders: FolderSummary[]): { title: string; detail: string } {
  const sum = (k: 'photos' | 'videos' | 'picked') => folders.reduce((n, f) => n + f[k], 0)
  const detail = [plural(sum('photos'), 'photo', 'photos')]
  if (sum('videos') > 0) detail.push(plural(sum('videos'), 'video', 'videos'))
  if (sum('picked') > 0) detail.push(plural(sum('picked'), 'picked', 'picked'))
  return { title: plural(folders.length, 'folder', 'folders'), detail: detail.join(' · ') }
}
