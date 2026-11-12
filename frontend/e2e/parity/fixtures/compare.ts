// Fixtures of the Compare states 400–409 (prototype/scenarios/compare.tsx):
// Focus's compare mode, so each is built as a Focus fixture is (focus.ts
// focusFixture): the route and Go's answers for the cluster. Compare itself,
// its panes and the active one, the shared zoom and pan, and pressing Pick
// are the case's (cases.ts, FOCUS_STEPS), the way a user does them.
import { scenarios } from '../../../prototype/scenarios/compare'
import type { FocusProps } from '../../../src/design/screens/focus/props'
import type { ParityFixture } from '../mock-bindings/control'
import { focusFixture, focusScenarioProps, mediaOf } from './focus'

/** The FocusScreen props Compare scenario `n` renders. */
export const compareScenarioProps = (n: number): FocusProps => focusScenarioProps(n, scenarios)

/** The Compare states' numbers (400–409). */
export const COMPARE_STATES: number[] = scenarios.map((s) => s.n)

export const COMPARE: Record<number, ParityFixture> = Object.fromEntries(COMPARE_STATES.map((n) => [n, focusFixture(compareScenarioProps(n))]))

const compareMedia = mediaOf(COMPARE_STATES, scenarios)
/** Content hash → the prototype photo each member of a Compare state shows (as FOCUS_MEDIA). */
export const COMPARE_MEDIA: Record<string, string> = compareMedia.media
if (compareMedia.unserved.length > 0) throw new Error('parity fixtures: a Compare state shows a media URL no app URL can stand for')
