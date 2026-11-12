import { createHash } from 'node:crypto'
import { describe, expect, test } from 'bun:test'
import { formatFile, parseStates, stateHash } from './snapshot.mjs'

describe('parseStates', () => {
  test('ranges and single numbers, sorted, without repeats', () => {
    expect(parseStates('300,100-102,101')).toEqual([100, 101, 102, 300])
  })
  test('rejects a reversed range and anything else', () => {
    expect(() => parseStates('5-3')).toThrow(/range/)
    expect(() => parseStates('1,a')).toThrow(/entry/)
  })
})

describe('the file', () => {
  test('sorted keys at every level (code-unit order), two-space indent, trailing newline', () => {
    const text = formatFile({ version: 1, states: { '11': { b: 1, a: 2 }, '100': {} }, browser: 'x' })
    expect(text).toBe('{\n  "browser": "x",\n  "states": {\n    "100": {},\n    "11": {\n      "a": 2,\n      "b": 1\n    }\n  },\n  "version": 1\n}\n')
  })

  test('a state hash is sha256 of the min, default and large hashes, then browser, then extractor', () => {
    const v = { 'min/dark/macos': 'a', 'default/dark/macos': 'b', 'large/dark/macos': 'c' }
    expect(stateHash(v, 'chromium 1', 'e')).toBe(createHash('sha256').update('abcchromium 1e').digest('hex'))
  })
})
