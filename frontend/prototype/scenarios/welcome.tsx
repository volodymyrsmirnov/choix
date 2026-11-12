// Welcome and shell states: 11–17 are the first-launch panel (an empty
// Library while a model is still to come; downloads run one at a time,
// CLIP first, so there is no "both downloading" state), 30–37 a folder job or
// rating passes as the Library shows them: in the folder's row, not the title
// bar (a status lives on the element it describes), 40 the full-window error
// screen (a screen failed to render and the return to the Library failed too).
import { ErrorScreen } from '../../src/design/screens/error/ErrorScreen'
import { LibraryScreen } from '../../src/design/screens/library/LibraryScreen'
import { DOLOMITES, LISBON, model, quality, replace, scanAt, scanning, SIX } from '../mock/data'
import type { Scenario } from '../registry'
import { libraryProps, type LibraryState } from './libraryWiring'

const FIRST_LAUNCH = 'mockup-2026-11-11#s-c'
const TITLE_BAR = 'mockup-2026-11-11#s-a'

const welcome = (n: number, label: string, s: Omit<LibraryState, 'folders'>): Scenario => ({
  n,
  group: 'welcome',
  label,
  design: FIRST_LAUNCH,
  subject: '[data-model]',
  render: (ctx) => <LibraryScreen {...libraryProps(ctx, { folders: [], ...s })} />,
})

const NO_NETWORK = 'No internet connection'

/** What #40 shows as the error: a render error's message as WebKit words it. */
export const RENDER_ERROR = "TypeError: undefined is not an object (evaluating 'cluster.members.length')"
/** One rating pass (31): Dolomites (95 photos). */
const ONE_PASS = quality({
  state: 'scoring',
  done: 41,
  total: 95,
  folder_id: DOLOMITES.id,
  passes: [{ folder_id: DOLOMITES.id, done: 41, total: 95 }],
})
/** Two rating passes at once (37): Dolomites' began first, Lisbon's is the latest. */
const TWO_PASSES = quality({
  state: 'scoring',
  done: 120,
  total: 417,
  folder_id: LISBON.id,
  passes: [
    { folder_id: DOLOMITES.id, done: 41, total: 95 },
    { folder_id: LISBON.id, done: 120, total: 417 },
  ],
})
/** Where Reload leads in the prototype: the filled Library. */
const RELOADED = 102

const shell = (n: number, label: string, progress: Parameters<typeof scanAt>, extra: Partial<LibraryState> = {}): Scenario => {
  const scan = scanAt(...progress)
  return {
    n,
    group: 'welcome',
    label,
    design: TITLE_BAR,
    // The scanning folder is the first row, so it is in view at every size.
    subject: '[data-folder="lisbon"]',
    render: (ctx) => (
      <LibraryScreen
        {...libraryProps(ctx, {
          folders: replace(SIX, scan.state === 'running' ? scanning(LISBON, scan) : { ...LISBON, state: 'error', error: 'Scan stopped. Rescan to finish it.' }),
          // What Go reports of the run (the screen draws it in the row, from folders).
          scan: { folder: LISBON.name, progress: scan },
          ...extra,
        })}
      />
    ),
  }
}

export const scenarios: Scenario[] = [
  welcome(11, 'CLIP downloading, quality queued', {
    models: [model('clip', 'downloading', 137_000_000), model('quality', 'queued')],
  }),
  welcome(12, 'CLIP failed (Retry)', {
    models: [model('clip', 'failed', 96_000_000, 'The download stopped at 96 MB: the connection was reset'), model('quality', 'downloading', 63_000_000)],
  }),
  welcome(13, 'Quality failed', {
    models: [model('clip', 'ready'), model('quality', 'failed', 0, 'The server answered 503 Service Unavailable')],
  }),
  welcome(14, 'Offline, both failed', {
    online: false,
    models: [model('clip', 'failed', 0, NO_NETWORK), model('quality', 'failed', 0, NO_NETWORK)],
  }),
  welcome(15, 'Visual grouping off', {
    models: [model('clip', 'disabled'), model('quality', 'downloading', 120_000_000)],
  }),
  welcome(16, 'Quality rating off', {
    models: [model('clip', 'downloading', 137_000_000), model('quality', 'disabled')],
  }),
  welcome(17, 'CLIP ready, quality downloading', {
    models: [model('clip', 'ready'), model('quality', 'downloading', 88_000_000)],
  }),
  shell(30, 'Scan discovering (indeterminate), in its row', ['discover', 0, 0]),
  {
    n: 31,
    group: 'welcome',
    label: 'Rating quality, in its row',
    design: 'v1:components/primitives.tsx',
    subject: '[data-folder="dolomites"]',
    render: (ctx) => <LibraryScreen {...libraryProps(ctx, { folders: SIX, quality: ONE_PASS })} />,
  },
  shell(32, 'Scan stopped, in its row', ['analyze', 141, 420, { state: 'stopped' }]),
  shell(33, 'Scan: reading metadata', ['metadata', 204, 420]),
  shell(34, 'Scan: thumbnails', ['thumb', 325, 420, { failed: 7 }]),
  shell(35, 'Scan: analyzing', ['analyze', 271, 420]),
  // Clustering is counted like the other stages (files grouped so far).
  shell(36, 'Scan: clustering', ['cluster', 316, 420]),
  {
    n: 37,
    group: 'welcome',
    label: 'Two folders rating quality, in their rows',
    design: 'v1:components/primitives.tsx',
    subject: '[data-folder="lisbon"], [data-folder="dolomites"]',
    render: (ctx) => <LibraryScreen {...libraryProps(ctx, { folders: SIX, quality: TWO_PASSES })} />,
  },
  {
    n: 40,
    group: 'welcome',
    label: 'Error screen (a screen failed to render, and so did the Library)',
    design: 'missing design',
    subject: '[data-testid="app-error"]',
    render: (ctx) => <ErrorScreen details={RENDER_ERROR} onReload={() => ctx.store.go(RELOADED)} onCopyDetails={() => {}} />,
  },
]
