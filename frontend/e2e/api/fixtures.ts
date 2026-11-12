// Shared fixtures of the binding-level end-to-end specs (*.e2e.ts). The
// specs drive the Go services of the server build (`go run -tags
// server,production ./cmd/choix`, started by playwright.config.ts) through
// the harness page (e2e/harness), which exposes the generated bindings as
// window.choix and records every app event in window.choix.events.
//
// Every call runs inside the page, so it goes through the real Wails
// runtime and transport. A failed call comes back as an Outcome with the
// error's name, message and cause — the app error {code, field?, reason?,
// message} that apperr.Marshal produced — instead of a rejected promise,
// whose cause Playwright would drop.
import http from 'node:http'
import { join } from 'node:path'
import { test as base, expect, type Page } from '@playwright/test'
import { runDirs } from '../global-setup'

/** The server Playwright starts (webServer in playwright.config.ts). */
export const PORT = 8090
export const BASE = `http://127.0.0.1:${PORT}`

/** The directories of this run (see global-setup.ts). */
export const dirs = runDirs()

/** folderPath(name) is the generated folder `name` under the E2E root. */
export function folderPath(name: string): string {
  return join(dirs.root, name)
}

/** How long a spec waits for the job queue to drain, by default. */
export const IDLE_MS = 60_000

export interface RecordedEvent {
  name: string
  data: any
  at: number
}

export interface AppErrorCause {
  code?: string
  field?: string
  reason?: string
  message?: string
}

export interface CallFailure {
  name: string
  message: string
  cause?: AppErrorCause
}

export type Outcome<T> = { ok: true; value: T } | ({ ok: false } & CallFailure)

/** The error call() throws for a rejected binding call. */
export class CallError extends Error {
  readonly failure: CallFailure
  constructor(failure: CallFailure) {
    super(`${failure.name}: ${failure.message}`, { cause: failure.cause })
    this.failure = failure
  }
}

export type ServiceName = 'Library' | 'Catalog' | 'Settings' | 'Models' | 'System' | 'TestSupport'

/** The shapes the specs read from the bindings (a subset of the models). */
export interface AddResult {
  id: string
  name: string
  existing: boolean
  relation?: string
}

export interface FolderSummary {
  id: string
  name: string
  parentPath: string
  state: string
  month: string | null
  photos: number
  videos: number
  clusters: number
  picked: number
  rejected: number
  previews: string[]
  scan?: { stage: string; done: number; total: number; failed: number } | null
  /** library.db's folders.added_at: unix seconds. */
  added_at: number
}

export interface Member {
  file_id: number
  path: string
  content_hash: string
  picked: boolean
  rejected: boolean
}

export interface FolderView {
  folder: string
  clusters: { id: number; device: string; members: Member[] }[]
  devices: string[]
  picks_dir: string
}

export interface PickResult {
  file_id: number
  state: string
  exported_path?: string
}

/** Harness drives one harness page served by the server at base. */
export class Harness {
  /** Every URL the page requested or opened a WebSocket to, in order. */
  readonly requests: string[] = []

  private constructor(
    readonly page: Page,
    readonly base: string,
  ) {
    // The request event does not report WebSockets.
    page.on('request', (r) => this.requests.push(r.url()))
    page.on('websocket', (ws) => this.requests.push(ws.url()))
  }

  /** The server's port (base's). */
  get port(): number {
    return Number(new URL(this.base).port)
  }

  /** open loads the harness page at base and waits for its event socket. */
  static async open(page: Page, base: string): Promise<Harness> {
    const h = new Harness(page, base)
    await h.load()
    return h
  }

  /**
   * load (re)loads the page and resolves once window.choix exists and the
   * runtime's event WebSocket (Wails' /wails/custom.js) is connected, so
   * no event of a later call is missed.
   */
  async load(): Promise<void> {
    const connected = this.page.waitForEvent('console', {
      predicate: (m) => m.text().includes('Event WebSocket connected'),
      timeout: 20_000,
    })
    const start = this.requests.length
    await this.page.goto(`${this.base}/`)
    await this.page.waitForFunction(() => window.choix !== undefined)
    await connected
    // The load itself (the page, its scripts, the event WebSocket), which
    // the per-test check (stayLocal) does not see.
    const loaded = this.requests.slice(start)
    expect(loaded.some((u) => u.startsWith(`ws://127.0.0.1:${this.port}/`)), 'the event WebSocket is among the URLs checked').toBe(true)
    expect(hostsAway(loaded, this.port), `requests and WebSockets of the page load not to 127.0.0.1:${this.port}`).toEqual([])
  }

  /** attempt calls Service.method(...args) in the page. */
  attempt<T>(service: ServiceName, method: string, ...args: unknown[]): Promise<Outcome<T>> {
    return this.page.evaluate(
      async ({ service, method, args }) => {
        const svc = window.choix[service] as unknown as Record<string, (...a: unknown[]) => Promise<unknown>>
        try {
          return { ok: true as const, value: await svc[method](...args) }
        } catch (e) {
          const err = e as Error & { cause?: unknown }
          return { ok: false as const, name: err.name, message: err.message, cause: err.cause }
        }
      },
      { service, method, args },
    ) as Promise<Outcome<T>>
  }

