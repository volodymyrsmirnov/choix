import { describe, expect, test } from 'bun:test'
import type { AppEvents, EventName, On } from './events'
import {
  createTitleBar,
  isTitleBarDoubleClick,
  TITLEBAR_BUTTON_GAP,
  TITLEBAR_CENTER_PROPERTY,
  TITLEBAR_DEFAULT_INSET,
  TITLEBAR_FULLSCREEN_INSET,
  TITLEBAR_INSET_PROPERTY,
  TITLEBAR_RETRY_MS,
  titleBarCenter,
  titleBarInset,
  type PointerDown,
  type TitleBarTarget,
} from './titleBar'

describe('titleBarInset', () => {
  test('the constants: the design default, the gap, the full-screen variant (pl-3), the properties', () => {
    expect(TITLEBAR_DEFAULT_INSET).toBe(76)
    expect(TITLEBAR_BUTTON_GAP).toBe(12)
    expect(TITLEBAR_FULLSCREEN_INSET).toBe(12)
    expect(TITLEBAR_INSET_PROPERTY).toBe('--titlebar-inset')
    expect(TITLEBAR_CENTER_PROPERTY).toBe('--titlebar-center')
  })

  test('max(76, buttons right + 12)', () => {
    expect(titleBarInset(79, false)).toBe(91) // macOS 27.2
    expect(titleBarInset(72, false)).toBe(84) // earlier releases
    expect(titleBarInset(64, false)).toBe(76) // exactly the default
    expect(titleBarInset(40, false)).toBe(76) // never narrower than the design
    expect(titleBarInset(79.4, false)).toBe(92) // whole pixels, never short of the gap
  })

  test('unknown (0, negative, not a number) leaves the default: null', () => {
    for (const v of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(titleBarInset(v, false)).toBeNull()
    }
  })

  test('full screen: the full-screen variant inset, measured or not', () => {
    expect(titleBarInset(79, true)).toBe(12)
    expect(titleBarInset(0, true)).toBe(12)
  })
})

describe('titleBarCenter', () => {
  test("the traffic lights' vertical centre, as measured", () => {
    expect(titleBarCenter(26, false)).toBe(26) // macOS 27.2
    expect(titleBarCenter(22.5, false)).toBe(22.5)
    expect(titleBarCenter(26.123456, false)).toBe(26.12) // two decimals at most
  })

  test('unknown (0, negative, not a number) leaves the design default: null', () => {
    for (const v of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(titleBarCenter(v, false)).toBeNull()
    }
  })

  test('full screen: no traffic lights, the design default: null', () => {
    expect(titleBarCenter(26, true)).toBeNull()
    expect(titleBarCenter(0, true)).toBeNull()
  })
})

/** A fake element: inside the title bar or not, inside a control or not, draggable or not. */
function el(opts: { inBar?: boolean; inControl?: boolean; drag?: boolean; w?: number; h?: number } = {}): TitleBarTarget & {
  drag: boolean
} {
  const { inBar = true, inControl = false, drag = true, w = 400, h = 48 } = opts
  return {
    drag,
    clientWidth: w,
    clientHeight: h,
    closest(selector: string) {
      if (selector === '[data-titlebar]') return inBar ? {} : null
      if (selector === '[data-titlebar-control]') return inControl ? {} : null
      throw new Error(`unexpected selector ${selector}`)
    },
  }
}

const draggable = (t: TitleBarTarget) => (t as unknown as { drag: boolean }).drag

function down(target: unknown, over: Partial<PointerDown> = {}): PointerDown {
  return { button: 0, detail: 2, offsetX: 10, offsetY: 10, target, ...over }
}

describe('isTitleBarDoubleClick', () => {
  test("the second press of a double-click on the bar's empty area", () => {
    expect(isTitleBarDoubleClick(down(el()), false, draggable)).toBe(true)
  })

  test('a single press, a triple press, another button', () => {
    expect(isTitleBarDoubleClick(down(el(), { detail: 1 }), false, draggable)).toBe(false)
    expect(isTitleBarDoubleClick(down(el(), { detail: 3 }), false, draggable)).toBe(false)
    expect(isTitleBarDoubleClick(down(el(), { button: 2 }), false, draggable)).toBe(false)
  })

  test('a control in the bar never triggers it, even one computing drag', () => {
    expect(isTitleBarDoubleClick(down(el({ inControl: true, drag: false })), false, draggable)).toBe(false)
    expect(isTitleBarDoubleClick(down(el({ inControl: true, drag: true })), false, draggable)).toBe(false)
  })

  test('not draggable, or outside the title bar', () => {
    expect(isTitleBarDoubleClick(down(el({ drag: false })), false, draggable)).toBe(false)
    expect(isTitleBarDoubleClick(down(el({ inBar: false })), false, draggable)).toBe(false)
  })

  test("outside the target's client box (a scrollbar), as Wails' test", () => {
    expect(isTitleBarDoubleClick(down(el({ w: 100 }), { offsetX: 100 }), false, draggable)).toBe(false)
    expect(isTitleBarDoubleClick(down(el(), { offsetY: -1 }), false, draggable)).toBe(false)
  })

  test('not in full screen', () => {
    expect(isTitleBarDoubleClick(down(el()), true, draggable)).toBe(false)
  })

  test('a target that is not an element', () => {
    expect(isTitleBarDoubleClick(down(null), false, draggable)).toBe(false)
    expect(isTitleBarDoubleClick(down({}), false, draggable)).toBe(false)
  })
})

