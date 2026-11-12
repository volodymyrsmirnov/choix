import type { LibraryMenu } from '../../components/LibraryFilterBar'
import type { FolderSummary, LibraryFilter, ModelStatus, QualityStatus, ScanProgress, ToastItem } from '../../types'

export type { LibraryMenu }

/** Overlay ids the Library registers; each is also the key that opens it. */
export const LIBRARY_OVERLAYS = {
  /** A folder row's context menu (replica of the native one). */
  menu: 'folder-menu',
  /** The "Remove from Library" alert (replica of the native one; dialogs.json). */
  remove: 'remove-folder',
  /** The toolbar's order menu (`openMenu === 'order'`). */
  order: 'library-order-menu',
  /** The toolbar's filter menu (`openMenu === 'show'`). */
  show: 'library-show-menu',
} as const

/**
 * The Library (spec §4.1), the app's home screen. With no folders it shows
 * the drop zone as soon as the list has loaded.
 */
export interface LibraryProps {
  /** Every folder in the Library, as Library.List answers (the screen searches, filters and orders them with `filter`); null while loading. */
  folders: FolderSummary[] | null
  /**
   * The toolbar: the search, which folders to show, and their order. The
   * screen applies it (screens/library/filter.ts) and draws the toolbar
   * whenever there are folders. Optional only so the app compiles until it
   * holds it; absent is LIBRARY_FILTER (no search, newest first, every
   * folder). Its own state: never saved.
   */
  filter?: LibraryFilter
  /** The toolbar menu that is open, if any. */
  openMenu?: LibraryMenu | null
  /** The user changed the toolbar (typed, chose a filter or an order, cleared it). */
  onFilter?(f: LibraryFilter): void
  /** A toolbar menu opens (or closes: null). */
  onOpenMenu?(m: LibraryMenu | null): void
  /** @deprecated removed in phase B: ignored, nothing is downloaded. */
  models?: ModelStatus[]
  /** Folders are being dragged over the window. */
  dropActive: boolean
  toasts: ToastItem[]
  selectedId: string | null
  /** Prototype only: one row's forced `hover` or `focus` look. */
  forceRow?: { id: string; force: string }
  /**
   * @deprecated Not drawn: each scan shows in its folder's row (the title
   * bar shows no status another element shows). Kept, optional, so the app,
   * which still feeds it, compiles; remove it with libraryView's indicator.
   */
  scan?: { folder: string; progress: ScanProgress } | { count: number } | null
  /** Row whose context-menu replica is open. */
  menuFor: string | null
  /** Row whose Remove alert replica is open. */
  removePrompt: string | null
  /** @deprecated removed in phase B: ignored, nothing is downloaded. */
  online?: boolean
  /**
   * The quality worker: each rating pass in flight shows in its folder's row
   * (from `passes`, or done/total for the folder_id when Go lists none). The
   * title bar shows no quality status: the rows already do.
   */
  quality: QualityStatus | null
  onAdd(): void
  onOpen(id: string): void
  onSelect(id: string): void
  onOpenPicks(id: string): void
  onMenu(id: string | null, at?: { x: number; y: number }): void
  onRemove(id: string): void
  onConfirmRemove(id: string, confirmed: boolean): void
  onSettings(): void
  onHelp(): void
  /** @deprecated removed in phase B: ignored, nothing is downloaded. */
  onRetryModel?(kind: ModelStatus['kind']): void
  /** @deprecated removed in phase B: ignored, nothing is downloaded. */
  onToggleModel?(kind: ModelStatus['kind'], on: boolean): void
  onUndoToast(id: string): void
  onDismissToast(id: string): void
}
