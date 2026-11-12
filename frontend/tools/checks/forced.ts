// Forced-look fidelity (gallery states): for every element rendered with a
// forced look (`data-force="hover" | "focus" | "focus-within" | "active"`),
// the computed look of it and its descendants equals the look of the same
// element with the attribute removed under the real interaction: a mouse
// hover, keyboard focus (focus-within: on its first focusable descendant),
// or a mouse button held down over it (released elsewhere, so nothing is
// clicked).
import type { Page } from '@playwright/test'
import type { Raw } from './dom'

declare global {
  interface Window {
    __forced?: Element[]
  }
}

const FOCUSABLE = 'a[href], button, input, select, textarea, [tabindex]'

function listForced(): number {
  window.__forced = Array.from(window.__checkDom!.root().querySelectorAll('[data-force]'))
  return window.__forced.length
}

/** Diff of two lookOf() snapshots, for the message. */
function lookDiff(forced: string[], real: string[]): string {
  const out: string[] = []
  for (let i = 0; i < Math.max(forced.length, real.length); i++) {
    const f = (forced[i] ?? '').split('; ')
    const r = (real[i] ?? '').split('; ')
    for (let k = 0; k < Math.max(f.length, r.length); k++) {
      if (f[k] !== r[k]) out.push(`${i === 0 ? 'element' : `descendant ${i}`}: forced ${f[k]} / real ${r[k]}`)
    }
  }
  return out.slice(0, 4).join('; ') + (out.length > 4 ? `; and ${out.length - 4} more` : '')
}

export async function forcedLookCheck(page: Page, reopen: () => Promise<void>): Promise<Raw[]> {
  const out: Raw[] = []
  const count = await page.evaluate(listForced)
  for (let i = 0; i < count; i++) {
    if (!(await page.evaluate((i) => window.__forced![i]!.isConnected, i))) {
      await reopen()
      if ((await page.evaluate(listForced)) !== count) {
        out.push({ check: 'forced-look', message: 'the state renders a different set of forced elements after reopening' })
        return out
      }
    }
    await page.mouse.move(0, 0)
    const before = await page.evaluate((i) => {
      ;(document.activeElement as HTMLElement | null)?.blur?.()
      const el = window.__forced![i]!
      el.scrollIntoView({ block: 'center', inline: 'center' })
      const force = el.getAttribute('data-force') ?? ''
      const look = window.__checkDom!.lookOf(el)
      el.removeAttribute('data-force')
      const r = el.getBoundingClientRect()
      return { force, look, x: r.left + r.width / 2, y: r.top + r.height / 2, selector: window.__checkDom!.selectorOf(el) }
    }, i)

    const focus = async (within: boolean) => {
      // A key press first, so programmatic focus counts as keyboard focus
      // (:focus-visible) even after a mouse press. Not a modifier: WebKit
      // ignores those. F12 means nothing to the prototype.
      await page.keyboard.press('F12')
      return page.evaluate(
        ({ i, within, sel }) => {
          const el = window.__forced![i]!
          const target = (within ? el.querySelector<HTMLElement>(sel) : el) as HTMLElement | null
          target?.focus()
          return !!target && target.matches(':focus-visible')
        },
        { i, within, sel: FOCUSABLE },
      )
    }
    let ok = true
    switch (before.force) {
      case 'hover':
        await page.mouse.move(before.x, before.y)
        break
      case 'active':
        await page.mouse.move(before.x, before.y)
        await page.mouse.down()
        break
      case 'focus':
        ok = await focus(false)
        break
      case 'focus-within':
        ok = await focus(true)
        break
      default:
        out.push({ check: 'forced-look', message: `unknown data-force value "${before.force}"`, selector: before.selector })
        continue
    }
    const real = await page.evaluate((i) => window.__checkDom!.lookOf(window.__forced![i]!), i)
    if (before.force === 'active') {
      await page.mouse.move(0, 0)
      await page.mouse.up()
    }
    await page.evaluate(
      ({ i, force }) => {
        ;(document.activeElement as HTMLElement | null)?.blur?.()
        window.__forced![i]!.setAttribute('data-force', force)
      },
      { i, force: before.force },
    )
    if (!ok) {
      out.push({ check: 'forced-look', message: `cannot give it real keyboard focus to compare its forced "${before.force}" look`, selector: before.selector })
    } else if (JSON.stringify(before.look) !== JSON.stringify(real)) {
      out.push({
        check: 'forced-look',
        message: `forced "${before.force}" look differs from the real one: ${lookDiff(before.look, real)}`,
        selector: before.selector,
      })
    }
  }
  await page.mouse.move(0, 0)
  return out
}
