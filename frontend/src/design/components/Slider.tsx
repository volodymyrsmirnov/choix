import type { CSSProperties } from 'react'

/**
 * A range slider with its value read out beside it (v1 Settings' visual
 * similarity slider: a pick-accented range, a mono readout 40 px wide). The
 * look lives in base.css `.slider`; the filled part of the track follows
 * `value` through the `--slider-fill` custom property. `label` names the
 * control; `format` renders the readout and, unless `valueText` is given, the announced value.
 */
export function Slider({
  id,
  label,
  value,
  min,
  max,
  step,
  disabled,
  force,
  onChange,
  format = String,
  valueText,
  describedBy,
  testId,
  className,
}: {
  id: string
  label: string
  value: number
  min: number
  max: number
  step: number
  disabled?: boolean
  /** Prototype only: `hover`, `focus`, `active` rendered as data-force. */
  force?: string
  onChange: (value: number) => void
  format?: (value: number) => string
  /** What a screen reader announces for the value; defaults to `format`. */
  valueText?: (value: number) => string
  describedBy?: string
  testId?: string
  /** Classes for the row (width, margins). */
  className?: string
}) {
  const span = max - min
  const pct = span > 0 ? Math.min(100, Math.max(0, ((value - min) / span) * 100)) : 0
  const shown = format(value)
  return (
    <div className={`flex items-center gap-3.5 ${className ?? ''}`}>
      <input
        id={id}
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        disabled={disabled}
        aria-label={label}
        aria-describedby={describedBy}
        aria-valuetext={valueText ? valueText(value) : shown}
        data-force={force}
        data-testid={testId}
        onChange={(e) => onChange(Number(e.target.value))}
        className="slider flex-1 min-w-0"
        style={{ '--slider-fill': `${pct}%` } as CSSProperties}
      />
      {/* The range's aria-valuetext already announces the value. */}
      <span
        aria-hidden="true"
        className={`font-mono text-12 w-10 text-right text-fg-0 tabular-nums ${disabled ? 'opacity-40' : ''}`}
      >
        {shown}
      </span>
    </div>
  )
}
