// Design lint: keeps literal design values in tokens.css, keeps design code
// free of app wiring, clocks, timers, pseudo-element content, font-metric
// units, inline motion and SVG url(#…) references, and keeps the app from
// reaching into the prototype.
//
//   bun tools/lint-design.ts        lint the frontend/ this file lives in
//
// Exits 1 and prints `file:line [rule] text` for every finding.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join, posix, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

export type Rule = 'token' | 'import' | 'raw-control' | 'pseudo' | 'clock' | 'timer' | 'lazy' | 'isolation' | 'inert' | 'font-unit' | 'inline-motion' | 'url-fragment'

/** `detail` names what was found when the line alone does not (the import rule's specifier). */
export type Finding = { rule: Rule; file: string; line: number; text: string; detail?: string }

const SOURCE_EXT = /\.(ts|tsx|js|jsx|mjs|cjs|css|html)$/
const SKIP_DIRS = new Set(['node_modules', 'dist', 'dist-prototype'])
/** Root-level files that are linted besides everything under src/ and prototype/. */
const ROOT_FILES = ['tailwind.config.ts', 'index.html', 'vite.config.ts']
const SCAN_DIRS = ['src', 'prototype']

const CSS_KEYWORD = String.raw`(?:var\(|inherit\b|initial\b|unset\b|revert\b)`

