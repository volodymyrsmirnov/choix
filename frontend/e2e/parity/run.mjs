// `task parity`: app ↔ prototype parity end to end (run from frontend/).
//
//   node e2e/parity/run.mjs [--app-port 8801] [--proto-port 8811] [-- <playwright args, e.g. --grep overlays>]
//
//  1. Refuses to start when anything already listens at either port, on
//     127.0.0.1 or [::1] (it never stops a server it did not start).
//  2. Builds the prototype (`bun run proto:build`, what `task design:build`
//     runs), the test-only fixture page the parity self-test opens
//     (FIXTURE_BUILD → .design/fixtures-dist/, what the `fixtures` setup
//     project builds) and the app's parity build (`bun run build:parity`),
//     one after the other.
//  3. Serves both on 127.0.0.1 — the prototype with tools/serve.mjs (what
//     `task design:serve` runs), the app with serve-app.mjs (what
//     `task parity:app` runs) — and waits until each says it is serving.
//     The servers are started here, not through `task`, so this process owns
//     their pids and can always stop them.
//  4. Runs `bunx playwright test -c playwright.design.config.ts --project
//     parity <args>` with PARITY_APP_URL set to the app and DESIGN_NO_BUILD=1
//     (everything was built in step 2; the prototype is not rebuilt while
//     served). DESIGN_NO_BUILD=1 also turns the `fixtures` setup project into
//     a no-op, which is why step 2 builds the fixture page: without it a
//     fresh checkout has no .design/fixtures-dist/ and every self-test times
//     out waiting for the page's harness.
//  5. Stops both servers whatever happened (also on Ctrl-C), and exits with
//     Playwright's status.
import { spawn, spawnSync } from 'node:child_process'
import { realpathSync } from 'node:fs'
import { connect } from 'node:net'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const FRONTEND = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const READY_MS = 30_000

/**
 * The fixture page's build: the command tools/fixtures/build.setup.ts runs
 * (run.test.ts keeps the two equal).
 */
export const FIXTURE_BUILD = ['bunx', ['vite', 'build', '--config', 'tools/fixtures/vite.config.ts', '--logLevel', 'error']]

/**
 * The options and the Playwright arguments; throws on a bad option.
 * @param {string[]} argv
 * @returns {{ appPort: number, protoPort: number, playwright: string[] }}
 */
export function parseArgs(argv) {
  /** @type {{ appPort: number, protoPort: number, playwright: string[] }} */
  const out = { appPort: 8801, protoPort: 8811, playwright: [] }
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    if (a === '--') {
      out.playwright = argv.slice(i + 1)
      break
    }
    if (a !== '--app-port' && a !== '--proto-port') throw new Error(`unknown option ${a} (Playwright arguments go after --)`)
    const port = Number(argv[++i])
    if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error(`invalid ${a} ${argv[i] ?? ''}`)
    out[a === '--app-port' ? 'appPort' : 'protoPort'] = port
  }
  if (out.appPort === out.protoPort) throw new Error(`the app and the prototype need two ports (both ${out.appPort})`)
  return out
}

/** Whether something accepts connections on `host`:`port`. */
function listening(host, port) {
  return new Promise((resolve) => {
    const s = connect(port, host)
    s.once('connect', () => {
      s.destroy()
      resolve(true)
    })
    s.once('error', () => resolve(false))
  })
}

/**
 * Whether something accepts connections on `port` at either loopback
 * address: a server on [::1] (Vite's dev server binds `localhost` there) also
 * answers http://localhost:<port>, so it counts as taken.
 */
export async function portBusy(port) {
  const [v4, v6] = await Promise.all([listening('127.0.0.1', port), listening('::1', port)])
  return v4 || v6
}

