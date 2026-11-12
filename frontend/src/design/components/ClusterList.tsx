import { useCallback, useEffect, useRef } from 'react'
import { scrollBehavior } from '../motion'
import type { Cluster, MediaURLs } from '../types'
import { isLetter, trackModality } from '../keys'
import { ClusterSection, type ClusterSectionCounts } from './ClusterSection'

function countsOf(c: Cluster): ClusterSectionCounts {
  let picked = 0
  let rejected = 0
  for (const m of c.members) {
    if (m.picked) picked++
    else if (m.rejected) rejected++
  }
  return { picked, rejected, reviewed: picked + rejected }
}

/**
 * The Folder page's clusters (v1 Library's list), with v1's keyboard model:
 * ↑/↓ (or K/J) move the active cluster, F or Enter open it in Focus. The keys
 * stand down while a text field or an open menu has focus, under ⌘ / ⌃ / ⌥,
 * while the list is inside an inert subtree, and for Enter on a control reached with Tab (that control's own click
 * wins). The active cluster is scrolled into view when it changes, but not
 * on mount, so a restored scroll position wins.
 */
export function ClusterList({
  clusters,
  activeId,
  media,
  qualityOn,
  emptyText,
  openDisabled,
  onSelect,
  onOpen,
}: {
  /** The visible clusters, already filtered and sorted. */
  clusters: Cluster[]
  activeId: number | null
  media: MediaURLs
  qualityOn: boolean
  /** Shown when no cluster is visible. */
  emptyText: string
  /** Opening Focus is unavailable (the folder's drive is offline): F, ↩, Open Focus and the thumbnails do nothing. */
  openDisabled?: boolean
  onSelect: (id: number) => void
  onOpen: (id: number, fileId?: number) => void
}) {
  const listRef = useRef<HTMLDivElement | null>(null)
  // The handler reads the latest props through a ref, so it binds once.
  const latest = useRef({ clusters, activeId, onSelect, onOpen, openDisabled })
  latest.current = { clusters, activeId, onSelect, onOpen, openDisabled }
  // Stable callbacks and per-cluster counts (v1), so moving the cursor
  // re-renders the two clusters whose `active` changed, not every thumbnail.
  const select = useCallback((id: number) => latest.current.onSelect(id), [])
  const open = useCallback((id: number, fileId?: number) => {
    if (!latest.current.openDisabled) latest.current.onOpen(id, fileId)
  }, [])
  // Per cluster object, so a new filtered array of the same clusters reuses
  // them (and ClusterSection's memo holds).
  const counts = useRef(new WeakMap<Cluster, ClusterSectionCounts>())
  const countFor = (c: Cluster): ClusterSectionCounts => {
    let n = counts.current.get(c)
    if (!n) {
      n = countsOf(c)
      counts.current.set(c, n)
    }
    return n
  }

  useEffect(() => {
    const modality = trackModality()
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return
      // A list behind an inert backdrop (a native menu replica) takes no keys.
      if (listRef.current?.closest('[inert]')) return
      const target = e.target instanceof Element ? e.target : null
      const tag = target?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return
      if (target?.closest('[role="menu"], [role="dialog"], [role="alertdialog"]')) return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === 'Enter' && modality.keyboardFocusedControl(e)) return
      const { clusters: list, activeId: id, onSelect: select, onOpen: open } = latest.current
      if (list.length === 0) return
      const found = list.findIndex((c) => c.id === id)
      const i = found < 0 ? 0 : found
      if (e.key === 'ArrowDown' || isLetter(e, 'j')) {
        e.preventDefault()
        const next = list[found < 0 ? 0 : Math.min(list.length - 1, i + 1)]
        if (next && next.id !== id) select(next.id)
        return
      }
      if (e.key === 'ArrowUp' || isLetter(e, 'k')) {
        e.preventDefault()
        const prev = list[Math.max(0, i - 1)]
        if (prev && prev.id !== id) select(prev.id)
        return
      }
      if (isLetter(e, 'f') || e.key === 'Enter') {
        e.preventDefault()
        const c = list[i]
        if (c && !latest.current.openDisabled) open(c.id)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      modality.stop()
    }
  }, [])

  const mounted = useRef(false)
  useEffect(() => {
    if (!mounted.current) {
      mounted.current = true
      return
    }
    const el = listRef.current?.querySelector<HTMLElement>('[data-active-cluster="true"]')
    el?.scrollIntoView({ block: 'nearest', behavior: scrollBehavior() })
  }, [activeId])

  if (clusters.length === 0) {
    return (
      <div className="text-fg-2 text-12 px-2" data-testid="clusters-empty">
        {emptyText}
      </div>
    )
  }
  return (
    <div ref={listRef} className="flex flex-col gap-6">
      {clusters.map((c) => (
        <ClusterSection
          key={c.id}
          cluster={c}
          active={c.id === activeId}
          counts={countFor(c)}
          media={media}
          qualityOn={qualityOn}
          openDisabled={openDisabled}
          onSelect={select}
          onOpen={open}
        />
      ))}
    </div>
  )
}
