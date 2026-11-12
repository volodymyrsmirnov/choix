// Self-tests of the state checks and the flow runner, against the broken
// fixture page (tools/fixtures, a separate test-only build) and the real
// prototype. Runs in the selftest-chromium and selftest-webkit projects:
//   task design:selftest
import { mkdtempSync, readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import type { Finding } from './checks/dom'
import { writeContactSheet } from './contactSheet'
import { FIXTURES_DIST, fixturesUrl, PROTO_DIST, protoUrl } from './designPaths'
import { runGroupFlow } from './flowRunner'
import { BROKEN, checkOf, CLEAN, CLOSABLE_OVERLAY, FIXTURE_MARKER, PARITY, STUCK_OVERLAY } from './fixtures/kinds'
import { checkState, listStates, newDesignContext, waitForHarness } from './stateRunner'

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? files(p) : [p]
  })
}

const bundleHas = (dir: string, needle: string) =>
  files(dir).some((f) => /\.(js|css|html)$/.test(f) && readFileSync(f, 'utf8').includes(needle))

test('the prototype build holds no fixture code', () => {
  expect(bundleHas(FIXTURES_DIST, FIXTURE_MARKER), 'the marker must be in the fixture bundle, or this test proves nothing').toBe(true)
  expect(bundleHas(PROTO_DIST, FIXTURE_MARKER)).toBe(false)
  expect(bundleHas(PROTO_DIST, 'Supercalifragilisticexpialidocious')).toBe(false)
})

test('each check reports exactly its own broken state, and the clean states pass', async ({ browser, browserName }) => {
  test.setTimeout(120_000)
  const context = await newDesignContext(browser, 'default')
  const states = await listStates(context, fixturesUrl())
  const findings: Finding[] = []
  for (const s of states) {
    findings.push(
      ...(await checkState(context, { baseUrl: fixturesUrl(), state: s, size: 'default', browser: browserName })),
    )
  }
  await context.close()

  // Tab and focus rings are Chromium-only; Escape on the top overlay runs in both.
  const chromiumOnly = new Set<string>(['focus-unreachable', 'focus-ring', 'focus-auto-ring'])
  const expected: Record<string, number[]> = {}
  for (const [kind, n] of Object.entries(BROKEN) as [keyof typeof BROKEN, number][]) {
    const check = checkOf(kind)
    if (chromiumOnly.has(kind) && browserName !== 'chromium') continue
    expected[check] = [...(expected[check] ?? []), n].sort((a, b) => a - b)
  }
  const actual: Record<string, number[]> = {}
  for (const f of findings) {
    const list = actual[f.check] ?? []
    if (!list.includes(f.state)) actual[f.check] = [...list, f.state].sort((a, b) => a - b)
  }
  const report = findings.map((f) => `s${f.state} ${f.check}: ${f.message}${f.selector ? ` @ ${f.selector}` : ''}`).join('\n')
  expect(actual, report).toEqual(expected)

  // The focus fixtures fail for the right reasons.
  if (browserName === 'chromium') {
    const focus = findings.filter((f) => f.check === 'focus')
    expect(focus.filter((f) => f.state === BROKEN['focus-unreachable']).map((f) => f.selector)).toEqual([
      expect.stringContaining('Unreachable'),
    ])
    expect(focus.find((f) => f.state === BROKEN['focus-ring'])?.message).toContain('no visible focus indicator')
    expect(focus.find((f) => f.state === BROKEN['focus-auto-ring'])?.message).toContain('outline-style: auto')
  }
  const escape = (n: number) => findings.find((f) => f.check === 'focus' && f.state === n)?.message
  expect(escape(BROKEN['focus-escape'])).toContain(`"${STUCK_OVERLAY}"`)
  expect(escape(BROKEN['focus-escape-covered'])).toContain('another overlay covers')
  const subject = findings.filter((f) => f.check === 'subject' && f.state === BROKEN.subject).map((f) => f.message)
  expect(subject.some((m) => m.includes('not fully in view'))).toBe(true)
  expect(subject.some((m) => m.includes('not visible (opacity 0'))).toBe(true)
  const pointerOnly = findings.filter((f) => f.check === 'focus' && f.state === BROKEN['focus-pointer-only']).map((f) => f.message)
  expect(pointerOnly.some((m) => m.includes('must not be focusable'))).toBe(true)
  expect(pointerOnly.some((m) => m.includes('needs a reason'))).toBe(true)
  for (const n of [...Object.values(CLEAN), ...Object.values(PARITY)]) expect(findings.filter((f) => f.state === n)).toEqual([])
})

