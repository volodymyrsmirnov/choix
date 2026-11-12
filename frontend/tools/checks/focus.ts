// Keyboard focus.
//
// focusCheck() (Chromium only: WebKit does not Tab to buttons by default):
//   - every enabled control is tabbable, or sits in a role menu, listbox,
//     toolbar, tablist, radiogroup or grid container that is tabbable (one of
//     its controls is a Tab stop) and reaches it with arrow keys from there;
//   - focus makes a visible difference (judgeRing): an outline that is drawn
//     (style not none, width > 0, colour not transparent) and was not there,
//     or another box-shadow that is not none, or a changed border; on the
//     control, or its nearest ancestor marked data-focus-ring (TextInput draws
//     its ring on the wrapper). The platform ring (outline-style: auto) is a
//     finding of its own: the design draws explicit rings.
//   While a modal traps Tab (every Tab stop lies inside one aria-modal
//   element), only the controls inside it count.
// pointerOnlyCheck() (both engines): an element marked data-pointer-only="<reason>"
//   is a pointer affordance whose job the keyboard does another way (the
//   Folder strip's +N pill: every thumbnail is a Tab stop). focusCheck does not
//   require it to be reachable; in exchange it must give a reason, must not be
//   focusable (tabIndex < 0) and must be hidden from assistive technology
//   (aria-hidden="true"). Any of the three missing is a finding.
// escapeCheck() (both engines; it needs no Tab): in a state with overlays,
//   one Escape closes exactly one open overlay, and one that no other overlay
//   covers (judged by stacking: elementFromPoint at the centre of each open
//   overlay root before the key, not by the order overlays were opened).
//
// Both mutate the state; the runner reopens it between phases.
import type { Page } from '@playwright/test'
import type { Raw, Ring } from './dom'

interface Seen {
  i: number
  /** The look while focused. */
  ring: Ring
}

interface FocusState {
  list: HTMLElement[]
  rest: Ring[]
  seen: Seen[]
  quiet: boolean
  off(): void
}

interface ControlInfo {
  selector: string
  enabled: boolean
  /** Index of the closest composite container (per enumeration), or -1. */
  container: number
  role: string
  /** Index of the closest aria-modal element, or -1. */
  modal: number
}

declare global {
  interface Window {
    __focusCheck?: FocusState
  }
}

const twoFrames = (page: Page) =>
  page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))

/**
 * Blurs, lists the controls with their unfocused look, and starts recording
 * focus. Forced looks (data-force, the gallery's static hover/focus/press
 * renderings) are removed first: they are not the control's rest look. The
 * runner reopens the state afterwards, which restores them.
 */
function enumerate(): ControlInfo[] {
  const d = window.__checkDom!
  window.__focusCheck?.off()
  for (const el of Array.from(d.root().querySelectorAll('[data-force]'))) el.removeAttribute('data-force')
  ;(document.activeElement as HTMLElement | null)?.blur?.()
  const list = d.controls().filter((el) => !el.closest('[data-pointer-only]'))
  const containers: Element[] = []
  const modals: Element[] = []
  const indexIn = (arr: Element[], el: Element | null) => {
    if (!el || !d.root().contains(el)) return -1
    if (!arr.includes(el)) arr.push(el)
    return arr.indexOf(el)
  }
  const state: FocusState = {
    list,
    rest: list.map((el) => d.ringOf(el)),
    seen: [],
    quiet: false,
    off: () => document.removeEventListener('focusin', onFocus, true),
  }
  function onFocus(e: FocusEvent) {
    if (state.quiet) return
    const i = list.indexOf(e.target as HTMLElement)
    if (i >= 0) state.seen.push({ i, ring: d.ringOf(list[i]!) })
  }
  document.addEventListener('focusin', onFocus, true)
  window.__focusCheck = state
  return list.map((el) => {
    const c = el.closest(d.COMPOSITE)
    return {
      selector: d.selectorOf(el),
      enabled: d.enabled(el),
      container: indexIn(containers, c),
      role: c?.getAttribute('role') ?? '',
      modal: indexIn(modals, el.closest('[aria-modal="true"]')),
    }
  })
}

const seenSoFar = (page: Page) => page.evaluate(() => window.__focusCheck!.seen)
const restLooks = (page: Page) => page.evaluate(() => window.__focusCheck!.rest)

const transparent = (color: string) =>
  color === 'transparent' || /^rgba\(.*,\s*0\)$/.test(color) || /\/\s*0(\.0+)?\s*\)$/.test(color)

