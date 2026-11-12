// The Library route rendered from a shell on fakes (react-dom/server: no
// effects run, so the stores are started and loaded first). It pins that
// the container renders the design's LibraryScreen as the only thing in
// AppRoot — no wrapper, nothing over the title bar — and that the shell's
// stores reach the screen.
import { describe, expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { AppRoot } from '../../design/components'
import type { QualityStatus, SettingsBody } from '../../design/types'
import { createActions } from '../shell/actions'
import { ShellContext, type Flag, type Shell } from '../shell/context'
import type { AppEvents, On } from '../shell/events'
import type { FolderSession } from '../shell/folderSession'
import { createQualityStore } from '../shell/qualityStore'
import { createRouter } from '../shell/router'
import { createScanStore } from '../shell/scanStore'
import { createSettingsStore } from '../shell/settingsStore'
import { createToasts } from '../shell/toasts'
import { LibraryRoute } from '.'
import { filterMemory } from './libraryFilter'
import { createLibraryStore, type FolderRowData } from './libraryStore'

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

function fakeBus() {
  const handlers = new Map<string, Set<(data: unknown) => void>>()
  const on: On = (name, handler) => {
    const set = handlers.get(name) ?? new Set()
    set.add(handler as (data: unknown) => void)
    handlers.set(name, set)
    return () => set.delete(handler as (data: unknown) => void)
  }
  const emit = <K extends keyof AppEvents>(name: K, data: AppEvents[K]) => {
    for (const h of [...(handlers.get(name) ?? [])]) h(data)
  }
  return { on, emit }
}

const row = (id: string, name: string, over: Partial<FolderRowData> = {}): FolderRowData => ({
  id,
  name,
  parentPath: '~/Pictures',
  state: 'ready',
  month: '2026-05',
  firstCaptured: '2026-05-02T10:00:00Z',
  lastCaptured: '2026-05-03T10:00:00Z',
  photos: 12,
  videos: 0,
  clusters: 4,
  picked: 0,
  rejected: 0,
  previews: [],
  scan: null,
  added_at: 1_700_000_000,
  ...over,
})

const IDLE: QualityStatus = { enabled: true, state: 'idle', active: false, done: 0, total: 0, rated: 0 }
const BODY: SettingsBody = {
  bucket_size: 600,
  similarity: 0.92,
  picks_dir: 'picks',
  advance_on_action: false,
  hide_rejected_photos: false,
  cross_device_merging: false,
  quality_rating: true,
  visual_grouping: true,
}

const flag = (): Flag => ({ get: () => false, set: () => {}, subscribe: () => () => {} })

/** A shell on fakes, its stores started and loaded. */
async function shellWith(rows: FolderRowData[], quality: QualityStatus = IDLE) {
  const bus = fakeBus()
  const toasts = createToasts()
  const router = createRouter({ getHash: () => '#/', setHash: () => {}, onHashChange: () => () => {} })
  const list = () => Promise.resolve(rows)
  const ok = () => Promise.resolve()
  const actions = createActions({
    catalog: { Undo: ok },
    library: { AddFolders: () => Promise.resolve([]), Rescan: ok, Remove: () => Promise.resolve(false) },
    system: { OpenPicksFolder: ok, RevealFolder: ok, OpenHelp: ok, SetMenuContext: ok },
    router,
    toggleShortcuts: () => {},
    document: { activeElement: null, execCommand: () => false },
    onError: () => {},
  })
  const shell: Shell = {
    router,
    session: {} as FolderSession,
    actions,
    toasts,
    scan: createScanStore({ list, on: bus.on }),
    quality: createQualityStore({ qualityStatus: () => Promise.resolve(quality), on: bus.on }),
    settings: createSettingsStore({ get: () => Promise.resolve(BODY) }),
    library: createLibraryStore({ list, on: bus.on, toasts }),
    sheet: flag(),
    start: () => () => {},
  }
  for (const s of [shell.scan, shell.quality, shell.settings, shell.library]) s.start()
  await flush()
  return { shell, bus }
}

/** What App renders for the library route: the route module inside AppRoot. */
const render = (shell: Shell) =>
  renderToStaticMarkup(
    <ShellContext.Provider value={shell}>
      <AppRoot>
        <LibraryRoute />
      </AppRoot>
    </ShellContext.Provider>,
  )

/** The screen's root (LibraryScreen's outer element) directly in AppRoot, the title bar its first child. */
const SCREEN_START = /^<div data-app-root="true" class="[^"]*"><div class="relative flex flex-col h-full min-h-0"><header data-titlebar="true"/

function expectTitleBarUncovered(html: string) {
  // 1. The container adds no element around the screen or before the title bar.
  expect(html).toMatch(SCREEN_START)
  // 2. Its controls opt out of the drag region (Add Folder, Help, Settings).
  const header = html.slice(0, html.indexOf('</header>'))
  expect(header).toContain('data-titlebar-control')
  expect(header).toContain('Add Folder')
  // 3. After the bar: the page body, then the toast layer, which lets clicks through.
  const after = html.slice(html.indexOf('</header>') + '</header>'.length)
  expect(after.startsWith('<main class="relative flex-1 min-h-0">')).toBe(true)
  const tail = after.slice(after.lastIndexOf('</main>') + '</main>'.length)
  expect(tail).toMatch(/^<div data-overlay="toasts" class="[^"]*pointer-events-none[^"]*"/)
  // 4. No replica overlay anywhere (the native menu and alert replace them).
  expect(html).not.toContain('data-overlay="folder-menu"')
  expect(html).not.toContain('data-overlay="remove-folder"')
}

