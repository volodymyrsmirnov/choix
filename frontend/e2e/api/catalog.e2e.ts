// Catalog: opening a scanned folder, its thumbnails, pick / reject / undo
// with the copies they make and remove under <folder>/picks/, parallel
// picks, and a bucket-size change that re-clusters the open folder.
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { BASE, expect, folderPath, test, type FolderView, type Member, type PickResult } from './fixtures'

test.describe.configure({ mode: 'serial' })

const path = folderPath('catalog')
const picksDir = join(path, 'picks')
let id = ''
let view: FolderView
let members: Member[] = []
let bucketBefore = 0

/** picked returns the files in the folder's picks directory. */
function picked(): string[] {
  return existsSync(picksDir) ? readdirSync(picksDir).filter((n) => !n.startsWith('.')) : []
}

test.beforeAll(async ({ harness }) => {
  await harness.resetLibrary()
  bucketBefore = (await harness.call<{ bucket_size: number }>('Settings', 'Get')).bucket_size
  id = (await harness.addAndWait(path)).id
})

test('Open returns the cluster grid of a two-camera folder', async ({ harness }) => {
  view = await harness.call<FolderView>('Catalog', 'Open', id)
  // Open queues a rescan of the folder at the front; let it finish.
  expect(await harness.waitIdle()).toBe(true)
  expect(view.clusters.length).toBeGreaterThanOrEqual(3)
  expect(view.devices).toHaveLength(2)
  expect(view.picks_dir).toBe('picks')
  members = view.clusters.flatMap((c) => c.members)
  expect(members).toHaveLength(24)
})

test("a member's thumbnail is served", async ({ harness }) => {
  const m = members[0]
  const url = `${BASE}/media/thumb/${id}/${m.path.split('/').map(encodeURIComponent).join('/')}?tier=thumb&v=${m.content_hash}`
  const res = await harness.page.request.get(url)
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toBe('image/jpeg')
})

test('pick copies the original, reject removes the copy, undo reverts both', async ({ harness }) => {
  const m = members[0]
  const original = readFileSync(join(path, m.path))

  const pick = await harness.call<PickResult>('Catalog', 'SetPick', id, m.file_id, 'pick')
  expect(pick.state).toBe('picked')
  expect(pick.exported_path).toBeTruthy()
  const copy = join(path, pick.exported_path!)
  expect(copy.startsWith(picksDir + '/')).toBe(true)
  expect(readFileSync(copy).equals(original)).toBe(true)
  expect(readFileSync(join(path, m.path)).equals(original)).toBe(true)

  const reject = await harness.call<PickResult>('Catalog', 'SetPick', id, m.file_id, 'reject')
  expect(reject.state).toBe('rejected')
  expect(existsSync(copy)).toBe(false)

  const undo1 = await harness.call<PickResult>('Catalog', 'Undo', id)
  expect(undo1).toMatchObject({ file_id: m.file_id, state: 'picked' })
  expect(picked()).toHaveLength(1)

  const undo2 = await harness.call<PickResult>('Catalog', 'Undo', id)
  expect(undo2).toMatchObject({ file_id: m.file_id, state: 'unmarked' })
  expect(picked()).toEqual([])
  expect(readFileSync(join(path, m.path)).equals(original)).toBe(true)
})

test('ten parallel picks on different files all succeed', async ({ harness }) => {
  const ten = members.slice(1, 11).map((m) => m.file_id)
  const results = await harness.page.evaluate(
    async ({ id, ten }) =>
      Promise.allSettled(ten.map((f) => window.choix.Catalog.SetPick(id, f, 'pick'))).then((rs) =>
        rs.map((r) => (r.status === 'fulfilled' ? { ok: true, state: r.value.state } : { ok: false, error: String(r.reason) })),
      ),
    { id, ten },
  )
  expect(results).toEqual(ten.map(() => ({ ok: true, state: 'picked' })))
  expect(picked()).toHaveLength(10)
})

test('a smaller bucket size re-clusters the open folder into more clusters', async ({ harness }) => {
  const before = (await harness.call<FolderView>('Catalog', 'Clusters', id)).clusters.length
  const saved = await harness.call<{ saved: boolean; reclustered: boolean }>('Settings', 'Save', { bucket_size: 60 })
  expect(saved).toMatchObject({ saved: true, reclustered: true })
  const after = (await harness.call<FolderView>('Catalog', 'Clusters', id)).clusters.length
  expect(after).toBeGreaterThan(before)
})

test.afterAll(async ({ harness }) => {
  if (bucketBefore > 0) {
    await harness.call('Settings', 'Save', { bucket_size: bucketBefore })
  }
  await harness.resetLibrary()
})
