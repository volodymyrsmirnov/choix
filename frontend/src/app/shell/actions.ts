// One function for menu ids and keys: runAction(id, data). The native menu
// sends `menu:action` (with the folder id as data for a folder-row context
// menu click), the keyboard (keys.ts) calls it for the keys the webview
// owns, and both run the same handler.
//
// The shell's own actions — Add Folder, the folder actions, Undo, Settings,
// Back, the shortcut sheet, Find, GitHub — have a default here. A screen's
// actions (pick, reject, next photo, zoom, compare, …: v1's key actions)
// are registered by the screen's container while it is mounted; a container
// may also take over a default (the Library takes Add Folder to show its
// result). An id nobody handles is ignored.
//
// The menu's context (screen, whether a folder is targeted, its flags) goes
// to Go with System.SetMenuContext: one complete context per route,
// selection or folder-state change (bindMenuContext), written one call at a
// time — while a call is in flight only the latest context waits, and it is
// sent when the call settles unless it equals what was last sent — so an
// older context can never land after a newer one.
import { folderFlags } from '../../design/menus'
import type { ScreenId } from '../../design/types'
import type { On } from './events'
import { isTextField } from './keys'
import type { Route } from './router'

export interface MenuContext {
  screen: ScreenId
  hasSelection: boolean
  flags: string[]
}

export type ActionHandler = (data?: string) => unknown

export interface ActionDeps {
  catalog: { Undo(folderId: string): PromiseLike<unknown> }
  library: {
    AddFolders(): PromiseLike<unknown>
    Rescan(folderId: string): PromiseLike<unknown>
    Remove(folderId: string): PromiseLike<unknown>
  }
  system: {
    OpenPicksFolder(folderId: string): PromiseLike<unknown>
    RevealFolder(folderId: string): PromiseLike<unknown>
    OpenHelp(): PromiseLike<unknown>
    SetMenuContext(c: MenuContext): PromiseLike<unknown>
  }
  router: { current(): Route; navigate(to: Route): unknown; back(): void }
  /** Opens or closes the shortcut sheet. */
  toggleShortcuts(): void
  /** Edit ▸ Find: focus the screen's search field, if it has one. */
  focusSearch?(): void
  /** The page's document: Undo in a text field is the field's own undo. */
  document: { readonly activeElement: unknown; execCommand(commandId: string): boolean }
  /** A handler threw or its promise rejected. */
  onError(e: unknown, id: string): void
  /** Catalog.Undo (the default undo) succeeded. */
  onUndone?(result: unknown): void
}

export interface ActionsState {
  /** What was last sent to SetMenuContext (or will be: the initial Library context). */
  context: MenuContext
  /** The Library's selected folder row. */
  selected: string | null
}

export interface Actions {
  runAction(id: string, data?: string): void
  /** A container's handler for `id`, until the returned function is called; the latest wins. */
  register(id: string, handler: ActionHandler): () => void
  /** Runs `menu:action` events; returns stop. */
  listen(on: On): () => void
  /** The Library's selected row (null for none). */
  select(folderId: string | null): void
  /** The folder the folder actions apply to: the selected row on the Library, the open folder elsewhere. */
  target(): string | null
  /** Merges into the menu context and sends it when it changed (serialised; see the header). */
  setContext(c: Partial<MenuContext>): void
  state(): ActionsState
  /** For useSyncExternalStore. */
  subscribe(listener: () => void): () => void
}

/** The folder the folder actions apply to on a route: its folder, else the Library's selected row. */
export function targetOf(route: Route, selected: string | null): string | null {
  if (route.name === 'folder' || route.name === 'focus') return route.folderId
  if (route.name === 'library') return selected
  return null
}

const contextKey = (c: MenuContext) => JSON.stringify([c.screen, c.hasSelection, c.flags])

const isThenable = (v: unknown): v is PromiseLike<unknown> =>
  typeof v === 'object' && v !== null && typeof (v as { then?: unknown }).then === 'function'

