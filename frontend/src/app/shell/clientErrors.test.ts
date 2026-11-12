// The frontend's error reports to Go's log (clientErrors.ts → the System
// binding ReportClientError): a screen that failed to render, an uncaught
// error and an unhandled rejection each leave one line in choix.log.
import { describe, expect, test } from 'bun:test'
import { errorParts, installErrorReports, reportRenderError } from './clientErrors'

type Call = [string, string, string]

function recorder(fail = false) {
  const calls: Call[] = []
  const report = (where: string, message: string, stack: string) => {
    calls.push([where, message, stack])
    return fail ? Promise.reject(new Error('quitting')) : Promise.resolve()
  }
  return { calls, report }
}

/** A window stand-in: addEventListener / removeEventListener and a dispatch. */
function fakeWindow() {
  const listeners = new Map<string, Set<(e: Event) => void>>()
  return {
    addEventListener(type: string, fn: (e: Event) => void) {
      const set = listeners.get(type) ?? new Set()
      set.add(fn)
      listeners.set(type, set)
    },
    removeEventListener(type: string, fn: (e: Event) => void) {
      listeners.get(type)?.delete(fn)
    },
    dispatch(type: string, e: unknown) {
      for (const fn of [...(listeners.get(type) ?? [])]) fn(e as Event)
    },
    count: (type: string) => listeners.get(type)?.size ?? 0,
  }
}

describe('errorParts', () => {
  test("an Error: its message and stack", () => {
    const e = new TypeError('bad')
    e.stack = 'TypeError: bad\n    at x'
    expect(errorParts(e)).toEqual({ message: 'bad', stack: 'TypeError: bad\n    at x' })
  })
  test('an Error without a message names its type', () => {
    const e = new RangeError('')
    e.stack = ''
    expect(errorParts(e)).toEqual({ message: 'RangeError', stack: '' })
  })
  test('anything else, as text, without a stack', () => {
    expect(errorParts('boom')).toEqual({ message: 'boom', stack: '' })
    expect(errorParts(undefined)).toEqual({ message: 'undefined', stack: '' })
    expect(errorParts({ code: 'x' })).toEqual({ message: '[object Object]', stack: '' })
  })
})

describe('reportRenderError', () => {
  test('where is render:<route>; the stack is the error stack, then the component stack', async () => {
    const r = recorder()
    const e = new TypeError('bad payload')
    e.stack = 'TypeError: bad payload\n    at FocusScreen'
    reportRenderError(r.report, e, { name: 'focus', folderId: 'f', clusterId: 3 }, '\n    at FocusContainer\n    at FocusRoute')
    expect(r.calls).toEqual([
      ['render:focus', 'bad payload', 'TypeError: bad payload\n    at FocusScreen\n--- components\n    at FocusContainer\n    at FocusRoute'],
    ])
  })
  test('a report that fails is dropped (it never becomes another unhandled rejection)', async () => {
    const r = recorder(true)
    reportRenderError(r.report, new Error('x'), { name: 'library' }, '')
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(r.calls.length).toBe(1)
  })
})

describe('installErrorReports', () => {
  test("window 'error' and 'unhandledrejection' are reported; stop removes both listeners", () => {
    const w = fakeWindow()
    const r = recorder()
    const stop = installErrorReports(w, r.report)
    const err = new Error('thrown')
    err.stack = 'Error: thrown\n    at handler'
    w.dispatch('error', { message: 'Uncaught Error: thrown', error: err, filename: 'app.js', lineno: 1, colno: 2 })
    const reason = new TypeError('rejected')
    reason.stack = 'TypeError: rejected\n    at then'
    w.dispatch('unhandledrejection', { reason })
    w.dispatch('unhandledrejection', { reason: 'plain' })
    expect(r.calls).toEqual([
      ['window:error', 'thrown', 'Error: thrown\n    at handler'],
      ['window:unhandledrejection', 'rejected', 'TypeError: rejected\n    at then'],
      ['window:unhandledrejection', 'plain', ''],
    ])
    stop()
    expect(w.count('error')).toBe(0)
    expect(w.count('unhandledrejection')).toBe(0)
  })

  test('an error event without an Error object (a script error) reports its message and place', () => {
    const w = fakeWindow()
    const r = recorder()
    installErrorReports(w, r.report)
    w.dispatch('error', { message: 'Script error.', error: null, filename: 'x.js', lineno: 4, colno: 9 })
    expect(r.calls).toEqual([['window:error', 'Script error.', 'x.js:4:9']])
  })

  test('a resource that failed to load (an error event with no message) is not reported', () => {
    const w = fakeWindow()
    const r = recorder()
    installErrorReports(w, r.report)
    w.dispatch('error', { message: undefined, error: undefined })
    expect(r.calls).toEqual([])
  })
})
