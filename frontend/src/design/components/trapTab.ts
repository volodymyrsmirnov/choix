import type { KeyboardEvent } from 'react'

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * Keeps Tab / Shift+Tab inside a modal (v1 Shortcuts' trap). Only the
 * wrap-around cases are prevented; every other Tab keeps the browser's order.
 */
export function trapTab(e: KeyboardEvent, container: HTMLElement | null): void {
  if (e.key !== 'Tab' || !container) return
  const items = Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE))
  if (items.length === 0) {
    e.preventDefault()
    container.focus()
    return
  }
  const first = items[0]
  const last = items[items.length - 1]
  const active = document.activeElement
  const inside = active instanceof Node && container.contains(active) && active !== container
  if (e.shiftKey && (!inside || active === first)) {
    e.preventDefault()
    last?.focus()
  } else if (!e.shiftKey && (!inside || active === last)) {
    e.preventDefault()
    first?.focus()
  }
}
