// What a route module (frontend/src/app/<route>/index.tsx) and the
// containers under it use from the shell, without importing App.tsx (no
// import cycle): the Shell's type, useShell, useRoute, and the toast props.
//
// Route-module contract (Tasks 54, 56, 58, 60):
//   - `library/index.tsx` exports `LibraryRoute`, `folder/index.tsx`
//     `FolderRoute`, `focus/index.tsx` `FocusRoute`, `settings/index.tsx`
//     `SettingsRoute`: a component without props. App renders the one for
//     the current route inside AppRoot, and nothing else there except the
//     shortcut sheet.
//   - It reads its parameters with `useRoute('<name>')` (folder id, cluster
//     id), typed for that route. It stays mounted while the route NAME
//     stays the same: going to another cluster in Focus changes the
//     parameters, not the component.
//   - It reaches everything else through `useShell()`: router, session
//     (the open folder: read it, never open or close), actions (register a
//     screen's handlers), toasts, the shared stores. It imports bindings
//     only through `../shell/api` and events through `../shell/events`.
//   - Its design screen draws the toasts: pass `useToastProps()` as the
//     screen's `toasts` / `onUndoToast` / `onDismissToast`. A route module
//     that renders no screen renders <FallbackToaster /> instead.
import { createContext, useContext, useSyncExternalStore } from 'react'
import { Toaster } from '../../design/components'
import type { ModelStatus, QualityStatus, SettingsBody, ToastItem } from '../../design/types'
import type { Actions } from './actions'
import type { FolderSummaryModel } from './api'
import type { FolderSession } from './folderSession'
import type { Route, Router } from './router'
import type { ScanMap } from './scanStore'
import type { Store } from './store'
import type { Toasts } from './toasts'

/** A boolean the UI follows (the shortcut sheet's open state). */
export interface Flag {
  get(): boolean
  set(v: boolean): void
  subscribe(listener: () => void): () => void
}

export interface Shell {
  router: Router
  /** The open folder (folderSession.ts). */
  session: FolderSession
  actions: Actions
  toasts: Toasts
  scan: Store<ScanMap>
  models: Store<ModelStatus[]>
  quality: Store<QualityStatus>
  settings: Store<SettingsBody>
  /**
   * The Library's folder list (library/libraryStore.ts). The shell's, not the
   * library route's: it runs on every route, so a folder deleted in Finder
   * while another screen shows is still toasted, once.
   */
  library: Store<FolderSummaryModel[]>
  /** The shortcut sheet is open. */
  sheet: Flag
  /** Starts every subscription and the first loads; returns stop. */
  start(): () => void
}

export const ShellContext = createContext<Shell | null>(null)

/** The shell, inside the App. */
export function useShell(): Shell {
  const shell = useContext(ShellContext)
  if (!shell) throw new Error('useShell outside the App')
  return shell
}

/** The current route. */
export function useRoute(): Route
/** The current route, which must be `name` (a route module asks for its own). */
export function useRoute<N extends Route['name']>(name: N): Extract<Route, { name: N }>
export function useRoute(name?: Route['name']): Route {
  const { router } = useShell()
  const route = useSyncExternalStore(router.subscribe, router.current)
  if (name !== undefined && route.name !== name) {
    throw new Error(`useRoute('${name}') on the ${route.name} route`)
  }
  return route
}

/** The props a design screen takes for its Toaster. */
export interface ToastProps {
  toasts: ToastItem[]
  onUndoToast(id: string): void
  onDismissToast(id: string): void
}

export function useToastProps(): ToastProps {
  const { toasts } = useShell()
  const items = useSyncExternalStore(toasts.subscribe, toasts.items)
  return { toasts: items, onUndoToast: toasts.undo, onDismissToast: toasts.dismiss }
}

/** The Toaster for a route module that renders no design screen (which would draw it). */
export function FallbackToaster() {
  const t = useToastProps()
  return <Toaster items={t.toasts} onUndo={t.onUndoToast} onDismiss={t.onDismissToast} />
}
