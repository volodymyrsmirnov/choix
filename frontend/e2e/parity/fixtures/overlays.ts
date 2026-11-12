// Fixtures of the overlay states 600–605 (prototype/scenarios/overlays.tsx):
// the filled Library — the six invented shoots, both models ready, no
// quality status — with the state's toasts. The shortcut sheet (600) is
// opened by its case through the native menu's event, not by the fixture.
import { MODELS_READY, SIX } from '../../../prototype/mock/data'
import { TOAST_ERROR, TOAST_INFO, TOAST_PICKED, TOAST_WARN } from '../../../prototype/scenarios/overlays'
import type { ToastItem } from '../../../src/design/types'
import type { ParityFixture } from '../mock-bindings/control'

/** The prototype's OverLibrary: libraryProps(ctx, { folders: SIX, toasts }) with its defaults. */
const overLibrary = (toasts: ToastItem[] = []): ParityFixture => ({
  route: '#/',
  folders: SIX,
  models: MODELS_READY,
  ...(toasts.length > 0 ? { toasts } : {}),
})

export const OVERLAYS: Record<number, ParityFixture> = {
  600: overLibrary(),
  601: overLibrary([TOAST_PICKED]),
  602: overLibrary([TOAST_ERROR]),
  603: overLibrary([TOAST_WARN, TOAST_INFO, TOAST_PICKED]),
  604: overLibrary([TOAST_WARN]),
  605: overLibrary([TOAST_INFO]),
}
