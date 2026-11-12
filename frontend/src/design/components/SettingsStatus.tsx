import { noOrphan } from '../format'
import { ProgressRail } from './ProgressRail'

/**
 * A live status line under a Settings field (v1: the quality status under
 * its switch), with an optional progress rail. The region is always present
 * so screen readers announce its changes; while empty it is out of the
 * flow (sr-only), so the field's column adds no gap for it. No margin of its
 * own: the field's column spaces it.
 */
export function SettingsStatus({
  text,
  tone = 'neutral',
  progress,
  testId,
}: {
  text: string
  tone?: 'neutral' | 'reject'
  progress?: { value: number; label: string; valueText: string }
  testId?: string
}) {
  return (
    <div className={text ? 'flex flex-col gap-1.5 max-w-[480px]' : 'sr-only'}>
      <p
        role="status"
        data-testid={testId}
        className={`m-0 text-11 leading-[1.5] ${tone === 'reject' ? 'text-reject' : 'text-fg-2'}`}
      >
        {text ? noOrphan(text) : null}
      </p>
      {text && progress && (
        <ProgressRail className="w-full" label={progress.label} value={progress.value} valueText={progress.valueText} />
      )}
    </div>
  )
}
