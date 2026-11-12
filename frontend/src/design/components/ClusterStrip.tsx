import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { baseName } from '../format'
import { scrollBehavior } from '../motion'
import type { MediaURLs, Member } from '../types'
import { Thumb } from './Thumb'

// Strip geometry (v1 Library). The overflow maths derives the "+N" count from
// these instead of measuring every thumbnail, so the strip's inline gap and
// padding use the same constants.
export const STRIP_THUMB = 116
const THUMB_GAP = 8
const STRIP_PAD_X = 2
const THUMB_STEP = STRIP_THUMB + THUMB_GAP
const FADE_PX = 40

/** The `data-measured-by` id of everything this strip computes from layout
 *  (registered in measured.ts). */
export const CLUSTER_STRIP_MEASURE = 'cluster-strip'



// One ResizeObserver shared by every strip on the page: with ~200 clusters a
// per-strip observer would be 200 observers for what is one window resize.
const stripMeasurers = new WeakMap<Element, () => void>()
let stripObserver: ResizeObserver | null = null
function observeStrip(el: Element, measure: () => void): () => void {
  if (typeof ResizeObserver === 'undefined') {
    measure()
    return () => {}
  }
  stripObserver ??= new ResizeObserver((entries) => {
    for (const entry of entries) stripMeasurers.get(entry.target)?.()
  })
  stripMeasurers.set(el, measure)
  // observe() always delivers an initial entry, which doubles as the first
  // measurement: no layout read during React's commit.
  stripObserver.observe(el)
  return () => {
    stripObserver?.unobserve(el)
    stripMeasurers.delete(el)
  }
}

type StripOverflow = { start: boolean; hidden: number }
const NO_OVERFLOW: StripOverflow = { start: false, hidden: 0 }

function fadeMask(start: boolean, end: boolean): string | undefined {
  if (!start && !end) return undefined
  const a = start ? `transparent 0, var(--mask-opaque) ${FADE_PX}px` : 'var(--mask-opaque) 0'
  const b = end ? `var(--mask-opaque) calc(100% - ${FADE_PX}px), transparent 100%` : 'var(--mask-opaque) 100%'
  return `linear-gradient(to right, ${a}, ${b})`
}

/** Thumbs whose right edge is inside the viewport; everything after them
 *  (including the one cut off under the fade) counts as not yet seen. */
const seenCount = (el: HTMLElement) => Math.floor((el.scrollLeft + el.clientWidth - STRIP_PAD_X + THUMB_GAP) / THUMB_STEP)

/**
 * A cluster's horizontally scrolling thumbnail row (v1 Library). The
 * scrollbar stays hidden, so overflow is signalled with an edge fade (a mask,
 * which matches any background and never intercepts clicks) and a "+N" pill
 * that pages the row forward.
 *
 * The overflow is measured (a shared ResizeObserver and the scroll position),
 * so for a fixed window size it is deterministic. The pill sits in its own
 * column after the row rather than over it, so it never covers a thumbnail;
 * growing that column only ever hides more thumbnails, so the measurement
 * settles in one extra frame. Everything written from the measurement
 * carries `data-measured` / `data-measured-by="cluster-strip"`.
 *
 * The DOM order is the visual order: the thumbnails, then the pill. Every
 * thumbnail is a Tab stop (v1), and tabbing scrolls the row with focus; by
 * the time Tab has passed the last thumbnail the row is at its end and the
 * pill is gone. So the pill is a pointer-only paging control (v1 in
 * practice): out of the Tab order and hidden from assistive technology,
 * marked data-pointer-only with the reason, which the focus check verifies.
 */
