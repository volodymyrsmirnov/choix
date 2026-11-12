import type { ModelStatus } from '../types'
import { ModelRow } from './ModelRow'

/**
 * A model's download under its Settings field's switch: the ModelRow in a
 * card, without the row's own switch (the field's switch holds the setting).
 * No margin of its own: the field's column spaces it.
 */
export function SettingsModelCard({
  model,
  retrying,
  retryTestId,
  onRetry,
}: {
  model: ModelStatus
  retrying?: boolean
  retryTestId?: string
  onRetry: () => void
}) {
  return (
    <div className="rounded-lg border border-line-1 bg-bg-2">
      <ModelRow
        id={`settings-model-${model.kind}`}
        model={model}
        showSwitch={false}
        retrying={retrying}
        retryTestId={retryTestId}
        onRetry={onRetry}
      />
    </div>
  )
}
