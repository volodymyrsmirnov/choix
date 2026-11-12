// The parity build's mock control: the fixture every mock binding answers
// from, the event bus a case emits on, and the `ui` / `toasts` host the app's
// parity channel reads (src/app/shell/parityUi.ts).
//
// In the page it installs
//   window.__choixParity   = { load(fixture), emit(name, data) }   for the cases
//   window.__choixParityUi = { ui(), toasts(), subscribe(listener) } for the app
// when the bundle evaluates (the mock services import this module), so both
// exist before the app first renders.
//
// Answers (answer()):
//   - A binding that reads data (Library.List, Settings.Get,
//     Catalog.Open/Clusters/Cluster/Ratings/QualityStatus, …) waits until a fixture is
//     loaded, then answers from it: the value (a fresh copy per call);
//     'pending': never answers (a loading state); 'error': rejects as the
//     Wails runtime rejects a Go error, an Error whose `cause` is the app
//     error JSON `{code: 'internal', message}`; a field the fixture leaves
//     out: never answers either, so a screen that reads data its fixture
//     forgot shows its loading state and parity reports the difference.
//     Where the fixture's `calls` names the binding, `calls` decides instead
//     of the field (as for an acting binding, below): a load that fails with
//     a given app error (Catalog.Cluster's in state 316).
//   - A binding that only acts (SetMenuContext, CheckNow, Rescan, …)
//     answers at once, fixture or not, as Go answers when nothing goes wrong.
//     Those wired through act() (Catalog.Recluster, Catalog.SetPick,
//     Settings.Save) answer as the loaded
//     fixture's `calls` says instead, where it names them: 'pending' never
//     answers (the call stays in flight), an app error rejects with it.
//
// load(fixture): stores the fixture, releases every waiting call, seeds the
// app's toasts (through the ui host's listeners), then routes: the location
// hash becomes `fixture.route` and load resolves after the router has heard
// it. One fixture per page (parity opens a fresh page for every state and
// size); a later load replaces the fixture for the calls that follow.
import { CancellablePromise } from '@wailsio/runtime/cancellable'
import type { FolderView } from '../../../bindings/github.com/volodymyrsmirnov/choix/internal/app/catalog/models'
import type { ParityUiHost } from '../../../src/app/shell/parityUi'
import type { FocusResponse, FolderSummary, QualityStatus, SettingsBody, ToastItem } from '../../../src/design/types'
import { dispatch } from './runtime'

/** A data field's value: the answer, or how the binding misbehaves. */
export type Answer<T> = T | 'pending' | 'error'

export interface ParityFixture {
  /** The location hash to show, e.g. `#/`, `#/folder/dolomites`, `#/settings`. */
  route: string
  /** Library.List (and so the scan store). */
  folders?: FolderSummary[] | 'pending'
  /** Catalog.QualityStatus. */
  quality?: QualityStatus
  /** Settings.Get. */
  settings?: SettingsBody | 'pending' | 'error'
  /** Catalog.Open and Catalog.Clusters, for any folder id. */
  view?: FolderView | 'pending' | 'error'
  /** Catalog.Cluster, for any folder and cluster id. */
  focus?: FocusResponse | 'pending' | 'error'
  /** Catalog.Ratings: file id (decimal) → raw rating. */
  ratings?: Record<string, number>
  /** Toasts on screen, seeded into the shell's toast list as they are (no expiry). */
  toasts?: ToastItem[]
  /** What is a prop in the prototype and local state in the app; read through uiOverride (src/app/shell/parityUi.ts). */
  ui?: Record<string, unknown>
  /** How a binding answers instead of at once (one wired through act()) or from its field (a data binding), by `<Service>.<Method>`: never ('pending'), or rejecting with this app error. */
  calls?: Record<string, 'pending' | AppErrorCause>
  /** Go's events the state's case emits once the page shows the bindings' answers (cases.ts); nothing here sends them. */
  events?: ParityEvent[]
}

/** One event as Go sends it (the runtime's name and payload). */
export interface ParityEvent {
  name: string
  data: unknown
}

/** What a parity case calls in the page. */
export interface ParityControl {
  load(f: ParityFixture): Promise<void>
  emit(name: string, data: unknown): void
}

