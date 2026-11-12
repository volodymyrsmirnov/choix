import { afterAll, describe, expect, test } from 'bun:test'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { lintDesign, type Finding } from './lint-design'

const FRONTEND = join(dirname(fileURLToPath(import.meta.url)), '..')
const made: string[] = []

/** Writes `files` (relative path → contents) into a fresh fake frontend root. */
function tree(files: Record<string, string>): string {
  const root = mkdtempSync(join(tmpdir(), 'lint-design-'))
  made.push(root)
  for (const [rel, body] of Object.entries(files)) {
    const abs = join(root, rel)
    mkdirSync(dirname(abs), { recursive: true })
    writeFileSync(abs, body)
  }
  return root
}

afterAll(() => {
  for (const root of made) rmSync(root, { recursive: true, force: true })
})

const rules = (findings: Finding[]) => findings.map((f) => f.rule)

type Case = [name: string, file: string, body: string]

const fixtures: Record<Finding['rule'], Case[]> = {
  token: [
    ['hex colour', 'src/design/components/A.tsx', `export const c = '#1a2b3c'\n`],
    ['oklch()', 'src/design/base.css', `.a { color: oklch(0.5 0.1 240); }\n`],
    ['rgb()', 'src/design/components/A.tsx', `export const c = 'rgb(1, 2, 3)'\n`],
    ['rgba()', 'prototype/main.tsx', `export const c = 'rgba(1, 2, 3, 0.5)'\n`],
    ['hsl()', 'src/design/base.css', `.a { color: hsl(10 20% 30%); }\n`],
    ['text-[…px]', 'src/design/components/A.tsx', `export const A = () => <p className="text-[13px]" />\n`],
    ['rounded-[', 'src/design/components/A.tsx', `export const A = () => <p className="rounded-[7px]" />\n`],
    ['duration-[', 'src/design/components/A.tsx', `export const A = () => <p className="duration-[90ms]" />\n`],
    ['font-size', 'src/design/base.css', `.a { font-size: 13px; }\n`],
    ['border-radius', 'src/design/base.css', `.a { border-radius: 999px; }\n`],
    ['transition-duration', 'src/design/base.css', `.a { transition-duration: 0.2s; }\n`],
    ['fontSize style', 'src/design/components/A.tsx', `export const s = { fontSize: 13 }\n`],
    ['tailwind config', 'tailwind.config.ts', `export default { theme: { colors: { x: 'oklch(0.5 0 0)' } } }\n`],
  ],
  import: [
    ['wails runtime', 'src/design/components/A.tsx', `import { Events } from '@wailsio/runtime'\n`],
    ['app path', 'src/design/screens/library/Library.tsx', `import { useLibrary } from '../../../app/library/store'\n`],
    ['bindings path', 'src/design/components/A.tsx', `import * as api from '../../bindings/choix/service'\n`],
    ['fetch()', 'src/design/components/A.tsx', `export const load = () => fetch('/api/library')\n`],
    ['@bindings alias', 'src/design/components/A.tsx', `import { Library } from '@bindings/choix/library'\n`],
    ['wails runtime subpath', 'src/design/screens/focus/Focus.tsx', `import { Window } from '@wailsio/runtime/window'\n`],
    ['a store outside the design folder', 'src/design/components/A.tsx', `import { useStore } from '../../store/library'\n`],
    ['zustand', 'src/design/components/A.tsx', `import { create } from 'zustand'\n`],
    ['react-dom outside a test', 'src/design/components/A.tsx', `import { createPortal } from 'react-dom'\n`],
    ['a test runner outside a test', 'src/design/format.ts', `import { expect } from 'bun:test'\n`],
    ['a package in a test file that no design test uses', 'src/design/format.test.ts', `import { z } from 'zod'\n`],
    ['dynamic import', 'src/design/components/A.tsx', `export const load = () => import('../../app/library/Library')\n`],
    ['require()', 'src/design/components/A.tsx', `const x = require('zustand')\n`],
    ['re-export', 'src/design/components/index.ts', `export { Window } from '@wailsio/runtime'\n`],
    ['side-effect import outside the design folder', 'src/design/base.ts', `import '../app/global.css'\n`],
    ['absolute path', 'src/design/components/A.tsx', `import x from '/src/app/x'\n`],
    ['a URL', 'src/design/components/A.tsx', `import x from 'https://example.com/x.js' // remote\n`],
    ['relative path that climbs out and back in by name', 'src/design/components/A.tsx', `import x from '../../designer/x'\n`],
  ],
  'raw-control': [
    ['<button', 'src/design/screens/focus/Focus.tsx', `export const F = () => <button>Pick</button>\n`],
    ['<input', 'src/design/screens/settings/Settings.tsx', `export const S = () => <input />\n`],
    ['<select', 'src/design/screens/settings/Settings.tsx', `export const S = () => <select />\n`],
    ['<textarea', 'src/design/screens/settings/Settings.tsx', `export const S = () => <textarea />\n`],
  ],
  pseudo: [
    ['before: variant', 'src/design/components/A.tsx', `export const A = () => <p className="before:block" />\n`],
    ['after: variant', 'src/design/components/A.tsx', `export const A = () => <p className="hover:after:block" />\n`],
    ['::before', 'src/design/base.css', `.a::before { display: block; }\n`],
    ['::after', 'src/design/base.css', `.a::after { display: block; }\n`],
  ],
  clock: [
    ['Date.now()', 'src/design/format.ts', `export const t = Date.now()\n`],
    ['new Date()', 'prototype/store.ts', `export const t = new Date()\n`],
    ['Math.random()', 'src/design/components/A.tsx', `export const r = Math.random()\n`],
  ],
  timer: [
    ['setTimeout()', 'src/design/components/Toast.tsx', `setTimeout(() => {}, 10)\n`],
    ['setInterval()', 'prototype/store.ts', `window.setInterval(() => {}, 10)\n`],
  ],
  lazy: [['loading="lazy"', 'src/design/components/Thumb.tsx', `export const T = () => <img loading="lazy" />\n`]],
  inert: [
    ['JSX attribute', 'src/design/components/Strip.tsx', `export const S = () => <div inert={true} />\n`],
    ['bare attribute', 'src/design/components/Strip.tsx', `export const S = () => <div inert />\n`],
    ['spread object', 'src/design/components/Thumb.tsx', `export const p = { inert: '' }\n`],
    ['setAttribute', 'src/design/screens/folder/Folder.tsx', `el.setAttribute('inert', '')\n`],
  ],
  'font-unit': [
    ['ch in a class', 'src/design/components/A.tsx', `export const A = () => <span className="min-w-[4ch]" />\n`],
    ['ex in CSS', 'src/design/base.css', `.a { width: 1.5ex; }\n`],
    ['lh in a prototype style', 'prototype/Frame.tsx', `export const s = { height: '2lh' }\n`],
    ['ch in a template literal', 'src/design/components/A.tsx', 'export const w = `${n * 2}ch`\n'],
    ['ch by concatenation', 'src/design/components/A.tsx', `export const w = n * 2 + 'ch'\n`],
    ['ex by concatenation, double quotes', 'prototype/Frame.tsx', `export const w = n + "ex"\n`],
  ],
  'inline-motion': [
    ['transition in a style object', 'src/design/components/A.tsx', `export const A = () => <p style={{ transition: 'opacity 1s' }} />\n`],
    ['transitionDuration key', 'src/design/components/A.tsx', `export const s = { opacity: 1, transitionDuration: 'var(--dur-fast)' }\n`],
    ['animation key', 'src/design/screens/focus/Focus.tsx', `const s: CSSProperties = { animation: 'choix-spin 1s' }\n`],
    ['quoted kebab key', 'src/design/components/A.tsx', `export const s = { 'animation-name': 'k' }\n`],
    ['style property assignment', 'src/design/components/A.tsx', `el.style.transitionProperty = 'opacity'\n`],
    ['setProperty', 'src/design/components/A.tsx', `el.style.setProperty('animation-duration', '1s')\n`],
    ['style.cssText assignment', 'src/design/components/A.tsx', `el.style.cssText = 'transition: opacity 1s'\n`],
    ['style.cssText from a variable', 'src/design/components/A.tsx', `el.style.cssText = css\n`],
    ['style.cssText appended', 'src/design/components/A.tsx', `el.style.cssText += extra\n`],
    ["setAttribute('style')", 'src/design/components/A.tsx', `el.setAttribute('style', 'animation: choix-spin 1s')\n`],
    ['setAttribute("style") from a variable', 'src/design/screens/focus/Focus.tsx', `node.setAttribute("style", css)\n`],
    ["computed key ['transition']", 'src/design/components/A.tsx', `export const s = { ['transition']: 'opacity 1s' }\n`],
    ['computed key ["animation-name"]', 'src/design/components/A.tsx', `export const s = { ["animation-name"]: 'k' }\n`],
    ['computed key [`transitionDelay`]', 'src/design/components/A.tsx', 'export const s = { [`transitionDelay`]: \'1s\' }\n'],
    ['Object.assign onto style', 'src/design/components/A.tsx', `Object.assign(el.style, { transitionProperty: 'opacity' })\n`],
    ['Object.assign onto style from a motion variable', 'src/design/components/A.tsx', `Object.assign(el.style, fadeTransition)\n`],
    ['Object.assign onto a ref style, kebab key', 'src/design/components/A.tsx', `Object.assign(ref.current.style, { 'animation-name': 'k' })\n`],
    ["bracket assignment style['transition']", 'src/design/components/A.tsx', `el.style['transition'] = 'opacity 1s'\n`],
    ['bracket assignment style["animationName"]', 'src/design/components/A.tsx', `el.style["animationName"] = 'k'\n`],
    ['bracket assignment style[`animation-duration`]', 'src/design/components/A.tsx', 'ref.current.style[`animation-duration`] = \'1s\'\n'],
    ["bracket assignment style['-webkit-transition']", 'src/design/components/A.tsx', `el.style['-webkit-transition'] = 'opacity 1s'\n`],
    ['vendor key WebkitTransition', 'src/design/components/A.tsx', `export const s = { WebkitTransition: 'opacity 1s' }\n`],
    ['vendor key MozAnimation', 'src/design/components/A.tsx', `export const s = { opacity: 1, MozAnimation: 'k 1s' }\n`],
    ['vendor key msTransitionDuration', 'src/design/components/A.tsx', `export const s = { msTransitionDuration: '1s' }\n`],
    ['vendor style property assignment', 'src/design/components/A.tsx', `el.style.WebkitTransition = 'opacity 1s'\n`],
    ['quoted vendor kebab key', 'src/design/components/A.tsx', `export const s = { '-webkit-transition': 'opacity 1s' }\n`],
    ['vendor computed key', 'src/design/components/A.tsx', `export const s = { ['-moz-animation']: 'k 1s' }\n`],
    ["setProperty('-webkit-…')", 'src/design/components/A.tsx', `el.style.setProperty('-webkit-animation', 'k 1s')\n`],
    ['Web Animations API', 'src/design/components/A.tsx', `ref.current?.animate([{ opacity: 0 }, { opacity: 1 }], 200)\n`],
    ['Web Animations API on an element variable', 'src/design/screens/focus/Focus.tsx', `const anim = el.animate(keyframes, options)\n`],
  ],
  'url-fragment': [
    ['fill attribute', 'src/design/components/Icon.tsx', `export const I = () => <rect fill="url(#g)" />\n`],
    ['clipPath attribute, quoted id', 'src/design/components/Icon.tsx', `export const I = () => <g clipPath="url('#c')" />\n`],
    ['inline style', 'src/design/components/A.tsx', `export const s = { mask: 'url(#m)' }\n`],
    ['a template literal', 'src/design/components/A.tsx', 'export const f = `url(#${id})`\n'],
    ['CSS', 'src/design/base.css', `.a { filter: url(#blur); }\n`],
    ['a prototype scenario', 'prototype/scenarios/gallery.tsx', `export const I = () => <path stroke="url(#s)" />\n`],
  ],
  isolation: [
    ['app imports prototype', 'src/app/library/Library.tsx', `import { photos } from '../../../prototype/mock/data'\n`],
    ['index.html', 'index.html', `<script type="module" src="/prototype/main.tsx"></script>\n`],
    ['vite.config.ts', 'vite.config.ts', `export default { root: 'prototype/' }\n`],
  ],
}

