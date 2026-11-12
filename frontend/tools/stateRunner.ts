// Checks one prototype state at one size in one browser: the orchestration
// behind check-states.pw.ts and the self-tests in checks.pw.ts. The checks
// themselves live in tools/checks/, one per file.
//
// For each wrap mode (`?bare&freeze`, then `?bare&freeze&nowrapfx`) a fresh
// page opens the state and waits for __choixProto.open(n). Each pass first
// proves its wrap mode with a probe (text-wrap: balance must compute
// text-wrap-style balance, then auto under nowrapfx; otherwise a `harness`
// finding). Both passes run the layout checks (root overflow, clipping,
// occlusion, orphans, alignment, centring, the state's subject in view) and watch the console and the
// network. The first pass also writes the
// screenshot and runs the interactive checks, each on a freshly reopened
// state: Escape on the top overlay and keyboard focus (Chromium), and the
// forced looks (gallery states). Anything that throws becomes a `harness`
// finding of the state instead of aborting the run.
import type { Browser, BrowserContext, Page } from '@playwright/test'
import { runAlign } from './checks/align'
import { runCentred } from './checks/centred'
import { runClip } from './checks/clip'
import { installDom, withCtx, type Finding, type Raw } from './checks/dom'
import { escapeCheck, focusCheck, pointerOnlyCheck } from './checks/focus'
import { forcedLookCheck } from './checks/forced'
import { blockExternal, watchPage } from './checks/network'
import { runOcclusion } from './checks/occlusion'
import { runOrphans } from './checks/orphans'
import { runSubject } from './checks/subject'
import { FIXED_NOW, SIZE_PX, type Size } from './designPaths'

export interface StateInfo {
  n: number
  group: string
  label: string
  overlays: string[]
  /** A selector for what the state's label names; it must be in view. */
  subject?: string
}

export interface CheckOpts {
  baseUrl: string
  state: StateInfo
  size: Size
  browser: string
  /** Where to write the first pass's screenshot. */
  screenshot?: string
  /** Run only these checks (plus the console and network watch). */
  only?: string[]
}

/** A context for scripted renders: the size's viewport, scale 1, UTC, en-US, reduced motion, fixed clock, no external requests. */
export async function newDesignContext(browser: Browser, size: Size): Promise<BrowserContext> {
  const [width, height] = SIZE_PX[size]
  const context = await browser.newContext({
    viewport: { width, height },
    deviceScaleFactor: 1,
    timezoneId: 'UTC',
    locale: 'en-US',
    colorScheme: 'dark',
    reducedMotion: 'reduce',
  })
  await context.clock.setFixedTime(FIXED_NOW)
  await blockExternal(context)
  return context
}

/** How long a page may take to install window.__choixProto. */
export const HARNESS_TIMEOUT_MS = 15_000

export async function waitForHarness(page: Page): Promise<void> {
  await page.waitForFunction(() => !!window.__choixProto, undefined, { timeout: HARNESS_TIMEOUT_MS })
}

/**
 * The computed text-wrap-style of a probe asking for `balance`: `balance` in
 * the first pass, `auto` in the nowrapfx pass, or that pass proves nothing.
 */
function wrapProbe(): string {
  const probe = document.createElement('div')
  probe.style.setProperty('text-wrap', 'balance')
  probe.textContent = 'probe'
  window.__checkDom!.root().appendChild(probe)
  const style = getComputedStyle(probe).getPropertyValue('text-wrap-style')
  probe.remove()
  return style
}

/** The states the page at `baseUrl` registers (window.__choixProto.states). */
export async function listStates(context: BrowserContext, baseUrl: string): Promise<StateInfo[]> {
  const page = await context.newPage()
  try {
    await page.goto(`${baseUrl}?bare&freeze`)
    await waitForHarness(page)
    return await page.evaluate(() => window.__choixProto!.states.map((s) => ({ ...s, overlays: [...s.overlays] })))
  } finally {
    await page.close()
  }
}

/** Opens state `n` again with a fresh store and waits until it is ready. */
export async function openState(page: Page, n: number): Promise<void> {
  await page.evaluate((n) => window.__choixProto!.open(n), n)
}

export async function checkState(context: BrowserContext, o: CheckOpts): Promise<Finding[]> {
  const n = o.state.n
  const want = (check: string) => !o.only || o.only.includes(check)
  const findings: Finding[] = []
  for (const nowrap of [false, true]) {
    const page = await context.newPage()
    const watch = watchPage(page)
    const raw: Raw[] = []
    try {
      await page.goto(`${o.baseUrl}?bare&freeze${nowrap ? '&nowrapfx' : ''}&size=${o.size}#s${n}`)
      await waitForHarness(page)
      try {
        await openState(page, n)
      } catch (e) {
        raw.push({ check: 'harness', message: `not ready: ${(e as Error).message}` })
        continue
      }
      await installDom(page)
      const wrap = await page.evaluate(wrapProbe)
      const wantWrap = nowrap ? 'auto' : 'balance'
      if (wrap !== wantWrap) {
        raw.push({ check: 'harness', message: `a text-wrap: balance probe computes text-wrap-style "${wrap}", not "${wantWrap}": the nowrapfx pass would not differ` })
      }
      if (want('root-overflow') || want('clip')) raw.push(...(await runClip(page)).filter((r) => want(r.check)))
      if (want('occlusion')) raw.push(...(await runOcclusion(page)))
      if (want('orphan')) raw.push(...(await runOrphans(page)))
      if (want('align')) raw.push(...(await runAlign(page)))
      if (want('centred')) raw.push(...(await runCentred(page)))
      if (want('subject')) raw.push(...(await runSubject(page, o.state.subject ?? null)))
      if (nowrap) continue

      if (o.screenshot) await page.screenshot({ path: o.screenshot, animations: 'disabled' })
      const reopen = () => openState(page, n)
      if (want('focus')) raw.push(...(await pointerOnlyCheck(page)))
      if (want('focus') && o.state.overlays.length > 0) raw.push(...(await escapeCheck(page)))
      if (want('focus') && o.browser === 'chromium') {
        await reopen()
        raw.push(...(await focusCheck(page, reopen)))
      }
      if (want('forced-look') && o.state.group === 'gallery') {
        await reopen()
        raw.push(...(await forcedLookCheck(page, reopen)))
      }
    } catch (e) {
      // A crashed page or a failed evaluate is a finding, not an aborted run.
      raw.push({ check: 'harness', message: `the checks could not run: ${(e as Error).message.split('\n')[0]}` })
    } finally {
      // Let late console and network events of this page arrive.
      await page.waitForTimeout(0).catch(() => {})
      raw.push(...watch.findings())
      await page.close().catch(() => {})
      const pass = nowrap ? ' [nowrapfx]' : ''
      findings.push(...withCtx({ state: n, size: o.size, browser: o.browser }, raw.map((r) => ({ ...r, message: r.message + pass }))))
    }
  }
  return findings
}
