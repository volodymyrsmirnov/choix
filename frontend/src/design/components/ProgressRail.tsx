/**
 * A 3 px progress bar. `value` is a fraction 0–1; leave it out for an
 * indeterminate bar, whose fill carries data-indet="true" (a sweep, or a slow
 * pulse under reduced motion). Width comes from `className` (e.g. `w-20`).
 */
export function ProgressRail({
  value,
  label,
  valueText,
  className,
}: {
  value?: number
  label: string
  valueText?: string
  className?: string
}) {
  const known = value !== undefined
  const pct = known ? Math.min(100, Math.max(0, value * 100)) : 0
  return (
    <div
      className={`progress-rail flex-shrink-0 ${className ?? ''}`}
      data-clip="intended"
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={known ? 100 : undefined}
      aria-valuenow={known ? Math.floor(pct) : undefined}
      aria-valuetext={valueText}
    >
      <div
        className="progress-fill"
        data-indet={known ? undefined : 'true'}
        style={known ? { width: `${pct}%` } : undefined}
      />
    </div>
  )
}
