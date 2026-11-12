import { noOrphan } from '../format'
import { useOverlay } from '../overlay'
import type { ToastItem } from '../types'
import { Button } from './Button'
import { IconButton } from './IconButton'

const TONE_BAR: Record<ToastItem['tone'], string> = {
  pick: 'bg-pick',
  reject: 'bg-reject',
  warn: 'bg-warn',
  info: 'bg-sel',
}

function Toast({ t, onUndo, onDismiss }: { t: ToastItem; onUndo: (id: string) => void; onDismiss: (id: string) => void }) {
  useOverlay(`toast:${t.id}`, true)
  return (
    <div
      className="w-[360px] rounded-lg border border-line-2 bg-toast backdrop-blur-md shadow-pop p-2.5 flex items-center gap-2.5 pointer-events-auto"
      data-overlay={`toast:${t.id}`}
      data-tone={t.tone}
      data-testid="toast"
    >
      <div className={`w-[3px] self-stretch rounded-full ${TONE_BAR[t.tone]}`} aria-hidden="true" />
      <div className="flex-1 min-w-0">
        <div className="text-12 font-semibold text-fg-0 leading-tight">{noOrphan(t.title)}</div>
        {t.body && <div className="text-11 text-fg-2 mt-0.5 leading-snug">{noOrphan(t.body)}</div>}
      </div>
      {t.undo && (
        <Button variant="ghost" className="h-[22px] text-11 px-2" kbd="⌘Z" onClick={() => onUndo(t.id)}>
          Undo
        </Button>
      )}
      <IconButton
        icon="x"
        label={`Dismiss notification: ${t.title}`}
        title="Dismiss"
        className="h-[22px] px-1 text-fg-3"
        onClick={() => onDismiss(t.id)}
      />
    </div>
  )
}

/**
 * The toast stack, bottom-right of its containing block (v1 Toaster): the
 * screen's root, or a pane the screen puts it in to keep a footer's primary
 * action clear (Settings, above its save bar). Controlled: the
 * app's container owns the items and their auto-dismiss timers; nothing here
 * keeps time. Each toast registers its own overlay id, `toast:<id>`.
 */
export function Toaster({
  items,
  onUndo,
  onDismiss,
}: {
  items: ToastItem[]
  onUndo: (id: string) => void
  onDismiss: (id: string) => void
}) {
  return (
    // data-overlay: the stack is the toasts' layer; it may lie over the page
    // (clicks pass through its gaps), so the occlusion check treats it as one.
    <div
      data-overlay="toasts"
      className="absolute bottom-4 right-4 z-50 flex flex-col gap-2.5 items-end pointer-events-none"
      role="status"
      aria-live="polite"
    >
      {items.map((t) => (
        <Toast key={t.id} t={t} onUndo={onUndo} onDismiss={onDismiss} />
      ))}
    </div>
  )
}
