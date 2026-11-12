// The Library toolbar's search, filter and order (pure; the screen applies
// them to the folders the app passes, as the Folder page does its clusters).
import { describe, expect, test } from 'bun:test'
import type { FolderSummary, LibraryFilter } from '../../types'
import { filterFolders, isFiltered, LIBRARY_FILTER, libraryGroups, matchesQuery, matchesShow } from './filter'

const f = (id: string, over: Partial<FolderSummary> = {}): FolderSummary => ({
  id,
  name: id,
  parentPath: '~/Pictures/Trips',
  state: 'ready',
  month: '2026-10',
  firstCaptured: '2026-10-01T10:00:00Z',
  lastCaptured: '2026-10-02T10:00:00Z',
  photos: 10,
  videos: 0,
  clusters: 2,
  picked: 0,
  rejected: 0,
  previews: [],
  ...over,
})
const ids = (list: FolderSummary[]) => list.map((x) => x.id)
const filter = (over: Partial<LibraryFilter>): LibraryFilter => ({ ...LIBRARY_FILTER, ...over })

describe('the default', () => {
  test('no search, capture date newest first (the approved timeline), every folder', () => {
    expect(LIBRARY_FILTER).toEqual({ query: '', order: 'date-desc', show: 'all' })
    expect(isFiltered(LIBRARY_FILTER)).toBe(false)
    expect(isFiltered(filter({ order: 'name' }))).toBe(false)
    expect(isFiltered(filter({ query: ' x ' }))).toBe(true)
    expect(isFiltered(filter({ query: '   ' }))).toBe(false)
    expect(isFiltered(filter({ show: 'attention' }))).toBe(true)
  })
})

describe('search', () => {
  const lisbon = f('lisbon', { name: 'Lisbon' })
  const faroe = f('faroe', { name: 'Faroe Islands', parentPath: '/Volumes/Atlas/Shoots' })
  test('matches the folder name and where it is, trimmed and ignoring case', () => {
    expect(matchesQuery(lisbon, 'lis')).toBe(true)
    expect(matchesQuery(lisbon, '  LISBON ')).toBe(true)
    expect(matchesQuery(faroe, 'atlas')).toBe(true)
    expect(matchesQuery(faroe, 'trips')).toBe(false)
    expect(matchesQuery(lisbon, 'trips')).toBe(true)
    expect(matchesQuery(lisbon, '')).toBe(true)
  })
  test('matches across the path and the name, as Finder shows it', () => {
    expect(matchesQuery(lisbon, 'Trips/Lis')).toBe(true)
    expect(matchesQuery(faroe, 'shoots/faroe')).toBe(true)
  })
})

describe('the filter', () => {
  const fresh = f('fresh')
  const started = f('started', { picked: 3 })
  const rejectedOnly = f('rejects', { rejected: 2 })
  const empty = f('empty', { photos: 0, videos: 0, clusters: 0 })
  const offline = f('offline', { state: 'offline' })
  const failed = f('failed', { state: 'error', error: 'This folder can no longer be found.', picked: 4 })
  const all = [fresh, started, rejectedOnly, empty, offline, failed]
  test('not reviewed yet: media and no pick or reject', () => {
    expect(ids(all.filter((x) => matchesShow(x, 'unreviewed')))).toEqual(['fresh', 'offline'])
  })
  test('review started: a pick or a reject', () => {
    expect(ids(all.filter((x) => matchesShow(x, 'started')))).toEqual(['started', 'rejects', 'failed'])
  })
  test('needs attention: offline, or in error (missing, access lost, a failed scan)', () => {
    expect(ids(all.filter((x) => matchesShow(x, 'attention')))).toEqual(['offline', 'failed'])
  })
  test('all: every folder', () => {
    expect(ids(all.filter((x) => matchesShow(x, 'all')))).toEqual(ids(all))
  })
  test('filterFolders applies the search and the filter, and keeps the order it was given', () => {
    expect(ids(filterFolders(all, filter({ show: 'attention', query: 'off' })))).toEqual(['offline'])
    expect(ids(filterFolders(all, filter({ query: 'e' })))).toEqual(['fresh', 'started', 'rejects', 'empty', 'offline', 'failed'])
  })
})

