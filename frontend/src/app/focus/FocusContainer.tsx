// Focus and its compare mode (#/folder/<id>/focus/<clusterId>): the design's
// FocusScreen, fed by the Focus store (focusStore.ts) inside the open
// folder's session. It renders the screen and nothing else, so the screen's
// title bar is the window's drag region with nothing over it.
//
// - The first photo is the thumbnail the Folder page opened Focus at
//   (takeFocusEntry); back from Settings, the photo Focus showed when it
//   left; else the first.
// - Picks, rejects and undo go through the open folder's store, so the grid
//   is right on the way back and ⌘Z there undoes what was done here. A pick
//   puts up v1's toast with Undo, bound to this folder session
//   (shell/toasts.ts pushPickToast): its Undo works from Folder, Focus or
//   Settings while the session is open, and the toast leaves when it closes.
// - Keys and the native menu: every Focus and Compare shortcut is an action
//   registered here with the shell (actions.register), so the webview's
//   single keys and the menu's accelerators run the same handler. Esc and
//   ⌘[ un-zoom, then leave compare, then go back; the screen's Back buttons
//   go back at once.
// - A rebuild that renumbers the clusters: the store follows the photo on
//   screen to its new cluster and the route is set to it (moved); a photo
//   in no cluster any more leaves for the Folder page (lost).
// - The menu's screen is `compare` while comparing, `focus` otherwise.
// - While the folder is offline Focus does not show (design ruling): the
//   Folder page and its offline notice do. A folder that could not be opened
//   goes there too, and that page reports it.
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { actionTitle } from '../../design/screens/focus/logic'
import type { PickAction } from '../../design/screens/focus/props'
import { FocusScreen } from '../../design/screens/focus/FocusScreen'
import type { FolderStore } from '../folder/folderStore'
import { useFolderStore } from '../folder/useFolderStore'
import { Catalog } from '../shell/api'
import { useShell, useToastProps } from '../shell/context'
import { appError, errorText, isQuitting } from '../shell/errors'
import { on } from '../shell/events'
import { mediaFor } from '../shell/media'
import { useStore } from '../shell/store'
import { ACTION_FAILED_TITLE, pushPickToast, undoWithToast } from '../shell/toasts'
import { focusActionHandlers } from './focusActions'
import { focusScreenProps, folderNameOf, leaveReason, qualityOnOf } from './focusProps'
import { createFocusStore } from './focusStore'
import { showInfoPrefs } from './prefs'

/** The photo Focus showed when it left for Settings, so the return shows it again; dropped on any other exit. */
const lastPhoto = new WeakMap<FolderStore, { clusterId: number; fileId: number }>()

/** The clicked thumbnail, else the photo last shown in this cluster. */
function startFileOf(folder: FolderStore, clusterId: number): number | undefined {
  const clicked = folder.takeFocusEntry(clusterId)
  if (clicked !== undefined) return clicked
  const last = lastPhoto.get(folder)
  return last?.clusterId === clusterId ? last.fileId : undefined
}

