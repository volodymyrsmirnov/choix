import { defineConfig } from '@playwright/test'

// Browser-driven design tooling. Every artefact a run writes lands under
// .design/ (git-ignored), never in a gated path. The global setup builds the
// prototype and serves it (and the fixture page) on ephemeral ports; see
// tools/design-setup.ts. The test-only fixture page is built by the
// `fixtures` setup project, which only the self-tests (and `parity`, whose
// self-test runs on it) depend on.
const shared = {
  viewport: { width: 1280, height: 800 },
  deviceScaleFactor: 1,
  timezoneId: 'UTC',
  locale: 'en-US',
  colorScheme: 'dark' as const,
  contextOptions: { reducedMotion: 'reduce' as const },
}
const chromium = { ...shared, browserName: 'chromium' as const }
const webkit = { ...shared, browserName: 'webkit' as const }

// The state checks and their self-tests run in both engines.
const CHECKS = '**/tools/check-states.pw.ts'
const SELFTEST = '**/tools/checks.pw.ts'

export default defineConfig({
  testDir: '.',
  testIgnore: ['**/node_modules/**'],
  outputDir: '.design/test-results',
  globalSetup: './tools/design-setup.ts',
  reporter: [
    ['list'],
    ['html', { outputFolder: '.design/playwright-report', open: 'never' }],
  ],
  forbidOnly: !!process.env.CI,
  projects: [
    { name: 'checks-chromium', testMatch: CHECKS, fullyParallel: true, use: chromium },
    { name: 'checks-webkit', testMatch: CHECKS, fullyParallel: true, use: webkit },
    { name: 'fixtures', testMatch: '**/tools/fixtures/build.setup.ts' },
    { name: 'selftest-chromium', testMatch: SELFTEST, dependencies: ['fixtures'], fullyParallel: true, use: chromium },
    { name: 'selftest-webkit', testMatch: SELFTEST, dependencies: ['fixtures'], fullyParallel: true, use: webkit },
    { name: 'flows', testMatch: '**/tools/flows.pw.ts', fullyParallel: true, use: chromium },
    { name: 'extract', testMatch: '**/tools/extract.pw.ts', use: chromium },
    { name: 'snapshot', testMatch: '**/tools/snapshot.pw.ts', use: chromium },
    // parity.pw.ts (app vs prototype) and its self-test, which runs on the fixture page.
    { name: 'parity', testMatch: '**/e2e/parity/*.pw.ts', dependencies: ['fixtures'], use: chromium },
  ],
})
