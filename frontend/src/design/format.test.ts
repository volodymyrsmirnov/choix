import { describe, expect, test } from 'bun:test'
import {
  abbreviatePath,
  acceleratorLabel,
  bytes,
  megabytes,
  monthLabel,
  dateRange,
  formatQuality,
  noOrphan,
  plural,
  qualityLabel,
  stageLabel,
} from './format'

const NBSP = ' '

describe('plural', () => {
  test('one, zero and many, with grouping', () => {
    expect(plural(1, 'photo', 'photos')).toBe('1 photo')
    expect(plural(0, 'photo', 'photos')).toBe('0 photos')
    expect(plural(5218, 'photo', 'photos')).toBe('5,218 photos')
  })
})

describe('dateRange', () => {
  test('same month', () => {
    expect(dateRange('2026-03-14T08:00:00Z', '2026-03-16T19:00:00Z')).toBe('Mar 14 – 16')
  })
  test('two months, one year', () => {
    expect(dateRange('2026-04-28T08:00:00Z', '2026-05-09T19:00:00Z')).toBe('Apr 28 – May 9')
  })
  test('two years', () => {
    expect(dateRange('2025-12-28T08:00:00Z', '2026-01-03T19:00:00Z')).toBe('Dec 28, 2025 – Jan 3, 2026')
  })
  test('one day', () => {
    expect(dateRange('2026-09-06T08:00:00Z', '2026-09-06T19:00:00Z')).toBe('Sep 6')
  })
  test('one end missing reads as one day', () => {
    expect(dateRange('2026-09-06T08:00:00Z', null)).toBe('Sep 6')
    expect(dateRange(null, '2026-09-06T08:00:00Z')).toBe('Sep 6')
  })
  test('undated', () => {
    expect(dateRange(null, null)).toBe('')
  })
  test('time zone decides the day', () => {
    expect(dateRange('2026-09-06T23:30:00Z', '2026-09-06T23:30:00Z', 'Asia/Tokyo')).toBe('Sep 7')
  })
})

describe('abbreviatePath', () => {
  test('home becomes ~', () => {
    expect(abbreviatePath('/Users/ann/Pictures/Trips', '/Users/ann')).toBe('~/Pictures/Trips')
    expect(abbreviatePath('/Users/ann', '/Users/ann')).toBe('~')
    expect(abbreviatePath('/Users/ann/Pictures', '/Users/ann/')).toBe('~/Pictures')
  })
  test('other paths are left alone', () => {
    expect(abbreviatePath('/Volumes/Card/DCIM', '/Users/ann')).toBe('/Volumes/Card/DCIM')
    expect(abbreviatePath('/Users/anna/Pictures', '/Users/ann')).toBe('/Users/anna/Pictures')
  })
})

describe('bytes', () => {
  test('decimal units, as Finder', () => {
    expect(bytes(0)).toBe('0 B')
    expect(bytes(999)).toBe('999 B')
    expect(bytes(999_999)).toBe('1 MB')
    expect(bytes(999_999_999)).toBe('1 GB')
    expect(bytes(999_600)).toBe('1 MB')
    expect(bytes(999_400)).toBe('999 KB')
    expect(bytes(2_048)).toBe('2 KB')
    expect(bytes(177_000_000)).toBe('177 MB')
    expect(bytes(352_400_000)).toBe('352 MB')
    expect(bytes(1_500_000_000)).toBe('1.5 GB')
  })
})

describe('noOrphan', () => {
  test('binds the last two words of four or more', () => {
    expect(noOrphan('Your photos never leave it.')).toBe(`Your photos never leave${NBSP}it.`)
    expect(noOrphan('Your photos never leave it.').endsWith(`leave${NBSP}it.`)).toBe(true)
  })
  test('leaves three words or fewer unchanged', () => {
    expect(noOrphan('never leave it')).toBe('never leave it')
    expect(noOrphan('one')).toBe('one')
    expect(noOrphan('')).toBe('')
  })
  test('is idempotent', () => {
    const once = noOrphan('Your photos never leave it.')
    expect(noOrphan(once)).toBe(once)
  })
})

describe('labels', () => {
  test('quality', () => {
    expect(formatQuality(7)).toBe('7.0')
    expect(qualityLabel(6.44)).toBe('quality 6.4 of 10')
  })
  test('pipeline stages', () => {
    expect(stageLabel('metadata')).toBe('Read EXIF')
    expect(stageLabel('cluster')).toBe('Cluster')
  })
  test('accelerators as macOS glyphs, in macOS modifier order', () => {
    expect(acceleratorLabel('CmdOrCtrl+O')).toBe('⌘O')
    expect(acceleratorLabel('CmdOrCtrl+Shift+P')).toBe('⇧⌘P')
    expect(acceleratorLabel('CmdOrCtrl+Backspace')).toBe('⌘⌫')
    expect(acceleratorLabel('CmdOrCtrl+Plus')).toBe('⌘+')
    expect(acceleratorLabel('CmdOrCtrl+-')).toBe('⌘−')
    expect(acceleratorLabel('Ctrl+CmdOrCtrl+F')).toBe('⌃⌘F')
    expect(acceleratorLabel('Alt+Shift+CmdOrCtrl+Return')).toBe('⌥⇧⌘↩')
    expect(acceleratorLabel('Escape')).toBe('Esc')
  })
})

describe('monthLabel', () => {
  test('a YYYY-MM month as the timeline names it', () => {
    expect(monthLabel('2026-10')).toBe('October 2026')
    expect(monthLabel('2025-01')).toBe('January 2025')
  })
})

describe('megabytes', () => {
  test('whole decimal megabytes, grouped, without the unit', () => {
    expect(megabytes(137_000_000)).toBe('137')
    expect(megabytes(352_400_000)).toBe('352')
    expect(megabytes(1_499_600_000)).toBe('1,500')
    expect(megabytes(0)).toBe('0')
  })
})
