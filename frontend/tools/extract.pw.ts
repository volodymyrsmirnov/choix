// Tests of the fingerprint extractor (tools/extract.ts), on Node through the
// `extract` project. Most use inline fixture pages, each served by its own
// server on an ephemeral 127.0.0.1 port; the rest use the built prototype
// (the design global setup serves it).
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { createServer, type Server } from 'node:http'
import { cpus, loadavg } from 'node:os'
import type { AddressInfo } from 'node:net'
import { join } from 'node:path'
import { expect, test, type Browser, type Page } from '@playwright/test'
import postcss from 'postcss'
import { MEASURED } from '../src/design/measured'
import { PROTO_MEASURED } from '../prototype/measured'
import { FRONTEND, protoUrl, SIZES, type Size } from './designPaths'
import { DENYLIST, extractPage, hashTree, measuredDigests, TYPED_PROPERTIES, type Tree } from './extract'
import { BUDGET_MS, budgetVerdict, CI_LIMIT_MS } from './extractBudget'
import { newDesignContext, openState, waitForHarness } from './stateRunner'
import forcePlugin from './postcss-force.cjs'

type Files = Record<string, string | Uint8Array>

const TYPES: Record<string, string> = {
  html: 'text/html; charset=utf-8',
  css: 'text/css',
  png: 'image/png',
  woff2: 'font/woff2',
}

