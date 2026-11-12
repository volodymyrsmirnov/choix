// serve-app.mjs: the parity build at `/`, the prototype's mock photos byte
// for byte under /mock/photos/, a member's /media/ URL as the photo the
// manifest names for its content hash, nothing outside either folder,
// 127.0.0.1 only.
import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import type { Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { MEDIA } from './fixtures/table'
import { loadMedia, PHOTOS, serve } from './serve-app.mjs'

let dir = ''
let server: Server
let base = ''

beforeAll(async () => {
  dir = mkdtempSync(join(tmpdir(), 'serve-app-'))
  mkdirSync(join(dir, 'assets'))
  writeFileSync(join(dir, 'index.html'), '<!doctype html><div id="root"></div>')
  writeFileSync(join(dir, 'assets', 'main.js'), 'export {}')
  writeFileSync(join(tmpdir(), 'serve-app-outside.txt'), 'outside')
  server = (await serve({ port: 0, dist: dir, media: { c0ffee1005: 'p07.jpg' } })) as Server
  const addr = server.address() as AddressInfo
  base = `http://127.0.0.1:${addr.port}`
  expect(addr.address).toBe('127.0.0.1')
})

afterAll(() => {
  server?.close()
  rmSync(dir, { recursive: true, force: true })
})

test('/ is the build’s index.html, its assets beside it', async () => {
  const index = await fetch(`${base}/`)
  expect(index.status).toBe(200)
  expect(index.headers.get('content-type')).toContain('text/html')
  expect(await index.text()).toContain('id="root"')
  const js = await fetch(`${base}/assets/main.js`)
  expect(js.headers.get('content-type')).toContain('text/javascript')
})

test('/mock/photos/<name> serves the prototype’s photo byte for byte', async () => {
  const r = await fetch(`${base}/mock/photos/p01.jpg`)
  expect(r.status).toBe(200)
  expect(r.headers.get('content-type')).toBe('image/jpeg')
  expect(new Uint8Array(await r.arrayBuffer())).toEqual(new Uint8Array(readFileSync(join(PHOTOS as string, 'p01.jpg'))))
})

test('nothing outside the two folders, nothing but GET and HEAD', async () => {
  expect((await fetch(`${base}/mock/photos/%2e%2e%2fdata.ts`)).status).toBe(403)
  expect((await fetch(`${base}/%2e%2e%2fserve-app-outside.txt`)).status).toBe(403)
  expect((await fetch(`${base}/missing.js`)).status).toBe(404)
  expect((await fetch(`${base}/`, { method: 'POST' })).status).toBe(405)
})

test('/media/thumb and /media/full serve the photo the manifest names for the URL’s content hash (v)', async () => {
  const p07 = new Uint8Array(readFileSync(join(PHOTOS as string, 'p07.jpg')))
  for (const url of ['/media/thumb/dolomites/Day%202/DSCF4101.RAF?tier=thumb&v=c0ffee1005', '/media/full/dolomites/Day%202/DSCF4101.RAF?v=c0ffee1005']) {
    const r = await fetch(`${base}${url}`)
    expect([url, r.status, r.headers.get('content-type')]).toEqual([url, 200, 'image/jpeg'])
    expect(new Uint8Array(await r.arrayBuffer())).toEqual(p07)
  }
  expect((await fetch(`${base}/media/thumb/dolomites/a.RAF?tier=thumb&v=c0ffee9999`)).status).toBe(404)
  expect((await fetch(`${base}/media/thumb/dolomites/a.RAF?tier=thumb`)).status).toBe(404)
  expect((await fetch(`${base}/media/thumb/dolomites/a.RAF?v=constructor`)).status).toBe(404)
  expect((await fetch(`${base}/media/other/dolomites/a.RAF?v=c0ffee1005`)).status).toBe(404)
})

test('the manifest the server loads is the fixtures’ (bun fixtures/media.ts)', () => {
  expect(loadMedia()).toEqual(MEDIA)
})
