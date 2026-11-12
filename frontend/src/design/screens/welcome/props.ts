import type { ModelStatus } from '../../types'

/** The first-launch panel (spec §4.2): the two model downloads and Add Folder. */
export interface WelcomeProps {
  models: ModelStatus[]
  /** False while the Mac has no network: failed rows say why. */
  online: boolean
  onAdd(): void
  onRetryModel(kind: ModelStatus['kind']): void
  onToggleModel(kind: ModelStatus['kind'], on: boolean): void
}
