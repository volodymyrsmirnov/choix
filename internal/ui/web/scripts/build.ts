// Build the choix SPA into ./dist with hashed filenames and an index.html
// that references them. Run with `bun run build`.
//
// Output:
//   dist/main.<hash>.js
//   dist/main.<hash>.css
//   dist/index.html  — references the hashed asset paths under /static/
//
// The hash is the first 8 hex chars of the SHA-256 of the bundle content.
// That way the embedded binary serves a fresh URL on every release and
// the browser cache for old URLs is irrelevant.

import { $ } from 'bun'
import { createHash } from 'node:crypto'
import { mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const ROOT = new URL('..', import.meta.url).pathname
const DIST = join(ROOT, 'dist')

function hash8(buf: Uint8Array | string): string {
  return createHash('sha256').update(buf).digest('hex').slice(0, 8)
}

// 1. Reset dist.
rmSync(DIST, { recursive: true, force: true })
mkdirSync(DIST, { recursive: true })

// 2. JS bundle. Bun's `--entry-naming` only takes a literal name, so we
//    bundle to a stable name first, hash, then rename.
await $`bun build ./src/main.tsx --outdir ./dist --minify --target=browser --define:process.env.NODE_ENV='"production"' --entry-naming=main.js`.cwd(ROOT)
const jsBuf = readFileSync(join(DIST, 'main.js'))
const jsHash = hash8(jsBuf)
const jsName = `main.${jsHash}.js`
writeFileSync(join(DIST, jsName), jsBuf)
rmSync(join(DIST, 'main.js'))

// 3. CSS. Same trick — emit to a stable name, hash, rename.
await $`bunx --bun tailwindcss -c ./tailwind.config.js -i ./src/styles.css -o ./dist/main.css --minify`.cwd(ROOT)
const cssBuf = readFileSync(join(DIST, 'main.css'))
const cssHash = hash8(cssBuf)
const cssName = `main.${cssHash}.css`
writeFileSync(join(DIST, cssName), cssBuf)
rmSync(join(DIST, 'main.css'))

// 4. index.html — substitute the asset references.
const html = readFileSync(join(ROOT, 'index.html'), 'utf8')
  .replace(/\/static\/main\.css/g, `/static/${cssName}`)
  .replace(/\/static\/main\.js/g, `/static/${jsName}`)
writeFileSync(join(DIST, 'index.html'), html)

console.log(`built dist/`)
for (const name of readdirSync(DIST).sort()) {
  console.log(`  ${name}`)
}