/** A fake event bus: emit delivers to the handlers `on` registered. */
function bus() {
  const handlers = new Map<string, Set<(data: unknown) => void>>()
  const on: On = (name, handler) => {
    const set = handlers.get(name) ?? new Set()
    handlers.set(name, set)
    const h = handler as (data: unknown) => void
    set.add(h)
    return () => set.delete(h)
  }
  const emit = <K extends EventName>(name: K, data?: AppEvents[K]) => {
    for (const h of [...(handlers.get(name) ?? [])]) h(data)
  }
  return { on, emit, count: (name: string) => handlers.get(name)?.size ?? 0 }
}

/** Answers System.Info() from a list (the last one repeats), or fails while `fail` is set. */
type Info = { windowButtonsRight: number; windowButtonsCenterY: number; windowFullScreen: boolean }
type Answer = Info | 'fail'

const unknown: Info = { windowButtonsRight: 0, windowButtonsCenterY: 0, windowFullScreen: false }

function harness(answers: Answer[] = [unknown]) {
  const props = new Map<string, string>()
  let writes = 0
  const root = {
    style: {
      setProperty: (name: string, value: string) => {
        writes++
        props.set(name, value)
      },
      removeProperty: (name: string) => {
        writes++
        const v = props.get(name) ?? ''
        props.delete(name)
        return v
      },
    },
  }
  let listener: ((e: PointerDown) => void) | null = null
  let capture: boolean | undefined
  const target = {
    addEventListener: (_type: 'mousedown', l: (e: PointerDown) => void, o: { capture: boolean }) => {
      listener = l
      capture = o.capture
    },
    removeEventListener: (_type: 'mousedown', l: (e: PointerDown) => void) => {
      if (listener === l) listener = null
    },
  }
  // A manual clock: schedule records, tick runs the earliest timer.
  const timers: { fn: () => void; ms: number; cancelled: boolean }[] = []
  const schedule = (fn: () => void, ms: number) => {
    const t = { fn, ms, cancelled: false }
    timers.push(t)
    return () => {
      t.cancelled = true
    }
  }
  const pending = () => timers.filter((t) => !t.cancelled)
  const b = bus()
  let clicks = 0
  let asked = 0
  const bar = createTitleBar({
    root,
    target,
    on: b.on,
    draggable,
    doubleClick: () => {
      clicks++
      return Promise.resolve()
    },
    info: () => {
      const a = answers[Math.min(asked, answers.length - 1)]!
      asked++
      return a === 'fail' ? Promise.reject(new Error('busy')) : Promise.resolve(a)
    },
    schedule,
  })
  return {
    bar,
    props,
    bus: b,
    press: (e: PointerDown) => listener?.(e),
    listening: () => listener !== null,
    capture: () => capture,
    clicks: () => clicks,
    asked: () => asked,
    writes: () => writes,
    /** The delays of the timers still armed. */
    armed: () => pending().map((t) => t.ms),
    /** Runs the next armed timer, then lets the answer settle. */
    async tick() {
      const t = pending()[0]
      if (!t) throw new Error('no timer armed')
      t.cancelled = true
      t.fn()
      await settle()
    },
  }
}

const settle = async () => {
  for (let i = 0; i < 5; i++) await Promise.resolve()
}

/** An answer with the buttons ending at right; centred at 26 when right is known, unless given. */
const known = (right: number, fullScreen = false, centerY = right > 0 ? 26 : 0): Answer => ({
  windowButtonsRight: right,
  windowButtonsCenterY: centerY,
  windowFullScreen: fullScreen,
})

