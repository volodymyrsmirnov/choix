import { stageLabel } from '../format'
import type { ScanProgress } from '../types'
import { Dot } from './Dot'
import { ProgressRail } from './ProgressRail'
import { Spinner } from './Spinner'

/** The title bar's compact scan progress (ported from v1 primitives.tsx). */
export function ScanIndicator({ scan }: { scan: ScanProgress }) {
  const { stage, done, total, failed, state } = scan
  const running = state === 'running'
  const label = stageLabel(stage)
  // Size the counter for the largest value this stage can show. `total` is
  // fixed for the whole stage and the font is monospaced, so the counter
  // never changes width tick-to-tick and the bar beside it never jitters.
  // In em, not ch: a JetBrains Mono character is 0.6em, while `ch` follows
  // each platform's rounded glyph advance (lint-design: font-unit).
  const totalText = total.toLocaleString('en-US')
  const counterWidth = `${((totalText.length * 2 + 3) * 6) / 10}em`
  return (
    <div data-testid="scan-progress" data-state={state} className="flex items-center gap-2 min-w-0 text-11">
      {running ? <Spinner small decorative /> : <Dot tone="reject" />}
      {/* Only the stage label is a live region: it changes a handful of times
          per scan, whereas the counter ticks once per file. */}
      <span role="status" aria-live="polite" className={`truncate ${running ? 'text-fg-1' : 'text-reject'}`}>
        <span className="sr-only">{running ? 'Scanning: ' : 'Scan stopped during '}</span>
        {label}
        {!running && <span aria-hidden="true"> · stopped</span>}
      </span>
      {total > 0 && (
        <span
          className="font-mono tabular-nums text-fg-2 text-right flex-shrink-0"
          style={{ width: counterWidth }}
          aria-hidden="true"
        >
          {done.toLocaleString('en-US')} / {totalText}
        </span>
      )}
      {running && (
        <ProgressRail
          className="w-20"
          label={`${label} progress`}
          value={total > 0 ? done / total : undefined}
          valueText={total > 0 ? `${done.toLocaleString('en-US')} of ${totalText} files` : undefined}
        />
      )}
      {failed > 0 && (
        <span className="font-mono tabular-nums text-reject flex-shrink-0">
          {failed.toLocaleString('en-US')} failed
        </span>
      )}
    </div>
  )
}
