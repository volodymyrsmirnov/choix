// Hash routes ([call] C30): the app is one page in one window, so the route
// lives in the location hash and nothing reloads the page.
//
//   #/                                Library
//   #/folder/<id>                     Folder
//   #/folder/<id>/focus/<clusterId>   Focus (and its compare mode)
//   #/settings                        Settings
//
// back() goes Focus → Folder → Library, and Settings → the route it was
// opened from (or, once that folder has gone away, wherever
// retargetSettings() says; a navigation the guard is holding that leads into
// the gone folder is moved there too). Scroll offsets are kept per route in
// memory, so leaving a screen and coming back restores where the user was.

export type Route =
  | { name: 'library' }
  | { name: 'folder'; folderId: string }
  | { name: 'focus'; folderId: string; clusterId: number }
  | { name: 'settings' }

const LIBRARY: Route = { name: 'library' }

/** The route of a location hash; anything unknown is the Library. */
export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#/, '')
  if (path === '/settings') return { name: 'settings' }
  const m = /^\/folder\/([^/]+)(?:\/focus\/(\d+))?$/.exec(path)
  if (!m) return LIBRARY
  let folderId: string
  try {
    folderId = decodeURIComponent(m[1])
  } catch {
    return LIBRARY
  }
  if (m[2] === undefined) return { name: 'folder', folderId }
  return { name: 'focus', folderId, clusterId: Number(m[2]) }
}

/** The location hash of a route. */
export function routeHash(r: Route): string {
  switch (r.name) {
    case 'library':
      return '#/'
    case 'settings':
      return '#/settings'
    case 'folder':
      return `#/folder/${encodeURIComponent(r.folderId)}`
    case 'focus':
      return `#/folder/${encodeURIComponent(r.folderId)}/focus/${r.clusterId}`
  }
}

/** The folder a route shows (its Folder or Focus), else null. */
function folderOf(r: Route): string | null {
  return r.name === 'folder' || r.name === 'focus' ? r.folderId : null
}

/** Where the hash lives: the window in the app, a fake in tests. */
export interface HashHost {
  getHash(): string
  setHash(hash: string): void
  onHashChange(fn: () => void): () => void
}

export function windowHashHost(): HashHost {
  return {
    getHash: () => window.location.hash,
    setHash: (hash) => {
      window.location.hash = hash
    },
    onHashChange: (fn) => {
      window.addEventListener('hashchange', fn)
      return () => window.removeEventListener('hashchange', fn)
    },
  }
}

/**
 * Asked before every navigation the app makes; false holds it (Settings
 * opens its leave-without-saving prompt, then navigates with `force`).
 */
export type RouteGuard = (to: Route) => boolean

export interface Router {
  current(): Route
  /** Goes to `to`; false when the guard held it. Navigating to the current route does nothing. */
  navigate(to: Route, opts?: { force?: boolean }): boolean
  back(): void
  /** The route Settings was opened from (the Library when it was not opened from anywhere). */
  settingsFrom(): Route
  /**
   * Settings returns to `to` from now on, without leaving it (the folder it
   * was opened from went away). Subscribers hear of a change.
   */
  retargetSettings(to: Route): void
  /**
   * The navigation the guard last held (Settings' prompt leaves for it on
   * Discard); null once a navigation goes through.
   */
  held(): Route | null
  setGuard(guard: RouteGuard | null): void
  saveScroll(route: Route, top: number): void
  scrollOf(route: Route): number
  /** For useSyncExternalStore. */
  subscribe(listener: () => void): () => void
  /** Follows hash changes made outside navigate(); returns stop. */
  start(): () => void
}

export function createRouter(host: HashHost): Router {
  let route = parseRoute(host.getHash())
  let from: Route = LIBRARY
  let guard: RouteGuard | null = null
  let held: Route | null = null
  const scroll = new Map<string, number>()
  const listeners = new Set<() => void>()

  /** Makes `to` current; false when it already is. */
  const apply = (to: Route): boolean => {
    if (routeHash(to) === routeHash(route)) return false
    if (to.name === 'settings' && route.name !== 'settings') from = route
    route = to
    held = null
    for (const l of [...listeners]) l()
    return true
  }

  const navigate: Router['navigate'] = (to, opts) => {
    if (routeHash(to) === routeHash(route)) return true
    if (!opts?.force && guard && !guard(to)) {
      held = to
      return false
    }
    apply(to)
    host.setHash(routeHash(to))
    return true
  }

  return {
    current: () => route,
    navigate,
    back() {
      switch (route.name) {
        case 'focus':
          navigate({ name: 'folder', folderId: route.folderId })
          return
        case 'folder':
          navigate(LIBRARY)
          return
        case 'settings':
          navigate(from)
          return
        case 'library':
          return
      }
    },
    settingsFrom: () => from,
    retargetSettings(to) {
      if (routeHash(to) === routeHash(from)) return
      const gone = folderOf(from)
      from = to
      // A held navigation into the folder Settings no longer returns to follows it.
      if (held && gone !== null && folderOf(held) === gone) held = to
      for (const l of [...listeners]) l()
    },
    held: () => held,
    setGuard(g) {
      guard = g
    },
    saveScroll(r, top) {
      scroll.set(routeHash(r), top)
    },
    scrollOf: (r) => scroll.get(routeHash(r)) ?? 0,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    start() {
      return host.onHashChange(() => {
        apply(parseRoute(host.getHash()))
      })
    },
  }
}
