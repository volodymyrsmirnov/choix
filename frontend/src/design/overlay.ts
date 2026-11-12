// Overlay registration. Every dialog, menu, popover, sheet, collapsible panel
// and toast calls `useOverlay(id, open)`. Its outermost element carries an
// overlay role (menu, listbox, dialog, alertdialog, tooltip) or
// `data-overlay="<id>"`, so the occlusion check (tools/checks/occlusion.ts)
// knows what an open overlay is allowed to cover.
//
// In the app the default context does nothing. The prototype provides a
// recorder that keeps `registered`, `opened` and `closed` (the flows check
// that every registered overlay was both opened and closed) and routes
// Escape to the topmost open overlay.
//
// Semantics of the calls a component makes:
//   register(id)  once per mount (and again if the id changes), open or not
//   open(id)      when `open` becomes true (or the component mounts open)
//   close(id)     when `open` becomes false, or the component unmounts open
//
// The provider's value must be referentially stable (create it once, or
// memoize it). useOverlay's effects depend on it: a new object on every render
// would re-run them, so every open overlay would record a spurious
// close + open, and every overlay a fresh register, on each render.
import { createContext, useContext, useEffect } from 'react'

export interface OverlayRecorder {
  register(id: string): void
  open(id: string): void
  close(id: string): void
}

const NOOP: OverlayRecorder = {
  register() {},
  open() {},
  close() {},
}

export const OverlayContext = createContext<OverlayRecorder>(NOOP)

export function useOverlay(id: string, open: boolean): void {
  const rec = useContext(OverlayContext)
  useEffect(() => {
    rec.register(id)
  }, [rec, id])
  useEffect(() => {
    if (!open) return
    rec.open(id)
    return () => rec.close(id)
  }, [rec, id, open])
}
