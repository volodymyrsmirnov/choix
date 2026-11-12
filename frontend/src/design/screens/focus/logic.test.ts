import { describe, expect, test } from 'bun:test'
import type { Member } from '../../types'
import {
  actionTarget,
  actionTitle,
  applyAction,
  compareGrid,
  compareSelect,
  enterCompare,
  exifRows,
  gpsOf,
  imageLabel,
  paneIndex,
  position,
  ratingOf,
  reviewedCount,
  stepIndex,
  stepZoom,
  toggleAction,
  undoActionFor,
  viewAt,
  zoomBy,
  zoomLabel,
} from './logic'
import { MAX_ZOOM } from './props'

const m = (file_id: number, extra: Partial<Member> = {}): Member => ({
  file_id,
  path: `Day 1/IMG_${file_id}.JPG`,
  content_hash: `h${file_id}`,
  picked: false,
  rejected: false,
  kind: 'photo',
  format: 'jpeg',
  ...extra,
})

const NONE = { picked: false, rejected: false }
const PICKED = { picked: true, rejected: false }
const REJECTED = { picked: false, rejected: true }

describe('marks', () => {
  test('pick and reject overwrite each other; un-* clear only their own flag', () => {
    expect(applyAction(REJECTED, 'pick')).toEqual(PICKED)
    expect(applyAction(PICKED, 'reject')).toEqual(REJECTED)
    expect(applyAction(PICKED, 'unpick')).toEqual(NONE)
    expect(applyAction(REJECTED, 'unpick')).toEqual(REJECTED)
    expect(applyAction(REJECTED, 'unreject')).toEqual(NONE)
  })

  test('one action always undoes the last one', () => {
    expect(undoActionFor(NONE, PICKED)).toBe('unpick')
    expect(undoActionFor(NONE, REJECTED)).toBe('unreject')
    expect(undoActionFor(PICKED, REJECTED)).toBe('pick')
    expect(undoActionFor(REJECTED, NONE)).toBe('reject')
    expect(undoActionFor(PICKED, PICKED)).toBeNull()
    expect(undoActionFor(NONE, NONE)).toBeNull()
  })

  test('Pick and Reject toggle; the toast names the action', () => {
    expect(toggleAction(NONE, 'picked')).toBe('pick')
    expect(toggleAction(PICKED, 'picked')).toBe('unpick')
    expect(toggleAction(PICKED, 'rejected')).toBe('reject')
    expect(toggleAction(REJECTED, 'rejected')).toBe('unreject')
    expect(['pick', 'reject', 'unpick', 'unreject'].map((a) => actionTitle(a as never))).toEqual(['Picked', 'Rejected', 'Cleared', 'Cleared'])
  })
})

describe('navigation and compare', () => {
  const ms = [m(1), m(2), m(3), m(4), m(5)]

  test('the action target is the photo on screen, or only the active pane while comparing', () => {
    expect(actionTarget(ms, 2, false, [])?.file_id).toBe(3)
    expect(actionTarget(ms, 2, true, [0, 2])?.file_id).toBe(3)
    expect(actionTarget(ms, 2, true, [0, 1])).toBeUndefined()
  })

  test('←/→ wrap through the cluster, or through the panes while comparing', () => {
    expect(stepIndex(5, 4, 1, false, [])).toBe(0)
    expect(stepIndex(5, 0, -1, false, [])).toBe(4)
    expect(stepIndex(5, 3, 1, true, [1, 3])).toBe(1)
    expect(stepIndex(5, 2, 1, true, [1, 3])).toBe(1)
    expect(stepIndex(5, 2, 1, true, [])).toBeNull()
    expect(stepIndex(0, 0, 1, false, [])).toBeNull()
  })

  test('entering compare starts from the photo on screen, or keeps the selection', () => {
    expect(enterCompare([], 3)).toEqual({ compareIdx: [3], activeIndex: 3 })
    expect(enterCompare([0, 1], 3)).toEqual({ compareIdx: [0, 1], activeIndex: 0 })
    expect(enterCompare([0, 3], 3)).toEqual({ compareIdx: [0, 3], activeIndex: 3 })
  })

  test('rail clicks add and remove panes, at most four, keeping an active pane', () => {
    expect(compareSelect([0], 2, 0)).toEqual({ compareIdx: [0, 2], activeIndex: 0 })
    expect(compareSelect([0, 1, 2, 3], 4, 0)).toBeNull()
    expect(compareSelect([0, 1, 2], 1, 1)).toEqual({ compareIdx: [0, 2], activeIndex: 2 })
    expect(compareSelect([0, 1, 2], 2, 2)).toEqual({ compareIdx: [0, 1], activeIndex: 1 })
    expect(compareSelect([0], 0, 0)).toEqual({ compareIdx: [], activeIndex: 0 })
    expect(compareSelect([], 3, 0)).toEqual({ compareIdx: [3], activeIndex: 3 })
  })

  test('pane keys and the grid', () => {
    expect(paneIndex([4, 1, 2], 5, 2)).toBe(1)
    expect(paneIndex([4, 1], 5, 3)).toBeUndefined()
    expect([1, 2, 3, 4].map(compareGrid)).toEqual([
      { cols: 1, rows: 1 },
      { cols: 2, rows: 1 },
      { cols: 2, rows: 2 },
      { cols: 2, rows: 2 },
    ])
  })
})

