// Self-test of the parity comparison (parity.ts), in the `parity` project,
// on the test-only fixture page (tools/fixtures, states in PARITY). Both
// sides are rendered and extracted exactly as parity.pw.ts renders the
// prototype and the app: same extractor, same normalisation, same
// comparison. The "app" of the hover test is a fixture state at rest whose
// tile gets a real mouse hover through a case's `interact`. The "app" of the
// render-mode test opens the fixture without ?freeze, as the app opens, and
// gets the prototype's render mode through scriptedRender, as appTree does.
import { expect, test } from '@playwright/test'
import { fixturesUrl } from '../../tools/designPaths'
import { PARITY, PARITY_HOVER_TARGET } from '../../tools/fixtures/kinds'
import { newDesignContext, openState, waitForHarness } from '../../tools/stateRunner'
import { compareTrees, parityTree, prototypeTree, runCase, scriptedRender, type ParityCase } from './parity'

test.describe.configure({ mode: 'parallel' })

test('a page opened without ?freeze, given scriptedRender, equals the prototype', async ({ browser }) => {
  const context = await newDesignContext(browser, 'default')
  try {
    const render = async (scripted: boolean) => {
      const page = await context.newPage()
      await page.goto(`${fixturesUrl()}?bare&size=default#s${PARITY.base}`)
      await waitForHarness(page)
      await openState(page, PARITY.base)
      if (await page.evaluate(() => document.documentElement.hasAttribute('data-freeze'))) throw new Error('the page set data-freeze itself')
      if (scripted) await scriptedRender(page)
      await runCase(page, { state: PARITY.base })
      return parityTree(page)
    }
    const proto = await prototypeTree(browser, fixturesUrl(), PARITY.base, 'default')
    // Without the render mode every element's animation / transition values
    // differ (base.css's reduced-motion collapse instead of the freeze rule)…
    expect(compareTrees(await render(false), proto)).toMatchObject({ field: expect.stringMatching(/^style\.(animation|transition)-/) })
    // …and with it the pages are equal.
    expect(compareTrees(await render(true), proto)).toBeNull()
  } finally {
    await context.close()
  }
})

test('one padding differs: the first difference names that node and style.padding-left', async ({ browser }) => {
  const [app, proto] = await Promise.all([
    prototypeTree(browser, fixturesUrl(), PARITY.padding, 'default'),
    prototypeTree(browser, fixturesUrl(), PARITY.base, 'default'),
  ])
  const diff = compareTrees(app, proto)
  expect(diff).toEqual({
    path: expect.stringMatching(/ > main\.fx-parity > ul\.fx-parity-list > li\.fx-parity-row:nth-child\(3\)$/),
    field: 'style.padding-left',
    app: '13px',
    prototype: '12px',
  })
})

test('identical pages: no difference', async ({ browser }) => {
  const [a, b] = await Promise.all([
    prototypeTree(browser, fixturesUrl(), PARITY.base, 'default'),
    prototypeTree(browser, fixturesUrl(), PARITY.base, 'default'),
  ])
  expect(compareTrees(a, b)).toBeNull()
  // The prototype's root fills the window from its corner and the document
  // does not scroll: what an app root that fills the window must match.
  expect(a.styles[a.root.style]).toMatchObject({ width: '1280px', height: '800px', 'root:x': '0px', 'root:y': '0px', 'root:document-scrolls': 'none' })
})

test('a real hover equals the same page with data-force="hover"', async ({ browser }) => {
  const hover: ParityCase = { state: PARITY.forcedHover, interact: (page) => page.hover(PARITY_HOVER_TARGET) }
  const context = await newDesignContext(browser, 'default')
  try {
    // The "app": the page at rest, then a real hover through the case.
    const render = async (c: ParityCase) => {
      const page = await context.newPage()
      await page.goto(`${fixturesUrl()}?bare&freeze&size=default#s${PARITY.base}`)
      await waitForHarness(page)
      await openState(page, PARITY.base)
      await runCase(page, c)
      return parityTree(page)
    }
    const forced = await prototypeTree(browser, fixturesUrl(), PARITY.forcedHover, 'default')
    const atRest = await render({ state: PARITY.forcedHover })
    const hovered = await render(hover)

    // The forced look is a look: without the hover the pages differ…
    expect(compareTrees(atRest, forced)).toMatchObject({ field: 'style.background-color', app: 'rgb(42, 42, 42)', prototype: 'rgb(64, 64, 64)' })
    // …and with it they are equal, data-force and its CSS twins included.
    expect(compareTrees(hovered, forced)).toBeNull()
  } finally {
    await context.close()
  }
})
