// The parity build's Catalog binding (the generated catalog/service.ts's
// functions, same names and types; control.ts says how they answer).
import type { CancellablePromise } from '@wailsio/runtime/cancellable'
import type * as $models from '../../../../bindings/github.com/volodymyrsmirnov/choix/internal/app/catalog/models'
import type * as events$0 from '../../../../bindings/github.com/volodymyrsmirnov/choix/internal/app/events/models'
import { act, answer, done, fail } from '../control'

export function Close(_folderID: string): CancellablePromise<void> {
  return done()
}

/** The fixture's `focus`, for any cluster (or as its `calls` says). */
export function Cluster(_folderID: string, _clusterID: number): CancellablePromise<$models.FocusResponse> {
  return answer<$models.FocusResponse>('Catalog.Cluster', (f) => f.focus)
}

/** The fixture's `view`. */
export function Clusters(_folderID: string): CancellablePromise<$models.FolderView> {
  return answer<$models.FolderView>('Catalog.Clusters', (f) => f.view)
}

/** The fixture's `view` (opening the folder answers its grid). */
export function Open(_folderID: string): CancellablePromise<$models.FolderView> {
  return answer<$models.FolderView>('Catalog.Open', (f) => f.view)
}

/** The fixture's `quality`, as Models.Quality answers it. */
export function QualityStatus(): CancellablePromise<events$0.Quality> {
  return answer<events$0.Quality>('Catalog.QualityStatus', (f) => f.quality)
}

/** The fixture's `ratings`. */
export function Ratings(_folderID: string): CancellablePromise<{ [_ in string]?: number }> {
  return answer<{ [_ in string]?: number }>('Catalog.Ratings', (f) => f.ratings)
}

/** Answers at once, unless the fixture's `calls` says otherwise (control.ts act). */
export function Recluster(_folderID: string): CancellablePromise<void> {
  return act<void>('Catalog.Recluster', undefined)
}

/** Go's states: picked, rejected, else unmarked. */
const STATE: Record<string, string> = { pick: 'picked', reject: 'rejected', unpick: 'unmarked', unreject: 'unmarked' }

/** Go's answer at once, unless the fixture's `calls` says otherwise (a pick in flight: 'pending'). */
export function SetPick(_folderID: string, fileID: number, action: string): CancellablePromise<$models.PickResult> {
  const state = STATE[action]
  if (state === undefined) return fail({ code: 'invalid', field: 'action', reason: 'unknown', message: `unknown action ${action}` })
  return act<$models.PickResult>('Catalog.SetPick', { file_id: fileID, state })
}

/** Nothing to undo (Go's not_found). */
export function Undo(_folderID: string): CancellablePromise<$models.PickResult> {
  return fail({ code: 'not_found', message: 'nothing to undo' })
}
