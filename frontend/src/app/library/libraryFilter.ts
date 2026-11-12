// The Library toolbar's page state in the app. The design applies the
// filter (screens/library/filter.ts: search, Show, Sort) to every folder the
// container passes; this module holds what the app needs around it, with the
// design's own functions so the app and the screen never disagree:
//
// - visibleIds: the rows on screen, in the order shown (the keys walk them).
// - hidesSelection: a selected folder the filter does not show. The container
//   then clears the selection, so a menu command (Open Picks, Remove, Reveal)
//   never acts on a folder the user cannot see.
// - filterAfterAdd: Add Folder selects the folder it added; when the filter
//   would not show it, the search and Show are cleared (the order stays), so
//   the selected row is on screen. That is decided only once the folder's
//   row is in the list: the picker's answer and the list's refresh arrive in
//   either order, so the added folder stays pending until then
//   (settleFilter, run by the container after every change).
// - filterMemory: the filter is never saved, but it is kept for the session
//   (per shell), like the list's scroll offset: back from a folder, or from
//   Settings, the Library is as the user left it. Which toolbar menu is open
//   is not kept: leaving the page closes it.
import { filterFolders, isFiltered, LIBRARY_FILTER, libraryGroups } from '../../design/screens/library/filter'
import type { FolderSummary, LibraryFilter } from '../../design/types'

/** The ids of the folders the screen shows with `filter`, in the order it shows them. */
export function visibleIds(folders: readonly FolderSummary[], filter: LibraryFilter): string[] {
  return libraryGroups(filterFolders([...folders], filter), filter.order).flatMap((g) => g.folders.map((f) => f.id))
}

/** Whether `selected` is a listed folder that `filter` does not show (a folder not listed yet hides nothing). */
export function hidesSelection(folders: readonly FolderSummary[], filter: LibraryFilter, selected: string | null): boolean {
  if (selected === null || !isFiltered(filter)) return false
  const f = folders.find((x) => x.id === selected)
  return f !== undefined && filterFolders([f], filter).length === 0
}

/** The filter once Add Folder's folder `folderId` is listed: without its search and Show when it hides that folder, else unchanged (also while the folder is not listed: nothing to decide yet). */
export function filterAfterAdd(filter: LibraryFilter, folders: readonly FolderSummary[], folderId: string): LibraryFilter {
  if (!isFiltered(filter)) return filter
  const f = folders.find((x) => x.id === folderId)
  if (f === undefined || filterFolders([f], filter).length === 1) return filter
  return { ...filter, query: '', show: 'all' }
}

export interface SettleInput {
  folders: readonly FolderSummary[]
  filter: LibraryFilter
  selected: string | null
  /** The folder Add Folder selected, until its row is listed. */
  pending: string | null
}

export interface Settled {
  filter: LibraryFilter
  pending: string | null
  /** The selection is a folder the filter hides: clear it. */
  clear: boolean
}

/**
 * One pass over the page state after any change (the list, the filter, the
 * selection, the picker's answer): the pending added folder is decided once
 * it is listed (filterAfterAdd) and forgotten when the selection moved on;
 * then a selection the filter hides is to be cleared.
 */
export function settleFilter(i: SettleInput): Settled {
  let pending = i.pending !== null && i.pending === i.selected ? i.pending : null
  let filter = i.filter
  if (pending !== null && i.folders.some((f) => f.id === pending)) {
    filter = filterAfterAdd(filter, i.folders, pending)
    pending = null
  }
  return { filter, pending, clear: hidesSelection(i.folders, filter, i.selected) }
}

export interface FilterMemory {
  get(): LibraryFilter
  set(f: LibraryFilter): void
}

const remembered = new WeakMap<object, LibraryFilter>()

/** The Library filter kept for `owner` (the shell): LIBRARY_FILTER until set. */
export function filterMemory(owner: object): FilterMemory {
  return {
    get: () => remembered.get(owner) ?? LIBRARY_FILTER,
    set: (f) => void remembered.set(owner, f),
  }
}
