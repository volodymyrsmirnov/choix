import React, { useEffect, useMemo, useRef, useState } from 'react'
import {
  AppBar,
  SearchIcon, FilterIcon, SortIcon, RefreshIcon, ChevronDown, InfoIcon,
  CheckIcon,
} from './primitives'
import type { LibraryResponse, Cluster, Member } from '../api'
import { postRecluster, thumbURL } from '../api'
import { toast } from './Toaster'

type SortKey = 'time-asc' | 'time-desc'

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
  const [deviceOpen, setDeviceOpen] = useState(false)
  const [sortOpen, setSortOpen] = useState(false)
  const deviceRef = useRef<HTMLDivElement | null>(null)
  const sortRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (deviceRef.current && !deviceRef.current.contains(e.target as Node)) setDeviceOpen(false)
      if (sortRef.current && !sortRef.current.contains(e.target as Node)) setSortOpen(false)
    }
    window.addEventListener('mousedown', onClick)
    return () => window.removeEventListener('mousedown', onClick)
  }, [])

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
          className="bg-transparent border-0 outline-none text-xs text-fg-0 placeholder:text-fg-3 flex-1 min-w-0"
          data-testid="filter-input"
        />
        <span className="kbd text-[9px]">⌘ K</span>
      </label>

      <div ref={deviceRef} className="relative">
        <button className="btn btn-ghost" onClick={() => setDeviceOpen(v => !v)} data-testid="device-filter">
          <FilterIcon /> {device === '' ? 'All clusters' : device} <ChevronDown />
        </button>
        {deviceOpen && (
          <div className="absolute z-10 mt-1 left-0 min-w-[200px] rounded-md border border-line-2 bg-bg-2 shadow-pop py-1">
            {[{ k: '', label: 'All clusters' }, ...devices.map(d => ({ k: d, label: d }))].map(opt => (
              <button key={opt.k}
                      onClick={() => { onDevice(opt.k); setDeviceOpen(false) }}
                      className={`w-full text-left px-3 py-1.5 text-xs ${device === opt.k ? 'text-fg-0 bg-bg-3' : 'text-fg-1 hover:bg-bg-3'}`}>
                {opt.label}
              </button>
            ))}
          </div>
        )}
      </div>

      <div ref={sortRef} className="relative">
        <button className="btn btn-ghost" onClick={() => setSortOpen(v => !v)} data-testid="sort-toggle">
          <SortIcon /> {sort === 'time-asc' ? 'Capture time ↑' : 'Capture time ↓'} <ChevronDown />
        </button>
        {sortOpen && (
          <div className="absolute z-10 mt-1 left-0 min-w-[200px] rounded-md border border-line-2 bg-bg-2 shadow-pop py-1">
            {([
              { k: 'time-asc' as SortKey, label: 'Capture time — earliest first' },
              { k: 'time-desc' as SortKey, label: 'Capture time — latest first' },
            ]).map(opt => (
              <button key={opt.k}
                      onClick={() => { onSort(opt.k); setSortOpen(false) }}
                      className={`w-full text-left px-3 py-1.5 text-xs ${sort === opt.k ? 'text-fg-0 bg-bg-3' : 'text-fg-1 hover:bg-bg-3'}`}>
                {opt.label}
              </button>
            ))}
          </div>
        )}
      </div>

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

const HelpHint: React.FC<{ picksDir: string }> = ({ picksDir }) => (
  <div className="flex items-center gap-3.5 px-3.5 py-2 border-b border-line-1 bg-bg-1 text-[11px] text-fg-2">
    <InfoIcon className="text-fg-3" />
    <span className="inline-flex items-center gap-1"><span className="kbd">↑</span><span className="kbd">↓</span> Navigate</span>
    <span className="inline-flex items-center gap-1"><span className="kbd">F</span> / <span className="kbd">↵</span> Open Focus</span>
    <span className="font-mono text-fg-3">·</span>
    <span className="inline-flex items-center gap-1"><span className="kbd">Space</span> Pick</span>
    <span className="inline-flex items-center gap-1"><span className="kbd">X</span> Reject</span>
    <span className="inline-flex items-center gap-1"><span className="kbd">C</span> Compare</span>
    <span className="inline-flex items-center gap-1"><span className="kbd">Z</span> Pixel-peep</span>
    <span className="flex-1" />
    <span>Picks copy to <span className="font-mono text-fg-1">{picksDir}</span></span>
  </div>
)

