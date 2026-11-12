// The parity build's Models binding (the generated models/service.ts's
// functions, same names and types; control.ts says how they answer).
import type { CancellablePromise } from '@wailsio/runtime/cancellable'
import type * as events$0 from '../../../../bindings/github.com/volodymyrsmirnov/choix/internal/app/events/models'
import { act, answer } from '../control'

/** The fixture's `quality`. */
export function Quality(): CancellablePromise<events$0.Quality> {
  return answer<events$0.Quality>('Models.Quality', (f) => f.quality)
}

/** Done at once; or as the fixture's `calls` says (a retry in flight, or refused). */
export function Retry(_kind: string): CancellablePromise<void> {
  return act<void>('Models.Retry', undefined)
}

/** The fixture's `models`. */
export function Status(): CancellablePromise<events$0.ModelStatus[]> {
  return answer<events$0.ModelStatus[]>('Models.Status', (f) => f.models)
}
