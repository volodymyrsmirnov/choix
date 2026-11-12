import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import wails from '@wailsio/runtime/plugins/vite'
import { defineConfig } from 'vite'

// The app: index.html → dist/, which the Go module embeds (embed.go). Run
// from frontend/ (the package scripts and `wails3 dev` do), so the entry,
// the output directory and postcss.config.cjs — the shared PostCSS chain
// with tailwind.config.ts — resolve there.
//
// Mode `parity` (`bun run build:parity`) builds the same app against the
// mock bindings for the parity tests: output in .design/dist-parity/, and
// both the generated bindings and the Wails runtime replaced by the mocks
// in e2e/parity/mock-bindings/.

const here = (path: string) => fileURLToPath(new URL(path, import.meta.url))

export default defineConfig(({ mode }) => {
  const parity = mode === 'parity'
  return {
    // wails(): typed custom events from the generated bindings (bindings/
    // is the generator's -d directory, committed). Not in parity mode: the
    // bindings and the runtime are mocks there, so there is nothing to register.
    plugins: parity ? [react()] : [react(), wails('./bindings')],
    resolve: {
      // '@bindings' also matches '@bindings/<package>/…'; the runtime alias
      // matches the bare specifier only.
      alias: [
        {
          find: '@bindings',
          replacement: parity
            ? here('./e2e/parity/mock-bindings')
            : here('./bindings/github.com/volodymyrsmirnov/choix/internal/app'),
        },
        ...(parity ? [{ find: /^@wailsio\/runtime$/, replacement: here('./e2e/parity/mock-bindings/runtime.ts') }] : []),
      ],
    },
    server: {
      host: '127.0.0.1',
      port: Number(process.env.WAILS_VITE_PORT) || 9245,
      strictPort: true,
    },
    build: {
      outDir: parity ? '.design/dist-parity' : 'dist',
      emptyOutDir: true,
    },
  }
})
