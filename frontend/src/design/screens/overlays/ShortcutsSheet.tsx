import { Sheet, ShortcutSection } from '../../components'
import { SHORTCUTS_OVERLAY, type ShortcutsSheetProps } from './props'
import { sectionOf, shortcutColumns, shortcutGroups } from './shortcuts'

/**
 * The keyboard-shortcut sheet (v1 Shortcuts.tsx on the shared Sheet): two
 * columns of sections, the current screen's first, then "Everywhere". The
 * open/closed state and the "?" key belong to the app; this renders the open
 * sheet.
 */
export function ShortcutsSheet({ screen, onClose }: ShortcutsSheetProps) {
  const groups = shortcutGroups()
  const current = sectionOf(screen)
  return (
    <Sheet id={SHORTCUTS_OVERLAY} title="Keyboard shortcuts" onClose={onClose} testId="shortcuts-overlay">
      <div className="grid grid-cols-2 gap-x-6 gap-y-4 p-4">
        {shortcutColumns(screen).map((column) => (
          <div key={column.join('-')}>
            {column.map((id, i) => (
              <ShortcutSection key={id} group={groups[id]} current={id === current} nested={i > 0} />
            ))}
          </div>
        ))}
      </div>
      <div className="px-4 pb-4">
        <ShortcutSection group={groups.global} current={false} />
      </div>
    </Sheet>
  )
}
