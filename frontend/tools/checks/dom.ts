// Shared vocabulary of the state checks: the finding shape, and the
// page-side DOM helpers (window.__checkDom) that the in-page checks use.
// installDom() evaluates defineDom() in the page once per document; every
// in-page check function is self-contained apart from window.__checkDom,
// because Playwright serialises it with Function#toString.
//
// Markers the checks read (each check's header has the details):
//   data-app-root            the app surface every check measures (AppRoot)
//   data-scroll              a scroller: its overflow is not clipping (clip)
//   data-clip="intended"     cropped on purpose: not clipping (clip)
//   data-overlay="<id>"      an overlay's outermost element (occlusion, focus)
//   data-focus-ring          draws the focus ring of the control inside (focus)
//   data-pointer-only="<why>" a pointer affordance the keyboard replaces: not
//                            required to be reachable; must give the reason,
//                            have tabIndex < 0 and aria-hidden="true" (focus)
//   data-align-x|y="<group>" these line up within 0.5 px (align)
//   data-centered="x|y|both", data-center-box   centred in its box (centred)
//   data-state-subject       what the state's label names: in view (subject)
//   data-measured="<props>", data-measured-by="<id>"   computed from layout:
//                            left out of the fingerprint (measured.ts)
//   [inert]                  out of reach: not a control (visible()); only a
//                            backdrop behind an overlay may set it (lint-design)
import type { Page } from '@playwright/test'

/** One failed expectation of one state, at one size, in one browser. */
export interface Finding {
  check: string
  state: number
  size: string
  browser: string
  message: string
  selector?: string
}

/** What a check reports from inside the page; the runner adds the context. */
export interface Raw {
  check: string
  message: string
  selector?: string
}

export interface Ctx {
  state: number
  size: string
  browser: string
}

export function withCtx(ctx: Ctx, raw: Raw[]): Finding[] {
  return raw.map((r) => ({
    check: r.check,
    state: ctx.state,
    size: ctx.size,
    browser: ctx.browser,
    message: r.message,
    ...(r.selector ? { selector: r.selector } : {}),
  }))
}

/** The computed focus-indicator properties of a control (or its [data-focus-ring] ancestor). */
export interface Ring {
  outlineStyle: string
  /** px */
  outlineWidth: number
  outlineColor: string
  boxShadow: string
  /** Widths, styles and colours of the four borders, as one string. */
  border: string
}

export interface CheckDom {
  /** Anything a user operates (focusable or with an interactive role). */
  CONTROL: string
  /** The outermost element of an open overlay: a `data-overlay` marker or an overlay role. */
  OVERLAY: string
  /** Containers whose items are reached with arrow keys from one Tab stop. */
  COMPOSITE: string
  root(): HTMLElement
  /** A readable path for messages: up to four ancestors, then a text snippet. */
  selectorOf(el: Element): string
  /** Rendered with a non-zero box, not visibility-hidden, not inert. */
  visible(el: Element): boolean
  /** Visible controls inside the app root, in document order. */
  controls(): HTMLElement[]
  enabled(el: Element): boolean
  /** The focus-indicator look of a control: its own, or its nearest [data-focus-ring] ancestor's. */
  ringOf(el: Element): Ring
  /** Computed visual properties of `el` and every descendant, for look comparisons. */
  lookOf(el: Element): string[]
}

declare global {
  interface Window {
    __checkDom?: CheckDom
  }
}

export async function installDom(page: Page): Promise<void> {
  await page.evaluate(defineDom)
}

