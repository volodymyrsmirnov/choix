// The open folder. This is the single owner of Catalog.Open and
// Catalog.Close: it opens a folder when the route's folder id appears or
// changes, keeps it open across Folder, Focus, Compare and a visit to
// Settings, and closes it only on entering the Library or opening another
// folder. The Folder and Focus containers read the session; they never call
// Open or Close.
//
// Calls go out one at a time, in order, so a Close can never overtake the
// Open before it. A `library:changed` for the open folder: `moved` opens it
// again (Go re-resolved its path), `offline` marks it (the screens show the
// notice) and `online` opens it again, `gone` (or `removed`) leaves for the
// Library, whose route closes it. On the Settings route a gone folder does
// not force the Library on the user (that would throw away unsaved edits):
// the session closes it and Settings will return to the Library instead of
// the folder; leaving Settings then goes through its leave prompt as usual.
//
// An Open that answers `busy` (Go is still closing the previous session) is
// asked again after 250 ms, 500 ms and 1 s while the session stays `opening`;
// then it fails like any other Open. Leaving the folder (or any other open or
// close) meanwhile ends the retries.
import type { FolderView } from './api'
import { appError, type AppError } from './errors'
import type { On } from './events'
import type { Route } from './router'

export type SessionStatus = 'closed' | 'opening' | 'open' | 'offline' | 'error'

export interface SessionState {
  folderId: string | null
  status: SessionStatus
  /** The cluster grid Open returned; kept while the folder is reopened or offline. */
  view: FolderView | null
  /** Why opening failed (status `error`, or `offline` from an offline error). */
  error: AppError | null
}

export interface FolderSessionDeps {
  open(folderId: string): Promise<FolderView>
  close(folderId: string): Promise<void>
  on: On
  /** Leave for the Library (the folder is gone). */
  toLibrary(): void
  /** The folder is gone while Settings shows: Settings now returns to the Library. */
  settingsToLibrary(): void
  /** A Close that failed; nothing else is done about it. */
  onCloseError?(e: unknown): void
  /** The back-off between busy retries (a timer; tests pass their own). */
  wait?(ms: number): Promise<void>
}

export interface FolderSession {
  /** Called on every route change. */
  route(r: Route): void
  get(): SessionState
  /** For useSyncExternalStore. */
  subscribe(listener: () => void): () => void
  /** Listens to library:changed; returns stop. */
  start(): () => void
}

const CLOSED: SessionState = { folderId: null, status: 'closed', view: null, error: null }

/** The waits before each retry of an Open that answered `busy`; then it fails. */
export const BUSY_RETRY_MS = [250, 500, 1000] as const

const timer = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))

export function createFolderSession(deps: FolderSessionDeps): FolderSession {
  let state: SessionState = CLOSED
  const listeners = new Set<() => void>()
  /** Bumped on every open or close; an answer for an older one is dropped. */
  let gen = 0
  let queue: Promise<unknown> = Promise.resolve()
  /** The route last passed to route(). */
  let current: Route | null = null

  const publish = (next: SessionState) => {
    state = next
    for (const l of [...listeners]) l()
  }
  const enqueue = (op: () => Promise<unknown>) => {
    queue = queue.then(op, op)
  }

  const wait = deps.wait ?? timer

  const open = (folderId: string, keepView: boolean) => {
    const mine = ++gen
    publish({ folderId, status: 'opening', view: keepView ? state.view : null, error: null })
    const attempt = (retry: number) =>
      enqueue(async () => {
        // A retry is dropped once the session moved on; the first Open always goes out (its Close follows it).
        if (retry > 0 && mine !== gen) return
        try {
          const view = await deps.open(folderId)
          if (mine === gen) publish({ folderId, status: 'open', view, error: null })
        } catch (e) {
          if (mine !== gen) return
          const error = appError(e)
          const delay = error.code === 'busy' ? BUSY_RETRY_MS[retry] : undefined
          if (delay !== undefined) {
            // Waited outside the queue, so a Close is never held up by it.
            void wait(delay).then(() => {
              if (mine === gen) attempt(retry + 1)
            })
            return
          }
          publish({ ...state, status: error.code === 'offline' ? 'offline' : 'error', error })
        }
      })
    attempt(0)
  }

  const close = () => {
    const folderId = state.folderId
    if (folderId === null) return
    gen++
    publish(CLOSED)
    enqueue(() => deps.close(folderId).catch((e: unknown) => deps.onCloseError?.(e)))
  }

  return {
    route(r) {
      current = r
      switch (r.name) {
        case 'library':
          close()
          return
        case 'folder':
        case 'focus':
          if (r.folderId === state.folderId) return
          close()
          open(r.folderId, false)
          return
        case 'settings':
          return
      }
    },
    get: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    start() {
      return deps.on('library:changed', (ev) => {
        const folderId = state.folderId
        if (folderId === null || ev.folderId !== folderId) return
        switch (ev.reason) {
          case 'moved':
            open(folderId, true)
            return
          case 'offline':
            gen++ // an Open in flight must not mark it open again
            publish({ ...state, status: 'offline' })
            return
          case 'online':
            if (state.status === 'offline') open(folderId, true)
            return
          case 'gone':
          case 'removed':
            if (current?.name === 'settings') {
              deps.settingsToLibrary()
              close()
              return
            }
            deps.toLibrary()
            return
        }
      })
    },
  }
}
