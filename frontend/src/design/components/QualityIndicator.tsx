import type { QualityStatus } from '../types'
import { ProgressRail } from './ProgressRail'
import { Spinner } from './Spinner'

/**
 * Whether the title bar shows a QualityIndicator for this status: a rating
 * pass with photos to rate. It is hidden while the scan indicator is shown,
 * so the two never appear at once (v1). Waiting for a scan says nothing in
 * the bar: the scan indicator is the progress then.
 */
export const qualityIndicatorVisible = (q: QualityStatus | null, scanShown: boolean): q is QualityStatus =>
  !!q && q.enabled && q.state === 'scoring' && !scanShown && q.total > 0

/**
 * The quality worker in the title bar, in the scan indicator's visual
 * language (ported from v1 primitives.tsx). Renders a rating pass; `waiting`,
 * like every other state, renders nothing. The caller checks
 * `qualityIndicatorVisible` first.
 */
export function QualityIndicator({ status }: { status: QualityStatus }) {
  if (status.state !== 'scoring' || status.total <= 0) return null
  const totalText = status.total.toLocaleString('en-US')
  return (
    <div data-testid="quality-progress" data-state={status.state} className="flex items-center gap-2 min-w-0 text-11">
      <Spinner small decorative />
      <span role="status" className="truncate text-fg-1">Rate quality</span>
      <span
        className="font-mono tabular-nums text-fg-2 text-right flex-shrink-0"
        style={{ width: `${((totalText.length * 2 + 3) * 6) / 10}em` /* 0.6em per JetBrains Mono character */ }}
        aria-hidden="true"
      >
        {status.done.toLocaleString('en-US')} / {totalText}
      </span>
      <ProgressRail
        className="w-20"
        label="Quality rating progress"
        value={Math.min(1, status.done / status.total)}
        valueText={`${status.done.toLocaleString('en-US')} of ${totalText} photos`}
      />
    </div>
  )
}
