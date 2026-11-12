// Wires a LibraryScreen state to the prototype store, so the Welcome, shell
// and Library states are clickable. Not a scenario file (.ts, so
// scenarios/index.ts does not collect it).
//
// Store keys: `selectedId`, `dropActive`, `removed` (ids), `libraryFilter`
// (the toolbar), and the overlay ids
// `folder-menu` and `remove-folder` (a folder id while open, false when
// closed), `library-order-menu` and `library-show-menu` (true while open),
// `toast:<id>`.
import type { LibraryProps } from '../../src/design/screens/library/props'
import { LIBRARY_OVERLAYS } from '../../src/design/screens/library/props'
import { LIBRARY_FILTER } from '../../src/design/screens/library/filter'
import type { FolderSummary, LibraryFilter, QualityStatus, ToastItem } from '../../src/design/types'
import type { ScenarioCtx } from '../registry'
import { read, toastsOf } from '../store'

export interface LibraryState {
  folders: FolderSummary[] | null
  dropActive?: boolean
  toasts?: ToastItem[]
  selectedId?: string | null
  forceRow?: { id: string; force: string }
  scan?: LibraryProps['scan']
  quality?: QualityStatus | null
  menuFor?: string | null
  removePrompt?: string | null
  /** The toolbar's search, filter and order (defaults: LIBRARY_FILTER). */
  filter?: Partial<LibraryFilter>
  /** The toolbar menu shown open. */
  openMenu?: LibraryProps['openMenu']
}

// Where a click leads in the prototype. Only the Dolomites folder has a
// Folder page (s203, whose counts are its row's); the others say so.
const GO = { added: 30, addedMore: 105, folder: 203, settings: 500, shortcuts: 600 }
const WITH_PAGE = 'dolomites'

export function libraryProps(ctx: ScenarioCtx, s: LibraryState): LibraryProps {
  const { state, set, go, toast } = ctx.store
  const removed = read<string[]>(state, 'removed', [])
  const folders = s.folders && s.folders.filter((f) => !removed.includes(f.id))
  const name = (id: string) => s.folders?.find((f) => f.id === id)?.name ?? id
  const open = (key: string, dflt: string | null | undefined) => read<string | false | null>(state, key, dflt ?? null) || null
  const filter = read<LibraryFilter>(state, 'libraryFilter', { ...LIBRARY_FILTER, ...s.filter })
  const menuOpen = (key: string, dflt: boolean) => read<boolean>(state, key, dflt)
  const openMenu = menuOpen(LIBRARY_OVERLAYS.order, s.openMenu === 'order')
    ? 'order'
    : menuOpen(LIBRARY_OVERLAYS.show, s.openMenu === 'show')
      ? 'show'
      : null

  return {
    folders,
    dropActive: read(state, 'dropActive', s.dropActive ?? false),
    toasts: toastsOf(state, s.toasts),
    selectedId: read(state, 'selectedId', s.selectedId ?? null),
    forceRow: s.forceRow,
    scan: s.scan ?? null,
    quality: s.quality ?? null,
    menuFor: open(LIBRARY_OVERLAYS.menu, s.menuFor),
    removePrompt: open(LIBRARY_OVERLAYS.remove, s.removePrompt),
    filter,
    openMenu,
    onFilter: (f) => set({ libraryFilter: f }),
    onOpenMenu: (m) => set({ [LIBRARY_OVERLAYS.order]: m === 'order', [LIBRARY_OVERLAYS.show]: m === 'show' }),
    onAdd: () => go(folders && folders.length > 0 ? GO.addedMore : GO.added),
    onOpen: (id) =>
      id === WITH_PAGE
        ? go(GO.folder)
        : toast({ id: `no-page-${id}`, tone: 'info', title: `“${name(id)}” has no folder page in the prototype`, body: 'Open “Dolomites” to see the Folder page.' }),
    onSelect: (id) => set({ selectedId: id }),
    onOpenPicks: (id) =>
      toast({ id: `picks-${id}`, tone: 'info', title: `Finder shows the picks of “${name(id)}”`, body: 'The picks folder opens in a Finder window.' }),
    onMenu: (id) => set({ [LIBRARY_OVERLAYS.menu]: id ?? false }),
    onRemove: (id) => set({ [LIBRARY_OVERLAYS.menu]: false, [LIBRARY_OVERLAYS.remove]: id, selectedId: id }),
    onConfirmRemove: (id, confirmed) =>
      set({
        [LIBRARY_OVERLAYS.remove]: false,
        ...(confirmed ? { removed: [...removed, id], selectedId: null } : {}),
      }),
    onSettings: () => go(GO.settings),
    onHelp: () => go(GO.shortcuts),
    onUndoToast: (id) => set({ [`toast:${id}`]: false }),
    onDismissToast: (id) => set({ [`toast:${id}`]: false }),
  }
}
