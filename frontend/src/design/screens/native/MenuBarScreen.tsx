import { MenuBarReplica, NativeBackdrop } from '../../components'
import { MENUS } from '../../menus'
import type { MenuBarScreenProps } from './props'

/**
 * The macOS menu bar with one menu open (replica, from menus.json) over the
 * app window, dimmed. For the owner to approve what each native menu holds
 * and when its items are enabled.
 */
export function MenuBarScreen(p: MenuBarScreenProps) {
  return (
    <div className="relative flex flex-col h-full min-h-0">
      <MenuBarReplica
        menus={MENUS}
        open={p.open}
        screen={p.screen}
        hasSelection={p.hasSelection}
        onOpen={p.onOpen}
        onItem={p.onItem}
      />
      <NativeBackdrop>{p.backdrop}</NativeBackdrop>
    </div>
  )
}
