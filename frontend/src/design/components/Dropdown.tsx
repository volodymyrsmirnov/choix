import { useEffect, useRef, type KeyboardEvent, type FocusEvent } from 'react'
import { useOverlay } from '../overlay'
import { Icon, type IconName } from './Icon'

export interface DropdownOption<K extends string> {
  value: K
  label: string
}

/**
 * A menu button with radio items (v1 Library's Dropdown), controlled: the
 * caller owns `open` and `value`.
 *
 * WAI-ARIA menu-button pattern, as v1: focus moves onto the checked item when
 * the menu opens; ↑/↓/Home/End move between items; Enter/Space select;
 * Escape closes and returns focus to the trigger; Tab or any other focus move
 * out of the menu closes it, as does a click outside. ↑/↓ on the closed
 * trigger deliberately do not open it (the Folder page uses them to move
 * between clusters).
 */
export function Dropdown<K extends string>({
  id,
  label,
  options,
  value,
  open,
  onOpenChange,
  onSelect,
  icon,
  srPrefix,
  menuLabel,
  align = 'left',
  disabled,
  testId,
  force,
  forceItem,
}: {
  /** Overlay id; also the menu element's id prefix. */
  id: string
  /** Trigger text, normally the current option's label. */
  label: string
  options: DropdownOption<K>[]
  value: K
  open: boolean
  onOpenChange: (open: boolean) => void
  onSelect: (value: K) => void
  icon?: IconName
  /** Screen-reader prefix before the label, e.g. "Camera:". */
  srPrefix?: string
  /** The menu's accessible name; defaults to `label`. */
  menuLabel?: string
  align?: 'left' | 'right'
  disabled?: boolean
  testId?: string
  /** Prototype only: `hover`, `focus`, `active` on the trigger, as data-force. */
  force?: string
  /** Prototype only: one item's forced look, by option value. */
  forceItem?: { id: string; force: string }
}) {
  useOverlay(id, open)
  const wrapRef = useRef<HTMLDivElement | null>(null)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const menuRef = useRef<HTMLDivElement | null>(null)
  // Set by a Tab keydown inside the menu so the focusout that follows closes
  // the menu even when Shift+Tab lands on our own trigger (which a mouse click
  // on the trigger must not do: the click itself toggles it shut).
  const tabbingRef = useRef(false)
  const menuId = `${id}-menu`

  const items = () => Array.from(menuRef.current?.querySelectorAll<HTMLElement>('[role="menuitemradio"]') ?? [])

  // Click outside closes. Listening only while open keeps idle pages free of
  // document-wide mousedown work.
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) onOpenChange(false)
    }
    window.addEventListener('mousedown', onDown)
    return () => window.removeEventListener('mousedown', onDown)
  }, [open, onOpenChange])

  // Move focus onto the checked item (or the first) as soon as the menu opens.
  useEffect(() => {
    if (!open) return
    tabbingRef.current = false
    const list = items()
    ;(list.find((el) => el.getAttribute('aria-checked') === 'true') ?? list[0])?.focus()
  }, [open])

  const choose = (k: K, refocus: boolean) => {
    onSelect(k)
    onOpenChange(false)
    // Keyboard users land back on the trigger; after a mouse pick focus is
    // left alone so Enter / F keep acting on the active cluster.
    if (refocus) triggerRef.current?.focus()
  }

  const onMenuKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const list = items()
    const n = list.length
    if (n === 0) return
    const i = list.indexOf(document.activeElement as HTMLElement)
    switch (e.key) {
      case 'ArrowDown':
        list[(i + 1 + n) % n]?.focus()
        break
      case 'ArrowUp':
        list[i <= 0 ? n - 1 : i - 1]?.focus()
        break
      case 'Home':
        list[0]?.focus()
        break
      case 'End':
        list[n - 1]?.focus()
        break
      case 'Escape':
        onOpenChange(false)
        triggerRef.current?.focus()
        break
      case 'Enter':
      case ' ': {
        const opt = options[i]
        if (opt) choose(opt.value, true)
        break
      }
      case 'Tab':
        // Let the browser move focus; onMenuBlur closes the menu.
        tabbingRef.current = true
        return
      default:
        return
    }
    // The menu owns these keys: preventDefault stops Enter/Space from also
    // clicking the item, stopPropagation keeps page-level ↑/↓/Enter handlers
    // (and the prototype's Escape routing) from acting behind the menu.
    e.preventDefault()
    e.stopPropagation()
  }

  const onMenuBlur = (e: FocusEvent<HTMLDivElement>) => {
    const next = e.relatedTarget as Node | null
    if (tabbingRef.current) {
      tabbingRef.current = false
      if (!next || !menuRef.current?.contains(next)) onOpenChange(false)
      return
    }
    // relatedTarget is null when the window loses focus or (Safari) a click
    // lands on a non-focusable spot; the mousedown listener covers clicks.
    if (next && !wrapRef.current?.contains(next)) onOpenChange(false)
  }

  return (
    <div ref={wrapRef} className="relative">
      <button
        ref={triggerRef}
        type="button"
        className="btn btn-ghost"
        data-force={force}
        disabled={disabled}
        onClick={() => onOpenChange(!open)}
        onKeyDown={(e) => {
          // Shift+Tab from the menu can leave focus on an open trigger.
          if (open && e.key === 'Escape') {
            e.preventDefault()
            e.stopPropagation()
            onOpenChange(false)
          }
        }}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        data-testid={testId}
      >
        {icon && <Icon name={icon} />}
        {srPrefix && <span className="sr-only">{srPrefix} </span>}
        {label}
        <Icon name="chevron-down" />
      </button>
      {open && (
        <div
          ref={menuRef}
          id={menuId}
          role="menu"
          aria-label={menuLabel ?? label}
          onKeyDown={onMenuKeyDown}
          onBlur={onMenuBlur}
          className={`absolute z-10 mt-1 ${align === 'right' ? 'right-0' : 'left-0'} min-w-[200px] rounded-md border border-line-2 bg-bg-2 shadow-pop py-1`}
        >
          {options.map((opt, i) => {
            const checked = opt.value === value
            // Roving tab stop: the checked item (or the first) is the one
            // Tab reaches; arrows move between the rest.
            const stop = checked || (i === 0 && !options.some((o) => o.value === value))
            return (
              <button
                key={opt.value}
                type="button"
                role="menuitemradio"
                aria-checked={checked}
                tabIndex={stop ? 0 : -1}
                data-force={forceItem?.id === opt.value ? forceItem.force : undefined}
                onClick={() => choose(opt.value, false)}
                className={`w-full flex items-center gap-2 text-left whitespace-nowrap px-3 py-1.5 text-12 cursor-pointer outline-none hover:bg-bg-3 focus:bg-bg-4 focus:text-fg-0 focus:shadow-menu-focus active:bg-bg-5 active:text-fg-0 active:shadow-menu-focus ${checked ? 'text-fg-0' : 'text-fg-1'}`}
              >
                {/* The check (not just a lighter text colour) marks the current value. */}
                <span className="w-3 flex-shrink-0" aria-hidden="true">
                  {checked && <Icon name="check" size={12} />}
                </span>
                {opt.label}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
