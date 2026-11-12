// Orphans (left-to-right text): a text block of ≥ 2 lines whose last line is
// a single word and narrower than 30 % of the widest line. Opt-out:
// data-wrap="any" on the block or an ancestor.
//
// measureTextBlocks() runs in the page: a block is the nearest non-inline
// ancestor of a text node; its words are runs of non-whitespace, where a
// no-break space separates words too (noOrphan binds the last two words with
// one, so they wrap together and the last line holds two words); each word's
// client rects are grouped into lines by vertical overlap. judgeOrphans()
// applies the threshold and is unit-tested.
import type { Page } from '@playwright/test'
import type { Raw } from './dom'

export interface TextBlock {
  selector: string
  /** Lines top to bottom: horizontal extent and the number of word fragments. */
  lines: { width: number; words: number }[]
}

export const ORPHAN_RATIO = 0.3

export function judgeOrphans(blocks: TextBlock[]): Raw[] {
  const out: Raw[] = []
  for (const b of blocks) {
    if (b.lines.length < 2) continue
    const last = b.lines[b.lines.length - 1]!
    const widest = Math.max(...b.lines.map((l) => l.width))
    if (last.words === 1 && last.width < ORPHAN_RATIO * widest) {
      out.push({
        check: 'orphan',
        message: `last of ${b.lines.length} lines is one word, ${Math.round(last.width)}px of the widest ${Math.round(widest)}px (< ${ORPHAN_RATIO * 100} %)`,
        selector: b.selector,
      })
    }
  }
  return out
}

export function measureTextBlocks(): TextBlock[] {
  const d = window.__checkDom!
  const root = d.root()
  const blocks = new Map<Element, Text[]>()
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const t = n as Text
    if (t.data.trim() === '') continue
    let b: Element | null = t.parentElement
    while (b && b !== root && ['inline', 'contents'].includes(getComputedStyle(b).display)) b = b.parentElement
    if (!b) continue
    const list = blocks.get(b)
    if (list) list.push(t)
    else blocks.set(b, [t])
  }

  const out: TextBlock[] = []
  for (const [block, texts] of blocks) {
    if (block.closest('[data-wrap="any"]') || block.closest('.sr-only') || !d.visible(block)) continue
    if (getComputedStyle(block).direction === 'rtl') continue
    const frags: { top: number; bottom: number; left: number; right: number }[] = []
    for (const t of texts) {
      const parent = t.parentElement
      if (!parent || getComputedStyle(parent).direction === 'rtl' || !d.visible(parent)) continue
      const words = /\S+/g
      for (let m = words.exec(t.data); m; m = words.exec(t.data)) {
        const range = document.createRange()
        range.setStart(t, m.index)
        range.setEnd(t, m.index + m[0].length)
        for (const r of Array.from(range.getClientRects())) {
          if (r.width > 0) frags.push({ top: r.top, bottom: r.bottom, left: r.left, right: r.right })
        }
      }
    }
    frags.sort((a, b) => a.top - b.top || a.left - b.left)
    const lines: { top: number; bottom: number; left: number; right: number; words: number }[] = []
    for (const f of frags) {
      const mid = (f.top + f.bottom) / 2
      const line = lines.find((l) => mid >= l.top && mid <= l.bottom)
      if (line) {
        line.left = Math.min(line.left, f.left)
        line.right = Math.max(line.right, f.right)
        line.words += 1
      } else {
        lines.push({ ...f, words: 1 })
      }
    }
    if (lines.length < 2) continue
    lines.sort((a, b) => a.top - b.top)
    out.push({ selector: d.selectorOf(block), lines: lines.map((l) => ({ width: l.right - l.left, words: l.words })) })
  }
  return out
}

export async function runOrphans(page: Page): Promise<Raw[]> {
  return judgeOrphans(await page.evaluate(measureTextBlocks))
}
