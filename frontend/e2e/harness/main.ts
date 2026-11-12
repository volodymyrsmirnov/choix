// The E2E harness page: a blank page served by the server build
// (CHOIX_ASSETS_DIR=frontend/e2e/harness/dist) that exposes the committed
// bindings to the Playwright specs as window.choix and records every app
// event the backend broadcasts.
//
// TestSupport is bound only in server builds, so the default binding
// generation has no module for it: its methods are called by name.
import { Call, Events } from '@wailsio/runtime'
import { Service as Catalog } from '@bindings/catalog'
import { Service as Library } from '@bindings/library'
import { Service as Models } from '@bindings/models'
import { Service as Settings } from '@bindings/settings'
import { Service as System } from '@bindings/system'
import * as EventNames from '../../bindings/events'

const TEST_SUPPORT = 'github.com/volodymyrsmirnov/choix/internal/app/testsupport.Service'

/** byName calls the test-support binding's method by its full name. */
function byName<T>(method: string) {
  return (...args: unknown[]): Promise<T> => Call.ByName(`${TEST_SUPPORT}.${method}`, ...args) as Promise<T>
}

/** The test-support binding (internal/app/testsupport, server builds only). */
const TestSupport = {
  AddFolderPath: byName<{ id: string; name: string; existing: boolean; relation?: string }>('AddFolderPath'),
  SetPickerPaths: byName<void>('SetPickerPaths'),
  SetConfirmAnswer: byName<void>('SetConfirmAnswer'),
  WaitIdle: byName<boolean>('WaitIdle'),
  EmitMenuAction: byName<void>('EmitMenuAction'),
}

/** One event as the page received it; at is Date.now() on arrival. */
export interface HarnessEvent {
  name: string
  data: any
  at: number
}

export interface Choix {
  Library: typeof Library
  Catalog: typeof Catalog
  Settings: typeof Settings
  Models: typeof Models
  System: typeof System
  TestSupport: typeof TestSupport
  events: HarnessEvent[]
}

declare global {
  interface Window {
    choix: Choix
  }
}

const events: HarnessEvent[] = []
for (const name of Object.values(EventNames)) {
  if (typeof name === 'string') {
    Events.On(name, (ev) => {
      events.push({ name: ev.name, data: ev.data, at: Date.now() })
    })
  }
}

window.choix = { Library, Catalog, Settings, Models, System, TestSupport, events }