function defineDom(): void {
  const CONTROL = [
    'a[href]',
    'button',
    'input:not([type="hidden"])',
    'select',
    'textarea',
    'summary',
    '[contenteditable=""]',
    '[contenteditable="true"]',
    '[tabindex]:not([tabindex="-1"])',
    ...[
      'button',
      'link',
      'menuitem',
      'menuitemradio',
      'menuitemcheckbox',
      'option',
      'tab',
      'radio',
      'checkbox',
      'switch',
      'slider',
      'spinbutton',
      'gridcell',
      'treeitem',
    ].map((r) => `[role="${r}"]`),
  ].join(', ')
  const OVERLAY = ['[data-overlay]', ...['menu', 'listbox', 'dialog', 'alertdialog', 'tooltip'].map((r) => `[role="${r}"]`)].join(', ')
  const COMPOSITE = ['menu', 'listbox', 'toolbar', 'tablist', 'radiogroup', 'grid'].map((r) => `[role="${r}"]`).join(', ')
  const NAMED = ['data-folder', 'data-overlay', 'role', 'data-testid', 'aria-label', 'data-align-y', 'data-align-x']
  const LOOK = [
    'color',
    'background-color',
    'background-image',
    'border-top-color',
    'border-right-color',
    'border-bottom-color',
    'border-left-color',
    'border-top-width',
    'outline-style',
    'outline-width',
    'outline-color',
    'outline-offset',
    'box-shadow',
    'opacity',
    'filter',
    'transform',
    'text-decoration-line',
    'fill',
    'stroke',
  ]

  const root = (): HTMLElement => {
    const r = document.querySelector<HTMLElement>('[data-app-root]')
    if (!r) throw new Error('no [data-app-root] on the page')
    return r
  }

  const selectorOf = (el: Element): string => {
    const top = document.querySelector('[data-app-root]')
    const parts: string[] = []
    let e: Element | null = el
    while (e && e !== top && e !== document.body && parts.length < 4) {
      let p = e.tagName.toLowerCase()
      if (e.id) p += `#${e.id}`
      for (const a of NAMED) {
        const v = e.getAttribute(a)
        if (v !== null) {
          p += v === '' ? `[${a}]` : `[${a}="${v.length > 40 ? `${v.slice(0, 40)}…` : v}"]`
          break
        }
      }
      const parent: Element | null = e.parentElement
      if (parent) {
        const same = Array.from(parent.children).filter((c) => c.tagName === e!.tagName)
        if (same.length > 1) p += `:nth-of-type(${same.indexOf(e) + 1})`
      }
      parts.unshift(p)
      e = parent
    }
    const text = (el.textContent ?? '').replace(/\s+/g, ' ').trim()
    return parts.join(' > ') + (text ? ` "${text.length > 40 ? `${text.slice(0, 40)}…` : text}"` : '')
  }

  const visible = (el: Element): boolean => {
    const r = el.getBoundingClientRect()
    if (r.width * r.height === 0) return false
    if (getComputedStyle(el).visibility !== 'visible') return false
    return !el.closest('[inert]')
  }

  const enabled = (el: Element): boolean =>
    !el.matches(':disabled') && el.getAttribute('aria-disabled') !== 'true'

  const controls = (): HTMLElement[] =>
    Array.from(root().querySelectorAll<HTMLElement>(CONTROL)).filter((el) => visible(el) && !el.closest('.sr-only'))

  const props = (el: Element, names: string[], pseudo?: string): string => {
    const cs = getComputedStyle(el, pseudo)
    return names.map((n) => `${n}: ${cs.getPropertyValue(n)}`).join('; ')
  }

  const BORDER = ['top', 'right', 'bottom', 'left'].flatMap((side) => [`border-${side}-width`, `border-${side}-style`, `border-${side}-color`])
  const ringOf = (el: Element): Ring => {
    const cs = getComputedStyle(el.closest('[data-focus-ring]') ?? el)
    return {
      outlineStyle: cs.outlineStyle,
      outlineWidth: parseFloat(cs.outlineWidth) || 0,
      outlineColor: cs.outlineColor,
      boxShadow: cs.boxShadow,
      border: BORDER.map((n) => cs.getPropertyValue(n)).join(' '),
    }
  }

  // A range input draws its track and knob as engine pseudo-elements: compare those too.
  const RANGE_PARTS = ['::-webkit-slider-runnable-track', '::-webkit-slider-thumb']
  const lookOf = (el: Element): string[] =>
    [el, ...Array.from(el.querySelectorAll('*'))].flatMap((e) => [
      props(e, LOOK),
      ...(e.matches('input[type="range"]') ? RANGE_PARTS.map((part) => `${part} ${props(e, LOOK, part)}`) : []),
    ])

  window.__checkDom = { CONTROL, OVERLAY, COMPOSITE, root, selectorOf, visible, controls, enabled, ringOf, lookOf }
}
