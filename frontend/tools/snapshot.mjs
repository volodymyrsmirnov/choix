// The state fingerprints: docs/design/state-hashes.json.
//
//   node tools/snapshot.mjs [--check] [--states 100-122,300] [--workers N] [--trees DIR]
//
//   1-3. tools/build-prototype.mjs: the source digest, the prototype build,
//      dist-prototype/build.json with that digest (serve.mjs answers
//      /__build.json from it), the digest again (a source that moved during
//      the build aborts);
//   4. the `snapshot` Playwright project, whose global setup serves the build
//      on an ephemeral port; it writes one fragment per state;
//   5. the fragments merged, sorted, into the file — or, with --check,
//      compared with it: nothing is written, and each differing state is
//      listed (exit 1).
//
// --states renders only those states. In write mode it updates their entries
// and keeps the others, which needs the file's sourceDigest, browser and
// extractor to equal the current ones (otherwise run without --states). With
// --check it compares only those states. --trees writes each variant's tree
// to DIR (for comparing two machines). Exit 2 is a usage or harness error.
//
// File format: { version: 1, sourceDigest, browser, extractor (first 16 hex
// of sha256(extract.ts)), states: { "<n>": { label, variants: {
// "min/dark/macos", "default/dark/macos", "large/dark/macos" }, hash } } },
// where hash = sha256(the three variant hashes in that order + browser +
// extractor), concatenated without separators. Sorted keys, two-space
// indent, trailing newline.
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { BuildError, buildPrototype, repoRoot as findRoot } from './build-prototype.mjs'

const FRONTEND = join(dirname(fileURLToPath(import.meta.url)), '..')
const GATE = JSON.parse(readFileSync(join(FRONTEND, '..', 'docs', 'design', 'gate.json'), 'utf8'))
/** Review sizes in gate.json order. */
const SIZES = Object.keys(GATE.sizes)
/** `<size>/<theme>/<os>` for every size × theme × os of gate.json, sizes outermost: the order of the state hash. */
export const VARIANTS = SIZES.flatMap((size) => GATE.themes.flatMap((theme) => GATE.os.map((os) => `${size}/${theme}/${os}`)))

const sha256 = (s) => createHash('sha256').update(s).digest('hex')

function fail(message, code = 2) {
  console.error(`snapshot: ${message}`)
  process.exit(code)
}

function repoRoot() {
  try {
    return findRoot()
  } catch (e) {
    fail(e.message)
  }
}

/** `100-122,300` → [100, …, 122, 300]. */
export function parseStates(spec) {
  const out = new Set()
  for (const part of spec.split(',').map((p) => p.trim()).filter(Boolean)) {
    const m = /^(\d+)(?:-(\d+))?$/.exec(part)
    if (!m) throw new Error(`bad --states entry "${part}"`)
    const a = Number(m[1])
    const b = m[2] === undefined ? a : Number(m[2])
    if (b < a) throw new Error(`bad --states range "${part}"`)
    for (let n = a; n <= b; n++) out.add(n)
  }
  return [...out].sort((x, y) => x - y)
}

function parseArgs(argv) {
  const o = { check: false, states: null, workers: null, trees: null }
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    if (a === '--check') o.check = true
    else if (a === '--states') o.states = argv[++i]
    else if (a === '--workers') o.workers = argv[++i]
    else if (a === '--trees') o.trees = argv[++i]
    else if (a === '--') continue
    else fail(`unknown argument ${a}\nusage: node tools/snapshot.mjs [--check] [--states 100-122,300] [--workers N] [--trees DIR]`)
  }
  if (o.states !== null) {
    try {
      o.states = parseStates(o.states)
    } catch (e) {
      fail(e.message)
    }
    if (o.states.length === 0) fail('--states selects no state')
  }
  if (o.workers !== null && !/^\d+%?$/.test(o.workers)) fail(`bad --workers ${o.workers}`)
  return o
}

export const stateHash = (variants, browser, extractor) => sha256(VARIANTS.map((v) => variants[v]).join('') + browser + extractor)

/** JSON with sorted keys (code-unit order, so "100" before "11"), two-space indent. */
function formatJson(v, indent) {
  if (v === null || typeof v !== 'object') return JSON.stringify(v)
  const inner = `${indent}  `
  if (Array.isArray(v)) return v.length ? `[\n${v.map((x) => inner + formatJson(x, inner)).join(',\n')}\n${indent}]` : '[]'
  const keys = Object.keys(v).sort()
  return keys.length ? `{\n${keys.map((k) => `${inner}${JSON.stringify(k)}: ${formatJson(v[k], inner)}`).join(',\n')}\n${indent}}` : '{}'
}

/** The file's text: sorted keys, two-space indent, trailing newline. */
export const formatFile = (file) => `${formatJson(file, '')}\n`

