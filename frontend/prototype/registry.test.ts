import { describe, expect, test } from 'bun:test'
import { createElement } from 'react'
import { defineScenarios, GROUPS, RegistryError, type GroupId, type Scenario } from './registry'

const s = (n: number, group: GroupId): Scenario => ({
  n,
  group,
  label: `state ${n}`,
  design: 'missing design',
  render: () => createElement('div'),
})

function codeOf(fn: () => unknown): string | undefined {
  try {
    fn()
  } catch (e) {
    if (e instanceof RegistryError) return e.code
    throw e
  }
  return undefined
}

describe('defineScenarios', () => {
  test('returns the scenarios ordered by number', () => {
    const out = defineScenarios([s(102, 'library'), s(10, 'welcome'), s(100, 'library')], [])
    expect(out.map((x) => x.n)).toEqual([10, 100, 102])
  })

  test('a number used twice is a duplicate', () => {
    expect(codeOf(() => defineScenarios([s(102, 'library'), s(102, 'library')], []))).toBe('duplicate')
  })

  test('a library scenario numbered 250 is out of range', () => {
    expect(codeOf(() => defineScenarios([s(250, 'library')], []))).toBe('out-of-range')
  })

  test('a retired number cannot be used again', () => {
    expect(codeOf(() => defineScenarios([s(107, 'library')], [107]))).toBe('retired')
  })

  test('the error names the scenario', () => {
    expect(() => defineScenarios([s(250, 'library')], [])).toThrow(/s250/)
  })
})

describe('GROUPS', () => {
  test('every group has a range and no two ranges overlap', () => {
    const ids: string[] = GROUPS.map((g) => g.id).sort()
    expect(ids).toEqual(
      ['compare', 'focus', 'folder', 'gallery', 'library', 'native', 'overlays', 'settings', 'welcome'].sort(),
    )
    const ranges = GROUPS.map((g) => g.range).sort((a, b) => a[0] - b[0])
    for (let i = 1; i < ranges.length; i++) expect(ranges[i]![0]).toBeGreaterThan(ranges[i - 1]![1])
  })

  test('ranges follow the plan', () => {
    const range = (id: GroupId) => GROUPS.find((g) => g.id === id)?.range
    expect(range('welcome')).toEqual([10, 49])
    expect(range('library')).toEqual([100, 199])
    expect(range('compare')).toEqual([400, 449])
    expect(range('gallery')).toEqual([800, 899])
  })
})
