import React, { useEffect, useState, useCallback, useMemo, useRef } from 'react'
import {
  AppBar,
  ArrowL, ArrowR, CheckIcon, XIcon, PanelIcon, ZoomIcon, CompareIcon, Spinner,
} from './primitives'
import { fetchCluster, postPick, thumbURL, fullURL, type FocusResponse, type Member } from '../api'
import { toast } from './Toaster'
import { isLetter } from '../keys'
import { shortcutsOpen } from './Shortcuts'

const MIN_ZOOM = 1
const MAX_ZOOM = 8
const ZOOM_STEP = 1.5 // multiplicative step for the +/- buttons and Z key

const clampZoom = (z: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z))

// A pointer that moves less than this between down and up on a compare pane
// counts as a click (activate the pane) rather than a pan drag.
const CLICK_SLOP_PX = 4

// Every successful Pick/Reject/Clear (and its Undo) reuses one toast slot so
// fast culling doesn't stack a tower of 4-second toasts.
const UNDO_TOAST_KEY = 'focus-undo'
const UNDO_HINT = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.userAgent) ? '⌘Z' : 'Ctrl+Z'

type PickAction = 'pick' | 'unpick' | 'reject' | 'unreject'
type Mark = { picked: boolean; rejected: boolean }

// applyAction mirrors the server's picks state machine (internal/picks):
// 'pick' on a rejected file overwrites the reject, 'reject' on a picked file
// unexports it, and the un-* actions only clear their own flag.
function applyAction(s: Mark, action: PickAction): Mark {
  switch (action) {
    case 'pick': return { picked: true, rejected: false }
    case 'reject': return { picked: false, rejected: true }
    case 'unpick': return { picked: false, rejected: s.rejected }
    case 'unreject': return { picked: s.picked, rejected: false }
  }
}

// undoActionFor returns the single server action that takes a file from `cur`
// back to `prev`, or null when they already match. One action is always
// enough because pick/reject each overwrite the other state.
function undoActionFor(prev: Mark, cur: Mark): PickAction | null {
  if (prev.picked) return cur.picked ? null : 'pick'
  if (prev.rejected) return cur.rejected ? null : 'reject'
  if (cur.picked) return 'unpick'
  if (cur.rejected) return 'unreject'
  return null
}

// One level of undo: the last successful Pick/Reject/Clear. `prev` is the
// file's mark before the action, `next` the mark the action produced.
//
// Both live at module level, not in the component, because the undo toast
// outlives the Focus instance that raised it: the user can press Esc, reopen
// the cluster and click Undo while the toast is still up. `undoSlot` lets the
// new instance's ⌘Z see that action, and `liveUndo` routes the toast's button
// to whichever Focus is mounted now, so the view on screen (not a discarded
// one) is patched and its in-flight guard applies.
let undoSlot: { fileID: number; prev: Mark; next: Mark } | null = null
let liveUndo: (() => Promise<void>) | null = null

// Completed mutations are announced at module level for the same reason: the
// request may have been started by a Focus instance that has since unmounted
// (Undo clicked from the Library, then the cluster reopened before the
// response). Whichever Focus is mounted when it completes subscribes here and
// patches its own members. `mutationSeq` lets a cluster load detect that a
// mutation completed while its fetch was in flight, in which case the
// response may predate the write and must not be shown as-is.
let mutationSeq = 0
const mutationListeners = new Set<(fileID: number, action: PickAction) => void>()

// Files with a mutation in flight. Shared across Focus instances as well: a
// reopened Focus must not start a second mutation on a file whose Undo (sent
// by the previous instance) is still pending, or it would record that file's
// soon-to-be-stale mark as the state to undo back to. Checked synchronously
// in mutate; instances mirror it into state to disable their buttons.
const inflightFiles = new Set<number>()
const inflightListeners = new Set<() => void>()
function setInflight(fileID: number, on: boolean) {
  if (on) inflightFiles.add(fileID)
  else inflightFiles.delete(fileID)
  for (const l of inflightListeners) l()
}

// The MediaQueryList is created once; reading `.matches` per use is cheap and
// still follows the user flipping the OS setting mid-session. CSS transitions
// are covered by the global reduced-motion rule, but the rail's smooth scroll
// and the inline crossfade/zoom durations are set from JS, so they check here.
const reducedMotionMQ = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
  ? window.matchMedia('(prefers-reduced-motion: reduce)')
  : null
const reducedMotion = () => reducedMotionMQ?.matches ?? false
const motionMs = (ms: number) => (reducedMotion() ? '0ms' : `${ms}ms`)

const baseName = (path: string) => path.slice(path.lastIndexOf('/') + 1)

// imageLabel is the accessible name of a hero / compare image: the file name
// (the full relative path is noise when read aloud) plus its mark, which is
// otherwise only conveyed by colour and glyphs.
const imageLabel = (m: Member) => `${baseName(m.path)}${m.picked ? ', picked' : m.rejected ? ', rejected' : ''}`

// PathLabel renders a relative path on one line inside a flex parent. Nested
// folders easily overflow the overlay, and the file name is the part the user
// needs, so the name never shrinks and only the folder part is ellipsised —
// from the left (direction: rtl), so the folder nearest the file survives.
// The LRM marks keep the slashes, which are bidi-neutral, from being
// reordered to the wrong end of the run under rtl. Full path in the title.
const PathLabel: React.FC<{ path: string; className?: string }> = ({ path, className }) => {
  const cut = path.lastIndexOf('/') + 1
  const dir = path.slice(0, cut)
  return (
    <span className={`flex min-w-0 ${className ?? ''}`} title={path}>
      {dir && (
        <span className="min-w-0 truncate" style={{ direction: 'rtl' }}>{`\u200E${dir}\u200E`}</span>
      )}
      <span className="shrink-0 max-w-full truncate">{path.slice(cut)}</span>
    </span>
  )
}

// MarkBadge shows a file's pick/reject state with a distinct glyph as well as
// the colour, so the two stay distinguishable without red-green vision.
// Decorative: every place that renders it also names the state in text or in
// an accessible name.
const MarkBadge: React.FC<{ m: Member; withText?: boolean }> = ({ m, withText }) => {
  if (m.picked) {
    return (
      <span className="inline-flex items-center gap-1 text-pick" aria-hidden={withText ? undefined : true}>
        <CheckIcon size={11} />{withText && 'Picked'}
      </span>
    )
  }
  if (m.rejected) {
    return (
      <span className="inline-flex items-center gap-1 text-reject" aria-hidden={withText ? undefined : true}>
        <XIcon size={11} />{withText && 'Rejected'}
      </span>
    )
  }
  return null
}

