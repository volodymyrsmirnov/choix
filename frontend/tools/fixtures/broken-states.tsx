// Test-only registry: one deliberately broken state per failure kind the
// state checks know, plus two clean states that must pass. Built into
// .design/fixtures-dist/ by tools/fixtures/vite.config.ts and served by the
// design global setup; never part of the prototype build (checks.pw.ts
// asserts dist-prototype/ holds no trace of it).
//
// Styles are inline on purpose: Tailwind does not scan tools/, and a fixture
// must not depend on a class some real component happens to use.
import { useEffect, type CSSProperties } from 'react'
import { ContextMenuReplica } from '../../src/design/components/ContextMenuReplica'
import { FOCUS_RING } from '../../src/design/components/focusRing'
import { MenuPanel } from '../../src/design/components/MenuPanel'
import { useOverlay } from '../../src/design/overlay'
import type { MenuItemDef } from '../../src/design/types'
import type { Scenario, ScenarioCtx } from '../../prototype/registry'
import { BROKEN, CLEAN, CLOSABLE_OVERLAY, OVER_OVERLAY, STUCK_OVERLAY, UNDER_OVERLAY } from './kinds'

const pad: CSSProperties = { padding: 24, display: 'flex', flexDirection: 'column', gap: 12, alignItems: 'flex-start' }
const plainButton: CSSProperties = {
  height: 28,
  padding: '0 10px',
  borderRadius: 6,
  border: '1px solid #444',
  background: '#2a2a2a',
  color: '#eee',
}

const ITEMS: MenuItemDef[] = [
  { id: 'fx-one', label: 'First', enabledIn: ['library'] },
  { id: 'fx-two', label: 'Second', enabledIn: ['library'] },
  { separator: true },
  { id: 'fx-three', label: 'Third', enabledIn: ['library'] },
]

function ConsoleError() {
  useEffect(() => {
    console.error('fixture: a console error')
  }, [])
  return <div style={pad}>Logs a console error</div>
}

function PageError() {
  useEffect(() => {
    queueMicrotask(() => {
      throw new Error('fixture: an uncaught page error')
    })
  }, [])
  return <div style={pad}>Throws an uncaught error</div>
}

function MissingImage() {
  return (
    <div style={pad}>
      <img src="/fixture-missing.png" alt="" width={32} height={32} />
    </div>
  )
}

function ExternalImage() {
  return (
    <div style={pad}>
      <img src="http://example.invalid/fixture.png" alt="" width={32} height={32} />
    </div>
  )
}

function RootOverflow() {
  return <div style={{ width: 4000, height: 8, flexShrink: 0 }} />
}

function Clipped() {
  // The text's centre lies below the clipping box, so occlusion skips it.
  return (
    <div style={pad}>
      <div style={{ width: 300, height: 20, overflow: 'hidden' }}>
        <div style={{ height: 60 }}>Clipped text</div>
      </div>
    </div>
  )
}

function Covered() {
  return (
    <div style={pad}>
      <div style={{ position: 'relative', width: 300, height: 40 }}>
        <span>Covered label</span>
        <div style={{ position: 'absolute', inset: 0, background: '#333' }} />
      </div>
    </div>
  )
}

function Orphan() {
  // min-content: every space breaks, so the last line is the lone "a".
  return (
    <div style={pad}>
      <div style={{ width: 'min-content', fontSize: 13 }}>Supercalifragilisticexpialidocious a</div>
    </div>
  )
}

function Misaligned() {
  return (
    <div style={{ ...pad, flexDirection: 'row', alignItems: 'flex-start' }}>
      <span data-align-y="fx-y">Left</span>
      <span data-align-y="fx-y" style={{ marginTop: 3 }}>
        Right
      </span>
      <div style={{ display: 'flex', flexDirection: 'column' }}>
        <span data-align-x="fx-x">Top</span>
        <span data-align-x="fx-x" style={{ marginLeft: 4 }}>
          Bottom
        </span>
      </div>
    </div>
  )
}

function Unreachable() {
  return (
    <div style={pad}>
      <button type="button" tabIndex={-1} style={plainButton}>
        Unreachable
      </button>
      {/* A roving menu: one Tab stop, arrows reach the rest. Not a failure. */}
      <MenuPanel label="Roving menu" items={ITEMS} isEnabled={() => true} />
    </div>
  )
}