describe('lintDesign: one fixture per rule yields exactly one finding of that rule', () => {
  for (const [rule, cases] of Object.entries(fixtures)) {
    for (const [name, file, body] of cases) {
      test(`${rule}: ${name}`, () => {
        const findings = lintDesign(tree({ [file]: body }))
        expect(rules(findings)).toEqual([rule as Finding['rule']])
        expect(findings[0]).toMatchObject({ file, line: 1 })
        expect(findings[0].text).toBe(body.trim())
      })
    }
  }
})

describe('lintDesign: clean code', () => {
  test('a clean screen yields no findings', () => {
    const root = tree({
      'src/design/screens/library/Library.tsx': [
        `import { Button } from '../../components/Button'`,
        `export const Library = () => (`,
        `  <div className="text-12 bg-bg-2 w-[132px]">`,
        `    <Button>Rescan</Button>`,
        `  </div>`,
        `)`,
        ``,
      ].join('\n'),
    })
    expect(lintDesign(root)).toEqual([])
  })

  test('literal values are allowed in tokens.css', () => {
    const root = tree({
      'src/design/tokens.css': `:root { --mask-opaque: #000; --color-bg-0: oklch(0.16 0.004 240); --text-9: 9px; }\n`,
    })
    expect(lintDesign(root)).toEqual([])
  })

  test('components may render raw controls; only screens may not', () => {
    const root = tree({ 'src/design/components/Button.tsx': `export const Button = () => <button type="button" />\n` })
    expect(lintDesign(root)).toEqual([])
  })

  test('the prototype harness may use a timer', () => {
    const root = tree({ 'prototype/harness.ts': `setTimeout(() => {}, 2000)\n` })
    expect(lintDesign(root)).toEqual([])
  })

  test('a Date with an argument is not a clock read', () => {
    const root = tree({ 'prototype/clock.ts': `export const NOW = new Date('2026-11-11T10:00:00Z')\n` })
    expect(lintDesign(root)).toEqual([])
  })

  test('a var() reference is not a literal', () => {
    const root = tree({
      'src/design/base.css': `.a { font-size: var(--text-12); border-radius: var(--radius-md); transition-duration: var(--dur-fast); }\n`,
    })
    expect(lintDesign(root)).toEqual([])
  })

  test('reports the right line', () => {
    const root = tree({ 'src/design/components/A.tsx': `export const a = 1\n\nexport const t = Date.now()\n` })
    expect(lintDesign(root)).toEqual([
      { rule: 'clock', file: 'src/design/components/A.tsx', line: 3, text: 'export const t = Date.now()' },
    ])
  })

  test('design files import only inside the design folder, React, and in tests the test tooling', () => {
    const root = tree({
      'src/design/screens/library/Library.tsx': [
        `import { useState, type ReactNode } from 'react'`,
        `import { Button } from '../../components/Button'`,
        `import type { Folder } from '../../types'`,
        `import menus from '../../menus.json'`,
        `import mark from '../../assets/mark.svg'`,
        `export { Row } from './Row'`,
        `const lazy = () => import('./Heavy')`,
        ``,
      ].join('\n'),
      'src/design/entry.ts': `import './base.css'\nimport './fonts/inter.woff2?url'\n`,
      'src/design/format.test.ts': [
        `import { expect, test } from 'bun:test'`,
        `import { readFileSync } from 'node:fs'`,
        `import { join } from 'node:path'`,
        `import { renderToStaticMarkup } from 'react-dom/server'`,
        `import { format } from './format'`,
        ``,
      ].join('\n'),
      'src/design/components/FocusMedia.test.tsx': `import { renderToStaticMarkup } from 'react-dom/server'\n`,
    })
    expect(lintDesign(root)).toEqual([])
  })

  test('comments that say "from" are not imports', () => {
    const root = tree({
      'src/design/components/A.tsx': [
        `/** A copy with placeholders filled from \`vars\`. */`,
        ` * Width comes from 'className' (e.g. w-20).`,
        `export const a = 1 // taken from 'zustand' in v1`,
        `// import { create } from 'zustand'`,
        `/* import { create } from 'zustand' */`,
        `  /* import { Events } from '@wailsio/runtime'`,
        `export const b = 2 /* from 'zustand' */ + 1`,
        ``,
      ].join('\n'),
    })
    expect(lintDesign(root)).toEqual([])
  })

  test('code after a closed block comment, or after a string holding //, is still checked', () => {
    const cases: [string, string][] = [
      [`/* x */ import { create } from 'zustand'`, 'zustand'],
      [`const u = 'http://example.com'; export const load = () => import('@wailsio/runtime')`, '@wailsio/runtime'],
      ['const u = `a//b`, v = "c//d"; import { create } from \'zustand\'', 'zustand'],
      [`const s = 'it\\'s // fine'; import { Events } from '@wailsio/runtime'`, '@wailsio/runtime'],
    ]
    for (const [line, spec] of cases) {
      const findings = lintDesign(tree({ 'src/design/components/A.tsx': `${line}\n` }))
      expect(findings.map((f) => [f.rule, f.detail]), line).toEqual([['import', spec]])
    }
  })

  test('an import finding names the specifier', () => {
    const root = tree({ 'src/design/components/A.tsx': `import { create } from 'zustand'\n` })
    expect(lintDesign(root)).toEqual([
      { rule: 'import', file: 'src/design/components/A.tsx', line: 1, text: `import { create } from 'zustand'`, detail: 'zustand' },
    ])
  })

  test('a backdrop behind an overlay may set inert, and anyone may read it', () => {
    const root = tree({
      'src/design/components/NativeBackdrop.tsx': `el?.setAttribute('inert', '')\n`,
      'src/design/components/List.tsx': `if (ref.current?.closest('[inert]')) return\n`,
    })
    expect(lintDesign(root)).toEqual([])
  })

  test('motion in stylesheets, in class names and in comments is not inline motion', () => {
    const root = tree({
      'src/design/base.css': `.a { transition: opacity var(--dur-fast); animation: choix-spin var(--dur-spin) linear infinite; }\n`,
      'src/design/components/A.tsx': [
        `// The first time without animation: it jumps.`,
        `/* transition: none */`,
        `export const A = () => <p className="transition-opacity duration-fast animate-spin" style={{ opacity: 1 }} />`,
        `if (el.style.transition === '') {}`,
        `if (el.style['transition'] !== '') {}`,
        `export const B = () => <p className="animate-spin" style={{ WebkitMaskImage: mask, maskImage: mask }} />`,
        `const animated = isAnimated(el)`,
        ``,
      ].join('\n'),
      'prototype/Frame.tsx': `export const s = { transition: 'none' }\n`,
    })
    expect(lintDesign(root)).toEqual([])
  })

  test('style reads, an ordinary computed key, and url() of a file are fine', () => {
    const root = tree({
      'src/design/components/A.tsx': [
        `const was = el.style.cssText`,
        `if (el.getAttribute('style') === null) {}`,
        `export const s = { [key]: 1, ['opacity']: 1 }`,
        `Object.assign(el.style, { opacity: '1' })`,
        `export const I = () => <a href="#top"><img src="/mark.svg" /></a>`,
        `// fill="url(#g)" would not be fingerprinted`,
        ``,
      ].join('\n'),
      'src/design/base.css': `@font-face { src: url(./fonts/inter-400.woff2); }\n.a { mask-image: url("./mask.svg"); }\n`,
    })
    expect(lintDesign(root)).toEqual([])
  })

  test('the real tree yields no findings', () => {
    expect(lintDesign(FRONTEND)).toEqual([])
  })
})
