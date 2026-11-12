import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { prototypePostcss } from '../../vite.prototype.config'

// Test-only build of the broken fixture states (tools/fixtures/index.html →
// .design/fixtures-dist/). The design global setup runs it; the prototype
// build (vite.prototype.config.ts) never sees these files. Its CSS goes
// through the prototype's own PostCSS chain, so the fixture page carries the
// prototype's stylesheet rules.
export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  plugins: [react()],
  css: { postcss: prototypePostcss() },
  logLevel: 'error',
  build: {
    outDir: fileURLToPath(new URL('../../.design/fixtures-dist', import.meta.url)),
    emptyOutDir: true,
  },
})
