// design:flows [--grep <group>]: for every group that has states, runs
// tools/flows/<group>.ts on a fresh Chromium page of the built prototype
// (`?bare&freeze`, default size). A group fails when its flow is missing or
// throws, when the page logs an error or makes a failed or external request,
// or when an overlay of the group was not both opened and closed (see
// flowCoverage.ts). Each flow module:
//
//   export default async function (page: Page, proto: ProtoHandle): Promise<void>
import { existsSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import { GROUPS } from '../prototype/registry'
import { FRONTEND, protoUrl, resultsDir } from './designPaths'
import { runGroupFlowDetailed, type Flow } from './flowRunner'
import { writeResult } from './results'
import { listStates, newDesignContext } from './stateRunner'

/** The page-side harness (window.__choixProto), as the flows drive it from Node. */
export interface ProtoHandle {
  /** Opens state `n` with a fresh store; resolves when it is ready. */
  open(n: number): Promise<void>
  setToggles(t: { size?: 'min' | 'default' | 'large'; freeze?: boolean; nowrapfx?: boolean }): Promise<void>
  /** The page's overlay log so far. */
  overlays(): Promise<{ registered: string[]; opened: string[]; closed: string[] }>
}

for (const group of GROUPS) {
  test(group.id, async ({ browser, browserName }) => {
    test.setTimeout(120_000)
    const file = join(FRONTEND, 'tools', 'flows', `${group.id}.ts`)
    const context = await newDesignContext(browser, 'default')
    try {
      const states = (await listStates(context, protoUrl())).filter((s) => s.group === group.id)
      test.skip(states.length === 0, `no ${group.id} states yet`)
      let flow: Flow | null = null
      if (existsSync(file)) flow = ((await import(`./flows/${group.id}.ts`)) as { default: Flow }).default
      const run = flow
        ? await runGroupFlowDetailed(context, protoUrl(), group.id, flow)
        : { states, failures: [{ group: group.id, message: `tools/flows/${group.id}.ts is missing` }] }
      writeResult(resultsDir(), 'flows', group.id, {
        browser: browserName,
        group: group.id,
        size: 'default',
        states: run.states.map((s) => s.n),
        renders: 0,
        failures: run.failures.map((f) => ({ check: 'flow', state: 0, size: 'default', browser: browserName, message: f.message })),
      })
      expect(run.failures.map((f) => f.message), `${group.id} flows`).toEqual([])
    } finally {
      await context.close()
    }
  })
}
