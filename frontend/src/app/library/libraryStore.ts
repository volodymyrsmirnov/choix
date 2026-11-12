// The Library's folder list and what the Library screen derives from it.
//
// The store (createLibraryStore) belongs to the shell, not to the library
// route: createShell makes it and starts it with the other stores, so a
// folder deleted in Finder while another screen shows is still toasted,
// once. Event kinds (shell/store.ts):
//   library:changed  reload — any reason, `stats` included: the rows carry
//                    the counts. `gone` (deleted or trashed in Finder) also
//                    shows the "left the Library" toast, keyed by folder so
//                    a repeat replaces it; `removed` (the user's own alert)
//                    says nothing. Both tell onLeft (the selection goes).
//   scan:state       reconcile — the row's state, and its error text (the
//                    only place that text arrives); a row's run progress is
//                    not kept here.
// Progress comes from the shell's scan store (scan:progress): libraryView
// lays it over the rows. The Library's title bar shows no scan: each row
// shows its own.
//
// createLibraryHandlers holds the screen's calls into Go: Add Folder (the
// native picker), the native folder menu and the picks folder. Each reports
// its own failures to onError.
import type { FolderState, FolderSummary, ScanProgress } from '../../design/types'
import type { AddResult, FolderSummaryModel } from '../shell/api'
import { appError, errorText } from '../shell/errors'
import type { On } from '../shell/events'
import type { ScanMap } from '../shell/scanStore'
import { createStore, type Store, type StoreSnapshot } from '../shell/store'
import type { Toasts } from '../shell/toasts'

/** A Library row as Library.List answers. */
export type FolderRowData = FolderSummaryModel

export interface LibraryStoreDeps {
  list(): Promise<FolderRowData[]>
  on: On
  toasts: Pick<Toasts, 'push'>
  /** A folder left the Library (`removed` or `gone`). */
  onLeft?(folderId: string): void
}

export function createLibraryStore(deps: LibraryStoreDeps): Store<FolderRowData[]> {
  const store: Store<FolderRowData[]> = createStore(
    () => deps.list(),
    (_set, reload, reconcile) => {
      const offLibrary = deps.on('library:changed', (ev) => {
        if ((ev.reason === 'gone' || ev.reason === 'removed') && ev.folderId) {
          if (ev.reason === 'gone') {
            const name = ev.name || store.get().value?.find((f) => f.id === ev.folderId)?.name || ev.folderId
            deps.toasts.push({
              tone: 'info',
              title: `“${name}” left the Library`,
              body: 'The folder was deleted or moved to the Trash in Finder.',
              key: `removed:${ev.folderId}`,
            })
          }
          deps.onLeft?.(ev.folderId)
        }
        reload()
      })
      const offState = deps.on('scan:state', (ev) => {
        reconcile((prev = []) =>
          prev.map((f) => {
            if (f.id !== ev.folderId) return f
            const { scan: _run, error: _old, ...rest } = f
            return { ...rest, state: ev.state, ...(ev.state === 'error' && ev.error ? { error: ev.error } : {}) }
          }),
        )
      })
      return () => {
        offLibrary()
        offState()
      }
    },
  )
  return store
}

/** What the Library screen shows (mirrors LibraryScreen's own choice; the tests state the rule with it). */
export type LibraryScreenKind = 'loading' | 'empty' | 'timeline'

export interface LibraryView {
  screen: LibraryScreenKind
  /** null while the list's first answer is still to come. */
  folders: FolderSummary[] | null
}

export interface LibraryViewInput {
  list: StoreSnapshot<FolderRowData[]>
  /** The shell's scan store value (undefined before its first load). */
  scan: ScanMap | undefined
}

/** A scan that has sent no progress yet is looking for photos (the indeterminate indicator). */
const DISCOVERING: ScanProgress = { stage: 'discover', done: 0, total: 0, failed: 0, state: 'running' }

const asProgress = (p: NonNullable<FolderRowData['scan']>): ScanProgress => ({
  stage: p.stage as ScanProgress['stage'],
  done: p.done,
  total: p.total,
  failed: p.failed,
  state: p.state as ScanProgress['state'],
})

