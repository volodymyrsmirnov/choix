import type { ReactNode } from 'react'
import type { ScreenId } from '../../types'

/** The overlay id a menu-bar menu registers (MenuBarReplica); also its store key. */
export const menuOverlay = (menuId: string): string => `menu:${menuId}`

/**
 * A replica of the macOS menu bar with one menu open, over the app window,
 * dimmed and inert (states 700–709). Built from menus.json, the file Go
 * builds the native menu from: what is enabled follows `screen` and
 * `hasSelection` exactly as the native menu will.
 */
export interface MenuBarScreenProps {
  /** Id of the open menu in menus.json, or null. */
  open: string | null
  /** The screen the window shows; enables the items whose `enabledIn` names it. */
  screen: ScreenId
  /** There is a target folder (the selected row on the Library, the open
   *  folder elsewhere); enables `needsSelection` items. */
  hasSelection: boolean
  /** The app window behind the menu bar, shown dimmed and inert. */
  backdrop: ReactNode
  onOpen(id: string | null): void
  onItem(id: string): void
}