// paneDigit returns 1–4 for the pane-select keys, preferring e.key and falling
// back to the physical key (AZERTY types '&', 'é', … on the digit row without
// Shift). Like isLetter, the physical fallback is skipped under Alt/AltGr.
function paneDigit(e: KeyboardEvent): number | null {
  if (e.key >= '1' && e.key <= '4' && e.key.length === 1) return Number(e.key)
  if (e.altKey || e.getModifierState('AltGraph')) return null
  const m = /^Digit([1-4])$/.exec(e.code)
  return m ? Number(m[1]) : null
}

// CrossfadeImage stacks a thumbnail and full-resolution image in the same
// bounding box and crossfades when the full version finishes loading. Both
// layers `object-contain` so they letterbox identically — only the sharpness
// changes between states. A spinner sits underneath while either is loading.
//
// Hero mode wraps with `relative w-full h-full`; compare mode (one of up to
// four panes in a grid) renders bare so its parent's grid cell controls layout.
//
// For video members the full-resolution layer is replaced with a <video> element
// with native controls. The thumbnail layer (a JPEG keyframe) is still used for
// the preload effect. Zoom/pan are disabled for videos.
const CrossfadeImage: React.FC<{
  member: Member
  pan: { x: number; y: number }
  zoom: number
  dragging: boolean
  variant: 'hero' | 'compare'
}> = ({ member, pan, zoom, dragging, variant }) => {
  const [thumbReady, setThumbReady] = useState(false)
  const [fullReady, setFullReady] = useState(false)
  // fullSrc is a Blob URL once the fetch completes. Using fetch+AbortController
  // (instead of binding fullURL straight to <img src>) lets us *actually*
  // cancel the request when the user navigates away — browser <img> load
  // cancellation on element removal is unreliable, and at the network layer
  // those orphaned requests pin the per-origin connection limit. The server
  // keeps transcoding on its BackgroundContext, so coming back to the same
  // photo hits the warm cache.
  const [fullSrc, setFullSrc] = useState<string>('')
  const isVideo = member.kind === 'video'
  const cacheKey = `${member.file_id}-${member.content_hash}`

  useEffect(() => { setThumbReady(false); setFullReady(false); setFullSrc('') }, [cacheKey])

  // Fetch the full-resolution image as a Blob with AbortController so
  // navigation cancels the HTTP request mid-flight. Videos keep their own
  // load lifecycle on the <video> element.
  useEffect(() => {
    if (isVideo) return
    const ctrl = new AbortController()
    let createdURL: string | null = null
    let cancelled = false
    fetch(fullURL(member), { signal: ctrl.signal })
      .then(r => {
        if (!r.ok) throw new Error(`full ${r.status}`)
        return r.blob()
      })
      .then(b => {
        if (cancelled) return
        createdURL = URL.createObjectURL(b)
        setFullSrc(createdURL)
      })
      .catch(() => { /* abort or fetch failure — leave the thumb visible */ })
    return () => {
      cancelled = true
      ctrl.abort()
      if (createdURL) URL.revokeObjectURL(createdURL)
    }
  }, [cacheKey, isVideo, member])

  // Videos never use pan/zoom — always render at natural size.
  const transform = isVideo ? 'none' : `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`
  const transitionDuration = dragging ? '0ms' : motionMs(120)
  const fadeDuration = motionMs(180)
  // Both layers stay in the accessibility tree (they're only faded), so only
  // the one currently on top carries the name; the other is decorative.
  const label = imageLabel(member)
  const thumbAlt = fullReady ? '' : label
  const fullAlt = fullReady ? label : ''
  const imgCls = variant === 'compare'
    ? 'absolute inset-0 w-full h-full object-contain bg-bg-0 transition-transform'
    : 'absolute inset-0 w-full h-full object-contain transition-transform'

  const spinner = !thumbReady && !fullReady ? (
    <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
      <Spinner />
    </div>
  ) : null

  const layers = isVideo ? (
    <>
      {variant === 'hero' && spinner}
      {/* Thumb keyframe shown while video hasn't loaded */}
      <img
        key={`thumb-${cacheKey}`}
        src={thumbURL(member)}
        alt={thumbAlt}
        draggable={false}
        onLoad={() => setThumbReady(true)}
        className={imgCls}
        style={{
          transform, transformOrigin: 'center center', transitionDuration,
          opacity: fullReady ? 0 : (thumbReady ? 1 : 0),
          pointerEvents: 'none',
        }}
      />
      <video
        key={`video-${cacheKey}`}
        src={fullURL(member)}
        aria-label={fullAlt || undefined}
        controls
        draggable={false}
        tabIndex={-1}
        onFocus={(e) => e.currentTarget.blur()}
        onCanPlay={() => setFullReady(true)}
        className={variant === 'compare'
          ? 'absolute inset-0 w-full h-full object-contain bg-bg-0'
          : 'absolute inset-0 w-full h-full object-contain'}
        style={{ opacity: fullReady ? 1 : 0, transition: `opacity ${fadeDuration}` }}
        data-testid={variant === 'hero' ? 'hero-video' : undefined}
      />
      {variant === 'compare' && spinner}
    </>
  ) : (
    <>
      {variant === 'hero' && spinner}
      <img
        key={`thumb-${cacheKey}`}
        src={thumbURL(member)}
        alt={thumbAlt}
        draggable={false}
        onLoad={() => setThumbReady(true)}
        className={imgCls}
        style={{
          transform, transformOrigin: 'center center', transitionDuration,
          opacity: fullReady ? 0 : (thumbReady ? 1 : 0),
          pointerEvents: 'none',
        }}
      />
      {fullSrc && (
        <img
          key={`full-${cacheKey}`}
          src={fullSrc}
          alt={fullAlt}
          draggable={false}
          onLoad={() => setFullReady(true)}
          className={`${imgCls.replace('transition-transform', 'transition-opacity')}`}
          style={{
            transform, transformOrigin: 'center center',
            transitionDuration: fadeDuration,
            opacity: fullReady ? 1 : 0,
            pointerEvents: 'none',
          }}
          data-testid={variant === 'hero' ? 'hero' : undefined}
        />
      )}
      {variant === 'compare' && spinner}
    </>
  )

  if (variant === 'hero') {
    return <div className="relative w-full h-full">{layers}</div>
  }
  return layers
}

