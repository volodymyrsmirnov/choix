// The Library store belongs to the shell, not to the library route: a
// folder deleted in Finder while another screen shows is still toasted,
// once. This runs the real createShell on mocked bindings and event bus
// (bun's mock.module) and a minimal window and document, restored after.
import { afterAll, expect, mock, test } from 'bun:test'
import type { AppEvents, On } from '../shell/events'

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

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

const row = (id: string, name: string) => ({
  id,
  name,
  parentPath: '~/Pictures',
  state: 'ready',
  month: '2026-05',
  firstCaptured: '2026-05-02T10:00:00Z',
  lastCaptured: '2026-05-03T10:00:00Z',
  photos: 12,
  videos: 0,
  clusters: 4,
  picked: 0,
  rejected: 0,
  previews: [],
  scan: null,
})

let rows = [row('a', 'Harbour walk'), row('b', 'Orchard')]
let lists = 0
const ok = () => Promise.resolve()

mock.module('../shell/api', () => ({
  Catalog: {
    Open: () => Promise.resolve({}),
    Close: ok,
    Undo: ok,
    QualityStatus: () => Promise.resolve({ enabled: true, state: 'idle', active: false, done: 0, total: 0, rated: 0 }),
  },
  Library: {
    List: () => {
      lists++
      return Promise.resolve(rows)
    },
    CheckNow: ok,
    AddFolders: () => Promise.resolve([]),
    Rescan: ok,
    Remove: () => Promise.resolve(false),
  },
  Settings: { Get: () => Promise.resolve({}), Save: () => Promise.resolve({ saved: true, reclustered: false }) },
  System: {
    Info: () => Promise.resolve({ singleKeysNative: false }),
    SetMenuContext: ok,
    OpenPicksFolder: ok,
    RevealFolder: ok,
    OpenHelp: ok,
    ShowFolderMenu: ok,
    TitleBarDoubleClick: ok,
  },
}))
mock.module('../shell/events', () => ({ on }))

const g = globalThis as Record<string, unknown>
const before = { window: g.window, document: g.document }
g.window = { location: { hash: '#/folder/b' }, addEventListener() {}, removeEventListener() {} }
g.document = { querySelector: () => null, getElementById: () => null, activeElement: null, execCommand: () => false }
afterAll(() => {
  g.window = before.window
  g.document = before.document
})

test('the Library store lives for the shell: gone on another screen toasts once', async () => {
  const { createShell } = await import('../shell/App')
  const shell = createShell()
  expect(shell.router.current().name).toBe('folder')
  const stop = shell.start()
  await flush()
  // Loaded by start(), with no Library screen mounted.
  expect(shell.library.get().value?.map((f) => f.id)).toEqual(['a', 'b'])
  // The quality worker from Catalog.QualityStatus; no models store (nothing is downloaded).
  expect(shell.quality.get().value).toMatchObject({ state: 'idle', active: false })
  expect('models' in shell).toBe(false)

  // The Library's selection points at the folder that goes away.
  shell.actions.select('a')
  rows = [row('b', 'Orchard')]
  emit('library:changed', { reason: 'gone', folderId: 'a', name: 'Harbour walk' })
  await flush()
  expect(shell.toasts.items().map((t) => t.title)).toEqual(['“Harbour walk” left the Library'])
  expect(shell.actions.state().selected).toBeNull()
  expect(shell.library.get().value?.map((f) => f.id)).toEqual(['b'])

  // stop() ends the subscription with the rest of the shell.
  stop()
  const listed = lists
  emit('library:changed', { reason: 'gone', folderId: 'b', name: 'Orchard' })
  await flush()
  expect(shell.toasts.items()).toHaveLength(1)
  expect(lists).toBe(listed)
})
