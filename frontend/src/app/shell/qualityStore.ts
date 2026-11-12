// The quality worker's snapshot (Models.Quality). Go sends the whole
// snapshot on `quality:status` whenever it changes, so there is nothing to
// poll (v1 polled /api/quality while `active`). Go coalesces a snapshot
// that differs only in received / size / done / total / rated and sends
// any other change at once (session.qualityProgressOnly); the store sorts
// them the same way (store.ts): progress only is transient, anything else
// is a state change (reconciled).
import type { QualityStatus } from '../../design/types'
import type { On } from './events'
import { createStore, type Store } from './store'

/** The snapshot as Go sends it (state as a plain string). */
export interface QualityData {
  enabled: boolean
  state: string
  active: boolean
  received: number
  size: number
  done: number
  total: number
  rated: number
  error?: string
  /** The folder of the oldest rating pass in flight (two folders can be rated at once). */
  folder_id?: string
  /** Every rating pass in flight, oldest first, with its own counts. */
  passes?: { folder_id: string; done: number; total: number }[]
}

export interface QualityStoreDeps {
  quality(): Promise<QualityData>
  on: On
}

const asQuality = (q: QualityData): QualityStatus => q as QualityStatus

/** Go's qualityProgressOnly: nothing but the counters differ. */
function progressOnly(a: QualityStatus | undefined, b: QualityStatus): boolean {
  return a !== undefined && a.enabled === b.enabled && a.state === b.state && a.active === b.active && a.error === b.error
}

export function createQualityStore(deps: QualityStoreDeps): Store<QualityStatus> {
  const store: Store<QualityStatus> = createStore(
    async () => asQuality(await deps.quality()),
    (set, _reload, reconcile) =>
      // Transient for counter progress, state change otherwise (see the header).
      deps.on('quality:status', (ev) => {
        const next = asQuality(ev)
        if (progressOnly(store.get().value, next)) set(next)
        else reconcile(next)
      }),
  )
  return store
}
