// The window's title bar, the app's side (the design's TitleBar draws it;
// internal/app/window measures the window and acts on it).
//
// Inset: the design reserves 76 px on the left for the traffic lights and
// reads the inherited custom property --titlebar-inset (default 76 px). Once
// System.Info() reports where the buttons really end (windowButtonsRight,
// CSS px from the window's left edge; 0 when unknown), the app sets the
// property on #root to max(76, that edge + TITLEBAR_BUTTON_GAP). Go answers
// 0 until the window is ready (shown, or its page's runtime loaded), so an
// unknown answer or a failed call is asked again after each delay of
// TITLEBAR_RETRY_MS (one shared budget), and again when the window is shown
// or leaves full screen; asking stops once the buttons are known.
//
// Vertical centre: the design reads --titlebar-center (default: its
// approved look), the y in CSS px from the top of the window where the
// bar's content is centred. System.Info().windowButtonsCenterY is the
// traffic lights' common vertical centre (0 when unknown); the app sets the
// property on #root to it, and removes it when unknown or in full screen
// (no traffic lights there: the design default). "Known" for the retries
// above means both the edge and the centre.
//
// Full screen: the buttons are gone, so the property is the design's
// full-screen inset instead, and every screen's bar looks like the
// full-screen variant. The state starts from System.Info().windowFullScreen
// (the window's current state: a page reloaded in full screen starts right)
// and then follows Wails' common window events; an answer older than the
// last of those events does not override it. The server build sends no
// event and answers false.
//
// Double-click: a native title bar zooms (or minimises, or does nothing) on
// a double-click, as the user's setting says. Wails' runtime already turns a
// dblclick on a draggable element into `wails:drag:doubleclick`, and its Go
// side acts for the settings it knows ("", Maximize, Minimize) and ignores
// the rest (Fill, …). This module reports the same double-click — detected
// on the second press, since Wails stops the dblclick event itself — to
// System.TitleBarDoubleClick, whose Go side acts only for the settings Wails
// ignores (internal/app/window choixDoubleClick). So for any one setting
// exactly one side acts, whichever event arrives first. Controls inside the
// bar never count, and nothing happens in full screen.
import type { On } from './events'

/** The design's inset (TitleBar's default when --titlebar-inset is not set). */
export const TITLEBAR_DEFAULT_INSET = 76
/** Space kept between the right-most traffic light and the bar's content. */
export const TITLEBAR_BUTTON_GAP = 12
/** The inset of the design's full-screen variant (its pl-3). */
export const TITLEBAR_FULLSCREEN_INSET = 12
/** The custom property the design's TitleBar reads for its left inset. */
export const TITLEBAR_INSET_PROPERTY = '--titlebar-inset'
/** The custom property the design's TitleBar reads for its content's vertical centre (px from the window's top). */
export const TITLEBAR_CENTER_PROPERTY = '--titlebar-center'
/** The delays (ms) before asking again while the buttons are unknown: about 8 s in all, then the default stays. */
export const TITLEBAR_RETRY_MS = [250, 500, 1000, 2000, 4000] as const

/**
 * The bar's left inset in px for buttons ending at windowButtonsRight, or
 * null to leave the design's default (the buttons are unknown).
 */
export function titleBarInset(windowButtonsRight: number, fullScreen: boolean): number | null {
  if (fullScreen) return TITLEBAR_FULLSCREEN_INSET
  if (!Number.isFinite(windowButtonsRight) || windowButtonsRight <= 0) return null
  return Math.max(TITLEBAR_DEFAULT_INSET, Math.ceil(windowButtonsRight + TITLEBAR_BUTTON_GAP))
}

/**
 * The y (px from the window's top) to centre the bar's content on for
 * traffic lights centred at windowButtonsCenterY, or null to leave the
 * design's default (unknown, or full screen). At most two decimals.
 */
export function titleBarCenter(windowButtonsCenterY: number, fullScreen: boolean): number | null {
  if (fullScreen || !Number.isFinite(windowButtonsCenterY) || windowButtonsCenterY <= 0) return null
  return Math.round(windowButtonsCenterY * 100) / 100
}

/** The element a press landed on, as far as the double-click test reads it. */
export interface TitleBarTarget {
  closest(selector: string): unknown
  clientWidth: number
  clientHeight: number
}

/** The fields of a mousedown event the double-click test reads. */
export interface PointerDown {
  button: number
  /** The click count: 2 on the second press of a double-click. */
  detail: number
  target: unknown
  offsetX: number
  offsetY: number
}

function isTarget(t: unknown): t is TitleBarTarget {
  return typeof t === 'object' && t !== null && typeof (t as { closest?: unknown }).closest === 'function'
}

/**
 * Whether e is the second press of a primary-button double-click on the
 * title bar's empty area: inside [data-titlebar], outside every
 * [data-titlebar-control], on an element Wails would drag by (draggable:
 * its computed --wails-draggable is `drag`) and inside its client box
 * (Wails' own test, runtime drag.ts isDraggableEvent). Never in full screen.
 */
