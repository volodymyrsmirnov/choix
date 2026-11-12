// The app: the design's AppRoot with the current route's module in it, plus
// the shortcut sheet. The shell objects — router, folder session, actions,
// toasts and the shared stores — are made once here (createShell) from the
// real bindings and event bus, and reach the route modules through
// useShell() (context.tsx, which also states the route-module contract).
//
// Toasts: every design screen draws the Toaster itself (its `toasts` prop;
// Settings puts it over its form pane), so a route module passes
// useToastProps() to its screen; a route module without a screen renders
// FallbackToaster.
//
// Folder drops: the window is the drop target (index.html marks #root
// with data-file-drop-target, the attribute Wails' runtime looks for); Go
// adds the dropped folders (window drop → Library.AddFolders), so nothing
// here handles the drop.
//
// A screen that throws while rendering (RouteBoundary.tsx) is logged, left
// for the route above it, and reported with the error toast; when the
// Library itself fails, the design's error screen fills the window
// (renderError.tsx). It never leaves the window empty. That error, and any uncaught error or unhandled
// rejection on the window, is also written to Go's log (clientErrors.ts,
// System.ReportClientError).
//
// The title bar (titleBar.ts): its traffic-light inset follows the window's
// real buttons and full screen (a property on #root), and a double-click on
// its empty area acts like a native title bar's.
import { useEffect, useState, useSyncExternalStore } from 'react'
import { AppRoot } from '../../design/components'
import { ShortcutsSheet } from '../../design/screens/overlays/ShortcutsSheet'
import { FocusRoute } from '../focus'
import { FolderRoute } from '../folder'
import { LibraryRoute } from '../library'
import { createLibraryStore } from '../library/libraryStore'
import { SettingsRoute } from '../settings'
import { bindMenuContext, createActions } from './actions'
import { Catalog, Library, Settings, System } from './api'
import { FallbackToaster, ShellContext, type Flag, type Shell } from './context'
import { installErrorReports, reportRenderError } from './clientErrors'
import { appError, errorText } from './errors'
import { on } from './events'
import { createFolderSession } from './folderSession'
import { installKeys } from './keys'
import { seedParityToasts, subscribeParityUi, uiOverride } from './parityUi'
import { createQualityStore } from './qualityStore'
import { AppErrorScreen, copyText, reloadToLibrary, renderErrorDetails } from './renderError'
import { RouteBoundary, recoverFromRenderError, renderErrorText } from './RouteBoundary'
import { createRouter, windowHashHost, type Route } from './router'
import { createScanStore } from './scanStore'
import { createSettingsStore } from './settingsStore'
import { computesDrag, createTitleBar } from './titleBar'
import { createToasts, PICK_TOAST_KEY, UNDONE_MS } from './toasts'

