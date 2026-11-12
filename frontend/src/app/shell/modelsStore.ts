// Both models' download state, always [clip, quality] (Models.Status).
// `models:progress` carries one model's whole status and replaces the entry
// of its kind. Go sends a download's byte ticks coalesced and every state
// change (queued → downloading, ready, failed, disabled) at once on this
// same event (internal/app/models emitLocked), so the payload decides the
// kind (store.ts): the same state and error as the stored entry is byte
// progress (transient); anything else is a state change (reconciled).
import type { ModelStatus } from '../../design/types'
import type { On } from './events'
import { createStore, type Store } from './store'

/** A model status as Go sends it (kind and state as plain strings). */
export interface ModelStatusData {
  kind: string
  state: string
  received: number
  size: number
  error?: string
}

export interface ModelsStoreDeps {
  status(): Promise<ModelStatusData[]>
  on: On
}

const asModel = (m: ModelStatusData): ModelStatus => m as ModelStatus

/** Whether `next` only moves the bytes of the stored entry of its kind. */
function progressOnly(prev: ModelStatus[] | undefined, next: ModelStatus): boolean {
  const cur = prev?.find((m) => m.kind === next.kind)
  return cur !== undefined && cur.state === next.state && cur.error === next.error
}

export function createModelsStore(deps: ModelsStoreDeps): Store<ModelStatus[]> {
  const store: Store<ModelStatus[]> = createStore(
    async () => (await deps.status()).map(asModel),
    (set, _reload, reconcile) =>
      // Transient for byte progress, a state change when the state moved (see the header).
      deps.on('models:progress', (ev) => {
        const next = asModel(ev)
        const patch = (prev: ModelStatus[] = []) =>
          prev.some((m) => m.kind === next.kind) ? prev.map((m) => (m.kind === next.kind ? next : m)) : [...prev, next]
        if (progressOnly(store.get().value, next)) set(patch)
        else reconcile(patch)
      }),
  )
  return store
}