const TOKEN: RegExp[] = [
  /(?<![&\w])#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})(?![\w-])/,
  /\boklch\(/,
  /\brgba?\(/,
  /\bhsla?\(/,
  /\btext-\[[^\]\s]*px\]/,
  /\brounded(?:-(?:t|r|b|l|s|e|tl|tr|br|bl|ss|se|es|ee))?-\[/,
  /\bduration-\[/,
  new RegExp(String.raw`(?<![\w-])(?:font-size|border-radius|transition-duration)\s*:\s*(?!${CSS_KEYWORD})\S`),
  /(?<![\w-])(?:fontSize|borderRadius|transitionDuration)\s*:\s*['"`]?\s*[\d.]/,
]

const IMPORT_SPEC = /(?:\bfrom\s*|\bimport\s*\(\s*|\bimport\s+|\brequire\s*\(\s*)['"`]([^'"`]+)['"`]/g
const FETCH = /\bfetch\s*\(/
const RAW_CONTROL = [/<(?:button|input|select|textarea)(?![\w-])/]
const PSEUDO = [/(?<![\w-])(?:before|after):(?=[\w[!-])/, /::?(?:before|after)(?![\w-])/]
const CLOCK = [/\bDate\.now\s*\(/, /\bnew\s+Date\s*\(\s*\)/, /\bMath\.random\s*\(/]
const TIMER = [/\b(?:setTimeout|setInterval)\s*\(/]
const LAZY = [/\bloading\s*[=:]\s*\{?\s*['"`]lazy['"`]/]
const ISOLATION = [/\bprototype\//]
/**
 * Setting `inert` (a JSX attribute or prop, an object key, setAttribute). The
 * checks treat inert content as out of reach, so only a backdrop behind an
 * overlay may make visible content inert; reading it (`closest('[inert]')`)
 * is fine anywhere.
 */
const INERT = [/\binert\s*[=:]/, /\binert(?=\s*\/?>)/, /setAttribute\(\s*['"`]inert['"`]/]
/**
 * A length in a font-metric unit (`4ch`, `1ex`, `2ic`, `1cap`, `1lh`): its
 * computed value follows the glyph advances of the platform's rasteriser
 * (Linux rounds them to whole pixels), so a state's fingerprint would differ
 * by machine (tools/extract.ts). Use `em` (JetBrains Mono's digit is 0.6em).
 */
const FONT_UNIT = [
  // A literal: `4ch`, `[1.5ex]`.
  /(?<![\w.#-])\d*\.?\d+(?:ch|ex|ic|cap|r?lh)(?![\w-])/,
  // A template literal's unit: `${n}ch`.
  /\}(?:ch|ex|ic|cap|r?lh)(?![\w-])/,
  // String concatenation: n + 'ch', n + "ex", n + `lh`.
  /\+\s*(['"`])(?:ch|ex|ic|cap|r?lh)\1/,
]
/**
 * Motion in an inline style (`style={{ transition: … }}`, `el.style.animation
 * = …`, `el.style['transition'] = …`, `setProperty('transition-…')`, a
 * computed key `['animation']: …`, `Object.assign(el.style, { transition… })`),
 * vendor-prefixed forms of each (`WebkitTransition`, `MozAnimation`,
 * `'-webkit-transition'`, `setProperty('-webkit-…')`), and the Web Animations
 * API (`el.animate(…)`). Under the prototype's freeze switch every element
 * computes animation and transition as none, so the fingerprint
 * (tools/extract.ts) sees motion only in stylesheet rules (Tree.motionRules);
 * an inline value or a scripted animation would reach no hash. Put motion in
 * a class or in base.css. A whole inline style written as a string
 * (`el.style.cssText = …`, `setAttribute('style', …)`) is refused outright:
 * what it holds cannot be checked here. Comments are not checked.
 */
/** A camel-case motion property, plain or vendor-prefixed (`transitionDelay`, `WebkitAnimation`). */
const MOTION_CAMEL = String.raw`(?:(?:transition|animation)(?:[A-Z][A-Za-z]*)?|(?:[Ww]ebkit|Moz|ms|O)(?:Transition|Animation)[A-Za-z]*)`
/** A kebab-case motion property, plain or vendor-prefixed (`animation-name`, `-webkit-transition`). */
const MOTION_KEBAB = String.raw`(?:-(?:webkit|moz|ms|o)-)?(?:transition|animation)(?:-[a-z-]+)?`
const MOTION_ANY = String.raw`(?:${MOTION_KEBAB}|${MOTION_CAMEL})`
const INLINE_MOTION = [
  new RegExp(String.raw`(?:^|[{,(\s])['"]?${MOTION_CAMEL}['"]?\s*:`),
  new RegExp(String.raw`(?:^|[{,(\s])['"]${MOTION_KEBAB}['"]\s*:`),
  new RegExp(String.raw`\.style\.${MOTION_CAMEL}\s*=(?!=)`),
  new RegExp(String.raw`\.style\s*\[\s*(['"\x60])${MOTION_ANY}\1\s*\]\s*=(?!=)`),
  new RegExp(String.raw`\.setProperty\(\s*['"\x60]${MOTION_KEBAB}`),
  new RegExp(String.raw`\[\s*(['"\x60])${MOTION_ANY}\1\s*\]\s*:`),
  /\bObject\.assign\(\s*[^,()]*\.style\s*,.*(?:transition|animation)/i,
  /\.style\.cssText\s*\+?=(?!=)/,
  /\.setAttribute\(\s*(['"`])style\1/,
  /\.animate\s*\(/,
]
/**
 * An SVG reference by fragment (`fill="url(#g)"`, `clipPath="url(#c)"`,
 * `style={{ mask: 'url(#m)' }}`, `filter: url(#f)`): the computed value is a
 * URL into the page itself, which has no resource identity, so the extractor
 * (tools/extract.ts) throws on it and the state cannot be fingerprinted. Use
 * an external SVG file (an <img>, hashed by its bytes) or draw without the
 * reference. Comments are not checked.
 */
const URL_FRAGMENT = [/\burl\(\s*['"]?\s*#/]
const codeOnly = (line: string) => (/^\s*(?:\*|\/\*)/.test(line) ? '' : line.replace(/\/\/.*$/, ''))
/** The components that render a backdrop behind an overlay, and may set `inert`. */
export const INERT_ALLOWED = ['src/design/components/NativeBackdrop.tsx']

/**
 * The packages design code may import: exactly those imported under
 * src/design/ when the rule became an allowlist. Anything else (a store, a
 * router, bindings, @wailsio/runtime, react-dom) is app wiring.
 */
export const DESIGN_PACKAGES = ['react']
/** What design tests import besides: the test runner, files and server rendering. */
export const DESIGN_TEST_PACKAGES = ['bun:test', 'node:fs', 'node:path', 'react-dom/server']
const DESIGN_DIR = 'src/design/'
const TEST_FILE = /\.test\.tsx?$/

/**
 * The first import specifier on the line that design code may not use, or
 * 'fetch(' for a fetch call; null when there is none. Allowed: a relative
 * path that resolves inside src/design/ (modules, styles, assets; a query
 * such as `?url` is ignored), a package in DESIGN_PACKAGES, and in a test
 * file one in DESIGN_TEST_PACKAGES.
 */
/**
 * The line with its comments blanked, so only code is checked: a JSDoc
 * continuation line (`* …`) is all comment; otherwise, outside string and
 * template literals (a simple quote-state scan with backslash escapes), a
 * complete `/* … *\/` span becomes a space, an unclosed `/*` blanks the rest
 * of the line, and `//` ends it. A `//` inside a string (a URL) stays code.
 */
export function stripComments(raw: string): string {
  if (/^\s*\*/.test(raw)) return ''
  let out = ''
  let quote = ''
  for (let i = 0; i < raw.length; i++) {
    const c = raw[i]!
    if (quote) {
      out += c
      if (c === '\\' && i + 1 < raw.length) out += raw[++i]
      else if (c === quote) quote = ''
      continue
    }
    if (c === '/' && raw[i + 1] === '/') break
    if (c === '/' && raw[i + 1] === '*') {
      const end = raw.indexOf('*/', i + 2)
      if (end < 0) break
      out += ' '
      i = end + 1
      continue
    }
    if (c === "'" || c === '"' || c === '`') quote = c
    out += c
  }
  return out
}

function forbiddenImport(raw: string, file: string): string | null {
  const line = stripComments(raw)
  if (FETCH.test(line)) return 'fetch('
  const packages = TEST_FILE.test(file) ? [...DESIGN_PACKAGES, ...DESIGN_TEST_PACKAGES] : DESIGN_PACKAGES
  for (const m of line.matchAll(IMPORT_SPEC)) {
    const spec = m[1]
    if (spec.startsWith('./') || spec.startsWith('../')) {
      const target = posix.normalize(posix.join(posix.dirname(file), spec.replace(/[?#].*$/, '')))
      if (target.startsWith(DESIGN_DIR)) continue
      return spec
    }
    if (packages.includes(spec)) continue
    return spec
  }
  return null
}

/** Which rules apply to a file, by its root-relative, forward-slash path. */
function rulesFor(file: string): Rule[] {
  const inSrc = file.startsWith('src/')
  const inDesign = file.startsWith('src/design/')
  const inScreens = file.startsWith('src/design/screens/')
  const inProto = file.startsWith('prototype/')
  const appHtml = file === 'index.html'
  const out: Rule[] = []
  if ((inSrc || inProto || appHtml || file === 'tailwind.config.ts') && file !== 'src/design/tokens.css') out.push('token')
  if (inDesign) out.push('import', 'pseudo')
  if (inScreens) out.push('raw-control')
  if ((inDesign || inProto) && file !== 'prototype/harness.ts') out.push('clock', 'timer')
  if (inSrc || inProto || appHtml) out.push('lazy')
  if (inSrc || appHtml || file === 'vite.config.ts') out.push('isolation')
  if (inDesign && !INERT_ALLOWED.includes(file)) out.push('inert')
  if (inDesign || inProto || file === 'tailwind.config.ts') out.push('font-unit')
  if (inDesign && /\.(?:ts|tsx|js|jsx|mjs|cjs)$/.test(file)) out.push('inline-motion')
  if (inDesign || inProto) out.push('url-fragment')
  return out
}

/** Whether the line breaks the rule; a string is a finding with that detail. */
function matches(rule: Rule, line: string, file: string): boolean | string {
  switch (rule) {
    case 'token':
      return TOKEN.some((re) => re.test(line))
    case 'import':
      return forbiddenImport(line, file) ?? false
    case 'raw-control':
      return RAW_CONTROL.some((re) => re.test(line))
    case 'pseudo':
      return PSEUDO.some((re) => re.test(line))
    case 'clock':
      return CLOCK.some((re) => re.test(line))
    case 'timer':
      return TIMER.some((re) => re.test(line))
    case 'lazy':
      return LAZY.some((re) => re.test(line))
    case 'isolation':
      return ISOLATION.some((re) => re.test(line))
    case 'inert':
      return INERT.some((re) => re.test(line))
    case 'font-unit':
      return FONT_UNIT.some((re) => re.test(line))
    case 'inline-motion':
      return INLINE_MOTION.some((re) => re.test(codeOnly(line)))
    case 'url-fragment':
      return URL_FRAGMENT.some((re) => re.test(codeOnly(line)))
  }
}

function walk(dir: string, out: string[]): void {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name.startsWith('.') || SKIP_DIRS.has(entry.name)) continue
    const abs = join(dir, entry.name)
    if (entry.isDirectory()) walk(abs, out)
    else if (entry.isFile() && SOURCE_EXT.test(entry.name)) out.push(abs)
  }
}

function sourceFiles(root: string): string[] {
  const files: string[] = []
  for (const dir of SCAN_DIRS) {
    const abs = join(root, dir)
    if (existsSync(abs) && statSync(abs).isDirectory()) walk(abs, files)
  }
  for (const name of ROOT_FILES) {
    const abs = join(root, name)
    if (existsSync(abs)) files.push(abs)
  }
  return files
}

/** Lints the frontend tree at `root`. Findings are ordered by file, then line, then rule. */
export function lintDesign(root: string): Finding[] {
  const findings: Finding[] = []
  for (const abs of sourceFiles(root)) {
    const file = relative(root, abs).split(sep).join('/')
    const rules = rulesFor(file)
    if (rules.length === 0) continue
    const lines = readFileSync(abs, 'utf8').split(/\r?\n/)
    lines.forEach((text, i) => {
      for (const rule of rules) {
        const hit = matches(rule, text, file)
        if (hit === false) continue
        findings.push(typeof hit === 'string' ? { rule, file, line: i + 1, text: text.trim(), detail: hit } : { rule, file, line: i + 1, text: text.trim() })
      }
    })
  }
  return findings.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line || a.rule.localeCompare(b.rule))
}

if (import.meta.main) {
  const root = join(dirname(fileURLToPath(import.meta.url)), '..')
  const findings = lintDesign(root)
  for (const f of findings) console.error(`${f.file}:${f.line} [${f.rule}]${f.detail ? ` ${f.detail}:` : ''} ${f.text}`)
  if (findings.length > 0) {
    console.error(`lint:design: ${findings.length} finding(s)`)
    process.exit(1)
  }
  console.log('lint:design: clean')
}
