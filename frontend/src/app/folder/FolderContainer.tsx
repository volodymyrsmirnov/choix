// The Folder page (#/folder/<id>): the design's FolderScreen, fed from the
// open folder's store (folderStore.ts), its Library row, and the shell's
// shared stores. It renders the screen and nothing else, so the screen's
// title bar is the window's drag region with nothing over it.
//
// - Filter, sort, the open menu and the active cluster are the page's own
//   state. Filter and cursor are remembered on the folder's store (v1 kept
//   them for the page lifetime) and restored on the way back from Focus or
//   Settings, together with the router's scroll offset; a folder opened
//   afresh from the Library starts at the top with no filter.
// - Opening a cluster goes to #/folder/<id>/focus/<clusterId>; the clicked
//   thumbnail is left on the store for Focus (takeFocusEntry). While the
//   folder is offline, Focus does not open (design ruling).
// - Menu and keys: Photo > Open in Focus and Edit > Undo are this page's
//   (actions.register); Back, Open Picks, Settings and the shortcut sheet
//   are the shell's defaults, which the screen's buttons run too.
// - A folder that could not be opened at all goes back to the Library with
//   the reason in a toast: the design has no state for it.
import { useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { actionTitle } from '../../design/screens/focus/logic'
import { filterClusters } from '../../design/screens/folder/filter'
import { FolderScreen } from '../../design/screens/folder/FolderScreen'
import type { FolderMenu } from '../../design/screens/folder/props'
import type { FolderFilter } from '../../design/types'
import { Library } from '../shell/api'
import { useShell, useToastProps } from '../shell/context'
import { appError, errorText, isQuitting } from '../shell/errors'
import { on } from '../shell/events'
import { mediaFor } from '../shell/media'
import type { Route } from '../shell/router'
import { useStore } from '../shell/store'
import { ACTION_FAILED_TITLE, pushPickToast, undoWithToast } from '../shell/toasts'
import { DEFAULT_FILTER, folderScreenProps, selectionOf, usableFilter } from './folderProps'
import { createSummaryStore } from './folderStore'
import { reclusterWithToast } from './recluster'
import { useFolderStore } from './useFolderStore'

/** The scroller FolderScreen renders around the clusters. */
const SCROLLER = 'main[data-testid="clusters"]'

export function FolderContainer({ folderId }: { folderId: string }) {
  const shell = useShell()
  const store = useFolderStore(folderId)
  const [summaryStore] = useState(() => createSummaryStore({ folderId, list: () => Library.List(), on }))
  useEffect(() => store.start(), [store])
  useEffect(() => summaryStore.start(), [summaryStore])

  const snap = useSyncExternalStore(store.subscribe, store.get)
  const row = useStore(summaryStore)
  const scanMap = useStore(shell.scan).value
  const models = useStore(shell.models).value
  const quality = useStore(shell.quality).value
  const settings = useStore(shell.settings).value
  const toasts = useToastProps()
  const media = useMemo(() => mediaFor(folderId), [folderId])

  // Back from Focus or Settings: the page as it was left. From the Library: afresh.
  const [restored] = useState(() => store.memory.visited)
  const [filter, setFilter] = useState<FolderFilter>(() => (restored ? store.memory.filter : DEFAULT_FILTER))
  const [chosen, setChosen] = useState<number | null>(() => (restored ? store.memory.selectedClusterId : null))
  const [openMenu, setOpenMenu] = useState<FolderMenu | null>(null)
  useEffect(() => {
    store.memory.visited = true
    store.memory.filter = filter
    store.memory.selectedClusterId = chosen
  }, [store, filter, chosen])

  const offline = (scanMap?.[folderId]?.state === 'offline' ? 'offline' : snap.notice) === 'offline'
  const clusters = snap.value?.clusters ?? null

  const failed = (title: string, e: unknown) => {
    const err = appError(e)
    if (isQuitting(err)) return
    shell.toasts.push({ tone: 'reject', title, body: errorText(err) })
  }

  const openCluster = (id: number, fileId?: number) => {
    if (offline) return
    store.setFocusEntry(id, fileId)
    // Remembered now: the page unmounts with this navigation, before its effects run again.
    store.memory.selectedClusterId = id
    setChosen(id)
    shell.router.navigate({ name: 'focus', folderId, clusterId: id })
  }

  // The handlers the native menu reaches read the page as it is now.
  const latest = useRef({ openCluster, active: null as number | null })
  const visible = useMemo(
    () => (clusters ? filterClusters(clusters, usableFilter(filter, snap.value?.devices ?? [])) : []),
    [clusters, filter, snap.value?.devices],
  )
  latest.current = { openCluster, active: selectionOf(visible, chosen) }

  useEffect(() => {
    const offs = [
      shell.actions.register('open-focus', () => {
        const { active, openCluster: open } = latest.current
        if (active !== null) open(active)
      }),
      // The grid shows what the undo changed (Go sends no event for it); the store is this session's UndoTarget.
      shell.actions.register('undo', () => undoWithToast(shell.toasts, store)),
    ]
    return () => {
      for (const off of offs) off()
    }
  }, [shell, store])

  // Scroll per route (the router's): restored once the clusters are there.
  const restoreScroll = useRef(restored)
  const hasGrid = clusters !== null && clusters.length > 0
  useLayoutEffect(() => {
    if (!hasGrid) return
    const el = document.querySelector<HTMLElement>(SCROLLER)
    if (!el) return
    const route: Route = { name: 'folder', folderId }
    if (restoreScroll.current) {
      el.scrollTop = shell.router.scrollOf(route)
      restoreScroll.current = false
    } else {
      shell.router.saveScroll(route, el.scrollTop)
    }
    const onScroll = () => shell.router.saveScroll(route, el.scrollTop)
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [hasGrid, folderId, shell])

  // Nothing could be loaded (the folder did not open): back to the Library.
  useEffect(() => {
    if (snap.status !== 'error' || snap.value !== undefined) return
    failed(ACTION_FAILED_TITLE, snap.error)
    shell.router.navigate({ name: 'library' }, { force: true })
  }, [snap.status, snap.value, snap.error])

  return (
    <FolderScreen
      {...folderScreenProps({
        folderId,
        snap,
        row: row.value,
        scan: scanMap?.[folderId],
        settings,
        models,
        quality,
        filter,
        chosen,
        openMenu,
        toasts,
        media,
        handlers: {
          onBack: () => shell.actions.runAction('back'),
          onOpenPicks: () => shell.actions.runAction('open-picks'),
          onOpenCluster: openCluster,
          onSelectCluster: setChosen,
          onFilter: setFilter,
          onOpenMenu: setOpenMenu,
          onRecluster: () => {
            void reclusterWithToast(store, shell.toasts)
          },
          // The screen never calls it; were it to, the same session-bound toast as Focus's.
          onPick: (fileId, action) => {
            store.pick(fileId, action).then(
              (outcome) => {
                if (outcome) pushPickToast(shell.toasts, actionTitle(action), store)
              },
              (e: unknown) => failed(ACTION_FAILED_TITLE, e),
            )
          },
          onSettings: () => shell.actions.runAction('settings'),
          onHelp: () => shell.actions.runAction('shortcuts'),
        },
      })}
    />
  )
}
