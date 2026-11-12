import { describe, expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { FolderScreen } from '../../design/screens/folder/FolderScreen'
import type { Cluster, FolderSummary, SettingsBody } from '../../design/types'
import type { FolderSnapshot } from './folderStore'
import { folderScreenProps, scanOf, SCAN_STARTING, selectionOf, summaryOf, usableFilter, type FolderInputs } from './folderProps'

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

const CLUSTERS: Cluster[] = [
  { id: 1, device: 'Camera A', time: 'Mar 1, 10:00', members: [{ file_id: 11, path: 'a.jpg', content_hash: 'h', picked: true, rejected: false, kind: 'photo', format: 'jpeg' }] },
  { id: 2, device: 'Camera B', time: 'Mar 1, 11:00', members: [{ file_id: 21, path: 'b.mov', content_hash: 'h', picked: false, rejected: true, kind: 'video', format: 'mov' }] },
]

const ROW: FolderSummary = {
  id: 'f1',
  name: 'Trip',
  parentPath: '/Volumes/Some Drive/Shoots',
  state: 'ready',
  month: '2026-03',
  firstCaptured: null,
  lastCaptured: null,
  photos: 1,
  videos: 1,
  clusters: 2,
  picked: 1,
  rejected: 1,
  previews: [],
  volumeName: 'Some Drive',
}

const snapshot = (over: Partial<FolderSnapshot> = {}): FolderSnapshot => ({
  status: 'ready',
  value: {
    name: 'Trip',
    picked: 1,
    total: 2,
    clusters: CLUSTERS,
    devices: ['Camera A', 'Camera B'],
    failed: 0,
    picksDir: 'picks',
    picksBlocked: null,
    picksMissing: 0,
  },
  error: null,
  notice: null,
  reclustering: false,
  busyFileIds: [],
  ratings: {},
  pickBlocked: null,
  marks: new Map(),
  ...over,
})

const noop = () => {}
const inputs = (over: Partial<FolderInputs> = {}): FolderInputs => ({
  folderId: 'f1',
  snap: snapshot(),
  row: ROW,
  scan: undefined,
  settings: settings(),
  quality: null,
  filter: { query: '', device: '', sort: 'time-asc' },
  chosen: null,
  openMenu: null,
  toasts: { toasts: [], onUndoToast: noop, onDismissToast: noop },
  media: { thumb: () => '/t.jpg', full: () => '/f.jpg' },
  handlers: {
    onBack: noop,
    onOpenPicks: noop,
    onOpenCluster: noop,
    onSelectCluster: noop,
    onFilter: noop,
    onOpenMenu: noop,
    onRecluster: noop,
    onPick: noop,
    onSettings: noop,
    onHelp: noop,
  },
  ...over,
})

describe('folderProps', () => {
  test('scan: progress when there is some, a starting look while scanning without, none when ready', () => {
    const p = { stage: 'thumb', done: 3, total: 9, failed: 0, state: 'running' } as const
    expect(scanOf({ state: 'scanning', progress: p }, CLUSTERS)).toBe(p)
    expect(scanOf({ state: 'scanning', progress: null }, CLUSTERS)).toBe(SCAN_STARTING)
    expect(scanOf({ state: 'offline', progress: { ...p, state: 'stopped' } }, CLUSTERS)).toMatchObject({ state: 'stopped' })
    expect(scanOf({ state: 'ready', progress: null }, CLUSTERS)).toBeNull()
    expect(scanOf(undefined, CLUSTERS)).toBeNull()
    // Queued before its first scan: the first-scan checklist, not "no photos".
    expect(scanOf({ state: 'queued', progress: null }, [])).toBe(SCAN_STARTING)
    expect(scanOf({ state: 'queued', progress: null }, CLUSTERS)).toBeNull()
  })

  test('no grouping: the design reads none, whatever the visual-grouping setting', () => {
    for (const s of [settings(), settings({ visual_grouping: false }), undefined]) {
      expect('grouping' in folderScreenProps(inputs({ settings: s }))).toBe(false)
    }
  })

  test('summary: the Library row, else what the grid says', () => {
    expect(summaryOf('f1', ROW, undefined)).toBe(ROW)
    expect(summaryOf('f1', null, snapshot().value)).toMatchObject({ id: 'f1', name: 'Trip', photos: 1, videos: 1, clusters: 2, picked: 1 })
    expect(summaryOf('f1', undefined, undefined)).toMatchObject({ id: 'f1', name: '', photos: 0 })
  })

  test('a camera that is gone is dropped from the filter; the cursor stays on the list', () => {
    expect(usableFilter({ query: 'x', device: 'Camera Z', sort: 'time-desc' }, ['Camera A'])).toEqual({ query: 'x', device: '', sort: 'time-desc' })
    expect(usableFilter({ query: '', device: 'Camera A', sort: 'time-asc' }, ['Camera A']).device).toBe('Camera A')
    expect(selectionOf(CLUSTERS, 2)).toBe(2)
    expect(selectionOf(CLUSTERS, 9)).toBe(1)
    expect(selectionOf([], 9)).toBeNull()
  })

  test('notice: offline from the scan state wins; the store notice otherwise', () => {
    expect(folderScreenProps(inputs({ snap: snapshot({ notice: 'read-only' }) })).notice).toBe('read-only')
    expect(folderScreenProps(inputs({ snap: snapshot({ notice: 'read-only' }), scan: { state: 'offline', progress: null } })).notice).toBe('offline')
  })

  test('every prop comes from the inputs', () => {
    const p = folderScreenProps(inputs({ filter: { query: '', device: '', sort: 'time-asc' }, chosen: 2 }))
    expect(p.folder).toBe(ROW)
    expect(p.clusters).toBe(CLUSTERS)
    expect(p.devices).toEqual(['Camera A', 'Camera B'])
    expect(p.selectedClusterId).toBe(2)
    expect(p.settings).toEqual({ hide_rejected_photos: false, quality_rating: true })
    expect(p.picksDir).toBe('picks')
    expect(folderScreenProps(inputs({ snap: snapshot({ status: 'loading', value: undefined }) })).clusters).toBeNull()
  })

  test('the screen renders its title bar first, uncovered (the window drags by it)', () => {
    for (const snap of [snapshot(), snapshot({ status: 'loading', value: undefined }), snapshot({ notice: 'offline' })]) {
      const html = renderToStaticMarkup(<FolderScreen {...folderScreenProps(inputs({ snap }))} />)
      // The screen's root, then the title bar as its first child: nothing wraps or precedes it.
      expect(html).toMatch(/^<div class="relative flex flex-col h-full min-h-0"><header[^>]*data-titlebar/)
      // Nothing positioned over the window (a full-size overlay would take the mouse-down).
      const beforeBar = html.slice(0, html.indexOf('data-titlebar'))
      expect(beforeBar).not.toMatch(/\b(absolute|fixed) inset-0/)
      // Nor anywhere after it: the one layer over the page is the toast stack, bottom-right and click-through.
      expect(html).not.toMatch(/\b(absolute|fixed) inset-0/)
      expect(html).toMatch(/data-overlay="toasts" class="absolute bottom-4 right-4 [^"]*pointer-events-none/)
    }
  })
})
