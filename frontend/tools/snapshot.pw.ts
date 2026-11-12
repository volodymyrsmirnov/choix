// The `snapshot` project: renders every selected prototype state at every
// review size, each in a fresh context and page (stateRunner's design
// context: scale 1, UTC, en-US, reduced motion, fixed clock; URL
// `?bare&freeze`), extracts and hashes its tree (extract.ts) and writes one
// JSON fragment per state into CHOIX_SNAPSHOT_DIR. tools/snapshot.mjs merges
// the fragments; run it rather than this project.
//
// The states are spread over SHARDS tests by their position in the
// registry, so the work runs in parallel and no fragment depends on which
// worker rendered it or what else that worker rendered.
//
//   CHOIX_SNAPSHOT_DIR     where the fragments go (required)
//   CHOIX_SNAPSHOT_STATES  comma-separated numbers to render (default: all)
//   CHOIX_SNAPSHOT_TREES   when set, also writes each variant's tree there
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { test } from '@playwright/test'
import { MEASURED } from '../src/design/measured'
import { PROTO_MEASURED } from '../prototype/measured'
import { FRONTEND, OSES, protoUrl, SIZES, THEMES } from './designPaths'
import { extractPage, hashTree, measuredDigests } from './extract'
import { listStates, newDesignContext, openState, waitForHarness } from './stateRunner'

const SHARDS = 36

/** Every variant under review, sizes outermost: `<size>/<theme>/<os>` (gate.json). */
const VARIANTS = SIZES.flatMap((size) => THEMES.flatMap((theme) => OSES.map((os) => ({ size, theme, os, key: `${size}/${theme}/${os}` }))))

const outDir = () => {
  const dir = process.env.CHOIX_SNAPSHOT_DIR
  if (!dir) throw new Error('CHOIX_SNAPSHOT_DIR is not set: run tools/snapshot.mjs')
  return dir
}

const selected = (): Set<number> | null => {
  const raw = process.env.CHOIX_SNAPSHOT_STATES
  return raw ? new Set(raw.split(',').map(Number)) : null
}

let measuredCache: Record<string, string> | null = null
/** `measured:<id>` → `<id>:<sha256>`, read once per worker. */
const measured = () => (measuredCache ??= measuredDigests(FRONTEND, [MEASURED, PROTO_MEASURED]))

// One build is served for the whole run, so a URL's bytes never change.
const resourceCache = new Map<string, string>()

test.describe.configure({ mode: 'parallel' })

for (let shard = 0; shard < SHARDS; shard++) {
  test(`snapshot shard ${shard}`, async ({ browser, browserName }) => {
    test.setTimeout(10 * 60_000)
    const dir = outDir()
    mkdirSync(dir, { recursive: true })
    const trees = process.env.CHOIX_SNAPSHOT_TREES
    if (trees) mkdirSync(trees, { recursive: true })
    const want = selected()

    const listing = await newDesignContext(browser, 'default')
    const all = await listStates(listing, protoUrl()).finally(() => listing.close())
    if (shard === 0) writeFileSync(join(dir, 'registered.json'), JSON.stringify(all.map((s) => ({ n: s.n, label: s.label }))))
    const mine = all.filter((_, i) => i % SHARDS === shard).filter((s) => !want || want.has(s.n))

    const browserId = `${browserName} ${browser.version()}`
    for (const state of mine) {
      const variants: Record<string, string> = {}
      const ms: Record<string, number> = {}
      for (const { size, theme, os, key } of VARIANTS) {
        const context = await newDesignContext(browser, size)
        try {
          const page = await context.newPage()
          await page.goto(`${protoUrl()}?bare&freeze&size=${size}&theme=${theme}&os=${os}#s${state.n}`)
          await waitForHarness(page)
          await openState(page, state.n)
          const started = performance.now()
          const tree = await extractPage(page, { measured: measured(), cache: resourceCache })
          ms[key] = Math.round(performance.now() - started)
          variants[key] = hashTree(tree)
          if (trees) writeFileSync(join(trees, `s${state.n}-${key.replaceAll('/', '-')}.json`), JSON.stringify(tree, null, 1))
        } catch (e) {
          throw new Error(`s${state.n} at ${key}: ${(e as Error).message}`)
        } finally {
          await context.close()
        }
      }
      writeFileSync(join(dir, `s${state.n}.json`), JSON.stringify({ n: state.n, label: state.label, browser: browserId, variants, ms }))
    }
  })
}
