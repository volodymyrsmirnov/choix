// No duplicate status (the owner's rule): a status lives on the element it
// describes, and the Library title bar shows one only when nothing else on
// the screen does. Scans and rating passes show in their folder's row, so
// the bar shows no status at all.
import { expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import type { FolderSummary, QualityStatus, ScanProgress } from '../../types'
import { LibraryScreen } from './LibraryScreen'
import type { LibraryProps } from './props'

const noop = () => {}
const progress: ScanProgress = { stage: 'thumb', done: 262, total: 420, failed: 0, state: 'running' }
const folder = (id: string, name: string, scanning = false): FolderSummary => ({
  id,
  name,
  parentPath: '/Trips',
  state: scanning ? 'scanning' : 'ready',
  month: '2026-10',
  firstCaptured: null,
  lastCaptured: null,
  ...(scanning ? { scan: progress } : {}),
  photos: 10,
  videos: 0,
  clusters: 2,
  picked: 0,
  rejected: 0,
  previews: [],
})

const props = (over: Partial<LibraryProps> = {}): LibraryProps => ({
  folders: [folder('a', 'Alpha'), folder('b', 'Bravo')],
  dropActive: false,
  toasts: [],
  selectedId: null,
  scan: null,
  menuFor: null,
  removePrompt: null,
  quality: null,
  onAdd: noop,
  onOpen: noop,
  onSelect: noop,
  onOpenPicks: noop,
  onMenu: noop,
  onRemove: noop,
  onConfirmRemove: noop,
  onSettings: noop,
  onHelp: noop,
  onUndoToast: noop,
  onDismissToast: noop,
  ...over,
})

const render = (over: Partial<LibraryProps> = {}) => renderToStaticMarkup(<LibraryScreen {...props(over)} />)
/** The title bar's markup. */
const bar = (html: string) => html.slice(html.indexOf('<header'), html.indexOf('</header>'))
/** One folder row's markup. */
const row = (html: string, id: string) => {
  const at = html.indexOf(`data-folder="${id}"`)
  const next = html.indexOf('data-folder="', at + 1)
  return html.slice(at, next < 0 ? undefined : next)
}

const status = (q: Partial<QualityStatus>): QualityStatus => ({
  enabled: true,
  state: 'scoring',
  active: true,
  done: 0,
  total: 0,
  rated: 0,
  ...q,
})

test('the Library bar shows no brand mark and no visible title, only a visually hidden heading', () => {
  const h = bar(render())
  expect(h).not.toContain('<img')
  expect(h).toContain('<h1 class="sr-only">Library</h1>')
})

test('nothing to say: the bar has no indicator slot and no divider', () => {
  const h = bar(render())
  expect(h).not.toContain('w-px h-[18px]')
  expect(h).not.toContain('data-testid="scan-progress"')
  expect(h).not.toContain('data-testid="quality-progress"')
})

test('scans show only in their rows, one or several', () => {
  const folders = [folder('a', 'Alpha', true), folder('b', 'Bravo', true)]
  for (const scan of [{ folder: 'Alpha', progress }, { count: 2 }, null] as LibraryProps['scan'][]) {
    const html = render({ folders, scan })
    expect(bar(html)).not.toContain('data-testid="scan-progress"')
    expect(bar(html)).not.toContain('Alpha')
    expect(row(html, 'a')).toContain('Generating thumbnails')
    expect(row(html, 'b')).toContain('Generating thumbnails')
  }
})

test('a rating pass shows in its folder’s row, with its own counts; never in the bar', () => {
  const quality = status({ done: 905, total: 2768, folder_id: 'b', passes: [{ folder_id: 'b', done: 905, total: 2768 }] })
  const html = render({ quality })
  expect(bar(html)).not.toContain('data-testid="quality-progress"')
  expect(row(html, 'b')).toContain('Rating quality')
  expect(row(html, 'b')).toContain('32%')
  expect(row(html, 'a')).not.toContain('Rating quality')
})

test('two passes, one just begun (0 / 0): each row shows its own pass; the new one without a percentage yet', () => {
  const quality = status({
    folder_id: 'a',
    passes: [
      { folder_id: 'b', done: 905, total: 2768 },
      { folder_id: 'a', done: 0, total: 0 },
    ],
  })
  const html = render({ quality })
  expect(bar(html)).not.toContain('data-testid="quality-progress"')
  expect(row(html, 'b')).toContain('32%')
  expect(row(html, 'a')).toContain('Rating quality')
  expect(row(html, 'a')).not.toContain('%</span>')
})

test('a folder still scanning (its job not finished) whose rating pass runs shows the rating, not the last scan stage', () => {
  const done: ScanProgress = { stage: 'cluster', done: 420, total: 420, failed: 0, state: 'running' }
  const folders = [{ ...folder('a', 'Alpha', true), scan: done }, folder('b', 'Bravo')]
  const quality = status({ done: 50, total: 200, folder_id: 'a', passes: [{ folder_id: 'a', done: 50, total: 200 }] })
  const html = render({ folders, quality })
  expect(row(html, 'a')).toContain('Rating quality')
  expect(row(html, 'a')).toContain('25%')
  expect(row(html, 'a')).not.toContain('Grouping')
})

test('a scan’s failed files show in its row, as the title bar used to show them', () => {
  const failing: ScanProgress = { stage: 'thumb', done: 325, total: 420, failed: 7, state: 'running' }
  const html = render({ folders: [{ ...folder('a', 'Alpha', true), scan: failing }, folder('b', 'Bravo', true)] })
  expect(row(html, 'a')).toContain('7 failed')
  expect(row(html, 'b')).not.toContain('failed')
})

test('the bar shows no quality indicator, whatever the worker is doing', () => {
  for (const state of ['disabled', 'waiting', 'scoring', 'idle'] as const) {
    const quality = status({ state, done: 50, total: 200, folder_id: 'a', passes: [{ folder_id: 'a', done: 50, total: 200 }] })
    expect(bar(render({ quality })), state).not.toContain('data-testid="quality-progress"')
  }
})

test('with no folders, once the list has loaded: the drop zone, never a welcome panel', () => {
  const html = render({ folders: [] })
  expect(html).toContain('data-drop-zone')
  expect(html).not.toContain('Welcome to Choix')
  expect(html).not.toContain('data-model')
  expect(html).not.toContain('Loading the Library')
})

test('no download banner above the folders', () => {
  const html = render()
  expect(html).not.toContain('data-testid="model-banner"')
  expect(html).not.toContain('download')
})

// ── The toolbar: search, filter and order (the owner's round 2) ─────────────

const trip = (id: string, name: string, over: Partial<FolderSummary> = {}): FolderSummary => ({ ...folder(id, name), ...over })
const TRIPS = [
  trip('lisbon', 'Lisbon', { month: '2026-10', firstCaptured: '2026-10-04T10:00:00Z', picked: 3 }),
  trip('kyoto', 'Kyoto', { month: '2026-09', firstCaptured: '2026-09-12T10:00:00Z', state: 'offline' }),
  trip('atlas', 'Faroe Islands', { parentPath: '/Volumes/Atlas', month: '2026-08', firstCaptured: '2026-08-01T10:00:00Z' }),
]
const rows = (html: string) => [...html.matchAll(/data-folder="([^"]+)"/g)].map((m) => m[1])
/** The markup with noOrphan's no-break spaces read as spaces. */
const plainText = (html: string) => html.replace(/\u00a0/g, ' ')
/** The toolbar's markup: from it to the first row, or the empty state, or the end. */
const toolbar = (html: string) => {
  const at = html.indexOf('data-testid="library-toolbar"')
  if (at < 0) return ''
  const ends = ['data-folder=', 'data-testid="folders-empty"'].map((m) => html.indexOf(m, at)).filter((i) => i > 0)
  return html.slice(html.lastIndexOf('<', at), ends.length > 0 ? Math.min(...ends) : undefined)
}

test('the toolbar sits at the end of the summary line whenever the Library lists folders: search (⌘F), filter and order', () => {
  const t = toolbar(render({ folders: TRIPS }))
  expect(t).toContain('data-testid="filter-input"')
  expect(t).toContain('placeholder="Search folders"')
  expect(t).toContain('aria-keyshortcuts="Meta+F"')
  expect(t).toContain('⌘F')
  expect(t).toContain('data-testid="library-show"')
  expect(t).toContain('All folders')
  expect(t).toContain('data-testid="library-order"')
  expect(t).toContain('Newest first')
})

test('no toolbar while loading or on the empty drop zone', () => {
  expect(toolbar(render({ folders: null }))).toBe('')
  expect(toolbar(render({ folders: [] }))).toBe('')
})

test('without a filter from the app: today’s timeline (newest first, every folder)', () => {
  const html = render({ folders: TRIPS })
  expect(rows(html)).toEqual(['lisbon', 'kyoto', 'atlas'])
  expect(html).toContain('data-month')
})

test('a search shows the folders whose name or location matches, under their months, and counts them', () => {
  const html = render({ folders: TRIPS, filter: { query: 'atlas', order: 'date-desc', show: 'all' } })
  expect(rows(html)).toEqual(['atlas'])
  expect(html).toContain('data-month')
  expect(html).toContain('1 of 3 folders')
})

test('a search that matches nothing says so, with a way back', () => {
  const html = plainText(render({ folders: TRIPS, filter: { query: 'harbour', order: 'date-desc', show: 'all' } }))
  expect(rows(html)).toEqual([])
  expect(html).toContain('data-testid="folders-empty"')
  expect(html).toContain('No folders match “harbour”')
  expect(html).toContain('Clear Search')
  // With a filter on as well, the way back clears both.
  const both = render({ folders: TRIPS, filter: { query: 'harbour', order: 'date-desc', show: 'attention' } })
  expect(both).toContain('Show All Folders')
})

test('a filter with nothing to show says what it looked for', () => {
  const html = plainText(render({ folders: TRIPS.filter((x) => x.state !== 'offline'), filter: { query: '', order: 'date-desc', show: 'attention' } }))
  expect(html).toContain('No folder needs attention')
  expect(html).toContain('Show All Folders')
})

test('the filter shows only its folders (needs attention: offline or in error)', () => {
  const html = render({ folders: TRIPS, filter: { query: '', order: 'date-desc', show: 'attention' } })
  expect(rows(html)).toEqual(['kyoto'])
  expect(toolbar(html)).toContain('Needs attention')
})

test('name order: one list, A to Z, without month headers', () => {
  const html = render({ folders: TRIPS, filter: { query: '', order: 'name', show: 'all' } })
  expect(rows(html)).toEqual(['atlas', 'kyoto', 'lisbon'])
  expect(html).not.toContain('data-month')
  expect(toolbar(html)).toContain('Name')
})

test('oldest first keeps the months, oldest first', () => {
  const html = render({ folders: TRIPS, filter: { query: '', order: 'date-asc', show: 'all' } })
  expect(rows(html)).toEqual(['atlas', 'kyoto', 'lisbon'])
  expect(html).toContain('data-month')
})

test('the open order and filter menus list their choices', () => {
  const order = render({ folders: TRIPS, openMenu: 'order' })
  for (const label of ['Capture date — newest first', 'Capture date — oldest first', 'Name — A to Z', 'Recently added']) expect(order).toContain(label)
  const show = render({ folders: TRIPS, openMenu: 'show' })
  for (const label of ['All folders', 'Not reviewed yet', 'Review started', 'Needs attention']) expect(show).toContain(label)
})

test('a new folder still processing sits under Processing, above the newest month, with a folder count', () => {
  const fresh = trip('new', 'New trip', { state: 'scanning', month: null, firstCaptured: null, lastCaptured: null, photos: 0, scan: progress })
  const html = plainText(render({ folders: [...TRIPS, fresh] }))
  expect(rows(html)[0]).toBe('new')
  const at = html.indexOf('aria-label="Processing"')
  expect(at).toBeGreaterThan(-1)
  expect(at).toBeLessThan(html.indexOf('aria-label="October 2026"'))
  expect(html.slice(at, html.indexOf('data-folder=', at))).toContain('>1 folder<')
})
