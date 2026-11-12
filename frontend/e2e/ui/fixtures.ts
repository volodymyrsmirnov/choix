// Shared fixtures of the UI end-to-end specs (the `ui` project of
// e2e/playwright.config.ts). The page is the real app: the frontend
// embedded in the server build (frontend/dist, built by `task frontend:dist`
// before global-setup.ts builds the binary), served without
// CHOIX_ASSETS_DIR.
//
// Every test gets its own server: the binary global-setup.ts built, started
// on port 8091 with a fresh data directory (visual grouping off, so the
// clusters the specs walk are the time buckets) and a fresh E2E root holding the folders the
// test generates with genfolder (invented EXIF values). The `ui` project
// shares port 8091 with the api project's quit spec; the run has a single
// worker, so the two never run at the same time, and every server started
// here is stopped when its test ends.
//
// The test-support binding (internal/app/testsupport, server builds only)
// has no generated module and the app page has no harness object, so its
// methods are called through the server's runtime endpoint directly
// (POST /wails/runtime, the request @wailsio/runtime's Call.ByName makes),
// from the test runner — never by importing a second copy of the runtime
// into the app page, which would replace the page's event dispatcher.
//
// Automatic checks after every test: the page logged no console error (none
// is exempt) and no console warning (except Wails' own browser-environment
// warning), it threw no uncaught error, every HTTP request and WebSocket it
// opened went to 127.0.0.1 on the server's port.
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { test as base, expect, type APIRequestContext, type Page } from '@playwright/test'
import { runDirs } from '../global-setup'

/** The port of the UI specs' servers (the api project's quit spec uses it too, never at the same time). */
export const UI_PORT = 8091
export const UI_BASE = `http://127.0.0.1:${UI_PORT}`

/** The directories of this run (see global-setup.ts). */
export const dirs = runDirs()

/** Visual grouping off: the clusters the specs walk are the time buckets. */
export const CONFIG_TOML = 'visual_grouping = false\n'

/** How long a spec waits for a folder's scan, by default. */
export const SCAN_MS = 60_000

/** The fully qualified name of the test-support binding's methods. */
const TEST_SUPPORT = 'github.com/volodymyrsmirnov/choix/internal/app/testsupport.Service'

/** The fully qualified name of the System binding's methods. */
const SYSTEM = 'github.com/volodymyrsmirnov/choix/internal/app/system.Service'

/** @wailsio/runtime's object ids (runtime.js objectNames) and the one method of each used here. */
const OBJECT_CALL = 0
const CALL_BINDING = 0
const OBJECT_EVENTS = 3
const EVENTS_EMIT = 0

/** The client id the runtime requests from the test runner carry (any id will do). */
const CLIENT_ID = `e2e-ui-${randomUUID()}`

/**
 * The warning Wails' runtime logs (console.warn, system.js) when the page
 * runs in a browser instead of the desktop webview: the one console message
 * the checks ignore, and only as a warning.
 */
const BROWSER_ENVIRONMENT = /Browser Environment Detected[\s\S]*Only UI previews are available in the browser/

export interface AddResult {
  id: string
  name: string
  existing: boolean
  relation?: string
}

/** Server is one run of the server binary for one test. */
export class Server {
  readonly proc: ChildProcess
  readonly exited: Promise<{ code: number | null; signal: NodeJS.Signals | null }>
  private log = ''

  private constructor(
    /** CHOIX_DATA_DIR: fresh for this server. */
    readonly data: string,
    /** CHOIX_E2E_ROOT: the folders this test generates live here. */
    readonly root: string,
  ) {
    const env: NodeJS.ProcessEnv = { ...process.env }
    // The embedded app, not the harness page.
    delete env.CHOIX_ASSETS_DIR
    delete env.WAILS_SERVER_HOST
    env.CHOIX_DATA_DIR = data
    env.CHOIX_E2E_ROOT = root
    env.WAILS_SERVER_PORT = String(UI_PORT)
    this.proc = spawn(dirs.bin, [], { env, stdio: ['ignore', 'pipe', 'pipe'] })
    this.proc.stdout?.on('data', (d: Buffer) => (this.log += d.toString()))
    this.proc.stderr?.on('data', (d: Buffer) => (this.log += d.toString()))
    this.exited = new Promise((resolve) => this.proc.once('exit', (code, signal) => resolve({ code, signal })))
  }

