import type { ReactNode } from 'react'
import { useOverlay } from '../overlay'

/**
 * A collapsible side panel (v1 Focus' Info panel): a floating card the
 * caller positions with `className`. Rendered only while `open`.
 */
export function Panel({
  id,
  open,
  children,
  label,
  className,
}: {
  id: string
  open: boolean
  children: ReactNode
  /** The panel's accessible name. */
  label?: string
  className?: string
}) {
  useOverlay(id, open)
  if (!open) return null
  return (
    <aside
      id={id}
      data-overlay={id}
      aria-label={label}
      className={`w-[280px] rounded-lg border border-line-2 bg-panel backdrop-blur-xl shadow-pop overflow-hidden flex flex-col ${className ?? ''}`}
    >
      {children}
    </aside>
  )
}
