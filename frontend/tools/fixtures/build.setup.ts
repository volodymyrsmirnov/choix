// The `fixtures` setup project: builds the test-only broken-states page into
// .design/fixtures-dist/ once per run. Only the self-test projects
// (selftest-chromium, selftest-webkit) depend on it, so design:check-states
// and design:flows never pay for it. DESIGN_NO_BUILD=1 reuses the last build.
import { test } from '@playwright/test'
import { run } from '../design-setup'

test('build the broken-states fixture page', () => {
  test.setTimeout(120_000)
  if (process.env.DESIGN_NO_BUILD) return
  run('fixture build', 'bunx', ['vite', 'build', '--config', 'tools/fixtures/vite.config.ts', '--logLevel', 'error'])
})
