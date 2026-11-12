// The quality worker's snapshot: loaded with Catalog.QualityStatus, then
// kept current by `quality:status`. Go sends the whole snapshot on that
// event whenever it changes, so there is nothing to poll (v1 polled
// /api/quality while `active`). Go coalesces a snapshot that differs only
// in its counters (done / total / rated) and sends any other change at once
// (session.qualityProgressOnly); the store sorts them the same way
// (store.ts): counters only (the same state and the same passes, in the
// same order) is transient, anything else is a state change (reconciled).
// When the window gains focus the store loads again, so an event that was
// missed anyway (a pass's final one) is repaired the next time the user
// looks at the app.
import type { QualityStatus } from '../../design/types'
import type { On } from './events'
import { createStore, type Store } from './store'

/** The snapshot as Go sends it (state as a plain string). */
export interface QualityData {
  enabled: boolean
  state: string
  active: boolean
  done: number
  total: number
  rated: number
  /** The folder of the latest rating pass begun (two folders can be rated at once). */
  folder_id?: string
  /** Every rating pass in flight, oldest first, with its own counts. */
  passes?: { folder_id: string; done: number; total: number }[]
}

export interface QualityStoreDeps {
  /** Catalog.QualityStatus. */
  qualityStatus(): Promise<QualityData>
  on: On
}

const asQuality = (q: QualityData): QualityStatus => q as QualityStatus

/**
 * Go's qualityProgressOnly: nothing but the counters differ. The same
 * enabled, state, active and folder_id, and the same passes (their folders,
 * in the same order); done, total, rated and each pass's counts may differ.
 */
function progressOnly(a: QualityStatus | undefined, b: QualityStatus): boolean {
  if (a === undefined) return false
  if (a.enabled !== b.enabled || a.state !== b.state || a.active !== b.active || a.folder_id !== b.folder_id) {
    return false
  }
  const ap = a.passes ?? []
  const bp = b.passes ?? []
  return ap.length === bp.length && ap.every((p, i) => p.folder_id === bp[i].folder_id)
}

export function createQualityStore(deps: QualityStoreDeps): Store<QualityStatus> {
  const store: Store<QualityStatus> = createStore(
    async () => asQuality(await deps.qualityStatus()),
    (set, reload, reconcile) => {
      // Transient for counter progress, state change otherwise (see the header).
      const offStatus = deps.on('quality:status', (ev) => {
        const next = asQuality(ev)
        if (progressOnly(store.get().value, next)) set(next)
        else reconcile(next)
      })
      // Back to the app: load again (or once after a load in flight).
      const offFocus = deps.on('common:WindowFocus', () => reload())
      return () => {
        offStatus()
        offFocus()
      }
    },
  )
  return store
}
