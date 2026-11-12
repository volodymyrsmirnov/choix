import { describe, expect, test } from 'bun:test'
import { bindMenuContext, createActions, type ActionDeps, type MenuContext } from './actions'
import type { AppEvents, On } from './events'
import { createKeyHandler, type KeyOptions } from './keys'
import type { Route } from './router'

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

function fakeBus() {
  const handlers = new Map<string, Set<(data: unknown) => void>>()
  const on: On = (name, handler) => {
    const set = handlers.get(name) ?? new Set()
    set.add(handler as (data: unknown) => void)
    handlers.set(name, set)
    return () => set.delete(handler as (data: unknown) => void)
  }
  const emit = <K extends keyof AppEvents>(name: K, data: AppEvents[K]) => {
    for (const h of [...(handlers.get(name) ?? [])]) h(data)
  }
  return { on, emit }
}

/** Every binding call the actions make, as `Service.Method(args)`. */
function setup(
  over: {
    route?: Route
    selected?: string | null
    activeElement?: unknown
    SetMenuContext?: (c: MenuContext) => PromiseLike<unknown>
  } = {},
) {
  const calls: string[] = []
  const commands: string[] = []
  const errors: unknown[] = []
  const contexts: MenuContext[] = []
  let route: Route = over.route ?? { name: 'library' }
  const rec =
    (name: string) =>
    (...args: unknown[]) => {
      calls.push(`${name}(${args.map((a) => JSON.stringify(a)).join(', ')})`)
      return Promise.resolve(undefined)
    }
  const deps: ActionDeps = {
    catalog: { Undo: rec('Catalog.Undo') },
    library: { AddFolders: rec('Library.AddFolders'), Rescan: rec('Library.Rescan'), Remove: rec('Library.Remove') },
    system: {
      OpenPicksFolder: rec('System.OpenPicksFolder'),
      RevealFolder: rec('System.RevealFolder'),
      OpenHelp: rec('System.OpenHelp'),
      SetMenuContext:
        over.SetMenuContext ??
        ((c: MenuContext) => {
          contexts.push(c)
          return Promise.resolve()
        }),
    },
    router: {
      current: () => route,
      navigate: (r: Route) => {
        calls.push(`navigate ${r.name}`)
        route = r
        return true
      },
      back: () => calls.push('back'),
    },
    toggleShortcuts: () => calls.push('toggleShortcuts'),
    document: {
      activeElement: over.activeElement ?? null,
      execCommand: (c: string) => {
        commands.push(c)
        return true
      },
    },
    onError: (e: unknown) => errors.push(e),
  }
  const actions = createActions(deps)
  actions.select(over.selected === undefined ? 'f1' : over.selected)
  return { calls, commands, errors, contexts, actions }
}

/** A keydown without a DOM. */
function key(init: Partial<KeyboardEvent> & { key: string }): KeyboardEvent & { prevented: boolean } {
  const e = {
    code: '',
    metaKey: false,
    ctrlKey: false,
    altKey: false,
    shiftKey: false,
    repeat: false,
    isComposing: false,
    defaultPrevented: false,
    target: null,
    prevented: false,
    getModifierState: () => false,
    preventDefault() {
      e.prevented = true
      e.defaultPrevented = true
    },
    ...init,
  }
  return e as unknown as KeyboardEvent & { prevented: boolean }
}

function keys(run: KeyOptions['run'], over: Partial<KeyOptions> = {}) {
  return createKeyHandler({ run, screen: () => 'library', singleKeysNative: () => false, ...over })
}

