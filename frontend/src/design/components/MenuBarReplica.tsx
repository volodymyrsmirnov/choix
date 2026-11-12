import { useOverlay } from '../overlay'
import type { MenuDef, ScreenId } from '../types'
import { FOCUS_RING } from './focusRing'
import { MenuPanel } from './MenuPanel'

function MenuTitle({
  menu,
  first,
  open,
  screen,
  hasSelection,
  onOpen,
  onItem,
  forceTitle,
  forceItem,
}: {
  menu: MenuDef
  first: boolean
  open: boolean
  screen: ScreenId
  hasSelection: boolean
  onOpen?: (id: string | null) => void
  onItem?: (id: string) => void
  forceTitle?: string
  forceItem?: { id: string; force: string }
}) {
  useOverlay(`menu:${menu.id}`, open)
  return (
    <div className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        data-force={forceTitle}
        onClick={() => onOpen?.(open ? null : menu.id)}
        className={`h-[22px] px-2 rounded-sm text-13 cursor-default ${first ? 'font-bold' : 'font-medium'} ${open ? 'bg-bg-4 text-fg-0' : 'text-fg-1 hover:bg-bg-3 hover:text-fg-0'} active:bg-bg-4 active:text-fg-0 ${FOCUS_RING}`}
      >
        {menu.title}
      </button>
      {open && (
        <MenuPanel
          className="absolute left-0 top-full mt-px z-30"
          label={menu.title}
          items={menu.items}
          isEnabled={(it) => it.enabledIn.includes(screen) && (!it.needsSelection || hasSelection)}
          onItem={onItem}
          forceItem={forceItem}
        />
      )}
    </div>
  )
}

/**
 * The macOS menu bar, as an HTML replica built from menus.json (the same file
 * Go builds the native menu from). Items are enabled from the current screen
 * and, for `needsSelection` items, from `hasSelection`. `open` is the open
 * menu's id.
 */
export function MenuBarReplica({
  menus,
  open,
  screen,
  hasSelection,
  onOpen,
  onItem,
  forceTitle,
  forceItem,
}: {
  menus: MenuDef[]
  open: string | null
  screen: ScreenId
  /** There is a target folder: the selected row on the Library, the open
   *  folder elsewhere. Enables `needsSelection` items. */
  hasSelection: boolean
  onOpen?: (id: string | null) => void
  onItem?: (id: string) => void
  /** Prototype only: a menu title's forced look, by menu id. */
  forceTitle?: { id: string; force: string }
  /** Prototype only: an item's forced look in the open menu, by item id. */
  forceItem?: { id: string; force: string }
}) {
  return (
    <nav aria-label="Menu bar" className="relative w-full flex items-center gap-0.5 h-6 px-2 bg-bg-0 border-b border-line-1 select-none">
      {menus.map((m, i) => (
        <MenuTitle
          key={m.id}
          menu={m}
          first={i === 0}
          open={open === m.id}
          screen={screen}
          hasSelection={hasSelection}
          onOpen={onOpen}
          onItem={onItem}
          forceTitle={forceTitle?.id === m.id ? forceTitle.force : undefined}
          forceItem={forceItem}
        />
      ))}
    </nav>
  )
}
