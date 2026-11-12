import { describe, expect, test } from 'bun:test'
import { overlayCoverage } from './flowCoverage'

describe('overlayCoverage', () => {
  test('passes when every declared and registered overlay was opened and closed', () => {
    expect(
      overlayCoverage(['a'], { registered: ['a', 'b'], opened: ['a', 'b'], closed: ['b', 'a'] }),
    ).toEqual([])
  })

  test('names an overlay that was opened but never closed', () => {
    expect(overlayCoverage([], { registered: ['menu'], opened: ['menu'], closed: [] })).toEqual([
      'overlay "menu" was opened but never closed',
    ])
  })

  test('a declared overlay counts even if it never registered (replicas register only while open)', () => {
    expect(overlayCoverage(['remove-folder'], { registered: [], opened: [], closed: [] })).toEqual([
      'overlay "remove-folder" was never opened or closed',
    ])
  })

  test('lists every missing id once, declared first, then registered', () => {
    expect(
      overlayCoverage(['x', 'y'], { registered: ['y', 'z'], opened: ['z'], closed: ['w'] }),
    ).toEqual([
      'overlay "x" was never opened or closed',
      'overlay "y" was never opened or closed',
      'overlay "z" was opened but never closed',
    ])
  })
})
