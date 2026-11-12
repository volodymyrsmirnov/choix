// Centring: an element marked data-centered="x", "y" or "both" must have its
// centre within 0.5 px of its box's content-box centre on that axis.
//
// Its box is the closest ancestor marked data-center-box, else its
// offsetParent (the nearest positioned ancestor, which is what an absolutely
// placed child is positioned against). The content box is the box's border
// box less its borders and padding. Measured with getBoundingClientRect, so a
// translate (e.g. -translate-y-1/2) counts. measureCentred() runs in the
// page; judgeCentred() applies the tolerance and is unit-tested.
import type { Page } from '@playwright/test'
import type { Raw } from './dom'

export interface CentreItem {
  selector: string
  axes: ('x' | 'y')[]
  /** Element centre minus content-box centre, px. */
  dx: number
  dy: number
}

export const CENTRE_TOLERANCE_PX = 0.5

export function judgeCentred(items: CentreItem[]): Raw[] {
  const out: Raw[] = []
  const round = (n: number) => Math.round(n * 100) / 100
  for (const it of items) {
    if (Number.isNaN(it.dx)) {
      out.push({ check: 'centred', message: 'has no box to be centred in (no data-center-box ancestor and no offsetParent)', selector: it.selector })
      continue
    }
    const off = it.axes.filter((a) => Math.abs(a === 'x' ? it.dx : it.dy) > CENTRE_TOLERANCE_PX)
    if (off.length === 0) continue
    const detail = off.map((a) => `${a === 'x' ? 'horizontally' : 'vertically'} by ${round(a === 'x' ? it.dx : it.dy)}px`).join(' and ')
    out.push({ check: 'centred', message: `off-centre in its box ${detail} (> ${CENTRE_TOLERANCE_PX}px)`, selector: it.selector })
  }
  return out
}

export function measureCentred(): CentreItem[] {
  const d = window.__checkDom!
  const out: CentreItem[] = []
  for (const el of Array.from(d.root().querySelectorAll<HTMLElement>('[data-centered]'))) {
    if (!d.visible(el)) continue
    const want = el.getAttribute('data-centered')
    const axes: ('x' | 'y')[] = want === 'x' ? ['x'] : want === 'y' ? ['y'] : ['x', 'y']
    const box = el.parentElement?.closest('[data-center-box]') ?? el.offsetParent
    if (!box) {
      out.push({ selector: d.selectorOf(el), axes, dx: Number.NaN, dy: Number.NaN })
      continue
    }
    const b = box.getBoundingClientRect()
    const cs = getComputedStyle(box)
    const px = (n: string) => parseFloat(cs.getPropertyValue(n)) || 0
    const left = b.left + px('border-left-width') + px('padding-left')
    const right = b.right - px('border-right-width') - px('padding-right')
    const top = b.top + px('border-top-width') + px('padding-top')
    const bottom = b.bottom - px('border-bottom-width') - px('padding-bottom')
    const r = el.getBoundingClientRect()
    out.push({
      selector: d.selectorOf(el),
      axes,
      dx: r.left + r.width / 2 - (left + right) / 2,
      dy: r.top + r.height / 2 - (top + bottom) / 2,
    })
  }
  return out
}

export async function runCentred(page: Page): Promise<Raw[]> {
  return judgeCentred(await page.evaluate(measureCentred))
}
