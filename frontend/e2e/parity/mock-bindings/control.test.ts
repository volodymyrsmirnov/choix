// The parity build's mock bindings (control.ts and the services beside it):
// answers come from the loaded fixture, 'pending' never answers, 'error'
// rejects the way the Wails runtime rejects a Go error, emit reaches the
// runtime's Events.On subscribers, and every generated binding the app
// calls has a mock of the same name and type (the app calls no Models
// binding: the parity build has no mock of it).
import { expect, test } from 'bun:test'
import { existsSync } from 'node:fs'
import * as RealCatalog from '@bindings/catalog/service'
import * as RealLibrary from '@bindings/library/service'
import * as RealSettings from '@bindings/settings/service'
import * as RealSystem from '@bindings/system/service'
import { appError } from '../../../src/app/shell/errors'
import type { FolderSummary } from '../../../src/design/types'
import * as Catalog from './catalog/service'
import { createControl, emit, load, type ParityFixture } from './control'
import * as Library from './library/service'
import { Events } from './runtime'
import * as Settings from './settings/service'
import * as System from './system/service'

// The type test: each mock module has the generated module's type (same
// names, same parameters, the same CancellablePromise results). A missing
// or mistyped mock fails `bun run check`.
const typed: [typeof RealCatalog, typeof RealLibrary, typeof RealSettings, typeof RealSystem] = [Catalog, Library, Settings, System]

const FOLDER: FolderSummary & { added_at: number } = {
  id: 'f1',
  name: 'Invented',
  parentPath: '~/Pictures',
  state: 'ready',
  month: '2026-01',
  firstCaptured: '2026-01-02T10:00:00Z',
  lastCaptured: '2026-01-02T11:00:00Z',
  photos: 3,
  videos: 0,
  clusters: 1,
  picked: 0,
  rejected: 0,
  previews: [],
  added_at: 1_767_000_000,
}

/** Whether `p` settles within a few macrotasks. */
async function settles(p: PromiseLike<unknown>): Promise<boolean> {
  let done = false
  p.then(
    () => (done = true),
    () => (done = true),
  )
  for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0))
  return done
}

test('load resolves bindings from the fixture', async () => {
  const f: ParityFixture = { route: '#/', folders: [FOLDER] }
  await load(f)
  expect(await Library.List()).toEqual([FOLDER])
})

test('a call made before load answers once the fixture is loaded, with a copy', async () => {
  const c = createControl()
  const early = c.answer('Library.List', (f) => f.folders)
  expect(await settles(early)).toBe(false)
  await c.load({ route: '#/', folders: [FOLDER] })
  const a = await early
  expect(a).toEqual([FOLDER])
  const b = await c.answer('Library.List', (f) => f.folders)
  expect(b).toEqual(a)
  expect(b).not.toBe(a)
})

test('a field the fixture leaves out never answers', async () => {
  const c = createControl()
  await c.load({ route: '#/' })
  expect(await settles(c.answer('Settings.Get', (f) => f.settings))).toBe(false)
})

test('the parity build has no Models mock: Catalog.QualityStatus is the only quality source', () => {
  expect(existsSync(new URL('./models', import.meta.url))).toBe(false)
})

test('the app-facing host hands out the ui record and the toasts, and tells listeners of each load', async () => {
  const c = createControl()
  let loads = 0
  c.uiHost.subscribe(() => loads++)
  expect(c.uiHost.ui()).toEqual({})
  expect(c.uiHost.toasts()).toEqual([])
  await c.load({ route: '#/', ui: { 'settings.reveal': 'models' }, toasts: [{ id: 'a', tone: 'info', title: 'Hello' }] })
  expect(loads).toBe(1)
  expect(c.uiHost.ui()).toEqual({ 'settings.reveal': 'models' })
  expect(c.uiHost.toasts()).toEqual([{ id: 'a', tone: 'info', title: 'Hello' }])
})

test('Catalog.QualityStatus answers the fixture\'s quality status, with the received and size Go still sends', async () => {
  const quality = { enabled: true, state: 'scoring', active: true, done: 2, total: 9, rated: 5 } as ParityFixture['quality']
  await load({ route: '#/', quality })
  const wire = { received: 0, size: 0, ...quality }
  expect(await Catalog.QualityStatus()).toEqual(wire as never)
})

test("'pending' never resolves", async () => {
  await load({ route: '#/', folders: 'pending', view: 'pending', focus: 'pending' })
  expect(await settles(Library.List())).toBe(false)
  expect(await settles(Catalog.Clusters('f1'))).toBe(false)
  expect(await settles(Catalog.Cluster('f1', 1))).toBe(false)
})

