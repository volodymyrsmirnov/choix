// Alignment: elements sharing a data-align-y value whose vertical centres
// differ by more than 0.5 px, or sharing a data-align-x value whose left edges
// differ by more than 0.5 px. measureAlign() runs in the page (visible
// elements only); judgeAlign() applies the tolerance and is unit-tested.
import type { Page } from '@playwright/test'
import type { Raw } from './dom'

export interface AlignGroup {
  axis: 'x' | 'y'
  name: string
  items: { selector: string; value: number }[]
}

export const ALIGN_TOLERANCE_PX = 0.5

export function judgeAlign(groups: AlignGroup[]): Raw[] {
  const out: Raw[] = []
  for (const g of groups) {
    if (g.items.length < 2) continue
    const values = g.items.map((i) => i.value)
    const spread = Math.max(...values) - Math.min(...values)
    if (spread <= ALIGN_TOLERANCE_PX) continue
    const what = g.axis === 'y' ? 'vertical centres' : 'left edges'
    const detail = g.items.map((i) => `${Math.round(i.value * 100) / 100}px ${i.selector}`).join('; ')
    out.push({
      check: 'align',
      message: `data-align-${g.axis}="${g.name}": ${what} differ by ${Math.round(spread * 100) / 100}px (> ${ALIGN_TOLERANCE_PX}px): ${detail}`,
    })
  }
  return out
}

export function measureAlign(): AlignGroup[] {
  const d = window.__checkDom!
  const root = d.root()
  const groups = new Map<string, AlignGroup>()
  for (const axis of ['x', 'y'] as const) {
    for (const el of Array.from(root.querySelectorAll(`[data-align-${axis}]`))) {
      if (!d.visible(el)) continue
      const name = el.getAttribute(`data-align-${axis}`) ?? ''
      const r = el.getBoundingClientRect()
      const key = `${axis}:${name}`
      const g = groups.get(key) ?? { axis, name, items: [] }
      g.items.push({ selector: d.selectorOf(el), value: axis === 'y' ? r.top + r.height / 2 : r.left })
      groups.set(key, g)
    }
  }
  return Array.from(groups.values())
}

export async function runAlign(page: Page): Promise<Raw[]> {
  return judgeAlign(await page.evaluate(measureAlign))
}