export const Focus: React.FC<{
  clusterID: number
  startFileID?: number
  totalPicked: number
  totalCount: number
  folder: string
  advanceOnAction: boolean
  onBack: () => void
  onCluster: (id: number) => void
  onSettings: () => void
  onLibrary: () => void
  onChanged: () => void
}> = ({ clusterID, startFileID, totalPicked, totalCount, folder, advanceOnAction, onBack, onCluster, onSettings, onLibrary, onChanged }) => {
  const [data, setData] = useState<FocusResponse | null>(null)
  const [current, setCurrent] = useState(0)
  const [showInfo, setShowInfo] = useState<boolean>(() => {
    try {
      const v = localStorage.getItem('choix.focus.showInfo')
      if (v === null) return true
      return v === '1'
    } catch { return true }
  })
  const [members, setMembersState] = useState<Member[]>([])
  const [zoom, setZoom] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [comparing, setComparingState] = useState(false)
  const [compareIdx, setCompareIdxState] = useState<number[]>([])
  // members / current / comparing / compareIdx are mirrored into refs that are
  // written *synchronously* alongside each setState. The keydown handler is
  // only re-registered after React commits, so with keyboard autorepeat a
  // second keydown can land on the previous render's closure; reading these
  // refs instead means it acts on the photo the user actually sees now (e.g.
  // the one advance-on-action just moved to), not the one before it.
  const membersRef = useRef<Member[]>([])
  const currentRef = useRef(0)
  const comparingRef = useRef(false)
  const compareIdxRef = useRef<number[]>([])
  const goTo = useCallback((i: number) => { currentRef.current = i; setCurrent(i) }, [])
  const setComparing = useCallback((on: boolean) => { comparingRef.current = on; setComparingState(on) }, [])
  const setCompareIdx = useCallback((xs: number[]) => { compareIdxRef.current = xs; setCompareIdxState(xs) }, [])
  const patchMember = useCallback((fileID: number, action: PickAction) => {
    const patch = (ms: Member[]) => ms.map(m => m.file_id === fileID ? { ...m, ...applyAction(m, action) } : m)
    membersRef.current = patch(membersRef.current)
    setMembersState(patch)
  }, [])
  useEffect(() => {
    mutationListeners.add(patchMember)
    return () => { mutationListeners.delete(patchMember) }
  }, [])
  // inflightIDs mirrors the module-level inflightFiles as state, only so the
  // Pick/Reject buttons can be disabled (including for a mutation a previous
  // Focus instance left pending).
  const [inflightIDs, setInflightIDs] = useState<Set<number>>(() => new Set(inflightFiles))
  useEffect(() => {
    const sync = () => setInflightIDs(new Set(inflightFiles))
    inflightListeners.add(sync)
    // Catch a change between the initial render and this subscription.
    sync()
    return () => { inflightListeners.delete(sync) }
  }, [])
  // The undo toast's action closure can outlive this render (and even this
  // component, if the user clicks Undo from the Library), so it reaches
  // onChanged through a ref rather than capturing a possibly stale prop.
  const onChangedRef = useRef(onChanged)
  useEffect(() => { onChangedRef.current = onChanged }, [onChanged])
  const dragState = useRef<{ x: number; y: number; px: number; py: number } | null>(null)
  // Where a pointer went down on a compare pane, to tell a click (activate
  // that pane) from a pan drag.
  const paneDown = useRef<{ x: number; y: number; idx: number } | null>(null)
  const railRef = useRef<HTMLElement | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const stageRef = useRef<HTMLDivElement | null>(null)
  const zoomRef = useRef(1)
  useEffect(() => { zoomRef.current = zoom }, [zoom])

  // Persist Info panel toggle across sessions.
  useEffect(() => {
    try { localStorage.setItem('choix.focus.showInfo', showInfo ? '1' : '0') } catch { /* ignore */ }
  }, [showInfo])

  // Keep the rail's active thumb visible when navigating with arrows / Pick / Reject.
  useEffect(() => {
    const rail = railRef.current
    if (!rail) return
    const target = rail.querySelector<HTMLElement>(`[data-rail-index="${current}"]`)
    if (target) {
      target.scrollIntoView({ block: 'nearest', behavior: reducedMotion() ? 'auto' : 'smooth' })
    }
  }, [current])

  // Warm the /full/ cache for nearby members so the next ←/→ keypress hits
  // an already-built JPEG instead of waiting on a fresh sips/ffmpeg pass.
  // We only prefetch photos whose format the browser can't render directly
  // (HEIC, RAF, TIFF, …) — for jpeg/png the /full/ handler just streams the
  // original, and for videos a full prefetch would eat tens of MB for no
  // perceptible win. Using fetch+AbortController gives us deterministic
  // cancellation when the user moves on; the server-side singleflight
  // collapses any race with the user's actual click into a single transcode.
  useEffect(() => {
    if (members.length === 0) return
    const offsets = [1, -1, 2, -2]
    const ctrl = new AbortController()
    for (const off of offsets) {
      const idx = current + off
      if (idx < 0 || idx >= members.length) continue
      const m = members[idx]
      if (m.kind !== 'photo') continue
      const fmt = (m.format || '').toLowerCase()
      if (fmt === 'jpeg' || fmt === 'jpg' || fmt === 'png') continue
      // Fire and forget. The browser HTTP cache holds the response so the
      // active fetch in CrossfadeImage will dedupe (or, failing that, the
      // server-side singleflight will).
      fetch(fullURL(m), { signal: ctrl.signal }).catch(() => { /* aborted or failed */ })
    }
    return () => { ctrl.abort() }
  }, [current, members])

  const load = useCallback(async () => {
    // Cancel any previous in-flight fetch so stale responses don't overwrite
    // fresh ones when navigating clusters rapidly.
    if (abortRef.current) abortRef.current.abort()
    const ctrl = new AbortController()
    abortRef.current = ctrl

    let d: FocusResponse
    for (;;) {
      const seq = mutationSeq
      try {
        d = await fetchCluster(clusterID, ctrl.signal)
      } catch (e) {
        if (ctrl.signal.aborted) return
        throw e
      }
      if (ctrl.signal.aborted) return
      // A pick/reject/undo finished while this fetch was in flight, so the
      // response may carry the marks from before it (and the listener patch
      // had nothing to patch yet on a first load). The DB is the source of
      // truth: fetch again rather than show possibly stale marks. A mutation
      // that finishes after this point is applied by the listener instead.
      if (mutationSeq === seq) break
    }

    setData(d)
    membersRef.current = d.cluster.members
    setMembersState(d.cluster.members)
    if (startFileID !== undefined) {
      const idx = d.cluster.members.findIndex(m => m.file_id === startFileID)
      // If the requested fileID is missing (cluster rebuilt), fall back to first.
      goTo(idx >= 0 ? idx : 0)
    } else {
      goTo(0)
    }
  }, [clusterID, startFileID, goTo])

  useEffect(() => { load().catch(e => toast.reject('Load cluster failed', { body: String(e) })) }, [load])

  // Focus stays mounted when Enter / Shift+Enter switches clusters, so state
  // tied to the previous cluster's member indices must be dropped here:
  // compareIdx holds *indices*, which would otherwise point at unrelated
  // photos in the new cluster, and zoom/pan would survive when both clusters
  // open on index 0 (the `current` effect below never fires).
  useEffect(() => {
    setComparing(false)
    setCompareIdx([])
    setZoom(1)
    setPan({ x: 0, y: 0 })
  }, [clusterID, setComparing, setCompareIdx])

  // Reset zoom + pan whenever the active photo changes — except in compare
  // mode, where `current` is just the active pane and every pane shares one
  // zoom/pan so switching panes must keep the user's pixel-peep position.
  useEffect(() => {
    if (comparingRef.current) return
    setZoom(1); setPan({ x: 0, y: 0 })
  }, [current])

  const member = members[current]

  // mutate posts one action for a file and patches local state on success.
  // Returns false (without doing anything) when a mutation for the same file
  // is already in flight; throws on server/network failure.
  const mutate = useCallback(async (fileID: number, action: PickAction): Promise<boolean> => {
    // Per-file-id in-flight guard: checked synchronously via ref so keyboard
    // autorepeat (rapid Space presses) or an Undo can't queue a second
    // mutation before the first one settles.
    if (inflightFiles.has(fileID)) return false
    setInflight(fileID, true)
    try {
      await postPick(fileID, action)
      // Announce instead of patching this instance directly: the user may
      // have switched clusters, or left and reopened Focus, while the request
      // was in flight, and the view on screen is the one that must update.
      // Listeners patch by file id, not index.
      mutationSeq++
      for (const l of mutationListeners) l(fileID, action)
      onChangedRef.current()
      return true
    } finally {
      setInflight(fileID, false)
    }
  }, [])

  const undo = useCallback(async () => {
    const slot = undoSlot
    if (!slot) return
    const action = undoActionFor(slot.prev, slot.next)
    if (!action) { undoSlot = null; return }
    try {
      // Busy (a mutation on this file is still pending): keep the slot so the
      // user can simply press ⌘Z again.
      if (!(await mutate(slot.fileID, action))) return
      // Only consume the slot if no newer action replaced it meanwhile; in
      // that case the newer action's toast (and its Undo) stays up.
      if (undoSlot === slot) {
        undoSlot = null
        toast.ok('Undone', { key: UNDO_TOAST_KEY, duration: 1500 })
      }
    } catch (e) {
      toast.reject('Undo failed', { body: String(e) })
    }
  }, [mutate])

  // Register this instance as the one the undo toast should call. With no
  // Focus mounted (Undo clicked from the Library) the toast falls back to the
  // closure it was created with, which still fixes the server state and
  // refreshes the Library.
  useEffect(() => {
    liveUndo = undo
    return () => { if (liveUndo === undo) liveUndo = null }
  }, [undo])

  // actionTarget is what Space / X / Pick / Reject act on: the current photo,
  // or in compare mode the active pane (none if `current` isn't compared).
  const actionTarget = useCallback((): Member | undefined => {
    const cur = currentRef.current
    if (comparingRef.current && !compareIdxRef.current.includes(cur)) return undefined
    return membersRef.current[cur]
  }, [])

  const toggle = useCallback(async (k: 'picked' | 'rejected') => {
    const target = actionTarget()
    if (!target) return
    const wasOn = target[k]
    const action: PickAction = k === 'picked' ? (wasOn ? 'unpick' : 'pick') : (wasOn ? 'unreject' : 'reject')
    const prev: Mark = { picked: target.picked, rejected: target.rejected }
    try {
      if (!(await mutate(target.file_id, action))) return
      undoSlot = { fileID: target.file_id, prev, next: applyAction(prev, action) }
      toast.ok(action === 'pick' ? 'Picked' : action === 'reject' ? 'Rejected' : 'Cleared', {
        key: UNDO_TOAST_KEY,
        action: { label: 'Undo', hint: UNDO_HINT, onClick: () => { void (liveUndo ?? undo)() } },
      })
      // Auto-advance on accept/reject (not on un-accept/un-reject), wrapping at
      // the end. Never while comparing — the panes are the user's working set —
      // and only if the user is still on the file they acted on (they may have
      // moved on, or switched clusters, while the request was in flight).
      const ms = membersRef.current
      const cur = currentRef.current
      if (advanceOnAction && !wasOn && !comparingRef.current && ms.length > 1 && ms[cur]?.file_id === target.file_id) {
        goTo((cur + 1) % ms.length)
      }
    } catch (e) {
      toast.reject('Action failed', { body: String(e) })
    }
  }, [actionTarget, mutate, undo, advanceOnAction, goTo])

  // step moves ←/→: through the whole cluster normally, or cycles the active
  // pane among the compared members in compare mode. Both wrap.
  const step = useCallback((dir: 1 | -1) => {
    const n = membersRef.current.length
    if (n === 0) return
    const cur = currentRef.current
    if (comparingRef.current) {
      const sel = compareIdxRef.current
      if (sel.length === 0) return
      const pos = sel.indexOf(cur)
      goTo(pos < 0 ? sel[0] : sel[(pos + dir + sel.length) % sel.length])
      return
    }
    goTo((cur + dir + n) % n)
  }, [goTo])

  const stepZoom = useCallback((dir: 1 | -1) => {
    setZoom(z => {
      const next = clampZoom(dir === 1 ? z * ZOOM_STEP : z / ZOOM_STEP)
      if (next <= MIN_ZOOM + 0.001) {
        setPan({ x: 0, y: 0 })
        return MIN_ZOOM
      }
      return next
    })
  }, [])

  const toggleCompare = useCallback(() => {
    if (comparingRef.current) { setComparing(false); return }
    // Re-entering compare keeps the previous selection; the first time, the
    // current photo becomes the first (and active) pane.
    const sel = compareIdxRef.current.length > 0 ? compareIdxRef.current : [currentRef.current]
    setCompareIdx(sel)
    setComparing(true)
    // The active pane is the one whose index equals `current`; if a retained
    // selection doesn't include the current photo, activate its first pane
    // so Space / X always have a visible target.
    if (!sel.includes(currentRef.current)) goTo(sel[0])
  }, [setComparing, setCompareIdx, goTo])

  const toggleCompareSelect = useCallback((i: number) => {
    const prev = compareIdxRef.current
    let next: number[]
    if (prev.includes(i)) next = prev.filter(x => x !== i)
    else if (prev.length >= 4) return // hard cap so the grid stays readable
    else next = [...prev, i]
    setCompareIdx(next)
    // Keep an active pane whenever there is one to activate: if the active
    // member was removed, move to the pane that slid into its slot (or the
    // new last one); if the set was empty, the newly added member is active.
    const cur = currentRef.current
    if (next.length > 0 && !next.includes(cur)) {
      const removedAt = prev.indexOf(cur)
      goTo(removedAt >= 0 ? next[Math.min(removedAt, next.length - 1)] : next[0])
    }
  }, [setCompareIdx, goTo])

  // activatePane makes the Nth (1-based) compare pane active. Filtering to
  // valid indices keeps the numbering identical to the rendered pane badges.
  const activatePane = useCallback((n: number) => {
    const idx = compareIdxRef.current.filter(i => i < membersRef.current.length)[n - 1]
    if (idx !== undefined) goTo(idx)
  }, [goTo])

  // `data` still describes the previous cluster until the new fetch lands;
  // ignore ↵ / ⇧↵ in that window so a quick ↵ then ⇧↵ can't follow the old
  // cluster's neighbour ids to the wrong place.
  const goNextCluster = useCallback(() => {
    if (data && data.cluster.id === clusterID && data.next_cluster_id) onCluster(data.next_cluster_id)
  }, [data, clusterID, onCluster])
  const goPrevCluster = useCallback(() => {
    if (data && data.cluster.id === clusterID && data.prev_cluster_id) onCluster(data.prev_cluster_id)
  }, [data, clusterID, onCluster])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      // The shortcuts dialog owns the keyboard while it's open (App handles
      // its ? / Esc). Bail before the video blur and before any
      // preventDefault, so Space / X / Esc / arrows neither act on the photo
      // behind it nor get swallowed from the dialog.
      if (shortcutsOpen()) return
      const tag = (e.target as HTMLElement)?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA') return
      // The native <video controls> intercepts Space (play/pause) and the
      // arrow keys (seek/volume) when its play button or the element itself
      // holds focus. We listen on the capture phase and blur any focused
      // video sub-control so the shortcuts always reach our handlers — and
      // call stopImmediatePropagation on the keys we care about so the
      // browser's UA shadow-DOM handlers never run.
      const focused = document.activeElement as HTMLElement | null
      if (focused && (focused.tagName === 'VIDEO' || focused.closest('video'))) {
        focused.blur()
      }
      // ⌘/Ctrl chords belong to the browser (⌘C copy, ⌘+ page zoom, …), not to
      // our single-key shortcuts — without this ⌘Z would toggle pixel-peep and
      // ⌘X would reject. ⌘Z / Ctrl+Z is the one chord we own: undo. AltGr is
      // reported as Ctrl+Alt on some layouts, so a Ctrl that comes with
      // AltGraph is a character-producing key, not a chord; it falls through
      // to the normal path where isLetter already rejects AltGr letters.
      const altGr = e.getModifierState('AltGraph')
      if (e.metaKey || (e.ctrlKey && !altGr)) {
        if (!e.shiftKey && !e.altKey && isLetter(e, 'z')) {
          e.preventDefault()
          if (!e.repeat) void undo()
        }
        return
      }
      const member = membersRef.current[currentRef.current]
      const isVideoMember = member?.kind === 'video'
      if (e.key === 'Escape') {
        e.preventDefault()
        if (zoom > 1) { setZoom(1); setPan({ x: 0, y: 0 }); return }
        if (comparingRef.current) { setComparing(false); return }
        onBack()
        return
      }
      if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
        e.preventDefault(); e.stopImmediatePropagation()
        step(-1)
        return
      }
      if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
        e.preventDefault(); e.stopImmediatePropagation()
        step(1)
        return
      }
      if (comparingRef.current) {
        const n = paneDigit(e)
        if (n !== null) { e.preventDefault(); activatePane(n); return }
      }
      if (e.key === ' ' || e.code === 'Space') { e.preventDefault(); e.stopImmediatePropagation(); toggle('picked'); return }
      if (isLetter(e, 'x')) { e.preventDefault(); toggle('rejected'); return }
      if (isLetter(e, 'i')) { e.preventDefault(); setShowInfo(v => !v); return }
      if (isLetter(e, 'z')) {
        e.preventDefault()
        if (isVideoMember) return // videos render at native size; zoom is hidden in the toolbar too
        // Toggle: if already zoomed, snap back to 1×; otherwise jump to 2×.
        if (zoom > 1) { setZoom(1); setPan({ x: 0, y: 0 }) } else { setZoom(2) }
        return
      }
      if (isLetter(e, 'c')) { e.preventDefault(); toggleCompare(); return }
      if (e.key === '+' || e.key === '=') { e.preventDefault(); if (!isVideoMember) stepZoom(1); return }
      if (e.key === '-' || e.key === '_') { e.preventDefault(); if (!isVideoMember) stepZoom(-1); return }
      if (e.key === '0') { e.preventDefault(); if (!isVideoMember) { setZoom(1); setPan({ x: 0, y: 0 }) } ; return }
      if (e.key === 'Enter') {
        e.preventDefault()
        if (e.shiftKey) goPrevCluster()
        else goNextCluster()
        return
      }
    }
    window.addEventListener('keydown', handler, { capture: true })
    return () => window.removeEventListener('keydown', handler, { capture: true } as unknown as EventListenerOptions)
  }, [toggle, undo, step, activatePane, onBack, goNextCluster, goPrevCluster, zoom, stepZoom, toggleCompare, setComparing])

  // Smooth pinch-to-zoom inside the photo stage. The App-level handler
  // preventDefaults pinch gestures everywhere else; here we re-handle them so
  // the photo zooms instead of the browser scaling the page. Three sources:
  //
  //   1. macOS trackpad pinch → wheel events with ctrlKey set, deltaY in
  //      "lines" (~tens of units per pinch tick). exp() maps it to a smooth
  //      multiplicative scale change.
  //   2. ⌘+wheel → same path via metaKey for users who prefer the modifier.
  //   3. Safari touch pinch → gesturestart/gesturechange with `scale`
  //      relative to the gesture's origin. We snapshot the zoom on start and
  //      multiply by `scale` on each change.
  //
  // Videos don't zoom; bail out early so the gesture falls through to the
  // browser's media controls.
  useEffect(() => {
    const el = stageRef.current
    if (!el) return
    const isVideo = () => member?.kind === 'video'
    const wheel = (e: WheelEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return
      e.preventDefault()
      if (isVideo()) return
      const next = clampZoom(zoomRef.current * Math.exp(-e.deltaY * 0.01))
      if (next <= MIN_ZOOM + 0.001) { setPan({ x: 0, y: 0 }); setZoom(MIN_ZOOM) }
      else setZoom(next)
    }
    let gestureBase = 1
    const gestureStart = (e: Event) => {
      e.preventDefault()
      if (isVideo()) return
      gestureBase = zoomRef.current
    }
    const gestureChange = (e: Event) => {
      e.preventDefault()
      if (isVideo()) return
      const scale = (e as unknown as { scale: number }).scale
      if (!isFinite(scale) || scale <= 0) return
      const next = clampZoom(gestureBase * scale)
      if (next <= MIN_ZOOM + 0.001) { setPan({ x: 0, y: 0 }); setZoom(MIN_ZOOM) }
      else setZoom(next)
    }
    const gestureEnd = (e: Event) => { e.preventDefault() }
    el.addEventListener('wheel', wheel, { passive: false })
    el.addEventListener('gesturestart', gestureStart as EventListener)
    el.addEventListener('gesturechange', gestureChange as EventListener)
    el.addEventListener('gestureend', gestureEnd as EventListener)
    return () => {
      el.removeEventListener('wheel', wheel)
      el.removeEventListener('gesturestart', gestureStart as EventListener)
      el.removeEventListener('gesturechange', gestureChange as EventListener)
      el.removeEventListener('gestureend', gestureEnd as EventListener)
    }
  }, [member])

  // Counted once per members change instead of on every render — the parent's
  // pointermove handlers re-render this component dozens of times per second.
  const reviewed = useMemo(
    () => members.reduce((n, m) => n + (m.picked || m.rejected ? 1 : 0), 0),
    [members],
  )

  if (!data || !member) {
    return (
      <div className="flex flex-col h-screen overflow-hidden bg-bg-1">
        <AppBar folder={folder} picked={totalPicked} total={totalCount} onSettings={onSettings} onLibrary={onLibrary} />
        <div className="flex-1 flex items-center justify-center text-fg-2 text-sm" role="status" aria-live="polite">Loading…</div>
      </div>
    )
  }

  const exif = data.exif[String(member.file_id)] ?? {}
  const exifRows: [string, string][] = []
  if (exif.camera) exifRows.push(['Camera', exif.camera])
  if (exif.aperture) exifRows.push(['Aperture', exif.aperture])
  if (exif.shutter) exifRows.push(['Shutter', exif.shutter])
  if (exif.iso) exifRows.push(['ISO', exif.iso])
  if (exif.focal_length) exifRows.push(['Focal', exif.focal_length])
  if (exif.captured) exifRows.push(['Captured', exif.captured])
  if (exif.size) exifRows.push(['Size', exif.size])
  const hasGPS = typeof exif.gps_lat === 'number' && typeof exif.gps_lon === 'number'
  const gpsLabel = hasGPS ? `${exif.gps_lat!.toFixed(5)}, ${exif.gps_lon!.toFixed(5)}` : ''
  const gpsHref = hasGPS ? `https://www.google.com/maps/search/?api=1&query=${exif.gps_lat},${exif.gps_lon}` : ''

  const onPanStart = (e: React.PointerEvent<HTMLDivElement>) => {
    if (zoom <= 1) return
    dragState.current = { x: e.clientX, y: e.clientY, px: pan.x, py: pan.y }
    ;(e.target as Element).setPointerCapture?.(e.pointerId)
  }
  const onPanMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragState.current) return
    setPan({
      x: dragState.current.px + (e.clientX - dragState.current.x),
      y: dragState.current.py + (e.clientY - dragState.current.y),
    })
  }
  const onPanEnd = (e: React.PointerEvent<HTMLDivElement>) => {
    dragState.current = null
    ;(e.target as Element).releasePointerCapture?.(e.pointerId)
  }

  const comparePanes = compareIdx.flatMap(idx => members[idx] ? [{ m: members[idx], idx }] : [])
  const compareCols = comparePanes.length <= 1 ? 1 : comparePanes.length === 2 ? 2 : 2
  const compareRows = comparePanes.length <= 2 ? 1 : 2
  // What Space / X / Pick / Reject act on (see actionTarget): in compare mode
  // only the active pane, so nothing when `current` isn't among the panes.
  const target: Member | undefined = comparing && !compareIdx.includes(current) ? undefined : member
  const infoVisible = showInfo && (exifRows.length > 0 || hasGPS) && !comparing

  // `data` keeps the previous cluster's response until the next fetch lands,
  // so a mismatched id means we're mid-load and must not claim "reviewed".
  const loading = data.cluster.id !== clusterID
  const clusterDone = !loading && members.length > 0 && reviewed === members.length
  // Keep the end-of-cluster pill clear of native <video> controls, which sit
  // along the bottom edge of the hero / compare panes.
  const liftPill = comparing ? comparePanes.some(p => p.m.kind === 'video') : member.kind === 'video'

  return (
    <div className="flex flex-col h-screen overflow-hidden bg-bg-1">
      <AppBar folder={folder} picked={totalPicked} total={totalCount} onSettings={onSettings} onLibrary={onLibrary} />

      <div className="flex items-center gap-3 h-10 px-3.5 border-b border-line-1 bg-bg-1">
        <button className="btn btn-ghost" onClick={onBack}><ArrowL /> Library</button>
        <div className="w-px h-[18px] bg-line-1" />
        <span className="font-semibold text-[13px]">{data.cluster.device}</span>
        <span className="font-mono text-[11px] text-fg-2">{data.cluster.time}</span>
        <span className="flex-1" />
        <span className="font-mono text-[11px] text-fg-2">
          Cluster {data.cluster_index} / {data.cluster_count} · {reviewed}/{members.length} reviewed
        </span>
        {/* Key caps inside buttons are hidden from assistive tech (a glyph
            like ⇧↵ reads as noise in the name); aria-keyshortcuts exposes
            the shortcut instead. Same pattern in the footer. */}
        <button className="btn btn-ghost" disabled={!data.prev_cluster_id}
                onClick={goPrevCluster} data-testid="prev-cluster" aria-keyshortcuts="Shift+Enter">
          Prev cluster <span className="kbd" aria-hidden="true">⇧↵</span>
        </button>
        <button className={`btn ${clusterDone && data.next_cluster_id ? 'btn-primary' : 'btn-ghost'}`} disabled={!data.next_cluster_id}
                onClick={goNextCluster} data-testid="next-cluster" aria-keyshortcuts="Enter">
          Next cluster <span className="kbd" aria-hidden="true">↵</span>
        </button>
      </div>

      <div className="flex-1 flex min-h-0 relative">
        <aside ref={railRef} className="w-[132px] bg-bg-1 border-r border-line-1 overflow-y-auto py-2.5 px-2 flex flex-col gap-1.5" data-testid="rail"
               aria-label={comparing ? 'Cluster photos, select up to 4 to compare' : 'Cluster photos'}>
          {members.map((m, i) => {
            let state: string
            if (comparing && compareIdx.includes(i)) state = 'selected'
            else if (i === current) state = 'active'
            else if (m.picked) state = 'picked'
            else if (m.rejected) state = 'rejected'
            else state = 'default'
            // A rail click navigates normally (not a toggle, so no
            // aria-pressed) and toggles compare membership while comparing.
            // aria-current marks the photo Space / X act on — in compare mode
            // only when it is one of the panes, as with `target` below.
            const isCurrent = i === current && (!comparing || compareIdx.includes(i))
            return (
              <button key={m.file_id}
                      onClick={() => {
                        if (comparing) toggleCompareSelect(i)
                        else goTo(i)
                      }}
                      data-testid="rail-thumb"
                      data-file-id={m.file_id}
                      data-rail-index={i}
                      aria-label={`${m.kind === 'video' ? 'Video' : 'Photo'} ${i + 1}: ${m.path}${m.picked ? ' (picked)' : m.rejected ? ' (rejected)' : ''}`}
                      aria-pressed={comparing ? compareIdx.includes(i) : undefined}
                      aria-current={isCurrent ? 'true' : undefined}
                      className="bg-transparent border-0 p-0 cursor-pointer flex flex-col gap-[3px] outline-none focus:outline-none focus-visible:ring-2 focus-visible:ring-pick focus-visible:ring-offset-1 focus-visible:ring-offset-bg-1 rounded-sm">
                <div className="thumb w-full aspect-square" data-state={state}>
                  <img src={thumbURL(m)} alt={m.path} loading="lazy" />
                </div>
                <div className="flex items-center justify-between h-[13px] px-0.5 text-[11px] leading-none font-mono">
                  <span className={i === current ? 'text-fg-0' : 'text-fg-3'}>{String(i + 1).padStart(2, '0')}</span>
                  <MarkBadge m={m} />
                </div>
              </button>
            )
          })}
        </aside>

        <main ref={stageRef} className="flex-1 relative bg-bg-0 overflow-hidden">
          {comparing ? (
            <div className="absolute inset-0 grid p-3 gap-3"
                 style={{
                   gridTemplateColumns: `repeat(${compareCols}, minmax(0, 1fr))`,
                   gridTemplateRows: `repeat(${compareRows}, minmax(0, 1fr))`,
                 }}>
              {comparePanes.length === 0 && (
                <div className="col-span-full row-span-full flex items-center justify-center text-fg-2 text-xs">
                  Click rail thumbnails to add to compare (up to 4). Then 1–4 or ←/→ choose a pane; Space / X mark it.
                </div>
              )}
              {comparePanes.map(({ m, idx }, pos) => {
                const active = idx === current
                return (
                  <div key={m.file_id}
                       className="compare-pane relative bg-bg-1 rounded-md overflow-hidden border border-line-1 select-none"
                       data-testid="compare-pane"
                       data-active={active}
                       style={{ cursor: zoom > 1 ? (dragState.current ? 'grabbing' : 'grab') : (active ? 'default' : 'pointer') }}
                       onPointerDown={(e) => {
                         if (e.button === 0) paneDown.current = { x: e.clientX, y: e.clientY, idx }
                         onPanStart(e)
                       }}
                       onPointerMove={onPanMove}
                       onPointerUp={(e) => {
                         // A press-and-release that barely moved is a click
                         // (activate this pane); anything more was a pan drag
                         // over the shared zoom, which must not switch panes.
                         const d = paneDown.current
                         paneDown.current = null
                         onPanEnd(e)
                         if (d && d.idx === idx && Math.hypot(e.clientX - d.x, e.clientY - d.y) < CLICK_SLOP_PX) goTo(idx)
                       }}
                       onPointerCancel={(e) => { paneDown.current = null; onPanEnd(e) }}>
                    <CrossfadeImage member={m} variant="compare"
                                    pan={pan} zoom={zoom} dragging={!!dragState.current} />
                    {/* The hero's position overlay is hidden while comparing,
                        so each pane carries its own cluster position next to
                        the pane key — the same number its rail thumb shows. */}
                    <div className="absolute top-2 left-2 flex items-center gap-2 max-w-[calc(100%-16px)] px-2 py-1 rounded-md text-[11px] font-mono backdrop-blur-md z-10"
                         style={{ background: 'oklch(0 0 0 / 0.5)' }}>
                      <span className="kbd shrink-0">{pos + 1}</span>
                      <span className="shrink-0 text-fg-0">
                        {String(idx + 1).padStart(2, '0')} / {String(members.length).padStart(2, '0')}
                      </span>
                      <span className="shrink-0 w-px h-3 bg-white/10" />
                      <PathLabel path={m.path} />
                      {(m.picked || m.rejected) && <span className="shrink-0"><MarkBadge m={m} withText /></span>}
                    </div>
                  </div>
                )
              })}
            </div>
          ) : (
            <div className="absolute inset-0 p-6 select-none"
                 style={{ cursor: zoom > 1 ? (dragState.current ? 'grabbing' : 'grab') : 'default' }}
                 onPointerDown={onPanStart}
                 onPointerMove={onPanMove}
                 onPointerUp={onPanEnd}
                 onPointerCancel={onPanEnd}>
              <CrossfadeImage
                member={member}
                variant="hero"
                pan={pan}
                zoom={zoom}
                dragging={!!dragState.current}
              />
            </div>
          )}

          {/* The one photo-position counter (the footer no longer repeats it).
              Hidden while comparing: every pane carries its own key, position
              and path label in the same corner, and this overlay would sit on
              top of pane 1's. The width cap leaves room for the zoom / Info
              controls on the right; long paths ellipsise their folder part.
              Deliberately not a live region: announcing every ←/→ would be
              noise, and the hero image's name already changes. */}
          {!comparing && (
            <div className="absolute top-3 left-3 flex items-center gap-2 max-w-[max(160px,calc(100%-280px))] px-2.5 py-1.5 rounded-md border border-white/5 backdrop-blur-md"
                 style={{ background: 'oklch(0 0 0 / 0.5)' }}>
              <span className="shrink-0 font-mono text-[11px] text-fg-0">
                {String(current + 1).padStart(2, '0')} / {String(members.length).padStart(2, '0')}
              </span>
              <span className="shrink-0 w-px h-3 bg-white/10" />
              <PathLabel path={member.path} className="font-mono text-[11px] text-fg-1" />
            </div>
          )}

          <div className="absolute top-3 right-3 flex items-center gap-1.5">
            {member.kind !== 'video' && (
              <div className="flex items-center gap-1 h-[34px] px-2 rounded-md border border-white/5 backdrop-blur-md font-mono"
                   style={{ background: 'oklch(0 0 0 / 0.5)' }}>
                <button className="btn btn-ghost h-[22px] px-1.5" onClick={() => stepZoom(-1)} disabled={zoom <= MIN_ZOOM} title="Zoom out (-)"
                        aria-label="Zoom out">−</button>
                <span className="text-[11px] text-fg-0 min-w-[40px] text-center">{Math.round(zoom * 100)}%</span>
                <button className="btn btn-ghost h-[22px] px-1.5" onClick={() => stepZoom(1)} disabled={zoom >= MAX_ZOOM} title="Zoom in (+)"
                        aria-label="Zoom in">+</button>
              </div>
            )}
            {!comparing && (
              <button onClick={() => setShowInfo(!showInfo)} title="Toggle info panel (I)"
                      aria-pressed={showInfo} aria-keyshortcuts="I"
                      className={`btn h-[34px] ${showInfo ? '' : 'btn-ghost'} backdrop-blur-md`}
                      style={!showInfo ? { background: 'oklch(0 0 0 / 0.5)' } : undefined}>
                <PanelIcon /> Info <span className="kbd" aria-hidden="true">I</span>
              </button>
            )}
          </div>

          {infoVisible && (
            <aside className="absolute top-14 right-3 bottom-3 w-[280px] rounded-lg border border-line-2 backdrop-blur-xl shadow-pop overflow-hidden flex flex-col"
                   style={{ background: 'oklch(0.20 0.005 240 / 0.92)' }}>
              <div className="px-3 py-2.5 border-b border-line-1 flex items-center gap-2">
                <span className="eyebrow">EXIF</span>
                <span className="flex-1" />
                <button className="btn btn-ghost h-[22px] px-1.5" onClick={() => setShowInfo(false)}
                        title="Close info panel (I)" aria-label="Close info panel"><XIcon /></button>
              </div>
              <div className="p-3 overflow-y-auto flex-1">
                <dl className="m-0 grid grid-cols-[1fr_auto] gap-x-2.5 gap-y-1 text-[11px]">
                  {exifRows.map(([k, v]) => (
                    <React.Fragment key={k}>
                      <dt className="text-fg-2">{k}</dt>
                      <dd className="m-0 text-fg-0 font-mono text-[11px]">{v}</dd>
                    </React.Fragment>
                  ))}
                  {hasGPS && (
                    <React.Fragment>
                      <dt className="text-fg-2">Coords</dt>
                      <dd className="m-0 text-fg-0 font-mono text-[11px]">
                        <a href={gpsHref}
                           target="_blank"
                           rel="noopener noreferrer"
                           title="Open in Google Maps (new tab)"
                           aria-label={`${gpsLabel}, open in Google Maps (opens in a new tab)`}
                           className="text-pick hover:underline">
                          {gpsLabel}
                        </a>
                      </dd>
                    </React.Fragment>
                  )}
                </dl>
              </div>
            </aside>
          )}

          {/* End-of-cluster cue. ←/→ and advance-on-action still wrap silently;
              this pill just tells the user there's nothing left to mark here.
              The wrapper stops short of the Info panel so the pill can never
              cover it, is click-through except for the pill itself, and the
              pill never takes focus (keyboard users already have ↵ / Esc).
              The wrapper stays mounted so the live region announces the pill. */}
          {!loading && (
            <div className={`absolute left-3 flex justify-center pointer-events-none ${infoVisible ? 'right-[304px]' : 'right-3'} ${liftPill ? 'bottom-20' : 'bottom-3'}`}
                 role="status" aria-live="polite">
              {clusterDone && (
                <button type="button" data-testid="cluster-done" tabIndex={-1}
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={data.next_cluster_id ? goNextCluster : onBack}
                        className="pointer-events-auto flex items-center gap-2 px-2.5 py-1.5 rounded-md border border-white/5 backdrop-blur-md cursor-pointer font-mono text-[11px] text-fg-0 hover:border-white/15"
                        style={{ background: 'oklch(0 0 0 / 0.5)' }}>
                  <CheckIcon className="text-pick" size={12} />
                  <span>{data.next_cluster_id ? 'Cluster reviewed' : 'Last cluster reviewed'}</span>
                  {/* Hidden from the live announcement so it reads the words
                      ("Cluster reviewed … Next cluster"), not a key glyph. */}
                  <span className="w-px h-3 bg-white/10" aria-hidden="true" />
                  <span className="kbd" aria-hidden="true">{data.next_cluster_id ? '↵' : 'Esc'}</span>
                  <span className="text-fg-1">{data.next_cluster_id ? 'Next cluster' : 'Library'}</span>
                </button>
              )}
            </div>
          )}
        </main>
      </div>

      <footer className="h-12 px-3 border-t border-line-1 bg-bg-2 flex items-center gap-1.5">
        {/* In compare mode ←/→ cycle the active pane, so they're useful with ≥2 panes. */}
        <button className="btn btn-ghost" onClick={() => step(-1)} disabled={comparing ? comparePanes.length <= 1 : members.length <= 1}
                aria-label={comparing ? 'Previous pane' : 'Previous photo'} aria-keyshortcuts="ArrowLeft">
          <ArrowL /> <span className="kbd" aria-hidden="true">←</span>
        </button>
        <button className="btn btn-ghost" onClick={() => step(1)} disabled={comparing ? comparePanes.length <= 1 : members.length <= 1}
                aria-label={comparing ? 'Next pane' : 'Next photo'} aria-keyshortcuts="ArrowRight">
          <span className="kbd" aria-hidden="true">→</span> <ArrowR />
        </button>
        <div className="w-px h-[22px] bg-line-1 mx-1.5" />
        {/* Pick / Reject keep their state-dependent text ("Picked") for
            sighted users; aria-pressed carries the same state to AT. */}
        <button className={`btn ${target?.picked ? 'btn-pick-active' : ''}`} onClick={() => toggle('picked')} data-testid="pick-btn" disabled={!target || inflightIDs.has(target.file_id)}
                aria-pressed={!!target?.picked} aria-keyshortcuts="Space">
          <CheckIcon /> {target?.picked ? 'Picked' : 'Pick'} <span className="kbd" aria-hidden="true">Space</span>
        </button>
        <button className={`btn ${target?.rejected ? 'btn-reject-active' : ''}`} onClick={() => toggle('rejected')} data-testid="reject-btn" disabled={!target || inflightIDs.has(target.file_id)}
                aria-pressed={!!target?.rejected} aria-keyshortcuts="X">
          <XIcon /> {target?.rejected ? 'Rejected' : 'Reject'} <span className="kbd" aria-hidden="true">X</span>
        </button>
        <div className="w-px h-[22px] bg-line-1 mx-1.5" />
        <button className={`btn ${comparing ? 'btn-pick-active' : 'btn-ghost'}`} onClick={toggleCompare} data-testid="compare-btn"
                aria-pressed={comparing} aria-keyshortcuts="C">
          <CompareIcon /> Compare <span className="kbd" aria-hidden="true">C</span>
        </button>
        {member.kind !== 'video' && (
          <button className={`btn ${zoom > 1 ? 'btn-pick-active' : 'btn-ghost'}`} onClick={() => { if (zoom > 1) { setZoom(1); setPan({ x: 0, y: 0 }) } else { setZoom(2) } }} data-testid="zoom-btn" disabled={comparing}
                  aria-pressed={zoom > 1} aria-keyshortcuts="Z">
            <ZoomIcon /> Pixel-peep <span className="kbd" aria-hidden="true">Z</span>
          </button>
        )}
        <span className="flex-1" />
        <button className="btn btn-ghost" onClick={onBack} aria-keyshortcuts="Escape"><ArrowL /> Back <span className="kbd" aria-hidden="true">Esc</span></button>
      </footer>
    </div>
  )
}
