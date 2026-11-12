import { formatQuality } from '../format'

/**
 * The advisory 0–10 quality rating. `standalone` gives it its own accessible
 * name (Focus hero, Compare panes); inside a labelled button the badge is
 * hidden from assistive tech and the button's name carries the rating. The
 * value is always text: colour is never the only signal.
 */
export function QualityBadge({ value, standalone, className }: { value: number; standalone?: boolean; className?: string }) {
  const a11y = standalone
    ? { role: 'img', 'aria-label': `Quality ${formatQuality(value)} of 10` }
    : { 'aria-hidden': true as const }
  return (
    <span
      {...a11y}
      data-testid="quality-badge"
      title={`Technical quality ${formatQuality(value)} of 10 (sharpness, noise). Compare within a cluster.`}
      className={`inline-flex items-center justify-center px-1 h-[18px] min-w-[26px] rounded-sm bg-scrim font-mono tabular-nums text-11 leading-none font-medium text-fg-0 ${className ?? ''}`}
    >
      {formatQuality(value)}
    </span>
  )
}
