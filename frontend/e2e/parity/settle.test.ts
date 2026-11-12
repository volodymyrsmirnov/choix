// settlePage's image wait (parity.ts) against a fake page: by default every
// image is waited for, exactly as before; a case's pendingImages leaves out
// exactly the images whose src or currentSrc is one of those URLs (resolved
// against the page's base URL) and nothing else. runCase hands settle the
// case's list, and validateCases refuses an empty or blank declaration.
import { afterEach, beforeEach, expect, test } from 'bun:test'
import type { Page } from '@playwright/test'
import { runCase, settlePage, validateCases } from './parity'

const BASE = 'http://127.0.0.1:8805/'

interface FakeImage {
  src: string
  currentSrc: string
  decoded: number
  decode(): Promise<void>
}

/** An image that decodes at once, or (pending) never, as Chromium's decode() of an unanswered request. */
function image(src: string, opts: { pending?: boolean; currentSrc?: string } = {}): FakeImage {
  const img: FakeImage = {
    src: new URL(src, BASE).href,
    currentSrc: opts.currentSrc === undefined ? new URL(src, BASE).href : opts.currentSrc,
    decoded: 0,
    decode() {
      img.decoded++
      return opts.pending ? new Promise<void>(() => {}) : Promise.resolve()
    },
  }
  return img
}

const saved: Record<string, unknown> = {}
const GLOBALS = ['document', 'requestAnimationFrame', 'MutationObserver'] as const

function fakePage(images: FakeImage[]) {
  const g = globalThis as Record<string, unknown>
  g.document = { fonts: { ready: Promise.resolve() }, baseURI: BASE, images, getAnimations: () => [] }
  g.requestAnimationFrame = (cb: (t: number) => void) => setTimeout(() => cb(0), 0)
  g.MutationObserver = class {
    observe() {}
    disconnect() {}
  }
}

beforeEach(() => {
  for (const k of GLOBALS) saved[k] = (globalThis as Record<string, unknown>)[k]
})
afterEach(() => {
  for (const k of GLOBALS) (globalThis as Record<string, unknown>)[k] = saved[k]
})

/** 'settled', or 'waiting' when settlePage has not resolved after `ms`. */
const outcome = (p: Promise<void>, ms = 200) =>
  Promise.race([p.then(() => 'settled' as const), new Promise<'waiting'>((resolve) => setTimeout(() => resolve('waiting'), ms))])

const FULL = '/media/full/dolomites/IMG_4105.jpg?v=c0ffee1009'

test('without pendingImages every image is waited for: one that never decodes keeps the page unsettled', async () => {
  const imgs = [image('/media/thumb/dolomites/a.jpg?tier=thumb'), image(FULL, { pending: true })]
  fakePage(imgs)
  expect(await outcome(settlePage())).toBe('waiting')
  expect(imgs.map((i) => i.decoded)).toEqual([1, 1])
})

test('the declared URL is not waited for; the page settles and every other image was decoded', async () => {
  const imgs = [image('/media/thumb/dolomites/a.jpg?tier=thumb'), image(FULL, { pending: true }), image('/mock/photos/p01.jpg')]
  fakePage(imgs)
  expect(await outcome(settlePage([FULL]))).toBe('settled')
  expect(imgs.map((i) => i.decoded)).toEqual([1, 0, 1])
})

test('a URL is matched exactly after resolving it against the page: another query, path or origin is still waited for', async () => {
  for (const other of ['/media/full/dolomites/IMG_4105.jpg?v=c0ffee1008', '/media/full/dolomites/IMG_4105.jpg', 'http://127.0.0.1:9999/media/full/dolomites/IMG_4105.jpg?v=c0ffee1009']) {
    const imgs = [image(FULL, { pending: true })]
    fakePage(imgs)
    expect([other, await outcome(settlePage([other]))]).toEqual([other, 'waiting'])
  }
  // The absolute form of the same URL is the same image.
  fakePage([image(FULL, { pending: true })])
  expect(await outcome(settlePage([new URL(FULL, BASE).href]))).toBe('settled')
})

test('an image is matched by its currentSrc as well as its src', async () => {
  const imgs = [image('/media/thumb/dolomites/x.jpg?tier=preview', { pending: true, currentSrc: new URL(FULL, BASE).href })]
  fakePage(imgs)
  expect(await outcome(settlePage([FULL]))).toBe('settled')
})

test('a declared URL leaves out every image showing it, and only those', async () => {
  const imgs = [image(FULL, { pending: true }), image(FULL, { pending: true }), image('/media/full/dolomites/IMG_4106.jpg?v=c0ffee100a', { pending: true })]
  fakePage(imgs)
  expect(await outcome(settlePage([FULL]))).toBe('waiting')
  expect(imgs.map((i) => i.decoded)).toEqual([0, 0, 1])
})

test("runCase settles with the case's pendingImages after drive and after interact; without them, with none", async () => {
  const calls: unknown[] = []
  const page = { evaluate: async (_fn: unknown, arg: unknown) => void calls.push(arg) } as unknown as Page
  await runCase(page, { state: 313, drive: async () => {}, interact: async () => {}, pendingImages: [FULL] })
  expect(calls).toEqual([[FULL], [FULL]])
  calls.length = 0
  await runCase(page, { state: 301, drive: async () => {} })
  expect(calls).toEqual([[]])
})

test('validateCases refuses an empty or blank pendingImages, and one on a skipped case', () => {
  expect(() => validateCases([{ state: 313, drive: async () => {}, pendingImages: [FULL] }])).not.toThrow()
  expect(() => validateCases([{ state: 313, drive: async () => {}, pendingImages: [] }])).toThrow('without a URL')
  expect(() => validateCases([{ state: 313, drive: async () => {}, pendingImages: [' '] }])).toThrow('without a URL')
  expect(() => validateCases([{ state: 313, skip: 'why', pendingImages: [FULL] }])).toThrow('skipped')
})
