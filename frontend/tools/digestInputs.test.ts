// gate.json's digestInputs cover every module that can change what the
// prototype build contains or how a state is rendered and hashed: the build
// (vite.prototype.config.ts and what it pulls in), the stamp
// (build-prototype.mjs), and the snapshot path (snapshot.mjs →
// snapshot.pw.ts → extract.ts, the design context, the server). Every local
// module those entry points import, transitively, is either a digest input
// or listed in NOT_INPUTS with the reason it cannot change a hash.
import { describe, expect, test } from 'bun:test'
import { existsSync, readFileSync, statSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { PROTOTYPE_CONTENT } from '../vite.prototype.config'

const FRONTEND = join(import.meta.dir, '..')
const ROOT = join(FRONTEND, '..')
const GATE = JSON.parse(readFileSync(join(ROOT, 'docs', 'design', 'gate.json'), 'utf8')) as { digestInputs: string[] }

/** gate.py's glob_regex: `**` crosses directories, `*` and `?` do not. */
function globRegex(pattern: string): RegExp {
  let out = ''
  for (let i = 0; i < pattern.length; ) {
    if (pattern.startsWith('**/', i)) {
      out += '(?:.*/)?'
      i += 3
    } else if (pattern.startsWith('**', i)) {
      out += '.*'
      i += 2
    } else if (pattern[i] === '*') {
      out += '[^/]*'
      i++
    } else if (pattern[i] === '?') {
      out += '[^/]'
      i++
    } else {
      out += pattern[i].replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
      i++
    }
  }
  return new RegExp(`^${out}$`)
}
const isInput = (rel: string) => GATE.digestInputs.some((p) => globRegex(p).test(rel))

/** Where the build and every hash start. */
const ENTRIES = [
  'frontend/vite.prototype.config.ts',
  'frontend/postcss.config.cjs',
  'frontend/tailwind.config.ts',
  'frontend/prototype/main.tsx',
  'frontend/tools/build-prototype.mjs',
  'frontend/tools/snapshot.mjs',
  'frontend/playwright.design.config.ts',
  'frontend/tools/design-setup.ts',
  'frontend/tools/snapshot.pw.ts',
  'frontend/tools/extract.ts',
]

/** Reached from an entry, but unable to change a build or a hash. */
const NOT_INPUTS: Record<string, string> = {
  'frontend/tools/results.ts': "design-setup's teardown: the summary line of check-states and flows results",
  'frontend/tools/checks/align.ts': 'a state check: stateRunner.checkState only, never on the snapshot path',
  'frontend/tools/checks/centred.ts': 'a state check: stateRunner.checkState only, never on the snapshot path',
  'frontend/tools/checks/clip.ts': 'a state check: stateRunner.checkState only, never on the snapshot path',
  'frontend/tools/checks/dom.ts': 'a state check: stateRunner.checkState only, never on the snapshot path',
  'frontend/tools/checks/focus.ts': 'a state check: stateRunner.checkState only, never on the snapshot path',
  'frontend/tools/checks/forced.ts': 'a state check: stateRunner.checkState only, never on the snapshot path',
  'frontend/tools/checks/occlusion.ts': 'a state check: stateRunner.checkState only, never on the snapshot path',
  'frontend/tools/checks/orphans.ts': 'a state check: stateRunner.checkState only, never on the snapshot path',
  'frontend/tools/checks/subject.ts': 'a state check: stateRunner.checkState only, never on the snapshot path',
}

const IMPORT = /(?:\bfrom\s*|\bimport\s*\(?\s*|\brequire\s*\(\s*)(['"])(\.{1,2}\/[^'"]+)\1/g
const CODE = /\.(?:[cm]?[jt]sx?)$/
const EXTS = ['', '.ts', '.tsx', '.mjs', '.cjs', '.js', '.json', '/index.ts', '/index.tsx']

function resolveImport(from: string, spec: string): string {
  const base = resolve(dirname(join(ROOT, from)), spec.split('?')[0])
  for (const ext of EXTS) {
    const p = base + ext
    if (existsSync(p) && statSync(p).isFile()) return relative(ROOT, p)
  }
  throw new Error(`${from}: cannot resolve ${spec}`)
}

/** Every local file the entries reach through relative imports (code files are followed). */
function reached(): Set<string> {
  const seen = new Set<string>()
  const queue = [...ENTRIES]
  while (queue.length > 0) {
    const file = queue.pop()!
    if (seen.has(file)) continue
    seen.add(file)
    if (!CODE.test(file)) continue
    for (const m of readFileSync(join(ROOT, file), 'utf8').matchAll(IMPORT)) queue.push(resolveImport(file, m[2]))
  }
  return seen
}

describe('digestInputs (docs/design/gate.json)', () => {
  const files = reached()

  test('every entry point is a digest input', () => {
    for (const e of ENTRIES) expect(isInput(e), e).toBe(true)
  })

  test('every module an entry point reaches is a digest input, or listed with its reason', () => {
    const missing = [...files].filter((f) => !isInput(f) && !(f in NOT_INPUTS)).sort()
    expect(missing).toEqual([])
  })

  test('NOT_INPUTS lists only modules that are reached and are not digest inputs', () => {
    for (const f of Object.keys(NOT_INPUTS)) {
      expect(files.has(f), `${f} is no longer reached`).toBe(true)
      expect(isInput(f), `${f} is a digest input`).toBe(false)
    }
  })

  test('the build script and the design context are digest inputs', () => {
    for (const f of ['frontend/tools/build-prototype.mjs', 'frontend/tools/designPaths.ts', 'frontend/tools/stateRunner.ts', 'frontend/tools/checks/network.ts', 'frontend/tools/serve.mjs']) {
      expect(files.has(f), f).toBe(true)
      expect(isInput(f), f).toBe(true)
    }
  })

  // A glob a tool reads is an input too: Tailwind's content decides which
  // rules the prototype's stylesheet holds.
  test("every Tailwind content glob of the prototype build lies inside digestInputs", () => {
    const positive = PROTOTYPE_CONTENT.filter((g) => !g.startsWith('!')).map((g) => `frontend/${g}`)
    expect(positive.length).toBeGreaterThan(0)
    for (const glob of positive) {
      // Every path the glob can ever match (any file under its static base) is an input…
      const base = glob.slice(0, glob.search(/[*?{[]/))
      expect(base.endsWith('/'), glob).toBe(true)
      expect(isInput(`${base}any/depth/file.tsx`) && isInput(`${base}file.html`), `${glob}: ${base}** is not a digest input`).toBe(true)
      // …and so is every file it matches today.
      const matched = Array.from(new Bun.Glob(glob.slice('frontend/'.length)).scanSync({ cwd: FRONTEND })).map((f) => `frontend/${f}`)
      expect(matched.length, glob).toBeGreaterThan(0)
      for (const f of matched) expect(isInput(f), f).toBe(true)
    }
  })

  test('the prototype content leaves out test files only, and not the app', () => {
    const negative = PROTOTYPE_CONTENT.filter((g) => g.startsWith('!'))
    expect(negative).toEqual(['!**/*.test.ts', '!**/*.test.tsx'])
    expect(PROTOTYPE_CONTENT.some((g) => /^(?:\.\/)?(?:index\.html|src\/\*\*)/.test(g))).toBe(false)
  })

  test('the glob matches as gate.py does', () => {
    expect(globRegex('frontend/src/design/**').test('frontend/src/design/a/b.tsx')).toBe(true)
    expect(globRegex('frontend/*.ts').test('frontend/a/b.ts')).toBe(false)
    expect(globRegex('**/*.pw.ts').test('frontend/tools/x.pw.ts')).toBe(true)
    expect(globRegex('**/*.pw.ts').test('x.pw.ts')).toBe(true)
  })
})