test('s102 passes every check at every size', async ({ browser, browserName }) => {
  test.setTimeout(120_000)
  const findings: Finding[] = []
  for (const size of ['min', 'default', 'large'] as const) {
    const context = await newDesignContext(browser, size)
    const [s102] = (await listStates(context, protoUrl())).filter((s) => s.n === 102)
    expect(s102).toBeDefined()
    findings.push(...(await checkState(context, { baseUrl: protoUrl(), state: s102!, size, browser: browserName })))
    await context.close()
  }
  expect(findings).toEqual([])
})

test('switch thumbs sit 1 px inside the track: centred, at the left edge off, at the right edge on', async ({ browser }) => {
  // A dedicated assertion (not data-inset-pair): the centred check holds the
  // thumb's vertical centre; this pins both horizontal ends in gallery s803,
  // which shows every Switch look off and on.
  const context = await newDesignContext(browser, 'default')
  const page = await context.newPage()
  await page.goto(`${protoUrl()}?bare&freeze#s803`)
  await waitForHarness(page)
  await page.evaluate(() => window.__choixProto!.open(803))
  const gaps = await page.evaluate(() =>
    Array.from(document.querySelectorAll<HTMLElement>('[data-app-root] [role="switch"]')).map((track) => {
      const thumb = track.querySelector('span')!.getBoundingClientRect()
      const t = track.getBoundingClientRect()
      const cs = getComputedStyle(track)
      const inner = {
        left: t.left + parseFloat(cs.borderLeftWidth),
        right: t.right - parseFloat(cs.borderRightWidth),
        top: t.top + parseFloat(cs.borderTopWidth),
        bottom: t.bottom - parseFloat(cs.borderBottomWidth),
      }
      const on = track.getAttribute('aria-checked') === 'true'
      return {
        on,
        edge: on ? inner.right - thumb.right : thumb.left - inner.left,
        top: thumb.top - inner.top,
        bottom: inner.bottom - thumb.bottom,
      }
    }),
  )
  await context.close()
  expect(gaps.some((g) => g.on) && gaps.some((g) => !g.on), 'gallery s803 shows switches off and on').toBe(true)
  for (const g of gaps) {
    expect(g.edge).toBeCloseTo(1, 1)
    expect(g.top).toBeCloseTo(1, 1)
    expect(g.bottom).toBeCloseTo(1, 1)
  }
})

test('a row scrolled out of a scroller is not an occlusion', async ({ browser, browserName }) => {
  const context = await newDesignContext(browser, 'default')
  const page = await context.newPage()
  await page.goto(`${fixturesUrl()}?bare&freeze#s${CLEAN.scroller}`)
  await waitForHarness(page)
  await page.evaluate((n) => window.__choixProto!.open(n), CLEAN.scroller)
  // Not vacuous: a hidden row's centre is inside the viewport, over the footer.
  const hidden = await page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll('[data-fx-row]'))
    return rows.filter((row) => {
      const r = row.getBoundingClientRect()
      const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2)
      return r.top + r.height / 2 < window.innerHeight && hit !== null && !row.contains(hit)
    }).length
  })
  expect(hidden).toBeGreaterThan(0)
  await context.close()

  const fresh = await newDesignContext(browser, 'default')
  const [scroller] = (await listStates(fresh, fixturesUrl())).filter((s) => s.n === CLEAN.scroller)
  const findings = await checkState(fresh, { baseUrl: fixturesUrl(), state: scroller!, size: 'default', browser: browserName, only: ['occlusion'] })
  await fresh.close()
  expect(findings).toEqual([])
})

const stuck = (failures: { message: string }[]) => failures.map((f) => f.message).filter((m) => m.includes(`"${STUCK_OVERLAY}"`))

