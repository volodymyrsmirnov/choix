import type { ReactNode } from 'react'
import { OverlayContext, type OverlayRecorder } from '../overlay'

const makeInert = (el: HTMLDivElement | null) => {
  // React 18 has no `inert` prop; set the attribute on the element itself.
  el?.setAttribute('inert', '')
}

/** The screen behind the replica is a picture: its overlays cannot open, so none registers. */
const INERT: OverlayRecorder = { register() {}, open() {}, close() {} }

/**
 * The app window behind a native-surface replica (a menu-bar menu): shown
 * dimmed, and inert, so nothing in it can be focused or clicked while the
 * replica is the thing under review. Its menus and panels do not register
 * as overlays (no user action could open or close them here).
 */
export function NativeBackdrop({ children }: { children: ReactNode }) {
  return (
    <div ref={makeInert} className="relative flex-1 min-h-0 flex flex-col" data-native-backdrop>
      <OverlayContext.Provider value={INERT}>{children}</OverlayContext.Provider>
      <div className="absolute inset-0 bg-backdrop" aria-hidden="true" />
    </div>
  )
}
