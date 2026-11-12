// The format of a scenario's `design` field (plan, Context): the mockup
// screen it follows (`mockup-2026-11-11#s-a`), the v1 source it was ported
// from, named by its path under internal/ui/web/src
// (`v1:components/Toaster.tsx`), or `missing design`. The v1 sources were
// removed from the tree (they remain in git history), so a `v1:` reference
// is historical: it records where a screen was ported from and is checked
// only lexically, never on disk. Test-only (the group tests import it).
import { posix } from 'node:path'

export const DESIGN_REF = /^(mockup-2026-11-11#[a-z0-9-]+|v1:[A-Za-z0-9_./-]+\.[a-z]+|missing design)$/

/** Why `ref` is not a valid design reference, or null when it is. */
export function designRefProblem(ref: string): string | null {
  if (!DESIGN_REF.test(ref)) return `"${ref}" is not mockup-2026-11-11#<id>, v1:<path> or missing design`
  if (!ref.startsWith('v1:')) return null
  const path = posix.normalize(posix.join('src', ref.slice(3)))
  if (!path.startsWith('src/')) return `"${ref}" leaves internal/ui/web/src`
  return null
}
