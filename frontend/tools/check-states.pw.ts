// design:check-states [--grep <group>]: every state of the built prototype ×
// size × browser (the checks-chromium and checks-webkit projects), in both
// wrap modes, through the checks in tools/checks/ (see stateRunner.ts). One
// test per group and size, titled `<group> <size>`, so `--grep library` picks
// a group. Writes .design/screenshots/<browser>/<size>/s<N>.png and a contact
// sheet per group, size and browser (contact-<group>.png next to them).
//
// Nothing a state does stops the run: a state whose checks throw becomes a
// `harness` finding, the other states are still checked, and the test's
// result file is always written, so the summary never omits a group.
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import { GROUPS } from '../prototype/registry'
import type { Finding } from './checks/dom'
import { writeContactSheet } from './contactSheet'
import { protoUrl, resultsDir, SCREENSHOTS, SIZES } from './designPaths'
import { writeResult } from './results'
import { checkState, listStates, newDesignContext, type StateInfo } from './stateRunner'

const format = (f: Finding) => `  s${f.state} ${f.size} ${f.browser} [${f.check}] ${f.message}${f.selector ? `\n      at ${f.selector}` : ''}`
const firstLine = (e: unknown) => (e instanceof Error ? e.message : String(e)).split('\n')[0]

for (const group of GROUPS) {
  for (const size of SIZES) {
    test(`${group.id} ${size}`, async ({ browser, browserName }) => {
      test.setTimeout(15 * 60_000)
      const harness = (state: number, message: string): Finding => ({ check: 'harness', state, size, browser: browserName, message })
      const context = await newDesignContext(browser, size)
      const findings: Finding[] = []
      let states: StateInfo[] = []
      try {
        states = (await listStates(context, protoUrl())).filter((s) => s.group === group.id)
      } catch (e) {
        findings.push(harness(0, `cannot list the states: ${firstLine(e)}`))
      }
      if (findings.length === 0 && states.length === 0) {
        await context.close()
        test.skip(true, `no ${group.id} states yet`)
        return
      }
      try {
        const dir = join(SCREENSHOTS, browserName, size)
        mkdirSync(dir, { recursive: true })
        for (const state of states) {
          const screenshot = join(dir, `s${state.n}.png`)
          try {
            findings.push(...(await checkState(context, { baseUrl: protoUrl(), state, size, browser: browserName, screenshot })))
          } catch (e) {
            findings.push(harness(state.n, `the state could not be checked: ${firstLine(e)}`))
          }
        }
        try {
          await writeContactSheet(context, {
            title: `${group.title} · ${size} · ${browserName}`,
            shots: states.map((s) => ({ n: s.n, label: s.label, file: join(dir, `s${s.n}.png`) })),
            out: join(dir, `contact-${group.id}.png`),
          })
        } catch (e) {
          findings.push(harness(0, `cannot write the contact sheet: ${firstLine(e)}`))
        }
      } finally {
        writeResult(resultsDir(), 'check-states', `${browserName}-${group.id}-${size}`, {
          browser: browserName,
          group: group.id,
          size,
          states: states.map((s) => s.n),
          renders: states.length * 2,
          failures: findings,
        })
        await context.close()
      }
      expect(findings.length, `${findings.length} failures:\n${findings.map(format).join('\n')}`).toBe(0)
    })
  }
}
