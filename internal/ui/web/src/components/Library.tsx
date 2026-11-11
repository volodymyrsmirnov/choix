import React, { memo, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import {
  AppBar,
  SearchIcon, FilterIcon, SortIcon, RefreshIcon, ChevronDown, InfoIcon,
  CheckIcon, XIcon, QualityBadge, qualityLabel } from './primitives'
import type { LibraryResponse, Cluster, Member } from '../api'
import { postRecluster, thumbURL } from '../api'
import { toast } from './Toaster'
import { isLetter, keyboardFocusedControl } from '../keys'
import { shortcutsOpen, setShortcutsOpen } from './Shortcuts'

export type SortKey = 'time-asc' | 'time-desc'

// The ⌘K handler accepts Ctrl as well, but the hint should name the key the
// user actually has. Same sniff as Focus's undo hint.
const IS_MAC = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.userAgent)
const MOD_LABEL = IS_MAC ? '⌘' : 'Ctrl'

function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

type MenuOption<K extends string> = { k: K; label: string }

// Menu button following the WAI-ARIA menu-button pattern with radio items:
// focus moves into the menu on open, ↑/↓/Home/End move between items,
// Enter/Space select, Escape closes and returns focus to the trigger, and
// Tab or any other focus move out of the menu closes it.
//
// ↑/↓ on the *closed* trigger deliberately do not open the menu (the APG
// suggests they could): after picking a camera the trigger keeps focus, and
// the user's next ↓ must keep moving the active cluster as it always has.
function Dropdown<K extends string>({ testid, icon, srPrefix, label, menuLabel, options, value, onSelect }: {
  testid: string
  icon: React.ReactNode
  srPrefix: string
  label: string
  menuLabel: string
  options: MenuOption<K>[]
  value: K
  onSelect: (k: K) => void
}) {
  const [open, setOpen] = useState(false)
  const wrapRef = useRef<HTMLDivElement | null>(null)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const menuRef = useRef<HTMLDivElement | null>(null)
  // Set by a Tab keydown inside the menu so the focusout that follows closes
  // the menu even when Shift+Tab lands on our own trigger (which a mouse
  // click on the trigger must not do — the click itself toggles it shut).
  const tabbingRef = useRef(false)
  const menuID = useId()

  const items = () => Array.from(menuRef.current?.querySelectorAll<HTMLElement>('[role="menuitemradio"]') ?? [])

  // Click-outside closes. Only listening while open keeps an idle Library
  // free of document-wide mousedown work.
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false)
    }
    window.addEventListener('mousedown', onDown)
    return () => window.removeEventListener('mousedown', onDown)
  }, [open])

  // Move focus onto the checked item (or the first) as soon as the menu opens.
  useEffect(() => {
    if (!open) return
    tabbingRef.current = false
    const list = items()
    ;(list.find(el => el.getAttribute('aria-checked') === 'true') ?? list[0])?.focus()
  }, [open])

  const choose = (k: K, refocus: boolean) => {
    onSelect(k)
    setOpen(false)
    // Keyboard users land back on the trigger; after a mouse pick we leave
    // focus alone so Enter / F keep acting on the active cluster.
    if (refocus) triggerRef.current?.focus()
  }

  const onMenuKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const list = items()
    const n = list.length
    if (n === 0) return
    const i = list.indexOf(document.activeElement as HTMLElement)
    switch (e.key) {
      case 'ArrowDown': list[(i + 1 + n) % n]?.focus(); break
      case 'ArrowUp': list[i <= 0 ? n - 1 : i - 1]?.focus(); break
      case 'Home': list[0]?.focus(); break
      case 'End': list[n - 1]?.focus(); break
      case 'Escape':
        setOpen(false)
        triggerRef.current?.focus()
        break
      case 'Enter':
      case ' ': {
        const opt = options[i]
        if (opt) choose(opt.k, true)
        break
      }
      case 'Tab':
        // Let the browser move focus; onMenuBlur closes the menu.
        tabbingRef.current = true
        return
      default:
        return
    }
    // The menu owns these keys: preventDefault stops Enter/Space from also
    // clicking the item, stopPropagation keeps the Library's window-level
    // ↑/↓/Enter navigation from moving or opening a cluster behind the menu.
    e.preventDefault()
    e.stopPropagation()
  }

  const onMenuBlur = (e: React.FocusEvent<HTMLDivElement>) => {
    const next = e.relatedTarget as Node | null
    if (tabbingRef.current) {
      tabbingRef.current = false
      if (!next || !menuRef.current?.contains(next)) setOpen(false)
      return
    }
    // relatedTarget is null when the window loses focus or (Safari) a click
    // lands on a non-focusable spot; the mousedown listener covers clicks.
    if (next && !wrapRef.current?.contains(next)) setOpen(false)
  }

  return (
    <div ref={wrapRef} className="relative">
      <button ref={triggerRef} type="button" className="btn btn-ghost"
              onClick={() => setOpen(v => !v)}
              onKeyDown={e => {
                // Shift+Tab from the menu can leave focus on an open trigger.
                if (open && e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); setOpen(false) }
              }}
              aria-haspopup="menu"
              aria-expanded={open}
              aria-controls={open ? menuID : undefined}
              data-testid={testid}>
        {icon}<span className="sr-only">{srPrefix} </span>{label} <ChevronDown />
      </button>
      {open && (
        <div ref={menuRef} id={menuID} role="menu" aria-label={menuLabel}
             onKeyDown={onMenuKeyDown} onBlur={onMenuBlur}
             className="absolute z-10 mt-1 left-0 min-w-[200px] rounded-md border border-line-2 bg-bg-2 shadow-pop py-1">
          {options.map(opt => {
            const checked = opt.k === value
            return (
              <button key={opt.k} type="button" role="menuitemradio" aria-checked={checked} tabIndex={-1}
                      onClick={() => choose(opt.k, false)}
                      className={`w-full flex items-center gap-2 text-left px-3 py-1.5 text-xs outline-none hover:bg-bg-3 focus:bg-bg-4 focus:text-fg-0 ${checked ? 'text-fg-0' : 'text-fg-1'}`}>
                {/* The check (not just a lighter text colour) marks the current value. */}
                <span className="w-3 flex-shrink-0" aria-hidden="true">{checked && <CheckIcon size={12} />}</span>
                {opt.label}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}

const SORT_OPTIONS: MenuOption<SortKey>[] = [
  { k: 'time-asc', label: 'Capture time — earliest first' },
  { k: 'time-desc', label: 'Capture time — latest first' },
]

// The Library's filter/sort view. App keeps the last value across Library
// unmounts (Focus and Settings replace the Library entirely) so returning
// lands on the same filtered, sorted list the saved scroll offset and active
// cluster were recorded against.
export type LibraryFilters = { query: string; device: string; sort: SortKey }

export const DEFAULT_LIBRARY_FILTERS: LibraryFilters = { query: '', device: '', sort: 'time-asc' }

const Subnav: React.FC<{
  summary: { clusters: number; total: number; visible: number }
  onRecluster: () => void
  busy: boolean
  query: string
  onQuery: (v: string) => void
  devices: string[]
  device: string
  onDevice: (v: string) => void
  sort: SortKey
  onSort: (v: SortKey) => void
  searchRef: React.RefObject<HTMLInputElement>
}> = ({ summary, onRecluster, busy, query, onQuery, devices, device, onDevice, sort, onSort, searchRef }) => {
  const deviceOptions = useMemo<MenuOption<string>[]>(
    () => [{ k: '', label: 'All cameras' }, ...devices.map(d => ({ k: d, label: d }))],
    [devices])

  return (
    <div className="flex items-center gap-2 h-10 px-3.5 border-b border-line-1 bg-bg-1">
      <label className="flex items-center gap-1.5 h-[26px] px-2 bg-bg-2 border border-line-1 rounded-md min-w-[240px] focus-within:border-pick">
        <SearchIcon className="text-fg-3" />
        <input
          ref={searchRef}
          type="text"
          value={query}
          onChange={e => onQuery(e.target.value)}
          placeholder="Filter clusters"
          aria-label="Filter clusters by camera, time or file name"
          aria-keyshortcuts={IS_MAC ? 'Meta+K' : 'Control+K'}
          className="bg-transparent border-0 outline-none text-xs text-fg-0 placeholder:text-fg-3 flex-1 min-w-0"
          data-testid="filter-input"
        />
        <span className="kbd text-[9px]" aria-hidden="true">{MOD_LABEL} K</span>
      </label>

      <Dropdown
        testid="device-filter"
        icon={<FilterIcon />}
        srPrefix="Camera:"
        label={device === '' ? 'All cameras' : device}
        menuLabel="Camera"
        options={deviceOptions}
        value={device}
        onSelect={onDevice}
      />

      <Dropdown
        testid="sort-toggle"
        icon={<SortIcon />}
        srPrefix="Sort:"
        label={sort === 'time-asc' ? 'Capture time ↑' : 'Capture time ↓'}
        menuLabel="Sort clusters"
        options={SORT_OPTIONS}
        value={sort}
        onSelect={onSort}
      />

      <span className="flex-1" />
      <span className="font-mono text-[11px] text-fg-2">
        {summary.visible !== summary.clusters
          ? `${summary.visible} of ${summary.clusters} clusters`
          : `${summary.clusters} clusters`}
        <span className="text-fg-3"> · </span>
        {summary.total} photos
      </span>
      <div className="w-px h-[18px] bg-line-1 mx-0.5" />
      <button className="btn btn-ghost" onClick={onRecluster} disabled={busy} data-testid="recluster">
        <RefreshIcon /> {busy ? 'Re-clustering…' : 'Re-cluster'}
      </button>
    </div>
  )
}

// Only the keys that act on this screen; Pick / Reject / Compare / Zoom live
// in Focus and are listed in the full shortcuts overlay.
const HelpHint: React.FC<{ picksDir: string }> = ({ picksDir }) => (
  <div className="flex items-center gap-3.5 px-3.5 py-2 border-b border-line-1 bg-bg-1 text-[11px] text-fg-2">
    <InfoIcon className="text-fg-3" />
    <span className="inline-flex items-center gap-1"><span className="kbd">↑</span><span className="kbd">↓</span> Navigate clusters</span>
    <span className="inline-flex items-center gap-1"><span className="kbd">F</span> / <span className="kbd">↵</span> Open Focus</span>
    <span className="inline-flex items-center gap-1"><span className="kbd">{MOD_LABEL} K</span> Search</span>
    <button type="button"
            onClick={() => setShortcutsOpen(true)}
            aria-haspopup="dialog"
            aria-keyshortcuts="?"
            className="inline-flex items-center gap-1 bg-transparent border-0 p-0 cursor-pointer text-fg-2 hover:text-fg-0 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-pick focus-visible:ring-offset-2 focus-visible:ring-offset-bg-1"
            data-testid="hint-shortcuts">
      <span className="kbd" aria-hidden="true">?</span> All shortcuts
    </button>
    <span className="flex-1" />
    <span>Picks copy to <span className="font-mono text-fg-1">{picksDir}</span></span>
  </div>
)

// Strip geometry. The overflow maths below derives the "+N" count from these
// instead of measuring every thumbnail, so the strip's inline gap/padding use
// the same constants.
const THUMB_SIZE = 116
const THUMB_GAP = 8
const STRIP_PAD_X = 2
const THUMB_STEP = THUMB_SIZE + THUMB_GAP
const FADE_PX = 40

function baseName(p: string): string {
  const i = p.lastIndexOf('/')
  return i >= 0 ? p.slice(i + 1) : p
}

const Thumb: React.FC<{ m: Member; onOpen: () => void; size?: number }> = ({ m, onOpen, size = THUMB_SIZE }) => {
  const state = m.picked ? 'picked' : (m.rejected ? 'rejected' : 'default')
  // The button's aria-label is the accessible name; the img is hidden from
  // assistive tech so the full-path tooltip (kept for mouse users) isn't read
  // out a second time as a description.
  // Videos are never rated; they keep the top-right corner for their icon.
  const quality = m.kind === 'photo' && typeof m.quality === 'number' ? m.quality : undefined
  const name = baseName(m.path)
    + (m.kind === 'video' ? ', video' : '')
    + (quality !== undefined ? `, ${qualityLabel(quality)}` : '')
    + (m.picked ? ', picked' : m.rejected ? ', rejected' : '')
  return (
    <button onClick={onOpen}
            className="thumb flex-shrink-0 bg-transparent p-0 cursor-pointer outline-none focus:outline-none focus-visible:ring-2 focus-visible:ring-pick focus-visible:ring-offset-2 focus-visible:ring-offset-bg-1"
            data-state={state}
            data-file-id={m.file_id}
            data-testid="thumb"
            aria-label={name}
            style={{ width: size, height: size }}>
      <img src={thumbURL(m)} alt="" aria-hidden="true" title={m.path} loading="lazy" />
      {m.kind === 'video' && (
        <div className="absolute top-1 right-1 w-5 h-5 rounded-sm flex items-center justify-center pointer-events-none"
             style={{ background: 'oklch(0 0 0 / 0.65)' }}
             aria-hidden="true">
          <svg width="10" height="10" viewBox="0 0 10 10" fill="currentColor" className="text-fg-0 ml-0.5">
            <path d="M2 1.5l7 3.5-7 3.5z" />
          </svg>
        </div>
      )}
      {/* Advisory quality rating. Absolutely positioned, so a rating that
          arrives after the thumbnail rendered moves nothing. */}
      {quality !== undefined && (
        <QualityBadge value={quality} className="absolute top-1 right-1 pointer-events-none" />
      )}
      {/* Badges ignore the pointer so hovering them still shows the img's path tooltip. */}
      {m.picked && (
        <div className="absolute bottom-1.5 left-1.5 px-1.5 py-0.5 rounded-sm text-[11px] font-semibold flex items-center gap-1 bg-pick text-pick-ink pointer-events-none"
             aria-hidden="true">
          <CheckIcon size={10} /> Pick
        </div>
      )}
      {m.rejected && (
        <div className="absolute bottom-1.5 left-1.5 px-1.5 py-0.5 rounded-sm text-[11px] font-semibold pointer-events-none"
             style={{ background: 'oklch(0.66 0.20 25 / 0.95)', color: 'oklch(0.16 0.06 25)' }}
             aria-hidden="true">
          Reject
        </div>
      )}
    </button>
  )
}

// One ResizeObserver shared by every strip on the page: with ~200 clusters a
// per-strip observer would be 200 observers for what is one window resize.
const stripMeasurers = new WeakMap<Element, () => void>()
let stripObserver: ResizeObserver | null = null
function observeStrip(el: Element, measure: () => void): () => void {
  if (typeof ResizeObserver === 'undefined') {
    measure()
    return () => {}
  }
  stripObserver ??= new ResizeObserver(entries => {
    for (const entry of entries) stripMeasurers.get(entry.target)?.()
  })
  stripMeasurers.set(el, measure)
  // observe() always delivers an initial entry, which doubles as the first
  // measurement — no layout read during React's commit.
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
  const a = start ? `transparent 0, #000 ${FADE_PX}px` : '#000 0'
  const b = end ? `#000 calc(100% - ${FADE_PX}px), transparent 100%` : '#000 100%'
  return `linear-gradient(to right, ${a}, ${b})`
}

// A cluster's horizontally scrolling thumbnail row. The scrollbar stays
// hidden, so overflow is signalled with an edge fade (a mask, which matches
// any section background and never intercepts clicks) and a "+N" pill that
// pages the row forward.
const ClusterStrip: React.FC<{
  clusterID: number
  members: Member[]
  onOpenCluster: (clusterID: number, fileID?: number) => void
}> = ({ clusterID, members, onOpenCluster }) => {
  const ref = useRef<HTMLDivElement | null>(null)
  const [ov, setOv] = useState<StripOverflow>(NO_OVERFLOW)
  const count = members.length

  const measure = useCallback(() => {
    const el = ref.current
    if (!el) return
    const { scrollLeft, clientWidth } = el
    // Thumbs whose right edge is inside the viewport; everything after them
    // (including the one cut off under the fade) counts as not yet seen.
    const seen = Math.floor((scrollLeft + clientWidth - STRIP_PAD_X + THUMB_GAP) / THUMB_STEP)
    const hidden = Math.max(0, count - seen)
    const start = scrollLeft > STRIP_PAD_X
    // Bail out unchanged so scrolling within a thumb, or a resize that
    // doesn't move an edge, re-renders nothing.
    setOv(p => (p.start === start && p.hidden === hidden ? p : { start, hidden }))
  }, [count])

  useEffect(() => {
    const el = ref.current
    if (!el) return
    let raf = 0
    // At most one read per frame however fast the trackpad fires scroll.
    const onScroll = () => {
      if (raf) return
      raf = requestAnimationFrame(() => { raf = 0; measure() })
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    // Re-observing when `measure` changes (member count changed) also buys
    // a fresh initial measurement.
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
    const seen = Math.floor((el.scrollLeft + el.clientWidth - STRIP_PAD_X + THUMB_GAP) / THUMB_STEP)
    // Land the first unseen thumb just past the left fade, leaving a sliver
    // of the previous one as context for where the row came from.
    el.scrollTo({ left: Math.max(0, seen * THUMB_STEP - FADE_PX), behavior: prefersReducedMotion() ? 'auto' : 'smooth' })
  }

  // Thumbs are memoised so an overflow update (the pill count changing as
  // the row scrolls) re-renders the strip shell, not every thumbnail.
  const thumbs = useMemo(() => members.map(m => (
    <Thumb key={m.file_id} m={m} onOpen={() => onOpenCluster(clusterID, m.file_id)} />
  )), [members, clusterID, onOpenCluster])

  const mask = fadeMask(ov.start, ov.hidden > 0)
  return (
    <div className="relative">
      <div ref={ref}
           className="flex overflow-x-auto py-2 [&::-webkit-scrollbar]:hidden"
           style={{ gap: THUMB_GAP, paddingLeft: STRIP_PAD_X, paddingRight: STRIP_PAD_X, scrollbarWidth: 'none', maskImage: mask, WebkitMaskImage: mask }}>
        {thumbs}
      </div>
      {ov.hidden > 0 && (
        <button type="button" onClick={showMore}
                aria-label={`Show more photos in this cluster (${ov.hidden} more)`}
                className="absolute right-1 top-1/2 -translate-y-1/2 h-6 min-w-[34px] px-2 rounded-full border border-line-2 bg-bg-3 text-fg-0 font-mono text-[11px] shadow-pop cursor-pointer hover:bg-bg-4 outline-none focus-visible:ring-2 focus-visible:ring-pick"
                data-testid="strip-more">
          +{ov.hidden}
        </button>
      )}
    </div>
  )
}

type ClusterCounts = { picked: number; rejected: number; reviewed: number }
const ZERO_COUNTS: ClusterCounts = { picked: 0, rejected: 0, reviewed: 0 }

const ClusterMeta: React.FC<{ c: Cluster; counts: ClusterCounts; onFocus: () => void; titleID: string; timeID: string }> = ({ c, counts, onFocus, titleID, timeID }) => {
  const n = c.members.length
  const done = n > 0 && counts.reviewed === n
  // Picked / rejected carry a ✓ / ✕ glyph and screen-reader text, so the
  // pick-green and reject-red are reinforcement rather than the only cue.
  return (
    <div className="flex items-center gap-3">
      <h2 id={titleID} className="font-semibold text-[13px] text-fg-0 tracking-tight">{c.device}</h2>
      <span id={timeID} className="font-mono text-[11px] text-fg-2">{c.time}</span>
      <span className="flex-1" />
      <div className="flex items-center gap-3 font-mono text-[11px] text-fg-2">
        <span className="inline-flex items-center gap-1" title={`${counts.picked} picked`}>
          <span className="text-pick" aria-hidden="true"><CheckIcon size={11} /></span>
          {counts.picked}<span className="sr-only"> picked</span>
        </span>
        <span className="inline-flex items-center gap-1" title={`${counts.rejected} rejected`}>
          <span className="text-reject" aria-hidden="true"><XIcon size={11} /></span>
          {counts.rejected}<span className="sr-only"> rejected</span>
        </span>
        {done ? (
          // Quiet, but enough to skim the page for clusters still to finish.
          <span className="inline-flex items-center gap-1 text-fg-1" title={`All ${n} reviewed`} data-testid="cluster-reviewed">
            <span aria-hidden="true"><CheckIcon size={11} /></span> Reviewed
          </span>
        ) : (
          <span className="text-fg-3" title={`${counts.reviewed} of ${n} reviewed`}>
            <span aria-hidden="true">{counts.reviewed}/{n}</span>
            <span className="sr-only">{counts.reviewed} of {n}</span> reviewed
          </span>
        )}
      </div>
      <button className="btn btn-ghost text-[11px]" onClick={onFocus}>
        Open Focus <span className="kbd" aria-hidden="true">F</span>
      </button>
    </div>
  )
}

// Memoised so ↑/↓ re-renders only the two sections whose active state
// flipped, not every cluster on the page.
const ClusterSection = memo(function ClusterSection({ c, idx, isActive, counts, onOpenCluster, onActivate }: {
  c: Cluster
  idx: number
  isActive: boolean
  counts: ClusterCounts
  onOpenCluster: (clusterID: number, fileID?: number) => void
  onActivate: (idx: number) => void
}) {
  const n = c.members.length
  const pct = n > 0 ? (counts.reviewed / n) * 100 : 0
  const titleID = `cluster-${c.id}-device`
  const timeID = `cluster-${c.id}-time`
  return (
    // role="group" rather than the implicit region: a named <section> per
    // cluster would flood the landmark list with hundreds of entries. The
    // device name is an h2, so heading navigation hops cluster to cluster.
    <section data-testid="cluster" data-cluster-id={c.id}
             data-active-cluster={isActive ? 'true' : undefined}
             role="group"
             aria-labelledby={`${titleID} ${timeID}`}
             aria-current={isActive ? 'true' : undefined}
             onClick={() => onActivate(idx)}
             className={`rounded-md transition-colors px-2 -mx-2 ${isActive ? 'bg-bg-2 ring-1 ring-line-2' : ''}`}>
      <div className="mb-2">
        <ClusterMeta c={c} counts={counts} onFocus={() => onOpenCluster(c.id)} titleID={titleID} timeID={timeID} />
        {/* Neutral fill: the rail's default pick-green gradient read as "picked". */}
        <div className="progress-rail mt-2 h-0.5"
             role="progressbar"
             aria-label="Reviewed"
             aria-valuemin={0}
             aria-valuemax={Math.max(1, n)}
             aria-valuenow={counts.reviewed}
             aria-valuetext={`${counts.reviewed} of ${n} reviewed`}>
          <div className="absolute inset-y-0 left-0 rounded-[inherit] bg-fg-2" style={{ width: `${pct}%` }} />
        </div>
      </div>
      <ClusterStrip clusterID={c.id} members={c.members} onOpenCluster={onOpenCluster} />
    </section>
  )
})

export const Library: React.FC<{
  data: LibraryResponse
  onOpenCluster: (clusterID: number, fileID?: number) => void
  onSettings: () => void
  onLibrary: () => void
  picksDir: string
  refresh: () => Promise<void>
  initialScroll?: number
  onScrollChange?: (top: number) => void
  initialActiveClusterID?: number | null
  onActiveClusterChange?: (id: number | null) => void
  initialFilters?: LibraryFilters
  onFiltersChange?: (f: LibraryFilters) => void
}> = ({ data, onOpenCluster, onSettings, onLibrary, picksDir, refresh, initialScroll, onScrollChange, initialActiveClusterID, onActiveClusterChange, initialFilters, onFiltersChange }) => {
  const [busy, setBusy] = useState(false)
  // Filters are seeded from the parent's remembered value so the very first
  // render already shows the list the user left; the scroll restore and the
  // active-cluster seed below both resolve against that first visible list.
  const init = initialFilters ?? DEFAULT_LIBRARY_FILTERS
  const [query, setQuery] = useState(init.query)
  // A remembered device that vanished while we were unmounted (re-cluster,
  // files removed) is dropped up front instead of via the reset effect below,
  // so the first paint isn't a spurious "No clusters match your filters."
  const [device, setDevice] = useState(() =>
    init.device && data.clusters.some(c => c.device === init.device) ? init.device : '')
  const [sort, setSort] = useState<SortKey>(init.sort)
  const [activeIdx, setActiveIdx] = useState(0)

  // Mirror filter changes outwards. An effect (rather than wrapping each
  // setter) also catches the device-reset effect clearing a stale camera, so
  // the parent never restores a filter that no longer matches anything.
  useEffect(() => {
    onFiltersChange?.({ query, device, sort })
  }, [query, device, sort, onFiltersChange])

  const searchRef = useRef<HTMLInputElement>(null)
  const mainRef = useRef<HTMLElement | null>(null)

  // Restore saved scroll position on (re)mount and when data first arrives.
  useEffect(() => {
    if (mainRef.current && typeof initialScroll === 'number') {
      mainRef.current.scrollTop = initialScroll
    }
    // We only want to restore once per mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.clusters.length])

  const handleScroll = (e: React.UIEvent<HTMLElement>) => {
    onScrollChange?.((e.target as HTMLElement).scrollTop)
  }

  // Cmd/Ctrl-K focuses the search input. Stands down while the shortcuts
  // dialog is open so focus isn't yanked out from behind it.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (shortcutsOpen()) return
      if ((e.metaKey || e.ctrlKey) && isLetter(e, 'k')) {
        e.preventDefault()
        searchRef.current?.focus()
        searchRef.current?.select()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  // Keyboard navigation: ↑/↓ (or j/k) move the active cluster, F / Enter open it.
  // The handler bails when the focus is on an input/textarea so the filter
  // box keeps its native key behavior, while the shortcuts dialog is open, and
  // for keys pressed inside an open dropdown menu (the menu also stops the
  // events it handles; this is the backstop).
  const visibleClustersRef = useRef<Cluster[]>([])
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (shortcutsOpen()) return
      const target = e.target instanceof Element ? e.target : null
      const tag = target?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA') return
      if (target?.closest('[role="menu"]')) return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      // Enter on a control reached with Tab (a thumbnail, the +N pill, a
      // dropdown trigger, Re-cluster…) belongs to that control's own click.
      // A control that is focused only because it was clicked doesn't claim
      // it: Enter opens the active cluster as usual, and the preventDefault
      // below stops the browser from also re-clicking that control.
      if (e.key === 'Enter' && keyboardFocusedControl(e)) return
      const list = visibleClustersRef.current
      if (list.length === 0) return
      if (e.key === 'ArrowDown' || isLetter(e, 'j')) {
        e.preventDefault()
        userMovedRef.current = true
        setActiveIdx(i => Math.min(list.length - 1, i + 1))
        return
      }
      if (e.key === 'ArrowUp' || isLetter(e, 'k')) {
        e.preventDefault()
        userMovedRef.current = true
        setActiveIdx(i => Math.max(0, i - 1))
        return
      }
      if (isLetter(e, 'f') || e.key === 'Enter') {
        e.preventDefault()
        const c = list[Math.min(activeIdx, list.length - 1)]
        if (c) onOpenCluster(c.id)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [activeIdx, onOpenCluster])

  const devices = useMemo(() => {
    const set = new Set<string>()
    data.clusters.forEach(c => set.add(c.device))
    return Array.from(set).sort()
  }, [data.clusters])

  // Reset device filter if it points at a device that's no longer present.
  useEffect(() => {
    if (device && !devices.includes(device)) setDevice('')
  }, [device, devices])

  const visibleClusters = useMemo(() => {
    const q = query.trim().toLowerCase()
    let out = data.clusters
    if (device) out = out.filter(c => c.device === device)
    if (q) {
      out = out.filter(c => {
        if (c.device.toLowerCase().includes(q)) return true
        if (c.time.toLowerCase().includes(q)) return true
        return c.members.some(m => m.path.toLowerCase().includes(q))
      })
    }
    if (sort === 'time-desc') {
      out = [...out].reverse()
    }
    return out
  }, [data.clusters, query, device, sort])

  // Keep the keyboard handler's view of the filtered list current without
  // forcing the handler to re-bind on every filter change.
  useEffect(() => {
    visibleClustersRef.current = visibleClusters
  }, [visibleClusters])

  // Clamp active index when the visible list shrinks (filter narrows, cluster
  // disappears after re-cluster, etc.) so navigation never points off the end.
  useEffect(() => {
    setActiveIdx(i => {
      if (visibleClusters.length === 0) return 0
      return Math.min(i, visibleClusters.length - 1)
    })
  }, [visibleClusters.length])

  // Seed activeIdx from the cluster ID the parent remembered across navigation
  // (Library → Focus → back). Runs once when the visible list first arrives so
  // the user's last-selected row stays selected. The filters are restored
  // before the first render, so this index is into the same filtered, sorted
  // list the ID was recorded against. Falls back to 0 if that cluster is no
  // longer visible (recluster hid it, a stale device filter was dropped, etc.).
  const seededFromIDRef = useRef(false)
  useEffect(() => {
    if (seededFromIDRef.current) return
    if (visibleClusters.length === 0) return
    seededFromIDRef.current = true
    if (initialActiveClusterID == null) return
    const idx = visibleClusters.findIndex(c => c.id === initialActiveClusterID)
    if (idx >= 0) setActiveIdx(idx)
  }, [visibleClusters, initialActiveClusterID])

  // Mirror the active cluster's ID outwards so the parent can restore it on
  // remount. Skipped while the visible list is empty so we don't clobber the
  // saved ID with null during a transient loading state.
  useEffect(() => {
    if (!onActiveClusterChange) return
    if (visibleClusters.length === 0) return
    const c = visibleClusters[Math.min(activeIdx, visibleClusters.length - 1)]
    onActiveClusterChange(c?.id ?? null)
  }, [activeIdx, visibleClusters, onActiveClusterChange])

  // Scroll the active cluster into view when keyboard nav moves it. Skipped
  // on first render so the saved scroll position from initialScroll wins; a
  // user-driven nav flips userMovedRef and from then on we follow activeIdx.
  const userMovedRef = useRef(false)
  useEffect(() => {
    if (!userMovedRef.current) return
    const main = mainRef.current
    if (!main) return
    const el = main.querySelector<HTMLElement>(`[data-active-cluster="true"]`)
    if (el) el.scrollIntoView({ block: 'nearest', behavior: prefersReducedMotion() ? 'auto' : 'smooth' })
  }, [activeIdx])

  // Counts are computed once per data.clusters change instead of three times per
  // cluster card per render. With 200 clusters of ~10 members each this is ~2,000
  // boolean checks per data refresh rather than ~8,000 per render.
  const counts = useMemo(() => {
    const m = new Map<number, ClusterCounts>()
    for (const c of data.clusters) {
      let p = 0, r = 0
      for (const x of c.members) {
        if (x.picked) p++
        else if (x.rejected) r++
      }
      m.set(c.id, { picked: p, rejected: r, reviewed: p + r })
    }
    return m
  }, [data.clusters])

  const handleRecluster = async () => {
    setBusy(true)
    try {
      await postRecluster()
      await refresh()
      toast.ok('Re-clustered')
    } catch (e) {
      toast.reject('Re-cluster failed', { body: String(e) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col h-screen overflow-hidden bg-bg-1">
      <AppBar folder={data.folder} picked={data.picked} total={data.total} onSettings={onSettings} onLibrary={onLibrary} />
      <Subnav
        summary={{ clusters: data.clusters.length, total: data.total, visible: visibleClusters.length }}
        onRecluster={handleRecluster}
        busy={busy}
        query={query}
        onQuery={setQuery}
        devices={devices}
        device={device}
        onDevice={setDevice}
        sort={sort}
        onSort={setSort}
        searchRef={searchRef}
      />
      <HelpHint picksDir={picksDir} />
      <main ref={mainRef} onScroll={handleScroll} className="flex-1 overflow-auto" data-testid="clusters">
        <div className="px-3.5 pt-4 pb-10 flex flex-col gap-6">
          {visibleClusters.length === 0 && (
            <div className="text-fg-2 text-xs px-2">No clusters match your filters.</div>
          )}
          {visibleClusters.map((c, idx) => (
            <ClusterSection key={c.id}
                            c={c}
                            idx={idx}
                            isActive={idx === activeIdx}
                            counts={counts.get(c.id) ?? ZERO_COUNTS}
                            onOpenCluster={onOpenCluster}
                            onActivate={setActiveIdx} />
          ))}
        </div>
      </main>
    </div>
  )
}
