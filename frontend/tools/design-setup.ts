// Global setup of playwright.design.config.ts (runs on Node):
//   1. builds the prototype (`bun run proto:build` → dist-prototype/), unless
//      DESIGN_NO_BUILD=1 (reuse the last build while iterating); the
//      test-only fixture page is built by the `fixtures` setup project, which
//      only the self-tests depend on (tools/fixtures/build.setup.ts);
//   2. serves the prototype and .design/fixtures-dist/ on ephemeral
//      127.0.0.1 ports (never a fixed port) as CHOIX_PROTO_URL /
//      CHOIX_FIXTURES_URL; the fixture server reads from disk per request,
//      so it serves whatever the setup project builds;
//   3. gives the run its own result folder (CHOIX_RESULTS_DIR under
//      .design/results/), so concurrent runs never wipe each other's results;
//   4. returns the teardown, which closes the servers and prints one summary
//      line per kind of result this run wrote, plus the wall-clock.
import { spawnSync } from 'node:child_process'
import { mkdirSync, readdirSync, rmSync } from 'node:fs'
import type { Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { join } from 'node:path'
import { FIXTURES_DIST, FRONTEND, PROTO_DIST, RESULTS } from './designPaths'
import { readResults, summarize } from './results'
import { serve } from './serve.mjs'

/** Result folders kept from earlier runs (the newest ones). */
const KEEP_RUNS = 10

export function run(label: string, cmd: string, args: string[]): void {
  const r = spawnSync(cmd, args, { cwd: FRONTEND, encoding: 'utf8' })
  if (r.status !== 0) {
    throw new Error(`design setup: ${label} failed (exit ${r.status ?? r.signal}):\n${r.stdout ?? ''}${r.stderr ?? ''}`)
  }
}

const close = (s: Server) => new Promise<void>((resolve) => s.close(() => resolve()))
const url = (s: Server) => `http://127.0.0.1:${(s.address() as AddressInfo).port}/`

function duration(ms: number): string {
  const s = Math.round(ms / 1000)
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`
}

function pruneRuns(): void {
  let runs: string[]
  try {
    runs = readdirSync(RESULTS).sort()
  } catch {
    return
  }
  for (const old of runs.slice(0, Math.max(0, runs.length - KEEP_RUNS))) rmSync(join(RESULTS, old), { recursive: true, force: true })
}

export default async function setup(): Promise<() => Promise<void>> {
  const started = Date.now()
  if (!process.env.DESIGN_NO_BUILD) run('prototype build', 'bun', ['run', 'proto:build'])

  pruneRuns()
  const results = join(RESULTS, `${new Date(started).toISOString().replace(/[:.]/g, '-')}-${process.pid}`)
  mkdirSync(results, { recursive: true })
  process.env.CHOIX_RESULTS_DIR = results

  const proto: Server = await serve({ port: 0, root: PROTO_DIST })
  const fixtures: Server = await serve({ port: 0, root: FIXTURES_DIST })
  process.env.CHOIX_PROTO_URL = url(proto)
  process.env.CHOIX_FIXTURES_URL = url(fixtures)

  return async () => {
    await Promise.all([close(proto), close(fixtures)])
    const lines = (['check-states', 'flows'] as const)
      .map((kind) => ({ kind, results: readResults(results, kind) }))
      .filter((k) => k.results.length > 0)
      .map((k) => summarize(k.kind, k.results))
    for (const line of lines) console.log(line)
    console.log(`design run wall-clock: ${duration(Date.now() - started)}`)
  }
}
