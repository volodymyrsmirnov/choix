import { expect, test } from 'bun:test'
import { existsSync } from 'node:fs'
import { join } from 'node:path'
import { MEASURED } from '../src/design/measured'
import { PROTO_MEASURED } from './measured'

const FRONTEND = join(import.meta.dir, '..')

test('every measured id names files that exist, on both sides', () => {
  for (const [id, { files }] of [...Object.entries(MEASURED), ...Object.entries(PROTO_MEASURED)]) {
    expect(files.length, id).toBeGreaterThan(0)
    for (const f of files) expect(existsSync(join(FRONTEND, f)), `${id}: ${f}`).toBe(true)
  }
})

test('the prototype side only extends ids src/design/measured.ts declares', () => {
  for (const id of Object.keys(PROTO_MEASURED)) expect(Object.keys(MEASURED)).toContain(id)
})

test('every data-measured-by id written in src/design is registered, with the properties it may drop', () => {
  const { readdirSync, readFileSync, statSync } = require('node:fs') as typeof import('node:fs')
  const walk = (dir: string): string[] =>
    readdirSync(dir).flatMap((n) => {
      const p = join(dir, n)
      return statSync(p).isDirectory() ? walk(p) : /\.tsx?$/.test(n) ? [p] : []
    })
  // Comments stripped: they mention `data-measured-by="<id>"` as prose.
  const files = walk(join(FRONTEND, 'src/design')).map((f) =>
    readFileSync(f, 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/(^|[^:])\/\/.*$/gm, '$1'),
  )
  // String constants anywhere in src/design: `data-measured-by={NAME}` names one.
  const constants = new Map<string, string>()
  for (const text of files) {
    for (const m of text.matchAll(/\bconst\s+([A-Za-z_$][\w$]*)\s*(?::\s*string\s*)?=\s*(['"])([^'"]*)\2/g)) constants.set(m[1], m[3])
  }
  const used = new Set<string>()
  const unresolved: string[] = []
  for (const text of files) {
    for (const m of text.matchAll(/data-measured-by=(?:"([^"]*)"|'([^']*)'|\{\s*(?:(['"`])([^'"`]*)\3|([A-Za-z_$][\w$.]*))\s*\})/g)) {
      const literal = m[1] ?? m[2] ?? m[4]
      if (literal !== undefined) used.add(literal)
      else if (constants.has(m[5])) used.add(constants.get(m[5])!)
      else unresolved.push(m[0])
    }
  }
  // Anything else (a computed id) cannot be checked here: write a literal or a constant.
  expect(unresolved).toEqual([])
  // ClusterStrip writes data-measured-by={CLUSTER_STRIP_MEASURE}.
  expect(used).toContain('cluster-strip')
  expect(used.size).toBeGreaterThan(0)
  for (const id of used) expect(Object.keys(MEASURED), id).toContain(id)
  for (const [id, { props }] of Object.entries(MEASURED)) expect(Array.isArray(props), id).toBe(true)
})
