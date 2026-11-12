// The Library toolbar's search, filter and order: pure, so the screen and its
// tests share one implementation. The app passes every folder and the
// toolbar's LibraryFilter; the screen shows filterFolders(…), grouped by
// libraryGroups(…), as the Folder page does with its clusters.
import type { FolderSummary, LibraryFilter } from '../../types'
import { monthGroups } from './months'

/** No search, capture date newest first (the month timeline), every folder. */
export const LIBRARY_FILTER: LibraryFilter = { query: '', order: 'date-desc', show: 'all' }

/** The search or the filter hides folders (the order never does). */
export function isFiltered(filter: LibraryFilter): boolean {
  return filter.query.trim() !== '' || filter.show !== 'all'
}

/** The query (trimmed, ignoring case) is in the folder's location and name, read as one path: `~/Pictures/Trips/Lisbon`. */
export function matchesQuery(f: FolderSummary, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return `${f.parentPath}/${f.name}`.toLowerCase().includes(q)
}

/** Whether the folder is one the filter shows. */
export function matchesShow(f: FolderSummary, show: LibraryFilter['show']): boolean {
  const marked = f.picked + f.rejected > 0
  switch (show) {
    case 'unreviewed':
      return !marked && f.photos + f.videos > 0
    case 'started':
      return marked
    case 'attention':
      return f.state === 'offline' || f.state === 'error'
    default:
      return true
  }
}

/** The folders the search and the filter show, in the order given. */
export function filterFolders(folders: FolderSummary[], filter: LibraryFilter): FolderSummary[] {
  return folders.filter((f) => matchesShow(f, filter.show) && matchesQuery(f, filter.query))
}

/** A run of folders on the Library page: a month with its header, or (`label` null) the one flat list of a name or added order. */
export interface LibraryGroup {
  key: string
  label: string | null
  folders: FolderSummary[]
}

const byName = (a: FolderSummary, b: FolderSummary) =>
  a.name.localeCompare(b.name, 'en', { numeric: true, sensitivity: 'base' }) || a.id.localeCompare(b.id)

/** Recently added first (`added_at`, unix seconds); a folder without it after every folder with it; ties by name. */
const byAdded = (a: FolderSummary, b: FolderSummary) => {
  const x = a.added_at
  const y = b.added_at
  if (x === undefined || y === undefined) return x === y ? byName(a, b) : x === undefined ? 1 : -1
  return y - x || byName(a, b)
}

/**
 * The folders in `order`: the capture-date orders keep the month timeline
 * (Processing first, then the months newest or oldest first, Undated last);
 * name and recently added are one list without month headers or a
 * Processing section, since they are not grouped (and recently added puts a
 * new folder first anyway). Recently
 * added sorts on `added_at` alone (the order the folders arrive in means
 * nothing: Library.List answers in capture-date order).
 */
export function libraryGroups(folders: FolderSummary[], order: LibraryFilter['order']): LibraryGroup[] {
  if (folders.length === 0) return []
  if (order === 'date-desc' || order === 'date-asc') return monthGroups(folders, order === 'date-desc' ? 'newest' : 'oldest')
  return [{ key: order, label: null, folders: [...folders].sort(order === 'name' ? byName : byAdded) }]
}
