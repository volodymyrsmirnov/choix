// Mock data for the Library states (100–122) and the shell states (30–36).
import type { FolderSummary, ToastItem } from '../../src/design/types'
import { addedAt, LISBON, PATAGONIA, photo, previewsOf } from './data'

const PLACES = [
  'Porto', 'Sintra', 'Madeira', 'Azores', 'Seville', 'Granada', 'Valencia', 'Mallorca',
  'Provence', 'Brittany', 'Annecy', 'Corsica', 'Tuscany', 'Puglia', 'Sardinia', 'Venice',
  'Lake Bled', 'Ljubljana', 'Graz', 'Salzburg', 'Prague', 'Kraków', 'Tatras', 'Budapest',
  'Transylvania', 'Athens', 'Crete', 'Santorini', 'Istanbul', 'Cappadocia', 'Tbilisi', 'Kazbegi',
  'Shetland', 'Skye', 'Snowdonia', 'Lake District', 'Cornwall', 'Connemara', 'Bergen', 'Abisko',
]

/** Forty folders, two or so a month back from October 2026 (state 109). */
export const FORTY: FolderSummary[] = PLACES.map((name, i) => {
  const index = 2026 * 12 + 9 - Math.floor((i * 3) / 2) // October 2026, then back
  const month = `${Math.floor(index / 12)}-${String((index % 12) + 1).padStart(2, '0')}`
  const day = 2 + ((i * 7) % 20)
  const first = `${month}-${String(day).padStart(2, '0')}T09:00:00Z`
  const last = `${month}-${String(day + 1 + (i % 5)).padStart(2, '0')}T18:00:00Z`
  const photos = 40 + ((i * 137) % 900)
  return {
    id: `f${i + 1}`,
    name,
    parentPath: i % 3 === 0 ? '~/Pictures/Family' : '~/Pictures/Trips',
    state: 'ready',
    month,
    firstCaptured: first,
    lastCaptured: last,
    photos,
    videos: i % 4 === 0 ? 3 + (i % 7) : 0,
    clusters: Math.round(photos / 4),
    picked: i % 5 === 0 ? 0 : Math.round(photos / 9),
    rejected: i % 3 === 0 ? 0 : Math.round(photos / 6),
    previews: Array.from({ length: 6 }, (_, j) => photo(((i * 5 + j * 7) % 36) + 1)),
    added_at: addedAt(20 + ((i * 17) % 40)),
  }
})

/** An 80-character name under a long path with RTL text and emoji (state 110). */
export const LONG_NAME: FolderSummary = {
  ...PATAGONIA,
  id: 'long',
  added_at: addedAt(7),
  name: 'Patagonia — Torres del Paine, the W Trek and the long road north to El Chaltén',
  parentPath: '~/Pictures/מסעות 🌍 رحلات/2026 · Southern Hemisphere ✈️ winter road trip/Chile y Argentina',
}

/** A folder whose shoot runs over three months (state 116). */
export const THREE_MONTHS: FolderSummary = {
  ...LISBON,
  id: 'camino',
  added_at: addedAt(8),
  name: 'Camino de Santiago',
  month: '2026-08',
  firstCaptured: '2026-08-28T07:10:00Z',
  lastCaptured: '2026-10-04T16:30:00Z',
  photos: 1840,
  videos: 22,
  clusters: 402,
  picked: 0,
  rejected: 0,
  previews: [photo(13), photo(8), photo(26), photo(2), photo(15), photo(5)],
}

/** A folder with no dated media (state 108). */
export const UNDATED: FolderSummary = {
  ...LISBON,
  id: 'scans',
  added_at: addedAt(9),
  name: 'Film scans',
  videos: 0,
  parentPath: '~/Pictures/Archive',
  month: null,
  firstCaptured: null,
  lastCaptured: null,
  photos: 148,
  clusters: 0,
  picked: 9,
  rejected: 0,
  previews: previewsOf(2).reverse(),
}

export const TOAST_REMOVED: ToastItem = {
  id: 'removed-kyoto',
  tone: 'info',
  title: '“Kyoto” left the Library',
  body: 'The folder was deleted or moved to the Trash in Finder.',
}

export const TOAST_ALREADY: ToastItem = {
  id: 'already-lisbon',
  tone: 'info',
  title: '“Lisbon” is already in the Library',
  body: 'Choix selected it for you.',
}

export const TOAST_READ_ONLY: ToastItem = {
  id: 'read-only-card',
  tone: 'warn',
  title: '“Card A” is on a read-only drive',
  body: 'Choix can show and review it, but cannot copy picks into it.',
}
