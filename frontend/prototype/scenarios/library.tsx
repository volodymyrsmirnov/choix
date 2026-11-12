// Library states 100–131 (spec §4.1, Layout A; 124–130 the toolbar's search, filter and order; 131 Processing).
// 112 and 120, the compact model-download banner, are retired: nothing is downloaded any more.
import { LibraryScreen } from '../../src/design/screens/library/LibraryScreen'
import { LIBRARY_OVERLAYS } from '../../src/design/screens/library/props'
import type { FolderSummary } from '../../src/design/types'
import {
  addedAt,
  DOLOMITES,
  KYOTO,
  LISBON,
  FAROE,
  photo,
  replace,
  scanAt,
  scanning,
  SIX,
} from '../mock/data'
import { FORTY, LONG_NAME, THREE_MONTHS, TOAST_ALREADY, TOAST_READ_ONLY, TOAST_REMOVED, UNDATED } from '../mock/library'
import type { Scenario } from '../registry'
import { libraryProps, type LibraryState } from './libraryWiring'

const TIMELINE = 'mockup-2026-11-11#s-a'
const EMPTY = 'mockup-2026-11-11#s-d'
const NEW = 'missing design'

/** What each state's label names, which must be in view at every size. */
const SUBJECT: Record<number, string> = {
  100: '[data-drop-zone]',
  101: '[data-drop-zone]',
  103: '[data-folder="dolomites"]',
  104: '[data-folder="kyoto"]',
  105: '[data-folder="lisbon"], [data-folder="kyoto"]',
  106: '[data-folder="kyoto"]',
  107: '[data-folder="lisbon"]',
  108: '[data-folder="scans"]',
  110: '[data-folder="long"]',
  113: '[data-overlay="toast:removed-kyoto"]',
  114: '[data-overlay="toast:already-lisbon"]',
  115: '[data-overlay="toast:read-only-card"]',
  116: '[data-folder="camino"]',
  117: '[data-folder="hokkaido"]',
  118: '[data-folder="dolomites"]',
  119: '[data-folder="empty-card"]',
  121: '[data-folder="one-photo"]',
  122: '[data-folder="one-video"]',
  111: '[aria-label="Loading the Library"]',
  123: '[data-folder="lisbon"], [data-folder="dolomites"], [data-folder="kyoto"]',
  124: '[data-testid="filter-input"], [data-folder="lisbon"], [data-folder="faroe"]',
  125: '[data-testid="folders-empty"]',
  126: '[role="menu"]',
  127: '[role="menu"]',
  128: '[data-folder="dolomites"]',
  129: '[data-folder="lisbon"], [data-folder="kyoto"]',
  130: '[data-testid="folders-empty"]',
  131: '[aria-label="Processing"]',
}

const lib = (n: number, label: string, s: LibraryState, design = TIMELINE, overlays?: string[]): Scenario => ({
  n,
  group: 'library',
  label,
  design,
  overlays,
  subject: SUBJECT[n],
  render: (ctx) => <LibraryScreen {...libraryProps(ctx, s)} />,
})

/** Lisbon (417 photos, 3 videos) generating thumbnails. */
const THUMBS = scanAt('thumb', 262, 420)

const CARD_A: FolderSummary = {
  ...DOLOMITES,
  id: 'card-a',
  added_at: addedAt(10),
  name: 'Card A',
  parentPath: '/Volumes/Card A/DCIM',
  volumeName: 'Card A',
  state: 'error',
  error: 'Read-only drive: picks cannot be copied into it',
  month: '2026-09',
  firstCaptured: '2026-09-12T08:00:00Z',
  lastCaptured: '2026-09-12T17:20:00Z',
  photos: 164,
  videos: 0,
  clusters: 38,
  picked: 0,
  rejected: 0,
  previews: [photo(31), photo(3), photo(17), photo(9), photo(28), photo(22)],
}

/** A folder just added: no dates, counts or previews until its first job ends, so it is processing (state 117). */
const HOKKAIDO: FolderSummary = {
  ...LISBON,
  id: 'hokkaido',
  added_at: addedAt(11),
  name: 'Hokkaido',
  videos: 0,
  month: null,
  firstCaptured: null,
  lastCaptured: null,
  photos: 0,
  clusters: 0,
  previews: [],
}

const EMPTY_CARD: FolderSummary = {
  ...LISBON,
  id: 'empty-card',
  added_at: addedAt(12),
  name: 'Card B',
  videos: 0,
  parentPath: '/Volumes/Card B/DCIM',
  month: null,
  firstCaptured: null,
  lastCaptured: null,
  photos: 0,
  clusters: 0,
  previews: [],
}

const ONE_PHOTO: FolderSummary = {
  ...KYOTO,
  id: 'one-photo',
  added_at: addedAt(13),
  name: 'Kyoto, Fushimi Inari',
  firstCaptured: '2026-07-04T06:12:00Z',
  lastCaptured: '2026-07-04T06:12:00Z',
  photos: 1,
  clusters: 1,
  picked: 1,
  rejected: 0,
  previews: [photo(14)],
}

const ONE_VIDEO: FolderSummary = {
  ...FAROE,
  id: 'one-video',
  added_at: addedAt(14),
  name: 'Aurora timelapse',
  parentPath: '~/Movies',
  volumeName: undefined,
  month: '2026-01',
  firstCaptured: '2026-01-21T23:10:00Z',
  lastCaptured: '2026-01-21T23:10:00Z',
  photos: 0,
  videos: 1,
  clusters: 1,
  picked: 0,
  rejected: 0,
  previews: [photo(36)],
}

