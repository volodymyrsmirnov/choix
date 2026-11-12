// Library: adding a folder by path runs its job to `ready` with the
// expected summary and preview strip; adding it again, or a folder inside
// it, adds nothing. Every row carries when it was added (added_at, the
// Library's "Recently added" order). No model is downloaded while both model settings are off.
import { mkdirSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { BASE, dirs, expect, folderPath, test, type AddResult } from './fixtures'

test.describe.configure({ mode: 'serial' })

const path = folderPath('library')
let added: AddResult

test.beforeAll(async ({ harness }) => {
  await harness.resetLibrary()
})

test('a new folder is scanned to ready with its summary and previews', async ({ harness }) => {
  added = await harness.addFolder(path)
  expect(added).toMatchObject({ name: 'library', existing: false })
  expect(await harness.waitIdle(60_000)).toBe(true)

  const now = Math.floor(Date.now() / 1000)
  const list = await harness.list()
  expect(list).toHaveLength(1)
  const [f] = list
  expect(f).toMatchObject({ id: added.id, state: 'ready', photos: 24, month: '2026-03' })
  // added_at: unix seconds, when it was added (not in the future).
  expect(Number.isInteger(f.added_at)).toBe(true)
  expect(f.added_at).toBeGreaterThan(0)
  expect(f.added_at).toBeLessThanOrEqual(now + 1)
  expect(f.previews).toHaveLength(6)
  for (const url of f.previews) {
    const res = await harness.page.request.get(BASE + url)
    expect(res.status(), url).toBe(200)
    expect(res.headers()['content-type'], url).toBe('image/jpeg')
  }
})

test('adding the same folder again answers existing, same', async ({ harness }) => {
  const again = await harness.addFolder(path)
  expect(again).toMatchObject({ id: added.id, existing: true, relation: 'same' })
  expect(await harness.list()).toHaveLength(1)
})

test('adding a folder inside it answers inside', async ({ harness }) => {
  const sub = join(path, 'inside')
  mkdirSync(sub, { recursive: true })
  const res = await harness.addFolder(sub)
  expect(res).toMatchObject({ relation: 'inside' })
  expect(await harness.waitIdle()).toBe(true)
  expect((await harness.list()).map((f) => f.id)).toEqual([added.id])
})

test('a folder added later has an added_at at least as large', async ({ harness }) => {
  const later = join(dirs.root, 'library-later')
  mkdirSync(later, { recursive: true })
  const res = await harness.addFolder(later)
  expect(res).toMatchObject({ existing: false })
  expect(await harness.waitIdle()).toBe(true)
  const list = await harness.list()
  const first = list.find((f) => f.id === added.id)!
  const second = list.find((f) => f.id === res.id)!
  expect(Number.isInteger(second.added_at)).toBe(true)
  expect(second.added_at).toBeGreaterThanOrEqual(first.added_at)
})

test('no model download is attempted with both model settings off', async ({ harness }) => {
  const settings = await harness.call<{ visual_grouping: boolean; quality_rating: boolean }>('Settings', 'Get')
  expect(settings).toMatchObject({ visual_grouping: false, quality_rating: false })
  const status = await harness.call<{ kind: string; state: string; received: number }[]>('Models', 'Status')
  expect(status.map((s) => [s.kind, s.state, s.received])).toEqual([
    ['clip', 'disabled', 0],
    ['quality', 'disabled', 0],
  ])
  // The rating is off: no pass runs, so the status names no folder and lists
  // no pass (both are left out of the payload, never null).
  const quality = await harness.call<Record<string, unknown>>('Models', 'Quality')
  expect(quality).toMatchObject({ enabled: false, state: 'disabled', active: false })
  expect(quality.passes ?? [], `Models.Quality ${JSON.stringify(quality)}`).toEqual([])
  expect(quality.folder_id, `Models.Quality ${JSON.stringify(quality)}`).toBeUndefined()
  // Nothing was written into the data directory's models directory.
  let models: string[] = []
  try {
    models = readdirSync(join(dirs.data, 'models'))
  } catch {
    // no models directory at all
  }
  expect(models).toEqual([])
})

test.afterAll(async ({ harness }) => {
  await harness.resetLibrary()
})