function PointerOnly({ broken }: { broken: boolean }) {
  // Clean: the pointer-only control gives its reason, leaves the Tab order and
  // is hidden from assistive technology. Broken: one is focusable, one has no
  // reason.
  return (
    <div style={pad}>
      <button type="button" className={FOCUS_RING} style={plainButton}>
        A normal control
      </button>
      {broken ? (
        <>
          <button type="button" aria-hidden="true" data-pointer-only="paging: the rows are Tab stops" style={plainButton}>
            Focusable pointer-only
          </button>
          <button type="button" tabIndex={-1} aria-hidden="true" data-pointer-only="" style={plainButton}>
            No reason
          </button>
        </>
      ) : (
        <button type="button" tabIndex={-1} aria-hidden="true" data-pointer-only="paging: the rows are Tab stops" style={plainButton}>
          Pointer-only
        </button>
      )}
    </div>
  )
}

function NoRing() {
  return (
    <div style={pad}>
      <button type="button" style={{ ...plainButton, outline: 'none' }}>
        No focus ring
      </button>
    </div>
  )
}

function Stuck() {
  // Registered and open, but it ignores the store, so Escape cannot close it.
  useOverlay(STUCK_OVERLAY, true)
  return (
    <div style={pad}>
      <div role="dialog" aria-label="Stuck overlay" style={{ padding: 12, border: '1px solid #555' }}>
        An overlay Escape does not close
      </div>
    </div>
  )
}

function ForcedMismatch() {
  // The forced hover look underlines; a real hover does not. Its focus ring
  // is explicit, so only forced-look reports it.
  return (
    <div style={pad}>
      <style>
        {'.fx-forced[data-force~="hover"] { text-decoration: underline; } .fx-forced:focus-visible { outline: 2px solid #3c3; }'}
      </style>
      <button type="button" className="fx-forced" data-force="hover" style={plainButton}>
        Forced hover
      </button>
    </div>
  )
}

function CoveredInOverlay() {
  // The overlay may cover the page, but not its own text.
  return (
    <div style={pad}>
      <div data-overlay="fx-own" style={{ position: 'relative', width: 300, padding: 12, border: '1px solid #555' }}>
        <span>Covered inside the overlay</span>
        <div style={{ position: 'absolute', inset: 0, background: '#333' }} />
      </div>
    </div>
  )
}

function PointerNoneLabel() {
  // pointer-events: none hides the label from hit testing, not from sight.
  return (
    <div style={pad}>
      <div style={{ position: 'relative', width: 300, height: 40 }}>
        <span style={{ pointerEvents: 'none' }}>Label under a box</span>
        <div style={{ position: 'absolute', inset: 0, background: '#333', pointerEvents: 'none' }} />
      </div>
    </div>
  )
}

function AutoRing() {
  // No focus styles of its own: the engine draws its platform ring (outline-style: auto).
  return (
    <div style={pad}>
      <button type="button" style={{ height: 28, padding: '0 10px', border: '1px solid #444', background: '#2a2a2a', color: '#eee' }}>
        Platform ring
      </button>
    </div>
  )
}

function CoveredOverlays({ ctx }: { ctx: ScenarioCtx }) {
  // fx-over opens first and is drawn on top; fx-under opens last, so the
  // recorder calls it the newest, but it lies underneath. Escape must close
  // what is on top; here it closes fx-under.
  const show = (id: string) => ctx.store.state[id] !== false
  const at = (left: number, z: number): CSSProperties => ({ position: 'absolute', top: 24, left, zIndex: z })
  return (
    <div style={{ position: 'relative', height: 300 }}>
      {show(OVER_OVERLAY) && (
        <div style={at(24, 2)}>
          <ContextMenuReplica id={OVER_OVERLAY} label="Over" items={ITEMS} flags={[]} onItem={() => ctx.store.set({ [OVER_OVERLAY]: false })} />
        </div>
      )}
      {show(UNDER_OVERLAY) && (
        <div style={at(40, 1)}>
          <ContextMenuReplica id={UNDER_OVERLAY} label="Under" items={ITEMS} flags={[]} onItem={() => ctx.store.set({ [UNDER_OVERLAY]: false })} />
        </div>
      )}
    </div>
  )
}

// A 40 × 20 bordered box with a 16 px dot inside it: 1 px low when broken.
function CentreBox({ nudge, axis = 'y' }: { nudge: number; axis?: 'x' | 'y' | 'both' }) {
  return (
    <div style={{ position: 'relative', width: 40, height: 20, border: '1px solid #555', padding: 1 }}>
      <span
        data-centered={axis}
        style={{ position: 'absolute', left: 11, top: 1 + nudge, width: 16, height: 16, borderRadius: 8, background: '#ccc' }}
      />
    </div>
  )
}

function OffCentre() {
  return (
    <div style={pad}>
      <CentreBox nudge={1} />
    </div>
  )
}

