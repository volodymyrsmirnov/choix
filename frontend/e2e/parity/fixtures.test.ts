// fixtureFor: every fixture survives the trip into the page (structured
// clone), names no photo by a file: URL, and serves each prototype photo's
// bytes; the case table drives exactly the states that have a fixture.
import { expect, test } from 'bun:test'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { DOLOMITES, SIX } from '../../prototype/mock/data'
import { CASES, FOCUS_KEPT_DRIVES, FOCUS_SKIPS, OVERLAY_DRIVES, OVERLAY_WRAPPER_SKIP } from './cases'
import { fixtureFor, fixtureStates, INLINE_LIMIT, PHOTOS_PATH, served, servedPhoto } from './fixtures'
import { TABLE } from './fixtures/table'

const PHOTOS = join(import.meta.dir, '..', '..', 'prototype', 'mock', 'photos')

/** The dump's stdout limit: the harness's (fixtures.ts reads the dump with the same); the table is past Node's 1 MB default. */
const DUMP_MAX_BUFFER = 256 * 1024 * 1024

test('every fixture is plain JSON data (Node reads the table as JSON) with no file: URL in it', () => {
  expect(fixtureStates().length).toBeGreaterThan(0)
  for (const n of fixtureStates()) {
    const f = fixtureFor(n)
    expect(JSON.parse(JSON.stringify(f))).toEqual(f)
    expect(JSON.stringify(f)).not.toContain('file:')
    expect(f.route.startsWith('#/')).toBe(true)
  }
})

test('a photo at or over the inline limit is served from /mock/photos/, a smaller one as the data: URL Vite inlines', () => {
  const big = servedPhoto(DOLOMITES.previews[0]!)
  expect(big.startsWith(PHOTOS_PATH)).toBe(true)
  const bytes = readFileSync(join(PHOTOS, big.slice(PHOTOS_PATH.length)))
  expect(bytes.length).toBeGreaterThanOrEqual(INLINE_LIMIT)

  const brokenHref = new URL('../../prototype/mock/photos/broken.jpg', import.meta.url).href
  const small = readFileSync(join(PHOTOS, 'broken.jpg'))
  expect(small.length).toBeLessThan(INLINE_LIMIT)
  expect(servedPhoto(brokenHref)).toBe(`data:image/jpeg;base64,${small.toString('base64')}`)
})

test('served rewrites photo URLs at any depth and leaves everything else alone', () => {
  const out = served({ folders: SIX, n: 3, none: null, text: 'Kyoto' })
  expect(out.folders.flatMap((f) => f.previews).every((p) => p.startsWith(PHOTOS_PATH))).toBe(true)
  expect(out.folders.map((f) => f.name)).toEqual(SIX.map((f) => f.name))
  expect(out).toMatchObject({ n: 3, none: null, text: 'Kyoto' })
})

test('the dump Node reads is the table bun imports', () => {
  const dump = join(import.meta.dir, 'fixtures', 'dump.ts')
  const r = spawnSync('bun', [dump], { encoding: 'utf8', maxBuffer: DUMP_MAX_BUFFER })
  // Says why when the dump cannot be read (an overflow is ENOBUFS, with no exit status).
  if (r.error || r.status !== 0) {
    throw new Error(`\`bun ${dump}\` failed (${r.error?.message ?? `exit ${r.status}`}):\n${r.stderr ?? ''}`)
  }
  expect(JSON.parse(r.stdout)).toEqual(JSON.parse(JSON.stringify(TABLE)))
})

test('an unknown state has no fixture', () => {
  expect(() => fixtureFor(1)).toThrow('no fixture for #1')
})

test('every state with a fixture is driven, or skipped with its drive kept (OVERLAY_DRIVES, FOCUS_KEPT_DRIVES), and only those', () => {
  const driven = CASES.filter((c) => c.drive).map((c) => c.state)
  const overlays = Object.keys(OVERLAY_DRIVES).map(Number)
  for (const n of overlays) expect([n, CASES.find((c) => c.state === n)?.skip]).toEqual([n, OVERLAY_WRAPPER_SKIP])
  const focus = Object.keys(FOCUS_KEPT_DRIVES).map(Number)
  expect(focus).toEqual(Object.keys(FOCUS_SKIPS).map(Number))
  for (const n of focus) expect([n, CASES.find((c) => c.state === n)?.skip]).toEqual([n, FOCUS_SKIPS[n]])
  expect([...driven, ...overlays, ...focus].sort((a, b) => a - b)).toEqual(fixtureStates())
})
