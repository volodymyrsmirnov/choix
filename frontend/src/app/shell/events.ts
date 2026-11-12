// The one module that imports the Wails event bus. `on` subscribes to an
// event with the payload type the bindings generate for it and returns the
// unsubscribe function; stores and the shell take `on` as a parameter so
// tests can hand them a fake bus.
import { Events } from '@wailsio/runtime'
import type {
  FolderChangedEvent,
  LibraryChangedEvent,
  MenuActionEvent,
  QualityEvent,
  ScanProgressEvent,
  ScanStateEvent,
} from './api'

/** Every event the app listens to, by name, with its payload. */
export interface AppEvents {
  'library:changed': LibraryChangedEvent
  'scan:progress': ScanProgressEvent
  'scan:state': ScanStateEvent
  'folder:changed': FolderChangedEvent
  'quality:status': QualityEvent
  'menu:action': MenuActionEvent
  /** The window became the key window (Wails' common window event; no payload). */
  'common:WindowFocus': void
  /** The window entered native full screen (Wails' common window event; no payload). */
  'common:WindowFullscreen': void
  /** The window left native full screen (Wails' common window event; no payload). */
  'common:WindowUnFullscreen': void
  /** The window was shown (Wails' common window event; no payload). */
  'common:WindowShow': void
}

export type EventName = keyof AppEvents

export type On = <K extends EventName>(name: K, handler: (data: AppEvents[K]) => void) => () => void

export const on: On = (name, handler) =>
  Events.On(name, (ev) => {
    handler(ev.data as AppEvents[typeof name])
  })
