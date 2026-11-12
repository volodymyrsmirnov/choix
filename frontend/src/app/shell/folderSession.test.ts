import { describe, expect, test } from 'bun:test'
import type { AppEvents, On } from './events'
import { createFolderSession } from './folderSession'
import { createRouter, type HashHost, type Route } from './router'

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

function setup() {
  const calls: string[] = []
  const bus = fakeBus()
  let toLibrary = 0
  let retargets = 0
  const session = createFolderSession({
    open: (id: string) => {
      calls.push(`open ${id}`)
      return Promise.resolve({ folderId: id, clusters: [] } as never)
    },
    close: (id: string) => {
      calls.push(`close ${id}`)
      return Promise.resolve()
    },
    on: bus.on,
    toLibrary: () => {
      toLibrary++
    },
    settingsToLibrary: () => {
      retargets++
    },
  })
  session.start()
  return { calls, bus, session, toLibraryCount: () => toLibrary, retargetCount: () => retargets }
}

const LIBRARY: Route = { name: 'library' }
const SETTINGS: Route = { name: 'settings' }
const folder = (folderId: string): Route => ({ name: 'folder', folderId })
const focus = (folderId: string, clusterId: number): Route => ({ name: 'focus', folderId, clusterId })
const opens = (calls: string[]) => calls.filter((c) => c.startsWith('open'))
const closes = (calls: string[]) => calls.filter((c) => c.startsWith('close'))