  /**
   * start makes a fresh data directory (config.toml with visual grouping off)
   * and E2E root under the run's temporary directory, starts the server on
   * UI_PORT and waits until it serves the embedded app.
   */
  static async start(): Promise<Server> {
    if (await listening()) {
      throw new Error(`port ${UI_PORT} is already in use; the UI specs need it (is another E2E run or a stray server running?)`)
    }
    const dir = mkdtempSync(join(dirs.tmp, 'ui-'))
    const data = join(dir, 'data')
    const root = join(dir, 'root')
    mkdirSync(data)
    mkdirSync(root)
    writeFileSync(join(data, 'config.toml'), CONFIG_TOML)
    const s = new Server(data, root)
    try {
      await s.ready()
    } catch (e) {
      await s.stop()
      throw e
    }
    return s
  }

  get running(): boolean {
    return this.proc.exitCode === null && this.proc.signalCode === null
  }

  /** ready waits for /health, then checks that / is the app and not the stub page. */
  private async ready(timeoutMs = 60_000): Promise<void> {
    const deadline = Date.now() + timeoutMs
    for (;;) {
      if (!this.running) {
        throw new Error(`the server exited before it was ready:\n${this.log}`)
      }
      if (await listening()) {
        break
      }
      if (Date.now() > deadline) {
        throw new Error(`the server was not ready within ${timeoutMs} ms:\n${this.log}`)
      }
      await new Promise((r) => setTimeout(r, 100))
    }
    const index = await (await fetch(`${UI_BASE}/`)).text()
    if (!index.includes('id="root"')) {
      throw new Error(
        'the server build embeds the frontend/dist stub page, not the app: build the app first (`task frontend:dist`, which `task test:e2e` runs)',
      )
    }
  }

  /** stop sends SIGTERM and waits up to 10 s, then SIGKILL. */
  async stop(): Promise<void> {
    if (!this.running) {
      return
    }
    this.proc.kill('SIGTERM')
    const timer = new Promise<'timeout'>((r) => setTimeout(() => r('timeout'), 10_000))
    if ((await Promise.race([this.exited, timer])) === 'timeout' && this.running) {
      this.proc.kill('SIGKILL')
      await this.exited
    }
  }

  output(): string {
    return this.log
  }

  /** generate writes a genfolder folder `name` (24 photos, 2 cameras, 3 bursts by default) under the root. */
  generate(name: string, n = 24): string {
    const out = join(this.root, name)
    execFileSync(dirs.genfolder, ['-out', out, '-n', String(n), '-cameras', '2', '-bursts', '3'], { stdio: 'pipe' })
    return out
  }
}

async function listening(): Promise<boolean> {
  try {
    return (await fetch(`${UI_BASE}/health`)).ok
  } catch {
    return false
  }
}

/**
 * Runtime calls the server's runtime endpoint the way @wailsio/runtime does
 * (runtime.js runtimeCallWithID; calls.js Call for bindings), from the test
 * runner.
 */
export class Runtime {
  constructor(private readonly request: APIRequestContext) {}

  private async post(object: number, method: number, args: unknown): Promise<unknown> {
    const res = await this.request.post(`${UI_BASE}/wails/runtime`, {
      headers: { 'Content-Type': 'application/json', 'x-wails-client-id': CLIENT_ID },
      data: JSON.stringify({ object, method, args }),
      // WaitIdle answers within 25 s; the server's write timeout is 30 s.
      timeout: 35_000,
    })
    const text = await res.text()
    if (!res.ok()) {
      throw new Error(`runtime call ${object}.${method} failed with ${res.status()}: ${text}`)
    }
    const type = res.headers()['content-type'] ?? ''
    return type.includes('application/json') ? (JSON.parse(text) as unknown) : text
  }

  /** testSupport calls TestSupport.<method>(...args) by name, as Call.ByName would. */
  testSupport<T>(method: string, ...args: unknown[]): Promise<T> {
    return this.post(OBJECT_CALL, CALL_BINDING, {
      'call-id': randomUUID(),
      methodName: `${TEST_SUPPORT}.${method}`,
      args,
    }) as Promise<T>
  }

  /** systemInfo calls System.Info() by name, as the app's binding does. */
  systemInfo(): Promise<{ windowButtonsRight: number; windowFullScreen: boolean }> {
    return this.post(OBJECT_CALL, CALL_BINDING, {
      'call-id': randomUUID(),
      methodName: `${SYSTEM}.Info`,
      args: [],
    }) as Promise<{ windowButtonsRight: number; windowFullScreen: boolean }>
  }

  /** emit sends an event through the runtime's Events.Emit; the server broadcasts it to every page. */
  async emit(name: string, data: unknown = null): Promise<void> {
    await this.post(OBJECT_EVENTS, EVENTS_EMIT, { name, data })
  }

