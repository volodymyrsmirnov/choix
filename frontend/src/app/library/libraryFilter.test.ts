// The Library toolbar's page state in the app (libraryFilter.ts): which rows
// the keys walk (the ones shown, in the order shown), the selection a filter
// hides, the filter after Add Folder, and the filter kept for the session.
import { describe, expect, test } from 'bun:test'
import { LIBRARY_FILTER } from '../../design/screens/library/filter'
import type { FolderSummary, LibraryFilter } from '../../design/types'
import { createLibraryKeyHandler } from './libraryKeys'
import { filterAfterAdd, filterMemory, hidesSelection, settleFilter, visibleIds } from './libraryFilter'
import { createLibraryHandlers } from './libraryStore'

const folder = (id: string, name: string, over: Partial<FolderSummary> = {}): FolderSummary => ({
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
  added_at: 1_700_000_000,
  ...over,
})

// As Library.List answers: capture date, newest first.
const FOLDERS: FolderSummary[] = [
  folder('may', 'Orchard', { month: '2026-05', firstCaptured: '2026-05-02T10:00:00Z', added_at: 1_700_000_300, picked: 2 }),
  folder('apr', 'Harbour walk', { month: '2026-04', firstCaptured: '2026-04-10T10:00:00Z', added_at: 1_700_000_100 }),
  folder('mar', 'Alpine lakes', { month: '2026-03', firstCaptured: '2026-03-01T10:00:00Z', added_at: 1_700_000_200, state: 'offline' }),
  folder('undated', 'Beach day', { month: null, firstCaptured: null, lastCaptured: null, added_at: 1_700_000_050 }),
]

const f = (over: Partial<LibraryFilter>): LibraryFilter => ({ ...LIBRARY_FILTER, ...over })

describe('visibleIds', () => {
  test('no filter: every folder, newest month first, undated last', () => {
    expect(visibleIds(FOLDERS, LIBRARY_FILTER)).toEqual(['may', 'apr', 'mar', 'undated'])
  })
  test('a search keeps only the folders it matches', () => {
    expect(visibleIds(FOLDERS, f({ query: 'ar' }))).toEqual(['may', 'apr'])
    expect(visibleIds(FOLDERS, f({ query: 'nothing like it' }))).toEqual([])
  })
  test('the orders: oldest first, by name, recently added', () => {
    expect(visibleIds(FOLDERS, f({ order: 'date-asc' }))).toEqual(['mar', 'apr', 'may', 'undated'])
    expect(visibleIds(FOLDERS, f({ order: 'name' }))).toEqual(['mar', 'undated', 'apr', 'may'])
    expect(visibleIds(FOLDERS, f({ order: 'added' }))).toEqual(['may', 'mar', 'apr', 'undated'])
  })
  test('Show: needs attention, review started', () => {
    expect(visibleIds(FOLDERS, f({ show: 'attention' }))).toEqual(['mar'])
    expect(visibleIds(FOLDERS, f({ show: 'started' }))).toEqual(['may'])
  })
})

describe('the keys walk the rows shown', () => {
  function keys(filter: LibraryFilter) {
    let selected: string | null = null
    const opened: string[] = []
    const onKey = createLibraryKeyHandler({
      rows: () => visibleIds(FOLDERS, filter),
      selected: () => selected,
      select: (id) => (selected = id),
      reveal: () => {},
      open: (id) => opened.push(id),
      canOpen: () => true,
      blocked: () => false,
    })
    const press = (key: string) =>
      onKey({ key, metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, isComposing: false, defaultPrevented: false, target: null, preventDefault() {} } as unknown as KeyboardEvent)
    return { press, opened, selected: () => selected }
  }

  test('↓ goes through the shown rows in the order shown, and stops at the last', () => {
    const k = keys(f({ order: 'name' }))
    const seen: (string | null)[] = []
    for (let i = 0; i < 5; i++) {
      k.press('ArrowDown')
      seen.push(k.selected())
    }
    expect(seen).toEqual(['mar', 'undated', 'apr', 'may', 'may'])
  })

  test('↩ with nothing selected opens the first row shown', () => {
    const k = keys(f({ query: 'harbour' }))
    k.press('Enter')
    expect(k.opened).toEqual(['apr'])
  })
})

describe('hidesSelection', () => {
  test('a listed folder the filter does not show', () => {
    expect(hidesSelection(FOLDERS, f({ query: 'harbour' }), 'may')).toBe(true)
    expect(hidesSelection(FOLDERS, f({ query: 'harbour' }), 'apr')).toBe(false)
  })
  test('nothing selected, or a folder not listed yet (just added): nothing hidden', () => {
    expect(hidesSelection(FOLDERS, f({ query: 'harbour' }), null)).toBe(false)
    expect(hidesSelection(FOLDERS, f({ query: 'harbour' }), 'new-folder')).toBe(false)
  })
})