describe('actions', () => {
  test('menu action and key run the same handler', async () => {
    const { calls, actions } = setup({ selected: 'f1' })
    const bus = fakeBus()
    actions.listen(bus.on)

    bus.emit('menu:action', { id: 'open-picks' })
    await flush()
    expect(calls).toEqual(['System.OpenPicksFolder("f1")'])

    // The key path: ⌘P reaches the webview only when no native menu owns
    // it (no accelerators); then it dispatches the very same action.
    const onKey = keys(actions.runAction, { accelerators: [] })
    onKey(key({ key: 'p', code: 'KeyP', metaKey: true }))
    await flush()
    expect(calls).toEqual(['System.OpenPicksFolder("f1")', 'System.OpenPicksFolder("f1")'])

    // A context-menu click names its folder; that folder wins over the selection.
    bus.emit('menu:action', { id: 'open-picks', data: 'f7' })
    await flush()
    expect(calls[2]).toBe('System.OpenPicksFolder("f7")')
  })

  test('a keydown matching a manifest accelerator is ignored', async () => {
    const { calls, actions } = setup()
    const bus = fakeBus()
    actions.listen(bus.on)
    const onKey = keys(actions.runAction)
    const cmdO = key({ key: 'o', code: 'KeyO', metaKey: true })
    onKey(cmdO)
    await flush()
    expect(calls).toEqual([])
    expect(cmdO.prevented).toBe(false)

    bus.emit('menu:action', { id: 'add-folder' })
    await flush()
    expect(calls).toEqual(['Library.AddFolders()'])
  })

  test('single keys are ignored when singleKeysNative', () => {
    const { actions } = setup({ route: { name: 'focus', folderId: 'f1', clusterId: 1 } })
    let rejects = 0
    actions.register('reject', () => {
      rejects++
    })
    const focusScreen = () => 'focus' as const

    keys(actions.runAction, { screen: focusScreen, singleKeysNative: () => true })(key({ key: 'x', code: 'KeyX' }))
    expect(rejects).toBe(0)

    const x = key({ key: 'x', code: 'KeyX' })
    keys(actions.runAction, { screen: focusScreen, singleKeysNative: () => false })(x)
    expect(rejects).toBe(1)
    expect(x.prevented).toBe(true)

    // Keys no menu item lists stay with the webview either way (Escape is Back).
    let backs = 0
    actions.register('back', () => {
      backs++
    })
    keys(actions.runAction, { screen: focusScreen, singleKeysNative: () => true })(key({ key: 'Escape', code: 'Escape' }))
    expect(backs).toBe(1)
  })

  test('undo in a text field does not call Catalog.Undo', async () => {
    const field = { tagName: 'INPUT', type: 'text' }
    const inField = setup({ route: { name: 'folder', folderId: 'f1' }, activeElement: field })
    inField.actions.runAction('undo')
    await flush()
    expect(inField.commands).toEqual(['undo'])
    expect(inField.calls).toEqual([])

    const outside = setup({ route: { name: 'folder', folderId: 'f1' }, activeElement: { tagName: 'BUTTON' } })
    outside.actions.runAction('undo')
    await flush()
    expect(outside.commands).toEqual([])
    expect(outside.calls).toEqual(['Catalog.Undo("f1")'])
  })

  test('unknown id is ignored', async () => {
    const { calls, commands, errors, actions } = setup()
    expect(() => actions.runAction('no-such-action')).not.toThrow()
    expect(() => actions.runAction('pick')).not.toThrow() // no screen registered it
    await flush()
    expect(calls).toEqual([])
    expect(commands).toEqual([])
    expect(errors).toEqual([])
  })

  test('the target folder is the open one outside the Library; navigation actions use the router', async () => {
    const { calls, actions } = setup({ route: { name: 'focus', folderId: 'f3', clusterId: 2 }, selected: null })
    actions.runAction('reveal-folder')
    actions.runAction('github')
    actions.runAction('back')
    actions.runAction('settings')
    actions.runAction('shortcuts')
    await flush()
    expect(calls).toEqual(['System.RevealFolder("f3")', 'System.OpenHelp()', 'back', 'navigate settings', 'toggleShortcuts'])
  })

  test('a failing action reports its error', async () => {
    const { errors, actions } = setup()
    actions.register('rescan-folder', () => Promise.reject(new Error('nope')))
    actions.runAction('rescan-folder')
    await flush()
    expect(errors).toHaveLength(1)
  })

  test('SetMenuContext is sent once per change', async () => {
    const { contexts, actions } = setup()
    actions.setContext({ screen: 'folder', hasSelection: true, flags: [] })
    actions.setContext({ screen: 'folder', hasSelection: true, flags: [] })
    actions.setContext({ flags: ['scanning'] })
    await flush()
    expect(contexts).toEqual([
      { screen: 'folder', hasSelection: true, flags: [] },
      { screen: 'folder', hasSelection: true, flags: ['scanning'] },
    ])
  })

  test('the native context after delayed calls equals the latest state', async () => {
    // Go's menu applies a context when its call returns; calls answer late.
    const calls: Array<{ c: MenuContext; resolve(): void }> = []
    let native = null as MenuContext | null
    let inFlight = 0
    let maxInFlight = 0
    const { actions } = setup({
      SetMenuContext: (c) => {
        inFlight++
        maxInFlight = Math.max(maxInFlight, inFlight)
        return new Promise<void>((resolve) =>
          calls.push({
            c,
            resolve: () => {
              native = c
              inFlight--
              resolve()
            },
          }),
        )
      },
    })
    const A: MenuContext = { screen: 'folder', hasSelection: true, flags: [] }
    const B: MenuContext = { screen: 'focus', hasSelection: true, flags: [] }
    const C: MenuContext = { screen: 'settings', hasSelection: false, flags: [] }
    actions.setContext(A)
    actions.setContext(B)
    actions.setContext(C)
    expect(calls.map((x) => x.c)).toEqual([A]) // one call in flight; B was superseded by C
    calls[0].resolve()
    await flush()
    expect(calls.map((x) => x.c)).toEqual([A, C])
    calls[1].resolve()
    await flush()
    expect(native).toEqual(actions.state().context)
    expect(native).toEqual(C)

    // Back to what was last sent while a call is in flight: nothing more to send.
    actions.setContext(A)
    actions.setContext(C)
    calls[2].resolve()
    await flush()
    expect(calls.map((x) => x.c)).toEqual([A, C, A, C])
    calls[3].resolve()
    await flush()
    actions.setContext(B)
    actions.setContext(C)
    actions.setContext(B)
    calls[4].resolve()
    await flush()
    expect(calls).toHaveLength(5) // the pending B equals what was last sent: skipped
    expect(native).toEqual(B)
    expect(native).toEqual(actions.state().context)
    expect(maxInFlight).toBe(1)
  })

  test('a rejected SetMenuContext does not stall later contexts', async () => {
    const sent: MenuContext[] = []
    const answers: Array<{ resolve(): void; reject(e: unknown): void }> = []
    const { actions } = setup({
      SetMenuContext: (c) => {
        sent.push(c)
        return new Promise<void>((resolve, reject) => answers.push({ resolve, reject }))
      },
    })
    const A: MenuContext = { screen: 'folder', hasSelection: true, flags: [] }
    const B: MenuContext = { screen: 'focus', hasSelection: true, flags: [] }
    actions.setContext(A)
    actions.setContext(B)
    answers[0].reject(new Error('unavailable'))
    await flush()
    expect(sent).toEqual([A, B])
    answers[1].reject(new Error('unavailable'))
    await flush()
    // A rejected context counts as not sent: the same context goes out again.
    actions.setContext(A)
    actions.setContext(B)
    await flush()
    expect(sent).toEqual([A, B, A])
  })

  test('a route change sends one context', async () => {
    const { contexts, actions } = setup({ selected: null })
    let route: Route = { name: 'library' }
    const routeListeners = new Set<() => void>()
    const folderListeners = new Set<() => void>()
    const states: Record<string, string> = { f1: 'scanning' }
    const stop = bindMenuContext(actions, {
      route: () => route,
      subscribeRoute: (fn) => {
        routeListeners.add(fn)
        return () => routeListeners.delete(fn)
      },
      folderState: (id) => states[id],
      subscribeFolders: (fn) => {
        folderListeners.add(fn)
        return () => folderListeners.delete(fn)
      },
    })
    const go = async (r: Route) => {
      route = r
      for (const l of [...routeListeners]) l()
      await flush()
    }
    await flush()
    expect(contexts).toEqual([{ screen: 'library', hasSelection: false, flags: [] }])

    await go({ name: 'folder', folderId: 'f1' })
    expect(contexts.slice(1)).toEqual([{ screen: 'folder', hasSelection: true, flags: ['scanning'] }])

    await go({ name: 'settings' })
    expect(contexts.slice(2)).toEqual([{ screen: 'settings', hasSelection: false, flags: [] }])

    // A folder's state change re-sends the flags with the screen it is on.
    await go({ name: 'focus', folderId: 'f1', clusterId: 4 })
    states.f1 = 'offline'
    for (const l of [...folderListeners]) l()
    await flush()
    expect(contexts.slice(3)).toEqual([
      { screen: 'focus', hasSelection: true, flags: ['scanning'] },
      { screen: 'focus', hasSelection: true, flags: ['offline'] },
    ])
    stop()
  })
})
