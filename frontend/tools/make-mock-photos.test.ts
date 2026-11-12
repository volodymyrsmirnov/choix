import { afterAll, expect, test } from 'bun:test'
import { mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import jpeg from 'jpeg-js'
import { makeMockPhotos, PHOTO_COUNT } from './make-mock-photos'

const dirs: string[] = []
const run = () => {
  const dir = mkdtempSync(join(tmpdir(), 'choix-mock-'))
  dirs.push(dir)
  makeMockPhotos(dir)
  return dir
}
afterAll(() => {
  for (const d of dirs) rmSync(d, { recursive: true, force: true })
})

const a = run()
const b = run()
const names = readdirSync(join(a, 'photos')).sort()

test('writes p01–p36, broken.jpg and photos.json', () => {
  const want = Array.from({ length: PHOTO_COUNT }, (_, i) => `p${String(i + 1).padStart(2, '0')}.jpg`)
  expect(PHOTO_COUNT).toBe(36)
  expect(names).toEqual([...want, 'broken.jpg'].sort())
  const meta = JSON.parse(readFileSync(join(a, 'photos.json'), 'utf8')) as Record<string, string>
  expect(Object.keys(meta).sort()).toEqual(want)
  for (const t of Object.values(meta)) expect(t).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/)
})

test('two runs are byte-identical', () => {
  for (const name of names) {
    expect(readFileSync(join(a, 'photos', name)).equals(readFileSync(join(b, 'photos', name)))).toBe(true)
  }
  expect(readFileSync(join(a, 'photos.json')).equals(readFileSync(join(b, 'photos.json')))).toBe(true)
})

test('each photo is a 480×320 JPEG of at most 40 KB', () => {
  for (const name of names.filter((n) => n !== 'broken.jpg')) {
    const buf = readFileSync(join(a, 'photos', name))
    expect(statSync(join(a, 'photos', name)).size).toBeLessThanOrEqual(40 * 1024)
    const img = jpeg.decode(buf, { useTArray: true })
    expect([img.width, img.height]).toEqual([480, 320])
  }
})

test('broken.jpg does not decode', () => {
  const buf = readFileSync(join(a, 'photos', 'broken.jpg'))
  expect(buf.length).toBeGreaterThan(0)
  expect(() => jpeg.decode(buf)).toThrow()
})
