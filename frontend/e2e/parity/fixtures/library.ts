// Fixtures of the Library states 100–131 (prototype/scenarios/library.tsx).
//
// Each fixture is read off the approved scenario itself: the scenario's
// render gives a LibraryScreen element, and its props (libraryProps with the
// state's data) are what the prototype shows. The fixture hands the app the
// same data the way Go would: the rows as Library.List answers them, the
// quality worker's snapshot when the
// state shows one, and the state's toasts (seeded: the mock bindings cannot
// answer AddFolders or OpenPicksFolder with the result that puts them up;
// ../libraryCases.test.ts holds the app's own toasts to the same copy).
//
// What a fixture cannot say — the selected row, a hovered or focused row, a
// drag over the window — is done by the state's case (cases.ts), the way a
// user or Wails does it.
import type { LibraryProps } from '../../../src/design/screens/library/props'
import type { FolderSummary } from '../../../src/design/types'
import { scenarios } from '../../../prototype/scenarios/library'
import type { Scenario, ScenarioCtx } from '../../../prototype/registry'
import type { ParityFixture } from '../mock-bindings/control'

/** A scenario context with an empty store: every state shows its defaults. */
const CTX: ScenarioCtx = {
  size: 'default',
  now: new Date(0),
  store: { state: {}, set: () => {}, go: () => {}, toast: () => {} },
}

/** The LibraryScreen props scenario `n` of `list` renders. */
export function scenarioProps(list: Scenario[], n: number): LibraryProps {
  const s = list.find((x) => x.n === n)
  if (!s) throw new Error(`parity fixtures: no scenario #${n}`)
  return s.render(CTX).props as LibraryProps
}

/** `value` as JSON data (keys whose value is undefined dropped), as Go's answer would be. */
const plain = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T

/** The Library route with `p`'s data: Library.List, the quality worker (Catalog.QualityStatus) and the toasts. */
export function libraryFixture(p: LibraryProps, folders: FolderSummary[] | null = p.folders): ParityFixture {
  return plain({
    route: '#/',
    folders: folders ?? 'pending',
    ...(p.quality ? { quality: p.quality } : {}),
    ...(p.toasts.length > 0 ? { toasts: p.toasts } : {}),
  })
}

/** The Library states' numbers (100–131). */
const LIBRARY_STATES: number[] = scenarios.map((s) => s.n)

export const LIBRARY: Record<number, ParityFixture> = Object.fromEntries(
  LIBRARY_STATES.map((n) => [n, libraryFixture(scenarioProps(scenarios, n))]),
)
