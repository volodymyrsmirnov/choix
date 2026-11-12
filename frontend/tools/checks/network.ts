// Console, page errors and requests, watched from Node for one page.
//
//   console   a console message of type error (except the browser's own
//             "Failed to load resource" line, which `request` reports)
//   pageerror an uncaught exception or unhandled rejection
//   request   a request that failed, or answered 400 or above
//   external  a request to a host other than localhost / 127.0.0.1; it is
//             aborted before it leaves the machine (blockExternal), and its
//             failure is not reported again as `request`
import type { BrowserContext, Page } from '@playwright/test'
import type { Raw } from './dom'

const LOCAL_HOSTS = new Set(['localhost', '127.0.0.1'])
const INERT_PROTOCOLS = new Set(['data:', 'blob:', 'about:'])

/** True for URLs that stay on this machine (or never touch the network). */
export function isLocal(url: string): boolean {
  let u: URL
  try {
    u = new URL(url)
  } catch {
    return false
  }
  return INERT_PROTOCOLS.has(u.protocol) || LOCAL_HOSTS.has(u.hostname)
}

/** Aborts every request that would leave localhost, for every page of the context. */
export async function blockExternal(context: BrowserContext): Promise<void> {
  await context.route((url) => !isLocal(url.href), (route) => route.abort('blockedbyclient'))
}

export interface PageWatch {
  findings(): Raw[]
}

export function watchPage(page: Page): PageWatch {
  const out: Raw[] = []
  page.on('console', (m) => {
    if (m.type() !== 'error' || /^Failed to load resource/.test(m.text())) return
    out.push({ check: 'console', message: `console error: ${m.text()}` })
  })
  page.on('pageerror', (e) => out.push({ check: 'pageerror', message: `page error: ${e.message}` }))
  page.on('request', (r) => {
    if (!isLocal(r.url())) out.push({ check: 'external', message: `request to another host: ${r.method()} ${r.url()}` })
  })
  page.on('requestfailed', (r) => {
    if (isLocal(r.url())) out.push({ check: 'request', message: `request failed: ${r.url()} (${r.failure()?.errorText ?? 'unknown'})` })
  })
  page.on('response', (r) => {
    if (r.status() >= 400) out.push({ check: 'request', message: `request answered ${r.status()}: ${r.url()}` })
  })
  return { findings: () => [...out] }
}
