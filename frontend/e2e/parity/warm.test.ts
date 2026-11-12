// parity.ts's resource warm-up (warmResources and its parts): a page's
// resource URLs are resolved into the cache a few at a time, a fetch whose
// connection was reset is tried again a bounded number of times, and the
// identities are exactly resolveResources' own — so warming changes when
// the bytes are fetched, never what is compared.
import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { resolveResources, resourceId } from '../../tools/extract'
import { isConnectionReset, mapLimit, WARM_ATTEMPTS, WARM_CONCURRENCY, warmResources, withRetry } from './parity'

const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 1))
const resetError = () => new TypeError('fetch failed', { cause: Object.assign(new Error('read ECONNRESET'), { code: 'ECONNRESET' }) })

describe('mapLimit', () => {
  test('never runs more than `limit` at once, and keeps the input order', async () => {
    let inFlight = 0
    let most = 0
    const out = await mapLimit(
      Array.from({ length: 40 }, (_, i) => i),
      6,
      async (x) => {
        inFlight++
        most = Math.max(most, inFlight)
        for (let k = 0; k < (x % 4) + 1; k++) await tick()
        inFlight--
        return x * 2
      },
    )
    expect(most).toBe(6)
    expect(out).toEqual(Array.from({ length: 40 }, (_, i) => i * 2))
  })

  test('an empty list, and a limit above the list length', async () => {
    expect(await mapLimit([], 4, async () => 1)).toEqual([])
    expect(await mapLimit([1, 2], 10, async (x) => x + 1)).toEqual([2, 3])
  })
})

describe('withRetry', () => {
  test('tries a retryable failure again, up to `attempts` in all, then throws the last error', async () => {
    let calls = 0
    const err = resetError()
    await expect(
      withRetry(
        async () => {
          calls++
          throw err
        },
        { attempts: 3, retryable: isConnectionReset },
      ),
    ).rejects.toBe(err)
    expect(calls).toBe(3)
  })

  test('succeeds on a later try', async () => {
    let calls = 0
    const got = await withRetry(
      async () => {
        if (++calls < 3) throw resetError()
        return 'ok'
      },
      { attempts: 5, retryable: isConnectionReset },
    )
    expect([got, calls]).toEqual(['ok', 3])
  })

  test('an error that is not retryable is thrown at once', async () => {
    let calls = 0
    await expect(
      withRetry(
        async () => {
          calls++
          throw new Error('extract: /media/x answered 404')
        },
        { attempts: 5, retryable: isConnectionReset },
      ),
    ).rejects.toThrow('answered 404')
    expect(calls).toBe(1)
  })
})

test('isConnectionReset: connection codes on the error or its causes, nothing else', () => {
  expect(isConnectionReset(resetError())).toBe(true)
  expect(isConnectionReset(new TypeError('fetch failed', { cause: { code: 'UND_ERR_SOCKET' } }))).toBe(true)
  expect(isConnectionReset({ code: 'ECONNREFUSED' })).toBe(true)
  expect(isConnectionReset(new Error('extract: /a answered 404'))).toBe(false)
  expect(isConnectionReset(new TypeError('fetch failed', { cause: { code: 'ENOTFOUND' } }))).toBe(false)
  expect(isConnectionReset(null)).toBe(false)
})

describe('warmResources against a server that resets connections', () => {
  // Each /r/<i> answers its own bytes after a short wait; the first two
  // requests for a path in /flaky/ have their connection destroyed (a reset
  // as the client sees it); /missing answers 404.
  let server: Server
  let base = ''
  let inFlight = 0
  let most = 0
  const seen = new Map<string, number>()
  beforeAll(async () => {
    server = createServer((req, res) => {
      const path = req.url ?? '/'
      const n = (seen.get(path) ?? 0) + 1
      seen.set(path, n)
      if (path.startsWith('/flaky/') && n <= 2) {
        req.socket.destroy()
        return
      }
      if (path === '/missing') {
        res.writeHead(404)
        res.end('not found')
        return
      }
      inFlight++
      most = Math.max(most, inFlight)
      setTimeout(() => {
        inFlight--
        res.writeHead(200, { 'Content-Type': 'application/octet-stream' })
        res.end(`bytes of ${path}`)
      }, 5)
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
  })
  afterAll(() => {
    server.closeAllConnections()
    server.close()
  })

  test('the real fetch error of a reset connection is retryable', async () => {
    const err = await fetch(`${base}/flaky/probe`).then(
      () => null,
      (e: unknown) => e,
    )
    expect(err).not.toBeNull()
    expect(isConnectionReset(err)).toBe(true)
  })

  test('fills the cache with resolveResources’ identities, at most WARM_CONCURRENCY at once, through resets', async () => {
    most = 0
    const urls = [...Array.from({ length: 60 }, (_, i) => `${base}/r/${i}`), `${base}/flaky/a`, `${base}/flaky/b`, 'data:image/png;base64,AAAA']
    const cache = new Map<string, string>()
    await warmResources([...urls, urls[0]!], cache, { delayMs: () => 1 })
    expect(most).toBeLessThanOrEqual(WARM_CONCURRENCY)
    expect(most).toBeGreaterThan(1)
    expect(seen.get('/flaky/a')).toBe(3)
    expect(seen.get('/r/0')).toBe(1)
    // The same identities resolveResources gives on its own (bytes for http, the text for data:).
    expect(Object.fromEntries(cache)).toEqual(await resolveResources(urls))
    expect(cache.get(`${base}/r/7`)).toBe(resourceId(new TextEncoder().encode('bytes of /r/7')))
    // Warm again: everything is cached, nothing is fetched.
    const before = seen.get('/r/7')
    await warmResources(urls, cache)
    expect(seen.get('/r/7')).toBe(before)
  })

  test('a URL that answers an HTTP error, or keeps resetting, stays uncached and does not throw', async () => {
    const cache = new Map<string, string>()
    let tries = 0
    await warmResources([`${base}/missing`, `${base}/r/ok`], cache, {
      resolve: (u, c) => {
        tries++
        return resolveResources(u, c)
      },
    })
    expect(tries).toBe(2) // the 404 was not retried
    expect(cache.has(`${base}/missing`)).toBe(false)
    expect(cache.has(`${base}/r/ok`)).toBe(true)

    let resets = 0
    await warmResources(['http://127.0.0.1:1/never'], cache, {
      delayMs: () => 0,
      resolve: async () => {
        resets++
        throw resetError()
      },
    })
    expect(resets).toBe(WARM_ATTEMPTS)
    expect(cache.has('http://127.0.0.1:1/never')).toBe(false)
  })
})