  addFolderPath(path: string): Promise<AddResult> {
    return this.testSupport<AddResult>('AddFolderPath', path)
  }

  async setPickerPaths(paths: string[]): Promise<void> {
    await this.testSupport('SetPickerPaths', paths)
  }

  async setConfirmAnswer(yes: boolean): Promise<void> {
    await this.testSupport('SetConfirmAnswer', yes)
  }

  async emitMenuAction(id: string, data = ''): Promise<void> {
    await this.testSupport('EmitMenuAction', id, data)
  }

  /** waitIdle loops on WaitIdle (capped at 25 s per call) until the queue is idle or timeoutMs passed. */
  async waitIdle(timeoutMs = SCAN_MS): Promise<boolean> {
    const deadline = Date.now() + timeoutMs
    for (;;) {
      const left = deadline - Date.now()
      if (left <= 0) {
        return false
      }
      if (await this.testSupport<boolean>('WaitIdle', Math.min(left, 25_000))) {
        return true
      }
    }
  }
}

/**
 * awayFrom returns the http(s) and ws(s) URLs that do not go to 127.0.0.1 on
 * `port` (other schemes — data:, blob:, about: — never leave the page).
 */
export function awayFrom(urls: string[], port: number): string[] {
  return urls.filter((u) => {
    const url = new URL(u)
    if (!['http:', 'https:', 'ws:', 'wss:'].includes(url.protocol)) return false
    const defaultPort = url.protocol === 'https:' || url.protocol === 'wss:' ? '443' : '80'
    return url.hostname !== '127.0.0.1' || (url.port || defaultPort) !== String(port)
  })
}

/** What a UI spec gets: the app page, its server and the runtime endpoint. */
export interface AppPage {
  page: Page
  server: Server
  runtime: Runtime
  /**
   * Console errors matching `pattern` are expected by this test (a spec
   * that makes a screen fail on purpose): the after-test check leaves them
   * out. Uncaught errors (pageerror) are never exempt.
   */
  expectConsoleErrors(pattern: RegExp): void
}

/**
 * openApp loads the app from the server and waits until its event socket is
 * connected (the "[Wails] Event WebSocket connected" line of the runtime's
 * /wails/custom.js), so no event of a later step is missed.
 */
async function openApp(page: Page): Promise<void> {
  const connected = page.waitForEvent('console', {
    predicate: (m) => m.text().includes('Event WebSocket connected'),
    timeout: 20_000,
  })
  await page.goto(`${UI_BASE}/`)
  await connected
}

export const test = base.extend<{ app: AppPage }>({
  app: async ({ page, request }, use, testInfo) => {
    const server = await Server.start()
    const problems: string[] = []
    const expected: RegExp[] = []
    const requests: string[] = []
    page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`))
    page.on('console', (m) => {
      const type = m.type()
      if (type === 'error' && expected.some((p) => p.test(m.text()))) return
      if (type === 'error' || (type === 'warning' && !BROWSER_ENVIRONMENT.test(m.text()))) {
        problems.push(`console.${type}: ${m.text()}`)
      }
    })
    // HTTP requests, and WebSockets, which the request event does not report.
    page.on('request', (r) => requests.push(r.url()))
    page.on('websocket', (ws) => requests.push(ws.url()))
    let checked = false
    try {
      await openApp(page)
      await use({ page, server, runtime: new Runtime(request), expectConsoleErrors: (p) => expected.push(p) })
      // Checked before the server stops: its socket closing is not the app's doing.
      expect(problems, 'console errors, warnings and uncaught errors on the page').toEqual([])
      expect(
        requests.some((u) => u.startsWith(`ws://127.0.0.1:${UI_PORT}/`)),
        "the app's event WebSocket is among the connections checked",
      ).toBe(true)
      expect(awayFrom(requests, UI_PORT), `requests and WebSockets not to 127.0.0.1:${UI_PORT}`).toEqual([])
      checked = true
    } finally {
      // Closing the browser context closes its sockets too. Chromium keeps a
      // pre-connected socket that never sends a request, and Go's
      // http.Server.Shutdown waits up to 5 s for such a connection before it
      // counts it as idle (the server build would take ~5 s longer to quit).
      await page.context().close().catch(() => {})
      await server.stop()
      if (testInfo.status !== testInfo.expectedStatus || !checked) {
        await testInfo.attach('server.log', { body: server.output(), contentType: 'text/plain' })
      }
    }
    // A clean quit (SIGTERM) exits 0; not checked when the test already failed.
    expect(await server.exited, 'the server quit cleanly').toEqual({ code: 0, signal: null })
  },
})

export { expect }