test('a flow that opens an overlay and never closes it fails naming it', async ({ browser, browserName }) => {
  test.skip(browserName !== 'chromium', 'flows run in Chromium')
  const context = await newDesignContext(browser, 'default')
  const failures = await runGroupFlow(context, fixturesUrl(), 'gallery', async (page, proto) => {
    await proto.open(CLEAN.closable)
    await page.keyboard.press('Escape')
    await proto.open(BROKEN['focus-escape'])
  })
  await context.close()
  expect(stuck(failures)).toEqual([`overlay "${STUCK_OVERLAY}" was opened but never closed`])
  expect(failures.map((f) => f.message).join('\n')).not.toContain(`"${CLOSABLE_OVERLAY}"`)

  const ok = await newDesignContext(browser, 'default')
  const none = await runGroupFlow(ok, fixturesUrl(), 'gallery', async (page, proto) => {
    await proto.open(CLEAN.closable)
    await page.keyboard.press('Escape')
  })
  await ok.close()
  expect(stuck(none)).toEqual([`overlay "${STUCK_OVERLAY}" was never opened or closed`])
  expect(none.map((f) => f.message).join('\n')).not.toContain(`"${CLOSABLE_OVERLAY}"`)
})

test('navigating away from an open overlay does not count as closing it', async ({ browser, browserName }) => {
  test.skip(browserName !== 'chromium', 'flows run in Chromium')
  const context = await newDesignContext(browser, 'default')
  const failures = await runGroupFlow(context, fixturesUrl(), 'gallery', async (page, proto) => {
    await proto.open(BROKEN['focus-escape'])
    // Leaves the state with fx-stuck open (and fx-menu, by a state change
    // inside the flow, closed by a user action).
    await proto.open(CLEAN.closable)
    await page.keyboard.press('Escape')
    await proto.open(CLEAN.scroller)
  })
  await context.close()
  expect(stuck(failures)).toEqual([`overlay "${STUCK_OVERLAY}" was opened but never closed`])
  expect(failures.map((f) => f.message).join('\n')).not.toContain(`"${CLOSABLE_OVERLAY}"`)
})

test('a click that leads to a state the prototype does not have fails the flow', async ({ browser, browserName }) => {
  test.skip(browserName !== 'chromium', 'flows run in Chromium')
  const context = await newDesignContext(browser, 'default')
  const failures = await runGroupFlow(context, fixturesUrl(), 'gallery', async (page, proto) => {
    await proto.open(CLEAN.closable)
    // What a wired callback does: store.go(n) for an n with no state.
    await page.evaluate(() => {
      window.location.hash = '#s12345'
    })
    await page.locator('[data-proto-missing]').waitFor()
  })
  await context.close()
  expect(failures.map((f) => f.message).filter((m) => m.startsWith('a click led to'))).toEqual([
    'a click led to a state the prototype does not have: s12345 is not a fixture state.',
  ])
})

test('a contact sheet holds every screenshot, or the run fails', async ({ browser }) => {
  const dir = mkdtempSync(join(tmpdir(), 'choix-sheet-'))
  const context = await newDesignContext(browser, 'default')
  const page = await context.newPage()
  const shots = []
  for (const [n, colour] of [[1, '#c33'], [2, '#3c3'], [3, '#33c'], [4, '#cc3'], [5, '#3cc'], [6, '#c3c'], [7, '#888']] as const) {
    await page.setContent(`<body style="margin:0;background:${colour}"><p style="color:#fff;font:40px sans-serif;margin:40px">s${n}</p></body>`)
    const file = join(dir, `s${n}.png`)
    await page.screenshot({ path: file })
    shots.push({ n, label: `State ${n}`, file })
  }
  await page.close()

  // Below the fold too: the seventh tile starts under the 900 px viewport the old full-page capture used.
  const out = join(dir, 'contact.png')
  await writeContactSheet(context, { title: 'Self-test', shots, out })
  const png = readFileSync(out)
  expect(png.subarray(1, 4).toString()).toBe('PNG')
  expect(png.readUInt32BE(16)).toBe(1640)
  expect(png.readUInt32BE(20)).toBeGreaterThan(900)

  const broken = join(dir, 'broken.png')
  writeFileSync(broken, 'not a png')
  await expect(writeContactSheet(context, { title: 'Broken', shots: [...shots, { n: 8, label: 'Broken', file: broken }], out })).rejects.toThrow()
  await context.close()
})
