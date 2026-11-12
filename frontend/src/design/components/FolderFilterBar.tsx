import { useEffect, useRef } from 'react'
import { plural } from '../format'
import type { FolderFilter } from '../types'
import { Button } from './Button'
import { isLetter } from '../keys'
import { Dropdown, type DropdownOption } from './Dropdown'
import { Kbd } from './Kbd'
import { TextInput } from './TextInput'

const SORT_OPTIONS: DropdownOption<FolderFilter['sort']>[] = [
  { value: 'time-asc', label: 'Capture time — earliest first' },
  { value: 'time-desc', label: 'Capture time — latest first' },
]

/**
 * The Folder page's filter bar (v1 Library's Subnav): the search field (⌘F,
 * and v1's ⌘K, focus and select it), the camera and sort menus, the cluster
 * and photo counts (with failed files, when any) and Re-cluster. Controlled:
 * the caller owns the filter and which menu is open.
 */
export function FolderFilterBar({
  filter,
  devices,
  openMenu,
  menuIds,
  visible,
  clusters,
  photos,
  videos,
  failed,
  reclustering,
  reclusterDisabled,
  onFilter,
  onOpenMenu,
  onRecluster,
}: {
  filter: FolderFilter
  devices: string[]
  openMenu: 'device' | 'sort' | null
  /** Overlay ids of the two menus. */
  menuIds: { device: string; sort: string }
  /** Clusters shown after filtering, of `clusters`. */
  visible: number
  clusters: number
  /** Photos and videos in the clusters, counted apart as the title bar does. */
  photos: number
  videos: number
  /** Files whose scan failed. */
  failed: number
  reclustering: boolean
  reclusterDisabled?: boolean
  onFilter: (f: FolderFilter) => void
  onOpenMenu: (m: 'device' | 'sort' | null) => void
  onRecluster: () => void
}) {
  const searchRef = useRef<HTMLInputElement>(null)

  // ⌘F (the Edit › Find accelerator, when the menu does not take it) and ⌘K
  // (v1) focus the search field. ⌘ only: ⌃⌘F is Enter Full Screen. Not while
  // a dialog is open (v1), nor when another handler took the key.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return
      if (!e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
      if (!isLetter(e, 'f') && !isLetter(e, 'k')) return
      if (document.querySelector('[role="dialog"], [role="alertdialog"]')) return
      // Not while the page sits inert behind a native menu replica.
      if (searchRef.current?.closest('[inert]')) return
      e.preventDefault()
      searchRef.current?.focus()
      searchRef.current?.select()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const deviceOptions: DropdownOption<string>[] = [{ value: '', label: 'All cameras' }, ...devices.map((d) => ({ value: d, label: d }))]
  const counts = `${visible !== clusters ? `${visible.toLocaleString('en-US')} of ${plural(clusters, 'cluster', 'clusters')}` : plural(clusters, 'cluster', 'clusters')} · ${plural(photos, 'photo', 'photos')}${videos > 0 ? ` · ${plural(videos, 'video', 'videos')}` : ''}`

  return (
    <div className="flex items-center gap-2 h-10 px-3.5 border-b border-line-1 bg-bg-1 flex-shrink-0">
      <TextInput
        ref={searchRef}
        size="sm"
        icon="search"
        className="w-[240px] min-w-[150px] flex-shrink"
        value={filter.query}
        onChange={(query) => onFilter({ ...filter, query })}
        placeholder="Filter clusters"
        label="Filter clusters by camera, time or file name"
        aria-keyshortcuts="Meta+F Meta+K"
        data-testid="filter-input"
        trailing={<Kbd hidden>⌘F</Kbd>}
      />

      <Dropdown
        id={menuIds.device}
        testId="device-filter"
        icon="filter"
        srPrefix="Camera:"
        label={filter.device === '' ? 'All cameras' : filter.device}
        menuLabel="Camera"
        options={deviceOptions}
        value={filter.device}
        open={openMenu === 'device'}
        onOpenChange={(open) => onOpenMenu(open ? 'device' : null)}
        onSelect={(device) => onFilter({ ...filter, device })}
      />

      <Dropdown
        id={menuIds.sort}
        testId="sort-toggle"
        icon="sort"
        srPrefix="Sort:"
        label={filter.sort === 'time-asc' ? 'Capture time ↑' : 'Capture time ↓'}
        menuLabel="Sort clusters"
        options={SORT_OPTIONS}
        value={filter.sort}
        open={openMenu === 'sort'}
        onOpenChange={(open) => onOpenMenu(open ? 'sort' : null)}
        onSelect={(sort) => onFilter({ ...filter, sort })}
      />

      <span className="flex-1" />
      {/* The counts may truncate at the narrowest window; the failed count,
          the thing to act on, never does. */}
      <span className="min-w-0 flex items-baseline font-mono text-11 text-fg-2" data-testid="cluster-counts">
        <span className="min-w-0 truncate">{counts}</span>
        {failed > 0 && (
          <span className="flex-shrink-0 whitespace-pre">
            <span className="text-fg-3"> · </span>
            <span className="text-reject" data-testid="failed-count" title={`${plural(failed, 'file', 'files')} could not be read and are left out`}>
              {failed.toLocaleString('en-US')} failed
            </span>
          </span>
        )}
      </span>
      <div className="w-px h-[18px] bg-line-1 mx-0.5 flex-shrink-0" aria-hidden="true" />
      <Button
        variant="ghost"
        icon="refresh"
        busy={reclustering}
        disabled={reclusterDisabled}
        onClick={onRecluster}
        data-testid="recluster"
        className="flex-shrink-0"
      >
        {reclustering ? 'Re-clustering…' : 'Re-cluster'}
      </Button>
    </div>
  )
}
