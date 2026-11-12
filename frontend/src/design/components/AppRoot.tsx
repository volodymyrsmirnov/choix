import type { ReactNode } from 'react'

/**
 * The app surface: the one `[data-app-root]` element, which carries the base
 * page styles (base.css) and is the containing block of the app's own
 * overlays. The real app mounts its screens inside it, and so does the
 * design prototype, so both render from the same root. Give it an exact
 * size with `width` and `height` (px); otherwise it fills its parent.
 */
export function AppRoot({
  children,
  width,
  height,
  className,
}: {
  children: ReactNode
  width?: number
  height?: number
  className?: string
}) {
  return (
    <div
      data-app-root
      className={`flex flex-col overflow-hidden ${className ?? ''}`}
      style={width !== undefined && height !== undefined ? { width, height } : undefined}
    >
      {children}
    </div>
  )
}
