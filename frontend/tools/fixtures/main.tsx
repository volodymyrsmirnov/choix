// Entry of the test-only fixture page: the prototype's own controller, frame
// and harness (window.__choixProto), bare, with the broken-states and parity
// self-test registries instead of the prototype's scenarios.
import { useSyncExternalStore } from 'react'
import { createRoot } from 'react-dom/client'
import '../../src/design/tokens.css'
import '../../src/design/base.css'
import { Controller } from '../../prototype/controller'
import { Frame } from '../../prototype/Frame'
import { installHarness } from '../../prototype/harness'
import { createOverlayLog } from '../../prototype/recorder'
import { defineScenarios } from '../../prototype/registry'
import { scenarios } from './broken-states'
import { FIXTURE_MARKER } from './kinds'
import { parityScenarios } from './parity-states'

document.documentElement.dataset.fixture = FIXTURE_MARKER

const controller = new Controller(defineScenarios([...scenarios, ...parityScenarios], []))
const log = createOverlayLog()
installHarness(controller, log)

function Fixture() {
  const view = useSyncExternalStore(controller.subscribe, controller.snapshot)
  if (!view.scenario) return <div data-proto-missing>{`s${view.n} is not a fixture state.`}</div>
  return <Frame key={view.epoch} view={view} scenario={view.scenario} controller={controller} log={log} />
}

const root = document.getElementById('root')
if (!root) throw new Error('fixtures: #root is missing')
createRoot(root).render(<Fixture />)