describe('LibraryContainer', () => {
  test('the title bar is rendered by the design and nothing covers it', async () => {
    // Timeline, with a toast up and a folder scanning (the indicator slot in the bar).
    const timeline = await shellWith([row('a', 'Harbour walk', { state: 'scanning', scan: { stage: 'thumb', done: 2, total: 9, failed: 0, state: 'running' } }), row('b', 'Orchard')])
    timeline.shell.toasts.push({ tone: 'info', title: 'Hello' })
    expectTitleBarUncovered(render(timeline.shell))

    // No folders (the drop zone), loading.
    expectTitleBarUncovered(render((await shellWith([])).shell))
    const loading = await shellWith([])
    const never = createLibraryStore({ list: () => new Promise(() => {}), on: fakeBus().on, toasts: createToasts() })
    never.start()
    expectTitleBarUncovered(render({ ...loading.shell, library: never }))
  })

  test('the shell’s stores reach the screen', async () => {
    // Folders: the rows, their scan state (in the row: the title bar shows
    // no status the row shows), the selection.
    const { shell, bus } = await shellWith([row('a', 'Harbour walk'), row('b', 'Orchard')])
    bus.emit('scan:state', { folderId: 'b', state: 'scanning' })
    bus.emit('scan:progress', { folderId: 'b', progress: { stage: 'analyze', done: 4, total: 12, failed: 0, state: 'running' }, phase: 'running' })
    shell.actions.select('a')
    const html = render(shell)
    expect(html).toContain('Harbour walk')
    expect(html.slice(html.indexOf('data-folder="b"'))).toContain('Analyzing')
    expect(html.slice(0, html.indexOf('</header>'))).not.toContain('Orchard')
    const rowA = html.slice(html.indexOf('data-folder="a"'), html.indexOf('data-folder="b"'))
    expect(rowA).toContain('aria-current="true"')
    expect(html.slice(html.indexOf('data-folder="b"'))).not.toContain('aria-current="true"')

    // No folders: the drop zone as soon as the list has loaded (nothing is downloaded).
    const empty = await shellWith([])
    const emptyHtml = render(empty.shell)
    expect(emptyHtml).toContain('data-drop-zone')
    expect(emptyHtml).not.toContain('Welcome to Choix')

    // A rating pass in its folder's row (not in the bar: no duplicate status).
    const scoring = await shellWith([row('a', 'Harbour walk')], {
      ...IDLE,
      state: 'scoring',
      active: true,
      done: 3,
      total: 40,
      folder_id: 'a',
      passes: [{ folder_id: 'a', done: 3, total: 40 }],
    })
    const scoringHtml = render(scoring.shell)
    expect(scoringHtml.slice(scoringHtml.indexOf('data-folder="a"'))).toContain('Rating quality')
    expect(scoringHtml.slice(0, scoringHtml.indexOf('</header>'))).not.toContain('data-testid="quality-progress"')

    // The real combination: the folder is still `scanning` (its job runs the
    // rating pass last) and its pass is listed: the row says Rating quality,
    // not the scan's last stage.
    const rating = await shellWith([row('a', 'Harbour walk')], {
      ...IDLE,
      state: 'scoring',
      active: true,
      done: 10,
      total: 40,
      folder_id: 'a',
      passes: [{ folder_id: 'a', done: 10, total: 40 }],
    })
    rating.bus.emit('scan:state', { folderId: 'a', state: 'scanning' })
    rating.bus.emit('scan:progress', { folderId: 'a', progress: { stage: 'cluster', done: 40, total: 40, failed: 2, state: 'running' }, phase: 'running' })
    const ratingRow = render(rating.shell).slice(render(rating.shell).indexOf('data-folder="a"'))
    expect(ratingRow).toContain('data-folder-state="scanning"')
    expect(ratingRow).toContain('Rating quality')
    expect(ratingRow).not.toContain('Grouping')
    expect(ratingRow).toContain('2 failed')
  })

  test('the toolbar shows with the folders, and Recently added reaches the screen as Go sent it', async () => {
    const { shell } = await shellWith([row('a', 'Harbour walk', { added_at: 1_700_000_100 }), row('b', 'Orchard', { added_at: 1_700_000_200 })])
    const html = render(shell)
    expect(html).toContain('data-testid="library-toolbar"')
    expect(html).toContain('data-testid="filter-input"')
    // Recently added: Orchard (added later) first, one list without a month header.
    filterMemory(shell).set({ query: '', order: 'added', show: 'all' })
    const added = render(shell)
    expect(added.indexOf('data-folder="b"')).toBeLessThan(added.indexOf('data-folder="a"'))
    expect(added).not.toContain('May 2026')
  })

  test('the filter is the page’s for the session: a new mount (back from a folder) shows it again', async () => {
    const { shell } = await shellWith([row('a', 'Harbour walk'), row('b', 'Orchard')])
    expect(render(shell)).toContain('data-folder="a"')
    filterMemory(shell).set({ query: 'orch', order: 'date-desc', show: 'all' })
    const html = render(shell)
    expect(html).not.toContain('data-folder="a"')
    expect(html).toContain('data-folder="b"')
    expect(html).toContain('value="orch"')
    // Another shell (another window session) starts afresh.
    const other = await shellWith([row('a', 'Harbour walk'), row('b', 'Orchard')])
    expect(render(other.shell)).toContain('data-folder="a"')
  })
})
