import { noOrphan } from '../format'
import { useOverlay } from '../overlay'
import { BrandMark } from './BrandMark'
import { FOCUS_RING } from './focusRing'
import { Prose } from './Prose'

export interface AlertButton {
  label: string
  role: 'default' | 'cancel' | 'destructive'
  onClick?: () => void
  /** Prototype only: `hover`, `focus`, `active` rendered as data-force. */
  force?: string
}

const LOOK: Record<AlertButton['role'], string> = {
  default: 'bg-sel text-bg-0',
  cancel: 'bg-bg-5 text-fg-0',
  destructive: 'bg-bg-5 text-reject',
}

/**
 * A native confirmation alert (NSAlert), as an HTML replica: the app icon, a
 * bold title, a message and the buttons. `buttons` is in NSAlert addButton
 * order (dialogs.json's order): the first is the default. macOS lays two
 * buttons side by side with the default trailing ([Cancel] [Remove]), and
 * stacks three or more with the default on top; the replica draws them the
 * same way, in DOM order too. The caller positions it; it is rendered only
 * while open.
 */
export function AlertReplica({
  id,
  title,
  message,
  buttons,
}: {
  id: string
  title: string
  message: string
  buttons: AlertButton[]
}) {
  useOverlay(id, true)
  return (
    <div
      role="alertdialog"
      aria-modal="true"
      aria-labelledby={`${id}-title`}
      aria-describedby={`${id}-desc`}
      className="w-[260px] rounded-xl border border-line-2 bg-bg-3 shadow-pop p-4 flex flex-col items-center text-center"
    >
      <BrandMark size={64} />
      <h2 id={`${id}-title`} className="m-0 mt-3 text-13 font-bold text-fg-0">
        {noOrphan(title)}
      </h2>
      <Prose id={`${id}-desc`} className="mt-1.5 text-11 text-fg-1 leading-snug">
        {message}
      </Prose>
      {/* One button spans the width; two sit side by side; more stack. */}
      <div
        className={`mt-4 w-full ${buttons.length === 2 ? 'grid grid-cols-2 gap-2' : 'flex flex-col gap-2'}`}
      >
        {(buttons.length === 2 ? [...buttons].reverse() : buttons).map((b) => (
          <button
            key={b.label}
            type="button"
            onClick={b.onClick}
            data-force={b.force}
            className={`h-7 px-3 rounded-md text-13 font-medium cursor-default hover:brightness-110 active:brightness-90 ${LOOK[b.role]} ${FOCUS_RING}`}
          >
            {b.label}
          </button>
        ))}
      </div>
    </div>
  )
}
