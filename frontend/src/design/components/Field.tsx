import type { ReactNode } from 'react'
import { noOrphan } from '../format'

/**
 * A settings row, in one column (the owner's order): the name; a switch on
 * the name's line (`aside`), at its right end as in macOS System Settings,
 * so every switch of a page lines up down one edge; any other control
 * (`children`: a number field, a slider, a text field) on its
 * own line under the name, at most 480 px wide; a live status line
 * (`status`); then the description, at a readable measure. An error belongs
 * to the control: it sits right under it. Every part is the column's 8 px
 * gap from the next and adds no margin of its own, so the spacing is the
 * same in every row type (a part that is empty takes no room: an empty
 * status line is out of the flow).
 * The label is `${controlId}-label`, the hint `${controlId}-hint`, the error
 * `${controlId}-error`, so controls can point aria-labelledby /
 * aria-describedby at them.
 */
export function Field({
  controlId,
  title,
  hint,
  error,
  aside,
  status,
  children,
}: {
  controlId: string
  title: string
  hint: ReactNode
  error?: string
  /** A control on the name's line (a switch). */
  aside?: ReactNode
  /** A live status line under the control (SettingsStatus). */
  status?: ReactNode
  /** A control on its own line under the name. */
  children?: ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-4 min-h-6">
        <label id={`${controlId}-label`} htmlFor={controlId} className="flex-1 min-w-0 block text-13 font-medium text-fg-0">
          {title}
        </label>
        {aside}
      </div>
      {children && <div className="max-w-[480px]">{children}</div>}
      {status}
      {error && (
        <p id={`${controlId}-error`} className="m-0 text-11 text-reject">
          {noOrphan(error)}
        </p>
      )}
      <p id={`${controlId}-hint`} className="m-0 max-w-[560px] text-11 text-fg-2 leading-[1.5]">
        {typeof hint === 'string' ? noOrphan(hint) : hint}
      </p>
    </div>
  )
}