/** The design's row: the scan store's state and progress over Library.List's row. */
function toRow(r: FolderRowData, scan: ScanMap | undefined): FolderSummary {
  const known = scan?.[r.id]
  const state = (known?.state ?? r.state) as FolderState
  const progress = known ? known.progress : r.scan ? asProgress(r.scan) : null
  const { scan: _run, error, volumeName, ...rest } = r
  const row: FolderSummary = { ...rest, state, previews: r.previews ?? [] }
  if (state === 'scanning') row.scan = progress ?? DISCOVERING
  if (state === 'error' && error) row.error = error
  if (volumeName) row.volumeName = volumeName
  return row
}

export function libraryView({ list, scan }: LibraryViewInput): LibraryView {
  // Nothing is downloaded: with no folders the drop zone shows as soon as
  // the list has loaded.
  if (list.value === undefined) return { screen: 'loading', folders: null }
  const folders = list.value.map((r) => toRow(r, scan))
  return { screen: folders.length > 0 ? 'timeline' : 'empty', folders }
}

/** What createLibraryHandlers calls. */
export interface LibraryHandlerDeps {
  library: { AddFolders(): PromiseLike<Pick<AddResult, 'id' | 'name' | 'existing' | 'relation'>[]> }
  system: {
    ShowFolderMenu(folderId: string, x: number, y: number): PromiseLike<unknown>
    OpenPicksFolder(folderId: string): PromiseLike<unknown>
  }
  toasts: Pick<Toasts, 'push'>
  /** The Library's selection (actions.select). */
  select(folderId: string | null): void
  /** A folder's name from the list, if it is in it. */
  folderName(folderId: string): string | undefined
  /** A call failed; `title` names what failed (an action when absent). */
  onError(e: unknown, title?: string): void
}

export interface LibraryHandlers {
  /** Add Folder: the native picker; the first result is selected. */
  add(): Promise<void>
  /** The folder's picks folder in Finder. */
  openPicks(folderId: string): Promise<void>
  /** The native folder menu at window coordinates (the ⋯ button and a right-click); null closes the replica: nothing to do. */
  menu(folderId: string | null, at?: { x: number; y: number }): void
}

export function createLibraryHandlers(deps: LibraryHandlerDeps): LibraryHandlers {
  const guard = async (run: () => PromiseLike<unknown>, title?: string) => {
    try {
      await run()
    } catch (e) {
      deps.onError(e, title)
    }
  }

  return {
    add: () =>
      guard(async () => {
        const added = await deps.library.AddFolders()
        // A cancelled picker answers nothing.
        if (added.length === 0) return
        deps.select(added[0]!.id)
        for (const r of added) {
          if (!r.existing) continue
          deps.toasts.push({
            tone: 'info',
            title: `“${r.name}” is already in the Library`,
            body: 'Choix selected it for you.',
            key: `already:${r.id}`,
          })
        }
      }),

    openPicks: async (folderId) => {
      try {
        await deps.system.OpenPicksFolder(folderId)
      } catch (e) {
        if (appError(e).code !== 'read_only') {
          deps.onError(e)
          return
        }
        const name = deps.folderName(folderId)
        deps.toasts.push({
          tone: 'warn',
          title: name ? `“${name}” is on a read-only drive` : 'This folder is on a read-only drive',
          body: 'Choix can show and review it, but cannot copy picks into it.',
          key: `read-only:${folderId}`,
        })
      }
    },

    menu(folderId, at) {
      if (folderId === null || !at) return
      void guard(() => deps.system.ShowFolderMenu(folderId, Math.round(at.x), Math.round(at.y)))
    },
  }
}

/**
 * The shell's way of showing a failed call (App's action errors): a reject
 * toast titled `title` ("Action failed" by default, v1's) with the design's
 * copy or Go's message; nothing once quit began (`unavailable`).
 */
export function reportError(toasts: Pick<Toasts, 'push'>) {
  return (e: unknown, title = 'Action failed') => {
    const err = appError(e)
    if (err.code === 'unavailable') return
    toasts.push({ tone: 'reject', title, body: errorText(err) })
  }
}
