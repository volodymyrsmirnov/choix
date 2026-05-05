import React, { useCallback, useEffect, useRef, useState } from 'react'
import { Library } from './components/Library'
import { Focus } from './components/Focus'
import { Settings } from './components/Settings'
import { Empty } from './components/Empty'
import { Toaster, toast } from './components/Toaster'
import {
  fetchLibrary, fetchSettings, subscribeProgress,
  type LibraryResponse, type ProgressEvent, type SettingsBody,
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

export const App: React.FC = () => {
  const [route, setRoute] = useState<Route>(parseRoute())
  const [library, setLibrary] = useState<LibraryResponse | null>(null)
  const [picksDir, setPicksDir] = useState('./picks/')
  const [advanceOnAction, setAdvanceOnAction] = useState(false)
  const [progress, setProgress] = useState<ProgressEvent | null>(null)
  const [scanDone, setScanDone] = useState(false)
  const libraryScrollRef = useRef(0)
  const libraryActiveIDRef = useRef<number | null>(null)
  const lastProgressKey = useRef('')

  const refreshLibrary = useCallback(async () => {
    try {
      const d = await fetchLibrary()
      setLibrary(d)
    } catch (e) {
      toast.reject('Library load failed', { body: String(e) })
    }
  }, [])

  const applySettings = useCallback((s: SettingsBody) => {
    setPicksDir(s.picks_dir)
    setAdvanceOnAction(!!s.advance_on_action)
  }, [])

  useEffect(() => {
    refreshLibrary()
    fetchSettings().then(applySettings).catch(() => { /* ignore */ })
  }, [refreshLibrary, applySettings])

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
      }
    })
    return close
  }, [refreshLibrary])

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

  if (!library) {
    return (
      <>
        <Empty folder={folder} mode="scanning" progress={progress} onSettings={goSettings} onLibrary={goLibrary} />
        <Toaster />
      </>
    )
  }

  if (showEmptyScanning && route.name === 'library') {
    return (
      <>
        <Empty folder={library.folder} mode="scanning" progress={progress} onSettings={goSettings} onLibrary={goLibrary} />
        <Toaster />
      </>
    )
  }

  if (showEmptyDone && route.name === 'library') {
    return (
      <>
        <Empty folder={library.folder} mode="empty" onSettings={goSettings} onLibrary={goLibrary} />
        <Toaster />
      </>
    )
  }

  let body: React.ReactNode
  if (route.name === 'focus') {
    body = (
      <Focus
        clusterID={route.clusterID}
        startFileID={route.fileID}
        totalPicked={picked}
        totalCount={total}
        folder={library.folder}
        advanceOnAction={advanceOnAction}
        onBack={goLibrary}
        onCluster={(id) => goFocus(id)}
        onSettings={goSettings}
        onLibrary={goLibrary}
        onChanged={refreshLibrary}
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
        onSaved={async (s) => { applySettings(s); await refreshLibrary() }}
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
        refresh={refreshLibrary}
        initialScroll={libraryScrollRef.current}
        onScrollChange={(top) => { libraryScrollRef.current = top }}
        initialActiveClusterID={libraryActiveIDRef.current}
        onActiveClusterChange={(id) => { libraryActiveIDRef.current = id }}
      />
    )
  }

  return (
    <>
      {body}
      <Toaster />
    </>
  )
}
