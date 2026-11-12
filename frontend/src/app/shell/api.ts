// The one module that imports the generated bindings (`@bindings/*`, the
// Go services through Wails). Everything else takes the services it needs as
// parameters, so the stores and the shell run under `bun test` with fakes,
// and the parity build swaps the bindings for its mocks (vite.config.ts).
// Other modules may `import type` from here: a type-only import is erased
// and never loads the bindings.
import * as Catalog from '@bindings/catalog/service'
import * as Library from '@bindings/library/service'
import * as Models from '@bindings/models/service'
import * as Settings from '@bindings/settings/service'
import * as System from '@bindings/system/service'

export { Catalog, Library, Models, Settings, System }

export type { FocusResponse, FolderView, PickResult } from '@bindings/catalog/models'
export type {
  FolderChangedEvent,
  LibraryChangedEvent,
  MenuActionEvent,
  ModelStatus as ModelStatusEvent,
  Progress,
  Quality as QualityEvent,
  ScanProgressEvent,
  ScanStateEvent,
} from '@bindings/events/models'
export type { AddResult, FolderSummary as FolderSummaryModel } from '@bindings/library/models'
export type { Body as SettingsBodyModel, Patch as SettingsPatch, SaveResult } from '@bindings/settings/models'
export type { Info, MenuContext as MenuContextModel } from '@bindings/system/models'
