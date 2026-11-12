import { describe, expect, test } from 'bun:test'
import { createRouter, parseRoute, routeHash, type HashHost, type Route } from './router'

/** A location hash without a browser: setHash fires hashchange like a browser would. */
function fakeHost(initial = ''): HashHost & { hash: string; external(hash: string): void } {
  const listeners = new Set<() => void>()
  const host = {
    hash: initial,
    getHash: () => host.hash,
    setHash(h: string) {
      if (h === host.hash) return
      host.hash = h
      for (const l of [...listeners]) l()
    },
    onHashChange(fn: () => void) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    /** The user (or a link) changed the hash. */
    external(h: string) {
      host.setHash(h)
    },
  }
  return host
}

const LIBRARY: Route = { name: 'library' }
const folder = (folderId: string): Route => ({ name: 'folder', folderId })
const focus = (folderId: string, clusterId: number): Route => ({ name: 'focus', folderId, clusterId })
const SETTINGS: Route = { name: 'settings' }

describe('router', () => {
  test('parses the four routes', () => {
    expect(parseRoute('')).toEqual(LIBRARY)
    expect(parseRoute('#/')).toEqual(LIBRARY)
    expect(parseRoute('#/folder/f1')).toEqual(folder('f1'))
    expect(parseRoute('#/folder/f1/focus/42')).toEqual(focus('f1', 42))
    expect(parseRoute('#/settings')).toEqual(SETTINGS)
    // Round trip, including an id that needs escaping.
    for (const r of [LIBRARY, folder('a b/c#d'), focus('x?y', 7), SETTINGS]) {
      expect(parseRoute(routeHash(r))).toEqual(r)
    }
    // Anything else is the Library.
    expect(parseRoute('#/folder/')).toEqual(LIBRARY)
    expect(parseRoute('#/folder/f1/focus/abc')).toEqual(LIBRARY)
    expect(parseRoute('#/nowhere')).toEqual(LIBRARY)
  })

  test('back from focus goes to the folder, then the library', () => {
    const host = fakeHost('#/folder/f1/focus/3')
    const router = createRouter(host)
    router.start()
    expect(router.current()).toEqual(focus('f1', 3))
    router.back()
    expect(router.current()).toEqual(folder('f1'))
    expect(host.hash).toBe('#/folder/f1')
    router.back()
    expect(router.current()).toEqual(LIBRARY)
    router.back()
    expect(router.current()).toEqual(LIBRARY)
  })

  test('back from settings returns to the folder it was opened from', () => {
    const router = createRouter(fakeHost())
    router.start()
    router.navigate(folder('f1'))
    router.navigate(SETTINGS)
    expect(router.settingsFrom()).toEqual(folder('f1'))
    router.back()
    expect(router.current()).toEqual(folder('f1'))

    // From Focus it goes back to Focus; a hash change counts as well.
    const host = fakeHost('#/folder/f2/focus/9')
    const r2 = createRouter(host)
    r2.start()
    host.external('#/settings')
    expect(r2.current()).toEqual(SETTINGS)
    r2.back()
    expect(r2.current()).toEqual(focus('f2', 9))

    // Opened from the Library, it goes back to the Library.
    const r3 = createRouter(fakeHost())
    r3.start()
    r3.navigate(SETTINGS)
    r3.back()
    expect(r3.current()).toEqual(LIBRARY)
  })

  test('retargetSettings changes where back from settings goes', () => {
    const router = createRouter(fakeHost())
    router.start()
    router.navigate(folder('f1'))
    router.navigate(SETTINGS)
    let changes = 0
    router.subscribe(() => changes++)
    router.retargetSettings(LIBRARY)
    expect(router.current()).toEqual(SETTINGS)
    expect(router.settingsFrom()).toEqual(LIBRARY)
    expect(changes).toBe(1)
    // The same target again is no change.
    router.retargetSettings(LIBRARY)
    expect(changes).toBe(1)
    // The guard still decides the way out.
    router.setGuard(() => false)
    router.back()
    expect(router.current()).toEqual(SETTINGS)
    router.setGuard(null)
    router.back()
    expect(router.current()).toEqual(LIBRARY)
  })

  test('retargeting moves a held navigation to the gone folder as well', () => {
    const router = createRouter(fakeHost())
    router.start()
    router.navigate(focus('f1', 3))
    router.navigate(SETTINGS)
    router.setGuard(() => false)
    router.back()
    expect(router.held()).toEqual(focus('f1', 3))
    router.retargetSettings(LIBRARY)
    expect(router.held()).toEqual(LIBRARY)

    // A held navigation elsewhere is left alone.
    const r2 = createRouter(fakeHost())
    r2.start()
    r2.navigate(folder('f1'))
    r2.navigate(SETTINGS)
    r2.setGuard(() => false)
    r2.navigate(folder('f2'))
    r2.retargetSettings(LIBRARY)
    expect(r2.held()).toEqual(folder('f2'))
    // A navigation that goes through clears it.
    r2.setGuard(null)
    r2.navigate(LIBRARY)
    expect(r2.held()).toBeNull()
  })

  test('restores scroll per route', () => {
    const router = createRouter(fakeHost())
    router.saveScroll(LIBRARY, 120)
    router.saveScroll(folder('f1'), 640)
    router.saveScroll(folder('f2'), 80)
    expect(router.scrollOf(LIBRARY)).toBe(120)
    expect(router.scrollOf(folder('f1'))).toBe(640)
    expect(router.scrollOf(folder('f2'))).toBe(80)
    expect(router.scrollOf(focus('f1', 1))).toBe(0)
    router.saveScroll(folder('f1'), 0)
    expect(router.scrollOf(folder('f1'))).toBe(0)
  })

  test('notifies subscribers once per change, and a guard can hold a navigation', () => {
    const router = createRouter(fakeHost())
    router.start()
    let changes = 0
    router.subscribe(() => changes++)
    router.navigate(folder('f1'))
    router.navigate(folder('f1'))
    expect(changes).toBe(1)

    router.setGuard((to) => to.name !== 'library')
    expect(router.navigate(LIBRARY)).toBe(false)
    expect(router.current()).toEqual(folder('f1'))
    expect(router.navigate(LIBRARY, { force: true })).toBe(true)
    expect(router.current()).toEqual(LIBRARY)
  })
})