  /** call is attempt that throws a CallError when the call is rejected. */
  async call<T>(service: ServiceName, method: string, ...args: unknown[]): Promise<T> {
    const r = await this.attempt<T>(service, method, ...args)
    if (!r.ok) {
      throw new CallError(r)
    }
    return r.value
  }

  /**
   * waitIdle waits up to timeoutMs for an idle job queue. The binding
   * caps one WaitIdle at 25 s (so its answer beats the server's write
   * timeout); this loops on it until it reports idle or the time is up.
   */
  async waitIdle(timeoutMs = IDLE_MS): Promise<boolean> {
    const deadline = Date.now() + timeoutMs
    for (;;) {
      const left = deadline - Date.now()
      if (left <= 0) {
        return false
      }
      if (await this.call<boolean>('TestSupport', 'WaitIdle', Math.min(left, 25_000))) {
        return true
      }
    }
  }

  addFolder(path: string): Promise<AddResult> {
    return this.call<AddResult>('TestSupport', 'AddFolderPath', path)
  }

  list(): Promise<FolderSummary[]> {
    return this.call<FolderSummary[]>('Library', 'List')
  }

  async folder(id: string): Promise<FolderSummary | undefined> {
    return (await this.list()).find((f) => f.id === id)
  }

  /** addAndWait adds the folder at path and waits for an idle queue. */
  async addAndWait(path: string): Promise<AddResult> {
    const res = await this.addFolder(path)
    expect(await this.waitIdle(), 'the job queue drains').toBe(true)
    return res
  }

  /** events returns every event the page has recorded so far. */
  events(): Promise<RecordedEvent[]> {
    return this.page.evaluate(() => window.choix.events)
  }

  /** clearEvents forgets the events recorded so far. */
  async clearEvents(): Promise<void> {
    await this.page.evaluate(() => {
      window.choix.events.length = 0
    })
  }

  /**
   * resetLibrary removes every folder from the Library (nothing on disk is
   * touched), so a spec starts from an empty Library whatever ran before.
   */
  async resetLibrary(): Promise<void> {
    const folders = await this.list()
    if (folders.length === 0) {
      return
    }
    await this.call('TestSupport', 'SetConfirmAnswer', true)
    try {
      for (const f of folders) {
        await this.call('Library', 'Remove', f.id)
      }
    } finally {
      await this.call('TestSupport', 'SetConfirmAnswer', false)
    }
    expect(await this.waitIdle(), 'the job queue drains after the reset').toBe(true)
    expect(await this.list()).toEqual([])
  }
}

/**
 * hostsAway returns the http(s) and ws(s) URLs whose host is not 127.0.0.1
 * or, when port is given, whose port is not port.
 */
export function hostsAway(urls: string[], port?: number): string[] {
  return urls.filter((u) => {
    const url = new URL(u)
    if (!['http:', 'https:', 'ws:', 'wss:'].includes(url.protocol)) return false
    const defaultPort = url.protocol === 'https:' || url.protocol === 'wss:' ? '443' : '80'
    return url.hostname !== '127.0.0.1' || (port !== undefined && (url.port || defaultPort) !== String(port))
  })
}

export interface RawResponse {
  status: number
  headers: http.IncomingHttpHeaders
  body: Buffer
}

/**
 * rawGet sends GET path to the server as it is: unlike fetch, Node's http
 * client does not normalise dot segments, so a traversal attempt reaches
 * the server verbatim.
 */
export function rawGet(path: string, headers: Record<string, string> = {}, port = PORT): Promise<RawResponse> {
  return new Promise((resolve, reject) => {
    const req = http.request({ host: '127.0.0.1', port, path, method: 'GET', headers }, (res) => {
      const chunks: Buffer[] = []
      res.on('data', (c: Buffer) => chunks.push(c))
      res.on('end', () => resolve({ status: res.statusCode ?? 0, headers: res.headers, body: Buffer.concat(chunks) }))
      res.on('error', reject)
    })
    req.on('error', reject)
    req.end()
  })
}

/**
 * test gives every spec the worker's harness page on the shared server, and
 * checks after each test that the page sent no request away from
 * 127.0.0.1 (the model settings are off, so nothing may be downloaded).
 */
export const test = base.extend<{ stayLocal: void }, { harness: Harness }>({
  harness: [
    async ({ browser }, use) => {
      const page = await browser.newPage()
      try {
        await use(await Harness.open(page, BASE))
      } finally {
        await page.close()
      }
    },
    { scope: 'worker' },
  ],
  stayLocal: [
    async ({ harness }, use) => {
      const start = harness.requests.length
      await use()
      expect(hostsAway(harness.requests.slice(start), PORT), `requests and WebSockets not to 127.0.0.1:${PORT}`).toEqual([])
    },
    { auto: true },
  ],
})

export { expect }
