// Fixtures of the welcome and title-bar states 30–37
// (prototype/scenarios/welcome.tsx), read off the approved scenarios as the
// Library's are (library.ts).
//
// 30, 33–36: the scanning folder is a Library.List row with its run's
// progress, as Go lists a folder being scanned. 31: the quality worker's
// snapshot (Catalog.QualityStatus); 37: two passes.
// 32: a run that stopped. Go lists only a scanning row's progress, so the
// fixture lists the folder scanning, at the stopped run's last numbers; the
// case then sends what Go sends when the job fails (scan:progress with
// state `stopped`, then scan:state `error` with the row's text).
// 40: the error screen, not the Library. Its fixture is the Library route
// with no folders (what is under the error does not show)
// and the scenario's error text in `ui`, under the key the case names
// (errorScreen.ts, cases.ts).
import { RENDER_ERROR, scenarios } from '../../../prototype/scenarios/welcome'
import type { FolderSummary } from '../../../src/design/types'
import { ERROR_SCREEN_STATE, RENDER_ERROR_KEY } from '../errorScreen'
import type { ParityFixture } from '../mock-bindings/control'
import { libraryFixture, scenarioProps } from './library'

/** The state whose run stopped (32). */
const STOPPED_STATE = 32

/** State 32's folders as Go lists them before the job fails: the stopped row still scanning. */
function beforeStop(): FolderSummary[] {
  const p = scenarioProps(scenarios, STOPPED_STATE)
  if (!p.folders || !p.scan || 'count' in p.scan) throw new Error(`parity fixtures: #${STOPPED_STATE} shows no stopped run`)
  const { folder, progress } = p.scan
  return p.folders.map((f) => {
    if (f.name !== folder) return f
    const { error: _text, ...row } = f
    return { ...row, state: 'scanning', scan: { ...progress, state: 'running' } }
  })
}

/** The welcome group's Library-screen numbers (30–37). */
const WELCOME_STATES: number[] = scenarios.map((s) => s.n).filter((n) => n !== ERROR_SCREEN_STATE)

/** The error screen's fixture (40). */
const errorScreen: ParityFixture = {
  route: '#/',
  folders: [],
  ui: { [RENDER_ERROR_KEY]: RENDER_ERROR },
}

export const WELCOME: Record<number, ParityFixture> = {
  ...Object.fromEntries(
    WELCOME_STATES.map((n) => {
      const p = scenarioProps(scenarios, n)
      return [n, libraryFixture(p, n === STOPPED_STATE ? beforeStop() : p.folders)]
    }),
  ),
  [ERROR_SCREEN_STATE]: errorScreen,
}
