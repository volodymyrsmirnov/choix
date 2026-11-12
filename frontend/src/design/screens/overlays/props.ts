import type { ScreenId } from '../../types'

/** The shortcut sheet's overlay id; also the store key that opens it. */
export const SHORTCUTS_OVERLAY = 'shortcuts'

/**
 * The keyboard-shortcut sheet (v1 Shortcuts.tsx, spec §4.3 and §4.4). The
 * app mounts it over whichever screen is showing; that screen's section comes
 * first and is marked "this screen". Rendered only while open. Toasts are
 * not a screen of their own: they are the `toasts` prop of the screen they
 * sit over (the overlay states show them over the Library).
 */
export interface ShortcutsSheetProps {
  screen: ScreenId
  /** Escape, the Close button, or a click on the backdrop. */
  onClose(): void
}
