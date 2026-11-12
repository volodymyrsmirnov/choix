// Re-cluster from the Folder page (its Re-cluster button) and the toast that
// reports it, with v1's titles (Library.tsx handleRecluster): "Re-clustered"
// once it went through, "Re-cluster failed" with the reason when it did not.
// Nothing when one was already running (the store refuses a second), nor
// when the app is quitting.
import { appError, errorText, isQuitting } from '../shell/errors'
import type { Toasts } from '../shell/toasts'
import type { FolderStore } from './folderStore'

export const RECLUSTERED = 'Re-clustered'
export const RECLUSTER_FAILED = 'Re-cluster failed'

/** Runs the store's re-cluster, then puts up its toast; never rejects. */
export function reclusterWithToast(store: Pick<FolderStore, 'recluster'>, toasts: Pick<Toasts, 'push'>): Promise<void> {
  return store.recluster().then(
    (ran) => {
      if (ran) toasts.push({ tone: 'pick', title: RECLUSTERED })
    },
    (e: unknown) => {
      const err = appError(e)
      if (isQuitting(err)) return
      toasts.push({ tone: 'reject', title: RECLUSTER_FAILED, body: errorText(err) })
    },
  )
}
