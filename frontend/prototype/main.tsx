// The design prototype: every screen state, clickable. `#s102` opens a
// state; `?bare` shows only the app surface at the exact size (for scripted
// renders); otherwise the navigator sits on the left. See location.ts for
// the URL contract and harness.ts for window.__choixProto.
//
// No StrictMode: its doubled effects would make the overlay recorder see
// opens and closes that never happened.
import { useSyncExternalStore } from 'react'
import { createRoot } from 'react-dom/client'
import '../src/design/tokens.css'
import '../src/design/base.css'
import { Controller } from './controller'
import { Frame } from './Frame'
import { installHarness } from './harness'
import { SIZE_PX } from './location'
import { Navigator } from './navigator/Navigator'
import { createOverlayLog } from './recorder'
import { SCENARIOS } from './scenarios'

const controller = new Controller(SCENARIOS)
const log = createOverlayLog()
installHarness(controller, log)

function Missing({ n }: { n: number | null }) {
  return (
    <div data-proto-missing className="p-6 font-sans text-13 text-fg-1">
      {n === null ? 'No state selected.' : `s${n} is not in the prototype yet.`}
    </div>
  )
}

function Prototype() {
  const view = useSyncExternalStore(controller.subscribe, controller.snapshot)
  const { scenario, toggles, epoch, n } = view
  const frame = scenario ? (
    <Frame key={epoch} view={view} scenario={scenario} controller={controller} log={log} />
  ) : (
    <Missing n={n} />
  )

  if (toggles.bare) return frame

  const [w, h] = SIZE_PX[toggles.size]
  return (
    <div className="flex h-screen bg-bg-0 font-sans">
      <Navigator
        scenarios={controller.scenarios}
        current={n}
        toggles={toggles}
        onGo={controller.go}
        onToggles={(t) => controller.setToggles(t)}
      />
      {/* Not a <main>: the screens draw the app's own main landmark. */}
      <div className="flex-1 min-w-0 overflow-auto p-6">
        <div data-proto-chrome className="mb-2 flex items-baseline gap-3 text-12 text-fg-2">
          <span className="font-mono text-fg-0">s{n}</span>
          <span className="text-fg-1">{scenario?.label}</span>
          <span className="font-mono text-11 text-fg-3">
            {scenario?.design} · {w} × {h}
          </span>
        </div>
        <div className="w-max border border-line-2 shadow-pop">{frame}</div>
      </div>
    </div>
  )
}

const root = document.getElementById('root')
if (!root) throw new Error('prototype: #root is missing')
createRoot(root).render(<Prototype />)
