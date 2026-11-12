// Global setup of the binding-level E2E run (playwright.config.ts).
//
// runDirs() makes the run's temporary directory once per run — the runner
// process creates it while loading the config and passes it to the workers
// as CHOIX_E2E_TMP — with two data directories whose config.toml turns both
// model settings off, so no model download is ever attempted. It runs when
// the config loads, not in the global setup proper, because Playwright
// starts the webServer before globalSetup and the server reads config.toml
// at start.
//
// The default export then builds genfolder and the server binary the quit
// spec and the ui specs run themselves (`go build -tags server,production`,
// which embeds frontend/dist: the ui specs need the app built there first),
// and generates the api specs' photo folders. Its teardown removes the temporary directory
// (kept when CHOIX_E2E_KEEP=1).
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = (path: string) => fileURLToPath(new URL(path, import.meta.url))

/** The repository root (go commands run there). */
export const repoRoot = here('../..')

/** The config.toml of every data directory: both model settings off. */
const CONFIG_TOML = 'visual_grouping = false\nquality_rating = false\n'

export interface RunDirs {
  /** The run's temporary directory; everything below lives in it. */
  tmp: string
  /** CHOIX_DATA_DIR and CHOIX_E2E_ROOT of the shared server (port 8090). */
  data: string
  root: string
  /** The quit spec's own data directory and root (port 8091). */
  quitData: string
  quitRoot: string
  /** The server binary the quit and ui specs start, and genfolder. */
  bin: string
  genfolder: string
  /** The built harness page (CHOIX_ASSETS_DIR). */
  harnessDist: string
}

/**
 * runDirs returns the run's directories, creating the temporary directory
 * (and setting CHOIX_E2E_TMP) on the first call of the run and the data
 * directories' config.toml when missing.
 */
export function runDirs(): RunDirs {
  let tmp = process.env.CHOIX_E2E_TMP
  if (!tmp) {
    // realpath: on macOS the temporary directory is behind /var → /private/var,
    // and the server reports resolved paths.
    tmp = realpathSync(mkdtempSync(join(tmpdir(), 'choix-e2e-')))
    process.env.CHOIX_E2E_TMP = tmp
  }
  const dirs: RunDirs = {
    tmp,
    data: join(tmp, 'data'),
    root: join(tmp, 'root'),
    quitData: join(tmp, 'quit-data'),
    quitRoot: join(tmp, 'quit-root'),
    bin: join(tmp, 'choix-e2e'),
    genfolder: join(tmp, 'genfolder'),
    harnessDist: here('./harness/dist'),
  }
  for (const data of [dirs.data, dirs.quitData]) {
    mkdirSync(data, { recursive: true })
    const config = join(data, 'config.toml')
    if (!existsSync(config)) {
      writeFileSync(config, CONFIG_TOML)
    }
  }
  mkdirSync(dirs.root, { recursive: true })
  mkdirSync(dirs.quitRoot, { recursive: true })
  return dirs
}

/** The folders the specs use: name → photo count, under each root. */
const FOLDERS = ['library', 'catalog', 'events', 'gone', 'media', 'remove']
const QUIT_FOLDERS: Record<string, number> = { small: 24, big: 300 }

function run(cmd: string, args: string[]): void {
  execFileSync(cmd, args, { cwd: repoRoot, stdio: 'inherit' })
}

export default function globalSetup(): () => void {
  const dirs = runDirs()
  run('go', ['build', '-o', dirs.genfolder, './internal/e2e/genfolder'])
  run('go', ['build', '-tags', 'server,production', '-o', dirs.bin, './cmd/choix'])

  const generate = (out: string, n: number) => {
    if (!existsSync(out)) {
      run(dirs.genfolder, ['-out', out, '-n', String(n), '-cameras', '2', '-bursts', '3'])
    }
  }
  for (const name of FOLDERS) {
    generate(join(dirs.root, name), 24)
  }
  for (const [name, n] of Object.entries(QUIT_FOLDERS)) {
    generate(join(dirs.quitRoot, name), n)
  }

  return () => {
    if (process.env.CHOIX_E2E_KEEP === '1') {
      console.log(`choix e2e: kept ${dirs.tmp}`)
      return
    }
    rmSync(dirs.tmp, { recursive: true, force: true })
  }
}
