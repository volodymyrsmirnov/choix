import { expect, test } from 'bun:test'
import { DEFAULT_TOGGLES, formatLocation, locationHref, parseLocation, shareUrl, SIZE_PX } from './location'

test('a bare hash names the state, with default toggles', () => {
  expect(parseLocation('', '#s102')).toEqual({ n: 102, toggles: DEFAULT_TOGGLES })
})

test('no hash, or a malformed one, names no state', () => {
  expect(parseLocation('', '').n).toBeNull()
  expect(parseLocation('', '#x102').n).toBeNull()
})

test('query toggles are read; flags count by presence', () => {
  const { toggles } = parseLocation('?bare&size=min&freeze&nowrapfx&theme=dark&os=macos', '#s10')
  expect(toggles).toEqual({ size: 'min', theme: 'dark', os: 'macos', freeze: true, nowrapfx: true, bare: true })
})

test('unknown toggle values fall back to the defaults', () => {
  const { toggles } = parseLocation('?size=huge&theme=light&os=linux&freeze=0', '')
  expect(toggles).toEqual(DEFAULT_TOGGLES)
})

test('formatting round-trips and leaves defaults out', () => {
  expect(formatLocation(102, DEFAULT_TOGGLES)).toBe('#s102')
  const t = { ...DEFAULT_TOGGLES, size: 'large' as const, bare: true, freeze: true }
  const url = formatLocation(803, t)
  expect(url).toBe('?bare&size=large&freeze#s803')
  const [search, hash] = [url.slice(0, url.indexOf('#')), url.slice(url.indexOf('#'))]
  expect(parseLocation(search, hash)).toEqual({ n: 803, toggles: t })
})

test('the three review sizes come from gate.json', () => {
  expect(SIZE_PX).toEqual({ min: [960, 600], default: [1280, 800], large: [1680, 1050] })
})

test('clearing a toggle removes it from the produced URL (the query is never inherited)', () => {
  const on = { ...DEFAULT_TOGGLES, freeze: true, size: 'min' as const }
  expect(locationHref('/', 102, on)).toBe('/?size=min&freeze#s102')
  const off = { ...on, freeze: false, size: 'default' as const }
  const href = locationHref('/', 102, off)
  expect(href).toBe('/#s102')
  // Resolved against a URL that still carries the old query, the old toggles are gone.
  const resolved = new URL(href, 'http://localhost:8811/?size=min&freeze#s101')
  expect(resolved.search).toBe('')
  expect(parseLocation(resolved.search, resolved.hash)).toEqual({ n: 102, toggles: DEFAULT_TOGGLES })
})

test("Copy link's URL carries exactly the current state and toggles", () => {
  const t = { ...DEFAULT_TOGGLES, nowrapfx: true, size: 'large' as const }
  const url = new URL(shareUrl('http://localhost:8811', '/', 805, t))
  expect(parseLocation(url.search, url.hash)).toEqual({ n: 805, toggles: t })
  const cleared = new URL(shareUrl('http://localhost:8811', '/', 805, DEFAULT_TOGGLES))
  expect(cleared.href).toBe('http://localhost:8811/#s805')
})