function createFlag(): Flag {
  let value = false
  const listeners = new Set<() => void>()
  return {
    get: () => value,
    set(v) {
      if (v === value) return
      value = v
      for (const l of [...listeners]) l()
    },
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

/** The shell on the real bindings, event bus, window and document. */
export function createShell(): Shell {
  const router = createRouter(windowHashHost())
  const toasts = createToasts()
  const sheet = createFlag()
  let singleKeysNative = false

  const session = createFolderSession({
    open: (folderId) => Catalog.Open(folderId),
    close: (folderId) => Catalog.Close(folderId),
    on,
    toLibrary: () => router.navigate({ name: 'library' }, { force: true }),
    settingsToLibrary: () => router.retargetSettings({ name: 'library' }),
  })

  const actions = createActions({
    catalog: Catalog,
    library: Library,
    system: System,
    router,
    toggleShortcuts: () => sheet.set(!sheet.get()),
    focusSearch: () => {
      const field = document.querySelector<HTMLInputElement>('[data-testid="filter-input"]')
      field?.focus()
      field?.select()
    },
    document,
    onError: (e, id) => {
      const err = appError(e)
      if (err.code === 'unavailable') return // quitting
      // v1's titles ("Undo failed", "Action failed"); the body is the design's copy or Go's message.
      toasts.push({ tone: 'reject', title: id === 'undo' ? 'Undo failed' : 'Action failed', body: errorText(err) })
    },
    onUndone: () => toasts.push({ tone: 'pick', title: 'Undone', key: PICK_TOAST_KEY, duration: UNDONE_MS }),
  })

  const scan = createScanStore({ list: () => Library.List(), on })
  const quality = createQualityStore({ qualityStatus: () => Catalog.QualityStatus(), on })
  const settings = createSettingsStore({ get: () => Settings.Get() })
  // The Library's list lives as long as the shell (its `gone` toast shows on any route).
  const library = createLibraryStore({
    list: () => Library.List(),
    on,
    toasts,
    onLeft: (folderId) => {
      if (actions.state().selected === folderId) actions.select(null)
    },
  })

  return {
    router,
    session,
    actions,
    toasts,
    scan,
    quality,
    settings,
    library,
    sheet,
    start() {
      // #root: outside the tree parity compares; the property inherits.
      const root = document.getElementById('root')
      const titleBar = root
        ? createTitleBar({
            root,
            target: window,
            on,
            doubleClick: () => System.TitleBarDoubleClick(),
            draggable: computesDrag,
            info: () => System.Info(),
            schedule: (fn, ms) => {
              const id = setTimeout(fn, ms)
              return () => clearTimeout(id)
            },
          })
        : null
      const stops = [
        // First, so an error in the rest of the start leaves a line in choix.log.
        installErrorReports(window, System.ReportClientError),
        router.start(),
        // The session follows the route (subscribed before the menu context,
        // so a route's folder is opening by the time the menu hears of it).
        router.subscribe(() => session.route(router.current())),
        bindMenuContext(actions, {
          route: router.current,
          subscribeRoute: router.subscribe,
          folderState: (folderId) => scan.get().value?.[folderId]?.state,
          subscribeFolders: scan.subscribe,
        }),
        session.start(),
        actions.listen(on),
        scan.start(),
        quality.start(),
        settings.start(),
        library.start(),
        // The parity fixture's toasts; does nothing outside the parity build (parityUi.ts).
        seedParityToasts(toasts),
        // Back to the app: a drive may have come or gone meanwhile.
        on('common:WindowFocus', () => {
          Library.CheckNow().then(undefined, () => {})
        }),
        installKeys({
          run: actions.runAction,
          screen: () => actions.state().context.screen,
          singleKeysNative: () => singleKeysNative,
          dialogOpen: () => document.querySelector('[role="dialog"], [role="alertdialog"]') !== null,
          sheetOpen: () => sheet.get(),
        }),
        titleBar?.start() ?? (() => {}),
      ]
      System.Info().then(
        (info) => {
          singleKeysNative = info.singleKeysNative
        },
        () => {},
      )
      session.route(router.current())
      return () => {
        for (const stop of stops) stop()
      }
    },
  }
}

/** The route module of each route (see context.tsx for their contract). */
function RouteView({ route }: { route: Route }) {
  switch (route.name) {
    case 'library':
      return <LibraryRoute />
    case 'folder':
      return <FolderRoute />
    case 'focus':
      return <FocusRoute />
    case 'settings':
      return <SettingsRoute />
  }
}

/** The parity channel's key for the error screen's message (e2e/parity/errorScreen.ts). */
const RENDER_ERROR_KEY = 'shell.renderError'

const copyDetails = (text: string) => {
  void copyText(text, navigator, document)
}
const reload = () => reloadToLibrary(window.location)

/** No StrictMode around it (main.tsx): its doubled effects would open and close the folder session twice. */
export function App({ shell: given }: { shell?: Shell }) {
  const [shell] = useState(() => given ?? createShell())
  useEffect(() => shell.start(), [shell])
  const route = useSyncExternalStore(shell.router.subscribe, shell.router.current)
  const sheetOpen = useSyncExternalStore(shell.sheet.subscribe, shell.sheet.get)
  const { context } = useSyncExternalStore(shell.actions.subscribe, shell.actions.state)

  // The parity build's #40: the fixture names the error the screen shows.
  const parityError = useSyncExternalStore(subscribeParityUi, () => uiOverride<string>(RENDER_ERROR_KEY))

  return (
    <ShellContext.Provider value={shell}>
      <AppRoot>
        {typeof parityError === 'string' ? (
          <AppErrorScreen details={parityError} full={parityError} copy={copyDetails} reload={reload} />
        ) : (
          <RouteBoundary
            route={route}
            onError={(error, failed, stack) => {
              reportRenderError(System.ReportClientError, error, failed, stack)
              recoverFromRenderError(shell, error, failed)
            }}
            fallback={(f) =>
              // The Library failed too: the error screen. Elsewhere the route
              // above takes over at once (recoverFromRenderError).
              f.route.name === 'library' ? (
                <AppErrorScreen
                  details={f.error === undefined ? '' : renderErrorText(f.error)}
                  full={f.error === undefined ? '' : renderErrorDetails(f.error, f.componentStack)}
                  copy={copyDetails}
                  reload={reload}
                />
              ) : (
                <FallbackToaster />
              )
            }
          >
            <RouteView route={route} />
          </RouteBoundary>
        )}
        {sheetOpen && <ShortcutsSheet screen={context.screen} onClose={() => shell.sheet.set(false)} />}
      </AppRoot>
    </ShellContext.Provider>
  )
}
