import { useRef } from 'react'
import { noOrphan } from '../format'
import { useOverlay } from '../overlay'
import { Button } from './Button'
import { Prose } from './Prose'
import { trapTab } from './trapTab'

export interface PromptButton {
  label: string
  /** 'default' is the primary button and takes focus, so a reflexive Enter
   *  never destroys anything; 'cancel' is what Escape and a backdrop click
   *  press (the default button when there is no cancel). */
  role: 'default' | 'cancel' | 'destructive'
  onClick: () => void
}

/**
 * An in-window confirmation (v1 Settings' "Discard unsaved changes?"): a
 * modal alertdialog over a dimmed backdrop. Rendered only while open.
 */
export function Prompt({
  id,
  title,
  message,
  buttons,
}: {
  id: string
  title: string
  message: string
  buttons: PromptButton[]
}) {
  useOverlay(id, true)
  const dialogRef = useRef<HTMLDivElement>(null)
  const dismiss = buttons.find((b) => b.role === 'cancel') ?? buttons.find((b) => b.role === 'default')

  return (
    <div
      data-overlay={id}
      className="absolute inset-0 z-50 flex items-center justify-center p-6 bg-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) dismiss?.onClick()
      }}
    >
      {/* tabIndex -1: clicking the dialog's text focuses the panel instead of
          <body>, so Escape and the Tab trap keep working. */}
      <div
        ref={dialogRef}
        role="alertdialog"
        aria-modal="true"
        tabIndex={-1}
        aria-labelledby={`${id}-title`}
        aria-describedby={`${id}-desc`}
        onKeyDown={(e) => {
          // Modal: keep every key away from page-level shortcut handlers.
          e.stopPropagation()
          if (e.key === 'Escape') {
            e.preventDefault()
            dismiss?.onClick()
            return
          }
          trapTab(e, dialogRef.current)
        }}
        className="w-full max-w-[380px] rounded-lg border border-line-2 bg-bg-2 shadow-pop p-5 outline-none"
      >
        <h2 id={`${id}-title`} className="m-0 text-15 font-semibold tracking-tight">
          {noOrphan(title)}
        </h2>
        <Prose id={`${id}-desc`} className="mt-2 text-13 text-fg-2 leading-[1.5]">
          {message}
        </Prose>
        <div className="flex justify-end gap-2 mt-5">
          {buttons.map((b) => (
            <Button
              key={b.label}
              variant={b.role === 'default' ? 'primary' : 'default'}
              className={b.role === 'destructive' ? 'text-reject' : undefined}
              autoFocus={b.role === 'default'}
              onClick={b.onClick}
            >
              {b.label}
            </Button>
          ))}
        </div>
      </div>
    </div>
  )
}