const Thumb: React.FC<{ m: Member; onOpen: () => void; size?: number }> = ({ m, onOpen, size = 116 }) => {
  const state = m.picked ? 'picked' : (m.rejected ? 'rejected' : 'default')
  return (
    <button onClick={onOpen}
            className="thumb flex-shrink-0 bg-transparent p-0 cursor-pointer outline-none focus:outline-none focus-visible:ring-2 focus-visible:ring-pick focus-visible:ring-offset-2 focus-visible:ring-offset-bg-1"
            data-state={state}
            data-file-id={m.file_id}
            data-testid="thumb"
            title={m.path}
            style={{ width: size, height: size }}>
      <img src={thumbURL(m)} alt={m.path} loading="lazy" />
      {m.kind === 'video' && (
        <div className="absolute top-1 right-1 w-5 h-5 rounded-sm flex items-center justify-center pointer-events-none"
             style={{ background: 'oklch(0 0 0 / 0.65)' }}
             aria-label="Video">
          <svg width="10" height="10" viewBox="0 0 10 10" fill="currentColor" className="text-fg-0 ml-0.5">
            <path d="M2 1.5l7 3.5-7 3.5z" />
          </svg>
        </div>
      )}
      {m.picked && (
        <div className="absolute bottom-1.5 left-1.5 px-1.5 py-0.5 rounded-sm text-[10px] font-semibold flex items-center gap-1 bg-pick text-pick-ink">
          <CheckIcon size={10} /> Pick
        </div>
      )}
      {m.rejected && (
        <div className="absolute bottom-1.5 left-1.5 px-1.5 py-0.5 rounded-sm text-[10px] font-semibold"
             style={{ background: 'oklch(0.66 0.20 25 / 0.95)', color: 'oklch(0.16 0.06 25)' }}>
          Reject
        </div>
      )}
    </button>
  )
}

type ClusterCounts = { picked: number; rejected: number; reviewed: number }

const ClusterMeta: React.FC<{ c: Cluster; counts: ClusterCounts; onFocus: () => void }> = ({ c, counts, onFocus }) => {
  return (
    <div className="flex items-center gap-3">
      <span className="font-semibold text-[13px] text-fg-0 tracking-tight">{c.device}</span>
      <span className="font-mono text-[11px] text-fg-2">{c.time}</span>
      <span className="flex-1" />
      <div className="flex items-center gap-2 font-mono text-[11px] text-fg-2">
        <span><span className="dot" data-tone="pick" /> <span className="ml-1">{counts.picked}</span></span>
        <span><span className="dot" data-tone="reject" /> <span className="ml-1">{counts.rejected}</span></span>
        <span className="text-fg-3">{counts.reviewed}/{c.members.length}</span>
      </div>
      <button className="btn btn-ghost text-[11px]" onClick={onFocus}>
        Open Focus <span className="kbd">F</span>
      </button>
    </div>
  )
}

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
}> = ({ data, onOpenCluster, onSettings, onLibrary, picksDir, refresh, initialScroll, onScrollChange, initialActiveClusterID, onActiveClusterChange }) => {
  const [busy, setBusy] = useState(false)
  const [query, setQuery] = useState('')
  const [device, setDevice] = useState('')
  const [sort, setSort] = useState<SortKey>('time-asc')
  const [activeIdx, setActiveIdx] = useState(0)
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

  // Cmd/Ctrl-K focuses the search input.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
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
  // box keeps its native key behavior.
  const visibleClustersRef = useRef<Cluster[]>([])
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA') return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const list = visibleClustersRef.current
      if (list.length === 0) return
      if (e.key === 'ArrowDown' || e.key === 'j' || e.key === 'J') {
        e.preventDefault()
        userMovedRef.current = true
        setActiveIdx(i => Math.min(list.length - 1, i + 1))
        return
      }
      if (e.key === 'ArrowUp' || e.key === 'k' || e.key === 'K') {
        e.preventDefault()
        userMovedRef.current = true
        setActiveIdx(i => Math.max(0, i - 1))
        return
      }
      if (e.key === 'f' || e.key === 'F' || e.key === 'Enter') {
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
  // the user's last-selected row stays selected; falls back to 0 if that
  // cluster is no longer visible (filter applied, recluster hid it, etc.).
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
    if (el) el.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
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
          {visibleClusters.map((c, idx) => {
            const cc = counts.get(c.id) ?? { picked: 0, rejected: 0, reviewed: 0 }
            const pct = c.members.length > 0 ? (cc.reviewed / c.members.length) * 100 : 0
            const isActive = idx === activeIdx
            return (
              <section key={c.id} data-testid="cluster" data-cluster-id={c.id}
                       data-active-cluster={isActive ? 'true' : undefined}
                       onClick={() => setActiveIdx(idx)}
                       className={`rounded-md transition-colors px-2 -mx-2 ${isActive ? 'bg-bg-2 ring-1 ring-line-2' : ''}`}>
                <div className="mb-2">
                  <ClusterMeta c={c} counts={cc} onFocus={() => onOpenCluster(c.id)} />
                  <div className="progress-rail mt-2 h-0.5">
                    <div className="fill" style={{ width: `${pct}%` }} />
                  </div>
                </div>
                <div className="flex gap-2 overflow-x-auto py-2 px-0.5 [&::-webkit-scrollbar]:hidden"
                     style={{ scrollbarWidth: 'none' }}>
                  {c.members.map(m => (
                    <Thumb key={m.file_id} m={m} onOpen={() => onOpenCluster(c.id, m.file_id)} />
                  ))}
                </div>
              </section>
            )
          })}
        </div>
      </main>
    </div>
  )
}
