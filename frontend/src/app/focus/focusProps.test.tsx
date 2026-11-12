import { describe, expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { FocusScreen } from '../../design/screens/focus/FocusScreen'
import type { FocusResponse, Member, SettingsBody } from '../../design/types'
import type { SessionState } from '../shell/folderSession'
import { mediaFor } from '../shell/media'
import { focusScreenProps, folderNameOf, leaveReason, qualityOnOf, type FocusInputs } from './focusProps'
import type { FocusSnapshot } from './focusStore'
import { SHOW_INFO_KEY, showInfoPrefs } from './prefs'

const member = (file_id: number, over: Partial<Member> = {}): Member => ({
  file_id,
  path: `Trip/IMG_${file_id}.jpg`,
  content_hash: `h${file_id}`,
  picked: false,
  rejected: false,
  kind: 'photo',
  format: 'jpeg',
  ...over,
})

const DATA: FocusResponse = {
  cluster: {
    id: 7,
    device: 'Camera A',
    time: 'Mar 1 · 10:00',
    members: [member(11), member(12, { picked: true, quality: 6.1 }), member(13, { kind: 'video', format: 'mov', path: 'Trip/CLIP_13.mov' })],
  },
  cluster_index: 2,
  cluster_count: 5,
  exif: { '11': { camera: 'Camera A', aperture: 'f/8.0', gps_lat: 46.1, gps_lon: 11.2 } },
  next_cluster_id: 8,
  prev_cluster_id: 6,
}

const snapshot = (over: Partial<FocusSnapshot> = {}): FocusSnapshot => ({
  clusterId: 7,
  data: DATA,
  loadError: null,
  activeIndex: 0,
  view: { zoom: 1, pan: { x: 0, y: 0 } },
  comparing: false,
  compareIdx: [],
  showInfo: false,
  mediaState: { 11: 'loading', 12: 'ready' },
  busyFileIds: [],
  ratings: { 12: 7.4 },
  ...over,
})

const noop = () => {}
const inputs = (over: Partial<FocusInputs> = {}): FocusInputs => ({
  snap: snapshot(),
  folderName: 'Trip',
  qualityOn: true,
  media: mediaFor('f1'),
  toasts: { toasts: [], onUndoToast: noop, onDismissToast: noop },
  handlers: {
    onBack: noop,
    onCluster: noop,
    onSelect: noop,
    onCompareSelect: noop,
    onCompare: noop,
    onPick: noop,
    onView: noop,
    onInfo: noop,
    onMediaState: noop,
    onRetry: noop,
    onHelp: noop,
    onSettings: noop,
  },
  ...over,
})

const settings = (over: Partial<SettingsBody> = {}): SettingsBody => ({
  bucket_size: 600,
  similarity: 0.92,
  picks_dir: 'picks',
  advance_on_action: false,
  hide_rejected_photos: false,
  cross_device_merging: false,
  quality_rating: true,
  visual_grouping: true,
  ...over,
})

describe('focusProps', () => {
  test('every prop comes from the snapshot, the inputs or the handlers', () => {
    const i = inputs({ snap: snapshot({ activeIndex: 1, view: { zoom: 2, pan: { x: 5, y: -3 } }, busyFileIds: [12] }) })
    const p = focusScreenProps(i)
    expect(p).toMatchObject({
      folder: 'Trip',
      data: DATA,
      loadError: null,
      activeIndex: 1,
      zoom: 2,
      pan: { x: 5, y: -3 },
      showInfo: false,
      comparing: false,
      compareIdx: [],
      busyFileIds: [12],
      mediaState: { 11: 'loading', 12: 'ready' },
      ratings: { 12: 7.4 },
      qualityOn: true,
      toasts: [],
    })
    expect(p.media).toBe(i.media)
    for (const k of Object.keys(i.handlers) as (keyof FocusInputs['handlers'])[]) expect(p[k]).toBe(i.handlers[k])
    expect(p.onUndoToast).toBe(i.toasts.onUndoToast)
    expect(p.onDismissToast).toBe(i.toasts.onDismissToast)
  })

  test('loadError shows only while there is no cluster', () => {
    expect(focusScreenProps(inputs({ snap: snapshot({ data: null, loadError: 'disk I/O error' }) })).loadError).toBe('disk I/O error')
    expect(focusScreenProps(inputs({ snap: snapshot({ loadError: 'disk I/O error' }) })).loadError).toBeNull()
  })

  test('quality is on until Settings say otherwise (the config default)', () => {
    expect(qualityOnOf(undefined)).toBe(true)
    expect(qualityOnOf(settings({ quality_rating: false }))).toBe(false)
  })

  test('the Back label is the folder’s name: the grid’s, else the Library row’s, else the session’s', () => {
    expect(folderNameOf('f1', 'Grid name', [{ id: 'f1', name: 'Row name' }], null)).toBe('Grid name')
    expect(folderNameOf('f1', undefined, [{ id: 'f2', name: 'Other' }, { id: 'f1', name: 'Row name' }], null)).toBe('Row name')
    expect(folderNameOf('f1', undefined, undefined, { folder: 'Session name' })).toBe('Session name')
    expect(folderNameOf('f1', undefined, undefined, null)).toBe('')
  })

  test('Focus leaves for the folder while it is offline or could not be opened', () => {
    const session = (status: SessionState['status'], folderId = 'f1'): SessionState => ({ folderId, status, view: null, error: null })
    expect(leaveReason('f1', session('open'), null, 'ready')).toBeNull()
    expect(leaveReason('f1', session('opening'), null, undefined)).toBeNull()
    expect(leaveReason('f1', session('offline'), null, 'ready')).toBe('offline')
    expect(leaveReason('f1', session('open'), 'offline', 'ready')).toBe('offline')
    expect(leaveReason('f1', session('open'), null, 'offline')).toBe('offline')
    expect(leaveReason('f1', session('error'), null, 'ready')).toBe('error')
    expect(leaveReason('f1', session('open'), 'read-only', 'ready')).toBeNull()
    // Another folder's session (the route is about to change it) says nothing about this one.
    expect(leaveReason('f1', session('error', 'f2'), null, 'ready')).toBeNull()
  })

  test('the screen renders its title bar first, uncovered (the window drags by it)', () => {
    const cases: FocusSnapshot[] = [
      snapshot(),
      snapshot({ data: null }),
      snapshot({ data: null, loadError: 'disk I/O error' }),
      snapshot({ showInfo: true }),
      snapshot({ activeIndex: 2 }),
      snapshot({ comparing: true, compareIdx: [0, 1, 2], activeIndex: 1, view: { zoom: 2.25, pan: { x: -40, y: 10 } } }),
      snapshot({ comparing: true, compareIdx: [] }),
    ]
    for (const snap of cases) {
      const html = renderToStaticMarkup(<FocusScreen {...focusScreenProps(inputs({ snap }))} />)
      // The screen's root, then the title bar as its first child: nothing wraps or precedes it.
      expect(html).toMatch(/^<div class="relative flex flex-col h-full min-h-0"><header[^>]*data-titlebar/)
      const barEnd = html.indexOf('</header>')
      expect(barEnd).toBeGreaterThan(0)
      // Nothing positioned covers the window: every full-size layer is inside the stage, after the bar.
      expect(html).not.toMatch(/\bfixed\b[^"]*inset-0/)
      const bar = html.slice(0, barEnd)
      expect(bar).not.toMatch(/\babsolute\b[^"]*inset-0/)
      const stage = html.indexOf('<main')
      expect(stage).toBeGreaterThan(barEnd)
      for (const m of html.matchAll(/class="[^"]*\babsolute\b[^"]*\binset-0\b/g)) expect(m.index).toBeGreaterThan(stage)
      // The toast stack is bottom-right and click-through.
      expect(html).toMatch(/data-overlay="toasts" class="absolute bottom-4 right-4 [^"]*pointer-events-none/)
    }
  })
})

describe('showInfoPrefs', () => {
  const memory = () => {
    const m = new Map<string, string>()
    return {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => {
        m.set(k, v)
      },
      m,
    }
  }

  test('open until the user closes it (v1), then remembered', () => {
    const s = memory()
    const prefs = showInfoPrefs(() => s)
    expect(prefs.showInfo()).toBe(true)
    prefs.setShowInfo(false)
    expect(s.m.get(SHOW_INFO_KEY)).toBe('0')
    expect(prefs.showInfo()).toBe(false)
    prefs.setShowInfo(true)
    expect(s.m.get(SHOW_INFO_KEY)).toBe('1')
    expect(SHOW_INFO_KEY).toBe('choix.focus.showInfo')
  })

  test('a storage that throws (or is missing) is ignored', () => {
    const throwing = showInfoPrefs(() => ({
      getItem: () => {
        throw new Error('denied')
      },
      setItem: () => {
        throw new Error('denied')
      },
    }))
    expect(throwing.showInfo()).toBe(true)
    expect(() => throwing.setShowInfo(false)).not.toThrow()
    const missing = showInfoPrefs(() => {
      throw new Error('no localStorage')
    })
    expect(missing.showInfo()).toBe(true)
    expect(() => missing.setShowInfo(true)).not.toThrow()
  })
})