/** Serves `files` on an ephemeral port; `{{ORIGIN}}` in a text file becomes the server's origin. */
async function serveFiles(files: Files): Promise<{ origin: string; close: () => Promise<void> }> {
  let origin = ''
  const server: Server = createServer((req, res) => {
    const path = new URL(req.url ?? '/', 'http://127.0.0.1').pathname
    const body = files[path === '/' ? '/index.html' : path]
    if (body === undefined) {
      res.writeHead(404).end()
      return
    }
    res.writeHead(200, { 'Content-Type': TYPES[path.split('.').pop() ?? ''] ?? 'text/html; charset=utf-8', 'Cache-Control': 'no-store' })
    res.end(typeof body === 'string' ? body.replaceAll('{{ORIGIN}}', origin) : body)
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
  return { origin, close: () => new Promise((resolve) => server.close(() => resolve())) }
}

const BASE_CSS = `
  @font-face { font-family: Fx; src: url("/f.woff2") format("woff2"); }
  body { margin: 0; font-family: Fx, sans-serif; }
  .box { width: 50%; color: rgb(10, 20, 30); box-shadow: 0 1px 2px rgb(0, 0, 0); font-variant-numeric: normal; }
  .probe { display: block; border: 1px solid rgb(0, 0, 0); }
`
const BASE_BODY = `
  <div class="box a" role="region" aria-label="Box">Hello <b>world</b></div>
  <a href="{{ORIGIN}}/next#s2">next</a>
  <img src="/i.png" alt="photo">
  <svg viewBox="0 0 10 10" width="10" height="10"><path d="M0 0L10 10" /></svg>
  <input class="field" value="first">
  <video poster="/p1.png"></video>
  <div class="probe"></div>
`

interface Fixture {
  css?: string
  body?: string
  /** Extra elements in <head>, before the stylesheet. */
  head?: string
  /** Attributes of <html>, after lang="en". */
  htmlAttrs?: string
  files?: Files
}

function fixtureFiles(f: Fixture = {}): Files {
  return {
    '/index.html': `<!doctype html><html lang="en"${f.htmlAttrs ? ` ${f.htmlAttrs}` : ''}><head><meta charset="utf-8">${f.head ?? ''}<link rel="stylesheet" href="/s.css"></head><body>${f.body ?? BASE_BODY}</body></html>`,
    '/s.css': f.css ?? BASE_CSS,
    '/f.woff2': 'font-bytes-1',
    '/i.png': 'image-bytes-1',
    '/p1.png': 'poster-bytes-1',
    '/p2.png': 'poster-bytes-2',
    ...f.files,
  }
}

interface Opts {
  /** Runs on the loaded page before the extraction. */
  before?: (page: Page) => Promise<void>
  measured?: Record<string, string>
}

async function extractFixture(browser: Browser, f: Fixture = {}, o: Opts = {}): Promise<Tree> {
  const server = await serveFiles(fixtureFiles(f))
  const context = await newDesignContext(browser, 'default')
  try {
    const page = await context.newPage()
    await page.goto(`${server.origin}/`)
    await o.before?.(page)
    return await extractPage(page, { measured: o.measured })
  } finally {
    await context.close()
    await server.close()
  }
}

const hashFixture = async (browser: Browser, f: Fixture = {}, o: Opts = {}) => hashTree(await extractFixture(browser, f, o))

const MEASURED_DIGESTS = measuredDigests(FRONTEND, [MEASURED, PROTO_MEASURED])

/** Extracts prototype state `n` at `size` in a fresh context; `first` opens other states before it. */
async function extractState(browser: Browser, n: number, size: Size = 'default', first: number[] = []): Promise<Tree> {
  const context = await newDesignContext(browser, size)
  try {
    const page = await context.newPage()
    await page.goto(`${protoUrl()}?bare&freeze&size=${size}#s${first[0] ?? n}`)
    await waitForHarness(page)
    for (const m of [...first, n]) await openState(page, m)
    return await extractPage(page, { measured: MEASURED_DIGESTS })
  } finally {
    await context.close()
  }
}

test.describe('a design change changes the hash', () => {
  let base: string
  test.beforeAll(async ({ browser }) => {
    base = await hashFixture(browser)
  })

  const cases: [string, Fixture | (() => Fixture), Opts?][] = [
    ['one text node', { body: BASE_BODY.replace('Hello', 'Hullo') }],
    ['one class', { body: BASE_BODY.replace('class="box a"', 'class="box a b"') }],
    ['one colour', { css: BASE_CSS.replace('rgb(10, 20, 30)', 'rgb(10, 20, 31)') }],
    ['width: 50% vs 51%', { css: BASE_CSS.replace('width: 50%', 'width: 51%') }],
    ['font-variant-numeric', { css: BASE_CSS.replace('font-variant-numeric: normal', 'font-variant-numeric: tabular-nums') }],
    ['box-shadow', { css: BASE_CSS.replace('0 1px 2px', '0 1px 3px') }],
    ['an SVG d', { body: BASE_BODY.replace('M0 0L10 10', 'M0 0L10 9') }],
    ['an input\'s live value', {}, { before: (page) => page.locator('input.field').evaluate((el: HTMLInputElement) => (el.value = 'second')).then(() => {}) }],
    ['a video poster', { body: BASE_BODY.replace('/p1.png', '/p2.png') }],
    ['the bytes behind an unchanged image URL', { files: { '/i.png': 'image-bytes-2' } }],
    ['the bytes behind an unchanged font URL', { files: { '/f.woff2': 'font-bytes-2' } }],
  ]
  for (const [name, fixture, opts] of cases) {
    test(name, async ({ browser }) => {
      const f = typeof fixture === 'function' ? fixture() : fixture
      expect(await hashFixture(browser, f, opts)).not.toBe(base)
    })
  }
})

test.describe('what is not design does not change the hash', () => {
  test('a data-measured property', async ({ browser }) => {
    const page = (w: number) => ({
      body: `${BASE_BODY}<div data-measured="width mask-image" data-measured-by="x" style="width: ${w}px; mask-image: linear-gradient(red ${w}%, blue)"></div>`,
    })
    const measured = { 'measured:x': 'x:0123', 'measuredProps:x': 'mask-image width' }
    const a = await extractFixture(browser, page(10), { measured })
    const b = await extractFixture(browser, page(20), { measured })
    expect(hashTree(a)).toBe(hashTree(b))
    const node = JSON.stringify(a)
    expect(node).toContain('"measuredBy":"x:0123"')
    // The measured id's files are part of the identity.
    expect(hashTree(await extractFixture(browser, page(10), { measured: { ...measured, 'measured:x': 'x:4567' } }))).not.toBe(hashTree(a))
  })

  test('data-measured and data-measured-by come together, and drop only what measured.ts allows', async ({ browser }) => {
    const measured = { 'measured:x': 'x:0123', 'measuredProps:x': 'width', 'measured:s': 's:89ab', 'measuredProps:s': '' }
    await expect(extractFixture(browser, { body: '<div data-measured="width"></div>' }, { measured })).rejects.toThrow(/data-measured without data-measured-by/)
    await expect(extractFixture(browser, { body: '<div data-measured-by="x"></div>' }, { measured })).rejects.toThrow(/data-measured-by without data-measured/)
    await expect(extractFixture(browser, { body: '<div data-measured="height" data-measured-by="x"></div>' }, { measured })).rejects.toThrow(/drops height/)
    // A scroller set from code drops nothing; its id's files still count.
    const scroller = await extractFixture(browser, { body: '<div data-measured="" data-measured-by="s"></div>' }, { measured })
    expect(JSON.stringify(scroller)).toContain('"measuredBy":"s:89ab"')
    const other = await extractFixture(browser, { body: '<div data-measured="" data-measured-by="s"></div>' }, { measured: { ...measured, 'measured:s': 's:cdef' } })
    expect(hashTree(other)).not.toBe(hashTree(scroller))
  })

  test('an unregistered measured id throws', async ({ browser }) => {
    await expect(extractFixture(browser, { body: '<div data-measured="width" data-measured-by="nobody"></div>' })).rejects.toThrow(/measured by "nobody"/)
  })

  test('the server port', async ({ browser }) => {
    const a = await extractFixture(browser)
    const b = await extractFixture(browser)
    expect(hashTree(a)).toBe(hashTree(b))
    const json = JSON.stringify(a)
    expect(json).toContain('"href":"/next#s2"')
    expect(json).not.toMatch(/127\.0\.0\.1|localhost|https?:/)
  })

  test('the order states were rendered', async ({ browser }) => {
    const fresh = hashTree(await extractState(browser, 109))
    const after = hashTree(await extractState(browser, 109, 'default', [202, 305]))
    expect(after).toBe(fresh)
  })

  test('generated element ids', async ({ browser }) => {
    const page = (id: string) => ({ body: `<label for="${id}">Name</label><span class="field-wrap"><input id="${id}" aria-describedby="${id}-hint"></span><p id="${id}-hint">Hint</p>` })
    const a = await extractFixture(browser, page(':r1:'))
    expect(hashTree(await extractFixture(browser, page(':r7:')))).toBe(hashTree(a))
    expect(JSON.stringify(a)).toMatch(/"for":"#\d+"/)
  })

  test('generated ids behind an href fragment (<use href>, <use xlink:href>, <a href>)', async ({ browser }) => {
    const page = (a: string, b: string, target = a) => ({
      body: `${BASE_BODY}<svg width="8" height="8"><path id="${a}" d="M0 0L4 4"></path><path id="${b}" d="M0 4L4 0"></path><use href="#${target}"></use><use xlink:href="#${b}"></use></svg><a href="#${a}">jump</a>`,
    })
    const one = await extractFixture(browser, page(':r1:', ':r2:'))
    expect(hashTree(await extractFixture(browser, page(':r7:', ':r9:')))).toBe(hashTree(one))
    const json = JSON.stringify(one)
    expect(json).not.toMatch(/:r\d:/)
    expect(json).toMatch(/"href":"#\d+"/)
    expect(json).toMatch(/"xlink:href":"#\d+"/)
    // Pointing the <use> at the other element still changes the hash.
    expect(hashTree(await extractFixture(browser, page(':r1:', ':r2:', ':r2:')))).not.toBe(hashTree(one))
  })

  test('a real hover hashes as data-force="hover"', async ({ browser }) => {
    const css = (await postcss([forcePlugin()]).process(`${BASE_CSS}\n.btn { background: rgb(1, 2, 3); } .btn:hover { background: rgb(4, 5, 6); }`, { from: undefined })).css
    expect(css).toContain('[data-force~="hover"]')
    const body = `${BASE_BODY}<button class="btn" type="button">Go</button>`
    const rest = await hashFixture(browser, { css, body })
    const hovered = await hashFixture(browser, { css, body }, { before: (page) => page.hover('.btn') })
    const forced = await hashFixture(browser, { css, body: body.replace('class="btn"', 'class="btn" data-force="hover"') })
    expect(hovered).not.toBe(rest)
    expect(forced).toBe(hovered)
  })

  test('an indeterminate ProgressRail under freeze, 300 ms apart', async ({ browser }) => {
    const context = await newDesignContext(browser, 'default')
    try {
      const page = await context.newPage()
      await page.goto(`${protoUrl()}?bare&freeze#s805`)
      await waitForHarness(page)
      await openState(page, 805)
      await expect(page.locator('.progress-fill[data-indet="true"]').first()).toBeAttached()
      const first = hashTree(await extractPage(page, { measured: MEASURED_DIGESTS }))
      await page.waitForTimeout(300)
      expect(hashTree(await extractPage(page, { measured: MEASURED_DIGESTS }))).toBe(first)
    } finally {
      await context.close()
    }
  })
})

test.describe('what the tree leaves out, and what it refuses', () => {
  test('data-proto-chrome is absent', async ({ browser }) => {
    const plain = await extractFixture(browser)
    const chrome = await extractFixture(browser, { body: `<div data-proto-chrome><span>s102 navigator chrome</span></div>${BASE_BODY}` })
    expect(JSON.stringify(chrome)).not.toContain('navigator chrome')
    expect(hashTree(chrome)).toBe(hashTree(plain))
  })

  test('script, style, template and the children of display: none are left out', async ({ browser }) => {
    const plain = await extractFixture(browser)
    const extra = await extractFixture(browser, {
      body: `${BASE_BODY}<script>1</script><style>.zz{}</style><template><p>t</p></template>`,
    })
    expect(hashTree(extra)).toBe(hashTree(plain))
    const hidden = (inner: string) => ({ body: `${BASE_BODY}<div style="display: none">${inner}</div>` })
    expect(hashTree(await extractFixture(browser, hidden('<p>one</p>')))).toBe(hashTree(await extractFixture(browser, hidden('<p>two</p>'))))
  })

  test('outline-style: auto throws', async ({ browser }) => {
    await expect(extractFixture(browser, { body: `${BASE_BODY}<button style="outline-style: auto">x</button>` })).rejects.toThrow(/outline-style: auto/)
  })

  test('pseudo-element content throws', async ({ browser }) => {
    await expect(extractFixture(browser, { css: `${BASE_CSS} .box::before { content: "x"; }` })).rejects.toThrow(/::before has content/)
    await expect(extractFixture(browser, { css: `${BASE_CSS} .box::after { content: ""; }` })).rejects.toThrow(/::after has content/)
  })

  test('a resource on another host throws', async ({ browser }) => {
    await expect(extractFixture(browser, { body: `${BASE_BODY}<img src="https://example.com/a.png" alt="">` })).rejects.toThrow(/another host/)
  })

  test('a video\'s user-agent shadow tree never reaches the tree', async ({ browser }) => {
    const tree = await extractState(browser, 306)
    const videos: Tree['root'][] = []
    const walk = (n: Tree['root']) => {
      if (n.tag === 'video') videos.push(n)
      n.children.forEach(walk)
    }
    walk(tree.root)
    expect(videos.length).toBeGreaterThan(0)
    for (const v of videos) expect(v.children).toEqual([])
    expect(hashTree(await extractState(browser, 306))).toBe(hashTree(tree))
  })
})

test.describe('pseudo-element rules and font faces (document-wide)', () => {
  const PSEUDO_CSS = `${BASE_CSS}
    :root { --knob: rgb(1, 2, 3); --bar: 10px; }
    input.field::-webkit-slider-thumb { background: var(--knob); width: 14px; }
    ::-webkit-scrollbar { width: var(--bar); }
    @media (prefers-reduced-motion: reduce) { ::-webkit-scrollbar-thumb { border-radius: 4px; } }
    input.field::placeholder { color: rgb(9, 9, 9); }
  `
  let base: Tree
  test.beforeAll(async ({ browser }) => {
    base = await extractFixture(browser, { css: PSEUDO_CSS })
  })

  test('the rules are in the tree, resolved against :root, with their at-rule context', () => {
    expect(base.pseudoRules).toContain('input.field::-webkit-slider-thumb { background: rgb(1, 2, 3); width: 14px }')
    expect(base.pseudoRules).toContain('::-webkit-scrollbar { width: 10px }')
    expect(base.pseudoRules.some((r) => r.startsWith('@media (prefers-reduced-motion: reduce) ::-webkit-scrollbar-thumb {'))).toBe(true)
  })

  test('the rules are in document (cascade) order, unsorted', () => {
    const at = (prefix: string) => base.pseudoRules.findIndex((r) => r.startsWith(prefix))
    const order = ['input.field::-webkit-slider-thumb', '::-webkit-scrollbar {', '@media', 'input.field::placeholder'].map(at)
    expect(order.every((i) => i >= 0)).toBe(true)
    expect(order).toEqual([...order].sort((a, b) => a - b))
  })

  const changes: [string, string, string][] = [
    ['a slider-thumb colour', 'background: var(--knob); width', 'background: rgb(4, 5, 6); width'],
    ['a scrollbar width', '::-webkit-scrollbar { width: var(--bar); }', '::-webkit-scrollbar { width: 12px; }'],
    ['a change inside an @media block', 'border-radius: 4px', 'border-radius: 5px'],
    ['a custom property such a rule reads', '--knob: rgb(1, 2, 3)', '--knob: rgb(1, 2, 4)'],
    ['a ::placeholder rule', 'color: rgb(9, 9, 9)', 'color: rgb(9, 9, 8)'],
  ]
  for (const [name, from, to] of changes) {
    test(`${name} changes the hash`, async ({ browser }) => {
      expect(PSEUDO_CSS).toContain(from)
      expect(hashTree(await extractFixture(browser, { css: PSEUDO_CSS.replace(from, to) }))).not.toBe(hashTree(base))
    })
  }

  test('a custom property set on an element, which such a rule reads, changes the hash', async ({ browser }) => {
    const css = `${BASE_CSS} input.field::-webkit-slider-thumb { background: var(--slider-knob); } input.field { --slider-knob: rgb(1, 1, 1); }`
    const a = await extractFixture(browser, { css })
    const b = await extractFixture(browser, { css: css.replace('--slider-knob: rgb(1, 1, 1)', '--slider-knob: rgb(2, 2, 2)') })
    expect(a.pseudoRules).toContain('input.field::-webkit-slider-thumb { background: var(--slider-knob) }')
    expect(hashTree(b)).not.toBe(hashTree(a))
  })

  test('a declaration CSSOM cannot read back throws', async ({ browser }) => {
    await expect(
      extractFixture(browser, { css: `${BASE_CSS} :root { --c: red; } .x::-webkit-scrollbar-thumb { background: var(--c); background-clip: content-box; }` }),
    ).rejects.toThrow(/cannot be read back/)
  })

  test('swapping two rules of the same selector with different values changes the hash (the later one wins)', async ({ browser }) => {
    const four = '.x::-webkit-scrollbar-thumb { width: 4px; }'
    const five = '.x::-webkit-scrollbar-thumb { width: 5px; }'
    const a = await extractFixture(browser, { css: `${BASE_CSS} ${four} ${five}` })
    const b = await extractFixture(browser, { css: `${BASE_CSS} ${five} ${four}` })
    expect(a.pseudoRules).toEqual(['.x::-webkit-scrollbar-thumb { width: 4px }', '.x::-webkit-scrollbar-thumb { width: 5px }'])
    expect(hashTree(b)).not.toBe(hashTree(a))
  })

  test('an unchanged page extracted twice gives the same list (duplicates kept, in place)', async ({ browser }) => {
    const css = `${BASE_CSS} .x::-webkit-scrollbar { width: 3px; } .y::-webkit-scrollbar { height: 4px; } .x::-webkit-scrollbar { width: 3px; }`
    const a = await extractFixture(browser, { css })
    const b = await extractFixture(browser, { css })
    expect(a.pseudoRules).toEqual(['.x::-webkit-scrollbar { width: 3px }', '.y::-webkit-scrollbar { height: 4px }', '.x::-webkit-scrollbar { width: 3px }'])
    expect(b.pseudoRules).toEqual(a.pseudoRules)
    expect(hashTree(b)).toBe(hashTree(a))
  })

  // The generic walker: an edit anywhere a pseudo-element rule can sit moves the hash.
  const placed: [string, string, string][] = [
    ['inside @supports', '@supports (display: grid) { .x::-webkit-scrollbar { width: 3px; } }', 'width: 3px'],
    ['inside @layer', '@layer ui { .x::-webkit-scrollbar { width: 3px; } }', 'width: 3px'],
    ['in a mixed selector list', '.y, .x::-webkit-slider-thumb { width: 3px; }', 'width: 3px'],
    ['in a rule nested in a pseudo-element rule', '.x::-webkit-scrollbar { width: 3px; &:hover { width: 6px; } }', 'width: 6px'],
    ['in declarations nested in a pseudo-element rule', '.x::-webkit-scrollbar { width: 3px; @media (min-width: 1px) { height: 6px; } }', 'height: 6px'],
    ['inside @scope', '@scope (.box) to (.probe) { .x::-webkit-scrollbar { width: 3px; } }', 'width: 3px'],
    ['inside @starting-style', '@starting-style { .x::-webkit-scrollbar { width: 3px; } }', 'width: 3px'],
  ]
  for (const [where, rule, decl] of placed) {
    test(`an edit ${where} changes the hash`, async ({ browser }) => {
      const a = await extractFixture(browser, { css: `${BASE_CSS} ${rule}` })
      expect(a.pseudoRules.length, JSON.stringify(a.pseudoRules)).toBeGreaterThan(0)
      const b = await extractFixture(browser, { css: `${BASE_CSS} ${rule.replace(decl, decl.replace(/\d/, '9'))}` })
      expect(hashTree(b)).not.toBe(hashTree(a))
    })
  }

  test('an @scope prelude is recorded in full, so a prelude edit changes the hash', async ({ browser }) => {
    const css = (end: string) => `${BASE_CSS} @scope (.box) to (${end}) { .x::-webkit-scrollbar { width: 3px; } }`
    const a = await extractFixture(browser, { css: css('.probe') })
    expect(a.pseudoRules).toContain('@scope (.box) to (.probe) .x::-webkit-scrollbar { width: 3px }')
    const b = await extractFixture(browser, { css: css('.field') })
    expect(hashTree(b)).not.toBe(hashTree(a))
  })

  test('@starting-style is named in the context', async ({ browser }) => {
    const a = await extractFixture(browser, { css: `${BASE_CSS} @starting-style { .x::-webkit-scrollbar { width: 3px; } }` })
    expect(a.pseudoRules).toContain('@starting-style .x::-webkit-scrollbar { width: 3px }')
  })

  test('cascade layers: reordering layers changes the hash when a recorded rule is layered', async ({ browser }) => {
    const rules = '@layer a { .x::-webkit-scrollbar { width: 3px; } } @layer b { .x::-webkit-scrollbar { width: 4px; } }'
    const ab = await extractFixture(browser, { css: `@layer a, b; ${BASE_CSS} ${rules}` })
    const ba = await extractFixture(browser, { css: `@layer b, a; ${BASE_CSS} ${rules}` })
    expect(ab.pseudoRules).toEqual(ba.pseudoRules)
    expect(ab.layers).toEqual(['a', 'b'])
    expect(ba.layers).toEqual(['b', 'a'])
    expect(hashTree(ba)).not.toBe(hashTree(ab))
    // The order in which layer blocks first appear counts too.
    const blocks = (first: string, second: string) => `@layer ${first} {} @layer ${second} {} ${BASE_CSS} ${rules}`
    const blocksAb = await extractFixture(browser, { css: blocks('a', 'b') })
    const blocksBa = await extractFixture(browser, { css: blocks('b', 'a') })
    expect(blocksBa.pseudoRules).toEqual(blocksAb.pseudoRules)
    expect(hashTree(blocksBa)).not.toBe(hashTree(blocksAb))
    // Nested and anonymous layers get their full names.
    const nested = await extractFixture(browser, { css: `${BASE_CSS} @layer outer { @layer in1, in2; } @layer { } @layer outer.in2 { .x::-webkit-scrollbar { width: 3px; } }` })
    expect(nested.layers).toEqual(['outer', 'outer.in1', 'outer.in2', '(anonymous 1)'])
  })

  test('without a layered rule, the layer order stays out of the tree', async ({ browser }) => {
    const tree = await extractFixture(browser, { css: `@layer b, a; ${BASE_CSS} @layer a { .box { color: rgb(1, 1, 1); } }` })
    expect(tree.layers).toBeUndefined()
  })

  test('::before / ::after rules are not pseudo rules (they throw when they draw)', () => {
    expect(base.pseudoRules.some((r) => /::(before|after)/.test(r))).toBe(false)
  })

  test('every @font-face descriptor counts', async ({ browser }) => {
    const plain = await extractFixture(browser)
    expect(plain.fonts[0].descriptors['font-family']).toBe('Fx')
    const display = await extractFixture(browser, { css: BASE_CSS.replace('format("woff2");', 'format("woff2"); font-display: swap;') })
    expect(display.fonts[0].descriptors['font-display']).toBe('swap')
    expect(hashTree(display)).not.toBe(hashTree(plain))
    const range = await extractFixture(browser, { css: BASE_CSS.replace('format("woff2");', 'format("woff2"); unicode-range: U+0000-00FF;') })
    expect(hashTree(range)).not.toBe(hashTree(plain))
  })

  test('@font-face rules keep document order (of two equal faces the later wins) and their context', async ({ browser }) => {
    const face = (src: string) => `@font-face { font-family: Fy; src: url("${src}") format("woff2"); }`
    const files = { '/g.woff2': 'font-bytes-g' }
    const a = await extractFixture(browser, { css: `${face('/f.woff2')} ${face('/g.woff2')} ${BASE_CSS}`, files })
    const b = await extractFixture(browser, { css: `${face('/g.woff2')} ${face('/f.woff2')} ${BASE_CSS}`, files })
    expect(a.fonts.map((f) => f.res)).not.toEqual(b.fonts.map((f) => f.res))
    expect(hashTree(b)).not.toBe(hashTree(a))
    const inside = (cond: string) => `@supports ${cond} { ${face('/g.woff2')} } ${BASE_CSS}`
    const yes = await extractFixture(browser, { css: inside('(display: grid)'), files })
    expect(yes.fonts.some((f) => f.context === '@supports (display: grid)')).toBe(true)
    expect(hashTree(await extractFixture(browser, { css: inside('not (display: grid)'), files }))).not.toBe(hashTree(yes))
  })

  test('a FontFace added from script throws (its source is in no stylesheet)', async ({ browser }) => {
    await expect(
      extractFixture(browser, {}, { before: (page) => page.evaluate(() => void document.fonts.add(new FontFace('Fz', 'url(/f.woff2)'))) }),
    ).rejects.toThrow(/FontFace/)
  })

  test('an @import rule throws', async ({ browser }) => {
    await expect(extractFixture(browser, { css: `@import url("/more.css");\n${BASE_CSS}`, files: { '/more.css': '.zz { color: red; }' } })).rejects.toThrow(/@import/)
  })

  test('an SVG <image> with an absolute href is a resource only, never a raw URL', async ({ browser }) => {
    const tree = await extractFixture(browser, { body: `${BASE_BODY}<svg width="4" height="4"><image href="{{ORIGIN}}/i.png" width="4" height="4"></image></svg>` })
    const json = JSON.stringify(tree)
    expect(json).not.toMatch(/127\.0\.0\.1|https?:/)
    let image: Tree['root'] | undefined
    const walk = (n: Tree['root']) => {
      if (n.tag === 'image') image = n
      n.children.forEach(walk)
    }
    walk(tree.root)
    expect(image?.attrs.href).toBeUndefined()
    expect(image?.svg?.href).toMatch(/^res:[0-9a-f]{16}$/)
  })
})

test.describe('what no element\'s computed style shows under freeze, and the document element', () => {
  // The prototype's freeze rule: under it every element computes animation
  // and transition as none, so their declarations reach the tree as rules.
  const FREEZE = 'html[data-freeze] * { animation: none !important; transition: none !important; }'
  const MOTION_CSS = `${BASE_CSS}
    :root { --dur: 120ms; }
    .box { transition: opacity var(--dur) ease-out; animation: k 1s linear infinite; }
    @keyframes k { from { opacity: 0; } to { opacity: 1; } }
    @property --p { syntax: "<color>"; inherits: false; initial-value: rgb(1, 2, 3); }
    @counter-style thumbs { system: cyclic; symbols: "*"; suffix: " "; }
    ${FREEZE}
  `
  const frozen = (css: string) => ({ css, htmlAttrs: 'data-freeze' })
  let base: Tree
  test.beforeAll(async ({ browser }) => {
    base = await extractFixture(browser, frozen(MOTION_CSS))
  })

  test('the rules are recorded, in document order, var() resolved against :root', () => {
    expect(base.motionRules).toHaveLength(2)
    expect(base.motionRules[0]).toMatch(/^\.box \{ animation: .*; transition: opacity 120ms ease-out \}$/)
    // (Chromium serialises `animation: none` with every longhand.)
    expect(base.motionRules[1]).toMatch(/^html\[data-freeze\] \* \{ animation: [^;]*none !important; transition: none !important \}$/)
    expect(base.atRules.some((r) => r.startsWith('@keyframes k {'))).toBe(true)
    expect(base.atRules.some((r) => r.startsWith('@property --p {'))).toBe(true)
    expect(base.atRules.some((r) => r.startsWith('@counter-style thumbs {'))).toBe(true)
    // Under freeze, the element itself computes none: only the rule carries the motion.
    expect(base.styles.every((s) => s['animation-name'] === 'none')).toBe(true)
  })

  const changes: [string, string, string][] = [
    ['a transition duration token', '--dur: 120ms', '--dur: 200ms'],
    ['a transition timing function', 'ease-out', 'ease-in'],
    ['an animation in a rule', 'k 1s linear', 'k 2s linear'],
    ['a @keyframes frame', 'to { opacity: 1; }', 'to { opacity: 0.5; }'],
    ['a @property registration', 'initial-value: rgb(1, 2, 3)', 'initial-value: rgb(1, 2, 4)'],
    ['a @counter-style', 'symbols: "*"', 'symbols: "+"'],
  ]
  for (const [name, from, to] of changes) {
    test(`${name} changes the hash under freeze`, async ({ browser }) => {
      expect(MOTION_CSS).toContain(from)
      expect(hashTree(await extractFixture(browser, frozen(MOTION_CSS.replace(from, to))))).not.toBe(hashTree(base))
    })
  }

  test('a prefers-reduced-motion motion rule is recorded with its @media context', async ({ browser }) => {
    const css = (d: string) => `${MOTION_CSS} @media (prefers-reduced-motion: reduce) { .box { transition-duration: ${d}; } }`
    const tree = await extractFixture(browser, frozen(css('var(--dur)')))
    expect(tree.motionRules).toContain('@media (prefers-reduced-motion: reduce) .box { transition-duration: 120ms }')
    expect(hashTree(await extractFixture(browser, frozen(css('1ms'))))).not.toBe(hashTree(tree))
  })

  test('the <html> element\'s own style counts (its background paints the canvas)', async ({ browser }) => {
    const a = await extractFixture(browser, { css: `${BASE_CSS} html { background: rgb(1, 2, 3); }` })
    const b = await extractFixture(browser, { css: `${BASE_CSS} html { background: rgb(1, 2, 4); }` })
    expect(a.styles[a.html.style]['background-color']).toBe('rgb(1, 2, 3)')
    expect(hashTree(b)).not.toBe(hashTree(a))
  })

  test('a color-scheme <meta> counts', async ({ browser }) => {
    const a = await extractFixture(browser, { head: '<meta name="color-scheme" content="dark">' })
    const b = await extractFixture(browser, { head: '<meta name="color-scheme" content="light">' })
    expect(a.html.colorScheme).toBe('dark')
    expect(hashTree(b)).not.toBe(hashTree(a))
  })
})

test.describe('attributes that change rendering without a computed style', () => {
  // [what, html, the attribute edit]: one row per attribute extract.ts adds
  // to ATTRS (or to svg) because no computed style shows it.
  const pairs: [string, string, string][] = [
    ['an SVG preserveAspectRatio', '<svg viewBox="0 0 10 20" width="10" height="10" preserveAspectRatio="none"><rect width="10" height="20"></rect></svg>', 'preserveAspectRatio="xMinYMin"'],
    ['an SVG pathLength', '<svg width="10" height="10"><path d="M0 0L10 10" pathLength="10" stroke-dasharray="1 1"></path></svg>', 'pathLength="20"'],
    ['a range input\'s min (same live value)', '<input type="range" min="0" max="100" value="50">', 'min="10"'],
    ['a range input\'s max (same live value)', '<input type="range" min="0" max="100" value="50">', 'max="200"'],
    ['a range input\'s step', '<input type="range" min="0" max="100" step="10" value="50">', 'step="25"'],
    ['a meter\'s low', '<meter min="0" max="10" low="2" high="8" optimum="5" value="6"></meter>', 'low="3"'],
    ['a meter\'s high', '<meter min="0" max="10" low="2" high="8" optimum="5" value="6"></meter>', 'high="7"'],
    ['a meter\'s optimum', '<meter min="0" max="10" low="2" high="8" optimum="5" value="6"></meter>', 'optimum="9"'],
    ['a meter\'s value (not a form control)', '<meter min="0" max="10" value="6"></meter>', 'value="7"'],
    ['a list item\'s value', '<ol><li value="4">a</li></ol>', 'value="5"'],
    ['an ordered list\'s start', '<ol start="2"><li>a</li></ol>', 'start="3"'],
    ['a textarea\'s rows', '<textarea rows="2"></textarea>', 'rows="3"'],
    ['a textarea\'s cols', '<textarea cols="20"></textarea>', 'cols="30"'],
    ['an input\'s size', '<input size="10">', 'size="20"'],
    ['a cell\'s colspan', '<table><tr><td colspan="1">a</td><td>b</td></tr></table>', 'colspan="2"'],
    ['a cell\'s rowspan', '<table><tr><td rowspan="1">a</td></tr><tr><td>b</td></tr></table>', 'rowspan="2"'],
    ['a column\'s span', '<table><colgroup><col span="1"></colgroup><tr><td>a</td><td>b</td></tr></table>', 'span="2"'],
    ['an image\'s sizes', '<img src="/i.png" srcset="/i.png 100w" sizes="50px" alt="">', 'sizes="60px"'],
  ]
  for (const [name, html, edit] of pairs) {
    test(`${name} changes the hash`, async ({ browser }) => {
      const attr = edit.split('=')[0]
      const changed = html.replace(new RegExp(`${attr}="[^"]*"`), edit)
      expect(changed).not.toBe(html)
      const a = await hashFixture(browser, { body: `${BASE_BODY}${html}` })
      expect(await hashFixture(browser, { body: `${BASE_BODY}${changed}` })).not.toBe(a)
    })
  }

  test('reversed is recorded as an attribute: present or not changes the hash', async ({ browser }) => {
    const list = (attr: string) => ({ body: `${BASE_BODY}<ol${attr}><li>a</li><li>b</li></ol>` })
    const reversed = await extractFixture(browser, list(' reversed'))
    expect(JSON.stringify(reversed)).toContain('"reversed":""')
    expect(hashTree(await extractFixture(browser, list('')))).not.toBe(hashTree(reversed))
  })

  test('every attribute of the second ATTRS row reaches attrs', async ({ browser }) => {
    const tree = await extractFixture(browser, {
      body: `${BASE_BODY}<input type="range" min="1" max="9" step="2" value="3" size="4"><meter low="1" high="2" optimum="3" value="1"></meter><textarea rows="2" cols="3"></textarea><ol start="2" reversed><li value="7">a</li></ol><table><colgroup><col span="2"></colgroup><tr><td colspan="2" rowspan="1">a</td></tr></table><img src="/i.png" srcset="/i.png 100w" sizes="50px" alt="">`,
    })
    const seen = new Set<string>()
    const walk = (n: Tree['root']) => {
      for (const k of Object.keys(n.attrs)) seen.add(k)
      n.children.forEach(walk)
    }
    walk(tree.root)
    for (const a of ['min', 'max', 'step', 'size', 'low', 'high', 'optimum', 'value', 'rows', 'cols', 'start', 'reversed', 'span', 'colspan', 'rowspan', 'sizes']) expect(seen, a).toContain(a)
  })

  test('an SVG element\'s generated id stays out of the tree', async ({ browser }) => {
    const svg = (id: string) => ({ body: `${BASE_BODY}<svg width="4" height="4"><rect id="${id}" width="4" height="4"></rect></svg>` })
    expect(hashTree(await extractFixture(browser, svg(':r7:')))).toBe(hashTree(await extractFixture(browser, svg(':r1:'))))
  })
})

test.describe('denylist: a change in a dropped property\'s design intent still changes the hash', () => {
  let base: string
  test.beforeAll(async ({ browser }) => {
    base = await hashFixture(browser)
  })
  for (const [prop, { reason, probe }] of Object.entries(DENYLIST)) {
    test(`${prop} (${reason})`, async ({ browser }) => {
      const tree = await extractFixture(browser, { css: `${BASE_CSS} .probe { ${probe}; }` })
      for (const style of tree.styles) expect(style).not.toHaveProperty(prop)
      expect(hashTree(tree)).not.toBe(base)
    })
  }
})

test.describe('prototype states', () => {
  test('s220 (the +N strip) extracts the same in five fresh pages, at every size', async ({ browser }) => {
    test.setTimeout(120_000)
    for (const size of SIZES) {
      const hashes = new Set<string>()
      for (let i = 0; i < 5; i++) hashes.add(hashTree(await extractState(browser, 220, size)))
      expect(hashes.size, `s220 at ${size}`).toBe(1)
    }
  })

  test('the +N pill has a fixed (px) computed width, not one that follows its text', async ({ browser }) => {
    const context = await newDesignContext(browser, 'large')
    try {
      const page = await context.newPage()
      await page.goto(`${protoUrl()}?bare&freeze&size=large#s220`)
      await waitForHarness(page)
      await openState(page, 220)
      const width = await page.locator('[data-testid="strip-more"]').first().evaluate((el) => String(el.computedStyleMap().get('width')))
      expect(width).toMatch(/^\d+(\.\d+)?px$/)
    } finally {
      await context.close()
    }
  })

  test('no URL, origin or path of this machine reaches a tree', async ({ browser }) => {
    const json = JSON.stringify(await extractState(browser, 202, 'large'))
    expect(json).not.toMatch(/127\.0\.0\.1|localhost|https?:\/\/|\/Users\/|\/home\/|file:/)
    expect(json).toMatch(/res:[0-9a-f]{16}/)
  })

  test('outside TYPED_PROPERTIES, getComputedStyle gives the computed value (s109, s202, s306)', async ({ browser }) => {
    for (const n of [109, 202, 306]) {
      const context = await newDesignContext(browser, 'large')
      try {
        const page = await context.newPage()
        await page.goto(`${protoUrl()}?bare&freeze&size=large#s${n}`)
        await waitForHarness(page)
        await openState(page, n)
        const skip = [...TYPED_PROPERTIES, ...Object.keys(DENYLIST)]
        const diffs = await page.evaluate((skip) => {
          const out: string[] = []
          for (const el of [document.body, ...Array.from(document.body.querySelectorAll('*'))]) {
            const resolved = getComputedStyle(el)
            for (const [prop, values] of el.computedStyleMap()) {
              if (prop.startsWith('--') || skip.includes(prop)) continue
              const typed = Array.from(values, String).join(', ')
              if (typed !== resolved.getPropertyValue(prop) && out.length < 10) out.push(`${prop}: typed "${typed}", resolved "${resolved.getPropertyValue(prop)}"`)
            }
          }
          return out
        }, skip)
        expect(diffs, `s${n}`).toEqual([])
      } finally {
        await context.close()
      }
    }
  })

  test('s109 extracts within the time budget (250 ms per variant: the fastest of 5 warm runs)', async ({ browser }) => {
    // Load on the machine only adds time, so the minimum is the extractor's
    // cost. On a machine busier than its cores (other jobs), even the minimum
    // is not the extractor's: skip there. In CI the runner is shared and
    // never skipped: the hard limit is 1000 ms, and a result between 250 and
    // 1000 ms is annotated as slow instead of failing (extractBudget.ts).
    const load = loadavg()[0]
    const cores = cpus().length
    if (!process.env.CI && load > cores) {
      test.info().annotations.push({ type: 'skipped: machine load', description: `1-minute load average ${load.toFixed(1)} > ${cores} cores` })
      test.skip(true, `machine load: 1-minute load average ${load.toFixed(1)} exceeds ${cores} cores`)
    }
    const context = await newDesignContext(browser, 'large')
    const cache = new Map<string, string>()
    const times: number[] = []
    try {
      for (let i = 0; i < 6; i++) {
        const page = await context.newPage()
        await page.goto(`${protoUrl()}?bare&freeze&size=large#s109`)
        await waitForHarness(page)
        await openState(page, 109)
        const timings: Record<string, number> = {}
        const started = performance.now()
        hashTree(await extractPage(page, { measured: MEASURED_DIGESTS, cache, timings }))
        if (i > 0) {
          times.push(performance.now() - started)
          console.log(`s109 large phases (ms): ${JSON.stringify(timings)}`)
        }
        await page.close()
      }
    } finally {
      await context.close()
    }
    times.sort((a, b) => a - b)
    console.log(`s109 large: min ${Math.round(times[0])} ms, median ${Math.round(times[2])} ms (${times.map((t) => Math.round(t)).join(', ')})`)
    const ci = !!process.env.CI
    if (budgetVerdict(times[0], ci) === 'slow') {
      test.info().annotations.push({
        type: 'slow',
        description: `s109 large: fastest warm extraction ${Math.round(times[0])} ms, over the ${BUDGET_MS} ms budget but within CI's ${CI_LIMIT_MS} ms limit`,
      })
    }
    expect(times[0]).toBeLessThanOrEqual(ci ? CI_LIMIT_MS : BUDGET_MS)
  })

  test('the prototype build is stamped with the current source digest', async () => {
    test.skip(!!process.env.DESIGN_NO_BUILD, 'DESIGN_NO_BUILD reuses an earlier build')
    const digest = execFileSync('python3', [join(FRONTEND, '..', 'tools', 'design', 'gate.py'), 'digest'], { encoding: 'utf8' }).trim()
    expect(JSON.parse(readFileSync(join(FRONTEND, 'dist-prototype', 'build.json'), 'utf8'))).toEqual({ sourceDigest: digest })
    const served = (await (await fetch(new URL('__build.json', protoUrl()))).json()) as { sourceDigest: string }
    expect(served.sourceDigest).toBe(digest)
  })
})
