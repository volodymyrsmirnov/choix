// Root overflow and clipping, measured inside the page.
//
// root-overflow: [data-app-root] (or the document) scrolls horizontally.
// clip: an element with overflow hidden or clip on an axis whose scroll size
//   exceeds its client size by more than 1 px on that axis, unless it
//   truncates by design on that axis (text-overflow: ellipsis on x,
//   -webkit-line-clamp on y) or carries data-scroll or data-clip="intended".
//   .sr-only, zero-area elements and replaced elements (img, video, canvas,
//   iframe: they crop by definition) are skipped. Form fields are not: a
//   field whose value is clipped is flagged unless the field itself carries
//   data-scroll or data-clip="intended" (TextInput marks its <input>). The
//   root's x axis is root-overflow's, not clip's.
import type { Page } from '@playwright/test'
import type { Raw } from './dom'

export function rootOverflowCheck(): Raw[] {
  const d = window.__checkDom!
  const root = d.root()
  const out: Raw[] = []
  if (root.scrollWidth - root.clientWidth > 1) {
    out.push({
      check: 'root-overflow',
      message: `[data-app-root] scrolls horizontally: content is ${root.scrollWidth}px wide in ${root.clientWidth}px`,
    })
  }
  const doc = document.scrollingElement
  if (doc && doc.scrollWidth - doc.clientWidth > 1) {
    out.push({
      check: 'root-overflow',
      message: `the document scrolls horizontally: ${doc.scrollWidth}px wide in a ${doc.clientWidth}px viewport`,
    })
  }
  return out
}

export function clipCheck(): Raw[] {
  const d = window.__checkDom!
  const root = d.root()
  const SKIP_TAGS = new Set(['IMG', 'VIDEO', 'CANVAS', 'IFRAME'])
  const clips = (v: string) => v === 'hidden' || v === 'clip'
  const out: Raw[] = []
  for (const el of [root, ...Array.from(root.querySelectorAll('*'))]) {
    if (!(el instanceof HTMLElement) || SKIP_TAGS.has(el.tagName)) continue
    if (el.closest('.sr-only')) continue
    if (el.hasAttribute('data-scroll') || el.getAttribute('data-clip') === 'intended') continue
    const r = el.getBoundingClientRect()
    if (r.width * r.height === 0) continue
    const cs = getComputedStyle(el)
    const dx = el.scrollWidth - el.clientWidth
    const dy = el.scrollHeight - el.clientHeight
    if (el !== root && clips(cs.overflowX) && cs.textOverflow !== 'ellipsis' && dx > 1) {
      out.push({
        check: 'clip',
        message: `clips ${dx}px horizontally (content ${el.scrollWidth}px in ${el.clientWidth}px, overflow-x: ${cs.overflowX})`,
        selector: d.selectorOf(el),
      })
    }
    const clamp = cs.getPropertyValue('-webkit-line-clamp')
    if (clips(cs.overflowY) && (clamp === '' || clamp === 'none') && dy > 1) {
      out.push({
        check: 'clip',
        message: `clips ${dy}px vertically (content ${el.scrollHeight}px in ${el.clientHeight}px, overflow-y: ${cs.overflowY})`,
        selector: d.selectorOf(el),
      })
    }
  }
  return out
}

export async function runClip(page: Page): Promise<Raw[]> {
  return [...(await page.evaluate(rootOverflowCheck)), ...(await page.evaluate(clipCheck))]
}
