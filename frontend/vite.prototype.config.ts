import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import tailwindcss from 'tailwindcss'
import { defineConfig } from 'vite'
import appTailwind from './tailwind.config'
import forcePlugin from './tools/postcss-force.cjs'

// The clickable design prototype: prototype/index.html → dist-prototype/.
// Run from frontend/ (the package scripts do), so `root` resolves there.

/**
 * What Tailwind scans for the prototype's stylesheet: the prototype's own
 * sources only (frontend-relative). The app's tailwind.config.ts scans the
 * whole of src/ and the app entry; were the prototype to share that, a
 * utility only the app uses (a duration, an animate-* and its @keyframes, a
 * placeholder: colour) would enter the prototype bundle and move every state
 * hash while the source digest stayed the same. Test files are left out, so
 * a class named only in a test creates no rule. Every positive glob here
 * must lie inside docs/design/gate.json's digestInputs
 * (tools/digestInputs.test.ts).
 */
export const PROTOTYPE_CONTENT = [
  'src/design/**/*.{ts,tsx}',
  'prototype/**/*.{ts,tsx,html}',
  '!**/*.test.ts',
  '!**/*.test.tsx',
]

const FRONTEND = fileURLToPath(new URL('.', import.meta.url))
const absolute = (glob: string) => (glob.startsWith('!') ? `!${FRONTEND}${glob.slice(1)}` : `${FRONTEND}${glob}`)

/**
 * The PostCSS chain of every prototype CSS build (this config and the
 * test-only fixture page, tools/fixtures/vite.config.ts): postcss.config.cjs's
 * chain (Tailwind, then the data-force twins of tools/postcss-force.cjs) with
 * the app's theme and tokens but the prototype's own content.
 */
export function prototypePostcss() {
  return {
    plugins: [tailwindcss({ ...appTailwind, content: { relative: false, files: PROTOTYPE_CONTENT.map(absolute) } }), forcePlugin()],
  }
}

export default defineConfig({
  root: 'prototype',
  plugins: [react()],
  css: { postcss: prototypePostcss() },
  build: {
    outDir: '../dist-prototype',
    emptyOutDir: true,
  },
  server: {
    port: 8811,
    strictPort: true,
    // frontend/, plus docs/design for gate.json and retired.json.
    fs: { allow: ['..', '../../docs/design'] },
  },
})
