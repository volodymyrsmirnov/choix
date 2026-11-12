// The build stamp (tools/build-prototype.mjs) with the digest and the vite
// build replaced, so no real build runs.
import { afterEach, expect, test } from 'bun:test'
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { BuildError, buildPrototype } from './build-prototype.mjs'

const dirs: string[] = []
afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true })
})
const stampPath = () => {
  const d = mkdtempSync(join(tmpdir(), 'choix-build-'))
  dirs.push(d)
  return join(d, 'build.json')
}
/** A digest that answers `values` in turn; an Error value is thrown. */
const digests = (...values: (string | Error)[]) => () => {
  const v = values.shift()
  if (v instanceof Error) throw v
  return v as string
}

test('a stable digest is stamped and returned', () => {
  const out = stampPath()
  expect(buildPrototype({ digest: digests('d1', 'd1'), build: () => {}, out })).toBe('d1')
  expect(JSON.parse(readFileSync(out, 'utf8'))).toEqual({ sourceDigest: 'd1' })
})

test('a digest that moved during the build removes the stamp', () => {
  const out = stampPath()
  expect(() => buildPrototype({ digest: digests('d1', 'd2'), build: () => {}, out })).toThrow(BuildError)
  expect(existsSync(out)).toBe(false)
})

test('a second digest that fails removes the stamp (it would be unverified)', () => {
  const out = stampPath()
  expect(() => buildPrototype({ digest: digests('d1', new BuildError('gate.py digest failed')), build: () => {}, out })).toThrow(/digest failed/)
  expect(existsSync(out)).toBe(false)
})

test('a failed build writes no stamp', () => {
  const out = stampPath()
  expect(() =>
    buildPrototype({
      digest: digests('d1', 'd1'),
      build: () => {
        throw new BuildError('the prototype build failed')
      },
      out,
    }),
  ).toThrow(/build failed/)
  expect(existsSync(out)).toBe(false)
})
