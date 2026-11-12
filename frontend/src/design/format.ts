// Pure text formatting shared by components and screens. en-US throughout:
// the app is English-only, and fixed formatting keeps prototype renders
// identical across machines.

const LOCALE = 'en-US'
const NBSP = ' '

/** `plural(5218, 'photo', 'photos')` → `'5,218 photos'`. */
export function plural(n: number, one: string, many: string): string {
  return `${n.toLocaleString(LOCALE)} ${n === 1 ? one : many}`
}

type DayParts = { year: string; month: string; day: string }

function dayParts(iso: string, timeZone: string): DayParts {
  const parts = new Intl.DateTimeFormat(LOCALE, { year: 'numeric', month: 'short', day: 'numeric', timeZone })
    .formatToParts(new Date(iso))
  const get = (type: Intl.DateTimeFormatPartTypes) => parts.find((p) => p.type === type)?.value ?? ''
  return { year: get('year'), month: get('month'), day: get('day') }
}

/**
 * A shoot's date range, as the Library row shows it:
 * `'Mar 14 – 16'`, `'Apr 28 – May 9'`, `'Dec 28, 2025 – Jan 3, 2026'`,
 * `'Sep 6'`, and `''` for an undated folder. The year appears only when the
 * range crosses one; the month header above the row already names it.
 */
export function dateRange(first: string | null, last: string | null, timeZone = 'UTC'): string {
  const a = first ?? last
  const b = last ?? first
  if (a === null || b === null) return ''
  const s = dayParts(a, timeZone)
  const e = dayParts(b, timeZone)
  if (s.year !== e.year) return `${s.month} ${s.day}, ${s.year} – ${e.month} ${e.day}, ${e.year}`
  if (s.month !== e.month) return `${s.month} ${s.day} – ${e.month} ${e.day}`
  if (s.day !== e.day) return `${s.month} ${s.day} – ${e.day}`
  return `${s.month} ${s.day}`
}

/** A `YYYY-MM` month as the Library timeline names it: `'October 2026'`. */
export function monthLabel(yyyyMm: string): string {
  return new Intl.DateTimeFormat(LOCALE, { month: 'long', year: 'numeric', timeZone: 'UTC' }).format(
    new Date(`${yyyyMm}-01T00:00:00Z`),
  )
}

/** Replaces the home folder prefix with `~`. */
export function abbreviatePath(p: string, home: string): string {
  const h = home.length > 1 ? home.replace(/\/+$/, '') : home
  if (p === h) return '~'
  if (h !== '' && p.startsWith(`${h}/`)) return `~${p.slice(h.length)}`
  return p
}

/**
 * Binds the last two words with a no-break space so a wrapped text block
 * never ends on a single short word (spec §4.6). Texts of fewer than four
 * words are left alone; running it twice changes nothing.
 */
export function noOrphan(text: string): string {
  const m = /(\s)\S+\s*$/.exec(text)
  if (!m || m[1] === NBSP) return text
  if (text.split(/\s+/).filter(Boolean).length < 4) return text
  return text.slice(0, m.index) + NBSP + text.slice(m.index + 1)
}

// ── Quality rating ─────────────────────────────────────────────────────────

export const formatQuality = (q: number): string => q.toFixed(1)
/** Spoken form, appended to a thumbnail's accessible name. */
export const qualityLabel = (q: number): string => `quality ${formatQuality(q)} of 10`

// ── Pipeline stages ────────────────────────────────────────────────────────

const STAGE_LABELS: Record<string, string> = {
  discover: 'Discover files',
  metadata: 'Read EXIF',
  thumb: 'Generate thumbs',
  analyze: 'Compute embeddings',
  cluster: 'Cluster',
}
export const STAGE_ORDER = ['discover', 'metadata', 'thumb', 'analyze', 'cluster'] as const
export const stageLabel = (stage: string): string => STAGE_LABELS[stage] ?? stage

// ── Menu accelerators ──────────────────────────────────────────────────────

const MODIFIERS: [name: string, glyph: string][] = [
  ['Ctrl', '⌃'],
  ['Alt', '⌥'],
  ['Option', '⌥'],
  ['Shift', '⇧'],
  ['CmdOrCtrl', '⌘'],
  ['Cmd', '⌘'],
  ['Command', '⌘'],
]
const MODIFIER_ORDER = ['⌃', '⌥', '⇧', '⌘']
const KEYS: Record<string, string> = {
  backspace: '⌫',
  delete: '⌦',
  return: '↩',
  enter: '↩',
  escape: 'Esc', // one Escape label everywhere (key caps, menus)
  tab: '⇥',
  space: 'Space',
  plus: '+',
  '-': '−',
  minus: '−',
  left: '←',
  right: '→',
  up: '↑',
  down: '↓',
}

/**
 * A Wails accelerator (`CmdOrCtrl+Shift+P`) as macOS shows it (`⇧⌘P`):
 * modifiers in the order ⌃ ⌥ ⇧ ⌘, then the key. Labels are always macOS
 * glyphs, never derived from the user agent.
 */
export function acceleratorLabel(acc: string): string {
  const parts = acc.split('+')
  const key = parts.pop() ?? ''
  const mods = new Set<string>()
  for (const p of parts) {
    const hit = MODIFIERS.find(([name]) => name.toLowerCase() === p.toLowerCase())
    if (hit) mods.add(hit[1])
  }
  const k = KEYS[key.toLowerCase()] ?? key.toUpperCase()
  return MODIFIER_ORDER.filter((g) => mods.has(g)).join('') + k
}

/** A path's last component (the file name). */
export const baseName = (path: string): string => path.slice(path.lastIndexOf('/') + 1)

/** The accessible name of a Focus image: file name plus its mark (v1 imageLabel). */
export const imageLabel = (m: { path: string; picked: boolean; rejected: boolean }): string =>
  `${baseName(m.path)}${m.picked ? ', picked' : m.rejected ? ', rejected' : ''}`

/** A zoom factor as a percentage (`2` → `200%`). */
export const zoomLabel = (z: number): string => `${Math.round(z * 100)}%`
