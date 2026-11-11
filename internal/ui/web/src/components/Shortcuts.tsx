import React, { useEffect, useRef, useSyncExternalStore } from 'react'

// Shared contract for the keyboard-shortcuts overlay ("?").
//
// The overlay is mounted once by App. Library and Focus own their own
// window-level key handlers, so they need two things from here without
// importing App: a way to open the overlay from a button, and a synchronous
// check so their handlers stand down while it is open (otherwise Space would
// pick a photo behind the dialog).
//
// This module must not import ./primitives: primitives imports
// setShortcutsOpen for the AppBar button, and a cycle would make the
// evaluation order of the two modules matter.
let open = false
const listeners = new Set<() => void>()

export function shortcutsOpen(): boolean { return open }
export function setShortcutsOpen(v: boolean): void {
  if (open === v) return
  open = v
  for (const l of listeners) l()
}
export function subscribeShortcuts(l: () => void): () => void {
  listeners.add(l)
  return () => { listeners.delete(l) }
}

// The mounted dialog element, for the Tab focus trap in the key handler.
let dialogEl: HTMLElement | null = null

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function isEditable(t: EventTarget | null): boolean {
  if (!(t instanceof HTMLElement)) return false
  const tag = t.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || t.isContentEditable
}

// Keep Tab / Shift+Tab inside the dialog. Only the wrap-around cases are
// prevented; every other Tab keeps the browser's native focus order.
function trapTab(e: KeyboardEvent) {
  if (!dialogEl) return
  const items = Array.from(dialogEl.querySelectorAll<HTMLElement>(FOCUSABLE))
  if (items.length === 0) { e.preventDefault(); dialogEl.focus(); return }
  const first = items[0]
  const last = items[items.length - 1]
  const active = document.activeElement
  const inside = active instanceof Node && dialogEl.contains(active) && active !== dialogEl
  if (e.shiftKey && (!inside || active === first)) { e.preventDefault(); last.focus(); return }
  if (!e.shiftKey && (!inside || active === last)) { e.preventDefault(); first.focus() }
}

// handleShortcutsKeydown is App's window keydown listener. App registers it
// in the capture phase from a layout effect, which runs before any screen's
// (passive) effects, so it sits ahead of Focus's own capture-phase listener
// in dispatch order — including after Focus re-registers its handler, since
// re-adding a listener appends it.
//
// While the dialog is open it swallows every keydown (stopImmediatePropagation)
// so nothing leaks to the screen behind: not Esc (Focus would navigate back),
// not Space (pick), not ⌘K. Default actions are left alone except where we
// consume the key, so Tab still moves focus, Enter/Space still press the
// focused button, and browser chords (⌘R, ⌘W) still work.
export function handleShortcutsKeydown(e: KeyboardEvent): void {
  // AltGr arrives as Ctrl+Alt on some layouts; a '?' typed through it is a
  // character, not a chord.
  const altGr = e.getModifierState('AltGraph')
  const chord = e.metaKey || ((e.ctrlKey || e.altKey) && !altGr)
  if (open) {
    e.stopImmediatePropagation()
    if (e.key === 'Escape' || (e.key === '?' && !chord)) {
      e.preventDefault()
      setShortcutsOpen(false)
      return
    }
    if (e.key === 'Tab') trapTab(e)
    return
  }
  // Match the produced character rather than Shift+Slash so layouts that put
  // '?' elsewhere (AZERTY: Shift+comma) work too.
  if (e.key !== '?' || chord || e.repeat || e.isComposing) return
  if (isEditable(e.target)) return
  e.preventDefault()
  e.stopImmediatePropagation()
  setShortcutsOpen(true)
}

// ⌘ on Apple platforms, Ctrl elsewhere — the screens accept either, but the
// help should show the one the user's keyboard actually has.
const IS_MAC = typeof navigator !== 'undefined'
  && /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent)
const mod = (k: string) => (IS_MAC ? `⌘${k}` : `Ctrl+${k}`)

// A row's keys: outer array = alternatives (joined with "or"), inner array =
// a set of related keys shown side by side with "/" (e.g. ← / →).
type Row = { keys: string[][]; label: string; note?: string }
type Group = { id: 'library' | 'focus' | 'compare' | 'global'; title: string; rows: Row[] }

// Mirrors the key handlers in Library.tsx and Focus.tsx — update both
// together when a shortcut changes.
function groups(): Record<Group['id'], Group> {
  return {
    library: {
      id: 'library', title: 'Library',
      rows: [
        { keys: [['↑', '↓'], ['J', 'K']], label: 'Move between clusters' },
        { keys: [['F'], ['↵']], label: 'Open cluster in Focus' },
        { keys: [[mod('K')]], label: 'Search' },
      ],
    },
    focus: {
      id: 'focus', title: 'Focus',
      rows: [
        { keys: [['←', '→'], ['↑', '↓']], label: 'Previous / next photo' },
        { keys: [['Space']], label: 'Pick', note: 'Press again to unpick' },
        { keys: [['X']], label: 'Reject', note: 'Press again to unreject' },
        { keys: [[mod('Z')]], label: 'Undo last pick or reject' },
        { keys: [['C']], label: 'Compare' },
        { keys: [['Z']], label: 'Pixel-peep', note: 'Photos only' },
        { keys: [['+', '−', '0']], label: 'Zoom in / out / fit' },
        { keys: [['I']], label: 'Info panel' },
        { keys: [['↵']], label: 'Next cluster' },
        { keys: [['⇧↵']], label: 'Previous cluster' },
        { keys: [['Esc']], label: 'Back to Library', note: 'Un-zooms or leaves compare first' },
      ],
    },
    compare: {
      id: 'compare', title: 'Compare',
      rows: [
        { keys: [['1–4'], ['←', '→']], label: 'Choose the active pane' },
        { keys: [['Space'], ['X']], label: 'Pick / reject the active pane' },
        { keys: [], label: 'Click rail thumbnails to add or remove panes', note: 'Up to 4' },
      ],
    },
    global: {
      id: 'global', title: 'Everywhere',
      rows: [
        { keys: [['?']], label: 'Show or hide this help' },
      ],
    },
  }
}