/** Starts a server; resolves once it prints `serving http://127.0.0.1:<port>`, rejects if it exits or stays silent. */
export function startServer(label, args, port) {
  const child = spawn(process.execPath, args, { cwd: FRONTEND, stdio: ['ignore', 'pipe', 'pipe'] })
  let output = ''
  const ready = new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${label}: not serving on 127.0.0.1:${port} after ${READY_MS / 1000} s\n${output}`)), READY_MS)
    const onData = (chunk) => {
      output += chunk
      if (output.includes(`serving http://127.0.0.1:${port}`)) {
        clearTimeout(timer)
        resolve()
      }
    }
    child.stdout.on('data', onData)
    child.stderr.on('data', (chunk) => (output += chunk))
    child.once('exit', (code, signal) => {
      clearTimeout(timer)
      reject(new Error(`${label} exited (${code ?? signal}) before serving:\n${output}`))
    })
  })
  // The caller awaits both servers together: the second one's failure
  // (often just being stopped) must not surface as an unhandled rejection.
  ready.catch(() => {})
  return { child, ready }
}

/** Stops a child: SIGTERM, then SIGKILL after 5 s; resolves once it has exited. */
export function stop(child) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve()
  return new Promise((resolve) => {
    const kill = setTimeout(() => child.kill('SIGKILL'), 5000)
    child.once('exit', () => {
      clearTimeout(kill)
      resolve()
    })
    child.kill('SIGTERM')
  })
}

function run(label, cmd, args) {
  console.log(`parity: ${label}`)
  const r = spawnSync(cmd, args, { cwd: FRONTEND, stdio: 'inherit' })
  if (r.status !== 0) throw new Error(`${label} failed (${r.error?.message ?? r.status ?? r.signal})`)
}

async function main() {
  let opts
  try {
    opts = parseArgs(process.argv.slice(2))
  } catch (e) {
    console.error(`parity: ${e.message}`)
    return 2
  }
  for (const [what, port] of [
    ['the prototype', opts.protoPort],
    ['the app', opts.appPort],
  ]) {
    if (await portBusy(port)) {
      console.error(
        `parity: something already listens on localhost:${port} (127.0.0.1 or [::1]; wanted for ${what}); stop it, or pick other ports: task parity APP_PORT=<n> PROTO_PORT=<n>`,
      )
      return 1
    }
  }

  // Every child this run started (servers, then Playwright): all are stopped
  // on the way out, and on a signal.
  const children = []
  const stopAll = () => Promise.all(children.map(stop))
  for (const signal of ['SIGINT', 'SIGTERM']) {
    process.on(signal, () => {
      void stopAll().then(() => process.exit(130))
    })
  }
  try {
    run('build the prototype (bun run proto:build)', 'bun', ['run', 'proto:build'])
    run('build the self-test fixture page (tools/fixtures)', ...FIXTURE_BUILD)
    run('build the app on the mock bindings (bun run build:parity)', 'bun', ['run', 'build:parity'])
    const proto = startServer('the prototype server', ['tools/serve.mjs', '--port', String(opts.protoPort)], opts.protoPort)
    children.push(proto.child)
    const app = startServer('the app server', ['e2e/parity/serve-app.mjs', '--port', String(opts.appPort)], opts.appPort)
    children.push(app.child)
    await Promise.all([proto.ready, app.ready])
    const appUrl = `http://127.0.0.1:${opts.appPort}`
    console.log(`parity: prototype on http://127.0.0.1:${opts.protoPort}, app on ${appUrl}`)
    const pw = spawn('bunx', ['playwright', 'test', '-c', 'playwright.design.config.ts', '--project', 'parity', ...opts.playwright], {
      cwd: FRONTEND,
      stdio: 'inherit',
      env: { ...process.env, PARITY_APP_URL: appUrl, DESIGN_NO_BUILD: '1' },
    })
    children.push(pw)
    return await new Promise((resolve) => {
      pw.once('error', (e) => {
        console.error(`parity: cannot run Playwright: ${e.message}`)
        resolve(1)
      })
      pw.once('exit', (code) => resolve(code ?? 1))
    })
  } catch (e) {
    console.error(`parity: ${e.message}`)
    return 1
  } finally {
    await stopAll()
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === realpathSync(process.argv[1])) {
  process.exit(await main())
}
