// The parity channel: the one way the parity build hands the app a piece of
// UI state that no binding answer, event, route or user input can set.
//
// Parity (frontend/e2e/parity/) drives the real app into each approved
// prototype state with a fixture: data through the mock bindings, events
// through the mock runtime, the route through the location hash, and
// clicks and keys through the case's `interact`. What is left is state the
// prototype passes as a prop and the app keeps to itself with no path to
// it from outside: that state travels in the fixture's `ui` record, and the
// fixture's `toasts` (the prototype seeds a screen's toasts; the app's come
// from timers and actions) go into the shell's toast list.
//
// Contract for the containers (later parity tasks wire their screens):
//   - `uiOverride<T>(key)` is the value of `fixture.ui[key]`, or undefined.
//     Keys are `<screen>.<what>` (for example `settings.reveal`). Read it
//     where the container decides its starting value, and treat undefined
//     as "no override": the container's own behaviour, unchanged.
//   - The fixture is loaded before any binding call answers (the mock
//     bindings hold their answers until then), so a container that renders
//     from binding data reads its override on its first render with data;
//     a route a fixture opens mounts after the fixture is loaded. One
//     fixture per page.
//   - An override read outside a container that re-renders with binding
//     data (the shell's `shell.renderError`) follows fixture loads through
//     `subscribeParityUi`.
//   - `seedParityToasts(toasts)` is wired once, by the shell (App.tsx); each
//     fixture's toasts are pushed into the toast list as they are, without
//     expiry, with Undo where the fixture's toast has it.
//
// Inert in every other build. Only a bundle built in mode `parity` (vite
// --mode parity, `bun run build:parity`) reads the page's
// `__choixParityUi`, which the mock control
// (e2e/parity/mock-bindings/control.ts) installs; in any other build
// import.meta.env.MODE is a different constant, the parity branch below is
// dropped from the bundle, uiOverride answers undefined and
// seedParityToasts does nothing. No styles here.
import type { ToastItem } from '../../design/types'
import type { Toasts } from './toasts'

/** What the parity build's mock control offers the app (window.__choixParityUi). */
export interface ParityUiHost {
  /** The loaded fixture's `ui` record; {} before the fixture is loaded. */
  ui(): Record<string, unknown>
  /** The loaded fixture's toasts; [] before the fixture is loaded. */
  toasts(): ToastItem[]
  /** Calls `listener` after each fixture load; returns unsubscribe. */
  subscribe(listener: () => void): () => void
}

declare global {
  interface Window {
    /** Installed by the parity build's mock control only. */
    __choixParityUi?: ParityUiHost
  }
}

export interface ParityUi {
  uiOverride<T>(key: string): T | undefined
  /** Pushes the loaded fixture's toasts now and after every load; returns stop. */
  seedToasts(toasts: Pick<Toasts, 'push'>): () => void
  /** Calls `listener` after each fixture load (for an override read outside a container); returns stop. */
  subscribe(listener: () => void): () => void
}

/** The longest delay setTimeout takes (about 24.8 days): a seeded toast does not expire during a comparison. */
const KEEP_MS = 2 ** 31 - 1

const INERT: ParityUi = {
  uiOverride: () => undefined,
  seedToasts: () => () => {},
  subscribe: () => () => {},
}

/** The channel for a build `mode`, reading the host `host()` returns (parity mode only). */
export function createParityUi(mode: string | undefined, host: () => ParityUiHost | undefined): ParityUi {
  if (mode !== 'parity') return INERT
  return {
    uiOverride<T>(key: string): T | undefined {
      return host()?.ui()[key] as T | undefined
    },
    seedToasts(toasts) {
      const h = host()
      if (!h) return () => {}
      const seed = () => {
        for (const t of h.toasts()) {
          toasts.push({
            tone: t.tone,
            title: t.title,
            ...(t.body !== undefined ? { body: t.body } : {}),
            duration: KEEP_MS,
            ...(t.undo ? { onUndo: () => {} } : {}),
          })
        }
      }
      // A fixture loaded before the shell started is seeded at once.
      seed()
      return h.subscribe(seed)
    },
    subscribe(listener) {
      return host()?.subscribe(listener) ?? (() => {})
    },
  }
}

// The mode is a constant in a Vite bundle, so outside mode `parity` this
// is INERT and the window lookup is not in the bundle at all.
const channel: ParityUi = import.meta.env.MODE === 'parity' ? createParityUi('parity', () => window.__choixParityUi) : INERT

/** The loaded fixture's `ui[key]` in the parity build; undefined in every other build. */
export function uiOverride<T>(key: string): T | undefined {
  return channel.uiOverride<T>(key)
}

/** Seeds the parity fixture's toasts into the shell's list (the parity build only); returns stop. */
export function seedParityToasts(toasts: Pick<Toasts, 'push'>): () => void {
  return channel.seedToasts(toasts)
}

/** Calls `listener` after each parity fixture load (the parity build only); returns stop. */
export function subscribeParityUi(listener: () => void): () => void {
  return channel.subscribe(listener)
}