function duration(ms) {
  const s = Math.round(ms / 1000)
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`
}

function main() {
  const started = Date.now()
  const o = parseArgs(process.argv.slice(2))
  const root = repoRoot()
  const outFile = join(root, 'docs', 'design', 'state-hashes.json')
  const extractor = sha256(readFileSync(join(FRONTEND, 'tools', 'extract.ts'))).slice(0, 16)

  let before
  try {
    before = buildPrototype()
  } catch (e) {
    fail(e instanceof BuildError ? e.message : String(e))
  }

  const dir = join(FRONTEND, '.design', 'snapshot', `${new Date(started).toISOString().replace(/[:.]/g, '-')}-${process.pid}`)
  mkdirSync(dir, { recursive: true })
  const cli = join(FRONTEND, 'node_modules', '@playwright', 'test', 'cli.js')
  const args = [cli, 'test', '-c', 'playwright.design.config.ts', '--project', 'snapshot', '--reporter', 'dot']
  if (o.workers) args.push('--workers', o.workers)
  const env = { ...process.env, DESIGN_NO_BUILD: '1', CHOIX_SNAPSHOT_DIR: dir }
  if (o.states) env.CHOIX_SNAPSHOT_STATES = o.states.join(',')
  else delete env.CHOIX_SNAPSHOT_STATES
  if (o.trees) env.CHOIX_SNAPSHOT_TREES = resolve(o.trees)
  else delete env.CHOIX_SNAPSHOT_TREES
  const pw = spawnSync(process.execPath, args, { cwd: FRONTEND, env, stdio: 'inherit' })
  if (pw.status !== 0) fail(`the snapshot run failed (exit ${pw.status ?? pw.signal}); fragments are in ${dir}`)

  // Merge.
  const registeredFile = join(dir, 'registered.json')
  if (!existsSync(registeredFile)) fail('the snapshot run listed no states')
  const registered = JSON.parse(readFileSync(registeredFile, 'utf8'))
  const known = new Set(registered.map((s) => s.n))
  const wanted = o.states ?? registered.map((s) => s.n)
  const unknown = wanted.filter((n) => !known.has(n))
  if (unknown.length > 0 && !o.check) fail(`not registered: ${unknown.map((n) => `s${n}`).join(', ')}`)

  const fragments = new Map()
  for (const f of readdirSync(dir).filter((f) => /^s\d+\.json$/.test(f))) {
    const frag = JSON.parse(readFileSync(join(dir, f), 'utf8'))
    fragments.set(frag.n, frag)
  }
  const browsers = new Set([...fragments.values()].map((f) => f.browser))
  if (browsers.size > 1) fail(`fragments from more than one browser: ${[...browsers].join(', ')}`)
  const browser = [...browsers][0] ?? ''
  const fresh = {}
  for (const n of wanted.filter((n) => known.has(n))) {
    const frag = fragments.get(n)
    if (!frag) fail(`no fragment for s${n}`)
    for (const v of VARIANTS) if (typeof frag.variants[v] !== 'string') fail(`s${n} has no ${v} variant`)
    const variants = Object.fromEntries(VARIANTS.map((v) => [v, frag.variants[v]]))
    fresh[n] = { label: frag.label, variants, hash: stateHash(variants, browser, extractor) }
  }

  const times = [...fragments.values()].flatMap((f) => Object.values(f.ms ?? {})).sort((a, b) => a - b)
  if (times.length > 0) {
    const pct = (p) => times[Math.min(times.length - 1, Math.floor((times.length * p) / 100))]
    console.log(`extract per variant: median ${pct(50)} ms, p95 ${pct(95)} ms, max ${times[times.length - 1]} ms (${times.length} variants)`)
  }
  rmSync(dir, { recursive: true, force: true })

  const current = existsSync(outFile) ? JSON.parse(readFileSync(outFile, 'utf8')) : null
  const header = { version: 1, sourceDigest: before, browser, extractor }

  if (o.check) {
    const diffs = []
    if (!current) {
      console.error('snapshot --check: docs/design/state-hashes.json does not exist')
      process.exit(1)
    }
    for (const k of ['version', 'sourceDigest', 'browser', 'extractor']) {
      if (current[k] !== header[k]) diffs.push(`${k}: file has ${JSON.stringify(current[k])}, this run ${JSON.stringify(header[k])}`)
    }
    const numbers = o.states ?? [...new Set([...Object.keys(current.states ?? {}).map(Number), ...Object.keys(fresh).map(Number)])].sort((a, b) => a - b)
    for (const n of numbers) {
      const was = current.states?.[n]
      const now = fresh[n]
      if (!now) diffs.push(`s${n}: ${known.has(n) ? 'not rendered' : 'not registered'}`)
      else if (!was) diffs.push(`s${n} ${now.label}: not in the file`)
      else if (was.hash !== now.hash || was.label !== now.label) {
        const which = VARIANTS.filter((v) => was.variants?.[v] !== now.variants[v])
        const parts = []
        if (which.length) parts.push(`variants differ: ${which.join(', ')}`)
        if (was.label !== now.label) parts.push(`label "${was.label}" → "${now.label}"`)
        if (!which.length && was.label === now.label) parts.push('hash differs (browser or extractor)')
        diffs.push(`s${n} ${now.label}: ${parts.join('; ')}`)
      }
    }
    console.log(`snapshot --check: ${Object.keys(fresh).length} states × ${VARIANTS.length} variants compared in ${duration(Date.now() - started)}`)
    if (diffs.length > 0) {
      console.error(`snapshot --check: ${diffs.length} difference${diffs.length === 1 ? '' : 's'} from docs/design/state-hashes.json:`)
      for (const d of diffs) console.error(`  ${d}`)
      process.exit(1)
    }
    console.log('snapshot --check: docs/design/state-hashes.json is current')
    return
  }

  let states = fresh
  if (o.states) {
    if (!current) fail('--states updates an existing file; run without --states first')
    for (const k of ['sourceDigest', 'browser', 'extractor']) {
      if (current[k] !== header[k]) fail(`--states cannot update a file whose ${k} differs from this run's; run without --states`)
    }
    states = { ...current.states, ...fresh }
  }
  writeFileSync(outFile, formatFile({ ...header, states }))
  console.log(`snapshot: ${Object.keys(fresh).length} states × ${VARIANTS.length} variants → docs/design/state-hashes.json in ${duration(Date.now() - started)}`)
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main()
