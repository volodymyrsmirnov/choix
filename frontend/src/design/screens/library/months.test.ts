import { expect, test } from 'bun:test'
import type { FolderSummary } from '../../types'
import { isProcessing, librarySummary, monthCount, monthGroups } from './months'

const f = (id: string, month: string | null, first: string | null, over: Partial<FolderSummary> = {}): FolderSummary => ({
  id,
  name: id,
  parentPath: '~/Pictures',
  state: 'ready',
  month,
  firstCaptured: first,
  lastCaptured: first,
  photos: 10,
  videos: 0,
  clusters: 2,
  picked: 0,
  rejected: 0,
  previews: [],
  ...over,
})

test('months run newest first, undated last; rows newest first inside a month', () => {
  const groups = monthGroups([
    f('a', '2026-08', '2026-08-02T10:00:00Z'),
    f('u', null, null),
    f('b', '2026-10', '2026-10-21T10:00:00Z'),
    f('c', '2026-08', '2026-08-19T10:00:00Z'),
  ])
  expect(groups.map((g) => g.label)).toEqual(['October 2026', 'August 2026', 'Undated'])
  expect(groups[1]!.folders.map((x) => x.id)).toEqual(['c', 'a'])
})

test('a month counts its folders, photos and videos', () => {
  expect(monthCount([f('a', '2026-08', null, { photos: 1 })])).toBe('1 folder · 1 photo')
  expect(monthCount([f('a', '2026-08', null, { photos: 2768, videos: 81 }), f('b', '2026-08', null, { photos: 0 })])).toBe(
    '2 folders · 2,768 photos · 81 videos',
  )
})

test('a month whose folders are all offline says so', () => {
  expect(monthCount([f('a', '2026-02', null, { state: 'offline' })])).toBe('1 folder · offline')
})

test('the summary adds up photos, videos and picks', () => {
  const s = librarySummary([
    f('a', '2026-08', null, { photos: 2768, videos: 81, picked: 142 }),
    f('b', '2026-08', null, { photos: 1126, videos: 9, picked: 87 }),
  ])
  expect(s).toEqual({ title: '2 folders', detail: '3,894 photos · 90 videos · 229 picked' })
  expect(librarySummary([f('a', null, null, { photos: 1 })])).toEqual({ title: '1 folder', detail: '1 photo' })
})

// ── Processing (the owner's round 3): a new folder goes to the top ───────────
//
// A folder is processing while its job runs or waits (state `scanning` or
// `queued`) and it has no capture month yet: its dates are written when the
// job ends. It sits in a "Processing" section above the newest month, in
// either date order, and moves to its month (or to Undated) once the job is
// over. A rescanned folder that already has dates stays in its month.

const shape = (gs: ReturnType<typeof monthGroups>) => gs.map((g) => [g.label, g.folders.map((x) => x.id)])

test('a new folder, scanning or queued without dates, is processing; nothing else is', () => {
  expect(isProcessing(f('new', null, null, { state: 'scanning', photos: 0 }))).toBe(true)
  expect(isProcessing(f('waits', null, null, { state: 'queued', photos: 0 }))).toBe(true)
  expect(isProcessing(f('rescan', '2026-08', '2026-08-02T10:00:00Z', { state: 'scanning' }))).toBe(false)
  expect(isProcessing(f('undated', null, null))).toBe(false)
  expect(isProcessing(f('failed', null, null, { state: 'error' }))).toBe(false)
  expect(isProcessing(f('offline', null, null, { state: 'offline' }))).toBe(false)
})

test('processing folders come first, above the newest month, in both date orders; Undated stays last', () => {
  const list = [
    f('aug', '2026-08', '2026-08-02T10:00:00Z'),
    f('scans', null, null),
    f('rome', null, null, { state: 'scanning', photos: 0, added_at: 200 }),
    f('oct', '2026-10', '2026-10-21T10:00:00Z', { state: 'scanning' }),
    f('naples', null, null, { state: 'queued', photos: 0, added_at: 300 }),
  ]
  expect(shape(monthGroups(list))).toEqual([
    ['Processing', ['naples', 'rome']],
    ['October 2026', ['oct']],
    ['August 2026', ['aug']],
    ['Undated', ['scans']],
  ])
  expect(shape(monthGroups(list, 'oldest'))).toEqual([
    ['Processing', ['naples', 'rome']],
    ['August 2026', ['aug']],
    ['October 2026', ['oct']],
    ['Undated', ['scans']],
  ])
  expect(monthGroups(list)[0]!.key).toBe('processing')
})

test('once its job is over, the folder leaves Processing: to its month, or to Undated', () => {
  const done = [f('rome', '2026-10', '2026-10-02T10:00:00Z', { added_at: 200 }), f('naples', null, null, { added_at: 300 })]
  expect(shape(monthGroups(done))).toEqual([
    ['October 2026', ['rome']],
    ['Undated', ['naples']],
  ])
})

test('processing folders run most recently added first, then by name', () => {
  const list = [
    f('b', null, null, { state: 'queued', name: 'Bravo' }),
    f('a', null, null, { state: 'queued', name: 'Alpha' }),
    f('c', null, null, { state: 'scanning', added_at: 10 }),
  ]
  expect(shape(monthGroups(list))).toEqual([['Processing', ['c', 'a', 'b']]])
})

test('the Processing header counts folders only (their photos are not counted yet)', () => {
  expect(monthCount([f('rome', null, null, { state: 'scanning', photos: 0 })])).toBe('1 folder')
  expect(monthCount([f('a', null, null, { state: 'scanning', photos: 0 }), f('b', null, null, { state: 'queued', photos: 0 })])).toBe('2 folders')
})
