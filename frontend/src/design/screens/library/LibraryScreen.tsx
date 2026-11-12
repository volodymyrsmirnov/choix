import { useRef } from 'react'
import {
  AlertReplica,
  Button,
  ContextMenuReplica,
  DropZone,
  EmptyState,
  FolderRow,
  IconButton,
  LibraryFilterBar,
  MonthHeader,
  showLabel,
  Spinner,
  TitleBar,
  Toaster,
} from '../../components'
import { dialog } from '../../dialogs'
import { FOLDER_ROW_MENU, folderFlags } from '../../menus'
import type { FolderSummary, LibraryFilter, QualityStatus } from '../../types'
import { filterFolders, isFiltered, LIBRARY_FILTER, libraryGroups } from './filter'
import { librarySummary, monthCount } from './months'
import { LIBRARY_OVERLAYS, type LibraryProps } from './props'


/**
 * The Library (spec §4.1, Layout A): the title bar, then the loading state,
 * the drop zone (no folders) or the folder rows under a summary line that
 * carries the toolbar (search, filter, order): the month timeline in a
 * capture-date order, one list in a name or added order.
 */
export function LibraryScreen(p: LibraryProps) {
  const { folders } = p
  const filter = p.filter ?? LIBRARY_FILTER
  const removing = p.removePrompt ? folders?.find((f) => f.id === p.removePrompt) : undefined

  let body
  if (folders === null) {
    body = (
      <div className="h-full flex items-center justify-center">
        <Spinner label="Loading the Library" />
      </div>
    )
  } else if (folders.length === 0) {
    body = (
      <div className="h-full overflow-y-auto" data-scroll>
        <div className="min-h-full flex items-center justify-center p-8">
          <DropZone active={p.dropActive} onAdd={p.onAdd} />
        </div>
      </div>
    )
  } else {
    body = <Timeline {...p} folders={folders} filter={filter} />
  }

  return (
    <div className="relative flex flex-col h-full min-h-0">
      {/* No indicator: a status lives on the element it describes (the
          owner's rule), and scans and rating passes show in their rows. */}
      <TitleBar
        title="Library"
        titleHidden
        trailing={
          <>
            <Button variant="primary" icon="plus" kbd="⌘O" onClick={p.onAdd}>
              Add Folder
            </Button>
            <IconButton icon="help" label="Keyboard shortcuts" onClick={p.onHelp} aria-haspopup="dialog" />
            <IconButton icon="gear" label="Settings" onClick={p.onSettings} />
          </>
        }
      />
      <main className="relative flex-1 min-h-0">
        {body}
        {p.dropActive && folders && folders.length > 0 && <DropZone overlay active onAdd={p.onAdd} />}
      </main>
      <Toaster items={p.toasts} onUndo={p.onUndoToast} onDismiss={p.onDismissToast} />
      {removing && (
        <div className="absolute inset-0 z-50 flex items-start justify-center pt-14" data-overlay={LIBRARY_OVERLAYS.remove}>
          <RemoveAlert folder={removing} onConfirm={(ok) => p.onConfirmRemove(removing.id, ok)} />
        </div>
      )}
    </div>
  )
}

/** A folder's rating pass in flight (its own counts), for its row. */
export function ratingOf(q: QualityStatus | null, folderId: string): { done: number; total: number } | undefined {
  if (!q || !q.enabled || q.state !== 'scoring') return undefined
  const pass = q.passes?.find((x) => x.folder_id === folderId)
  if (pass) return { done: pass.done, total: pass.total }
  return !q.passes && q.folder_id === folderId ? { done: q.done, total: q.total } : undefined
}

/** What the Library says when the toolbar leaves no folder to show, and the way back. */
function NoMatch({ filter, onClear }: { filter: LibraryFilter; onClear: (f: LibraryFilter) => void }) {
  const query = filter.query.trim()
  const filtered = filter.show !== 'all'
  const title = query
    ? `No folders match “${query}”`
    : filter.show === 'attention'
      ? 'No folder needs attention'
      : filter.show === 'started'
        ? 'No folder has a pick or a reject yet'
        : 'No folder is waiting for review'
  const body = query
    ? filtered
      ? `Search looks at folder names and where they are, among the folders “${showLabel(filter.show)}” shows.`
      : 'Search looks at folder names and where they are.'
    : undefined
  return (
    <div data-testid="folders-empty" className="flex justify-center pt-4">
      <EmptyState level={2} art={false} title={title} body={body}>
        <Button
          variant="default"
          onClick={() => onClear(filtered ? { ...filter, query: '', show: 'all' } : { ...filter, query: '' })}
        >
          {filtered ? 'Show All Folders' : 'Clear Search'}
        </Button>
      </EmptyState>
    </div>
  )
}

