import { useEffect, useRef } from 'react'
import { qualityLabel } from '../format'
import { scrollBehavior } from '../motion'
import type { MediaURLs, Member } from '../types'
import { FocusMark } from './FocusChip'
import { QualityBadge } from './QualityBadge'
import { VideoMark } from './Thumb'

/**
 * The cluster's members down the left of Focus (v1 rail). A click shows the
 * member, or while comparing adds or removes it as a pane. The thumbnail's
 * look says which is current (the current item's blue look: the active
 * photo, or while comparing every photo in the compare set), picked or
 * rejected (a current mark keeps its edge inside the blue ring); the row
 * under it carries the position and the mark glyph.
 * aria-current marks the photo Space / X act on. The active thumbnail is
 * kept in view (v1): scrolled to the nearest edge when it changes, smoothly
 * unless the user asked for reduced motion, and when the rail first shows.
 */
export function FocusRail({
  members,
  activeIndex,
  comparing,
  compareIdx,
  rating,
  media,
  onItem,
}: {
  members: Member[]
  activeIndex: number
  comparing: boolean
  compareIdx: number[]
  /** The rating to show for a member, or undefined. */
  rating: (m: Member) => number | undefined
  media: MediaURLs
  onItem: (index: number) => void
}) {
  const rail = useRef<HTMLElement | null>(null)
  const shown = useRef(false)
  useEffect(() => {
    const el = rail.current
    // The whole item (thumbnail, position and mark) is kept in view.
    const target = el?.querySelector<HTMLElement>(`[data-rail-index="${activeIndex}"]`)?.closest<HTMLElement>('[data-rail-item]')
    if (el && target) {
      // v1's scrollIntoView({ block: 'nearest' }), but on the rail alone: the
      // window and the stage never move. The first time without animation:
      // the rail opens on the active photo.
      const r = el.getBoundingClientRect()
      const t = target.getBoundingClientRect()
      const pad = 10
      const delta = t.top < r.top + pad ? t.top - r.top - pad : t.bottom > r.bottom - pad ? t.bottom - r.bottom + pad : 0
      if (delta !== 0) el.scrollTo({ top: el.scrollTop + delta, behavior: shown.current ? scrollBehavior() : 'auto' })
    }
    shown.current = true
  }, [activeIndex])
  return (
    <aside
      ref={rail}
      className="w-[132px] flex-shrink-0 bg-bg-1 border-r border-line-1 overflow-y-auto py-2.5 px-2 flex flex-col gap-1.5"
      data-testid="rail"
      data-scroll
      data-measured=""
      data-measured-by="focus-rail-scroll"
      aria-label={comparing ? 'Cluster photos, select up to 4 to compare' : 'Cluster photos'}
    >
      {members.map((m, i) => {
        const inCompare = comparing && compareIdx.includes(i)
        // The current photo, or while comparing the compare set, is the
        // current item's look; picked and rejected stay marks around it.
        const current = comparing ? inCompare : i === activeIndex
        const state = m.picked ? 'picked' : m.rejected ? 'rejected' : undefined
        const isCurrent = i === activeIndex && (!comparing || compareIdx.includes(i))
        const q = rating(m)
        return (
          // The thumbnail is the button, so keyboard focus shows on it: the
          // focus ring on any photo, the deeper current ring on a current
          // one (base.css). The position and mark sit under it.
          <div key={m.file_id} className="flex-shrink-0 flex flex-col gap-[3px]" data-rail-item>
            <button
              type="button"
              onClick={() => onItem(i)}
              data-testid="rail-thumb"
              data-file-id={m.file_id}
              data-rail-index={i}
              data-state={state}
              data-current={current ? 'true' : undefined}
              title={m.path}
              aria-label={`${m.kind === 'video' ? 'Video' : 'Photo'} ${i + 1}: ${m.path}${q !== undefined ? `, ${qualityLabel(q)}` : ''}${m.picked ? ' (picked)' : m.rejected ? ' (rejected)' : ''}`}
              aria-pressed={comparing ? inCompare : undefined}
              aria-current={isCurrent ? 'true' : undefined}
              className="thumb thumb-btn w-full aspect-square"
            >
              <img src={media.thumb(m, 'thumb')} alt="" aria-hidden="true" draggable={false} />
              {m.kind === 'video' && <VideoMark />}
              {q !== undefined && <QualityBadge value={q} className="absolute top-1 right-1 pointer-events-none" />}
            </button>
            <span className="flex items-center justify-between h-[13px] px-0.5 text-11 leading-none font-mono" aria-hidden="true">
              <span className={i === activeIndex ? 'text-fg-0' : 'text-fg-3'}>{String(i + 1).padStart(2, '0')}</span>
              <FocusMark m={m} />
            </span>
          </div>
        )
      })}
    </aside>
  )
}
