import { describe, expect, test } from 'bun:test'
import type { SettingsDraft } from './props'
import { FIELD_COPY, minutesTextToSeconds, picksDirReason, secondsToMinutesText, validateSettings } from './validate'

const ok: SettingsDraft = {
  bucket_size: '10',
  similarity: 0.9,
  picks_dir: 'picks',
  advance_on_action: false,
  hide_rejected_photos: false,
  cross_device_merging: false,
  quality_rating: true,
  visual_grouping: true,
}
const errs = (patch: Partial<SettingsDraft>) => validateSettings({ ...ok, ...patch })

describe('validateSettings', () => {
  test('a valid draft has no errors', () => {
    expect(validateSettings(ok)).toEqual({})
  })

  test('time bucket: empty or not a number (v1 copy)', () => {
    for (const t of ['', '   ', 'abc', '1e400']) {
      expect(errs({ bucket_size: t })).toEqual({ bucket_size: 'Enter the gap in minutes, e.g. 10 or 2.5.' })
    }
  })

  test('time bucket: under one minute (v1 copy)', () => {
    expect(errs({ bucket_size: '0.5' })).toEqual({ bucket_size: 'Must be at least 1 minute.' })
    expect(errs({ bucket_size: '0' })).toEqual({ bucket_size: 'Must be at least 1 minute.' })
    expect(errs({ bucket_size: '-3' })).toEqual({ bucket_size: 'Must be at least 1 minute.' })
  })

  test('time bucket: one minute and fractions above it are fine', () => {
    expect(errs({ bucket_size: '1' })).toEqual({})
    expect(errs({ bucket_size: '2.5' })).toEqual({})
    expect(errs({ bucket_size: ' 16.67 ' })).toEqual({})
  })

  test('similarity outside (0, 1]', () => {
    expect(errs({ similarity: 0 })).toEqual({ similarity: FIELD_COPY.similarity.range })
    expect(errs({ similarity: 1.01 })).toEqual({ similarity: FIELD_COPY.similarity.range })
    expect(errs({ similarity: Number.NaN })).toEqual({ similarity: FIELD_COPY.similarity.range })
    expect(errs({ similarity: 1 })).toEqual({})
  })

  test('picks folder: empty (v1 copy)', () => {
    expect(errs({ picks_dir: '' })).toEqual({ picks_dir: 'Enter a folder for your picks.' })
  })

  test('picks folder: ".." escapes (v1 copy)', () => {
    const msg = 'Must stay inside the scanned folder — ".." would leave it.'
    for (const p of ['..', '../x', 'a/../../b', './../picks']) expect(errs({ picks_dir: p })).toEqual({ picks_dir: msg })
  })

  test('picks folder: ".." that stays inside is fine', () => {
    for (const p of ['a/../picks', 'exports/luminar', 'picks/', './picks', 'a//b']) expect(errs({ picks_dir: p })).toEqual({})
  })

  test('picks folder: every absolute path is rejected (2.0, relative-only)', () => {
    const msg = 'Use a folder name inside the scanned folder, not an absolute path.'
    for (const p of ['/', '/tmp/picks', '/Users/someone/Pictures/picks', '//x']) expect(errs({ picks_dir: p })).toEqual({ picks_dir: msg })
  })

  test('picks folder: the scanned folder itself or .choix is reserved', () => {
    for (const p of ['.', './', 'a/..', '.choix', '.choix/x', './.choix', '.CHOIX']) {
      expect(errs({ picks_dir: p })).toEqual({ picks_dir: FIELD_COPY.picks_dir.reserved })
    }
  })

  test('several fields fail at once', () => {
    expect(errs({ bucket_size: '', picks_dir: '/abs' })).toEqual({
      bucket_size: FIELD_COPY.bucket_size.nan,
      picks_dir: FIELD_COPY.picks_dir.absolute,
    })
  })
})

describe('picksDirReason', () => {
  test('matches the Go reason codes', () => {
    expect(picksDirReason('')).toBe('empty')
    expect(picksDirReason('/x')).toBe('absolute')
    expect(picksDirReason('../x')).toBe('traversal')
    expect(picksDirReason('.choix/x')).toBe('reserved')
    expect(picksDirReason('picks')).toBeNull()
  })

  test('every Go reason has copy', () => {
    for (const r of ['empty', 'absolute', 'traversal', 'reserved', 'has_originals', 'symlink'] as const) {
      expect(FIELD_COPY.picks_dir[r].length).toBeGreaterThan(0)
    }
    expect(FIELD_COPY.bucket_size.min).toBe('Must be at least 1 minute.')
  })
})

describe('minutes ↔ seconds', () => {
  test('whole seconds round-trip through two decimals (v1)', () => {
    for (const sec of [60, 90, 600, 1000, 3600, 86_400]) {
      expect(minutesTextToSeconds(secondsToMinutesText(sec))).toBe(sec)
    }
    expect(secondsToMinutesText(600)).toBe('10')
    expect(secondsToMinutesText(1000)).toBe('16.67')
  })

  test('unusable text is null', () => {
    expect(minutesTextToSeconds('')).toBeNull()
    expect(minutesTextToSeconds('x')).toBeNull()
    expect(minutesTextToSeconds('1e400')).toBeNull()
    expect(minutesTextToSeconds('1e300')).toBeNull()
    expect(minutesTextToSeconds('2.5')).toBe(150)
  })
})