const Keys: React.FC<{ keys: string[][] }> = ({ keys }) => (
  <span className="flex items-center gap-1 flex-shrink-0 text-[11px] text-fg-3">
    {keys.map((alt, i) => (
      <React.Fragment key={i}>
        {i > 0 && <span className="px-0.5">or</span>}
        {alt.map((k, j) => (
          <React.Fragment key={k}>
            {j > 0 && <span aria-hidden="true">/</span>}
            <kbd className="kbd">{k}</kbd>
          </React.Fragment>
        ))}
      </React.Fragment>
    ))}
  </span>
)

const GroupView: React.FC<{ g: Group; current: boolean; nested?: boolean }> = ({ g, current, nested }) => {
  const titleID = `shortcuts-group-${g.id}`
  return (
    <section aria-labelledby={titleID} className={nested ? 'mt-3' : ''}>
      <h3 id={titleID} className={`eyebrow m-0 mb-1.5 flex items-center gap-2 ${current ? 'text-pick' : ''}`}>
        {g.title}
        {current && (
          <span className="normal-case tracking-normal font-sans font-normal text-fg-2">· this screen</span>
        )}
      </h3>
      <ul className="list-none m-0 p-0 flex flex-col">
        {g.rows.map(r => (
          <li key={r.label} className="flex items-center gap-3 py-1 border-b border-line-1 last:border-b-0">
            <span className="flex-1 min-w-0 text-xs text-fg-1">
              {r.label}
              {r.note && <span className="text-[11px] text-fg-3"> — {r.note}</span>}
            </span>
            {r.keys.length > 0 && <Keys keys={r.keys} />}
          </li>
        ))}
      </ul>
    </section>
  )
}

export const ShortcutsOverlay: React.FC<{ screen: 'library' | 'focus' | 'settings' }> = ({ screen }) => {
  const isOpen = useSyncExternalStore(subscribeShortcuts, shortcutsOpen)
  const dialogRef = useRef<HTMLDivElement>(null)
  // Backdrop close needs both the press and the release on the backdrop: a
  // text selection dragged from inside the dialog and released outside would
  // otherwise dispatch its click on the backdrop and close the dialog.
  const downOnBackdrop = useRef(false)

  useEffect(() => {
    if (!isOpen) return
    dialogEl = dialogRef.current
    // Focus the dialog itself (not its first button) so screen readers start
    // at the labelled title, and remember where focus was to hand it back.
    const prev = document.activeElement instanceof HTMLElement ? document.activeElement : null
    dialogRef.current?.focus()
    return () => {
      dialogEl = null
      if (prev && prev.isConnected && prev !== document.body) prev.focus({ preventScroll: true })
    }
  }, [isOpen])

  if (!isOpen) return null

  const g = groups()
  const focusFirst = screen === 'focus'
  const library = <GroupView g={g.library} current={screen === 'library'} />
  const focus = (
    <div>
      <GroupView g={g.focus} current={focusFirst} />
      <GroupView g={g.compare} current={false} nested />
    </div>
  )

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center p-6"
         style={{ background: 'oklch(0 0 0 / 0.55)' }}
         onMouseDown={e => { downOnBackdrop.current = e.target === e.currentTarget }}
         onClick={e => {
           if (downOnBackdrop.current && e.target === e.currentTarget) setShortcutsOpen(false)
           downOnBackdrop.current = false
         }}>
      <div ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby="shortcuts-title"
           tabIndex={-1} data-testid="shortcuts-overlay"
           className="w-[640px] max-w-full max-h-[calc(100vh-48px)] overflow-y-auto rounded-lg border border-line-2 bg-bg-2 shadow-pop outline-none">
        <div className="flex items-center gap-3 px-4 h-11 border-b border-line-1 sticky top-0 bg-bg-2">
          <h2 id="shortcuts-title" className="m-0 flex-1 text-[13px] font-semibold text-fg-0">Keyboard shortcuts</h2>
          <button className="btn btn-ghost" onClick={() => setShortcutsOpen(false)}>
            Close <span className="kbd">Esc</span>
          </button>
        </div>
        {/* The current screen's group comes first; Settings has no shortcuts
            of its own, so it keeps the natural Library → Focus order. */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-4 p-4">
          {focusFirst ? <>{focus}{library}</> : <>{library}{focus}</>}
        </div>
        <div className="px-4 pb-4">
          <GroupView g={g.global} current={false} />
        </div>
      </div>
    </div>
  )
}
