// Terms that must never appear in this repository's text files, stored as
// salted hashes so the list itself discloses nothing. To add a term:
// `printf … | shasum -a 256` as documented in docs/design/README.md.
import { expect, test } from 'bun:test'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

const SALT = 'choix-privacy-v1'

/** sha256(SALT + ':' + normalised term), normalised as in `normalise`. */
const DENY_HASHES: string[] = [
  '2015c35e6d13e1849a22afc50aadd59e35d14381a50f3f33b07f79ec874c2cb0',
  '2a129f0b21fc54b6a370b68aeca4e2291d7900c3d95c95e71e81a8aadf5efcef',
  '2e9bdee8ed3eb772a1342e183aecb35497e1454876c6141561c678d133b8d156',
  '4995cf9eab15406349aec23d9794c43579a500ec053a007f75bf7853c29a8cf1',
  '5f76d27d6097b3faebdc706e53cf3b624ff43829a01601736687557ff38a5f23',
  '7070434a739001e5659e9ad8878df86947c10d7b6f2f4778eb83e1c25516d7b2',
  'c985c2da7c7ee4d6d81e588c2581c05fcc1a07f015c3550ec80db5740ad64a9d',
  'f4a6de277e215a4effa38dbe24cd02082ec3a5c9f5fe0de78bf8af2200dc41cb',
]

const MAX_BYTES = 2 * 1024 * 1024
/**
 * Dependency lock and integrity files are not scanned: their base64
 * integrity strings split into random short tokens, which would give false
 * hits, and they hold nothing written by hand.
 */
const LOCK_FILE = /(?:^|\/)(?:[^/]*\.lock|go\.sum|package-lock\.json)$/

/** Whether a file is scanned: any file that is not a lock file and not binary (a NUL byte in its first 8 KB). */
function scannable(rel: string, head: Buffer): boolean {
  return !LOCK_FILE.test(rel) && !head.subarray(0, 8192).includes(0)
}

/** NFC, lower-case, inner whitespace collapsed to one space. */
const normalise = (term: string) => term.normalize('NFC').toLowerCase().trim().replace(/\s+/g, ' ')

const hashTerm = (term: string) => createHash('sha256').update(`${SALT}:${normalise(term)}`).digest('hex')

/**
 * The line numbers (1-based) of `text` holding a denied term: every token
 * (a run of letters and digits, any script) and every pair of adjacent
 * tokens joined by one space is hashed and looked up, which is a whole-word,
 * case-insensitive match for one- and two-word terms. `seen` caches the
 * verdict per distinct token or pair across calls.
 */
function hitLines(text: string, deny: Set<string>, seen: Map<string, boolean>): number[] {
  const denied = (s: string) => {
    let v = seen.get(s)
    if (v === undefined) {
      v = deny.has(createHash('sha256').update(`${SALT}:${s}`).digest('hex'))
      seen.set(s, v)
    }
    return v
  }
  const hits: number[] = []
  text.split('\n').forEach((line, i) => {
    const tokens = line.normalize('NFC').toLowerCase().match(/[\p{L}\p{N}]+/gu) ?? []
    for (let t = 0; t < tokens.length; t++) {
      if (denied(tokens[t]) || (t + 1 < tokens.length && denied(`${tokens[t]} ${tokens[t + 1]}`))) {
        hits.push(i + 1)
        return
      }
    }
  })
  return hits
}

/** Every tracked (and not-ignored untracked) file, relative to the repository root. */
function repositoryFiles(): { root: string; files: string[] } {
  const git = (args: string[]) => spawnSync('git', args, { cwd: import.meta.dir, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
  const top = git(['rev-parse', '--show-toplevel'])
  if (top.error || top.status !== 0) {
    throw new Error(`privacy scan needs git to list the repository's files: ${top.error?.message ?? top.stderr}`)
  }
  const root = top.stdout.trim()
  const ls = spawnSync('git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard'], {
    cwd: root,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  })
  if (ls.error || ls.status !== 0) throw new Error(`git ls-files failed: ${ls.error?.message ?? ls.stderr}`)
  return { root, files: [...new Set(ls.stdout.split('\0').filter(Boolean))] }
}

test('no denied term appears in any non-binary file of the repository', () => {
  const { root, files } = repositoryFiles()
  const deny = new Set(DENY_HASHES)
  const seen = new Map<string, boolean>()
  const hits: string[] = []
  let scanned = 0
  for (const rel of files) {
    if (LOCK_FILE.test(rel)) continue
    const abs = join(root, rel)
    let size: number
    try {
      const st = statSync(abs)
      if (!st.isFile()) continue
      size = st.size
    } catch {
      continue // listed but deleted in the working tree
    }
    if (size > MAX_BYTES) continue
    const buf = readFileSync(abs)
    if (!scannable(rel, buf)) continue
    scanned++
    for (const n of hitLines(buf.toString('utf8'), deny, seen)) hits.push(`${rel}:${n}`)
  }
  expect(scanned).toBeGreaterThan(100)
  expect(hits).toEqual([])
})

test('every non-binary file is scanned except dependency lock and integrity files', () => {
  const text = Buffer.from('plain text\n')
  for (const rel of ['Makefile', '.gitignore', 'go.mod', 'internal/store/migrations/001_initial.sql', 'README', 'a/b.ts', 'x.yml']) {
    expect(scannable(rel, text), rel).toBe(true)
  }
  for (const rel of ['frontend/bun.lock', 'go.sum', 'package-lock.json', 'web/yarn.lock', 'a/Cargo.lock']) {
    expect(scannable(rel, text), rel).toBe(false)
  }
  expect(scannable('assets/icon.png', Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x00, 0x01]))).toBe(false)
})

test('the matcher finds whole words in any case, and only whole words', () => {
  // Invented words, hashed here, so the test discloses nothing.
  const deny = new Set([hashTerm('Marlowick'), hashTerm('Q9'), hashTerm('Válé  Dörrin')])
  const lines = (text: string) => hitLines(text, deny, new Map())

  expect(lines('the MARLOWICK trip')).toEqual([1])
  expect(lines('marlowick')).toEqual([1])
  expect(lines('/Volumes/Q9/Trips')).toEqual([1])
  expect(lines('first\nsecond q9\nthird')).toEqual([2])

  expect(lines('hash 9aQ9b')).toEqual([])
  expect(lines('Marlowicks and preMarlowick')).toEqual([])
  expect(lines('Q90 and Q9x')).toEqual([])

  expect(lines('válé dörrin, 2026')).toEqual([1])
  expect(lines('VÁLÉ DÖRRIN')).toEqual([1])
  expect(lines('Válé Dörrin'.normalize('NFD'))).toEqual([1])
  expect(lines('Válé\tDörrin')).toEqual([1])
  expect(lines('Vale Dorrin')).toEqual([]) // diacritics are part of the spelling
  expect(lines('Válé and Dörrin')).toEqual([])
})

test('a term is hashed after NFC, lower-casing and whitespace collapsing', () => {
  expect(hashTerm(' Válé \n Dörrin ')).toBe(hashTerm('válé dörrin'))
  expect(hashTerm('Válé Dörrin'.normalize('NFD'))).toBe(hashTerm('válé dörrin'))
  expect(hashTerm('q9')).toBe(createHash('sha256').update('choix-privacy-v1:q9').digest('hex'))
})
