// One mounted scenario inside the shared AppRoot, at the exact review size.
// It provides the overlay recorder (stable for the mount), routes Escape to
// the top open overlay through the store, and tells the controller when it
// has committed (harness.open waits for that).
import { useEffect, useLayoutEffect, useMemo, useSyncExternalStore } from 'react'
import { AppRoot } from '../src/design/components'
import { OverlayContext } from '../src/design/overlay'
import { NOW } from './clock'
import type { Controller, View } from './controller'
import { SIZE_PX } from './location'
import { FrameRecorder, type OverlayLog } from './recorder'
import type { Scenario } from './registry'
import type { ProtoStore } from './store'

export function Frame({
  view,
  scenario,
  controller,
  log,
}: {
  view: View
  scenario: Scenario
  controller: Controller
  log: OverlayLog
}) {
  const { host, epoch, toggles } = view
  // Created once per mount (the parent keys this component by epoch).
  const rec = useMemo(() => new FrameRecorder(log), [log])
  // Layout cleanups run before the overlays' effect cleanups on unmount, so
  // closes caused only by leaving the state are not recorded.
  useLayoutEffect(
    () => () => {
      rec.dead = true
    },
    [rec],
  )

  const state = useSyncExternalStore(host.subscribe, host.snapshot)
  const store: ProtoStore = useMemo(
    () => ({ state, set: host.set, go: (n) => void controller.go(n), toast: host.toast }),
    [state, host, controller],
  )

  useEffect(() => {
    controller.markRendered(epoch)
  }, [controller, epoch])

  // Escape closes the top open overlay: its id is the store key that opened
  // it. Components that handle Escape themselves (Sheet, Prompt, Dropdown)
  // stop it first.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return
      if (e.target instanceof Element && e.target.closest('[data-proto-chrome]')) return
      const top = rec.top()
      if (!top) return
      e.preventDefault()
      host.set({ [top]: false })
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [rec, host])

  const [width, height] = SIZE_PX[toggles.size]
  return (
    <OverlayContext.Provider value={rec}>
      <AppRoot width={width} height={height}>
        {scenario.render({ size: toggles.size, store, now: NOW })}
      </AppRoot>
    </OverlayContext.Provider>
  )
}
