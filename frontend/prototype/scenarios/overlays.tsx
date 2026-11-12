// Overlay states 600–605: the keyboard-shortcut sheet and the toast kinds,
// over the filled Library (a plausible screen for each: v1 kept Focus's pick
// toast, and its Undo, alive after going back to the Library).
//
// Store keys: `shortcuts` (the sheet's overlay id; true while open) and
// `toast:<id>` (false once a toast is dismissed), as libraryWiring reads them.
import { LibraryScreen } from '../../src/design/screens/library/LibraryScreen'
import { SHORTCUTS_OVERLAY } from '../../src/design/screens/overlays/props'
import { ShortcutsSheet } from '../../src/design/screens/overlays/ShortcutsSheet'
import type { ToastItem } from '../../src/design/types'
import { plural } from '../../src/design/format'
import { SIX } from '../mock/data'
import type { Scenario, ScenarioCtx } from '../registry'
import { read } from '../store'
import { libraryProps } from './libraryWiring'

// v1 Focus: "Picked" with Undo ⌘Z; Undo swaps it for "Undone".
export const TOAST_PICKED: ToastItem = { id: 'picked', tone: 'pick', title: 'Picked', undo: true }
export const TOAST_UNDONE: ToastItem = { id: 'undone', tone: 'pick', title: 'Undone' }
export const TOAST_ERROR: ToastItem = {
  id: 'rescan-failed',
  tone: 'reject',
  title: 'Rescan of “Kyoto” failed',
  body: 'Choix may no longer read this folder. Add it again to give Choix access.',
}
export const TOAST_WARN: ToastItem = {
  id: 'unreadable',
  tone: 'warn',
  title: `${plural(3, 'file', 'files')} in “Patagonia” could not be read`,
  body: 'They are left out; every other photo is ready to review.',
}
export const TOAST_INFO: ToastItem = {
  id: 'clip-ready',
  tone: 'info',
  title: 'Visual grouping is ready',
  body: 'Folders scanned without it are being grouped again.',
}

function OverLibrary({ ctx, toasts = [], sheet = false }: { ctx: ScenarioCtx; toasts?: ToastItem[]; sheet?: boolean }) {
  const { state, set } = ctx.store
  const lib = libraryProps(ctx, { folders: SIX, toasts })
  const open = read(state, SHORTCUTS_OVERLAY, sheet)
  return (
    <div className="relative flex flex-col h-full min-h-0">
      <LibraryScreen
        {...lib}
        onHelp={() => set({ [SHORTCUTS_OVERLAY]: true })}
        onUndoToast={(id) => {
          lib.onUndoToast(id)
          if (id === TOAST_PICKED.id) ctx.store.toast(TOAST_UNDONE)
        }}
      />
      {open && <ShortcutsSheet screen="library" onClose={() => set({ [SHORTCUTS_OVERLAY]: false })} />}
    </div>
  )
}

const toastState = (n: number, label: string, toasts: ToastItem[]): Scenario => ({
  n,
  group: 'overlays',
  label,
  design: 'v1:components/Toaster.tsx',
  overlays: toasts.map((t) => `toast:${t.id}`),
  subject: toasts.map((t) => `[data-overlay="toast:${t.id}"]`).join(', '),
  render: (ctx) => <OverLibrary ctx={ctx} toasts={toasts} />,
})

export const scenarios: Scenario[] = [
  {
    n: 600,
    group: 'overlays',
    label: 'Shortcut sheet',
    design: 'v1:components/Shortcuts.tsx',
    overlays: [SHORTCUTS_OVERLAY],
    subject: '[data-shortcuts-group="library"]',
    render: (ctx) => <OverLibrary ctx={ctx} sheet />,
  },
  toastState(601, 'Pick toast with Undo', [TOAST_PICKED]),
  toastState(602, 'Error toast', [TOAST_ERROR]),
  toastState(603, 'Three stacked toasts', [TOAST_WARN, TOAST_INFO, TOAST_PICKED]),
  toastState(604, 'Warning toast', [TOAST_WARN]),
  toastState(605, 'Info toast', [TOAST_INFO]),
]
