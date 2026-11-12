// Fixture numbers never sit where a real prototype state may: they live in
// their own blocks at the top of the gallery range (kinds.ts), and no group
// file under prototype/scenarios uses a number there.
import { expect, test } from 'bun:test'
import { readdirSync } from 'node:fs'
import { join } from 'node:path'
import type { Scenario } from '../../prototype/registry'
import { BROKEN, CLEAN, FIXTURE_BLOCKS, PARITY } from './kinds'

const inBlock = (n: number, [lo, hi]: readonly [number, number]) => n >= lo && n <= hi

test('broken fixtures sit at 880–899, clean and parity ones at 870–879, each number once', () => {
  for (const n of Object.values(BROKEN)) expect(inBlock(n, FIXTURE_BLOCKS.broken)).toBe(true)
  for (const n of [...Object.values(CLEAN), ...Object.values(PARITY)]) expect(inBlock(n, FIXTURE_BLOCKS.clean)).toBe(true)
  const all = [...Object.values(BROKEN), ...Object.values(CLEAN), ...Object.values(PARITY)]
  expect(new Set(all).size).toBe(all.length)
})

test('no prototype state uses a fixture number', async () => {
  const dir = join(import.meta.dir, '..', '..', 'prototype', 'scenarios')
  const used: number[] = []
  for (const file of readdirSync(dir).filter((f) => f.endsWith('.tsx'))) {
    const mod = (await import(join(dir, file))) as { scenarios: Scenario[] }
    used.push(...mod.scenarios.map((s) => s.n))
  }
  expect(used.length).toBeGreaterThan(100)
  const reserved = (n: number) => inBlock(n, FIXTURE_BLOCKS.clean) || inBlock(n, FIXTURE_BLOCKS.broken)
  expect(used.filter(reserved)).toEqual([])
})
