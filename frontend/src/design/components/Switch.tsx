import { FOCUS_RING } from './focusRing'

/**
 * An on/off switch (v1 Settings' Toggle). Name it with `label` or
 * `labelledBy`; the visible "On" / "Off" beside it is the caller's.
 */
export function Switch({
  checked,
  onChange,
  id,
  label,
  labelledBy,
  describedBy,
  disabled,
  testId,
  force,
}: {
  checked: boolean
  onChange: (checked: boolean) => void
  id?: string
  label?: string
  labelledBy?: string
  describedBy?: string
  disabled?: boolean
  testId?: string
  /** Prototype only: `hover`, `focus`, `active` rendered as data-force. */
  force?: string
}) {
  const track = checked
    ? 'bg-pick border-pick enabled:hover:bg-pick-hover enabled:hover:border-pick-hover enabled:active:bg-pick enabled:active:border-pick-ink'
    : 'bg-bg-3 border-line-1 enabled:hover:bg-bg-4 enabled:hover:border-line-2 enabled:active:bg-bg-2 enabled:active:border-line-2'
  return (
    <button
      type="button"
      id={id}
      role="switch"
      aria-checked={checked}
      aria-label={label}
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      disabled={disabled}
      data-testid={testId}
      data-force={force}
      onClick={() => onChange(!checked)}
      className={`relative w-9 h-5 flex-shrink-0 rounded-full border transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed ${FOCUS_RING} ${track}`}
    >
      {/* The thumb sits 1 px inside the track's inner box on every side.
          Vertically it is centred (top-1/2, -translate-y-1/2), so the track's
          height cannot unbalance it. Horizontally: inner width 34 px (w-9
          less two 1 px borders) = 1 + 16 (thumb) + 16 (travel) + 1, so off is
          left-px and on slides translate-x-4. data-centered: the centred
          check holds it to the track's centre. */}
      <span
        aria-hidden="true"
        data-centered="y"
        className={`absolute left-px top-1/2 -translate-y-1/2 w-4 h-4 rounded-full transition-all ${checked ? 'translate-x-4 bg-pick-ink' : 'bg-fg-1'}`}
      />
    </button>
  )
}
