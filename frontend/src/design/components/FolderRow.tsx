import { useRef, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import { dateRange, noOrphan, plural } from '../format'
import type { FolderSummary, ScanProgress } from '../types'
import { Button } from './Button'
import { Dot } from './Dot'
import { CURRENT, CURRENT_FOCUS } from './current'
import { IconButton } from './IconButton'
import { ProgressRail } from './ProgressRail'
import { Spinner } from './Spinner'

// What the row says while its folder is being scanned.
const STAGE: Record<ScanProgress['stage'], string> = {
  discover: 'Looking for photos',
  metadata: 'Reading metadata',
  thumb: 'Generating thumbnails',
  analyze: 'Analyzing',
  cluster: 'Grouping',
}

// The preview strip (Layout A): dimmed and slightly desaturated at rest,
// full strength on hover or focus, dimmer while scanning, grey when offline.
const STRIP: Record<'rest' | 'scanning' | 'offline', string> = {
  rest: 'opacity-50 saturate-[.8] group-hover/row:opacity-95 group-hover/row:saturate-100 group-focus-within/row:opacity-95 group-focus-within/row:saturate-100',
  scanning: 'opacity-30',
  offline: 'opacity-[.18] grayscale',
}
const STRIP_MASK = 'linear-gradient(90deg, transparent 0, var(--mask-opaque) 46%)'

export interface FolderRowProps {
  folder: FolderSummary
  selected?: boolean
  /** The row's context menu is open: its actions stay visible. */
  menuOpen?: boolean
  /** The open context menu, placed under the row's ⋯ button. */
  menu?: ReactNode
  onOpen: () => void
  onSelect: () => void
  onOpenPicks: () => void
  /** Open the row's menu; `at` is where (viewport px), when known. */
  onMenu: (at?: { x: number; y: number }) => void
  /** Prototype only: the row's `hover` or `focus` look. */
  force?: string
  /** The folder's rating pass in flight: the row says so in place of its counts, as it does a scan. */
  rating?: { done: number; total: number }
}

/**
 * One folder on the Library timeline (Layout A, 116 px): name, parent path,
 * date range and counts on the left; six previews fading in from the right.
 * The row is one control (click or Enter opens the folder). Open Picks and
 * the ⋯ menu appear on hover, and whenever the row or one of them has
 * keyboard focus; the context-menu key and ⇧F10 open the menu too. An
 * offline folder cannot be opened, but keeps its menu (to remove it).
 */
export function FolderRow({ folder, selected, menuOpen, menu, onOpen, onSelect, onOpenPicks, onMenu, force, rating }: FolderRowProps) {
  const dots = useRef<HTMLButtonElement>(null)
  const offline = folder.state === 'offline'
  const scanning = folder.state === 'scanning'
  const when = folder.firstCaptured || folder.lastCaptured ? dateRange(folder.firstCaptured, folder.lastCaptured) : 'No capture dates'
  const look = offline ? 'offline' : scanning ? 'scanning' : 'rest'

  // A forced hover is the row's and its button's; a forced focus is the
  // button's ring plus the row's focus-within look.
  const rowForce = force === 'focus' ? 'focus-within' : force
  const openMenuAt = (e: MouseEvent) => {
    e.preventDefault()
    onSelect()
    onMenu({ x: e.clientX, y: e.clientY })
  }
  const onKeyDown = (e: KeyboardEvent<HTMLButtonElement>) => {
    if (e.key === 'ContextMenu' || (e.key === 'F10' && e.shiftKey)) {
      e.preventDefault()
      const r = e.currentTarget.getBoundingClientRect()
      onMenu({ x: r.right - 24, y: r.top + 44 })
    }
  }

  return (
    <div
      className={`group/row relative ${menuOpen ? 'z-20' : ''}`}
      data-folder={folder.id}
      data-folder-state={folder.state}
      data-force={rowForce}
    >
      <button
        type="button"
        // The current row is the current item's look; focus selects the row,
        // so keyboard focus draws that look too (deeper ring), never an
        // outline on top of it.
        className={`relative block w-full h-[116px] p-0 rounded-lg border bg-bg-2 overflow-hidden text-left transition-colors ${
          selected ? CURRENT : 'border-line-1 hover:border-line-2'
        } ${offline ? 'cursor-default' : 'cursor-pointer'} ${CURRENT_FOCUS}`}
        data-force={force}
        aria-disabled={offline ? 'true' : undefined}
        aria-current={selected ? 'true' : undefined}
        onClick={offline ? undefined : onOpen}
        onFocus={onSelect}
        onContextMenu={openMenuAt}
        onKeyDown={onKeyDown}
      >
        {folder.previews.length > 0 && (
          <span
            aria-hidden="true"
            className={`absolute inset-y-0 right-0 left-[30%] flex justify-end gap-[2px] transition-[opacity,filter] duration-base ${STRIP[look]}`}
            style={{ maskImage: STRIP_MASK, WebkitMaskImage: STRIP_MASK }}
          >
            {folder.previews.slice(0, 6).map((src, i) => (
              <img
                key={`${i}-${src}`}
                src={src}
                alt=""
                draggable={false}
                className="block h-full min-w-0 object-cover bg-bg-3"
                style={{ flex: '0 0 calc((100% - 10px) / 6)' }}
              />
            ))}
          </span>
        )}
        {/* Layout A's text column: 46 % of the row, but never narrower than
            480 px. At the minimum window size 46 % alone (≈ 410 px) wrapped a
            five-item stats line onto a second line that touched the date
            above it; the photo strip is positioned on its own, so the wider
            column only lies over its faded part. */}
        <span data-align-x="library-text" className="relative flex flex-col h-full max-w-[max(46%,480px)] px-[18px] py-4">
          <span className="flex items-baseline gap-2 min-w-0">
            <span
              className={`min-w-0 max-w-[65%] truncate text-17 font-semibold tracking-[-0.015em] ${offline ? 'text-fg-2' : 'text-fg-0'}`}
            >
              {folder.name}
            </span>
            {/* 2.4em is 4 JetBrains Mono digits; `ch` would follow each
                platform's rounded glyph advance (lint-design: font-unit). */}
            <span dir="auto" className="min-w-[2.4em] shrink-[3] truncate font-mono text-11 text-fg-3">
              {folder.parentPath}
            </span>
          </span>
          <span className="mt-[3px] font-mono text-11 text-fg-2 truncate">{when}</span>
          <span className="mt-auto flex flex-wrap items-center gap-x-3 gap-y-1 text-12 text-fg-1 min-w-0">
            <Status folder={folder} rating={rating} />
          </span>
        </span>
      </button>

      <div
        className={`absolute right-3 top-3 flex gap-1.5 transition-opacity duration-fast group-hover/row:opacity-100 group-focus-within/row:opacity-100 ${menuOpen ? 'opacity-100' : 'opacity-0'}`}
      >
        {!offline && (
          <Button variant="default" icon="folder" onClick={onOpenPicks} aria-label={`Open Picks of ${folder.name}`}>
            Open Picks
          </Button>
        )}
        <IconButton
          ref={dots}
          icon="dots"
          variant="default"
          label={`More actions for ${folder.name}`}
          title="More actions"
          aria-haspopup="menu"
          aria-expanded={menuOpen ? 'true' : 'false'}
          onClick={() => {
            onSelect()
            const r = dots.current?.getBoundingClientRect()
            onMenu(r ? { x: r.left, y: r.bottom + 4 } : undefined)
          }}
        />
      </div>
      {menu && <div className="absolute right-3 top-[46px] z-30">{menu}</div>}
    </div>
  )
}

/**
 * Work in flight on the row's status line: a spinner, what is done, a rail
 * and the percentage once known, then the scan's failed files if any (the
 * row is where the Library shows a scan, so its failures show here too).
 */
function Working({ label, railLabel, done, total, failed = 0 }: { label: string; railLabel: string; done: number; total: number; failed?: number }) {
  const frac = total > 0 ? done / total : undefined
  return (
    <span className="flex items-center gap-2 text-11 text-fg-1 min-w-0">
      <Spinner small decorative />
      <span className="truncate">{label}</span>
      <ProgressRail className="w-[120px]" label={railLabel} value={frac} />
      {frac !== undefined && <span className="font-mono tabular-nums text-fg-2 w-[2.4em]">{Math.floor(frac * 100)}%</span>}
      {failed > 0 && (
        <span data-testid="row-scan-failed" className="font-mono tabular-nums text-reject flex-shrink-0">
          {failed.toLocaleString('en-US')} failed
        </span>
      )}
    </span>
  )
}

function Status({ folder, rating }: { folder: FolderSummary; rating?: { done: number; total: number } }) {
  const { state, photos, videos, clusters, picked, rejected, scan } = folder
  // A folder stays `scanning` until its whole job ends, and its rating pass
  // is the job's last step: while the pass runs, the row says so instead of
  // the scan's last stage.
  if ((state === 'ready' || state === 'scanning') && rating) {
    return (
      <Working
        label="Rating quality"
        railLabel={`${folder.name}: rating quality`}
        done={rating.done}
        total={rating.total}
        failed={state === 'scanning' ? scan?.failed : 0}
      />
    )
  }
  if (state === 'scanning' && scan) {
    return (
      <Working
        label={STAGE[scan.stage]}
        railLabel={`${folder.name}: ${STAGE[scan.stage]}`}
        done={scan.done}
        total={scan.total}
        failed={scan.failed}
      />
    )
  }
  if (state === 'queued') {
    return (
      <span className="flex items-center gap-2 text-fg-2">
        <Dot />
        {noOrphan('Waiting to scan')}
      </span>
    )
  }
  if (state === 'error') {
    return (
      <span className="flex items-center gap-2 text-reject min-w-0" title={folder.error}>
        <Dot tone="reject" />
        <span className="truncate">{noOrphan(folder.error ?? 'Scan failed')}</span>
      </span>
    )
  }

  const items: ReactNode[] = []
  if (state === 'offline') {
    items.push(
      <span key="off" className="text-fg-2">
        {noOrphan(`Drive “${folder.volumeName ?? 'unknown'}” is not connected`)}
      </span>,
    )
  } else if (photos === 0 && videos === 0) {
    items.push(
      <span key="none" className="text-fg-2">
        {noOrphan('No photos or videos')}
      </span>,
    )
  } else {
    if (photos > 0 || videos === 0) items.push(<span key="p">{plural(photos, 'photo', 'photos')}</span>)
    if (videos > 0) items.push(<span key="v">{plural(videos, 'video', 'videos')}</span>)
    if (clusters > 0) items.push(<span key="c">{plural(clusters, 'cluster', 'clusters')}</span>)
  }
  if (picked > 0)
    items.push(
      <span key="pk" className="flex items-center gap-[5px]">
        <Dot tone="pick" />
        {plural(picked, 'picked', 'picked')}
      </span>,
    )
  if (rejected > 0 && state !== 'offline')
    items.push(
      <span key="rj" className="flex items-center gap-[5px]">
        <Dot tone="reject" />
        {plural(rejected, 'rejected', 'rejected')}
      </span>,
    )
  if (state === 'ready' && clusters > 0 && picked === 0 && rejected === 0)
    items.push(
      <span key="nr" className="text-fg-3">
        {noOrphan('Not reviewed yet')}
      </span>,
    )
  return <>{items}</>
}