test("'error' rejects with an app error", async () => {
  await load({ route: '#/settings', settings: 'error' })
  let caught: unknown
  try {
    await Settings.Get()
  } catch (e) {
    caught = e
  }
  // The Wails runtime's shape: an Error whose `cause` is Go's app error JSON.
  // (Plain comparisons: bun's toMatchObject writes its matchers into the received object.)
  expect(caught).toBeInstanceOf(Error)
  expect((caught as Error).name).toBe('RuntimeError')
  expect((caught as Error).cause).toEqual({ code: 'internal', message: 'parity fixture: Settings.Get failed' })
  // …which the app reads as that app error.
  expect(appError(caught)).toEqual({ code: 'internal', message: 'parity fixture: Settings.Get failed' })
})

test("an acting binding answers at once, unless the fixture's calls name it: 'pending' never answers, an app error rejects", async () => {
  const c = createControl()
  expect(await c.act('Catalog.Recluster', 'done')).toBe('done')
  await c.load({ route: '#/', calls: { 'Catalog.Recluster': 'pending' } })
  expect(await settles(c.act('Catalog.Recluster', undefined))).toBe(false)
  expect(await c.act('Catalog.Close', 'other')).toBe('other')
  await c.load({ route: '#/', calls: { 'Catalog.Recluster': { code: 'read_only', message: 'the drive is read-only' } } })
  let caught: unknown
  try {
    await c.act('Catalog.Recluster', undefined)
  } catch (e) {
    caught = e
  }
  expect((caught as Error).name).toBe('RuntimeError')
  expect(appError(caught)).toEqual({ code: 'read_only', message: 'the drive is read-only' })
})

test("Catalog.Recluster answers as the fixture's calls say", async () => {
  await load({ route: '#/', calls: { 'Catalog.Recluster': 'pending' } })
  expect(await settles(Catalog.Recluster('f1'))).toBe(false)
  await load({ route: '#/' })
  expect(await settles(Catalog.Recluster('f1'))).toBe(true)
})

test("Catalog.SetPick answers Go's state at once, unless the fixture's calls name it", async () => {
  await load({ route: '#/', calls: { 'Catalog.SetPick': 'pending' } })
  expect(await settles(Catalog.SetPick('f1', 7, 'unpick'))).toBe(false)
  await load({ route: '#/' })
  expect(await Catalog.SetPick('f1', 7, 'unpick')).toEqual({ file_id: 7, state: 'unmarked' })
  expect(await Catalog.SetPick('f1', 7, 'reject')).toEqual({ file_id: 7, state: 'rejected' })
})

test("Settings.Save answers saved at once, unless the fixture's calls name it: in flight, or refused with Go's error", async () => {
  await load({ route: '#/settings', calls: { 'Settings.Save': 'pending' } })
  expect(await settles(Settings.Save({ picks_dir: 'x' }))).toBe(false)
  await load({ route: '#/settings', calls: { 'Settings.Save': { code: 'internal', message: 'the disk is full' } } })
  let caught: unknown
  try {
    await Settings.Save({ picks_dir: 'x' })
  } catch (e) {
    caught = e
  }
  expect(appError(caught)).toEqual({ code: 'internal', message: 'the disk is full' })
  await load({ route: '#/settings' })
  expect(await Settings.Save({ picks_dir: 'x' })).toEqual({ saved: true, reclustered: false })
})

test("Settings.Get 'pending' never answers", async () => {
  await load({ route: '#/settings', settings: 'pending' })
  expect(await settles(Settings.Get())).toBe(false)
})

test("a data binding the fixture's calls name answers as they say instead of from its field", async () => {
  const c = createControl()
  await c.load({ route: '#/', focus: 'pending', calls: { 'Catalog.Cluster': { code: 'internal', message: 'disk I/O error' } } })
  let caught: unknown
  try {
    await c.answer('Catalog.Cluster', (f) => f.focus)
  } catch (e) {
    caught = e
  }
  expect((caught as Error).name).toBe('RuntimeError')
  expect(appError(caught)).toEqual({ code: 'internal', message: 'disk I/O error' })
  await c.load({ route: '#/', folders: [FOLDER], calls: { 'Library.List': 'pending' } })
  expect(await settles(c.answer('Library.List', (f) => f.folders))).toBe(false)
  // Other data bindings still answer from their fields.
  await c.load({ route: '#/', folders: [FOLDER], calls: { 'Catalog.Cluster': 'pending' } })
  expect(await c.answer('Library.List', (f) => f.folders)).toEqual([FOLDER])
})

test('emit reaches Events.On subscribers', () => {
  const seen: unknown[] = []
  const off = Events.On('library:changed', (ev) => seen.push([ev.name, ev.data]))
  emit('library:changed', { reason: 'stats' })
  off()
  emit('library:changed', { reason: 'after-off' })
  expect(seen).toEqual([['library:changed', { reason: 'stats' }]])
})

test('every generated binding name the app calls has a mock', () => {
  const names = (m: object) => Object.keys(m).sort()
  expect(typed.map(names)).toEqual([RealCatalog, RealLibrary, RealSettings, RealSystem].map(names))
})
