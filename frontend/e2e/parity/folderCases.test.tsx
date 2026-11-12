// The Folder cases (200–229) against the approved scenarios, without a
// browser:
//   - the cases cover exactly the registered Folder states, each driven;
//   - FOLDER_STEPS (cases.ts, which runs on Node and cannot import the
//     scenarios) stands for exactly the props the fixture cannot carry, and
//     the fixture's `calls` for exactly the re-cluster the steps press;
//   - the app's own session and stores, fed each fixture and the steps'
//     events and calls, give the Folder screen the scenario's data;
//   - the toasts the app puts up itself after a re-cluster carry the
//     approved copy of the toasts 222 and 223 seed;
//   - FOLDER_MEDIA gives every member the photo the scenario shows for it.
import { describe, expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { TOAST_RECLUSTER_FAILED, TOAST_RECLUSTERED } from '../../prototype/mock/folder'
import { scenarios as folderSceneList } from '../../prototype/scenarios/folder'
import type { AppEvents, On } from '../../src/app/shell/events'
import { createFolderSession } from '../../src/app/shell/folderSession'
import { createScanStore } from '../../src/app/shell/scanStore'
import { createToasts } from '../../src/app/shell/toasts'
import { folderScreenProps } from '../../src/app/folder/folderProps'
import { createFolderStore, createSummaryStore, type SummaryRow } from '../../src/app/folder/folderStore'
import { reclusterWithToast } from '../../src/app/folder/recluster'
import { filterClusters } from '../../src/design/screens/folder/filter'
import { FolderScreen } from '../../src/design/screens/folder/FolderScreen'
import type { FolderProps } from '../../src/design/screens/folder/props'
import type { FolderFilter, ModelStatus, QualityStatus, SettingsBody } from '../../src/design/types'
import type { FolderView } from '../../bindings/github.com/volodymyrsmirnov/choix/internal/app/catalog/models'
import { CASES, FOLDER_STATES, FOLDER_STEPS, type FolderSteps } from './cases'
import { mediaPhoto } from './fixtures'
import { FOLDER, FOLDER_MEDIA, folderScenarioProps, RECLUSTER_CALLS } from './fixtures/folder'
import { MEDIA } from './fixtures/table'
import { bindingError, type AppErrorCause, type ParityFixture } from './mock-bindings/control'

const flush = async () => {
  for (let i = 0; i < 5; i++) await new Promise<void>((resolve) => setTimeout(resolve, 0))
}

function fakeBus() {
  const handlers = new Map<string, Set<(data: unknown) => void>>()
  const on: On = (name, handler) => {
    const set = handlers.get(name) ?? new Set()
    set.add(handler as (data: unknown) => void)
    handlers.set(name, set)
    return () => set.delete(handler as (data: unknown) => void)
  }
  const emit = <K extends keyof AppEvents>(name: K, data: AppEvents[K]) => {
    for (const h of [...(handlers.get(name) ?? [])]) h(data)
  }
  return { on, emit }
}

const never = <T,>() => new Promise<T>(() => {})

/** The label of the checked item of the menu `menu` of `p`'s page, as the user reads it there. */
function checkedLabel(p: FolderProps, menu: 'device' | 'sort'): string {
  const html = renderToStaticMarkup(<FolderScreen {...p} openMenu={menu} />)
  const m = /aria-checked="true"[\s\S]*?<\/span>([^<]+)<\/button>/.exec(html)
  if (!m) throw new Error(`no checked item in the ${menu} menu`)
  return m[1]!
}

/** The filter the steps leave: typed text, chosen camera, chosen order (by its label in the sort menu). */
function filterOf(p: FolderProps, s: FolderSteps): FolderFilter {
  return { query: s.query ?? '', device: s.device ?? '', sort: s.sort !== undefined && checkedLabel(p, 'sort') === s.sort ? p.filter.sort : 'time-asc' }
}

const recluster = (n: number) => RECLUSTER_CALLS[n]
const isError = (c: 'pending' | AppErrorCause | undefined): c is AppErrorCause => typeof c === 'object'

test('the cases cover exactly the registered Folder states, each driven', () => {
  expect(FOLDER_STATES).toEqual(folderSceneList.map((s) => s.n))
  expect(Object.keys(FOLDER).map(Number)).toEqual(FOLDER_STATES)
  for (const n of FOLDER_STATES) {
    const c = CASES.find((x) => x.state === n)
    expect([n, c?.skip, typeof c?.drive]).toEqual([n, undefined, 'function'])
  }
  for (const n of Object.keys(FOLDER_STEPS).map(Number)) expect(FOLDER_STATES).toContain(n)
  // `calls` only where a step presses Re-cluster, and every such step has its answer.
  expect(Object.keys(RECLUSTER_CALLS).map(Number)).toEqual(FOLDER_STATES.filter((n) => FOLDER_STEPS[n]?.recluster))
})

describe('each step stands for the scenario prop the fixture cannot carry, and only where it shows', () => {
  for (const n of FOLDER_STATES) {
    test(`#${n}`, () => {
      const p = folderScenarioProps(n)
      const s = FOLDER_STEPS[n] ?? {}
      expect(p.filter).toEqual(filterOf(p, s))
      if (s.sort !== undefined) expect(p.filter.sort).toBe('time-desc')
      if (s.device !== undefined) {
        expect(p.devices).toContain(s.device)
        expect(checkedLabel(p, 'device')).toBe(s.device)
      }
      expect(p.openMenu).toBe(s.menu ?? null)
      const visible = p.clusters ? filterClusters(p.clusters, p.filter) : []
      expect(p.selectedClusterId).toBe(visible[s.down ?? 0]?.id ?? null)
      expect(p.scan?.state === 'stopped').toBe(s.stopRun !== undefined)
      const call = recluster(n)
      expect(p.reclustering).toBe(s.recluster === true && call === 'pending')
      const readOnly = s.recluster === true && isError(call) && call.code === 'read_only'
      expect(p.notice).toBe(s.offline ? 'offline' : readOnly ? 'read-only' : null)
      // A refused re-cluster puts up a toast, which the state does not show: the step dismisses it.
      expect(s.dismissToast === true).toBe(s.recluster === true && isError(call))
      if (s.dismissToast) expect(p.toasts).toEqual([])
    })
  }
})

describe("the app's session and stores, fed the fixture, give the screen the scenario's data", () => {
  for (const n of FOLDER_STATES) {
    test(`#${n}`, async () => {
      const p = folderScenarioProps(n)
      const s = FOLDER_STEPS[n] ?? {}
      const f: ParityFixture = FOLDER[n]!
      const folderId = p.folder.id
      expect(f.route).toBe(`#/folder/${folderId}`)
      const bus = fakeBus()
      const view = () => (f.view === 'pending' || f.view === 'error' || f.view === undefined ? never<FolderView>() : Promise.resolve(structuredClone(f.view)))
      const rows = () => Promise.resolve(structuredClone(f.folders) as unknown as SummaryRow[])
      const call = recluster(n)

      const session = createFolderSession({ open: view, close: async () => {}, on: bus.on, toLibrary: () => {}, settingsToLibrary: () => {} })
      const settings = f.settings as SettingsBody
      const store = createFolderStore({
        folderId,
        session,
        on: bus.on,
        clusters: view,
        ratings: async () => ({}),
        setPick: never,
        undo: never,
        recluster: () => (call === 'pending' ? never<void>() : isError(call) ? Promise.reject(bindingError(call)) : Promise.resolve()),
        hideRejected: () => settings.hide_rejected_photos,
      })
      const summary = createSummaryStore({ folderId, list: rows, on: bus.on })
      const scan = createScanStore({ list: rows, on: bus.on })
      const stops = [session.start(), store.start(), summary.start(), scan.start()]
      session.route({ name: 'folder', folderId })
      await flush()

      // What the case does after the load (cases.ts folderCase), in its order.
      if (s.stopRun !== undefined) {
        const run = (f.folders as SummaryRow[])[0]!
        bus.emit('scan:progress', { folderId, progress: { ...run.scan!, state: 'stopped' }, phase: 'failed' })
        bus.emit('scan:state', { folderId, state: 'error', error: s.stopRun })
      }
      if (s.offline) bus.emit('library:changed', { reason: 'offline', folderId })
      if (s.recluster) void store.recluster().catch(() => {})
      await flush()

      const filter = filterOf(p, s)
      const visible = store.get().value ? filterClusters(store.get().value!.clusters, filter) : []
      const got = folderScreenProps({
        folderId,
        snap: store.get(),
        row: summary.get().value,
        scan: scan.get().value?.[folderId],
        settings,
        models: f.models as ModelStatus[],
        quality: (f.quality as QualityStatus | undefined) ?? null,
        filter,
        chosen: s.down ? (visible[s.down]?.id ?? null) : null,
        openMenu: s.menu ?? null,
        toasts: { toasts: f.toasts ?? [], onUndoToast: () => {}, onDismissToast: () => {} },
        media: p.media,
        handlers: {} as never,
      })

      // The row of a folder shown scanning is `scanning` with its run; the screen reads neither field of it.
      // Nor `added_at` (the Library's "Recently added" order only), which the app's summary row
      // carries once the Library binding sends it.
      const unread = <T extends { added_at?: number }>({ added_at: _a, ...rest }: T) => rest
      const shown = ({ state: _s, scan: _r, ...rest }: FolderProps['folder']) => unread(rest)
      expect(shown(got.folder)).toEqual(shown(p.folder))
      if (!p.scan) expect(unread(got.folder)).toEqual(unread(p.folder))
      for (const key of [
        'clusters',
        'scan',
        'filter',
        'devices',
        'openMenu',
        'selectedClusterId',
        'settings',
        'grouping',
        'quality',
        'reclustering',
        'failedFiles',
        'picksMissing',
        'notice',
        'picksDir',
        'toasts',
      ] as const) {
        expect([key, got[key]]).toEqual([key, p[key]])
      }
      for (const stop of stops) stop()
    })
  }
})

describe('the toasts the app puts up itself carry the approved copy of 222–223', () => {
  const copy = (t: { tone: string; title: string; body?: string }) => ({ tone: t.tone, title: t.title, body: t.body })

  test('222: Re-cluster went through', async () => {
    const toasts = createToasts()
    await reclusterWithToast({ recluster: async () => true }, toasts)
    expect(toasts.items().map(copy)).toEqual([copy(TOAST_RECLUSTERED)])
  })

  test('223: Re-cluster failed, Go’s message in the body', async () => {
    const toasts = createToasts()
    await reclusterWithToast(
      {
        recluster: async () => {
          throw bindingError({ code: 'internal', message: TOAST_RECLUSTER_FAILED.body! })
        },
      },
      toasts,
    )
    expect(toasts.items().map(copy)).toEqual([copy(TOAST_RECLUSTER_FAILED)])
  })
})

test('FOLDER_MEDIA gives each member the photo the scenario shows for it, and serve-app reads the same manifest', () => {
  for (const n of FOLDER_STATES) {
    const p = folderScenarioProps(n)
    for (const m of (p.clusters ?? []).flatMap((c) => c.members)) {
      expect([m.content_hash, FOLDER_MEDIA[m.content_hash]]).toEqual([m.content_hash, mediaPhoto(p.media.thumb(m))])
    }
  }
  expect(Object.keys(FOLDER_MEDIA).length).toBeGreaterThan(50)
  for (const [hash, name] of Object.entries(FOLDER_MEDIA)) expect(MEDIA[hash]).toBe(name)
  const r = Bun.spawnSync(['bun', `${import.meta.dir}/fixtures/media.ts`])
  expect(r.exitCode).toBe(0)
  expect(JSON.parse(r.stdout.toString())).toEqual(MEDIA)
})
