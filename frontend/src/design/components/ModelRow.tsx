import { bytes, megabytes } from '../format'
import type { ModelStatus } from '../types'
import { Button } from './Button'
import { Icon } from './Icon'
import { ProgressRail } from './ProgressRail'
import { Prose } from './Prose'
import { Switch } from './Switch'

const INFO: Record<ModelStatus['kind'], { title: string; detail: string; optional?: boolean }> = {
  clip: { title: 'Visual grouping', detail: 'CLIP ViT-B/32' },
  quality: { title: 'Quality rating', detail: 'TOPIQ', optional: true },
}

/**
 * One model download in the welcome panel (spec §4.2): name and size, its
 * state (progress, queued, ready, off, failed with Retry) and its on/off
 * switch, which is the model's setting (`visual_grouping` for CLIP,
 * `quality_rating` for the quality model).
 */
export type ModelRowProps = {
  /** Unique in the document; prefixes the row's element ids. */
  id: string
  model: ModelStatus
  onRetry: () => void
  /** A Retry is in flight: the button shows busy. */
  retrying?: boolean
  /** data-testid of the Retry button (v1 Settings: `quality-retry`). */
  retryTestId?: string
} & (
  | { showSwitch?: true; onToggle: (on: boolean) => void }
  /** Where another control holds the setting (Settings' field switch, which
   *  may hold an unsaved edit): the row only reports the download. */
  | { showSwitch: false; onToggle?: never }
)

export function ModelRow(props: ModelRowProps) {
  const { id, model, onRetry, retrying, retryTestId } = props
  const showSwitch = props.showSwitch !== false
  const info = INFO[model.kind]
  const size = model.size // 0 while unknown
  const titleId = `${id}-title`
  const on = model.state !== 'disabled'
  const downloading = model.state === 'downloading'
  const failed = model.state === 'failed'

  return (
    <div
      id={id}
      data-model={model.kind}
      data-state={model.state}
      className={`grid ${showSwitch ? 'grid-cols-[minmax(0,1fr)_auto_auto]' : 'grid-cols-[minmax(0,1fr)_auto]'} items-center gap-x-3 gap-y-1 px-3.5 py-3`}
    >
      <div className="min-w-0">
        <div id={titleId} className={`text-13 font-semibold ${on ? 'text-fg-0' : 'text-fg-2'}`}>
          {info.title}
        </div>
        <div className="font-mono text-11 text-fg-2 truncate">
          {info.detail}
          {size > 0 ? ` · ${bytes(size)}` : ''}
          {info.optional ? ' · optional' : ''}
        </div>
      </div>

      <div className="flex items-center gap-2 text-11 text-fg-2 whitespace-nowrap">
        {downloading && (
          <span className="font-mono tabular-nums">
            {size > 0 ? `${megabytes(model.received)} / ${megabytes(size)} MB` : `${megabytes(model.received)} MB`}
          </span>
        )}
        {model.state === 'queued' && <span className="font-mono">Queued</span>}
        {model.state === 'disabled' && <span className="font-mono">Off</span>}
        {model.state === 'ready' && (
          <span className="flex items-center gap-1 font-mono text-pick">
            <Icon name="check" size={12} />
            Ready
          </span>
        )}
        {failed && (
          <>
            <span className="font-mono text-reject">Failed</span>
            <Button variant="default" icon="refresh" busy={retrying} onClick={onRetry} aria-label={`Retry the ${info.title.toLowerCase()} download`} data-testid={retryTestId}>
              Retry
            </Button>
          </>
        )}
      </div>

      {props.showSwitch !== false && <Switch checked={on} onChange={props.onToggle} labelledBy={titleId} />}

      {downloading && (
        <ProgressRail
          className="col-span-full mt-1"
          label={`${info.title} model download`}
          value={size > 0 ? Math.min(1, model.received / size) : undefined}
          valueText={size > 0 ? `${megabytes(model.received)} of ${megabytes(size)} MB` : undefined}
        />
      )}
      {failed && model.error && (
        <Prose className="col-span-full text-11 text-reject leading-snug">{model.error}</Prose>
      )}
    </div>
  )
}