/** Two folders just added to a long Library: one scanning, one waiting for its turn (state 131). */
const NEW_FOLDER = (id: string, name: string, n: number): FolderSummary => ({
  ...HOKKAIDO,
  id,
  name,
  added_at: addedAt(n),
  parentPath: '~/Pictures/Trips',
})
const TALLINN = scanning(NEW_FOLDER('tallinn', 'Tallinn', 61), scanAt('thumb', 118, 340))
const RIGA: FolderSummary = { ...NEW_FOLDER('riga', 'Riga', 60), state: 'queued' }

export const scenarios: Scenario[] = [
  lib(100, 'Empty (drop zone)', { folders: [] }, EMPTY),
  lib(101, 'Drop target active (needs spike S1)', { folders: [], dropActive: true }, EMPTY),
  lib(102, 'Filled, six folders', { folders: SIX }),
  lib(103, 'Row hovered (actions shown)', { folders: SIX, forceRow: { id: 'dolomites', force: 'hover' } }),
  lib(104, 'Row keyboard-focused', { folders: SIX, selectedId: 'kyoto', forceRow: { id: 'kyoto', force: 'focus' } }),
  lib(105, 'One scanning, one queued', {
    // The first and third rows, so both are on screen at 960 × 600.
    folders: replace(SIX, scanning(LISBON, THUMBS), { ...KYOTO, state: 'queued' }),
  }),
  lib(106, 'Scan error row', {
    folders: replace(SIX, { ...KYOTO, state: 'error', error: 'Scan failed: Choix may not read this folder' }),
  }),
  lib(107, 'Offline row', {
    folders: replace(SIX, { ...LISBON, state: 'offline', parentPath: '/Volumes/Atlas/Trips', volumeName: 'Atlas' }),
  }),
  // Undated folders sort last: two dated ones keep it on screen.
  lib(108, 'Undated folder', { folders: [LISBON, DOLOMITES, UNDATED] }),
  lib(109, 'Forty folders', { folders: FORTY }),
  lib(110, '80-Char name, long RTL/emoji path', { folders: [LONG_NAME, LISBON, DOLOMITES] }),
  lib(111, 'Loading', { folders: null }, 'missing design'),
  lib(
    113,
    '“Folder removed” toast',
    { folders: SIX.filter((f) => f.id !== 'kyoto'), toasts: [TOAST_REMOVED] },
    'v1:components/Toaster.tsx',
    [`toast:${TOAST_REMOVED.id}`],
  ),
  lib(
    114,
    '“Already in Library” toast',
    { folders: SIX, selectedId: 'lisbon', toasts: [TOAST_ALREADY] },
    'v1:components/Toaster.tsx',
    [`toast:${TOAST_ALREADY.id}`],
  ),
  lib(
    115,
    'Read-only volume error',
    { folders: [...SIX, CARD_A], selectedId: 'card-a', toasts: [TOAST_READ_ONLY] },
    TIMELINE,
    [`toast:${TOAST_READ_ONLY.id}`],
  ),
  lib(116, 'Folder spanning three months', { folders: [...SIX, THREE_MONTHS] }),
  lib(117, 'Scanning, previews not ready', {
    folders: [...SIX, scanning(HOKKAIDO, scanAt('metadata', 270, 980))],
  }),
  lib(118, 'Single folder', { folders: [DOLOMITES] }),
  lib(119, 'Folder with 0 photos', { folders: [EMPTY_CARD, DOLOMITES, KYOTO] }),
  lib(121, 'Folder with 1 photo', { folders: [ONE_PHOTO, LISBON] }),
  lib(122, 'Folder with 1 video', { folders: [ONE_VIDEO, LISBON] }),
  // Two folder jobs at once (the engine runs two): each row shows its own
  // progress (the title bar shows none). The first three rows, on screen at 960 × 600.
  lib(123, 'Two folders scanning, one queued', {
    folders: replace(SIX, scanning(LISBON, THUMBS), scanning(DOLOMITES, scanAt('analyze', 38, 96)), { ...KYOTO, state: 'queued' }),
  }),
  // The toolbar (the owner's round 2): search, filter and order. State 102 is the
  // toolbar at rest. A search matches names and locations: "is" finds Lisbon
  // and the Faroe Islands, under their months.
  lib(124, 'Search with results', { folders: SIX, filter: { query: 'is' } }, NEW),
  lib(125, 'Search, no match', { folders: SIX, filter: { query: 'harbour' } }, NEW),
  lib(126, 'Sort menu open', { folders: SIX, openMenu: 'order' }, NEW, [LIBRARY_OVERLAYS.order]),
  lib(127, 'Show menu open', { folders: SIX, openMenu: 'show' }, NEW, [LIBRARY_OVERLAYS.show]),
  // A month says nothing in a name order: one list.
  lib(128, 'Sorted by name (one list, no month headers)', { folders: SIX, filter: { order: 'name' } }, NEW),
  lib(129, 'Showing the folders that need attention', {
    folders: replace(
      SIX,
      { ...LISBON, state: 'offline', parentPath: '/Volumes/Atlas/Trips', volumeName: 'Atlas' },
      { ...KYOTO, state: 'error', error: 'Scan failed: Choix may not read this folder' },
    ),
    filter: { show: 'attention' },
  }, NEW),
  lib(130, 'Filter with nothing to show', { folders: SIX, filter: { show: 'attention' } }, NEW),
  // The owner's round 3: a folder still being processed for the first time
  // (no dates yet) sits under Processing at the top, above the newest month,
  // not under Undated at the bottom of a Library longer than one screen.
  lib(131, 'New folder processing, at the top', { folders: [...FORTY, RIGA, TALLINN] }, NEW),
]
