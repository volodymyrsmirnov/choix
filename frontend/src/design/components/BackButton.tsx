import { menuKey } from '../menus'
import { Button } from './Button'

/**
 * The title bar's back control, the same on every screen (approved mockup:
 * "‹ Library"): a ghost button with the back chevron, labelled with where it
 * goes — "Library" from a folder or from Settings opened on the Library, the
 * folder's name from Focus. Its shortcut (View ▸ Back, ⌘[) is in the
 * tooltip and aria-keyshortcuts, not drawn as a key cap.
 */
export function BackButton({ to, onClick, testId }: { to: string; onClick: () => void; testId?: string }) {
  return (
    <Button
      variant="ghost"
      icon="back"
      onClick={onClick}
      aria-label={`Back to ${to}`}
      title={`Back to ${to} (${menuKey('back')})`}
      aria-keyshortcuts="Meta+BracketLeft"
      data-testid={testId}
      className="max-w-[200px]"
    >
      <span className="truncate">{to}</span>
    </Button>
  )
}
