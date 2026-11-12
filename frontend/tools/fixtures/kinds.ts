// The broken fixture states (broken-states.tsx), by the check that must
// catch them and only them. Shared by the fixture page and checks.pw.ts.
// Test-only: none of this is ever part of the prototype build.

/** A string that appears in the fixture bundle and must never appear in dist-prototype/. */
export const FIXTURE_MARKER = 'choix-broken-states-fixture-7f3a'

/** Fixture state → the check that must report it (and nothing else may). */
export const BROKEN = {
  console: 880,
  pageerror: 881,
  request: 882,
  external: 883,
  'root-overflow': 884,
  clip: 885,
  occlusion: 886,
  orphan: 887,
  align: 888,
  /** A control Tab never reaches, beside a roving menu whose items arrows do reach. */
  'focus-unreachable': 889,
  /** A control whose focused look equals its unfocused one. */
  'focus-ring': 890,
  /** A registered overlay that Escape does not close. */
  'focus-escape': 891,
  'forced-look': 892,
  /** Text covered by another element inside the same overlay. */
  'occlusion-in-overlay': 895,
  /** A pointer-events: none label under an opaque box. */
  'occlusion-pointer-none': 896,
  /** A control whose only focus indicator is the platform ring (outline-style: auto). */
  'focus-auto-ring': 897,
  /** Escape closes an overlay that another overlay covers, not the top one. */
  'focus-escape-covered': 898,
  /** An element marked data-centered="y" sitting 1 px low in its box. */
  centred: 899,
  /** A data-state-subject scrolled out of its pane, and a subject at opacity 0. */
  subject: 893,
  /** Two data-pointer-only controls: one focusable, one with no reason. */
  'focus-pointer-only': 894,
} as const

/** Clean fixture states: no check may report them. */
export const CLEAN = {
  /** Rows scrolled out of a scroller, over a footer: not occlusions. */
  scroller: 870,
  /** A context-menu overlay that Escape closes and arrows walk. */
  closable: 871,
  /** Elements marked data-centered that are centred. */
  centred: 872,
  /** A valid pointer-only control (reason, tabIndex -1, aria-hidden) beside a normal button. */
  'pointer-only': 873,
} as const

/**
 * The parity self-test's states (parity-states.tsx, e2e/parity/selftest.pw.ts).
 * Clean like CLEAN (no check may report them), in the same block; the
 * self-test compares their trees with each other.
 */
export const PARITY = {
  /** A list and a tile with a :hover look, at rest. */
  base: 874,
  /** `base` with the third row's padding-left 1 px wider. */
  padding: 875,
  /** `base` with the tile's hover look forced (data-force="hover"). */
  forcedHover: 876,
} as const

/** The tile whose hover look the parity self-test compares (parity-states.tsx). */
export const PARITY_HOVER_TARGET = '[data-fx-parity-tile]'

/**
 * The numbers reserved for fixtures, at the top of the gallery range, which
 * no prototype state may use (fixtures.test.ts): broken states 880–899, clean
 * ones (CLEAN and PARITY) 870–879.
 */
export const FIXTURE_BLOCKS = { clean: [870, 879], broken: [880, 899] } as const

/** The check name each broken state is caught by (focus-* are all `focus`, occlusion-* `occlusion`). */
export function checkOf(kind: keyof typeof BROKEN): string {
  for (const check of ['focus', 'occlusion']) if (kind.startsWith(`${check}-`)) return check
  return kind
}

export const STUCK_OVERLAY = 'fx-stuck'
export const CLOSABLE_OVERLAY = 'fx-menu'
/** s898: drawn on top, opened first. */
export const OVER_OVERLAY = 'fx-over'
/** s898: drawn underneath, opened last (the recorder's newest). */
export const UNDER_OVERLAY = 'fx-under'
