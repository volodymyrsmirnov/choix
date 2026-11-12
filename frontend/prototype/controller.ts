// What the prototype shows: the current state (number), the toggles, and a
// mount counter (`epoch`) that changes whenever a state is opened, so the
// scenario remounts with a fresh store. Kept in sync with the URL
// (location.ts): opening a state pushes a history entry; a toggle replaces
// the current one.
import type { Scenario } from './registry'
import { locationHref, parseLocation, type Toggles } from './location'
import { createStoreHost, type StoreHost } from './store'

export interface View {
  n: number | null
  scenario: Scenario | undefined
  toggles: Toggles
  epoch: number
  /** The store of this mount; a new one for every opened state. */
  host: StoreHost
}

export class Controller {
  private view: View
  private readonly listeners = new Set<() => void>()
  private readonly waiters: { epoch: number; resolve: () => void }[] = []
  private renderedEpoch = -1

  constructor(readonly scenarios: Scenario[]) {
    const { n, toggles } = parseLocation(window.location.search, window.location.hash)
    const first = n ?? scenarios[0]?.n ?? null
    this.view = { n: first, scenario: this.find(first), toggles, epoch: 0, host: createStoreHost() }
    window.history.replaceState(null, '', locationHref(window.location.pathname, first, toggles))
    this.applyDocumentToggles()
    window.addEventListener('popstate', () => this.syncFromUrl())
    window.addEventListener('hashchange', () => this.syncFromUrl())
  }

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  readonly snapshot = (): View => this.view

  find(n: number | null): Scenario | undefined {
    return n === null ? undefined : this.scenarios.find((s) => s.n === n)
  }

  /** Opens state `n` with a fresh store (also when it is already open). Returns the new epoch. */
  readonly go = (n: number): number => {
    window.history.pushState(null, '', locationHref(window.location.pathname, n, this.view.toggles))
    return this.mount(n)
  }

  setToggles(patch: Partial<Toggles>): void {
    const toggles = { ...this.view.toggles, ...patch }
    window.history.replaceState(null, '', locationHref(window.location.pathname, this.view.n, toggles))
    this.view = { ...this.view, toggles }
    this.applyDocumentToggles()
    this.emit()
  }

  /** Called by the mounted scenario after it commits. */
  markRendered(epoch: number): void {
    this.renderedEpoch = Math.max(this.renderedEpoch, epoch)
    for (const w of [...this.waiters]) {
      if (w.epoch <= this.renderedEpoch) {
        this.waiters.splice(this.waiters.indexOf(w), 1)
        w.resolve()
      }
    }
  }

  /** Resolves once the scenario of `epoch` (or a later one) has rendered. */
  rendered(epoch: number): Promise<void> {
    if (epoch <= this.renderedEpoch) return Promise.resolve()
    return new Promise((resolve) => this.waiters.push({ epoch, resolve }))
  }

  private mount(n: number): number {
    const epoch = this.view.epoch + 1
    this.view = { ...this.view, n, scenario: this.find(n), epoch, host: createStoreHost() }
    this.emit()
    return epoch
  }

  private syncFromUrl(): void {
    const { n, toggles } = parseLocation(window.location.search, window.location.hash)
    this.view = { ...this.view, toggles }
    this.applyDocumentToggles()
    if (n !== null && n !== this.view.n) this.mount(n)
    else this.emit()
  }

  private applyDocumentToggles(): void {
    const html = document.documentElement
    const { freeze, nowrapfx, theme, os } = this.view.toggles
    html.toggleAttribute('data-freeze', freeze)
    html.toggleAttribute('data-nowrapfx', nowrapfx)
    html.dataset.theme = theme
    html.dataset.os = os
  }

  private emit(): void {
    for (const l of this.listeners) l()
  }
}
