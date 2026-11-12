import { describe, expect, test } from 'bun:test'
import type { QualityStatus, SettingsBody } from '../../types'
import { atDefaults, DEFAULTS, dirtySections, draftOf, qualityStatusText, reclusterFields, saveNote } from './draft'

const saved: SettingsBody = { ...DEFAULTS, bucket_size: 900, picks_dir: 'selects' }
const q = (s: Partial<QualityStatus>): QualityStatus => ({
  enabled: true,
  state: 'idle',
  active: false,
  done: 0,
  total: 0,
  rated: 0,
  ...s,
})

describe('defaults and drafts', () => {
  test('defaults match config.Defaults() and the 2.0 visual_grouping', () => {
    expect(DEFAULTS).toEqual({
      bucket_size: 600,
      similarity: 0.9,
      picks_dir: 'picks',
      advance_on_action: false,
      hide_rejected_photos: false,
      cross_device_merging: false,
      quality_rating: true,
      visual_grouping: true,
    })
  })

  test('a draft shows the bucket in minutes', () => {
    expect(draftOf(saved).bucket_size).toBe('15')
    expect(draftOf(saved).picks_dir).toBe('selects')
  })

  test('atDefaults compares parsed values', () => {
    expect(atDefaults(draftOf(DEFAULTS))).toBe(true)
    expect(atDefaults({ ...draftOf(DEFAULTS), bucket_size: '10.0' })).toBe(true)
    expect(atDefaults(draftOf(saved))).toBe(false)
  })
})

describe('dirty sections', () => {
  test('nothing is dirty before loading or at the saved values', () => {
    expect(dirtySections(null, draftOf(saved))).toEqual({ grouping: false, behaviour: false, output: false })
    expect(dirtySections(saved, draftOf(saved))).toEqual({ grouping: false, behaviour: false, output: false })
  })

  test('the bucket compares whole seconds, not text (v1)', () => {
    expect(dirtySections(saved, { ...draftOf(saved), bucket_size: '15.00' }).grouping).toBe(false)
    expect(dirtySections(saved, { ...draftOf(saved), bucket_size: '15.004' }).grouping).toBe(false)
    expect(dirtySections(saved, { ...draftOf(saved), bucket_size: '' }).grouping).toBe(true)
  })

  test('each field marks its own section', () => {
    const d = draftOf(saved)
    expect(dirtySections(saved, { ...d, visual_grouping: false })).toEqual({ grouping: true, behaviour: false, output: false })
    expect(dirtySections(saved, { ...d, cross_device_merging: true }).grouping).toBe(true)
    expect(dirtySections(saved, { ...d, similarity: 0.85 }).grouping).toBe(true)
    expect(dirtySections(saved, { ...d, advance_on_action: true }).behaviour).toBe(true)
    expect(dirtySections(saved, { ...d, hide_rejected_photos: true }).behaviour).toBe(true)
    expect(dirtySections(saved, { ...d, quality_rating: false }).behaviour).toBe(true)
    expect(dirtySections(saved, { ...d, picks_dir: 'picks' })).toEqual({ grouping: false, behaviour: false, output: true })
  })
})

describe('save note', () => {
  const d = draftOf(saved)
  test('names what re-clusters', () => {
    expect(reclusterFields(saved, { ...d, bucket_size: '5', similarity: 0.8 })).toEqual(['time bucket', 'similarity'])
    expect(reclusterFields(saved, { ...d, visual_grouping: false, cross_device_merging: true })).toEqual([
      'visual grouping',
      'cross-device merge',
    ])
    expect(saveNote(saved, { ...d, bucket_size: '5' }, false)).toBe(
      'Saving will re-cluster your folders (time bucket changed). Picks are kept.',
    )
  })

  test('says when no re-cluster is needed, or what to fix', () => {
    expect(saveNote(saved, d, false)).toBeNull()
    expect(saveNote(saved, { ...d, advance_on_action: true }, false)).toBe('Saving applies immediately — no re-cluster needed.')
    expect(saveNote(saved, { ...d, picks_dir: '/x' }, true)).toBe('Fix the highlighted field to save.')
  })
})

describe('quality status line', () => {
  test('one line per state the worker reports', () => {
    expect(qualityStatusText(null)).toBe('')
    expect(qualityStatusText(q({ enabled: false, state: 'disabled' }))).toBe('Off: no ratings.')
    expect(qualityStatusText(q({ state: 'waiting' }))).toBe('Waiting for the scan to finish before rating photos.')
    expect(qualityStatusText(q({ state: 'scoring', done: 1234, total: 5218 }))).toBe('Rating photos: 1,234 of 5,218.')
    expect(qualityStatusText(q({ state: 'idle' }))).toBe('Ratings are up to date.')
  })

  test('no download or error text: a state the worker no longer reports says nothing', () => {
    // What the worker reported while it downloaded a model (the app's types
    // keep the names until phase B).
    for (const state of ['downloading', 'download_failed', 'error'] as unknown as QualityStatus['state'][]) {
      expect(qualityStatusText(q({ state })), state).toBe('')
    }
  })
})
