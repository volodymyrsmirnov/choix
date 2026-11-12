// The safety net around the routed screen. Without it, an exception thrown
// while a screen renders unmounts the whole React tree and leaves the window
// empty (only the traffic lights), while the shell (router, keys, menu)
// keeps running underneath. With it, the failure is logged, the route that
// failed is left for the one above it (Focus → its folder, the Folder page or
// Settings → the Library), the shell's error toast says what happened, and
// the error goes to Go's log (clientErrors.ts), where it stays.
//
// No loop: the Library has no route above it, so a Library that fails to
// render stays failed and shows the fallback (the design's error screen)
// until the route changes; a route that fails is tried again only after another route
// was shown. The boundary resets whenever the route changes.
import { Component, type ErrorInfo, type ReactNode } from 'react'
import { routeHash, type Route } from './router'
import { ACTION_FAILED_TITLE, type Toasts } from './toasts'

export interface RouteBoundaryProps {
  /** The route whose screen is inside. */
  route: Route
  /** Called once per failure, after it is logged: recover (navigate, toast) and report it to Go's log. */
  onError(error: unknown, route: Route, componentStack: string): void
  /** What shows while the screen has failed (the failure is filled in once it was handed on). */
  fallback(failure: RenderFailure): ReactNode
  children: ReactNode
}

/** A screen's failure as the fallback sees it. */
export interface RenderFailure {
  route: Route
  /** The error handed on (the first one React caught); undefined until then. */
  error: unknown
  componentStack: string
}

interface RouteBoundaryState {
  /** routeHash of the route the children were last rendered for. */
  route: string
  failed: boolean
  /** The failure handed on (componentDidCatch), for the fallback. */
  caught: { error: unknown; componentStack: string } | null
}

/** Where a screen that failed to render goes: the route above it, or null for the Library. */
export function recoveryOf(route: Route): Route | null {
  switch (route.name) {
    case 'focus':
      return { name: 'folder', folderId: route.folderId }
    case 'folder':
    case 'settings':
      return { name: 'library' }
    case 'library':
      return null
  }
}

/** The text of a render error, for the toast's body. */
export function renderErrorText(error: unknown): string {
  if (error instanceof Error) return error.message || error.name
  return String(error)
}

/**
 * The app's recovery from a screen that failed to render: the shell's error
 * toast with the error's text (the existing "Action failed" toast; no copy of
 * its own), then the route above the failed one, while the router still shows
 * it. Forced: a failed Settings page has no edits worth its leave prompt.
 */
export function recoverFromRenderError(
  deps: {
    toasts: Pick<Toasts, 'push'>
    router: { current(): Route; navigate(to: Route, opts?: { force?: boolean }): unknown }
  },
  error: unknown,
  failed: Route,
): void {
  const to = recoveryOf(failed)
  // The Library has nowhere to go: the error screen (the boundary's fallback) says what happened.
  if (to === null) return
  deps.toasts.push({ tone: 'reject', title: ACTION_FAILED_TITLE, body: renderErrorText(error) })
  if (routeHash(deps.router.current()) !== routeHash(failed)) return
  deps.router.navigate(to, { force: true })
}

export class RouteBoundary extends Component<RouteBoundaryProps, RouteBoundaryState> {
  override state: RouteBoundaryState = { route: '', failed: false, caught: null }

  static getDerivedStateFromProps(props: RouteBoundaryProps, state: RouteBoundaryState): Partial<RouteBoundaryState> | null {
    const route = routeHash(props.route)
    return route === state.route ? null : { route, failed: false, caught: null }
  }

  static getDerivedStateFromError(_error: unknown): Partial<RouteBoundaryState> {
    return { failed: true }
  }

  /**
   * routeHash of the route whose failure was handed on: React retries a
   * failed render once and can catch another component's error each time,
   * so one failure may arrive as several componentDidCatch calls. Cleared
   * when the route changes.
   */
  private reported: string | null = null

  override componentDidUpdate(prev: RouteBoundaryProps): void {
    if (routeHash(prev.route) !== routeHash(this.props.route)) this.reported = null
  }

  override componentDidCatch(error: unknown, info: ErrorInfo): void {
    const { route } = this.props
    const hash = routeHash(route)
    if (this.reported === hash) return
    this.reported = hash
    const stack = info.componentStack ?? ''
    console.error(`choix: the ${route.name} screen failed to render`, error, stack)
    this.setState({ caught: { error, componentStack: stack } })
    this.props.onError(error, route, stack)
  }

  override render(): ReactNode {
    if (!this.state.failed) return this.props.children
    const { caught } = this.state
    return this.props.fallback({ route: this.props.route, error: caught?.error, componentStack: caught?.componentStack ?? '' })
  }
}