export const ClusterStrip = memo(function ClusterStrip({
  clusterId,
  members,
  media,
  qualityOn,
  openDisabled,
  onOpen,
}: {
  clusterId: number
  members: Member[]
  media: MediaURLs
  /** Show the advisory rating badges (the quality_rating setting). */
  qualityOn: boolean
  /** Opening Focus is unavailable (the folder's drive is offline). */
  openDisabled?: boolean
  /** Stable across renders (it is called with the cluster's id). */
  onOpen: (clusterId: number, fileId: number) => void
}) {
  const ref = useRef<HTMLDivElement | null>(null)
  const [ov, setOv] = useState<StripOverflow>(NO_OVERFLOW)
  const count = members.length

  const measure = useCallback(() => {
    const el = ref.current
    if (!el) return
    const hidden = Math.max(0, count - seenCount(el))
    const start = el.scrollLeft > STRIP_PAD_X
    // Bail out unchanged so scrolling within a thumb, or a resize that does
    // not move an edge, re-renders nothing.
    setOv((p) => (p.start === start && p.hidden === hidden ? p : { start, hidden }))
  }, [count])

  useEffect(() => {
    const el = ref.current
    if (!el) return
    let raf = 0
    // At most one read per frame however fast the trackpad fires scroll.
    const onScroll = () => {
      if (raf) return
      raf = requestAnimationFrame(() => {
        raf = 0
        measure()
      })
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    // Re-observing when `measure` changes (member count changed) also buys a
    // fresh initial measurement.
    const unobserve = observeStrip(el, measure)
    return () => {
      el.removeEventListener('scroll', onScroll)
      if (raf) cancelAnimationFrame(raf)
      unobserve()
    }
  }, [measure])

  const showMore = () => {
    const el = ref.current
    if (!el) return
    // Land the first unseen thumb just past the left fade, leaving a sliver
    // of the previous one as context for where the row came from.
    el.scrollTo({ left: Math.max(0, seenCount(el) * THUMB_STEP - FADE_PX), behavior: scrollBehavior() })
  }

  // Thumbs are memoised so an overflow update (the pill count changing as the
  // row scrolls) re-renders the strip shell, not every thumbnail.
  const thumbs = useMemo(
    () =>
      members.map((m) => (
        <Thumb
          key={m.file_id}
          src={media.thumb(m, 'thumb')}
          alt={baseName(m.path)}
          title={m.path}
          fileId={m.file_id}
          state={m.picked ? 'picked' : m.rejected ? 'rejected' : undefined}
          video={m.kind === 'video'}
          quality={qualityOn && m.kind === 'photo' ? m.quality : undefined}
          size={STRIP_THUMB}
          testId="thumb"
          disabled={openDisabled}
          onClick={() => onOpen(clusterId, m.file_id)}
        />
      )),
    [members, media, qualityOn, openDisabled, clusterId, onOpen],
  )

  const mask = fadeMask(ov.start, ov.hidden > 0)
  return (
    <div className="flex items-center min-w-0">
      <div
        ref={ref}
        className="flex flex-1 min-w-0 overflow-x-auto py-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
        data-scroll
        data-measured="mask-image -webkit-mask-image"
        data-measured-by={CLUSTER_STRIP_MEASURE}
        style={{ gap: THUMB_GAP, paddingLeft: STRIP_PAD_X, paddingRight: STRIP_PAD_X, maskImage: mask, WebkitMaskImage: mask }}
      >
        {thumbs}
      </div>
      {/* A fixed width that holds "+9999": a pill as wide as its text would
          move the strip's edge, and with it the measured count, by glyph
          advances, which differ between platforms' font rasterisers. */}
      {ov.hidden > 0 && (
        <button
          type="button"
          onClick={showMore}
          tabIndex={-1}
          aria-hidden="true"
          data-pointer-only="paging: every thumbnail is reachable by Tab"
          title={`Show more photos in this cluster (${ov.hidden} more)`}
          className={`ml-2 flex-shrink-0 h-6 w-9 px-0 rounded-full border border-line-2 bg-bg-3 text-fg-0 font-mono text-11 shadow-pop cursor-pointer hover:bg-bg-4 active:bg-bg-2`}
          data-testid="strip-more"
        >
          +{ov.hidden}
        </button>
      )}
    </div>
  )
})
