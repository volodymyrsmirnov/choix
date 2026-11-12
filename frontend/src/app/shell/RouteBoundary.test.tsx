// The safety net around the routed screen (RouteBoundary.tsx): a screen that
// throws while rendering must never leave an empty window. There is no DOM
// in these tests, so the boundary's React lifecycle is driven by hand (the
// static and instance methods React calls) and its output is rendered with
// react-dom/server.
import { afterEach, beforeEach, describe, expect, spyOn, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import type { Route } from './router'
import { RouteBoundary, recoverFromRenderError, recoveryOf, renderErrorText, type RouteBoundaryProps } from './RouteBoundary'
import { ACTION_FAILED_TITLE, type ToastInput } from './toasts'

const LIBRARY: Route = { name: 'library' }
const FOLDER: Route = { name: 'folder', folderId: 'f1' }
const FOCUS: Route = { name: 'focus', folderId: 'f1', clusterId: 7 }
const SETTINGS: Route = { name: 'settings' }

function props(route: Route, over: Partial<RouteBoundaryProps> = {}): RouteBoundaryProps {
  return {
    route,
    onError: () => {},
    fallback: (f) => <p>fallback {f.route.name} {f.error === undefined ? '-' : renderErrorText(f.error)}</p>,
    children: <p>screen</p>,
    ...over,
  }
}

/** A boundary outside React: its setState is applied by hand, as React would once mounted. */
function boundary(p: RouteBoundaryProps): RouteBoundary {
  const b = new RouteBoundary(p)
  b.setState = ((s: object) => {
    b.state = { ...b.state, ...s }
  }) as typeof b.setState
  return b
}

describe('recoveryOf', () => {
  test('Focus falls back to its folder', () => {
    expect(recoveryOf(FOCUS)).toEqual({ name: 'folder', folderId: 'f1' })
  })
  test('the Folder page and Settings fall back to the Library', () => {
    expect(recoveryOf(FOLDER)).toEqual(LIBRARY)
    expect(recoveryOf(SETTINGS)).toEqual(LIBRARY)
  })
  test('the Library has nowhere to go: no navigation, so no loop', () => {
    expect(recoveryOf(LIBRARY)).toBeNull()
  })
})

describe('renderErrorText', () => {
  test("an Error's message", () => {
    expect(renderErrorText(new TypeError("Cannot read properties of undefined (reading 'x')"))).toBe(
      "Cannot read properties of undefined (reading 'x')",
    )
  })
  test('anything else, as text', () => {
    expect(renderErrorText('boom')).toBe('boom')
    expect(renderErrorText(new Error(''))).toBe('Error')
  })
})

describe('RouteBoundary', () => {
  let logged: unknown[][]
  let spy: ReturnType<typeof spyOn>
  beforeEach(() => {
    logged = []
    spy = spyOn(console, 'error').mockImplementation((...a: unknown[]) => {
      logged.push(a)
    })
  })
  afterEach(() => spy.mockRestore())

  test('renders the screen while nothing failed', () => {
    expect(renderToStaticMarkup(<RouteBoundary {...props(FOCUS)} />)).toBe('<p>screen</p>')
  })

  test('a render error switches to the fallback', () => {
    const b = boundary(props(FOCUS))
    b.state = { ...b.state, ...RouteBoundary.getDerivedStateFromProps(b.props, b.state) }
    b.state = { ...b.state, ...RouteBoundary.getDerivedStateFromError(new Error('x')) }
    expect(renderToStaticMarkup(<>{b.render()}</>)).toBe('<p>fallback focus -</p>')
  })

  test('the fallback gets the failure handed on: the first error, its component stack and the route', () => {
    const seen: unknown[] = []
    const b = boundary(props(LIBRARY, { fallback: (f) => (seen.push(f), null) }))
    b.state = { ...b.state, ...RouteBoundary.getDerivedStateFromProps(b.props, b.state) }
    b.state = { ...b.state, ...RouteBoundary.getDerivedStateFromError(new Error('first')) }
    const first = new RangeError('Invalid time value')
    b.componentDidCatch(first, { componentStack: '\n    at FolderRow' })
    b.componentDidCatch(new Error('second'), { componentStack: '\n    at Other' })
    b.render()
    expect(seen).toEqual([{ error: first, componentStack: '\n    at FolderRow', route: LIBRARY }])
  })

  test('the error is logged and handed to onError with the route that failed and the component stack', () => {
    const seen: [unknown, Route, string][] = []
    const err = new TypeError('bad payload')
    const b = boundary(props(FOCUS, { onError: (e, r, stack) => seen.push([e, r, stack]) }))
    b.componentDidCatch(err, { componentStack: '\n    at FocusContainer' })
    expect(seen).toEqual([[err, FOCUS, '\n    at FocusContainer']])
    expect(logged.length).toBe(1)
    expect(String(logged[0]![0])).toContain('focus')
    expect(logged[0]).toContain(err)
  })

  test('one failure is one recovery: errors React reports for the same render are handed on once', () => {
    // React retries a failed render once and may catch a different
    // component's error each time (the rail, then the stage): the mounted
    // test (e2e/ui/render-error.e2e.ts) saw three for one failure.
    const seen: string[] = []
    const b = boundary(props(FOCUS, { onError: (e) => seen.push((e as Error).message) }))
    b.componentDidCatch(new TypeError('e.split is not a function'), { componentStack: '' })
    b.componentDidCatch(new TypeError('e.lastIndexOf is not a function'), { componentStack: '' })
    b.componentDidCatch(new TypeError('e.lastIndexOf is not a function'), { componentStack: '' })
    expect(seen).toEqual(['e.split is not a function'])
    expect(logged.length).toBe(1)
  })

  test('the same route failing again after another route showed is a new failure', () => {
    const seen: Route[] = []
    const onError = (_e: unknown, r: Route) => seen.push(r)
    const b = boundary(props(FOCUS, { onError }))
    b.componentDidCatch(new Error('first'), { componentStack: '' })
    const prev = b.props
    ;(b as { props: RouteBoundaryProps }).props = props(FOLDER, { onError })
    b.componentDidUpdate(prev)
    ;(b as { props: RouteBoundaryProps }).props = props(FOCUS, { onError })
    b.componentDidUpdate(props(FOLDER, { onError }))
    b.componentDidCatch(new Error('again'), { componentStack: '' })
    expect(seen).toEqual([FOCUS, FOCUS])
  })

  test('another route clears the failure (the screen is tried again there)', () => {
    const b = boundary(props(FOCUS))
    let state = { ...b.state, ...RouteBoundary.getDerivedStateFromProps(b.props, b.state) }
    state = { ...state, ...RouteBoundary.getDerivedStateFromError(new Error('x')) }
    expect(state.failed).toBe(true)
    // The same route rendering again stays failed: no retry loop.
    expect(RouteBoundary.getDerivedStateFromProps(props(FOCUS), state)).toBeNull()
    const next = { ...state, ...RouteBoundary.getDerivedStateFromProps(props(FOLDER), state) }
    expect(next.failed).toBe(false)
  })
})

describe('recoverFromRenderError', () => {
  function deps(current: Route) {
    const pushed: ToastInput[] = []
    const went: [Route, unknown][] = []
    return {
      pushed,
      went,
      toasts: { push: (t: ToastInput) => (pushed.push(t), 'id') },
      router: {
        current: () => current,
        navigate: (to: Route, opts?: { force?: boolean }) => (went.push([to, opts]), true),
      },
    }
  }

  test("Focus failed: the error toast, then its folder (forced past Settings' guard)", () => {
    const d = deps(FOCUS)
    recoverFromRenderError(d, new TypeError('bad payload'), FOCUS)
    expect(d.pushed).toEqual([{ tone: 'reject', title: ACTION_FAILED_TITLE, body: 'bad payload' }])
    expect(d.went).toEqual([[{ name: 'folder', folderId: 'f1' }, { force: true }]])
  })

  test('the Library failed: no toast (the error screen says it), and no navigation (no loop)', () => {
    const d = deps(LIBRARY)
    recoverFromRenderError(d, new Error('x'), LIBRARY)
    expect(d.pushed).toEqual([])
    expect(d.went).toEqual([])
  })

  test('the router already moved on: it stays where it is', () => {
    const d = deps(LIBRARY)
    recoverFromRenderError(d, new Error('x'), FOCUS)
    expect(d.pushed.length).toBe(1)
    expect(d.went).toEqual([])
  })
})
