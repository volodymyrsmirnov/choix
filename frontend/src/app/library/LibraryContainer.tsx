// The Library container: the design's LibraryScreen, fed from the shell.
// LibraryScreen itself shows the drop zone (no folders), the loading state
// or the timeline, so the container always renders it, as the prototype's
// Library states do — and renders nothing else: the screen's own title bar
// is the window's drag region, and nothing here sits over it.
//
// Sources: the folder list from the shell's Library store (libraryStore.ts,
// alive for the whole shell), each row's scan state and progress from the
// shell's scan store, the quality worker from its store, the selection
// from the shell's actions, `dropActive` from the window
// (windowSources.ts). The replica props stay closed: the ⋯ button
// and a right-click open the native folder menu (System.ShowFolderMenu),
// and Remove is the native alert (Library.Remove, through the actions).
//
// Menu and key actions arrive through the shell's actions: while mounted,
// the container takes over Add Folder (to select the result and say when it
// was already there) and Open Picks (to say when the drive is read-only).
// The screen's own buttons run the same actions.
//
// Keys (libraryKeys.ts): ↓ / ↑ choose a row and ↩ opens it, from a cold
// start too (taken on the window; nothing has to be clicked first). They
// walk the rows the toolbar leaves on screen, in the order shown. Back
// from a folder, its row has the keyboard focus again, so the keys go on
// from where the user was.
//
// The toolbar (search, Show, Sort; libraryFilter.ts): the filter and the
// open toolbar menu are the page's own state, never saved. The screen
// applies the filter to every folder Library.List answers, passed as they
// came. The filter is kept for the session, so back from a folder or
// Settings the Library is as the user left it; the open menu is not. A
// selection the filter hides is cleared, and Add Folder clears a search or
// Show that would hide the folder it selects. Edit ▸ Find (⌘F) focuses the
// search field through the shell's `find` action, as on the Folder page.
//
// Hooks pass a server snapshot to useSyncExternalStore so the test can
// render the container with react-dom/server.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { LibraryScreen } from '../../design/screens/library/LibraryScreen'
import type { LibraryMenu, LibraryProps } from '../../design/screens/library/props'
import type { FolderSummary, LibraryFilter } from '../../design/types'
import { Library, System } from '../shell/api'
import { useShell } from '../shell/context'
import type { Store, StoreSnapshot } from '../shell/store'
import { filterMemory, settleFilter, visibleIds } from './libraryFilter'
import { createLibraryKeyHandler } from './libraryKeys'
import { createLibraryHandlers, libraryView, reportError } from './libraryStore'
import { dropSource, keepScroll, type Source } from './windowSources'

const LIBRARY = { name: 'library' } as const

/** The rows on screen, in the timeline's order. */
const rowElements = (): HTMLElement[] => Array.from(document.querySelectorAll<HTMLElement>('[data-app-root] [data-folder]'))

/** A row's own button (the one control a row is), by folder id. */
const rowButton = (folderId: string): HTMLButtonElement | null =>
  rowElements()
    .find((el) => el.dataset.folder === folderId)
    ?.querySelector<HTMLButtonElement>(':scope > button') ?? null

/** A layout effect in the window; a plain one under react-dom/server, where neither runs. */
const useWindowLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect

function useSnapshot<T>(store: Store<T>): StoreSnapshot<T> {
  return useSyncExternalStore(store.subscribe, store.get, store.get)
}

// Made on first use: there is no window under react-dom/server, where only
// the fallback is read.
let drop: Source<boolean> | null = null
const dropNow = () => (drop ??= dropSource(document, MutationObserver))

function useWindowSource(source: () => Source<boolean>, fallback: boolean): boolean {
  return useSyncExternalStore(
    (onChange) => source().subscribe(onChange),
    () => source().get(),
    () => fallback,
  )
}

