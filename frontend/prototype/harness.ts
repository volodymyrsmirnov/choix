// window.__choixProto: the page-side handle the scripted renders (checks,
// flows, snapshots) drive. This is the one prototype file allowed a timer:
// open() gives up after OPEN_TIMEOUT_MS.
import type { Controller } from './controller'
import type { Toggles } from './location'
import type { OverlayLog } from './recorder'
import type { GroupId } from './registry'

export interface Harness {
  states: { n: number; group: GroupId; label: string; overlays: string[]; subject?: string }[]
  setToggles(t: { size?: 'min' | 'default' | 'large'; theme?: 'dark'; os?: 'macos'; freeze?: boolean; nowrapfx?: boolean }): void
  open(n: number): Promise<void>
  overlays: { registered: string[]; opened: string[]; closed: string[] }
}

declare global {
  interface Window {
    __choixProto?: Harness
  }
}

/** How long open() waits for a state to render: generous, for shared CI runners. */
export const OPEN_TIMEOUT_MS = 10_000

const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))

/** Resolves after two consecutive animation frames with no DOM mutation. */
async function quiet(): Promise<void> {
  let mutated = false
  const observer = new MutationObserver(() => {
    mutated = true
  })
  observer.observe(document, { subtree: true, childList: true, attributes: true, characterData: true })
  try {
    let still = 0
    while (still < 2) {
      mutated = false
      await frame()
      still = mutated ? 0 : still + 1
    }
  } finally {
    observer.disconnect()
  }
}

export function installHarness(controller: Controller, log: OverlayLog): Harness {
  const harness: Harness = {
    states: controller.scenarios.map((s) => ({ n: s.n, group: s.group, label: s.label, overlays: s.overlays ?? [], ...(s.subject ? { subject: s.subject } : {}) })),
    setToggles(t) {
      const patch: Partial<Toggles> = {}
      if (t.size !== undefined) patch.size = t.size
      if (t.theme !== undefined) patch.theme = t.theme
      if (t.os !== undefined) patch.os = t.os
      if (t.freeze !== undefined) patch.freeze = t.freeze
      if (t.nowrapfx !== undefined) patch.nowrapfx = t.nowrapfx
      controller.setToggles(patch)
    },
    /**
     * Opens state `n` with a fresh store and resolves when it is ready to
     * capture: rendered, fonts loaded, every image decoded (or failed), and
     * with `freeze` every running animation cancelled; then two consecutive
     * animation frames without a DOM mutation. Rejects after 2 s, or at once
     * for an unknown state.
     */
    async open(n) {
      if (!controller.find(n)) throw new Error(`no state s${n}`)
      let timer: ReturnType<typeof setTimeout> | undefined
      const deadline = new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error(`s${n} was not ready within ${OPEN_TIMEOUT_MS} ms`)), OPEN_TIMEOUT_MS)
      })
      const ready = (async () => {
        const epoch = controller.go(n)
        await controller.rendered(epoch)
        await document.fonts.ready
        await Promise.allSettled(Array.from(document.images, (img) => img.decode()))
        if (controller.snapshot().toggles.freeze) for (const a of document.getAnimations()) a.cancel()
        await quiet()
      })()
      try {
        await Promise.race([ready, deadline])
      } finally {
        clearTimeout(timer)
      }
    },
    overlays: log,
  }
  window.__choixProto = harness
  return harness
}
