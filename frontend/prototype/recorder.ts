// The prototype's overlay recorder (the OverlayContext value; see
// src/design/overlay.ts). One OverlayLog lives for the whole page: the flows
// check that every overlay a state registers was both opened and closed.
// Each mounted scenario gets its own FrameRecorder, created once per mount,
// so it is referentially stable across renders.
//
// No spurious records:
//   - the prototype entry does not use StrictMode, so effects run once;
//   - when a scenario unmounts (another state is opened), its frame is
//     marked dead before React runs the overlays' effect cleanups, so an
//     overlay that was open is not recorded as closed by the user.
import type { OverlayRecorder } from '../src/design/overlay'

export interface OverlayLog { registered: string[]; opened: string[]; closed: string[] }

export function createOverlayLog(): OverlayLog {
  return { registered: [], opened: [], closed: [] }
}

const add = (list: string[], id: string) => {
  if (!list.includes(id)) list.push(id)
}

export class FrameRecorder implements OverlayRecorder {
  /** Set when the scenario unmounts; later calls are ignored. */
  dead = false
  /** Open overlays of this frame, oldest first. */
  private stack: string[] = []

  constructor(private readonly log: OverlayLog) {}

  register(id: string): void {
    if (!this.dead) add(this.log.registered, id)
  }

  open(id: string): void {
    if (this.dead) return
    add(this.log.opened, id)
    this.stack = [...this.stack.filter((x) => x !== id), id]
  }

  close(id: string): void {
    if (this.dead) return
    add(this.log.closed, id)
    this.stack = this.stack.filter((x) => x !== id)
  }

  /** The most recently opened overlay that is still open. */
  top(): string | undefined {
    return this.stack[this.stack.length - 1]
  }
}
