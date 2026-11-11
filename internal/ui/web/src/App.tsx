import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Library, DEFAULT_LIBRARY_FILTERS, type LibraryFilters } from './components/Library'
import { Focus } from './components/Focus'
import { Settings } from './components/Settings'
import { Empty } from './components/Empty'
import { Toaster, toast } from './components/Toaster'
import { ShortcutsOverlay, handleShortcutsKeydown } from './components/Shortcuts'
import { ScanProgressContext, QualityContext, type ScanProgress, type QualityContextValue } from './components/primitives'
import {
  fetchLibrary, fetchSettings, fetchQuality, subscribeProgress,
  type LibraryResponse, type ProgressEvent, type SettingsBody, type QualityStatus,
} from './api'

type Route =
  | { name: 'library' }
  | { name: 'focus'; clusterID: number; fileID?: number }
  | { name: 'settings' }

function parseRoute(): Route {
  const p = window.location.pathname
  const m = /^\/focus\/(\d+)/.exec(p)
  if (m) {
    const params = new URLSearchParams(window.location.search)
    const fid = params.get('file')
    return { name: 'focus', clusterID: +m[1], fileID: fid ? +fid : undefined }
  }
  if (p.startsWith('/settings')) return { name: 'settings' }
  return { name: 'library' }
}

// How long a scan must be running before the AppBar indicator appears. The
// SSE hub replays its buffered events to every new subscriber, so a page
// opened after a finished scan receives a burst of stale stage events ending
// in cluster/done; without the delay that burst would flash the indicator.
// Real scans run for seconds to minutes, so the delay costs nothing.
const SCAN_INDICATOR_DELAY_MS = 400

// Quality worker polling. The worker's state is a snapshot endpoint, not
// part of the SSE stream, so the App polls it — but only while the snapshot
// says `active` (work in flight or queued, or a scan running); otherwise it
// is fetched once on load, when a scan starts or finishes and after a
// settings save. `active` is set synchronously on the server when work is
// requested, so a single fetch after a trigger never misses it.
const QUALITY_POLL_MS = 1000
// While photos are being rated, new ratings are pulled into the Library at
// most this often (and once more when the pass ends).
const RATINGS_REFRESH_MS = 10_000

function sameQuality(a: QualityStatus | null, b: QualityStatus): boolean {
  return !!a && a.enabled === b.enabled && a.state === b.state && a.active === b.active
    && a.rated === b.rated && a.received === b.received
    && a.size === b.size && a.done === b.done && a.total === b.total && a.error === b.error
}