function Timeline(p: LibraryProps & { folders: FolderSummary[]; filter: LibraryFilter }) {
  const list = useRef<HTMLDivElement>(null)
  const visible = filterFolders(p.folders, p.filter)
  const narrowed = isFiltered(p.filter)
  const summary = librarySummary(visible)
  return (
    <div className="h-full overflow-y-auto" data-scroll>
      <div ref={list} className="px-7 pt-[22px] pb-7">
        {/* The summary line: what is shown on the left, the toolbar at its right end. */}
        <div className="flex items-center gap-4 min-w-0">
          {visible.length > 0 && (
            <div className="flex items-baseline gap-2.5 min-w-0">
              <h2 className="m-0 text-20 font-semibold tracking-[-0.02em] text-fg-0 whitespace-nowrap">
                {narrowed ? `${visible.length.toLocaleString('en-US')} of ${librarySummary(p.folders).title}` : summary.title}
              </h2>
              <span className="text-12 text-fg-2 truncate">{summary.detail}</span>
            </div>
          )}
          <span className="flex-1" />
          <LibraryFilterBar
            filter={p.filter}
            openMenu={p.openMenu ?? null}
            menuIds={LIBRARY_OVERLAYS}
            onFilter={(f) => p.onFilter?.(f)}
            onOpenMenu={(m) => p.onOpenMenu?.(m)}
            onFirstRow={() => list.current?.querySelector<HTMLElement>('[data-folder] > button')?.focus()}
          />
        </div>
        {visible.length === 0 && <NoMatch filter={p.filter} onClear={(f) => p.onFilter?.(f)} />}
        {libraryGroups(visible, p.filter.order).map((g, i) => (
          <section key={g.key} aria-label={g.label ?? 'Folders'}>
            {g.label !== null ? <MonthHeader label={g.label} count={monthCount(g.folders)} first={i === 0} /> : <div className="mt-4" />}
            <div className="flex flex-col gap-2">
              {g.folders.map((f) => (
                <FolderRow
                  key={f.id}
                  folder={f}
                  rating={ratingOf(p.quality, f.id)}
                  selected={p.selectedId === f.id}
                  menuOpen={p.menuFor === f.id}
                  menu={
                    p.menuFor === f.id ? (
                      <ContextMenuReplica
                        id={LIBRARY_OVERLAYS.menu}
                        label={`Actions for ${f.name}`}
                        items={FOLDER_ROW_MENU}
                        flags={folderFlags(f.state)}
                        onItem={(item) => {
                          if (item === 'remove-folder') return p.onRemove(f.id)
                          p.onMenu(null)
                          if (item === 'open-folder') p.onOpen(f.id)
                          if (item === 'open-picks') p.onOpenPicks(f.id)
                        }}
                      />
                    ) : undefined
                  }
                  force={p.forceRow?.id === f.id ? p.forceRow.force : undefined}
                  onOpen={() => p.onOpen(f.id)}
                  onSelect={() => p.onSelect(f.id)}
                  onOpenPicks={() => p.onOpenPicks(f.id)}
                  onMenu={(at) => p.onMenu(f.id, at)}
                />
              ))}
            </div>
          </section>
        ))}
      </div>
    </div>
  )
}

function RemoveAlert({ folder, onConfirm }: { folder: FolderSummary; onConfirm: (ok: boolean) => void }) {
  const d = dialog('remove-folder', { name: folder.name })
  return (
    <AlertReplica
      id={LIBRARY_OVERLAYS.remove}
      title={d.title}
      message={d.message}
      buttons={d.buttons.map((b) => ({ ...b, onClick: () => onConfirm(b.role !== 'cancel') }))}
    />
  )
}
