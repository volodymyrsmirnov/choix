import { STAGE_ORDER, stageLabel } from '../format'
import type { ScanProgress } from '../types'
import { Icon } from './Icon'
import { ProgressRail } from './ProgressRail'
import { Spinner } from './Spinner'

/**
 * A folder's first scan, stage by stage (v1 Empty.tsx's panel): the current
 * stage with its counter and percentage over a progress rail, then the five
 * stages, done ones checked, the current one spinning. `scan` null means the
 * job has not reported yet ("Starting…").
 */
export function FolderScanChecklist({ scan }: { scan: ScanProgress | null }) {
  const active = scan?.stage ?? null
  const activeIdx = active ? STAGE_ORDER.indexOf(active) : -1
  const total = scan?.total ?? 0
  const done = scan?.done ?? 0
  const pct = total > 0 ? Math.round((done / total) * 100) : 0
  return (
    <div className="p-3.5 bg-bg-2 border border-line-1 rounded-lg text-left flex flex-col gap-2.5" data-testid="scan-checklist">
      <div className="flex items-center gap-2.5">
        {/* The stage name is the live region; the counters tick per file and
            would flood a screen reader. */}
        <span className="font-mono text-11 text-fg-2 flex-1">
          <span role="status" aria-live="polite">
            {scan ? stageLabel(scan.stage) : 'Starting…'}
          </span>
          {scan && total > 0 && `: ${done.toLocaleString('en-US')} / ${total.toLocaleString('en-US')}`}
        </span>
        {total > 0 && <span className="font-mono text-11 text-fg-1">{pct}%</span>}
      </div>
      <ProgressRail className="h-1" label="Scan progress" value={total > 0 ? done / total : undefined} />
      <ol className="list-none m-0 p-0 flex flex-col gap-1">
        {STAGE_ORDER.map((stage, i) => {
          const isDone = activeIdx >= 0 && i < activeIdx
          const isActive = stage === active
          const tone = isDone ? 'text-pick' : isActive ? 'text-fg-0' : 'text-fg-3'
          const status = isDone ? 'done' : isActive ? 'in progress' : 'pending'
          return (
            <li key={stage} className="flex items-center gap-2 text-11" aria-current={isActive ? 'step' : undefined} data-stage={stage} data-status={status}>
              <span className={`w-3.5 h-3.5 flex items-center justify-center ${tone}`} aria-hidden="true">
                {isDone ? <Icon name="check" /> : isActive ? <Spinner small decorative /> : '·'}
              </span>
              <span className={`flex-1 ${isDone ? 'text-fg-2' : isActive ? 'text-fg-0' : 'text-fg-3'}`}>
                {stageLabel(stage)}
                <span className="sr-only"> ({status})</span>
              </span>
              {isActive && total > 0 && (
                <span className="font-mono text-fg-3 text-11" aria-hidden="true">
                  {done.toLocaleString('en-US')} / {total.toLocaleString('en-US')}
                </span>
              )}
            </li>
          )
        })}
      </ol>
    </div>
  )
}
