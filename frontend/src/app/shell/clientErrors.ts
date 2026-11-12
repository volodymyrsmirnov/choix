// Errors the page could not handle, written to Go's log (choix.log) through
// the System binding ReportClientError, so a failure leaves evidence even
// when nobody reads the screen: a screen that failed to render
// (RouteBoundary, `render:<route>`), and an uncaught error or an unhandled
// rejection on the window (`window:error`, `window:unhandledrejection`).
// Go cuts each report to a bounded size and rate-limits them. A report that
// fails (quit began) is dropped: it never becomes another unhandled
// rejection.
import type { Route } from './router'

export type ReportFn = (where: string, message: string, stack: string) => PromiseLike<unknown>

/** An error's message and stack as text (anything that is not an Error has no stack). */
export function errorParts(error: unknown): { message: string; stack: string } {
  if (error instanceof Error) return { message: error.message || error.name, stack: error.stack ?? '' }
  return { message: String(error), stack: '' }
}

function send(report: ReportFn, where: string, message: string, stack: string): void {
  try {
    report(where, message, stack).then(undefined, () => {})
  } catch {
    // The binding could not even be called: nothing else to do.
  }
}

/** A screen that failed to render: its error, then React's component stack. */
export function reportRenderError(report: ReportFn, error: unknown, route: Route, componentStack: string): void {
  const { message, stack } = errorParts(error)
  const components = componentStack.trim() === '' ? '' : `\n--- components${componentStack.startsWith('\n') ? '' : '\n'}${componentStack}`
  send(report, `render:${route.name}`, message, `${stack}${components}`)
}

interface ErrorEventLike {
  message?: unknown
  error?: unknown
  filename?: unknown
  lineno?: unknown
  colno?: unknown
}

/** The window, or a test's stand-in. */
export interface ListenerTarget {
  addEventListener(type: string, fn: (e: Event) => void): void
  removeEventListener(type: string, fn: (e: Event) => void): void
}

/** Reports the window's uncaught errors and unhandled rejections; returns stop. */
export function installErrorReports(target: ListenerTarget, report: ReportFn): () => void {
  const onError = (ev: Event) => {
    const e = ev as unknown as ErrorEventLike
    if (e.error instanceof Error) {
      const { message, stack } = errorParts(e.error)
      send(report, 'window:error', message, stack)
      return
    }
    // An element that failed to load fires a plain `error` without a message: not ours to report.
    if (typeof e.message !== 'string' || e.message === '') return
    const at = typeof e.filename === 'string' && e.filename !== '' ? `${e.filename}:${String(e.lineno)}:${String(e.colno)}` : ''
    send(report, 'window:error', e.message, at)
  }
  const onRejection = (ev: Event) => {
    const { message, stack } = errorParts((ev as unknown as { reason?: unknown }).reason)
    send(report, 'window:unhandledrejection', message, stack)
  }
  target.addEventListener('error', onError)
  target.addEventListener('unhandledrejection', onRejection)
  return () => {
    target.removeEventListener('error', onError)
    target.removeEventListener('unhandledrejection', onRejection)
  }
}