declare global {
  interface Window {
    __choixParity: ParityControl
  }
}

/** The app error a mock rejects with (Go's apperr JSON; src/app/shell/errors.ts). */
export interface AppErrorCause {
  code: string
  message: string
  field?: string
  reason?: string
}

/** The rejection of a failed binding call, shaped as the Wails runtime's RuntimeError. */
export function bindingError(cause: AppErrorCause): Error {
  const err = new Error(cause.message, { cause })
  err.name = 'RuntimeError'
  return err
}

export interface RouteHost {
  /** Shows `hash`; resolves once the page has applied it. */
  go(hash: string): Promise<void>
}

/** The page's location hash: set it, then wait for its hashchange and one more task. */
function windowRoute(): RouteHost {
  return {
    async go(hash) {
      const want = hash.startsWith('#') ? hash : `#${hash}`
      if (window.location.hash !== want) {
        const changed = new Promise<void>((resolve) => window.addEventListener('hashchange', () => resolve(), { once: true }))
        window.location.hash = want
        await changed
      }
      await new Promise((resolve) => setTimeout(resolve, 0))
    },
  }
}

export interface Control extends ParityControl {
  /** A data binding's answer from the fixture (see the header). */
  answer<T>(binding: string, pick: (f: ParityFixture) => Answer<T> | undefined): CancellablePromise<T>
  /** An acting binding's answer: `value` at once, unless the loaded fixture's `calls` names `binding`. */
  act<T>(binding: string, value: T): CancellablePromise<T>
  /** The app's view of the fixture (window.__choixParityUi). */
  uiHost: ParityUiHost
}

/** A control on its own (tests); the page uses the one this module installs. */
export function createControl(route?: RouteHost): Control {
  let fixture: ParityFixture | null = null
  let release: (f: ParityFixture) => void = () => {}
  const loaded = new Promise<ParityFixture>((resolve) => {
    release = resolve
  })
  const listeners = new Set<() => void>()

  return {
    async load(f) {
      fixture = f
      release(f)
      for (const l of [...listeners]) l()
      if (route) await route.go(f.route)
    },
    emit: dispatch,
    answer<T>(binding: string, pick: (f: ParityFixture) => Answer<T> | undefined) {
      return new CancellablePromise<T>((resolve, reject) => {
        void loaded.then(() => {
          const how = fixture!.calls?.[binding]
          if (how === 'pending') return
          if (how !== undefined) {
            reject(bindingError(how))
            return
          }
          const v = pick(fixture!)
          if (v === undefined || v === 'pending') return
          if (v === 'error') {
            reject(bindingError({ code: 'internal', message: `parity fixture: ${binding} failed` }))
            return
          }
          resolve(structuredClone(v))
        })
      })
    },
    act<T>(binding: string, value: T) {
      const how = fixture?.calls?.[binding]
      if (how === undefined) return CancellablePromise.resolve(value)
      if (how === 'pending') return new CancellablePromise<T>(() => {})
      return CancellablePromise.reject(bindingError(how)) as CancellablePromise<T>
    },
    uiHost: {
      ui: () => fixture?.ui ?? {},
      toasts: () => fixture?.toasts ?? [],
      subscribe(listener) {
        listeners.add(listener)
        return () => {
          listeners.delete(listener)
        }
      },
    },
  }
}

const inPage = typeof window !== 'undefined'

/** The control the mock services answer from. */
export const control = createControl(inPage ? windowRoute() : undefined)
export const load = control.load
export const emit = control.emit
export const answer = control.answer
export const act = control.act

/** A binding that only acts: it answers at once. */
export function done(): CancellablePromise<void>
export function done<T>(value: T): CancellablePromise<T>
export function done<T>(value?: T): CancellablePromise<T | undefined> {
  return CancellablePromise.resolve(value)
}

/** A binding that fails with `cause`, at once. */
export function fail<T>(cause: AppErrorCause): CancellablePromise<T> {
  return CancellablePromise.reject(bindingError(cause)) as CancellablePromise<T>
}

if (inPage) {
  window.__choixParity = { load, emit }
  window.__choixParityUi = control.uiHost
}
