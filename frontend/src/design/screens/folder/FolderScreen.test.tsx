// The Folder page's title bar shows only this folder's work: during its
// first scan the checklist is the progress (nothing in the bar); after it, a
// scan of this folder or this folder's rating pass, never another folder's.
import { expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import type { Cluster, FolderSummary, MediaURLs, QualityStatus, ScanProgress } from '../../types'
import { FolderScreen } from './FolderScreen'
import type { FolderProps } from './props'

const noop = () => {}
const folder: FolderSummary = {
  id: 'here',
  name: 'Here',
  parentPath: '/Trips',
  state: 'ready',
  month: '2026-09',
  firstCaptured: null,
  lastCaptured: null,
  photos: 0,
  videos: 0,
  clusters: 0,
  picked: 0,
  rejected: 0,
  previews: [],
}
const clustering: ScanProgress = { stage: 'cluster', done: 140, total: 248, failed: 0, state: 'running' }
const pass = (folder_id: string): QualityStatus => ({
  enabled: true,
  state: 'scoring',
  active: true,
  done: 108,
  total: 248,
  rated: 0,
  folder_id,
  passes: [{ folder_id, done: 108, total: 248 }],
})
const media: MediaURLs = { thumb: () => '/t.jpg', full: () => '/f.jpg' }

function page(p: { clusters: Cluster[]; scan: ScanProgress | null; quality: QualityStatus | null } & Partial<FolderProps>): string {
  const props = {
    folder,
    filter: { query: '', device: '', sort: 'time-asc' },
    devices: [],
    openMenu: null,
    selectedClusterId: null,
    settings: { hide_rejected_photos: false, quality_rating: true },
    reclustering: false,
    failedFiles: 0,
    picksMissing: 0,
    notice: null,
    picksDir: 'picks',
    toasts: [],
    media,
    ...p,
  } as unknown as FolderProps
  const all = new Proxy(props, { get: (t, k) => (k in t ? t[k as keyof FolderProps] : noop) })
  return renderToStaticMarkup(<FolderScreen {...all} />)
}

function bar(p: { clusters: Cluster[]; scan: ScanProgress | null; quality: QualityStatus | null }): string {
  const html = page(p)
  return html.slice(html.indexOf('<header'), html.indexOf('</header>'))
}

const ONE = [{ id: 1, device: 'X-T5', time: 'Sep 3 · 10:00', members: [] } as unknown as Cluster]

test('no hint row under the filter bar (the owner: the shortcut sheet is the keyboard help)', () => {
  const h = page({ clusters: ONE, scan: null, quality: null, settings: { hide_rejected_photos: true, quality_rating: true } })
  expect(h).not.toContain('data-testid="hint-shortcuts"')
  expect(h).not.toContain('Navigate clusters')
  expect(h).not.toContain('Picks copy to')
  expect(h).not.toContain('data-testid="rejected-hidden"')
  // The sheet stays one click away, in the title bar.
  expect(bar({ clusters: ONE, scan: null, quality: null })).toContain('aria-label="Keyboard shortcuts"')
})

test('Open Picks shows ⌘P', () => {
  const h = bar({ clusters: ONE, scan: null, quality: null })
  expect(h).toContain('⌘P')
  expect(h).not.toContain('⇧⌘P')
})

test('no time-only notice: grouping by time is the user’s choice, and nothing is downloaded', () => {
  const html = page({ clusters: ONE, scan: null, quality: null })
  expect(html).not.toContain('data-testid="notice-time-only"')
  expect(html).not.toContain('Grouped by capture time only')
  expect(html).not.toContain('download')
})

test('first scan: the bar shows no indicator, whatever the quality worker does', () => {
  for (const quality of [pass('elsewhere'), pass('here'), null]) {
    const h = bar({ clusters: [], scan: clustering, quality })
    expect(h).not.toContain('data-testid="quality-progress"')
    expect(h).not.toContain('data-testid="scan-progress"')
  }
})

test('after the scan: this folder’s rating pass shows, another folder’s does not', () => {
  const cluster = { id: 1, members: [] } as unknown as Cluster
  expect(bar({ clusters: [cluster], scan: null, quality: pass('here') })).toContain('data-testid="quality-progress"')
  expect(bar({ clusters: [cluster], scan: null, quality: pass('elsewhere') })).not.toContain('data-testid="quality-progress"')
})

test('another folder’s pass just begun (0 / 0, the status’s counts): this folder’s older pass still shows its counts', () => {
  const cluster = { id: 1, members: [] } as unknown as Cluster
  const q: QualityStatus = {
    ...pass('elsewhere'),
    done: 0,
    total: 0,
    passes: [
      { folder_id: 'here', done: 905, total: 2768 },
      { folder_id: 'elsewhere', done: 0, total: 0 },
    ],
  }
  expect(bar({ clusters: [cluster], scan: null, quality: q })).toContain('905 / 2,768')
})

test('this folder’s own pass just begun (0 / 0): nothing to count yet, so no indicator (as a single pass)', () => {
  const cluster = { id: 1, members: [] } as unknown as Cluster
  const q: QualityStatus = { ...pass('here'), done: 0, total: 0, passes: [{ folder_id: 'here', done: 0, total: 0 }] }
  expect(bar({ clusters: [cluster], scan: null, quality: q })).not.toContain('data-testid="quality-progress"')
})

test('two passes, this folder’s the older: the bar shows this folder’s own counts, not the latest pass’s', () => {
  const cluster = { id: 1, members: [] } as unknown as Cluster
  const both: QualityStatus = {
    ...pass('elsewhere'),
    passes: [
      { folder_id: 'here', done: 30, total: 75 },
      { folder_id: 'elsewhere', done: 108, total: 248 },
    ],
  }
  const h = bar({ clusters: [cluster], scan: null, quality: both })
  expect(h).toContain('30 / 75')
  expect(h).not.toContain('108 / 248')
})