export const App: React.FC = () => {
  const [route, setRoute] = useState<Route>(parseRoute())
  const [library, setLibrary] = useState<LibraryResponse | null>(null)
  const [picksDir, setPicksDir] = useState('./picks/')
  const [advanceOnAction, setAdvanceOnAction] = useState(false)
  const [progress, setProgress] = useState<ProgressEvent | null>(null)
  const [scanDone, setScanDone] = useState(false)
  const [quality, setQuality] = useState<QualityStatus | null>(null)
  const libraryScrollRef = useRef(0)
  const libraryActiveIDRef = useRef<number | null>(null)
  // Library's query/device/sort, remembered for the page lifetime so that
  // Library → Focus/Settings → back restores the same filtered view the saved
  // scroll offset and active cluster refer to. A ref (initial value in,
  // changes mirrored out) rather than lifted useState: Library still owns the
  // live state, so typing in the filter box re-renders only Library, never
  // the whole App. Deliberately not localStorage — a filter left over from
  // another folder or session would be confusing.
  const libraryFiltersRef = useRef<LibraryFilters>(DEFAULT_LIBRARY_FILTERS)
  const lastProgressKey = useRef('')

  // `background` = nobody asked (new ratings arrived): the request is marked
  // so it does not postpone idle shutdown.
  const refreshLibrary = useCallback(async (background = false) => {
    try {
      const d = await fetchLibrary(background)
      setLibrary(d)
    } catch (e) {
      toast.reject('Library load failed', { body: String(e) })
    }
  }, [])

  // What screens get as their "refresh the Library" callback. A wrapper so
  // that whatever a caller passes (a click event, say) can never be taken
  // for refreshLibrary's `background` flag.
  const refreshLibraryNow = useCallback(() => refreshLibrary(), [refreshLibrary])

  // Stable identity so Library's mirror effect only fires on real filter
  // changes, not on every App re-render (e.g. each SSE progress tick).
  const onLibraryFiltersChange = useCallback((f: LibraryFilters) => {
    libraryFiltersRef.current = f
  }, [])

  const applySettings = useCallback((s: SettingsBody) => {
    setPicksDir(s.picks_dir)
    setAdvanceOnAction(!!s.advance_on_action)
  }, [])

  // Poll the quality worker: one fetch, then keep going once a second while
  // the snapshot says `active`. Any caller may kick it.
  const qualityTimer = useRef<number | undefined>(undefined)
  const qualityGen = useRef(0)
  const refreshQuality = useCallback(async () => {
    // Each call supersedes the previous one. Responses can arrive out of
    // order (a poll overlapping a settings save), and an older snapshot
    // must neither overwrite a newer one nor decide whether to keep
    // polling — so only the latest call may touch state and the timer.
    const gen = ++qualityGen.current
    window.clearTimeout(qualityTimer.current)
    let s: QualityStatus
    try {
      s = await fetchQuality()
    } catch {
      return // server unreachable (shutting down): stop quietly
    }
    if (gen !== qualityGen.current) return
    setQuality(prev => (sameQuality(prev, s) ? prev : s))
    if (s.active) {
      qualityTimer.current = window.setTimeout(refreshQuality, QUALITY_POLL_MS)
    }
  }, [])
  useEffect(() => () => { qualityGen.current++; window.clearTimeout(qualityTimer.current) }, [])

  useEffect(() => {
    refreshLibrary()
    refreshQuality()
    fetchSettings().then(applySettings).catch(() => { /* ignore */ })
  }, [refreshLibrary, refreshQuality, applySettings])

  // Bring freshly computed ratings into the Library. The trigger is the
  // worker's `rated` counter, which only grows: whenever it differs from the
  // value we last refreshed at, there are ratings we have not fetched —
  // regardless of which states the polls happened to see (a short pass can
  // start and finish between two polls). While the worker is still active
  // the refresh is throttled; the first poll after it goes quiet, or after
  // the throttle expires, catches up. Badges are absolutely positioned, so
  // this never shifts layout. `ratingsRev` tells Focus the same thing.
  const ratingsSeen = useRef({ rated: 0, at: 0 })
  const [ratingsRev, setRatingsRev] = useState(0)
  useEffect(() => {
    if (!quality) return
    const seen = ratingsSeen.current
    if (quality.rated === seen.rated) return
    const now = Date.now()
    if (quality.active && now - seen.at < RATINGS_REFRESH_MS) return
    seen.rated = quality.rated
    seen.at = now
    setRatingsRev(quality.rated)
    refreshLibrary(true)
  }, [quality, refreshLibrary])

  useEffect(() => {
    const close = subscribeProgress(ev => {
      // The pipeline emits one event per processed file (~6,000 for a
      // 2,000-photo scan). Skip identical-shape events so the App tree
      // doesn't reconcile thousands of times during a single scan.
      const key = `${ev.stage}|${ev.phase ?? ''}|${ev.done}|${ev.total}|${ev.failed}`
      if (key === lastProgressKey.current) return
      lastProgressKey.current = key
      setProgress(ev)
      if (ev.stage === 'cluster' && ev.phase === 'done') {
        setScanDone(true)
        refreshLibrary()
        // The scan may have produced photos to rate; the worker is woken
        // server-side, so start watching it.
        refreshQuality()
      }
    })
    return close
  }, [refreshLibrary, refreshQuality])

  useEffect(() => {
    const onPop = () => setRoute(parseRoute())
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  // Block browser-level pinch-zoom (trackpad pinch, Safari gestures, ⌘+wheel)
  // app-wide. Focus mode re-enables the gesture inside its hero/compare area
  // by attaching its own non-passive listeners that update the photo zoom
  // instead of letting the browser scale the page.
  useEffect(() => {
    const wheel = (e: WheelEvent) => { if (e.ctrlKey || e.metaKey) e.preventDefault() }
    const gesture = (e: Event) => e.preventDefault()
    document.addEventListener('wheel', wheel, { passive: false })
    document.addEventListener('gesturestart', gesture)
    document.addEventListener('gesturechange', gesture)
    document.addEventListener('gestureend', gesture)
    return () => {
      document.removeEventListener('wheel', wheel)
      document.removeEventListener('gesturestart', gesture)
      document.removeEventListener('gesturechange', gesture)
      document.removeEventListener('gestureend', gesture)
    }
  }, [])

  const navigate = useCallback((path: string, r: Route) => {
    if (window.location.pathname + window.location.search !== path) {
      window.history.pushState(null, '', path)
    }
    setRoute(r)
  }, [])

  const goLibrary = useCallback(() => navigate('/', { name: 'library' }), [navigate])
  const goSettings = useCallback(() => navigate('/settings', { name: 'settings' }), [navigate])
  const goFocus = useCallback((clusterID: number, fileID?: number) => {
    const q = fileID ? `?file=${fileID}` : ''
    navigate(`/focus/${clusterID}${q}`, { name: 'focus', clusterID, fileID })
  }, [navigate])

  // The "?" shortcuts overlay's key handling. Capture phase on window, from a
  // layout effect: layout effects run before any child's passive useEffect,
  // so this listener is registered ahead of Focus's capture-phase keydown
  // listener and can swallow keys (Esc, Space, …) while the dialog is open
  // before Focus would act on them. See handleShortcutsKeydown.
  useLayoutEffect(() => {
    window.addEventListener('keydown', handleShortcutsKeydown, { capture: true })
    return () => window.removeEventListener('keydown', handleShortcutsKeydown, { capture: true })
  }, [])

  const folder = library?.folder ?? '…'
  const picked = library?.picked ?? 0
  const total = library?.total ?? 0

  // A scan is in progress if there is a progress event whose stage is not yet
  // at cluster/done. Once we observe cluster/done, isScanning becomes false.
  const isScanning = progress !== null && !(progress.stage === 'cluster' && progress.phase === 'done')
  // Show "scanning" when actively processing. Show "empty" only after the scan
  // has fully completed (cluster/done seen) and there are still no clusters.
  const showEmptyScanning = library !== null && library.clusters.length === 0 && isScanning
  const showEmptyDone = library !== null && library.clusters.length === 0 && scanDone && !isScanning

  // A scan that starts later (e.g. after the CLIP model is installed) will
  // hand the quality worker new photos: start watching it again.
  useEffect(() => { if (isScanning) refreshQuality() }, [isScanning, refreshQuality])

  // AppBar scan indicator: appears once a scan has been running for
  // SCAN_INDICATOR_DELAY_MS, disappears the moment it finishes. Keyed on the
  // isScanning boolean only, so per-file ticks don't restart the timer.
  const [scanVisible, setScanVisible] = useState(false)
  useEffect(() => {
    if (!isScanning) { setScanVisible(false); return }
    const t = setTimeout(() => setScanVisible(true), SCAN_INDICATOR_DELAY_MS)
    return () => clearTimeout(t)
  }, [isScanning])
  const scan = useMemo<ScanProgress | null>(() => {
    // isScanning is checked here as well as through scanVisible so the
    // indicator hides on the cluster/done render itself, not one effect later.
    if (!scanVisible || !isScanning || !progress) return null
    return {
      stage: progress.stage,
      done: progress.done,
      total: progress.total,
      failed: progress.failed,
      // A "failed" phase is terminal: the pipeline returns right after
      // emitting it, so no further events will arrive for this run.
      state: progress.phase === 'failed' ? 'stopped' : 'running',
    }
  }, [scanVisible, isScanning, progress])

  const qualityCtx = useMemo<QualityContextValue>(() => ({ status: quality, refresh: refreshQuality }), [quality, refreshQuality])
  // file_id → rating, from the Library payload. Focus reads ratings from here
  // so they update live without replacing its member objects (which its image
  // loader and prefetcher key on).
  const ratings = useMemo(() => {
    const m = new Map<number, number>()
    for (const c of library?.clusters ?? []) {
      for (const mem of c.members) if (typeof mem.quality === 'number') m.set(mem.file_id, mem.quality)
    }
    return m
  }, [library])
  const qualityEnabled = !!quality?.enabled

  // Every route renders inside the same wrapper so Toaster (and the
  // overlay) keep a stable position in the tree and never remount — a
  // remount would drop live toasts and briefly unregister toast().
  let body: React.ReactNode
  if (!library) {
    body = <Empty folder={folder} mode="scanning" progress={progress} onSettings={goSettings} onLibrary={goLibrary} />
  } else if (showEmptyScanning && route.name === 'library') {
    body = <Empty folder={library.folder} mode="scanning" progress={progress} onSettings={goSettings} onLibrary={goLibrary} />
  } else if (showEmptyDone && route.name === 'library') {
    body = <Empty folder={library.folder} mode="empty" onSettings={goSettings} onLibrary={goLibrary} />
  } else if (route.name === 'focus') {
    body = (
      <Focus
        clusterID={route.clusterID}
        startFileID={route.fileID}
        totalPicked={picked}
        totalCount={total}
        folder={library.folder}
        advanceOnAction={advanceOnAction}
        ratings={ratings}
        ratingsRev={ratingsRev}
        qualityEnabled={qualityEnabled}
        onBack={goLibrary}
        onCluster={(id) => goFocus(id)}
        onSettings={goSettings}
        onLibrary={goLibrary}
        onChanged={refreshLibraryNow}
      />
    )
  } else if (route.name === 'settings') {
    body = (
      <Settings
        folder={library.folder}
        totalPicked={picked}
        totalCount={total}
        onSettings={goSettings}
        onLibrary={goLibrary}
        onSaved={async (s) => { applySettings(s); refreshQuality(); await refreshLibrary() }}
      />
    )
  } else {
    body = (
      <Library
        data={library}
        onOpenCluster={goFocus}
        onSettings={goSettings}
        onLibrary={goLibrary}
        picksDir={picksDir}
        refresh={refreshLibraryNow}
        initialScroll={libraryScrollRef.current}
        onScrollChange={(top) => { libraryScrollRef.current = top }}
        initialActiveClusterID={libraryActiveIDRef.current}
        onActiveClusterChange={(id) => { libraryActiveIDRef.current = id }}
        initialFilters={libraryFiltersRef.current}
        onFiltersChange={onLibraryFiltersChange}
      />
    )
  }

  return (
    <ScanProgressContext.Provider value={scan}>
      <QualityContext.Provider value={qualityCtx}>
        {body}
        <ShortcutsOverlay screen={route.name} />
        <Toaster />
      </QualityContext.Provider>
    </ScanProgressContext.Provider>
  )
}