export function isTitleBarDoubleClick(e: PointerDown, fullScreen: boolean, draggable: (t: TitleBarTarget) => boolean): boolean {
  if (fullScreen || e.button !== 0 || e.detail !== 2) return false
  const t = e.target
  if (!isTarget(t)) return false
  if (!t.closest('[data-titlebar]') || t.closest('[data-titlebar-control]')) return false
  return draggable(t) && e.offsetX >= 0 && e.offsetX < t.clientWidth && e.offsetY >= 0 && e.offsetY < t.clientHeight
}

/** Whether an element computes --wails-draggable: drag (what Wails' runtime reads). */
export function computesDrag(t: TitleBarTarget): boolean {
  return getComputedStyle(t as unknown as Element).getPropertyValue('--wails-draggable').trim() === 'drag'
}

export interface TitleBarDeps {
  /** The element the inset property is set on (#root, outside what parity compares). */
  root: { style: { setProperty(name: string, value: string): void; removeProperty(name: string): string } }
  /** Where the press listener goes (window), in the capture phase. */
  target: {
    addEventListener(type: 'mousedown', listener: (e: PointerDown) => void, options: { capture: boolean }): void
    removeEventListener(type: 'mousedown', listener: (e: PointerDown) => void, options: { capture: boolean }): void
  }
  on: On
  /** System.TitleBarDoubleClick. */
  doubleClick: () => Promise<unknown>
  draggable: (t: TitleBarTarget) => boolean
  /** System.Info(): the fields read here. */
  info: () => Promise<{ windowButtonsRight: number; windowButtonsCenterY: number; windowFullScreen: boolean }>
  /** Runs fn after ms (setTimeout); returns the cancel. */
  schedule: (fn: () => void, ms: number) => () => void
}

export interface TitleBarShell {
  /** Asks System.Info(), follows full screen and listens for double-clicks; returns the stop. */
  start(): () => void
}

export function createTitleBar(d: TitleBarDeps): TitleBarShell {
  let right = 0
  let centerY = 0
  let fullScreen = false
  /** Counts full-screen events, so an answer read before the last one does not override it. */
  let fullScreenEvents = 0
  let retries = 0
  let cancelRetry: (() => void) | null = null
  let stopped = false

  const positive = (v: number) => Number.isFinite(v) && v > 0
  const known = () => positive(right) && positive(centerY)

  /** The values last written to the root (null: none); the root is written only when one changes. */
  const applied = new Map<string, string | null>()
  const write = (property: string, px: number | null) => {
    const value = px === null ? null : `${px}px`
    if ((applied.get(property) ?? null) === value) return
    applied.set(property, value)
    if (value === null) d.root.style.removeProperty(property)
    else d.root.style.setProperty(property, value)
  }
  const apply = () => {
    write(TITLEBAR_INSET_PROPERTY, titleBarInset(right, fullScreen))
    write(TITLEBAR_CENTER_PROPERTY, titleBarCenter(centerY, fullScreen))
  }

  /** Arms the next retry, unless one is armed or the budget is spent. */
  const retry = () => {
    if (stopped || known() || cancelRetry || retries >= TITLEBAR_RETRY_MS.length) return
    cancelRetry = d.schedule(() => {
      cancelRetry = null
      ask()
    }, TITLEBAR_RETRY_MS[retries++]!)
  }

  const ask = () => {
    const seen = fullScreenEvents
    d.info().then(
      (info) => {
        if (stopped) return
        if (seen === fullScreenEvents) fullScreen = info.windowFullScreen === true
        if (positive(info.windowButtonsRight)) right = info.windowButtonsRight
        if (positive(info.windowButtonsCenterY)) centerY = info.windowButtonsCenterY
        apply()
        retry()
      },
      () => retry(),
    )
  }

  /** Asks again on an event that can change the answer, while the buttons are not fully known. */
  const refresh = () => {
    if (!known()) ask()
  }

  const onPress = (e: PointerDown) => {
    if (!isTitleBarDoubleClick(e, fullScreen, d.draggable)) return
    // Unavailable while quitting; nothing to tell the user either way.
    d.doubleClick().then(undefined, () => {})
  }

  return {
    start() {
      const options = { capture: true }
      d.target.addEventListener('mousedown', onPress, options)
      const stops = [
        d.on('common:WindowFullscreen', () => {
          fullScreenEvents++
          fullScreen = true
          apply()
        }),
        d.on('common:WindowUnFullscreen', () => {
          fullScreenEvents++
          fullScreen = false
          apply()
          refresh()
        }),
        d.on('common:WindowShow', refresh),
      ]
      ask()
      return () => {
        stopped = true
        cancelRetry?.()
        cancelRetry = null
        d.target.removeEventListener('mousedown', onPress, options)
        for (const stop of stops) stop()
      }
    },
  }
}
