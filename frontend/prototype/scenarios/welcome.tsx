// Welcome and shell states: 30–37 a folder job or rating passes as the
// Library shows them: in the folder's row, not the title bar (a status lives
// on the element it describes), 40 the full-window error screen (a screen
// failed to render and the return to the Library failed too). 11–17, the
// first-launch panel of the model downloads, are retired: nothing is
// downloaded any more, and an empty Library is the drop zone (100).
import { ErrorScreen } from '../../src/design/screens/error/ErrorScreen'
import { LibraryScreen } from '../../src/design/screens/library/LibraryScreen'
import { DOLOMITES, LISBON, quality, replace, scanAt, scanning, SIX } from '../mock/data'
import type { Scenario } from '../registry'
import { libraryProps, type LibraryState } from './libraryWiring'

const TITLE_BAR = 'mockup-2026-11-11#s-a'

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
