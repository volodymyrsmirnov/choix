import type { ReactNode } from 'react'

/** A key cap. Labels are macOS glyphs (⌘ ⇧ ⌥ ⌃ ↩ ⌫), never derived from the user agent. */
export function Kbd({ children, hidden }: { children: ReactNode; hidden?: boolean }) {
  return (
    <kbd className="kbd" aria-hidden={hidden ? 'true' : undefined}>
      {children}
    </kbd>
  )
}