describe('createTitleBar', () => {
  test('the retry delays: a handful over the first seconds', () => {
    expect(TITLEBAR_RETRY_MS.length).toBeGreaterThanOrEqual(3)
    expect(TITLEBAR_RETRY_MS.length).toBeLessThanOrEqual(8)
    expect(TITLEBAR_RETRY_MS.reduce((a, b) => a + b, 0)).toBeLessThanOrEqual(10_000)
  })

  test('sets --titlebar-inset and --titlebar-center on the root from System.Info(); no retry once known', async () => {
    const h = harness([known(79)])
    const stop = h.bar.start()
    await settle()
    expect(h.props.get('--titlebar-inset')).toBe('91px')
    expect(h.props.get('--titlebar-center')).toBe('26px')
    expect(h.armed()).toEqual([])
    expect(h.asked()).toBe(1)
    stop()
  })

  test('a centre unknown while the edge is known: asked again until it arrives', async () => {
    const h = harness([known(79, false, 0), known(79, false, 0), known(79, false, 25.5)])
    const stop = h.bar.start()
    await settle()
    expect(h.props.get('--titlebar-inset')).toBe('91px')
    expect(h.props.has('--titlebar-center')).toBe(false)
    expect(h.armed()).toEqual([TITLEBAR_RETRY_MS[0]])
    await h.tick()
    expect(h.props.has('--titlebar-center')).toBe(false)
    await h.tick()
    expect(h.props.get('--titlebar-center')).toBe('25.5px')
    expect(h.armed()).toEqual([])
    // The window being shown asks nothing more once both are known.
    h.bus.emit('common:WindowShow')
    await settle()
    expect(h.asked()).toBe(3)
    stop()
  })

  test('an edge unknown while the centre is known: the centre is set, the edge asked again', async () => {
    const h = harness([known(0, false, 26), known(79, false, 26)])
    const stop = h.bar.start()
    await settle()
    expect(h.props.has('--titlebar-inset')).toBe(false)
    expect(h.props.get('--titlebar-center')).toBe('26px')
    await h.tick()
    expect(h.props.get('--titlebar-inset')).toBe('91px')
    expect(h.armed()).toEqual([])
    stop()
  })

  test('recovers from 0 (window not ready yet) and from a failed call: 0, fail, 79 → 91px', async () => {
    const h = harness([known(0), 'fail', known(79)])
    const stop = h.bar.start()
    await settle()
    expect(h.props.has('--titlebar-inset')).toBe(false)
    expect(h.armed()).toEqual([TITLEBAR_RETRY_MS[0]])
    await h.tick() // fails
    expect(h.props.has('--titlebar-inset')).toBe(false)
    expect(h.armed()).toEqual([TITLEBAR_RETRY_MS[1]])
    await h.tick() // 79
    expect(h.props.get('--titlebar-inset')).toBe('91px')
    expect(h.armed()).toEqual([])
    expect(h.asked()).toBe(3)
    stop()
  })

  test('a permanent 0 ends the retries and leaves the default', async () => {
    const h = harness([known(0)])
    const stop = h.bar.start()
    await settle()
    for (const ms of TITLEBAR_RETRY_MS) {
      expect(h.armed()).toEqual([ms])
      await h.tick()
    }
    expect(h.armed()).toEqual([])
    expect(h.asked()).toBe(1 + TITLEBAR_RETRY_MS.length)
    expect(h.props.has('--titlebar-inset')).toBe(false)
    expect(h.props.has('--titlebar-center')).toBe(false)
    expect(h.writes(), 'an unchanged answer never writes the root').toBe(0)
    stop()
  })

  test('the window being shown or leaving full screen asks again while the buttons are unknown', async () => {
    const h = harness([known(0)])
    const stop = h.bar.start()
    await settle()
    for (const _ of TITLEBAR_RETRY_MS) await h.tick()
    const before = h.asked()
    h.bus.emit('common:WindowShow')
    await settle()
    expect(h.asked()).toBe(before + 1)
    h.bus.emit('common:WindowFullscreen')
    h.bus.emit('common:WindowUnFullscreen')
    await settle()
    expect(h.asked()).toBe(before + 2)
    expect(h.armed()).toEqual([]) // the retry budget is spent: no new chain
    stop()

    // Known: the events change nothing but the full-screen state.
    const k = harness([known(79)])
    const stopK = k.bar.start()
    await settle()
    k.bus.emit('common:WindowShow')
    k.bus.emit('common:WindowUnFullscreen')
    await settle()
    expect(k.asked()).toBe(1)
    stopK()
  })

  test('starting in full screen: the full-screen inset at once, double-click detection off', async () => {
    const h = harness([known(79, true)])
    const stop = h.bar.start()
    await settle()
    expect(h.props.get('--titlebar-inset')).toBe('12px')
    expect(h.props.has('--titlebar-center'), 'no traffic lights in full screen: the design default').toBe(false)
    h.press(down(el()))
    expect(h.clicks()).toBe(0)
    h.bus.emit('common:WindowUnFullscreen')
    expect(h.props.get('--titlebar-inset')).toBe('91px')
    expect(h.props.get('--titlebar-center')).toBe('26px')
    h.press(down(el()))
    expect(h.clicks()).toBe(1)
    stop()
  })

  test('a full-screen event newer than the answer wins over it', async () => {
    let resolve: (a: Answer) => void = () => {}
    const props = new Map<string, string>()
    const b = bus()
    const bar = createTitleBar({
      root: { style: { setProperty: (n, v) => void props.set(n, v), removeProperty: (n) => String(props.delete(n)) } },
      target: { addEventListener: () => {}, removeEventListener: () => {} },
      on: b.on,
      draggable,
      doubleClick: () => Promise.resolve(),
      info: () => new Promise((r) => (resolve = r as (a: Answer) => void)),
      schedule: () => () => {},
    })
    const stop = bar.start()
    b.emit('common:WindowFullscreen') // after the question, before the answer
    resolve(known(79, false)) // read before the window entered full screen
    await settle()
    expect(props.get('--titlebar-inset')).toBe('12px')
    stop()
  })

  test('full screen switches to the full-screen inset and back', async () => {
    const h = harness([known(79)])
    const stop = h.bar.start()
    await settle()
    h.bus.emit('common:WindowFullscreen')
    expect(h.props.get('--titlebar-inset')).toBe('12px')
    expect(h.props.has('--titlebar-center')).toBe(false)
    h.bus.emit('common:WindowUnFullscreen')
    expect(h.props.get('--titlebar-inset')).toBe('91px')
    expect(h.props.get('--titlebar-center')).toBe('26px')

    // Unmeasured: back to the design default (the property removed).
    const u = harness([known(0)])
    const stopU = u.bar.start()
    await settle()
    u.bus.emit('common:WindowFullscreen')
    expect(u.props.get('--titlebar-inset')).toBe('12px')
    u.bus.emit('common:WindowUnFullscreen')
    expect(u.props.has('--titlebar-inset')).toBe(false)
    expect(u.props.has('--titlebar-center')).toBe(false)
    stop()
    stopU()
  })

  test('a double-click on the bar calls the binding once; not in full screen, not on a control', async () => {
    const h = harness()
    const stop = h.bar.start()
    await settle()
    expect(h.capture()).toBe(true)
    h.press(down(el(), { detail: 1 }))
    h.press(down(el()))
    expect(h.clicks()).toBe(1)
    h.press(down(el({ inControl: true, drag: false })))
    expect(h.clicks()).toBe(1)
    h.bus.emit('common:WindowFullscreen')
    h.press(down(el()))
    expect(h.clicks()).toBe(1)
    h.bus.emit('common:WindowUnFullscreen')
    h.press(down(el()))
    expect(h.clicks()).toBe(2)
    stop()
  })

  test('stop removes the listener, the subscriptions and the armed retry; a late answer changes nothing', async () => {
    let resolve: (a: Answer) => void = () => {}
    const h = harness([known(0)])
    const stop = h.bar.start()
    await settle()
    expect(h.listening()).toBe(true)
    expect(h.bus.count('common:WindowFullscreen')).toBe(1)
    expect(h.armed().length).toBe(1)
    stop()
    expect(h.listening()).toBe(false)
    expect(h.bus.count('common:WindowFullscreen')).toBe(0)
    expect(h.bus.count('common:WindowUnFullscreen')).toBe(0)
    expect(h.bus.count('common:WindowShow')).toBe(0)
    expect(h.armed()).toEqual([])

    const props = new Map<string, string>()
    const bar = createTitleBar({
      root: { style: { setProperty: (n, v) => void props.set(n, v), removeProperty: (n) => String(props.delete(n)) } },
      target: { addEventListener: () => {}, removeEventListener: () => {} },
      on: bus().on,
      draggable,
      doubleClick: () => Promise.resolve(),
      info: () => new Promise((r) => (resolve = r as (a: Answer) => void)),
      schedule: () => () => {},
    })
    bar.start()()
    resolve(known(79))
    await settle()
    expect(props.size).toBe(0)
  })

  test('a failed binding call is swallowed (quitting answers unavailable)', async () => {
    const b = bus()
    let target: ((e: PointerDown) => void) | null = null
    const bar = createTitleBar({
      root: { style: { setProperty: () => {}, removeProperty: () => '' } },
      target: {
        addEventListener: (_t: 'mousedown', l: (e: PointerDown) => void) => {
          target = l
        },
        removeEventListener: () => {},
      },
      on: b.on,
      draggable,
      doubleClick: () => Promise.reject(new Error('unavailable')),
      info: () => Promise.resolve(unknown),
      schedule: () => () => {},
    })
    const stop = bar.start()
    await settle()
    target!(down(el()))
    await settle()
    stop()
  })
})
