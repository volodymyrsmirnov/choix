// FocusScreen's props from the Focus store's snapshot, the shell's stores and
// the container's handlers (pure, so the rendered tree can be tested without
// a DOM), plus the small decisions the container makes from shell state.
import type { FocusProps } from '../../design/screens/focus/props'
import type { MediaURLs, SettingsBody } from '../../design/types'
import type { FolderNotice } from '../folder/folderStore'
import type { ToastProps } from '../shell/context'
import type { SessionState } from '../shell/folderSession'
import type { FocusSnapshot } from './focusStore'

export type FocusHandlers = Pick<
  FocusProps,
  | 'onBack'
  | 'onCluster'
  | 'onSelect'
  | 'onCompareSelect'
  | 'onCompare'
  | 'onPick'
  | 'onView'
  | 'onInfo'
  | 'onMediaState'
  | 'onRetry'
  | 'onHelp'
  | 'onSettings'
>

export interface FocusInputs {
  snap: FocusSnapshot
  folderName: string
  qualityOn: boolean
  media: MediaURLs
  toasts: ToastProps
  handlers: FocusHandlers
}

export function focusScreenProps(i: FocusInputs): FocusProps {
  const { snap } = i
  return {
    folder: i.folderName,
    data: snap.data,
    // The screen shows the error only in place of a cluster.
    loadError: snap.data ? null : snap.loadError,
    activeIndex: snap.activeIndex,
    zoom: snap.view.zoom,
    pan: snap.view.pan,
    showInfo: snap.showInfo,
    comparing: snap.comparing,
    compareIdx: snap.compareIdx,
    busyFileIds: snap.busyFileIds,
    mediaState: snap.mediaState,
    ratings: snap.ratings,
    qualityOn: i.qualityOn,
    media: i.media,
    toasts: i.toasts.toasts,
    onUndoToast: i.toasts.onUndoToast,
    onDismissToast: i.toasts.onDismissToast,
    ...i.handlers,
  }
}

/** Settings › Quality rating; on until the settings have loaded (the config default). */
export const qualityOnOf = (settings: SettingsBody | undefined): boolean => settings?.quality_rating ?? true

/** The folder's name for the Back button: the grid's, else its Library row's, else the view Open returned. */
export function folderNameOf(
  folderId: string,
  gridName: string | undefined,
  rows: readonly { id: string; name: string }[] | undefined,
  sessionView: { folder: string } | null,
): string {
  return gridName ?? rows?.find((r) => r.id === folderId)?.name ?? sessionView?.folder ?? ''
}

/**
 * Why Focus cannot stay: the folder is offline (Focus does not open while
 * it is; the Folder page shows the offline notice), or it could not be
 * opened (the Folder page reports that and leaves for the Library). Null
 * while Focus can stay.
 */
export function leaveReason(
  folderId: string,
  session: SessionState,
  notice: FolderNotice,
  scanState: string | undefined,
): 'offline' | 'error' | null {
  const mine = session.folderId === folderId
  if (scanState === 'offline' || notice === 'offline' || (mine && session.status === 'offline')) return 'offline'
  if (mine && session.status === 'error') return 'error'
  return null
}