describe('filterAfterAdd', () => {
  test('no search and every folder shown: unchanged', () => {
    const order = f({ order: 'name' })
    expect(filterAfterAdd(order, FOLDERS, 'apr')).toBe(order)
  })
  test('a folder the filter already shows: unchanged', () => {
    const search = f({ query: 'harbour' })
    expect(filterAfterAdd(search, FOLDERS, 'apr')).toBe(search)
  })
  test('a folder the filter hides: the search and Show clear, the order stays', () => {
    expect(filterAfterAdd(f({ query: 'harbour', order: 'name' }), FOLDERS, 'may')).toEqual(f({ order: 'name' }))
    expect(filterAfterAdd(f({ show: 'attention', order: 'added' }), FOLDERS, 'may')).toEqual(f({ order: 'added' }))
  })
  test('a folder not listed yet: no decision, unchanged', () => {
    const search = f({ query: 'harbour' })
    expect(filterAfterAdd(search, FOLDERS, 'new-folder')).toBe(search)
  })
})

describe('settleFilter', () => {
  const search = f({ query: 'harbour', order: 'name' })
  test('the added folder not listed yet: it stays pending, nothing changes', () => {
    expect(settleFilter({ folders: FOLDERS, filter: search, selected: 'new', pending: 'new' })).toEqual({ filter: search, pending: 'new', clear: false })
  })
  test('the added folder listed: decided once, then no longer pending', () => {
    const hidden = settleFilter({ folders: FOLDERS, filter: search, selected: 'may', pending: 'may' })
    expect(hidden).toEqual({ filter: f({ order: 'name' }), pending: null, clear: false })
    const shown = settleFilter({ folders: FOLDERS, filter: search, selected: 'apr', pending: 'apr' })
    expect(shown.filter).toBe(search)
    expect(shown).toMatchObject({ pending: null, clear: false })
  })
  test('the selection moved on before the folder was listed: the add is forgotten', () => {
    expect(settleFilter({ folders: FOLDERS, filter: search, selected: 'apr', pending: 'may' })).toEqual({ filter: search, pending: null, clear: false })
  })
  test('nothing pending: a selection the filter hides is cleared', () => {
    expect(settleFilter({ folders: FOLDERS, filter: search, selected: 'may', pending: null })).toEqual({ filter: search, pending: null, clear: true })
  })
})

describe('Add Folder through the picker, in both orders', () => {
  const NEW_MATCH = folder('new', 'Harbour at night', { month: '2026-06', firstCaptured: '2026-06-01T10:00:00Z', added_at: 1_700_000_400 })
  const NEW_OTHER = folder('new', 'Lighthouse', { month: '2026-06', firstCaptured: '2026-06-01T10:00:00Z', added_at: 1_700_000_400 })

  /** The page: the picker's answer selects (and marks pending), and the list arrives; settleFilter runs after each, as the container's effect does. */
  function page(filter: LibraryFilter) {
    const st = { folders: FOLDERS, filter, selected: null as string | null, pending: null as string | null }
    const settle = () => {
      const r = settleFilter(st)
      st.filter = r.filter
      st.pending = r.pending
      if (r.clear) st.selected = null
    }
    const handlers = createLibraryHandlers({
      library: { AddFolders: () => Promise.resolve([{ id: 'new', name: 'x', existing: false }]) },
      system: { ShowFolderMenu: () => Promise.resolve(), OpenPicksFolder: () => Promise.resolve() },
      toasts: { push: () => '' },
      select: (id) => {
        st.pending = id
        st.selected = id
        settle()
      },
      folderName: () => undefined,
      onError: (e) => {
        throw e
      },
    })
    const refresh = (added: FolderSummary) => {
      st.folders = [added, ...FOLDERS]
      settle()
    }
    return { st, handlers, refresh }
  }

  const search = f({ query: 'harbour', order: 'name' })
  for (const [label, added, want] of [
    ['a folder the search matches keeps the search', NEW_MATCH, search],
    ['a folder the search hides clears it (the order stays)', NEW_OTHER, f({ order: 'name' })],
  ] as const) {
    test(`${label}: the answer before the list refresh`, async () => {
      const p = page(search)
      await p.handlers.add()
      expect(p.st.filter).toBe(search)
      expect(p.st.pending).toBe('new')
      p.refresh(added)
      expect(p.st.filter).toEqual(want)
      expect(p.st.selected).toBe('new')
      expect(p.st.pending).toBeNull()
    })
    test(`${label}: the list refresh before the answer`, async () => {
      const p = page(search)
      p.refresh(added)
      await p.handlers.add()
      expect(p.st.filter).toEqual(want)
      expect(p.st.selected).toBe('new')
      expect(p.st.pending).toBeNull()
    })
  }
})

describe('filterMemory', () => {
  test('starts with no search, newest first, every folder', () => {
    expect(filterMemory({}).get()).toEqual(LIBRARY_FILTER)
  })
  test('keeps the filter for its owner (the shell) across mounts; another owner starts afresh', () => {
    const owner = {}
    filterMemory(owner).set(f({ query: 'lake', order: 'name' }))
    expect(filterMemory(owner).get()).toEqual(f({ query: 'lake', order: 'name' }))
    expect(filterMemory({}).get()).toEqual(LIBRARY_FILTER)
  })
})
