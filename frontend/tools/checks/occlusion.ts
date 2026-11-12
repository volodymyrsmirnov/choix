// Occlusion, measured inside the page: for every text-bearing or interactive
// element whose centre is inside the viewport and inside every clipping
// ancestor's box, elementFromPoint at that centre must return the element, a
// descendant or an ancestor, unless the covering element belongs to an open
// overlay that the element is not part of (an overlay may cover the page, but
// its own content must not cover itself).
//
// An overlay is recognised by its outermost element: `data-overlay="<id>"`
// or an overlay role (menu, listbox, dialog, alertdialog, tooltip). Overlay
// components without such a role (a backdrop, a toast, the toast stack, the
// drop target) must carry data-overlay. Skipped: .sr-only and elements
// without a box.
//
// pointer-events: none only changes where clicks go, not what is seen, so
// for the duration of the check every element is made hit-testable: a badge
// or label with pointer-events: none is a candidate, and can be a cover.
import type { Page } from '@playwright/test'
import type { Raw } from './dom'

export function occlusionCheck(): Raw[] {
  const d = window.__checkDom!
  const root = d.root()
  const controls = new Set<Element>(d.controls())
  const vw = window.innerWidth
  const vh = window.innerHeight
  const out: Raw[] = []
  const hitAll = document.createElement('style')
  hitAll.textContent = '* { pointer-events: auto !important; }'
  document.head.appendChild(hitAll)
  try {
    for (const el of Array.from(root.querySelectorAll('*'))) check(el)
  } finally {
    hitAll.remove()
  }
  return out

  function check(el: Element): void {
    const text = Array.from(el.childNodes).some((n) => n.nodeType === Node.TEXT_NODE && (n.textContent ?? '').trim() !== '')
    if (!text && !controls.has(el)) return
    if (!d.visible(el) || el.closest('.sr-only')) return
    const r = el.getBoundingClientRect()
    const cx = r.left + r.width / 2
    const cy = r.top + r.height / 2
    if (cx < 0 || cy < 0 || cx >= vw || cy >= vh) return
    let inside = true
    for (let a = el.parentElement; a && inside; a = a.parentElement) {
      const s = getComputedStyle(a)
      if (s.overflowX === 'visible' && s.overflowY === 'visible') continue
      const b = a.getBoundingClientRect()
      inside = cx >= b.left && cx < b.right && cy >= b.top && cy < b.bottom
    }
    if (!inside) return
    const hit = document.elementFromPoint(cx, cy)
    if (!hit || hit === el || el.contains(hit) || hit.contains(el)) return
    const overlay = hit.closest(d.OVERLAY)
    if (overlay && !overlay.contains(el)) return
    out.push({ check: 'occlusion', message: `covered at its centre by ${d.selectorOf(hit)}`, selector: d.selectorOf(el) })
  }
}

export async function runOcclusion(page: Page): Promise<Raw[]> {
  return page.evaluate(occlusionCheck)
}
