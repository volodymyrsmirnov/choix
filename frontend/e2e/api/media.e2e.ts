// Media: the /media handler refuses traversal and unknown folders, and
// serves byte ranges of an original.
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { BASE, expect, folderPath, rawGet, test, type FolderView, type RawResponse } from './fixtures'

/** getFollowing is rawGet that follows up to five redirects on the server. */
async function getFollowing(path: string): Promise<RawResponse> {
  let res = await rawGet(path)
  for (let i = 0; i < 5 && res.status >= 300 && res.status < 400; i++) {
    const next = new URL(String(res.headers.location), BASE)
    expect(next.origin, `the redirect of ${path} stays on the server`).toBe(BASE)
    res = await rawGet(next.pathname + next.search)
  }
  return res
}

test.describe.configure({ mode: 'serial' })

const path = folderPath('media')
let id = ''
let view: FolderView

test.beforeAll(async ({ harness }) => {
  await harness.resetLibrary()
  id = (await harness.addAndWait(path)).id
  view = await harness.call<FolderView>('Catalog', 'Open', id)
  expect(await harness.waitIdle()).toBe(true)
})

test('a traversal in a thumb URL is not found', async () => {
  // Sent verbatim, so the dot segments reach the server. Go's ServeMux
  // answers a path with dot segments with a redirect to its cleaned form
  // (/etc/passwd) before any handler runs; the redirect is followed, and
  // what it ends on must be a 404 that serves nothing.
  for (const p of [`/media/thumb/${id}/../../etc/passwd`, `/media/thumb/${id}/..%2F..%2Fetc%2Fpasswd`]) {
    const res = await getFollowing(p)
    expect(res.status, p).toBe(404)
    expect(res.body.toString('latin1'), p).not.toContain('root:')
  }
})

test('an unknown folder id is not found', async () => {
  const m = view.clusters[0].members[0]
  const res = await rawGet(`/media/thumb/0123456789abcdef/${m.path}?tier=thumb&v=${m.content_hash}`)
  expect(res.status).toBe(404)
})

test('a Range request on an original answers 206 with those bytes', async () => {
  const m = view.clusters[0].members[0]
  const original = readFileSync(join(path, m.path))
  const res = await rawGet(`/media/full/${id}/${m.path}?v=${m.content_hash}`, { Range: 'bytes=0-9' })
  expect(res.status).toBe(206)
  expect(res.headers['content-range']).toBe(`bytes 0-9/${original.length}`)
  expect(res.body.equals(original.subarray(0, 10))).toBe(true)
})

test.afterAll(async ({ harness }) => {
  await harness.resetLibrary()
})
