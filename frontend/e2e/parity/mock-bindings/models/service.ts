// The parity build's Models binding (the generated models/service.ts's
// functions, same names and types; control.ts says how they answer).
//
// Until phase B: Go's Models binding is a compatibility shim
// (internal/app/models) that the unchanged app still calls. Nothing is
// downloaded, so Status answers both kinds `ready`, whatever the fixture,
// as the shim does; the app's Library waits for that answer before it
// leaves its loading state. Phase B deletes this mock with the binding.
import type { CancellablePromise } from '@wailsio/runtime/cancellable'
import type * as events$0 from '../../../../bindings/github.com/volodymyrsmirnov/choix/internal/app/events/models'
import { act, answer, wireQuality } from '../control'
import { shimStatus } from './shim'

/** The fixture's `quality` (wireQuality). */
export function Quality(): CancellablePromise<events$0.Quality> {
  return answer<events$0.Quality>('Models.Quality', (f) => wireQuality(f.quality))
}

/** Done at once; or as the fixture's `calls` says. */
export function Retry(_kind: string): CancellablePromise<void> {
  return act<void>('Models.Retry', undefined)
}

/** Both kinds ready (shimStatus), once a fixture is loaded; no fixture field. */
export function Status(): CancellablePromise<events$0.ModelStatus[]> {
  return answer<events$0.ModelStatus[]>('Models.Status', () => shimStatus())
}