describe('zoom', () => {
  const at = (zoom: number, x = 0, y = 0) => ({ zoom, pan: { x, y } })

  test('steps by 1.5×, clamps to 1–8, and resets the pan at fit', () => {
    expect(stepZoom(at(2, 10, 5), 1)).toEqual(at(3, 10, 5))
    expect(stepZoom(at(6, 1, 1), 1).zoom).toBe(MAX_ZOOM)
    expect(stepZoom(at(1.2, 40, 9), -1)).toEqual(at(1))
    expect(viewAt(0.5, { x: 3, y: 3 })).toEqual(at(1))
  })

  test('a wheel or pinch step scales smoothly, within the range', () => {
    expect(zoomBy(at(2, 4, 4), 1.25)).toEqual(at(2.5, 4, 4))
    expect(zoomBy(at(2, 4, 4), 0.1)).toEqual(at(1))
    expect(zoomBy(at(4), 10).zoom).toBe(MAX_ZOOM)
  })

  test('labels', () => {
    expect(zoomLabel(1)).toBe('100%')
    expect(zoomLabel(2.25)).toBe('225%')
    expect(position(6, 12)).toBe('07 / 12')
  })
})

describe('info and ratings', () => {
  test('ratings show only while on, never for videos, App’s map first', () => {
    expect(ratingOf(m(1, { quality: 6.1 }), { 1: 7.4 }, true)).toBe(7.4)
    expect(ratingOf(m(1, { quality: 6.1 }), {}, true)).toBe(6.1)
    expect(ratingOf(m(1, { quality: 6.1 }), {}, false)).toBeUndefined()
    expect(ratingOf(m(1, { kind: 'video' }), { 1: 5 }, true)).toBeUndefined()
  })

  test('EXIF rows in v1 order, Quality last', () => {
    const rows = exifRows({ iso: '400', camera: 'X-T5', aperture: 'f/2.8', captured: 'Sep 2, 05:45' }, 7.25)
    expect(rows.map(([k]) => k)).toEqual(['Camera', 'Aperture', 'ISO', 'Captured', 'Quality'])
    expect(rows.at(-1)).toEqual(['Quality', '7.3 / 10'])
    expect(exifRows({}, undefined)).toEqual([])
  })

  test('GPS label and map link', () => {
    expect(gpsOf({ gps_lat: 46.5405, gps_lon: 11.8853 })).toEqual({
      label: '46.54050, 11.88530',
      href: 'https://www.google.com/maps/search/?api=1&query=46.5405,11.8853',
    })
    expect(gpsOf({ gps_lat: 46.5 })).toBeNull()
  })

  test('reviewed count and image names', () => {
    expect(reviewedCount([m(1, { picked: true }), m(2), m(3, { rejected: true })])).toBe(2)
    expect(imageLabel(m(9, { picked: true }))).toBe('IMG_9.JPG, picked')
    expect(imageLabel(m(9))).toBe('IMG_9.JPG')
  })
})
