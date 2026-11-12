// The parity build's Library binding (the generated library/service.ts's
// functions, same names and types; control.ts says how they answer).
import type { CancellablePromise } from '@wailsio/runtime/cancellable'
import type * as $models from '../../../../bindings/github.com/volodymyrsmirnov/choix/internal/app/library/models'
import { answer, done, type Answer } from '../control'

/** The native picker, cancelled: nothing added. */
export function AddFolders(): CancellablePromise<$models.AddResult[]> {
  return done<$models.AddResult[]>([])
}

export function CheckNow(): CancellablePromise<void> {
  return done()
}

/**
 * The fixture's `folders`, as they are. Go sends `added_at` on every folder;
 * the design's type keeps it optional only for literals, and every
 * prototype folder carries it (prototype/scenarios/library.test.ts).
 */
export function List(): CancellablePromise<$models.FolderSummary[]> {
  return answer<$models.FolderSummary[]>('Library.List', (f) => f.folders as Answer<$models.FolderSummary[]> | undefined)
}

/** The native alert, declined: nothing removed. */
export function Remove(_id: string): CancellablePromise<boolean> {
  return done(false)
}

export function Rescan(_id: string): CancellablePromise<void> {
  return done()
}
