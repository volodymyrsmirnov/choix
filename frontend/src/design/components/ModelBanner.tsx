import { megabytes } from '../format'
import type { ModelStatus } from '../types'
import { Banner } from './Banner'
import { Button } from './Button'
import { ProgressRail } from './ProgressRail'

const NAME: Record<ModelStatus['kind'], string> = { clip: 'visual grouping', quality: 'quality rating' }
const WAIT: Record<ModelStatus['kind'], string> = {
  clip: 'Folders are grouped by time until it arrives.',
  quality: 'Photos get their quality rating when it arrives.',
}

/**
 * The compact form of a model download, above a filled Library (spec §4.2):
 * progress while it downloads, the error and Retry when it failed. Renders
 * nothing in any other state.
 */
export function ModelBanner({ model, onRetry }: { model: ModelStatus; onRetry: () => void }) {
  const name = NAME[model.kind]
  if (model.state === 'failed') {
    return (
      <Banner
        testId="model-banner"
        tone="error"
        icon="alert"
        title={`The ${name} model did not download`}
        body={model.error ? `${model.error}. ${WAIT[model.kind]}` : WAIT[model.kind]}
        actions={
          <Button variant="default" icon="refresh" onClick={onRetry} aria-label={`Retry the ${name} download`}>
            Retry
          </Button>
        }
      />
    )
  }
  if (model.state !== 'downloading') return null
  const frac = model.size > 0 ? Math.min(1, model.received / model.size) : undefined
  return (
    <Banner testId="model-banner" tone="info" icon="info" title={`Downloading the ${name} model`} body={WAIT[model.kind]} live>
      <div className="mt-2 flex items-center gap-3">
        <ProgressRail className="flex-1" label={`${name} model download`} value={frac} />
        {model.size > 0 && (
          <span className="font-mono text-11 tabular-nums text-fg-2 whitespace-nowrap">
            {megabytes(model.received)} / {megabytes(model.size)} MB
          </span>
        )}
      </div>
    </Banner>
  )
}