export function LibraryContainer() {
  const shell = useShell()
  const { actions, toasts, router } = shell
  const list = useSnapshot(shell.library)
  const scan = useSnapshot(shell.scan)
  const quality = useSnapshot(shell.quality)
  const toastItems = useSyncExternalStore(toasts.subscribe, toasts.items, toasts.items)
  const { selected } = useSyncExternalStore(actions.subscribe, actions.state, actions.state)
  const dropActive = useWindowSource(dropNow, false)

  const view = libraryView({ list, scan: scan.value })

  // The toolbar's state; the latest folders and filter for the window's key
  // handler and Add Folder, which are made once.
  const memory = useMemo(() => filterMemory(shell), [shell])
  const [filter, setFilterState] = useState<LibraryFilter>(() => memory.get())
  const [openMenu, setOpenMenu] = useState<LibraryMenu | null>(null)
  const latest = useRef<{ folders: FolderSummary[]; filter: LibraryFilter }>({ folders: [], filter })
  latest.current = { folders: view.folders ?? [], filter }
  const setFilter = useCallback(
    (f: LibraryFilter) => {
      memory.set(f)
      latest.current = { ...latest.current, filter: f }
      setFilterState(f)
    },
    [memory],
  )

  // After every change: the folder Add Folder selected is decided once its
  // row is listed (the picker's answer and the list's refresh come in either
  // order), and a selected folder the filter does not show is no longer the
  // target of the menu's folder commands.
  const pendingAdd = useRef<string | null>(null)
  useEffect(() => {
    if (!view.folders) return
    const s = settleFilter({ folders: view.folders, filter, selected: actions.state().selected, pending: pendingAdd.current })
    pendingAdd.current = s.pending
    if (s.filter !== filter) setFilter(s.filter)
    if (s.clear) actions.select(null)
  })

  const handlers = useMemo(() => {
    const report = reportError(toasts)
    return createLibraryHandlers({
      library: Library,
      system: System,
      toasts,
      select: (id) => {
        pendingAdd.current = id
        actions.select(id)
      },
      folderName: (id) => shell.library.get().value?.find((f) => f.id === id)?.name,
      onError: report,
    })
  }, [shell, actions, toasts, setFilter])

  // The native menu (File ▸ Add Folder…, the folder menu's Open Picks) and
  // the keys run these while the Library shows.
  useEffect(() => {
    const stops = [
      actions.register('add-folder', () => handlers.add()),
      actions.register('open-picks', (data) => {
        const folderId = data ?? actions.target()
        return folderId === null ? undefined : handlers.openPicks(folderId)
      }),
    ]
    return () => {
      for (const stop of stops) stop()
    }
  }, [actions, handlers])

  useEffect(() => {
    const onKey = createLibraryKeyHandler({
      rows: () => visibleIds(latest.current.folders, latest.current.filter),
      selected: () => actions.state().selected,
      select: actions.select,
      reveal: (id) => rowButton(id)?.focus(),
      open: (id) => actions.runAction('open-folder', id),
      canOpen: (id) => rowElements().find((el) => el.dataset.folder === id)?.dataset.folderState !== 'offline',
      blocked: () => shell.sheet.get() || document.querySelector('[role="dialog"], [role="alertdialog"], [role="menu"]') !== null,
    })
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [shell, actions])

  // The timeline's scroll offset survives a visit to a folder.
  const timeline = view.screen === 'timeline'
  useWindowLayoutEffect(() => {
    if (!timeline) return
    const el = document.querySelector<HTMLElement>('[data-app-root] [data-scroll]')
    if (!el) return
    return keepScroll(el, { offset: () => router.scrollOf(LIBRARY), save: (top) => router.saveScroll(LIBRARY, top) })
  }, [timeline, router])

  // Back from a folder: its row (still the selection) takes the keyboard
  // focus once the timeline shows, without scrolling (the offset is restored
  // above). Once per visit.
  const refocused = useRef(false)
  useEffect(() => {
    if (!timeline || refocused.current) return
    refocused.current = true
    const id = actions.state().selected
    if (id !== null) rowButton(id)?.focus({ preventScroll: true })
  }, [timeline, actions])

  const props: LibraryProps = {
    folders: view.folders,
    dropActive,
    toasts: toastItems,
    selectedId: selected,
    menuFor: null,
    removePrompt: null,
    filter,
    openMenu,
    onFilter: setFilter,
    onOpenMenu: setOpenMenu,
    quality: quality.value ?? null,
    onAdd: () => actions.runAction('add-folder'),
    onOpen: (id) => actions.runAction('open-folder', id),
    onSelect: (id) => actions.select(id),
    onOpenPicks: (id) => actions.runAction('open-picks', id),
    onMenu: handlers.menu,
    onRemove: (id) => actions.runAction('remove-folder', id),
    onConfirmRemove: () => {},
    onSettings: () => actions.runAction('settings'),
    onHelp: () => actions.runAction('shortcuts'),
    onUndoToast: toasts.undo,
    onDismissToast: toasts.dismiss,
  }
  return <LibraryScreen {...props} />
}
