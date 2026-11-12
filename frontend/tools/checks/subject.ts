// State subject: what a state's label names must be on screen without
// scrolling. Its elements are those marked data-state-subject in the app
// root, plus those matching the scenario's `subject` selector. Each must lie
// fully inside the app root's box and inside the box of every ancestor that
// clips its content (overflow other than visible), so an element scrolled
// out of a pane or below the window's fold fails. A `subject` selector that
// matches nothing fails too, and so does a subject that is not seen: its
// visibility is not `visible`, or it or an ancestor has opacity 0. measureSubject() runs in the page;
// judgeSubject() applies the tolerance and is unit-tested.
import type { Page } from '@playwright/test'
import type { Raw } from './dom'

export interface SubjectItem {
  selector: string
  /** How far the element sticks out of its visible box on each side, px (≤ 0 is inside). */
  out: { top: number; right: number; bottom: number; left: number }
  /** True when the element has no rendered box. */
  hidden: boolean
  /** True when it has a box but is not seen (visibility, or opacity 0 on it or an ancestor). */
  unseen?: boolean
}

export interface SubjectMeasure {
  items: SubjectItem[]
  /** The scenario's selector, when it matched nothing. */
  missing: string | null
}

export const SUBJECT_TOLERANCE_PX = 0.5

export function judgeSubject(m: SubjectMeasure): Raw[] {
  const out: Raw[] = []
  if (m.missing !== null) out.push({ check: 'subject', message: `the state's subject "${m.missing}" is not on the page` })
  for (const it of m.items) {
    if (it.hidden) {
      out.push({ check: 'subject', message: 'the state subject is not rendered', selector: it.selector })
      continue
    }
    if (it.unseen) {
      out.push({ check: 'subject', message: 'the state subject is not visible (opacity 0 or visibility hidden)', selector: it.selector })
      continue
    }
    const sides = (['top', 'right', 'bottom', 'left'] as const).filter((s) => it.out[s] > SUBJECT_TOLERANCE_PX)
    if (sides.length === 0) continue
    const detail = sides.map((s) => `${Math.round(it.out[s] * 10) / 10}px past the ${s}`).join(', ')
    out.push({ check: 'subject', message: `the state subject is not fully in view without scrolling (${detail})`, selector: it.selector })
  }
  return out
}

export function measureSubject(selector: string | null): SubjectMeasure {
  const d = window.__checkDom!
  const root = d.root()
  const els = new Set<Element>(Array.from(root.querySelectorAll('[data-state-subject]')))
  let missing: string | null = null
  if (selector) {
    const found = Array.from(root.querySelectorAll(selector))
    if (found.length === 0) missing = selector
    for (const el of found) els.add(el)
  }
  const items: SubjectItem[] = []
  for (const el of els) {
    const r = el.getBoundingClientRect()
    if (r.width === 0 && r.height === 0) {
      items.push({ selector: d.selectorOf(el), hidden: true, out: { top: 0, right: 0, bottom: 0, left: 0 } })
      continue
    }
    let unseen = getComputedStyle(el).visibility !== 'visible'
    for (let a: Element | null = el; a && !unseen; a = a === root ? null : a.parentElement) {
      if (parseFloat(getComputedStyle(a).opacity) === 0) unseen = true
    }
    const rb = root.getBoundingClientRect()
    let box = { top: rb.top, right: rb.right, bottom: rb.bottom, left: rb.left }
    for (let a = el.parentElement; a && a !== root; a = a.parentElement) {
      const cs = getComputedStyle(a)
      if (cs.overflowX === 'visible' && cs.overflowY === 'visible') continue
      const ar = a.getBoundingClientRect()
      box = {
        top: Math.max(box.top, ar.top),
        right: Math.min(box.right, ar.right),
        bottom: Math.min(box.bottom, ar.bottom),
        left: Math.max(box.left, ar.left),
      }
    }
    items.push({
      selector: d.selectorOf(el),
      hidden: false,
      unseen,
      out: { top: box.top - r.top, right: r.right - box.right, bottom: r.bottom - box.bottom, left: box.left - r.left },
    })
  }
  return { items, missing }
}

export async function runSubject(page: Page, selector: string | null): Promise<Raw[]> {
  return judgeSubject(await page.evaluate(measureSubject, selector))
}
