import type { FocusView, MediaState, MediaURLs, Member } from '../types'
import { FocusChipRule, FocusMark, FocusPathLabel } from './FocusChip'
import { FocusMedia } from './FocusMedia'
import { FocusPanSurface } from './FocusPanSurface'
import { FOCUS_RING } from './focusRing'
import { QualityBadge } from './QualityBadge'

/**
 * One compare pane (v1): the member's media under the shared zoom and pan,
 * its pane key (1–4), its position in the cluster, its path and mark. The
 * active pane (the one Space / X act on) has the blue edge. A click on the
 * pane, or on its label (the keyboard path), makes it active; a drag pans.
 */
export function ComparePane({
  member,
  pane,
  index,
  total,
  active,
  rating,
  qualityOn,
  underZoom,
  zoomable,
  media,
  state,
  view,
  zoomBy,
  onView,
  onActivate,
  onMediaState,
}: {
  member: Member
  /** 0-based pane position; the key cap shows pane + 1. */
  pane: number
  /** The member's index in the cluster. */
  index: number
  total: number
  active: boolean
  rating: number | undefined
  /** Keep room for a rating at the top-right corner. */
  qualityOn: boolean
  /** The stage's zoom control floats over this pane's top-right corner. */
  underZoom: boolean
  /** The stage zooms (v1: not while the active member is a video; never on a video pane). */
  zoomable: boolean
  media: MediaURLs
  state: MediaState
  view: FocusView
  zoomBy: (v: FocusView, factor: number) => FocusView
  onView: (v: FocusView) => void
  onActivate: () => void
  onMediaState: (state: 'ready' | 'error') => void
}) {
  const video = member.kind === 'video'
  const mark = member.picked ? ', picked' : member.rejected ? ', rejected' : ''
  // The label leaves room for the rating badge, or under the stage's zoom
  // control for the control itself.
  const labelWidth = underZoom ? 'max-w-[calc(100%-136px)]' : qualityOn ? 'max-w-[calc(100%-58px)]' : 'max-w-[calc(100%-16px)]'
  return (
    <FocusPanSurface
      view={view}
      zoomable={zoomable && !video}
      zoomBy={zoomBy}
      onView={onView}
      onClick={onActivate}
      active={active}
      testId="compare-pane"
      className="relative min-w-0 min-h-0 bg-bg-1 rounded-md overflow-hidden border border-line-1 transition-[border-color,box-shadow] duration-fast data-[active=true]:border-sel data-[active=true]:shadow-sel-ring"
    >
      <FocusMedia member={member} media={media} state={state} zoom={view.zoom} pan={view.pan} variant="compare" onState={onMediaState} />
      <button
        type="button"
        onClick={onActivate}
        aria-label={`Pane ${pane + 1}: photo ${index + 1} of ${total}, ${member.path}${mark}`}
        aria-current={active ? 'true' : undefined}
        aria-keyshortcuts={String(pane + 1)}
        className={`absolute top-2 left-2 z-10 flex items-center gap-2 px-2 py-1 rounded-md border-0 bg-chip backdrop-blur-md text-11 font-mono text-fg-1 cursor-pointer ${labelWidth} ${FOCUS_RING}`}
      >
        <span className="kbd flex-shrink-0 group-data-[active=true]:text-sel group-data-[active=true]:border-sel">{pane + 1}</span>
        <span className="flex-shrink-0 text-fg-0">
          {String(index + 1).padStart(2, '0')} / {String(total).padStart(2, '0')}
        </span>
        <FocusChipRule />
        <FocusPathLabel path={member.path} />
        {(member.picked || member.rejected) && (
          <span className="flex-shrink-0" data-testid="pane-mark">
            <FocusMark m={member} withText />
          </span>
        )}
      </button>
      {rating !== undefined && (
        <QualityBadge
          value={rating}
          standalone
          className={`absolute right-2 z-10 pointer-events-none ${underZoom ? 'top-[42px]' : 'top-2'}`}
        />
      )}
    </FocusPanSurface>
  )
}
