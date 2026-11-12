// Result files of a design run: each check-states or flows test writes one
// JSON file under .design/results/<kind>/, and the global teardown prints one
// summary line per kind. Pure apart from the two fs helpers at the bottom.
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Finding } from './checks/dom'

export type Kind = 'check-states' | 'flows'

export interface Result {
  browser: string
  group: string
  /** check-states: the size checked; flows: the size the flows ran at. */
  size: string
  /** State numbers covered by this test. */
  states: number[]
  /** Pages rendered and checked (check-states: states × wrap modes). */
  renders: number
  failures: Finding[]
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

/** One summary line, e.g. `check-states: 0 failures · 55 states × 3 sizes × 2 browsers, 660 renders`. */
export function summarize(kind: Kind, results: Result[]): string {
  const failures = results.reduce((n, r) => n + r.failures.length, 0)
  const states = new Set(results.flatMap((r) => r.states))
  const sizes = new Set(results.map((r) => r.size))
  const browsers = new Set(results.map((r) => r.browser))
  const groups = [...new Set(results.map((r) => r.group))].sort()
  const head = `${kind}: ${plural(failures, 'failure', 'failures')}`
  if (kind === 'flows') return `${head} · ${plural(groups.length, 'group', 'groups')} (${groups.join(', ')}), ${plural(states.size, 'state', 'states')}`
  const renders = results.reduce((n, r) => n + r.renders, 0)
  return `${head} · ${plural(states.size, 'state', 'states')} × ${plural(sizes.size, 'size', 'sizes')} × ${plural(browsers.size, 'browser', 'browsers')}, ${renders} renders`
}

export function writeResult(dir: string, kind: Kind, name: string, result: Result): void {
  mkdirSync(join(dir, kind), { recursive: true })
  writeFileSync(join(dir, kind, `${name}.json`), JSON.stringify(result, null, 2))
}

export function readResults(dir: string, kind: Kind): Result[] {
  let names: string[]
  try {
    names = readdirSync(join(dir, kind)).filter((f) => f.endsWith('.json'))
  } catch {
    return []
  }
  return names.map((f) => JSON.parse(readFileSync(join(dir, kind, f), 'utf8')) as Result)
}