const drawn = (r: Ring) => r.outlineStyle !== 'none' && r.outlineWidth > 0 && !transparent(r.outlineColor)

/** Why a focused look is not a visible focus indicator, or null when it is one. */
export function judgeRing(rest: Ring, focused: Ring): string | null {
  if (focused.outlineStyle === 'auto') return 'platform focus ring (outline-style: auto): the design draws explicit rings'
  const outline =
    drawn(focused) &&
    (!drawn(rest) ||
      rest.outlineStyle !== focused.outlineStyle ||
      rest.outlineWidth !== focused.outlineWidth ||
      rest.outlineColor !== focused.outlineColor)
  const shadow = focused.boxShadow !== 'none' && focused.boxShadow !== rest.boxShadow
  const border = focused.border !== rest.border
  if (outline || shadow || border) return null
  return 'no visible focus indicator: focus draws no outline, adds no box-shadow and changes no border'
}

export async function focusCheck(page: Page, reopen: () => Promise<void>): Promise<Raw[]> {
  const out: Raw[] = []
  await page.mouse.move(0, 0)
  const controls = await page.evaluate(enumerate)

  // Phase A: Tab through the page once (and a little more, to wrap).
  for (let k = 0; k < controls.length + 3; k++) await page.keyboard.press('Tab')
  const seen = await seenSoFar(page)
  let rest = await restLooks(page)
  const ringChecked = new Set<number>()
  const ringFail = (s: Seen) => {
    if (ringChecked.has(s.i)) return
    ringChecked.add(s.i)
    const why = judgeRing(rest[s.i]!, s.ring)
    if (why) out.push({ check: 'focus', message: why, selector: controls[s.i]!.selector })
  }
  seen.forEach(ringFail)
  const tabbed = new Set(seen.map((s) => s.i))

  // A modal that traps Tab: only its controls count.
  const modalsSeen = new Set(seen.map((s) => controls[s.i]!.modal))
  const trapped = modalsSeen.size === 1 && !modalsSeen.has(-1) ? [...modalsSeen][0]! : null
  const counts = (i: number) => controls[i]!.enabled && (trapped === null || controls[i]!.modal === trapped)

  // Phase B: controls Tab missed, by composite container.
  const missed = controls.map((_, i) => i).filter((i) => counts(i) && !tabbed.has(i))
  const byContainer = new Map<number, number[]>()
  for (const i of missed) {
    const c = controls[i]!.container
    if (c < 0) {
      out.push({ check: 'focus', message: 'not reachable with Tab, and not in a composite (menu, listbox, toolbar, tablist, radiogroup, grid)', selector: controls[i]!.selector })
      continue
    }
    byContainer.set(c, [...(byContainer.get(c) ?? []), i])
  }
  for (const [c, items] of byContainer) {
    const role = controls[items[0]!]!.role
    const stop = controls.findIndex((ctl, i) => ctl.container === c && tabbed.has(i))
    if (stop < 0) {
      for (const i of items) {
        out.push({ check: 'focus', message: `not reachable: its role=${role} container has no Tab stop`, selector: controls[i]!.selector })
      }
      continue
    }
    await reopen()
    const again = await page.evaluate(enumerate)
    rest = await restLooks(page)
    if (again.length !== controls.length) {
      out.push({ check: 'focus', message: `the state renders ${again.length} controls after reopening, ${controls.length} before: cannot follow arrow keys` })
      continue
    }
    const size = controls.filter((ctl) => ctl.container === c).length
    for (const key of ['ArrowDown', 'ArrowRight', 'ArrowUp', 'ArrowLeft']) {
      await page.evaluate((i) => {
        const s = window.__focusCheck!
        s.quiet = true
        s.list[i]!.focus()
        s.quiet = false
      }, stop)
      for (let k = 0; k < size; k++) await page.keyboard.press(key)
    }
    const arrowed = await seenSoFar(page)
    arrowed.forEach(ringFail)
    const reached = new Set(arrowed.map((s) => s.i))
    for (const i of items) {
      if (!reached.has(i)) {
        out.push({
          check: 'focus',
          message: `not reachable: neither Tab nor arrow keys from its role=${role} container's Tab stop reach it`,
          selector: controls[i]!.selector,
        })
      }
    }
  }
  await page.evaluate(() => window.__focusCheck?.off())
  return out
}

interface OverlayRoots {
  /** Open overlay roots in document order, with whether anything covers each at its centre. */
  roots: { selector: string; covered: boolean }[]
}

