// Runs one group's flows on a fresh page and judges them: the flow must not
// throw, the page must log no console or page error and make no failed or
// external request while the flow runs, no click may lead to a state the
// prototype does not have (the "not in the prototype yet" placeholder), and
// every overlay of the group (see flowCoverage.ts) must have been opened and
// closed by a user action while its state was shown. Used by flows.pw.ts and by the self-tests in
// checks.pw.ts.
//
// Leaving a state is not closing its overlays: the prototype's recorder
// already ignores the unmount of a state that is being replaced, and the
// handle's open() additionally withholds credit from every close recorded
// while it navigates.
import type { BrowserContext, Page } from '@playwright/test'
import { watchPage } from './checks/network'
import { overlayCoverage } from './flowCoverage'
import type { ProtoHandle } from './flows.pw'
import { HARNESS_TIMEOUT_MS, type StateInfo } from './stateRunner'

export type Flow = (page: Page, proto: ProtoHandle) => Promise<void>

export interface FlowFailure {
  group: string
  message: string
}

const readLog = (page: Page) =>
  page.evaluate(() => {
    const o = window.__choixProto!.overlays
    return { registered: [...o.registered], opened: [...o.opened], closed: [...o.closed] }
  })

/**
 * The page-side harness (window.__choixProto), driven from Node. Its
 * overlays() leaves out closes recorded while open() navigated.
 */
export function protoHandle(page: Page): ProtoHandle {
  const byNavigation = new Set<string>()
  return {
    async open(n) {
      const before = (await readLog(page)).closed
      await page.evaluate((n) => window.__choixProto!.open(n), n)
      for (const id of (await readLog(page)).closed) if (!before.includes(id)) byNavigation.add(id)
    },
    setToggles: (t) => page.evaluate((t) => window.__choixProto!.setToggles(t), t),
    async overlays() {
      const log = await readLog(page)
      return { ...log, closed: log.closed.filter((id) => !byNavigation.has(id)) }
    },
  }
}

type MissingLog = { __choixMissing?: string[] }

/** Records the text of every "no such state" placeholder that mounts from now on. */
const watchMissing = (page: Page) =>
  page.evaluate(() => {
    const seen: string[] = []
    ;(window as MissingLog).__choixMissing = seen
    new MutationObserver((records) => {
      for (const r of records) {
        for (const node of Array.from(r.addedNodes)) {
          if (!(node instanceof Element)) continue
          const el = node.matches('[data-proto-missing]') ? node : node.querySelector('[data-proto-missing]')
          if (el) seen.push(el.textContent ?? '')
        }
      }
    }).observe(document.body, { childList: true, subtree: true })
  })

const readMissing = (page: Page) => page.evaluate(() => [...((window as MissingLog).__choixMissing ?? [])])

export interface FlowRun {
  failures: FlowFailure[]
  /** The group's states (whose declared overlays were required). */
  states: StateInfo[]
}

/** Loads the page at `?bare&freeze` on no state, runs `flow`, and returns what failed. */
export async function runGroupFlow(context: BrowserContext, baseUrl: string, group: string, flow: Flow): Promise<FlowFailure[]> {
  return (await runGroupFlowDetailed(context, baseUrl, group, flow)).failures
}

export async function runGroupFlowDetailed(context: BrowserContext, baseUrl: string, group: string, flow: Flow): Promise<FlowRun> {
  const page = await context.newPage()
  const failures: FlowFailure[] = []
  const fail = (message: string) => failures.push({ group, message })
  try {
    // #s0 is no state: nothing mounts, so nothing registers before the flow.
    await page.goto(`${baseUrl}?bare&freeze#s0`)
    await page.waitForFunction(() => !!window.__choixProto, undefined, { timeout: HARNESS_TIMEOUT_MS })
    const states = (await page.evaluate(() => window.__choixProto!.states)).filter((s) => s.group === group)
    if (states.length === 0) {
      fail(`no ${group} states to run flows on`)
      return { failures, states: [] }
    }
    const proto = protoHandle(page)
    const watch = watchPage(page)
    await watchMissing(page)
    try {
      await flow(page, proto)
    } catch (e) {
      fail(`the flow failed: ${(e as Error).message.split('\n')[0]}`)
    }
    await page.waitForTimeout(0)
    for (const r of watch.findings()) fail(`${r.check}: ${r.message}`)
    for (const text of await readMissing(page)) fail(`a click led to a state the prototype does not have: ${text}`)
    const declared = states.flatMap((s) => s.overlays)
    for (const message of overlayCoverage(declared, await proto.overlays())) fail(message)
    return { failures, states: states.map((s) => ({ ...s, overlays: [...s.overlays] })) }
  } finally {
    await page.close()
  }
}
