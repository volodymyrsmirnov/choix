// App ↔ prototype parity (the `parity` project): one test per approved state
// (docs/design/approvals.json at the current state-hashes.json hash) in a
// group listed in enabled.json. Each test renders the prototype's #sN and the
// app driven by the state's case at every review size, and fails on the first
// difference between the two trees (parity.ts). With enabled.json `[]` it
// defines no tests (run with --pass-with-no-tests).
//
//   PARITY_APP_URL   the served app parity build (required once a group is enabled)
//
// A state without a case fails with "no parity case for #N"; a case with
// `skip` is skipped with its reason. An enabled group that plans nothing to
// compare (no state of it approved at its current hash, and not a group of
// skip entries only) gets one failing test named after it, so
// --pass-with-no-tests cannot pass quietly.
import { expect, test } from '@playwright/test'
import { protoUrl, SIZES } from '../../tools/designPaths'
import { CASES } from './cases'
import {
  appTree,
  appUrl,
  compareTrees,
  formatDiff,
  loadPlanInputs,
  planParity,
  prototypeTree,
  requireCase,
  silentGroupMessage,
  silentGroups,
  validateCases,
} from './parity'

validateCases(CASES)
const { enabled, registered, approved } = loadPlanInputs()
const planned = planParity(approved, enabled, CASES)

test.describe.configure({ mode: 'parallel' })

for (const group of silentGroups(registered, planned, enabled, CASES)) {
  test(`group ${group}`, () => {
    throw new Error(silentGroupMessage(group))
  })
}

// Titled `<group> #<n> <label>`, so `--grep <group>` runs one group (as in check-states).
for (const state of planned) {
  test(`${state.group} #${state.n} ${state.label}`, async ({ browser }) => {
    test.setTimeout(120_000)
    const c = requireCase(state)
    test.skip(c.skip !== undefined, c.skip)
    const app = appUrl()
    for (const size of SIZES) {
      const [appSide, protoSide] = await Promise.all([appTree(browser, app, c, size), prototypeTree(browser, protoUrl(), state.n, size)])
      const diff = compareTrees(appSide, protoSide)
      expect(diff, diff ? `#${state.n} at ${size}: ${formatDiff(diff)}` : '').toBeNull()
    }
  })
}
