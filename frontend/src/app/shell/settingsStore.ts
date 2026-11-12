// The saved settings (Settings.Get), machine-wide. Go sends no event for
// them: the app is their only writer, and the Settings container puts what
// it saved with `set` (or calls reload() after a save that failed half-way).
import type { SettingsBody } from '../../design/types'
import { createStore, type Store } from './store'

export interface SettingsStoreDeps {
  get(): Promise<SettingsBody>
}

export function createSettingsStore(deps: SettingsStoreDeps): Store<SettingsBody> {
  return createStore(
    () => deps.get(),
    () => () => {},
  )
}
