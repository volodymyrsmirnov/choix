import { useEffect, useRef, type ReactNode } from 'react'
import { noOrphan } from '../format'
import { useOverlay } from '../overlay'
import { Button } from './Button'
import { trapTab } from './trapTab'

/**
 * A modal sheet over the window (v1's keyboard-shortcuts overlay): a dimmed
 * backdrop, a titled panel with a Close button, scrollable content. It takes
 * focus when it opens and gives it back when it closes; Tab stays inside;
 * Escape and a click on the backdrop call `onClose`. Rendered only while open.
 */
export function Sheet({
  id,
  title,
  onClose,
  children,
  testId,
}: {
  id: string
  title: string
  onClose: () => void
  children: ReactNode
  testId?: string
}) {
  useOverlay(id, true)
  const dialogRef = useRef<HTMLDivElement>(null)
  const downOnBackdrop = useRef(false)

  useEffect(() => {
    const prev = document.activeElement instanceof HTMLElement ? document.activeElement : null
    dialogRef.current?.focus()
    return () => {
      if (prev && prev.isConnected && prev !== document.body) prev.focus({ preventScroll: true })
    }
  }, [])

  return (
    <div
      data-overlay={id}
      className="absolute inset-0 z-[60] flex items-center justify-center p-6 bg-backdrop"
      onMouseDown={(e) => {
        downOnBackdrop.current = e.target === e.currentTarget
      }}
      onClick={(e) => {
        if (downOnBackdrop.current && e.target === e.currentTarget) onClose()
        downOnBackdrop.current = false
      }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={`${id}-title`}
        tabIndex={-1}
        data-testid={testId}
        data-scroll
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            e.stopPropagation()
            onClose()
            return
          }
          trapTab(e, dialogRef.current)
        }}
        className="w-[640px] max-w-full max-h-full overflow-y-auto rounded-lg border border-line-2 bg-bg-2 shadow-pop outline-none"
      >
        <div className="flex items-center gap-3 px-4 h-11 border-b border-line-1 sticky top-0 z-10 bg-bg-2">
          <h2 id={`${id}-title`} className="m-0 flex-1 text-13 font-semibold text-fg-0">
            {noOrphan(title)}
          </h2>
          <Button variant="ghost" onClick={onClose} kbd="Esc">
            Close
          </Button>
        </div>
        {children}
      </div>
    </div>
  )
}
