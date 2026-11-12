// The one typed import of menus.json, the manifest Go builds the native menu
// bar and the folder-row context menu from.
import { acceleratorLabel } from './format'
import manifest from './menus.json'
import type { MenuDef, MenuItemDef } from './types'

export interface MenuManifest {
  menus: MenuDef[]
  context: Record<string, MenuItemDef[]>
}

export const MENU_MANIFEST = manifest as MenuManifest
export const MENUS: MenuDef[] = MENU_MANIFEST.menus
/** The Library row's context menu. */
export const FOLDER_ROW_MENU: MenuItemDef[] = MENU_MANIFEST.context['folder-row'] ?? []

export function menuById(id: string): MenuDef {
  const m = MENUS.find((x) => x.id === id)
  if (!m) throw new Error(`menus.json has no menu "${id}"`)
  return m
}

/** The flags a folder's state sets for the context menu's `hiddenWhen` (`offline`, `scanning`). */
export function folderFlags(state: string): string[] {
  return state === 'offline' ? ['offline'] : state === 'scanning' ? ['scanning'] : []
}

/** A menu item's shortcut as the menu shows it (`⇧⌘R`, or a single key's `display`), by item id. */
export function menuKey(id: string): string {
  for (const menu of MENUS) {
    const item = menu.items.find((x): x is Extract<MenuItemDef, { id: string }> => 'id' in x && x.id === id)
    if (!item) continue
    const key = item.accelerator ? acceleratorLabel(item.accelerator) : item.display
    if (key) return key
  }
  throw new Error(`menus.json has no shortcut for "${id}"`)
}
