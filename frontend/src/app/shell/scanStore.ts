// Every folder's scan state and progress, for the title-bar indicator and
// the Library rows. Loaded from Library.List; `scan:progress` and
// `scan:state` patch one folder; a `library:changed` that adds, removes or
// moves folders reloads (its `stats` reason changes counts only). Event
// kinds (store.ts): progress is transient, so a scan's stream of progress
// never makes Library.List run again; state and list changes reconcile.
import type { FolderState, ScanProgress } from '../../design/types'
import type { On } from './events'
import { createStore, type Store } from './store'

export interface FolderScan {
  state: FolderState
  /** The latest progress while a scan runs (or stopped); null when there is none to show. */
  progress: ScanProgress | null
}

/** By folder id. */
export type ScanMap = Record<string, FolderScan>

/** What the store needs from a Library row (FolderSummary). */
export interface ScanRow {
  id: string
  state: string
  scan?: { stage: string; done: number; total: number; failed: number; state: string } | null
}

export interface ScanStoreDeps {
  list(): Promise<ScanRow[]>
  on: On
}

const asProgress = (p: NonNullable<ScanRow['scan']>): ScanProgress => ({
  stage: p.stage as ScanProgress['stage'],
  done: p.done,
  total: p.total,
  failed: p.failed,
  state: p.state as ScanProgress['state'],
})

/** States after which the last run's progress no longer describes anything. */
const CLEARS_PROGRESS: ReadonlySet<string> = new Set(['ready', 'queued'])

export function createScanStore(deps: ScanStoreDeps): Store<ScanMap> {
  const load = async (): Promise<ScanMap> => {
    const out: ScanMap = {}
    for (const row of await deps.list()) {
      out[row.id] = { state: row.state as FolderState, progress: row.scan ? asProgress(row.scan) : null }
    }
    return out
  }
  return createStore(load, (set, reload, reconcile) => {
    // Transient: only a folder the value shows scanning takes progress, so
    // a late tick cannot bring back a scan the loaded value says is over.
    const offProgress = deps.on('scan:progress', (ev) => {
      set((prev = {}) => {
        const before = prev[ev.folderId]
        if (before?.state !== 'scanning') return prev
        return { ...prev, [ev.folderId]: { state: before.state, progress: asProgress(ev.progress) } }
      })
    })
    // State change, carried by the event.
    const offState = deps.on('scan:state', (ev) => {
      reconcile((prev) => {
        const before = prev?.[ev.folderId]
        // A new run starts without the previous run's numbers (the indicator
        // is indeterminate until its first progress event).
        const fresh = CLEARS_PROGRESS.has(ev.state) || (ev.state === 'scanning' && before?.state !== 'scanning')
        return {
          ...prev,
          [ev.folderId]: { state: ev.state as FolderState, progress: fresh ? null : (before?.progress ?? null) },
        }
      })
    })
    // State change of the list itself: load it again (`stats` changes counts only).
    const offLibrary = deps.on('library:changed', (ev) => {
      if (ev.reason !== 'stats') reload()
    })
    return () => {
      offProgress()
      offState()
      offLibrary()
    }
  })
}
