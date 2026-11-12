// The parity build's Settings binding (the generated settings/service.ts's
// functions, same names and types; control.ts says how they answer).
import type { CancellablePromise } from '@wailsio/runtime/cancellable'
import type * as $models from '../../../../bindings/github.com/volodymyrsmirnov/choix/internal/app/settings/models'
import { act, answer } from '../control'

/** The fixture's `settings`. */
export function Get(): CancellablePromise<$models.Body> {
  return answer<$models.Body>('Settings.Get', (f) => f.settings)
}

/** Saved, nothing re-clustered; or as the fixture's `calls` says (a save in flight, or refused). */
export function Save(_p: $models.Patch): CancellablePromise<$models.SaveResult> {
  return act<$models.SaveResult>('Settings.Save', { saved: true, reclustered: false })
}
