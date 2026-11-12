// The prototype's CSS goes through prototypePostcss() (vite.prototype.config.ts),
// the app's through postcss.config.cjs. The two chains must be the same apart
// from Tailwind's `content` (which files Tailwind scans): the same plugins in
// the same order, the same theme and tokens, the same data-force twins.
// Otherwise a state could pass in the prototype and render differently in
// the app, where parity compares the two.
import { describe, expect, test } from 'bun:test'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import postcss, { type AcceptedPlugin } from 'postcss'
import tailwindcss from 'tailwindcss'
import appTailwind from '../tailwind.config'
import { prototypePostcss } from '../vite.prototype.config'
import forcePlugin from './postcss-force.cjs'

const FRONTEND = join(import.meta.dir, '..')
const require = createRequire(import.meta.url)
const appChain = () => (require(join(FRONTEND, 'postcss.config.cjs')) as { plugins: AcceptedPlugin[] }).plugins
const protoChain = () => prototypePostcss().plugins as AcceptedPlugin[]

/** A plugin's name, whether it is a plugin object or a creator marked `postcss: true`. */
function nameOf(p: AcceptedPlugin): string {
  const o = p as { postcssPlugin?: string; postcss?: unknown }
  if (typeof o.postcssPlugin === 'string') return o.postcssPlugin
  if (typeof p === 'function') {
    const made = (p as () => { postcssPlugin?: string })()
    if (made && typeof made.postcssPlugin === 'string') return made.postcssPlugin
  }
  return String(p)
}

/**
 * CSS whose output depends on the theme, the tokens and the plugin order but
 * not on `content`: @tailwind base (preflight and the base layer) does not
 * read what Tailwind scans, @apply generates its utilities whatever Tailwind
 * scans, and no content-dependent directive (components, utilities) is used.
 */
const PROBE = `
@tailwind base;
.probe { @apply text-12 bg-bg-2 rounded-md shadow-pop duration-fast tracking-tightish font-mono; }
.probe:hover { @apply bg-bg-4 text-fg-0; }
.probe:focus-visible { @apply shadow-sel-ring; }
.probe-wrap:focus-within .probe { @apply text-pick; }
.probe:active { @apply bg-pick-bg; }
`

async function run(plugins: AcceptedPlugin[]): Promise<string> {
  const result = await postcss(plugins).process(PROBE, { from: join(FRONTEND, 'src', 'design', 'probe.css') })
  return result.css
}

describe('postcss.config.cjs and prototypePostcss()', () => {
  test('the same plugins in the same order', () => {
    const app = appChain().map(nameOf)
    expect(app).toEqual(['tailwindcss', 'choix-force'])
    expect(protoChain().map(nameOf)).toEqual(app)
  })

  test('the same output for CSS that does not depend on content (theme, tokens, data-force twins)', async () => {
    const app = await run(appChain())
    const proto = await run(protoChain())
    // Not vacuous: the theme resolved to tokens, and the twins were added.
    expect(app).toContain('var(--text-12)')
    expect(app).toContain('var(--color-bg-4)')
    expect(app).toContain('[data-force~="hover"]')
    expect(app).toContain('[data-force~="focus-within"]')
    expect(app).toContain('box-sizing: border-box') // preflight: the base layer is in the probe
    expect(proto).toBe(app)
  })

  test('a chain that differs is told apart: order, a missing plugin, another theme, another base layer', async () => {
    const app = await run(appChain())
    const [tw, force] = protoChain()
    expect([force, tw].map(nameOf)).not.toEqual(appChain().map(nameOf))
    expect(await run([tw])).not.toBe(app)
    const theme = appTailwind.theme
    const otherTheme = { ...theme, extend: { ...theme.extend, colors: { ...theme.extend.colors, bg: { ...theme.extend.colors.bg, 4: 'var(--color-bg-5)' } } } }
    expect(await run([tailwindcss({ ...appTailwind, theme: otherTheme }), forcePlugin()])).not.toBe(app)
    // A base-layer difference (here: no preflight) is told apart too.
    expect(await run([tailwindcss({ ...appTailwind, corePlugins: { preflight: false } }), forcePlugin()])).not.toBe(app)
  })
})

describe("the app's Tailwind content (tailwind.config.ts)", () => {
  test('is the app entry and src/ without test files, never the prototype', () => {
    expect(appTailwind.content).toEqual({
      relative: true,
      files: ['./index.html', './src/**/*.{ts,tsx}', '!**/*.test.ts', '!**/*.test.tsx'],
    })
    expect(appTailwind.content.files.some((g) => g.includes('prototype'))).toBe(false)
  })
})
