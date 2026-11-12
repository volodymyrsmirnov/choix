// Test-only states of the parity self-test (e2e/parity/selftest.pw.ts),
// built into the same fixture page as broken-states.tsx. Three variants of
// one small page: at rest, with one padding 1 px wider, and with the tile's
// hover look forced through data-force (the prototype's way of drawing an
// interaction state). All three are clean: the state checks report nothing.
import type { Scenario } from '../../prototype/registry'
import { PARITY } from './kinds'
import './parity.css'

function Page({ widerRow = false, forceHover = false }: { widerRow?: boolean; forceHover?: boolean }) {
  return (
    <main className="fx-parity">
      <ul className="fx-parity-list">
        <li className="fx-parity-row">First row</li>
        <li className="fx-parity-row">Second row</li>
        <li className="fx-parity-row" style={widerRow ? { paddingLeft: 13 } : undefined}>
          Third row
        </li>
      </ul>
      <div data-fx-parity-tile className="fx-parity-tile" data-force={forceHover ? 'hover' : undefined}>
        Hover tile
      </div>
    </main>
  )
}

const fx = (n: number, label: string, render: Scenario['render']): Scenario => ({
  n,
  group: 'gallery',
  label,
  design: 'missing design',
  render,
})

export const parityScenarios: Scenario[] = [
  fx(PARITY.base, 'Parity: at rest', () => <Page />),
  fx(PARITY.padding, 'Parity: one padding differs', () => <Page widerRow />),
  fx(PARITY.forcedHover, 'Parity: forced hover', () => <Page forceHover />),
]