describe('folderSession', () => {
  test('opens once for folder → focus → folder', async () => {
    const { calls, session } = setup()
    session.route(folder('f1'))
    session.route(focus('f1', 3))
    session.route(folder('f1'))
    await flush()
    expect(opens(calls)).toEqual(['open f1'])
    expect(closes(calls)).toEqual([])
    expect(session.get()).toMatchObject({ folderId: 'f1', status: 'open' })
  })

  test('stays open across settings', async () => {
    const { calls, session } = setup()
    session.route(folder('f1'))
    session.route(SETTINGS)
    session.route(focus('f1', 2))
    await flush()
    expect(opens(calls)).toEqual(['open f1'])
    expect(closes(calls)).toEqual([])
  })

  test('closes on the library route', async () => {
    const { calls, session } = setup()
    session.route(folder('f1'))
    session.route(LIBRARY)
    await flush()
    expect(calls).toEqual(['open f1', 'close f1'])
    expect(session.get()).toMatchObject({ folderId: null, status: 'closed' })
    // Nothing is open: the Library again closes nothing.
    session.route(LIBRARY)
    await flush()
    expect(closes(calls)).toEqual(['close f1'])
  })

  test('switching folders closes the first and opens the second', async () => {
    const { calls, session } = setup()
    session.route(folder('f1'))
    session.route(focus('f2', 1))
    await flush()
    expect(calls).toEqual(['open f1', 'close f1', 'open f2'])
    expect(session.get()).toMatchObject({ folderId: 'f2', status: 'open' })
  })

  test('moved reopens', async () => {
    const { calls, bus, session } = setup()
    session.route(folder('f1'))
    await flush()
    bus.emit('library:changed', { reason: 'moved', folderId: 'f2' })
    await flush()
    expect(opens(calls)).toEqual(['open f1'])
    bus.emit('library:changed', { reason: 'moved', folderId: 'f1' })
    await flush()
    expect(opens(calls)).toEqual(['open f1', 'open f1'])
    expect(closes(calls)).toEqual([])
  })

  test('offline marks the session, and online opens it again', async () => {
    const { calls, bus, session } = setup()
    session.route(folder('f1'))
    await flush()
    bus.emit('library:changed', { reason: 'offline', folderId: 'f1' })
    expect(session.get()).toMatchObject({ folderId: 'f1', status: 'offline' })
    bus.emit('library:changed', { reason: 'online', folderId: 'f1' })
    await flush()
    expect(opens(calls)).toEqual(['open f1', 'open f1'])
    expect(session.get().status).toBe('open')
  })

  test('gone navigates to the library', async () => {
    const { bus, session, toLibraryCount, retargetCount } = setup()
    session.route(folder('f1'))
    await flush()
    bus.emit('library:changed', { reason: 'gone', folderId: 'f9' })
    expect(toLibraryCount()).toBe(0)
    bus.emit('library:changed', { reason: 'gone', folderId: 'f1' })
    expect(toLibraryCount()).toBe(1)
    expect(retargetCount()).toBe(0)
  })

  test('gone while settings is open keeps the settings route and retargets back to the library', async () => {
    // Wired as App.tsx wires it: the real router, the session following it.
    const listeners = new Set<() => void>()
    const host: HashHost & { hash: string } = {
      hash: '',
      getHash: () => host.hash,
      setHash(h) {
        if (h === host.hash) return
        host.hash = h
        for (const l of [...listeners]) l()
      },
      onHashChange(fn) {
        listeners.add(fn)
        return () => listeners.delete(fn)
      },
    }
    const router = createRouter(host)
    router.start()
    const calls: string[] = []
    const bus = fakeBus()
    const session = createFolderSession({
      open: (id: string) => {
        calls.push(`open ${id}`)
        return Promise.resolve({ folder: id, clusters: [] } as never)
      },
      close: (id: string) => {
        calls.push(`close ${id}`)
        return Promise.resolve()
      },
      on: bus.on,
      toLibrary: () => router.navigate(LIBRARY, { force: true }),
      settingsToLibrary: () => router.retargetSettings(LIBRARY),
    })
    session.start()
    router.subscribe(() => session.route(router.current()))

    router.navigate(focus('f1', 3))
    await flush()
    router.navigate(SETTINGS)
    expect(router.settingsFrom()).toEqual(focus('f1', 3))
    // Settings holds unsaved edits: its guard refuses every navigation.
    let held = 0
    router.setGuard(() => {
      held++
      return false
    })

    bus.emit('library:changed', { reason: 'gone', folderId: 'f1' })
    await flush()
    // Still on Settings, edits intact (the guard was never asked); the folder is closed.
    expect(router.current()).toEqual(SETTINGS)
    expect(held).toBe(0)
    expect(router.settingsFrom()).toEqual(LIBRARY)
    expect(calls).toEqual(['open f1', 'close f1'])
    expect(session.get()).toMatchObject({ folderId: null, status: 'closed', view: null })

    // Leaving goes to the Library, through the guard.
    router.back()
    expect(held).toBe(1)
    expect(router.current()).toEqual(SETTINGS)
    router.setGuard(null)
    router.back()
    expect(router.current()).toEqual(LIBRARY)
    await flush()
    expect(closes(calls)).toEqual(['close f1'])

    // `removed` behaves the same.
    router.navigate(folder('f2'))
    await flush()
    router.navigate(SETTINGS)
    bus.emit('library:changed', { reason: 'removed', folderId: 'f2' })
    await flush()
    expect(router.current()).toEqual(SETTINGS)
    expect(router.settingsFrom()).toEqual(LIBRARY)
    expect(session.get().folderId).toBeNull()
    expect(closes(calls)).toEqual(['close f1', 'close f2'])
  })

  describe('busy (the previous session is still closing)', () => {
    const busy = () => Object.assign(new Error('busy'), { cause: { code: 'busy', message: 'The previous folder is still closing' } })

    /** A session whose Open answers from `answers` in turn, and whose back-off waits are released by hand. */
    function busySetup(answers: ('busy' | 'ok')[]) {
      const calls: string[] = []
      const waits: { ms: number; release(): void }[] = []
      const session = createFolderSession({
        open: (id: string) => {
          calls.push(`open ${id}`)
          return answers.shift() === 'busy' ? Promise.reject(busy()) : Promise.resolve({ folder: id, clusters: [] } as never)
        },
        close: (id: string) => {
          calls.push(`close ${id}`)
          return Promise.resolve()
        },
        on: fakeBus().on,
        toLibrary: () => {},
        settingsToLibrary: () => {},
        wait: (ms) =>
          new Promise<void>((resolve) => {
            waits.push({ ms, release: resolve })
          }),
      })
      session.start()
      /** Lets the latest back-off end. */
      const release = async () => {
        waits.at(-1)!.release()
        await flush()
      }
      return { calls, waits, session, release }
    }

    test('open retries on busy and succeeds', async () => {
      const { calls, waits, session, release } = busySetup(['busy', 'busy', 'ok'])
      session.route(folder('f1'))
      await flush()
      // Still opening while it waits: the screens keep waiting for the folder.
      expect(session.get()).toMatchObject({ folderId: 'f1', status: 'opening', error: null })
      expect(waits.map((w) => w.ms)).toEqual([250])
      await release()
      expect(waits.map((w) => w.ms)).toEqual([250, 500])
      expect(session.get().status).toBe('opening')
      await release()
      expect(opens(calls)).toEqual(['open f1', 'open f1', 'open f1'])
      expect(session.get()).toMatchObject({ folderId: 'f1', status: 'open', error: null })
    })

    test('open gives up on busy after the back-off and reports it like any open failure', async () => {
      const { calls, waits, session, release } = busySetup(['busy', 'busy', 'busy', 'busy', 'ok'])
      session.route(folder('f1'))
      await flush()
      await release()
      await release()
      await release()
      expect(waits.map((w) => w.ms)).toEqual([250, 500, 1000])
      expect(opens(calls)).toHaveLength(4)
      expect(session.get()).toMatchObject({ folderId: 'f1', status: 'error', error: { code: 'busy', message: 'The previous folder is still closing' } })
    })

    test('open stops retrying when the route leaves the folder', async () => {
      const { calls, waits, session, release } = busySetup(['busy', 'ok', 'ok'])
      session.route(folder('f1'))
      await flush()
      expect(waits).toHaveLength(1)
      session.route(LIBRARY)
      await release()
      expect(opens(calls)).toEqual(['open f1'])
      expect(session.get()).toMatchObject({ folderId: null, status: 'closed' })
      // Leaving for another folder: that one opens, the first is not asked again.
      const b = busySetup(['busy', 'ok', 'ok'])
      b.session.route(folder('f1'))
      await flush()
      b.session.route(focus('f2', 1))
      await flush()
      await b.release()
      expect(opens(b.calls)).toEqual(['open f1', 'open f2'])
      expect(b.session.get()).toMatchObject({ folderId: 'f2', status: 'open' })
    })
  })

  test('an offline open error marks the session offline', async () => {
    const bus = fakeBus()
    const session = createFolderSession({
      open: () => Promise.reject(Object.assign(new Error('x'), { cause: { code: 'offline', message: 'offline' } })),
      close: () => Promise.resolve(),
      on: bus.on,
      toLibrary: () => {},
      settingsToLibrary: () => {},
    })
    session.start()
    session.route(folder('f1'))
    await flush()
    expect(session.get()).toMatchObject({ folderId: 'f1', status: 'offline' })
  })
})
