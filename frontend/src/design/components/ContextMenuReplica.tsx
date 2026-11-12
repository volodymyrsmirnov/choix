import { useOverlay } from '../overlay'
import type { MenuItemDef } from '../types'
import { MenuPanel, visibleEntries } from './MenuPanel'

/**
 * A native context menu, as an HTML replica built from menus.json. Items whose
 * `hiddenWhen` names one of `flags` (e.g. `offline`, `scanning`) are left out.
 * The caller positions it; it is rendered only while open.
 */
export function ContextMenuReplica({
  id,
  items,
  flags,
  label = 'Context menu',
  onItem,
  forceItem,
}: {
  id: string
  items: MenuItemDef[]
  flags: string[]
  label?: string
  onItem?: (id: string) => void
  /** Prototype only: an item's forced look, by item id. */
  forceItem?: { id: string; force: string }
}) {
  useOverlay(id, true)
  return (
    <MenuPanel
      label={label}
      items={visibleEntries(items, flags)}
      isEnabled={() => true}
      onItem={onItem}
      forceItem={forceItem}
    />
  )
}
