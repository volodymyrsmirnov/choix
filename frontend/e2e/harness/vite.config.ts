import { fileURLToPath } from 'node:url'
import wails from '@wailsio/runtime/plugins/vite'
import { defineConfig } from 'vite'

// The E2E harness page (`bun run harness:build`, run from frontend/):
// e2e/harness/index.html → e2e/harness/dist/, which the server build serves
// through CHOIX_ASSETS_DIR. It imports the committed bindings like the app
// does (the '@bindings' alias of ../../vite.config.ts) with the Wails
// plugin's typed events.

const here = (path: string) => fileURLToPath(new URL(path, import.meta.url))

export default defineConfig({
  root: here('.'),
  // No public directory and no PostCSS chain: the page has no styles.
  publicDir: false,
  css: { postcss: {} },
  plugins: [wails(here('../../bindings'))],
  resolve: {
    alias: [{ find: '@bindings', replacement: here('../../bindings/github.com/volodymyrsmirnov/choix/internal/app') }],
  },
  build: {
    outDir: here('./dist'),
    emptyOutDir: true,
  },
})