declare global {
  interface Window {
    __escapeRoots?: Element[]
    /** For each root, the index of the nearest root enclosing it, or -1. */
    __escapeInside?: number[]
  }
}

/**
 * Lists the visible overlay roots and whether each is covered at its centre
 * (scrolled into view first; everything hit-testable while measuring), and
 * remembers which roots enclose which.
 */
function overlayRoots(): OverlayRoots {
  const d = window.__checkDom!
  const roots = Array.from(d.root().querySelectorAll(d.OVERLAY)).filter((el) => d.visible(el))
  window.__escapeRoots = roots
  window.__escapeInside = roots.map((el) => roots.findIndex((o) => o !== el && o.contains(el)))
  const hitAll = document.createElement('style')
  hitAll.textContent = '* { pointer-events: auto !important; }'
  document.head.appendChild(hitAll)
  try {
    return {
      roots: roots.map((el) => {
        el.scrollIntoView({ block: 'center', inline: 'center' })
        const r = el.getBoundingClientRect()
        const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2)
        return { selector: d.selectorOf(el), covered: !hit || !el.contains(hit) }
      }),
    }
  } finally {
    hitAll.remove()
  }
}

/** Indices of the roots gone (or hidden) since overlayRoots(), outermost only (by the enclosure seen before). */
function goneRoots(): number[] {
  const d = window.__checkDom!
  const roots = window.__escapeRoots ?? []
  const inside = window.__escapeInside ?? []
  const gone = roots.map((el, i) => (!el.isConnected || !d.visible(el) ? i : -1)).filter((i) => i >= 0)
  const enclosedByGone = (i: number): boolean => {
    for (let o = inside[i] ?? -1; o >= 0; o = inside[o] ?? -1) if (gone.includes(o)) return true
    return false
  }
  return gone.filter((i) => !enclosedByGone(i))
}

export async function escapeCheck(page: Page): Promise<Raw[]> {
  const openIds = await page.evaluate(() => {
    const o = window.__choixProto!.overlays
    return o.opened.filter((id) => !o.closed.includes(id))
  })
  const before = await page.evaluate(overlayRoots)
  if (before.roots.length === 0) return []
  const names = openIds.map((id) => `"${id}"`).join(', ')
  await page.keyboard.press('Escape')
  await twoFrames(page)
  const gone = await page.evaluate(goneRoots)
  if (gone.length === 0) return [{ check: 'focus', message: `Escape closed no overlay (open: ${names || 'none registered'})` }]
  if (gone.length > 1) {
    return [{ check: 'focus', message: `one Escape closed ${gone.length} overlays at once: ${gone.map((i) => before.roots[i]!.selector).join('; ')}` }]
  }
  const closed = before.roots[gone[0]!]!
  if (closed.covered) {
    return [{ check: 'focus', message: `Escape closed an overlay another overlay covers, not the top one: ${closed.selector}` }]
  }
  return []
}

export interface PointerOnlyItem {
  selector: string
  reason: string
  tabIndex: number
  ariaHidden: string | null
}

/** Findings for data-pointer-only elements (see the header). */
export function judgePointerOnly(items: PointerOnlyItem[]): Raw[] {
  const out: Raw[] = []
  for (const it of items) {
    if (it.reason.trim() === '') out.push({ check: 'focus', message: 'data-pointer-only needs a reason (what the keyboard uses instead)', selector: it.selector })
    if (it.tabIndex >= 0) out.push({ check: 'focus', message: `a pointer-only control must not be focusable (tabIndex ${it.tabIndex}; use -1)`, selector: it.selector })
    if (it.ariaHidden !== 'true') out.push({ check: 'focus', message: 'a pointer-only control must be hidden from assistive technology (aria-hidden="true")', selector: it.selector })
  }
  return out
}

function measurePointerOnly(): PointerOnlyItem[] {
  const d = window.__checkDom!
  return Array.from(d.root().querySelectorAll<HTMLElement>('[data-pointer-only]'))
    .filter((el) => d.visible(el))
    .map((el) => ({
      selector: d.selectorOf(el),
      reason: el.getAttribute('data-pointer-only') ?? '',
      tabIndex: el.tabIndex,
      ariaHidden: el.getAttribute('aria-hidden'),
    }))
}

export async function pointerOnlyCheck(page: Page): Promise<Raw[]> {
  return judgePointerOnly(await page.evaluate(measurePointerOnly))
}
