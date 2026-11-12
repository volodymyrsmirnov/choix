import type { ReactNode } from 'react'
import type { Member } from '../types'
import { Icon } from './Icon'

/**
 * A dark chip floating over the Focus stage (v1's position counter, zoom
 * control, quality readout and cluster-done pill): translucent black with a
 * faint edge, so it reads on any photo.
 */
export function FocusChip({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={`flex items-center gap-2 rounded-md border border-chip-line bg-chip backdrop-blur-md ${className ?? ''}`}>
      {children}
    </div>
  )
}

/** The hairline between two parts of a chip. */
export function FocusChipRule() {
  return <span className="flex-shrink-0 w-px h-3 bg-chip-rule" aria-hidden="true" />
}

/**
 * A relative path on one line inside a flex parent (v1 PathLabel). The file
 * name never shrinks; only the folder part is ellipsised, from the left
 * (direction: rtl) so the folder nearest the file survives. The LRM marks
 * keep the bidi-neutral slashes at the right end. Full path in the title.
 */
export function FocusPathLabel({ path, className }: { path: string; className?: string }) {
  const cut = path.lastIndexOf('/') + 1
  const dir = path.slice(0, cut)
  return (
    <span className={`flex min-w-0 ${className ?? ''}`} title={path}>
      {dir && <span className="min-w-0 truncate [direction:rtl]">{`‎${dir}‎`}</span>}
      <span className="flex-shrink-0 max-w-full truncate">{path.slice(cut)}</span>
    </span>
  )
}

/**
 * A file's pick / reject mark with a glyph as well as the colour, so the two
 * stay apart without red-green vision (v1 MarkBadge). Decorative unless
 * `withText`: every place that shows it also names the state.
 */
export function FocusMark({ m, withText }: { m: Pick<Member, 'picked' | 'rejected'>; withText?: boolean }) {
  if (!m.picked && !m.rejected) return null
  return (
    <span
      className={`inline-flex items-center gap-1 ${m.picked ? 'text-pick' : 'text-reject'}`}
      aria-hidden={withText ? undefined : true}
    >
      <Icon name={m.picked ? 'check' : 'x'} size={11} />
      {withText && (m.picked ? 'Picked' : 'Rejected')}
    </span>
  )
}