export function createActions(deps: ActionDeps): Actions {
  const handlers = new Map<string, ActionHandler[]>()
  const listeners = new Set<() => void>()
  let state: ActionsState = { context: { screen: 'library', hasSelection: false, flags: [] }, selected: null }
  /** The context last given to setContext (null before the first: that one is always sent). */
  let desired: string | null = null
  /** The context of the last call that was made and did not fail. */
  let sent: string | null = null
  let writing = false
  let waiting: MenuContext | null = null

  /** One SetMenuContext at a time; the latest waiting context goes when the call settles. */
  const write = (c: MenuContext) => {
    if (writing) {
      waiting = c
      return
    }
    const key = contextKey(c)
    if (key === sent) return
    writing = true
    sent = key
    const settle = (failed: boolean) => {
      // Go refuses only an unknown screen (the type rules it out), or every
      // call once quit began; a failed call counts as not sent.
      if (failed && sent === key) sent = null
      writing = false
      const next = waiting
      waiting = null
      if (next) write(next)
    }
    deps.system.SetMenuContext(c).then(
      () => settle(false),
      () => settle(true),
    )
  }

  const publish = (next: ActionsState) => {
    state = next
    for (const l of [...listeners]) l()
  }

  const target = (): string | null => targetOf(deps.router.current(), state.selected)

  /** Runs `fn` on the context menu's folder, else the target; nothing when there is neither. */
  const onFolder =
    (fn: (folderId: string) => unknown): ActionHandler =>
    (data) => {
      const folderId = data ?? target()
      return folderId === null ? undefined : fn(folderId)
    }

  const defaults: Record<string, ActionHandler> = {
    'add-folder': () => deps.library.AddFolders(),
    'open-folder': onFolder((folderId) => deps.router.navigate({ name: 'folder', folderId })),
    'open-picks': onFolder((folderId) => deps.system.OpenPicksFolder(folderId)),
    'reveal-folder': onFolder((folderId) => deps.system.RevealFolder(folderId)),
    'rescan-folder': onFolder((folderId) => deps.library.Rescan(folderId)),
    'remove-folder': onFolder((folderId) => deps.library.Remove(folderId)),
    undo: onFolder((folderId) => deps.catalog.Undo(folderId).then((r) => deps.onUndone?.(r))),
    github: () => deps.system.OpenHelp(),
    settings: () => deps.router.navigate({ name: 'settings' }),
    back: () => deps.router.back(),
    shortcuts: () => deps.toggleShortcuts(),
    find: () => deps.focusSearch?.(),
  }

  const runAction = (id: string, data?: string) => {
    // ⌘Z belongs to the native menu, so a text field's own undo comes here.
    if (id === 'undo' && isTextField(deps.document.activeElement)) {
      deps.document.execCommand('undo')
      return
    }
    const stack = handlers.get(id)
    const handler = stack && stack.length > 0 ? stack[stack.length - 1] : Object.hasOwn(defaults, id) ? defaults[id] : null
    if (!handler) return
    try {
      const result = handler(data)
      if (isThenable(result)) result.then(undefined, (e: unknown) => deps.onError(e, id))
    } catch (e) {
      deps.onError(e, id)
    }
  }

  return {
    runAction,
    register(id, handler) {
      const stack = handlers.get(id) ?? []
      stack.push(handler)
      handlers.set(id, stack)
      return () => {
        const i = stack.lastIndexOf(handler)
        if (i >= 0) stack.splice(i, 1)
      }
    },
    listen(on) {
      return on('menu:action', (ev) => runAction(ev.id, ev.data))
    },
    select(folderId) {
      if (folderId !== state.selected) publish({ ...state, selected: folderId })
    },
    target,
    setContext(c) {
      const context: MenuContext = { ...state.context, ...c, flags: [...(c.flags ?? state.context.flags)] }
      const key = contextKey(context)
      if (key === desired) return
      desired = key
      publish({ ...state, context })
      write(context)
    },
    state: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

/** What bindMenuContext follows: the route, and each folder's state (scanStore). */
export interface MenuContextSource {
  route(): Route
  subscribeRoute(listener: () => void): () => void
  /** The folder's FolderState, if known. */
  folderState(folderId: string): string | undefined
  subscribeFolders(listener: () => void): () => void
}

/**
 * Keeps the menu context complete and current: a route change sets the
 * screen, the target and its flags in one context; a selection or folder
 * state change re-sends the target and flags with the screen as it is (so
 * a container's `compare` stays until the route changes). Returns stop.
 */
export function bindMenuContext(actions: Actions, src: MenuContextSource): () => void {
  const targetPart = () => {
    const folderId = targetOf(src.route(), actions.state().selected)
    const state = folderId === null ? undefined : src.folderState(folderId)
    return { hasSelection: folderId !== null, flags: state ? folderFlags(state) : [] }
  }
  const onRoute = () => actions.setContext({ screen: src.route().name, ...targetPart() })
  const onTarget = () => actions.setContext(targetPart())
  let selected = actions.state().selected
  const stops = [
    src.subscribeRoute(onRoute),
    src.subscribeFolders(onTarget),
    actions.subscribe(() => {
      if (actions.state().selected === selected) return
      selected = actions.state().selected
      onTarget()
    }),
  ]
  onRoute()
  return () => {
    for (const stop of stops) stop()
  }
}
