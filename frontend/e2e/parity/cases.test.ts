// The case table against the prototype registry: every native surface
// (700–799) and every gallery state (800–899) has its skip, no case names a
// state the registry does not have, and enabled.json is valid.
import { expect, test } from 'bun:test'
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Scenario } from '../../prototype/registry'
import { CASES, GALLERY_SKIP, NATIVE_SKIP } from './cases'
import { parseEnabled, validateCases } from './parity'

async function registered(): Promise<Scenario[]> {
  const dir = join(import.meta.dir, '..', '..', 'prototype', 'scenarios')
  const all: Scenario[] = []
  for (const file of readdirSync(dir).filter((f) => f.endsWith('.tsx'))) {
    all.push(...((await import(join(dir, file))) as { scenarios: Scenario[] }).scenarios)
  }
  return all
}

test('the case table is valid', () => {
  expect(() => validateCases(CASES)).not.toThrow()
})

test('every native state is skipped as a native surface, every gallery state as the gallery', async () => {
  const all = await registered()
  expect(all.length).toBeGreaterThan(100)
  const skips = new Map(CASES.map((c) => [c.state, c.skip]))
  const native = all.filter((s) => s.group === 'native').map((s) => s.n)
  const gallery = all.filter((s) => s.group === 'gallery').map((s) => s.n)
  expect(native.length).toBeGreaterThan(0)
  expect(gallery.length).toBeGreaterThan(0)
  for (const n of native) expect([n, skips.get(n)]).toEqual([n, NATIVE_SKIP])
  for (const n of gallery) expect([n, skips.get(n)]).toEqual([n, GALLERY_SKIP])
})

test('no case names an unregistered state', async () => {
  const numbers = new Set((await registered()).map((s) => s.n))
  expect(CASES.map((c) => c.state).filter((n) => !numbers.has(n))).toEqual([])
})

test('enabled.json parses', () => {
  expect(() => parseEnabled(readFileSync(join(import.meta.dir, 'enabled.json'), 'utf8'))).not.toThrow()
})
