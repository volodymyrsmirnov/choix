import { formatQuality, zoomLabel } from '../format'
import { Button } from './Button'
import { FocusChip, FocusChipRule } from './FocusChip'
import { FOCUS_RING } from './focusRing'
import { Icon } from './Icon'

/** The hero photo's rating, ahead of the zoom control (v1 quality-hero). */
export function FocusQuality({ value }: { value: number }) {
  return (
    <div
      role="img"
      aria-label={`Quality ${formatQuality(value)} of 10`}
      data-testid="quality-hero"
      title="Technical quality (sharpness, noise), 0–10. Advisory; compare within a cluster."
      className="flex items-center gap-1.5 h-[34px] px-2.5 rounded-md border border-chip-line bg-chip backdrop-blur-md font-mono text-11"
    >
      <span className="text-fg-2">Quality</span>
      <span className="text-fg-0 tabular-nums text-12 font-medium">{formatQuality(value)}</span>
    </div>
  )
}

/** − 100% + (v1). Hidden by the caller for videos. */
export function FocusZoom({
  zoom,
  min,
  max,
  onStep,
}: {
  zoom: number
  min: number
  max: number
  onStep: (dir: 1 | -1) => void
}) {
  return (
    <FocusChip className="h-[34px] px-2 gap-1 font-mono">
      <Button
        variant="ghost"
        className="h-[22px] px-1.5"
        onClick={() => onStep(-1)}
        disabled={zoom <= min}
        title="Zoom out (−)"
        aria-label="Zoom out"
        aria-keyshortcuts="-"
      >
        −
      </Button>
      <span className="text-11 text-fg-0 min-w-[40px] text-center tabular-nums" data-testid="zoom-level">
        {zoomLabel(zoom)}
      </span>
      <Button
        variant="ghost"
        className="h-[22px] px-1.5"
        onClick={() => onStep(1)}
        disabled={zoom >= max}
        title="Zoom in (+)"
        aria-label="Zoom in"
        aria-keyshortcuts="+"
      >
        +
      </Button>
    </FocusChip>
  )
}

/** Opens and closes the Info panel (v1 Info toggle). */
export function FocusInfoToggle({ open, controls, onToggle }: { open: boolean; controls: string; onToggle: () => void }) {
  return (
    <Button
      variant={open ? 'default' : 'ghost'}
      icon="panel"
      kbd="I"
      onClick={onToggle}
      title="Toggle info panel (I)"
      aria-pressed={open}
      aria-controls={open ? controls : undefined}
      aria-keyshortcuts="I"
      className={`h-[34px] backdrop-blur-md ${open ? '' : 'bg-chip'}`}
    >
      Info
    </Button>
  )
}

/**
 * The end-of-cluster cue (v1): every member is marked. The pill leads to the
 * next cluster, or back to the folder after the last one. The wrapper is a
 * live region that stays mounted (so the pill is announced) and lets clicks
 * through around the pill.
 */
export function FocusDonePill({
  show,
  last,
  lifted,
  besideInfo,
  onClick,
}: {
  show: boolean
  /** No next cluster: the pill goes back to the folder. */
  last: boolean
  /** Clear of native video controls along the bottom edge. */
  lifted: boolean
  /** Stop short of the open Info panel. */
  besideInfo: boolean
  onClick: () => void
}) {
  return (
    <div
      className={`absolute left-3 flex justify-center pointer-events-none ${besideInfo ? 'right-[304px]' : 'right-3'} ${lifted ? 'bottom-20' : 'bottom-3'}`}
      role="status"
      aria-live="polite"
    >
      {show && (
        <button
          type="button"
          data-testid="cluster-done"
          onClick={onClick}
          aria-keyshortcuts={last ? 'Escape' : 'Enter'}
          className={`pointer-events-auto flex items-center gap-2 px-2.5 py-1.5 rounded-md border border-chip-line bg-chip backdrop-blur-md cursor-pointer font-mono text-11 text-fg-0 hover:border-chip-line-hover ${FOCUS_RING}`}
        >
          <Icon name="check" size={12} className="text-pick" />
          <span>{last ? 'Last cluster reviewed' : 'Cluster reviewed'}</span>
          <FocusChipRule />
          <span className="kbd" aria-hidden="true">
            {last ? 'Esc' : '↩'}
          </span>
          <span className="text-fg-1">{last ? 'Back to folder' : 'Next cluster'}</span>
        </button>
      )}
    </div>
  )
}
