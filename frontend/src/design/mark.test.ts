import { expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// The design copy of the brand mark must not drift from the app icon master.
test('assets/mark.svg is byte-identical to assets/icon/favicon.svg', () => {
  const repo = join(import.meta.dir, '..', '..', '..')
  const master = readFileSync(join(repo, 'assets', 'icon', 'favicon.svg'))
  const copy = readFileSync(join(import.meta.dir, 'assets', 'mark.svg'))
  expect(copy.equals(master)).toBe(true)
})