export function FocusContainer({ folderId, clusterId }: { folderId: string; clusterId: number }) {
  const shell = useShell()
  const folder = useFolderStore(folderId)
  const [store] = useState(() => {
    const startFileId = startFileOf(folder, clusterId)
    /** Only while the route still shows Focus on this folder (the store may answer after the user left). */
    const here = () => {
      const r = shell.router.current()
      return r.name === 'focus' && r.folderId === folderId
    }
    return createFocusStore({
      folderId,
      clusterId,
      ...(startFileId !== undefined ? { startFileId } : {}),
      session: shell.session,
      cluster: (id, cluster) => Catalog.Cluster(id, cluster),
      folder,
      on,
      advanceOnAction: () => shell.settings.get().value?.advance_on_action ?? false,
      prefs: showInfoPrefs(() => window.localStorage),
      // A rebuild renumbered the clusters: the route names the photo's cluster
      // again (Back still goes to the folder), or Focus ends when the photo is gone.
      moved: (id) => {
        if (here()) shell.router.navigate({ name: 'focus', folderId, clusterId: id })
      },
      lost: () => {
        if (here()) shell.router.navigate({ name: 'folder', folderId })
      },
    })
  })
  // The folder's store too: it carries the picks, the ratings and the busy files.
  useEffect(() => folder.start(), [folder])
  useEffect(() => store.start(), [store])

  // Another cluster (↩, ⇧↩, the done pill): the route's parameter changed.
  // After the store's own move (moved) it already shows that cluster: open does nothing.
  const shownCluster = useRef(clusterId)
  useEffect(() => {
    if (shownCluster.current === clusterId) return
    shownCluster.current = clusterId
    store.open(clusterId, startFileOf(folder, clusterId))
  }, [store, folder, clusterId])
  // Back on the Folder page, its cursor is on the cluster seen last.
  useEffect(() => {
    folder.memory.selectedClusterId = clusterId
  }, [folder, clusterId])

  const snap = useSyncExternalStore(store.subscribe, store.get)
  const folderSnap = useSyncExternalStore(folder.subscribe, folder.get)
  const session = useSyncExternalStore(shell.session.subscribe, shell.session.get)
  const scanState = useStore(shell.scan).value?.[folderId]?.state
  const rows = useStore(shell.library).value
  const settings = useStore(shell.settings).value
  const toasts = useToastProps()
  const media = useMemo(() => mediaFor(folderId), [folderId])

  const active = snap.data?.cluster.members[snap.activeIndex]
  useEffect(() => {
    if (snap.data && active) lastPhoto.set(folder, { clusterId: snap.data.cluster.id, fileId: active.file_id })
  }, [folder, snap.data, active])
  // On unmount the router already shows the next route: only Settings comes back here.
  useEffect(
    () => () => {
      if (shell.router.current().name !== 'settings') lastPhoto.delete(folder)
    },
    [shell, folder],
  )

  const leave = leaveReason(folderId, session, folderSnap.notice, scanState)
  useEffect(() => {
    if (leave !== null) shell.router.navigate({ name: 'folder', folderId })
  }, [leave, shell, folderId])

  useEffect(() => {
    shell.actions.setContext({ screen: snap.comparing ? 'compare' : 'focus' })
  }, [shell, snap.comparing])

  // Actions: stable for the page's lifetime (they read the stores, not the render).
  const [act] = useState(() => {
    const failed = (title: string, e: unknown) => {
      const err = appError(e)
      if (isQuitting(err)) return
      shell.toasts.push({ tone: 'reject', title, body: errorText(err) })
    }
    // The folder store is this session's UndoTarget.
    const undo = (): Promise<void> => undoWithToast(shell.toasts, folder)
    const done = (action: PickAction) => {
      pushPickToast(shell.toasts, actionTitle(action), folder)
    }
    return {
      undo,
      pick: (fileId: number, action: PickAction): Promise<void> =>
        store.act(fileId, action).then(
          (outcome) => {
            if (outcome) done(action)
          },
          (e: unknown) => failed(ACTION_FAILED_TITLE, e),
        ),
      mark: (k: 'picked' | 'rejected'): Promise<void> =>
        store.mark(k).then(
          (r) => {
            if (r) done(r.action)
          },
          (e: unknown) => failed(ACTION_FAILED_TITLE, e),
        ),
      toCluster: (id: number | null) => {
        if (id !== null) shell.router.navigate({ name: 'focus', folderId, clusterId: id })
      },
      toFolder: () => {
        shell.router.navigate({ name: 'folder', folderId })
      },
    }
  })

  useEffect(() => {
    const handlers = focusActionHandlers(store, act)
    const offs = Object.entries(handlers).map(([id, handler]) => shell.actions.register(id, handler))
    return () => {
      for (const off of offs) off()
    }
  }, [shell, store, act])

  return (
    <FocusScreen
      {...focusScreenProps({
        snap,
        folderName: folderNameOf(folderId, folderSnap.value?.name, rows, session.folderId === folderId ? session.view : null),
        qualityOn: qualityOnOf(settings),
        media,
        toasts,
        handlers: {
          onBack: act.toFolder,
          onCluster: act.toCluster,
          onSelect: store.select,
          onCompareSelect: store.compareSelect,
          onCompare: store.compare,
          onPick: (fileId, action) => {
            void act.pick(fileId, action)
          },
          onView: store.setView,
          onInfo: store.setInfo,
          onMediaState: store.setMediaState,
          onRetry: store.retry,
          onHelp: () => shell.actions.runAction('shortcuts'),
          onSettings: () => shell.actions.runAction('settings'),
        },
      })}
    />
  )
}
