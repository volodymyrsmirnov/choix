import { defineConfig } from '@playwright/test'
import { repoRoot, runDirs } from './global-setup'

// End-to-end specs against the server build of the app (`task test:e2e`,
// which builds the app and the harness page first). Two projects:
//
//   api  binding-level specs (e2e/api): they drive the Go services through
//        the generated bindings on the harness page (e2e/harness), served by
//        the webServer below on 8090 (CHOIX_ASSETS_DIR); see e2e/api/fixtures.ts.
//   ui   UI specs (e2e/ui): they drive the real app — the frontend embedded
//        in the server binary global-setup.ts builds, so frontend/dist must
//        be the app (`task frontend:dist`) — on a server each test starts on
//        8091 without CHOIX_ASSETS_DIR; see e2e/ui/fixtures.ts.
//
// One worker: the api specs share one server and one Library, and the quit
// spec and the ui specs both use port 8091, one at a time.
//
// The run's directories are made here, while the config loads: Playwright
// starts the webServer before globalSetup, and the server must find its
// config.toml (both model settings off) when it starts. globalSetup builds
// genfolder and the server binary the quit and ui specs start (with the
// app embedded), and generates the api specs' folders.
const dirs = runDirs()

export default defineConfig({
  testMatch: '**/*.e2e.ts',
  outputDir: '../.design/e2e/test-results',
  // One server, one Library, one port for the quit and ui specs: the specs run one at a time.
  workers: 1,
  fullyParallel: false,
  timeout: 120_000,
  forbidOnly: !!process.env.CI,
  reporter: [['list']],
  globalSetup: './global-setup.ts',
  use: {
    browserName: 'chromium',
    baseURL: 'http://127.0.0.1:8090',
  },
  projects: [
    { name: 'api', testDir: './api' },
    { name: 'ui', testDir: './ui', use: { baseURL: 'http://127.0.0.1:8091' } },
  ],
  webServer: {
    command: 'go run -tags server,production ./cmd/choix',
    cwd: repoRoot,
    url: 'http://127.0.0.1:8090/health',
    reuseExistingServer: false,
    // The first `go run` compiles the cgo packages and Wails.
    timeout: 600_000,
    stdout: 'pipe',
    stderr: 'pipe',
    gracefulShutdown: { signal: 'SIGTERM', timeout: 10_000 },
    env: {
      CHOIX_DATA_DIR: dirs.data,
      CHOIX_E2E_ROOT: dirs.root,
      CHOIX_ASSETS_DIR: dirs.harnessDist,
      WAILS_SERVER_PORT: '8090',
      // Keep CHOIX_E2E_TMP for the workers (also set by runDirs).
      CHOIX_E2E_TMP: dirs.tmp,
    },
  },
})
