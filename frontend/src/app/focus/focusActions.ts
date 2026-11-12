// Focus's and Compare's actions, by menu id (menus.json) and by the ids the
// shell's key bindings give v1's keys that no menu item lists (keys.ts): the
// container registers each with the shell's one dispatcher (runAction), so
// a single key in the webview and an accelerator from the native menu run
// the same handler. The shell's own defaults (Settings, Add Folder, Open
// Picks Folder, Reveal, the shortcut sheet, GitHub) are not taken over.
import type { ActionHandler } from '../shell/actions'
import type { FocusStore } from './focusStore'

export interface FocusActionDeps {
  /** Space / X with its toast, or the error's. */
  mark(k: 'picked' | 'rejected'): Promise<void>
  /** ⌘Z with its toast, or the error's. */
  undo(): Promise<void>
  /** Opens that cluster in Focus; nothing for null. */
  toCluster(id: number | null): void
  /** Back to the Folder page. */
  toFolder(): void
}

export function focusActionHandlers(store: FocusStore, d: FocusActionDeps): Record<string, ActionHandler> {
  return {
    pick: () => d.mark('picked'),
    reject: () => d.mark('rejected'),
    'next-photo': () => store.step(1),
    'prev-photo': () => store.step(-1),
    'next-cluster': () => d.toCluster(store.neighbour('next')),
    'prev-cluster': () => d.toCluster(store.neighbour('prev')),
    compare: () => store.toggleCompare(),
    'pixel-peep': () => store.peep(),
    'info-panel': () => store.toggleInfo(),
    'zoom-in': () => store.zoom(1),
    'zoom-out': () => store.zoom(-1),
    'zoom-fit': () => store.zoomFit(),
    // 1–4 while comparing (keys.ts sends the digit as data).
    pane: (n) => store.pane(Number(n)),
    // Esc and ⌘[: un-zoom, then leave compare, then back to the folder.
    back: () => {
      if (!store.escape()) d.toFolder()
    },
    undo: () => d.undo(),
  }
}
