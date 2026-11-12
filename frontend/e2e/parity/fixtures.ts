// The parity fixture of every state a case drives: fixtureFor(n). The
// fixtures themselves are written per group (fixtures/<group>.ts, merged in
// fixtures/table.ts) from the prototype's own mock data (prototype/mock/*,
// the scenarios' constants), so the app is fed exactly what the prototype
// state shows.
//
// Where the table is evaluated. The prototype's modules are Vite modules:
// mock/data.ts imports photos.json without an import attribute and the
// scenario files import design components that import .svg assets. Bun
// evaluates them (the bun tests import the table directly); Node, which runs
// Playwright, refuses both. So on Node the table comes from one
// `bun fixtures/dump.ts` per process, as JSON. A fixture is therefore plain
// JSON data (fixtures.test.ts holds every fixture to that), which is also
// what page.evaluate hands the page.
//
// Media. The prototype's mock photos are `new URL('./photos/pNN.jpg',
// import.meta.url)`: in the prototype build Vite turns each into an asset
// URL (the file's own bytes) or, for a file under Vite's inline limit (the
// 60-byte broken.jpg), a base64 data: URL. Evaluated outside Vite the same
// expression is a file: URL, which a page cannot load. fixtureFor rewrites
// every such string anywhere in the fixture to what gives the app build the
// same bytes: `/mock/photos/<name>` (served by serve-app.mjs from
// prototype/mock/photos/) or the data: URL Vite inlines. Parity compares
// images by their bytes (`res:<sha256>`; a data: URL by its text), so the
// app's images match the prototype's whatever their URLs.
import { spawnSync } from 'node:child_process'
import { readFileSync, statSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import type { ParityFixture } from './mock-bindings/control'

/** Where the prototype's mock photos live, as a file: URL prefix. */
const PHOTOS = new URL('../../prototype/mock/photos/', import.meta.url).href
/** The path serve-app.mjs serves them under. */
export const PHOTOS_PATH = '/mock/photos/'
/** Vite's default build.assetsInlineLimit (vite.prototype.config.ts keeps the default): smaller files become data: URLs. */
export const INLINE_LIMIT = 4096

const MIME: Record<string, string> = { jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', webp: 'image/webp' }

/** A prototype mock photo's file: URL as the app build gets the same bytes; any other string unchanged. */
export function servedPhoto(href: string): string {
  if (!href.startsWith(PHOTOS)) return href
  const name = href.slice(PHOTOS.length)
  if (name === '' || name.includes('/')) throw new Error(`parity fixture: unexpected photo URL ${href}`)
  const file = fileURLToPath(href)
  if (statSync(file).size >= INLINE_LIMIT) return `${PHOTOS_PATH}${name}`
  const mime = MIME[name.slice(name.lastIndexOf('.') + 1).toLowerCase()]
  if (!mime) throw new Error(`parity fixture: no MIME type for ${name}`)
  return `data:${mime};base64,${readFileSync(file).toString('base64')}`
}

/**
 * The file name (under prototype/mock/photos/) of a prototype photo a member's
 * media URL shows, for serve-app.mjs's /media/ manifest (fixtures/media.ts).
 * Throws for anything else, and for a photo Vite inlines as a data: URL: the
 * extractor identifies a data: URL by its text and an http URL by its bytes,
 * so no URL the app builds could match it.
 */
export function mediaPhoto(href: string): string {
  const served = servedPhoto(href)
  if (!served.startsWith(PHOTOS_PATH)) {
    throw new Error(`parity fixture: ${href} is not a served prototype photo (a data: URL the app's /media/ URLs cannot match, or another URL)`)
  }
  return served.slice(PHOTOS_PATH.length)
}

/** `value` with every prototype photo URL in it rewritten (servedPhoto); a new value. */
export function served<T>(value: T): T {
  if (typeof value === 'string') return servedPhoto(value) as T
  if (Array.isArray(value)) return value.map(served) as T
  if (value !== null && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, served(v)])) as T
  }
  return value
}

let table: Record<number, ParityFixture> | null = null

/** The fixture table: imported under bun, read from `bun fixtures/dump.ts` on Node. */
function fixtureTable(): Record<number, ParityFixture> {
  if (table) return table
  if (typeof Bun !== 'undefined') {
    table = (createRequire(import.meta.url)('./fixtures/table') as { TABLE: Record<number, ParityFixture> }).TABLE
    return table
  }
  const dump = fileURLToPath(new URL('./fixtures/dump.ts', import.meta.url))
  const r = spawnSync('bun', [dump], { encoding: 'utf8', maxBuffer: 256 * 1024 * 1024 })
  if (r.error || r.status !== 0) {
    throw new Error(`parity: \`bun ${dump}\` failed (${r.error?.message ?? `exit ${r.status}`}):\n${r.stderr ?? ''}`)
  }
  table = JSON.parse(r.stdout) as Record<number, ParityFixture>
  return table
}

/** The fixture of state `n`, ready to hand to window.__choixParity.load in the page. */
export function fixtureFor(n: number): ParityFixture {
  const t = fixtureTable()
  if (!Object.hasOwn(t, n)) throw new Error(`parity: no fixture for #${n} (add it to its group's file in e2e/parity/fixtures/)`)
  return served(t[n]!)
}

/** The states that have a fixture, in order. */
export function fixtureStates(): number[] {
  return Object.keys(fixtureTable())
    .map(Number)
    .sort((a, b) => a - b)
}
