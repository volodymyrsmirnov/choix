import type { KeyboardEvent } from 'react'
import { acceleratorLabel } from '../format'
import { ROLE_ITEMS } from '../menuRoles'
import type { MenuItemDef } from '../types'

type Item = Extract<MenuItemDef, { id: string }>

/**
 * Drops items hidden by any of `flags`, then the separators that would be
 * left leading, trailing or doubled.
 */
export function visibleEntries(items: MenuItemDef[], flags: string[]): MenuItemDef[] {
  const kept = items.filter((e) => !('id' in e) || !(e.hiddenWhen ?? []).some((f) => flags.includes(f)))
  const out: MenuItemDef[] = []
  for (const e of kept) {
    const sep = 'separator' in e
    if (sep && (out.length === 0 || 'separator' in (out[out.length - 1] as MenuItemDef))) continue
    out.push(e)
  }
  while (out.length > 0 && 'separator' in (out[out.length - 1] as MenuItemDef)) out.pop()
  return out
}

/**
 * A native menu's panel, as an HTML replica: items with their shortcut
 * glyphs, separators, disabled items greyed. ↑/↓/Home/End move between the
 * enabled items; the first enabled item is the Tab stop. `forceItem` shows one
 * item (by id, or role name) in a forced look, e.g. highlighted.
 */
export function MenuPanel({
  label,
  items,
  isEnabled,
  onItem,
  forceItem,
  className,
}: {
  label: string
  items: MenuItemDef[]
  isEnabled: (item: Item) => boolean
  onItem?: (id: string) => void
  /** Prototype only: one item's `hover` / `focus` / `active` look, as data-force. */
  forceItem?: { id: string; force: string }
  className?: string
}) {
  const rows = items.map((e, i) => {
    if ('separator' in e) return { key: `sep-${i}`, separator: true as const }
    if ('role' in e) {
      const r = ROLE_ITEMS[e.role] ?? {}
      return { key: `role-${e.role}`, id: e.role, label: e.label, shortcut: r.display, enabled: r.enabled ?? true }
    }
    return {
      key: e.id,
      id: e.id,
      label: e.label,
      shortcut: e.accelerator ? acceleratorLabel(e.accelerator) : e.display,
      enabled: isEnabled(e),
    }
  })
  const firstEnabled = rows.findIndex((r) => !('separator' in r) && r.enabled)

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const list = Array.from(e.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)'))
    const n = list.length
    if (n === 0) return
    const i = list.indexOf(document.activeElement as HTMLButtonElement)
    let next: HTMLButtonElement | undefined
    if (e.key === 'ArrowDown') next = list[(i + 1 + n) % n]
    else if (e.key === 'ArrowUp') next = list[i <= 0 ? n - 1 : i - 1]
    else if (e.key === 'Home') next = list[0]
    else if (e.key === 'End') next = list[n - 1]
    else return
    e.preventDefault()
    e.stopPropagation()
    next?.focus()
  }

  return (
    <div
      role="menu"
      aria-label={label}
      onKeyDown={onKeyDown}
      className={`min-w-[220px] rounded-md border border-line-2 bg-bg-2 shadow-pop p-1 text-13 ${className ?? ''}`}
    >
      {rows.map((r, i) =>
        'separator' in r ? (
          <div key={r.key} role="separator" className="h-px bg-line-2 my-1 mx-2" />
        ) : (
          <button
            key={r.key}
            type="button"
            role="menuitem"
            disabled={!r.enabled}
            tabIndex={i === firstEnabled ? 0 : -1}
            data-force={forceItem?.id === r.id ? forceItem.force : undefined}
            onClick={() => onItem?.(r.id)}
            className="w-full flex items-center gap-6 h-[22px] px-2 rounded-sm text-left text-fg-0 cursor-default outline-none enabled:hover:bg-sel enabled:hover:text-bg-0 focus:bg-sel focus:text-bg-0 focus-visible:shadow-menu-focus enabled:active:bg-sel enabled:active:text-bg-0 disabled:text-fg-3"
          >
            <span className="flex-1 whitespace-nowrap">{r.label}</span>
            {r.shortcut && (
              <span className="font-sans tabular-nums opacity-60 whitespace-nowrap" aria-hidden="true">
                {r.shortcut}
              </span>
            )}
          </button>
        ),
      )}
    </div>
  )
}
