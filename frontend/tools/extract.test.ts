// The Node side of the fingerprint (tools/extract.ts) that needs no browser.
import { createHash } from 'node:crypto'
import { describe, expect, test } from 'bun:test'
import { canonicalJson, DENYLIST, hashTree, measuredDigests, resourceId, type Tree } from './extract'

describe('canonicalJson', () => {
  test('sorts keys at every level of nesting, keeps array order', () => {
    const a = { b: { z: 1, a: [{ y: 2, x: 1 }, 3] }, a: 'x' }
    expect(canonicalJson(a)).toBe('{"a":"x","b":{"a":[{"x":1,"y":2},3],"z":1}}')
  })

  test('the same content in another key order gives the same text and hash', () => {
    const one: Tree = { fonts: [], pseudoRules: [], motionRules: [], atRules: [], html: { style: 0, colorScheme: '' }, styles: [{ color: 'red', a: 'b' }], root: { tag: 'body', cls: [], text: '', attrs: {}, props: {}, style: 0, children: [] } }
    const two = JSON.parse('{"root":{"children":[],"style":0,"props":{},"attrs":{},"text":"","cls":[],"tag":"body"},"styles":[{"a":"b","color":"red"}],"pseudoRules":[],"html":{"colorScheme":"","style":0},"atRules":[],"motionRules":[],"fonts":[]}') as Tree
    expect(canonicalJson(two)).toBe(canonicalJson(one))
    expect(hashTree(two)).toBe(hashTree(one))
    expect(hashTree(one)).toBe(createHash('sha256').update(canonicalJson(one)).digest('hex'))
  })

  test('undefined members are left out, as JSON.stringify does', () => {
    expect(canonicalJson({ b: undefined, a: 1 })).toBe('{"a":1}')
  })
})

test('resourceId is res: and the first 16 hex of the sha256 of the bytes', () => {
  expect(resourceId('abc')).toBe(`res:${createHash('sha256').update('abc').digest('hex').slice(0, 16)}`)
})

test('measuredDigests merges both registries per id and lists the properties each may drop', () => {
  const out = measuredDigests(`${import.meta.dir}/..`, [
    { a: { files: ['package.json'], props: ['transform'] }, b: { files: ['tsconfig.json'], props: [] } },
    { a: { files: ['tsconfig.json'] } },
  ])
  expect(Object.keys(out).sort()).toEqual(['measured:a', 'measured:b', 'measuredProps:a', 'measuredProps:b'])
  expect(out['measured:a']).toMatch(/^a:[0-9a-f]{64}$/)
  expect(out['measured:a']).not.toBe(out['measured:b'].replace(/^b/, 'a'))
  expect(out['measuredProps:a']).toBe('transform')
  expect(out['measuredProps:b']).toBe('')
})

test('every denylist entry has a one-line reason and a probe declaration', () => {
  for (const [prop, { reason, probe }] of Object.entries(DENYLIST)) {
    expect(reason.length, prop).toBeGreaterThan(10)
    expect(reason.includes('\n'), prop).toBe(false)
    expect(probe.startsWith(`${prop}:`), prop).toBe(true)
  }
})