function Centred() {
  // Centred both ways in the offsetParent's content box, and once via data-center-box.
  return (
    <div style={pad}>
      <CentreBox nudge={0} axis="both" />
      <div data-center-box style={{ width: 60, height: 30, border: '1px solid #555', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <span data-centered="both" style={{ width: 10, height: 10, background: '#ccc' }} />
      </div>
    </div>
  )
}

function Scroller() {
  return (
    <div style={{ display: 'flex', flexDirection: 'column' }}>
      <div data-scroll style={{ height: 120, overflowY: 'auto' }}>
        {Array.from({ length: 10 }, (_, i) => (
          <div key={i} data-fx-row style={{ height: 40, padding: '0 24px' }}>
            Row {i + 1}
          </div>
        ))}
      </div>
      <div data-fx-footer style={{ height: 400, padding: 24, background: '#222' }}>
        Footer below the scroller
      </div>
    </div>
  )
}

function SubjectBelowFold() {
  // The subject is the last row of a scroller that shows only its first
  // three: scrolled out of view, so it fails; the scenario's selector names
  // an element drawn at opacity 0, which fails too.
  return (
    <div>
      <div data-scroll style={{ height: 120, overflowY: 'auto' }}>
        {Array.from({ length: 10 }, (_, i) => (
          <div key={i} data-state-subject={i === 9 ? '' : undefined} style={{ height: 40, padding: '0 24px' }}>
            Row {i + 1}
          </div>
        ))}
      </div>
      <div data-fx-faded style={{ opacity: 0, padding: '0 24px' }}>
        A subject drawn at opacity 0
      </div>
    </div>
  )
}

function Closable({ ctx }: { ctx: ScenarioCtx }) {
  const open = ctx.store.state[CLOSABLE_OVERLAY] !== false
  return (
    <div style={pad}>
      {open && (
        <ContextMenuReplica
          id={CLOSABLE_OVERLAY}
          label="Closable menu"
          items={ITEMS}
          flags={[]}
          onItem={() => ctx.store.set({ [CLOSABLE_OVERLAY]: false })}
        />
      )}
    </div>
  )
}

const fx = (n: number, label: string, render: Scenario['render'], overlays?: string[]): Scenario => ({
  n,
  group: 'gallery',
  label,
  design: 'missing design',
  overlays,
  render,
})

export const scenarios: Scenario[] = [
  fx(BROKEN.console, 'Console error', () => <ConsoleError />),
  fx(BROKEN.pageerror, 'Page error', () => <PageError />),
  fx(BROKEN.request, 'Failed request', () => <MissingImage />),
  fx(BROKEN.external, 'Request to another host', () => <ExternalImage />),
  fx(BROKEN['root-overflow'], 'Root overflows horizontally', () => <RootOverflow />),
  fx(BROKEN.clip, 'Clipped text', () => <Clipped />),
  fx(BROKEN.occlusion, 'Covered label', () => <Covered />),
  fx(BROKEN.orphan, 'Orphaned word', () => <Orphan />),
  fx(BROKEN.align, 'Misaligned groups', () => <Misaligned />),
  fx(BROKEN['focus-unreachable'], 'Unreachable control', () => <Unreachable />),
  fx(BROKEN['focus-ring'], 'No focus ring', () => <NoRing />),
  fx(BROKEN['focus-escape'], 'Escape does not close', () => <Stuck />, [STUCK_OVERLAY]),
  fx(BROKEN['forced-look'], 'Forced look differs', () => <ForcedMismatch />),
  fx(BROKEN['occlusion-in-overlay'], 'Covered inside its own overlay', () => <CoveredInOverlay />),
  fx(BROKEN['occlusion-pointer-none'], 'pointer-events: none label under a box', () => <PointerNoneLabel />),
  fx(BROKEN['focus-auto-ring'], 'Platform focus ring', () => <AutoRing />),
  fx(BROKEN['focus-escape-covered'], 'Escape closes a covered overlay', (ctx) => <CoveredOverlays ctx={ctx} />, [OVER_OVERLAY, UNDER_OVERLAY]),
  fx(BROKEN.centred, 'Off-centre element', () => <OffCentre />),
  { ...fx(BROKEN.subject, 'Subject out of view', () => <SubjectBelowFold />), subject: '[data-fx-faded]' },
  { ...fx(CLEAN.scroller, 'Rows scrolled out of view', () => <Scroller />), subject: '[data-fx-footer]' },
  fx(CLEAN.closable, 'Closable overlay', (ctx) => <Closable ctx={ctx} />, [CLOSABLE_OVERLAY]),
  fx(CLEAN.centred, 'Centred elements', () => <Centred />),
  fx(BROKEN['focus-pointer-only'], 'Pointer-only controls done wrong', () => <PointerOnly broken />),
  fx(CLEAN['pointer-only'], 'A pointer-only control done right', () => <PointerOnly broken={false} />),
]
