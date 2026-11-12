// Builds the prototype and stamps it with its source digest.
//
//   node tools/build-prototype.mjs        (bun run proto:build, task design:build)
//
//   1. the source digest (python3 tools/design/gate.py digest);
//   2. vite build --config vite.prototype.config.ts → dist-prototype/;
//   3. dist-prototype/build.json = { "sourceDigest": <digest> }, which
//      serve.mjs answers at /__build.json (the owner's `approve` checks it);
//   4. the digest again: if the sources moved during the build, the stamp
//      would lie, so the build fails (exit 1) and build.json is removed;
//      so it is if this second digest itself fails (an unverified stamp).
//
// tools/snapshot.mjs builds through buildPrototype(), so every build, from
// either path, reports the digest of the sources it was made from.
import { spawnSync } from 'node:child_process'
import { rmSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const FRONTEND = join(dirname(fileURLToPath(import.meta.url)), '..')
export const BUILD_JSON = join(FRONTEND, 'dist-prototype', 'build.json')

export class BuildError extends Error {}

function run(cmd, args, cwd) {
  const r = spawnSync(cmd, args, { cwd, encoding: 'utf8' })
  if (r.error) throw new BuildError(`${cmd} could not start: ${r.error.message}`)
  return r
}

export function repoRoot() {
  const r = run('git', ['rev-parse', '--show-toplevel'], FRONTEND)
  if (r.status !== 0) throw new BuildError(`not in a git repository: ${r.stderr.trim()}`)
  return r.stdout.trim()
}

/** The source digest (gate.py digest) of the repository at `root`. */
export function sourceDigest(root = repoRoot()) {
  const r = run('python3', [join(root, 'tools', 'design', 'gate.py'), 'digest'], root)
  if (r.status !== 0) throw new BuildError(`gate.py digest failed:\n${r.stderr}`)
  return r.stdout.trim()
}

/** vite build --config vite.prototype.config.ts → dist-prototype/. Throws BuildError. */
function viteBuild() {
  const vite = join(FRONTEND, 'node_modules', 'vite', 'bin', 'vite.js')
  const build = spawnSync(process.execPath, [vite, 'build', '--config', 'vite.prototype.config.ts'], { cwd: FRONTEND, encoding: 'utf8' })
  if (build.status !== 0) throw new BuildError(`the prototype build failed:\n${build.stdout ?? ''}${build.stderr ?? ''}`)
}

/**
 * Builds dist-prototype/ and stamps build.json; returns the digest. Throws
 * BuildError. The options replace the digest, the build and the stamp's
 * path (tests only).
 *
 * @param {{ digest?: () => string, build?: () => void, out?: string }} [options]
 * @returns {string}
 */
export function buildPrototype({ digest, build = viteBuild, out = BUILD_JSON } = {}) {
  let current = digest
  if (!current) {
    const root = repoRoot()
    current = () => sourceDigest(root)
  }
  const before = current()
  rmSync(out, { force: true })
  build()
  writeFileSync(out, `${JSON.stringify({ sourceDigest: before })}\n`)
  // Until the second digest agrees, the stamp is unverified: any failure removes it.
  let after
  try {
    after = current()
  } catch (e) {
    rmSync(out, { force: true })
    throw e
  }
  if (after !== before) {
    rmSync(out, { force: true })
    throw new BuildError('the sources changed during the build; run it again')
  }
  return before
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const digest = buildPrototype()
    console.log(`prototype built: dist-prototype/ (source digest ${digest.slice(0, 16)}…)`)
  } catch (e) {
    console.error(`build-prototype: ${e.message}`)
    process.exit(1)
  }
}