describe('order and grouping', () => {
  const oct1 = f('oct1', { name: 'Bravo', month: '2026-10', firstCaptured: '2026-10-01T10:00:00Z', added_at: 1793523600 })
  const oct9 = f('oct9', { name: 'alpha', month: '2026-10', firstCaptured: '2026-10-09T10:00:00Z', added_at: 1793696400 })
  const sep = f('sep', { name: 'Charlie 10', month: '2026-09', firstCaptured: '2026-09-04T10:00:00Z', added_at: 1793610000 })
  const sep2 = f('sep2', { name: 'Charlie 9', month: '2026-09', firstCaptured: '2026-09-02T10:00:00Z', added_at: 1793782800 })
  const undated = f('undated', { name: 'Scans', month: null, firstCaptured: null, lastCaptured: null, added_at: 1793350800 })
  const all = [oct1, oct9, sep, sep2, undated]
  const shape = (order: LibraryFilter['order']) => libraryGroups(all, order).map((g) => [g.label, ids(g.folders)])

  test('newest first (today’s timeline): months newest first, each newest first, Undated last', () => {
    expect(shape('date-desc')).toEqual([
      ['October 2026', ['oct9', 'oct1']],
      ['September 2026', ['sep', 'sep2']],
      ['Undated', ['undated']],
    ])
  })
  test('oldest first: months oldest first, each oldest first, Undated still last', () => {
    expect(shape('date-asc')).toEqual([
      ['September 2026', ['sep2', 'sep']],
      ['October 2026', ['oct1', 'oct9']],
      ['Undated', ['undated']],
    ])
  })
  test('name: one list without month headers, A to Z, ignoring case, numbers in numeric order', () => {
    expect(shape('name')).toEqual([[null, ['oct9', 'oct1', 'sep2', 'sep', 'undated']]])
  })
  test('recently added: one list without month headers, newest first', () => {
    expect(shape('added')).toEqual([[null, ['sep2', 'oct9', 'sep', 'oct1', 'undated']]])
  })
  test('recently added does not trust the order it is given (Library.List answers in capture-date order)', () => {
    expect(libraryGroups([...all].reverse(), 'added').map((g) => ids(g.folders))).toEqual([['sep2', 'oct9', 'sep', 'oct1', 'undated']])
  })
  test('recently added: a folder without added_at goes after every folder with it, by name; equal times by name too', () => {
    const late = f('late', { name: 'Zulu', added_at: 1793782800 })
    const { added_at: _a, ...noTimeB } = oct1
    const { added_at: _b, ...noTimeA } = oct9
    const list = [noTimeB, late, sep2, noTimeA]
    expect(libraryGroups(list, 'added').map((g) => ids(g.folders))).toEqual([['sep2', 'late', 'oct9', 'oct1']])
  })
  test('an empty list has no group', () => {
    expect(libraryGroups([], 'date-desc')).toEqual([])
    expect(libraryGroups([], 'name')).toEqual([])
  })
  test('the input is never changed', () => {
    const copy = [...all]
    libraryGroups(all, 'name')
    libraryGroups(all, 'date-asc')
    expect(all).toEqual(copy)
  })
})

describe('Processing under the toolbar (the owner’s round 3)', () => {
  const rome = f('rome', { name: 'Rome', state: 'scanning', month: null, firstCaptured: null, lastCaptured: null, photos: 0, clusters: 0 })
  const lisbon = f('lisbon', { name: 'Lisbon' })
  test('only the date orders have a Processing section; name and recently added stay one list', () => {
    expect(libraryGroups([lisbon, rome], 'date-desc').map((g) => g.label)).toEqual(['Processing', 'October 2026'])
    expect(libraryGroups([lisbon, rome], 'date-asc').map((g) => g.label)).toEqual(['Processing', 'October 2026'])
    expect(libraryGroups([lisbon, rome], 'name').map((g) => g.label)).toEqual([null])
    expect(libraryGroups([lisbon, rome], 'added').map((g) => g.label)).toEqual([null])
  })
  test('a processing folder that a search matches stays in Processing', () => {
    const shown = filterFolders([lisbon, rome], filter({ query: 'rom' }))
    expect(libraryGroups(shown, 'date-desc').map((g) => [g.label, ids(g.folders)])).toEqual([['Processing', ['rome']]])
  })
})
