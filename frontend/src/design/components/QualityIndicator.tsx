import type { QualityStatus } from '../types'
import { Dot } from './Dot'
import { ProgressRail } from './ProgressRail'
import { Spinner } from './Spinner'

/**
 * Whether the title bar shows a QualityIndicator for this status: a download,
 * a failure, or a rating pass. A rating pass is hidden while the scan
 * indicator is shown, so the two never appear at once (v1).
 */
export const qualityIndicatorVisible = (q: QualityStatus | null, scanShown: boolean): q is QualityStatus =>
  !!q &&
  q.enabled &&
  (q.state === 'download_failed' ||
    q.state === 'error' ||
    q.state === 'downloading' ||
    (q.state === 'scoring' && !scanShown && q.total > 0))

const mb = (n: number) => Math.round(n / 1e6).toLocaleString('en-US')

/**
 * The quality worker in the title bar, in the scan indicator's visual
 * language (ported from v1 primitives.tsx). Renders nothing when there is
 * nothing to say; the caller checks `qualityIndicatorVisible` first.
 */
export function QualityIndicator({ status }: { status: QualityStatus }) {
  const { state } = status
  if (state === 'download_failed' || state === 'error') {
    const text = state === 'download_failed' ? 'Quality model download failed' : 'Quality rating stopped'
    return (
      <div
        data-testid="quality-progress"
        data-state={state}
        role="status"
        title={`${status.error ?? ''}${state === 'download_failed' ? ' — retry from Settings' : ' — restart Choix to retry'}`}
        className="flex items-center gap-2 min-w-0 text-11 text-reject"
      >
        <Dot tone="reject" />
        <span className="truncate">{text}</span>
      </div>
    )
  }
  if (state === 'downloading') {
    const known = status.size > 0
    const frac = known ? Math.min(1, status.received / status.size) : undefined
    return (
      <div
        data-testid="quality-progress"
        data-state={state}
        title="Downloading the model for the quality rating (one-time). Turn it off in Settings."
        className="flex items-center gap-2 min-w-0 text-11"
      >
        <Spinner small decorative />
        <span role="status" className="truncate text-fg-1">Quality model</span>
        <span className="font-mono tabular-nums text-fg-2 text-right flex-shrink-0 w-[2.4em]" aria-hidden="true">
          {frac !== undefined ? `${Math.floor(frac * 100)}%` : ''}
        </span>
        <ProgressRail
          className="w-20"
          label="Quality model download"
          value={frac}
          valueText={known ? `${mb(status.received)} of ${mb(status.size)} MB` : undefined}
        />
      </div>
    )
  }
  if (state === 'scoring' && status.total > 0) {
    const totalText = status.total.toLocaleString('en-US')
    return (
      <div data-testid="quality-progress" data-state={state} className="flex items-center gap-2 min-w-0 text-11">
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
  return null
}
