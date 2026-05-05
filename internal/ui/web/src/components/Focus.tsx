import React, { useEffect, useState, useCallback, useMemo, useRef } from 'react'
import {
  AppBar,
  ArrowL, ArrowR, CheckIcon, XIcon, PanelIcon, ZoomIcon, CompareIcon, Spinner,
} from './primitives'
import { fetchCluster, postPick, thumbURL, fullURL, type FocusResponse, type Member } from '../api'
import { toast } from './Toaster'

const MIN_ZOOM = 1
const MAX_ZOOM = 8
const ZOOM_STEP = 1.5 // multiplicative step for the +/- buttons and Z key

const clampZoom = (z: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z))

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
  const isVideo = member.kind === 'video'
  const cacheKey = `${member.file_id}-${member.content_hash}`

  useEffect(() => { setThumbReady(false); setFullReady(false) }, [cacheKey])

  // Videos never use pan/zoom — always render at natural size.
  const transform = isVideo ? 'none' : `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`
  const transitionDuration = dragging ? '0ms' : '120ms'
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
        alt={member.path}
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
        controls
        draggable={false}
        onCanPlay={() => setFullReady(true)}
        className={variant === 'compare'
          ? 'absolute inset-0 w-full h-full object-contain bg-bg-0'
          : 'absolute inset-0 w-full h-full object-contain'}
        style={{ opacity: fullReady ? 1 : 0, transition: 'opacity 180ms' }}
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
        alt={member.path}
        draggable={false}
        onLoad={() => setThumbReady(true)}
        className={imgCls}
        style={{
          transform, transformOrigin: 'center center', transitionDuration,
          opacity: fullReady ? 0 : (thumbReady ? 1 : 0),
          pointerEvents: 'none',
        }}
      />
      <img
        key={`full-${cacheKey}`}
        src={fullURL(member)}
        alt={member.path}
        draggable={false}
        onLoad={() => setFullReady(true)}
        className={`${imgCls.replace('transition-transform', 'transition-opacity')}`}
        style={{
          transform, transformOrigin: 'center center',
          transitionDuration: '180ms',
          opacity: fullReady ? 1 : 0,
          pointerEvents: 'none',
        }}
        data-testid={variant === 'hero' ? 'hero' : undefined}
      />
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
  const [members, setMembers] = useState<Member[]>([])
  const [zoom, setZoom] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [comparing, setComparing] = useState(false)
  const [compareIdx, setCompareIdx] = useState<number[]>([])
  // inflightRef is checked synchronously in toggle (including keyboard autorepeat paths).
  // inflightIDs mirrors it as state only so the Pick/Reject buttons can be disabled.
  const inflightRef = useRef<Set<number>>(new Set())
  const [inflightIDs, setInflightIDs] = useState<Set<number>>(new Set())
  const dragState = useRef<{ x: number; y: number; px: number; py: number } | null>(null)
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
      target.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
    }
  }, [current])

  const load = useCallback(async () => {
    // Cancel any previous in-flight fetch so stale responses don't overwrite
    // fresh ones when navigating clusters rapidly.
    if (abortRef.current) abortRef.current.abort()
    const ctrl = new AbortController()
    abortRef.current = ctrl

    let d: FocusResponse
    try {
      d = await fetchCluster(clusterID, ctrl.signal)
    } catch (e) {
      if (ctrl.signal.aborted) return
      throw e
    }
    if (ctrl.signal.aborted) return

    setData(d)
    setMembers(d.cluster.members)
    if (startFileID !== undefined) {
      const idx = d.cluster.members.findIndex(m => m.file_id === startFileID)
      // If the requested fileID is missing (cluster rebuilt), fall back to first.
      setCurrent(idx >= 0 ? idx : 0)
    } else {
      setCurrent(0)
    }
  }, [clusterID, startFileID])

  useEffect(() => { load().catch(e => toast.reject('Load cluster failed', { body: String(e) })) }, [load])

  // Reset zoom + pan whenever the active photo changes.
  useEffect(() => { setZoom(1); setPan({ x: 0, y: 0 }) }, [current])

  const member = members[current]

  const toggle = useCallback(async (k: 'picked' | 'rejected') => {
    if (!member) return
    // Per-file-id in-flight guard: checked synchronously via ref so keyboard
    // autorepeat (rapid Space presses) can't queue a second mutation before
    // React commits the state update from the first.
    if (inflightRef.current.has(member.file_id)) return
    inflightRef.current.add(member.file_id)
    const wasOn = member[k]
    const action = k === 'picked' ? (wasOn ? 'unpick' : 'pick') : (wasOn ? 'unreject' : 'reject')
    // Mirror to state so the Pick/Reject buttons reflect the in-flight status.
    setInflightIDs(s => new Set(s).add(member.file_id))
    try {
      await postPick(member.file_id, action)
      setMembers(ms => ms.map((m, i) => i === current ? {
        ...m,
        picked: k === 'picked' ? !wasOn : (k === 'rejected' && !wasOn ? false : m.picked),
        rejected: k === 'rejected' ? !wasOn : (k === 'picked' && !wasOn ? false : m.rejected),
      } : m))
      onChanged()
      toast.ok(action === 'pick' ? 'Picked' : action === 'reject' ? 'Rejected' : 'Cleared', { body: member.path })
      // Auto-advance on accept/reject (not on un-accept/un-reject), wrapping at the end.
      if (advanceOnAction && !wasOn && members.length > 1) {
        setCurrent(c => (c + 1) % members.length)
      }
    } catch (e) {
      toast.reject('Action failed', { body: String(e) })
    } finally {
      inflightRef.current.delete(member.file_id)
      setInflightIDs(s => { const n = new Set(s); n.delete(member.file_id); return n })
    }
  }, [member, current, advanceOnAction, members.length, onChanged])

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
    setComparing(c => {
      const next = !c
      if (next) {
        setCompareIdx(prev => prev.length > 0 ? prev : [current])
      }
      return next
    })
  }, [current])

  const toggleCompareSelect = useCallback((i: number) => {
    setCompareIdx(prev => {
      if (prev.includes(i)) return prev.filter(x => x !== i)
      if (prev.length >= 4) return prev // hard cap so the grid stays readable
      return [...prev, i]
    })
  }, [])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA') return
      if (e.key === 'Escape') {
        e.preventDefault()
        if (zoom > 1) { setZoom(1); setPan({ x: 0, y: 0 }); return }
        if (comparing) { setComparing(false); return }
        onBack()
        return
      }
      if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
        e.preventDefault()
        if (members.length > 0) setCurrent(c => (c - 1 + members.length) % members.length)
        return
      }
      if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
        e.preventDefault()
        if (members.length > 0) setCurrent(c => (c + 1) % members.length)
        return
      }
      if (e.key === ' ' || e.code === 'Space') { e.preventDefault(); toggle('picked'); return }
      if (e.key === 'x' || e.key === 'X') { e.preventDefault(); toggle('rejected'); return }
      if (e.key === 'i' || e.key === 'I') { e.preventDefault(); setShowInfo(v => !v); return }
      if (e.key === 'z' || e.key === 'Z') {
        e.preventDefault()
        // Toggle: if already zoomed, snap back to 1×; otherwise jump to 2×.
        if (zoom > 1) { setZoom(1); setPan({ x: 0, y: 0 }) } else { setZoom(2) }
        return
      }
      if (e.key === 'c' || e.key === 'C') { e.preventDefault(); toggleCompare(); return }
      if (e.key === '+' || e.key === '=') { e.preventDefault(); stepZoom(1); return }
      if (e.key === '-' || e.key === '_') { e.preventDefault(); stepZoom(-1); return }
      if (e.key === '0') { e.preventDefault(); setZoom(1); setPan({ x: 0, y: 0 }); return }
      if (e.key === 'Enter') {
        e.preventDefault()
        if (data?.next_cluster_id) onCluster(data.next_cluster_id)
        return
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [members.length, toggle, onBack, onCluster, data, zoom, stepZoom, comparing, toggleCompare])

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
        <div className="flex-1 flex items-center justify-center text-fg-2 text-sm">Loading…</div>
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

  const compareMembers = compareIdx.map(i => members[i]).filter(Boolean)
  const compareCols = compareMembers.length <= 1 ? 1 : compareMembers.length === 2 ? 2 : 2
  const compareRows = compareMembers.length <= 2 ? 1 : 2

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
        <button className="btn btn-ghost" disabled={!data.next_cluster_id}
                onClick={() => data.next_cluster_id && onCluster(data.next_cluster_id)}>
          Next cluster <span className="kbd">↵</span>
        </button>
      </div>

      <div className="flex-1 flex min-h-0 relative">
        <aside ref={railRef} className="w-[132px] bg-bg-1 border-r border-line-1 overflow-y-auto py-2.5 px-2 flex flex-col gap-1.5" data-testid="rail">
          {members.map((m, i) => {
            let state: string
            if (comparing && compareIdx.includes(i)) state = 'selected'
            else if (i === current) state = 'active'
            else if (m.picked) state = 'picked'
            else if (m.rejected) state = 'rejected'
            else state = 'default'
            return (
              <button key={m.file_id}
                      onClick={() => {
                        if (comparing) toggleCompareSelect(i)
                        else setCurrent(i)
                      }}
                      data-testid="rail-thumb"
                      data-file-id={m.file_id}
                      data-rail-index={i}
                      aria-label={`${m.kind === 'video' ? 'Video' : 'Photo'} ${i + 1}: ${m.path}${m.picked ? ' (picked)' : m.rejected ? ' (rejected)' : ''}`}
                      aria-pressed={i === current}
                      className="bg-transparent border-0 p-0 cursor-pointer flex flex-col gap-[3px] outline-none focus:outline-none focus-visible:ring-2 focus-visible:ring-pick focus-visible:ring-offset-1 focus-visible:ring-offset-bg-1 rounded-sm">
                <div className="thumb w-full aspect-square" data-state={state}>
                  <img src={thumbURL(m)} alt={m.path} loading="lazy" />
                </div>
                <div className="flex justify-between px-0.5 text-[9px] font-mono">
                  <span className={i === current ? 'text-fg-0' : 'text-fg-3'}>{String(i + 1).padStart(2, '0')}</span>
                  <span>
                    {m.picked && <span className="text-pick">●</span>}
                    {m.rejected && <span className="text-reject">●</span>}
                  </span>
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
              {compareMembers.length === 0 && (
                <div className="col-span-full row-span-full flex items-center justify-center text-fg-2 text-xs">
                  Click rail thumbnails to add to compare. Up to 4.
                </div>
              )}
              {compareMembers.map((m) => (
                <div key={m.file_id}
                     className="relative bg-bg-1 rounded-md overflow-hidden border border-line-1 select-none"
                     style={{ cursor: zoom > 1 ? (dragState.current ? 'grabbing' : 'grab') : 'default' }}
                     onPointerDown={onPanStart}
                     onPointerMove={onPanMove}
                     onPointerUp={onPanEnd}
                     onPointerCancel={onPanEnd}>
                  <CrossfadeImage member={m} variant="compare"
                                  pan={pan} zoom={zoom} dragging={!!dragState.current} />
                  <div className="absolute top-2 left-2 px-2 py-1 rounded-md text-[11px] font-mono backdrop-blur-md z-10"
                       style={{ background: 'oklch(0 0 0 / 0.5)' }}>
                    {m.path}
                    {m.picked && <span className="ml-2 text-pick">● Pick</span>}
                    {m.rejected && <span className="ml-2 text-reject">● Reject</span>}
                  </div>
                </div>
              ))}
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

          <div className="absolute top-3 left-3 flex items-center gap-2 px-2.5 py-1.5 rounded-md border border-white/5 backdrop-blur-md"
               style={{ background: 'oklch(0 0 0 / 0.5)' }}>
            <span className="font-mono text-[11px] text-fg-0">
              {String(current + 1).padStart(2, '0')} / {String(members.length).padStart(2, '0')}
            </span>
            <span className="w-px h-3 bg-white/10" />
            <span className="font-mono text-[11px] text-fg-1">{member.path}</span>
          </div>

          <div className="absolute top-3 right-3 flex items-center gap-1.5">
            <div className="flex items-center gap-1 h-[34px] px-2 rounded-md border border-white/5 backdrop-blur-md font-mono"
                 style={{ background: 'oklch(0 0 0 / 0.5)' }}>
              <button className="btn btn-ghost h-[22px] px-1.5" onClick={() => stepZoom(-1)} disabled={zoom <= MIN_ZOOM} title="Zoom out (-)">−</button>
              <span className="text-[11px] text-fg-0 min-w-[40px] text-center">{Math.round(zoom * 100)}%</span>
              <button className="btn btn-ghost h-[22px] px-1.5" onClick={() => stepZoom(1)} disabled={zoom >= MAX_ZOOM} title="Zoom in (+)">+</button>
            </div>
            {!comparing && (
              <button onClick={() => setShowInfo(!showInfo)} title="Toggle info panel (I)"
                      className={`btn h-[34px] ${showInfo ? '' : 'btn-ghost'} backdrop-blur-md`}
                      style={!showInfo ? { background: 'oklch(0 0 0 / 0.5)' } : undefined}>
                <PanelIcon /> Info <span className="kbd">I</span>
              </button>
            )}
          </div>

          {showInfo && (exifRows.length > 0 || hasGPS) && !comparing && (
            <aside className="absolute top-14 right-3 bottom-3 w-[280px] rounded-lg border border-line-2 backdrop-blur-xl shadow-pop overflow-hidden flex flex-col"
                   style={{ background: 'oklch(0.20 0.005 240 / 0.92)' }}>
              <div className="px-3 py-2.5 border-b border-line-1 flex items-center gap-2">
                <span className="eyebrow">EXIF</span>
                <span className="flex-1" />
                <button className="btn btn-ghost h-[22px] px-1.5" onClick={() => setShowInfo(false)}><XIcon /></button>
              </div>
              <div className="p-3 overflow-y-auto flex-1">
                <dl className="m-0 grid grid-cols-[1fr_auto] gap-x-2.5 gap-y-1 text-[11px]">
                  {exifRows.map(([k, v]) => (
                    <React.Fragment key={k}>
                      <dt className="text-fg-2">{k}</dt>
                      <dd className="m-0 text-fg-0 font-mono text-[10px]">{v}</dd>
                    </React.Fragment>
                  ))}
                  {hasGPS && (
                    <React.Fragment>
                      <dt className="text-fg-2">Coords</dt>
                      <dd className="m-0 text-fg-0 font-mono text-[10px]">
                        <a href={gpsHref}
                           target="_blank"
                           rel="noopener noreferrer"
                           title="Open in Google Maps"
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
        </main>
      </div>

      <footer className="h-12 px-3 border-t border-line-1 bg-bg-2 flex items-center gap-1.5">
        <button className="btn btn-ghost" onClick={() => setCurrent(c => (c - 1 + members.length) % members.length)} disabled={members.length <= 1 || comparing}>
          <ArrowL /> <span className="kbd">←</span>
        </button>
        <button className="btn btn-ghost" onClick={() => setCurrent(c => (c + 1) % members.length)} disabled={members.length <= 1 || comparing}>
          <span className="kbd">→</span> <ArrowR />
        </button>
        <div className="w-px h-[22px] bg-line-1 mx-1.5" />
        <button className={`btn ${member.picked ? 'btn-pick-active' : ''}`} onClick={() => toggle('picked')} data-testid="pick-btn" disabled={comparing || inflightIDs.has(member.file_id)}>
          <CheckIcon /> {member.picked ? 'Picked' : 'Pick'} <span className="kbd">Space</span>
        </button>
        <button className={`btn ${member.rejected ? 'btn-reject-active' : ''}`} onClick={() => toggle('rejected')} data-testid="reject-btn" disabled={comparing || inflightIDs.has(member.file_id)}>
          <XIcon /> {member.rejected ? 'Rejected' : 'Reject'} <span className="kbd">X</span>
        </button>
        <div className="w-px h-[22px] bg-line-1 mx-1.5" />
        <button className={`btn ${comparing ? 'btn-pick-active' : 'btn-ghost'}`} onClick={toggleCompare} data-testid="compare-btn">
          <CompareIcon /> Compare <span className="kbd">C</span>
        </button>
        <button className={`btn ${zoom > 1 ? 'btn-pick-active' : 'btn-ghost'}`} onClick={() => { if (zoom > 1) { setZoom(1); setPan({ x: 0, y: 0 }) } else { setZoom(2) } }} data-testid="zoom-btn" disabled={comparing || member.kind === 'video'} title={member.kind === 'video' ? 'Zoom not available for videos' : undefined}>
          <ZoomIcon /> Pixel-peep <span className="kbd">Z</span>
        </button>
        <span className="flex-1" />
        <span className="font-mono text-[11px] text-fg-2">
          {String(current + 1).padStart(2, '0')} / {String(members.length).padStart(2, '0')}
        </span>
        <div className="w-px h-[22px] bg-line-1 mx-1.5" />
        <button className="btn btn-ghost" onClick={onBack}><ArrowL /> Back <span className="kbd">Esc</span></button>
      </footer>
    </div>
  )
}
