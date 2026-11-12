// The welcome and Library cases (11–17, 30–37, 100–131; the welcome group's
// error screen, 40, is shellCases.test.ts's) against the
// approved scenarios, without a browser:
//   - the cases cover exactly the registered states of both groups;
//   - LIBRARY_STEPS (cases.ts, which runs on Node and cannot import the
//     scenarios) stands for exactly the props the fixture cannot carry;
//   - the app's own stores, fed each fixture (and state 32's events), give
//     the Library screen the scenario's folders, title-bar run, quality
//     and toasts;
//   - the toasts the app puts up itself carry the approved copy of the
//     toasts 113–115 seed (the mock bindings cannot answer AddFolders or
//     OpenPicksFolder with the result that puts them up).
import { describe, expect, test } from 'bun:test'
import { TOAST_ALREADY, TOAST_READ_ONLY, TOAST_REMOVED } from '../../prototype/mock/library'
import { scenarios as librarySceneList } from '../../prototype/scenarios/library'
import { scenarios as welcomeSceneList } from '../../prototype/scenarios/welcome'
import type { Scenario } from '../../prototype/registry'
import { createLibraryHandlers, createLibraryStore, libraryView, type FolderRowData } from '../../src/app/library/libraryStore'
import type { AppEvents, On } from '../../src/app/shell/events'
import { createScanStore } from '../../src/app/shell/scanStore'
import { createToasts } from '../../src/app/shell/toasts'
import type { LibraryProps } from '../../src/design/screens/library/props'
import type { ModelStatus } from '../../src/design/types'
import { ORDER_OPTIONS, SHOW_OPTIONS } from '../../src/design/components/LibraryFilterBar'
import { LIBRARY_FILTER } from '../../src/design/screens/library/filter'
import { CASES, ERROR_SCREEN_STATE, LIBRARY_STATES, LIBRARY_STEPS, WELCOME_STATES } from './cases'
import { LIBRARY, scenarioProps } from './fixtures/library'
import { WELCOME } from './fixtures/welcome'
import { bindingError, type ParityFixture } from './mock-bindings/control'

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

/** A toolbar menu item's label for `value`, as the menu shows it. */
function menuLabel(menu: 'show' | 'order', value: string): string {
  const hit = (menu === 'show' ? SHOW_OPTIONS : ORDER_OPTIONS).find((o) => o.value === value)
  if (!hit) throw new Error(`no ${menu} item for ${value}`)
  return hit.label
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

const GROUPS: { name: string; states: number[]; scenarios: Scenario[]; fixtures: Record<number, ParityFixture> }[] = [
  {
    name: 'welcome',
    states: WELCOME_STATES,
    scenarios: welcomeSceneList.filter((s) => s.n !== ERROR_SCREEN_STATE),
    fixtures: Object.fromEntries(Object.entries(WELCOME).filter(([n]) => Number(n) !== ERROR_SCREEN_STATE)),
  },
  { name: 'library', states: LIBRARY_STATES, scenarios: librarySceneList, fixtures: LIBRARY },
]

const ALL = GROUPS.flatMap((g) => g.states.map((n) => ({ n, scenarios: g.scenarios, fixture: g.fixtures[n]! })))

test('the cases cover exactly the registered welcome and Library states, each driven', () => {
  for (const g of GROUPS) {
    expect([g.name, g.states]).toEqual([g.name, g.scenarios.map((s) => s.n)])
    expect([g.name, Object.keys(g.fixtures).map(Number)]).toEqual([g.name, g.states])
    for (const n of g.states) {
      const c = CASES.find((x) => x.state === n)
      expect([n, c?.skip, typeof c?.drive]).toEqual([n, undefined, 'function'])
    }
  }
  for (const n of Object.keys(LIBRARY_STEPS).map(Number)) expect(ALL.some((s) => s.n === n)).toBe(true)
})

describe('each step stands for the scenario prop the fixture cannot carry, and only where it shows', () => {
  for (const { n, scenarios } of ALL) {
    test(`#${n}`, () => {
      const p = scenarioProps(scenarios, n)
      const s = LIBRARY_STEPS[n] ?? {}
      expect(p.online).toBe(!s.offline)
      expect(p.dropActive).toBe(s.dragOver === true)
      const force = s.hover !== undefined ? { id: s.hover, force: 'hover' } : s.focus !== undefined ? { id: s.focus, force: 'focus' } : undefined
      expect(p.forceRow).toEqual(force)
      expect(p.selectedId).toBe(s.select ?? s.focus ?? null)
      // The toolbar: what the case types and chooses is the scenario's filter, and only there.
      const f = p.filter ?? LIBRARY_FILTER
      expect(f.query).toBe(s.query ?? '')
      expect(menuLabel('show', f.show)).toBe(s.show ?? 'All folders')
      expect(menuLabel('order', f.order)).toBe(s.order ?? 'Capture date — newest first')
      expect(p.openMenu ?? null).toBe(s.menu ?? null)
      // No state of these groups shows a replica: the app opens the native menu and alert.
      expect([p.menuFor, p.removePrompt]).toEqual([null, null])
      const named = p.scan && !('count' in p.scan) ? p.scan : null
      if (s.stopRun !== undefined) {
        expect(named?.progress.state).toBe('stopped')
        expect(p.folders?.find((f) => f.name === named?.folder)).toMatchObject({ state: 'error', error: s.stopRun })
      } else if (named) {
        expect(named.progress.state).toBe('running')
      } else if (p.scan && 'count' in p.scan) {
        // Several jobs are counted: exactly that many rows are scanning.
        expect(p.folders?.filter((f) => f.state === 'scanning').length).toBe(p.scan.count)
      }
    })
  }
})

describe("the app's stores, fed the fixture, show the scenario's data", () => {
  for (const { n, scenarios, fixture } of ALL) {
    test(`#${n}`, async () => {
      const p = scenarioProps(scenarios, n)
      const bus = fakeBus()
      const list = () =>
        fixture.folders === 'pending' || fixture.folders === undefined
          ? new Promise<never>(() => {})
          : Promise.resolve(structuredClone(fixture.folders) as unknown as FolderRowData[])
      const toasts = createToasts()
      const scan = createScanStore({ list, on: bus.on })
      const library = createLibraryStore({ list, on: bus.on, toasts })
      const stops = [scan.start(), library.start()]
      await flush()

      const stop = LIBRARY_STEPS[n]?.stopRun
      if (stop !== undefined) {
        // What the case sends (cases.ts stopRun).
        const run = (fixture.folders as FolderRowData[]).find((f) => f.state === 'scanning')!
        bus.emit('scan:progress', { folderId: run.id, progress: { ...run.scan!, state: 'stopped' }, phase: 'failed' })
        bus.emit('scan:state', { folderId: run.id, state: 'error', error: stop })
        await flush()
      }

      const models = fixture.models as ModelStatus[]
      const view = libraryView({ list: library.get(), models: { status: 'ready', value: models, error: null }, scan: scan.get().value })
      expect(view.folders).toEqual(p.folders)
      // The title bar draws no scan any more (p.scan is deprecated, not drawn): the rows carry it.
      expect('scan' in view).toBe(false)
      expect(view.models).toEqual(p.models)
      expect(fixture.quality ?? null).toEqual(p.quality)
      expect(fixture.toasts ?? []).toEqual(p.toasts)
      for (const s of stops) s()
    })
  }
})

describe('the toasts the app puts up itself carry the approved copy of 113–115', () => {
  const copy = (t: { tone: string; title: string; body?: string }) => ({ tone: t.tone, title: t.title, body: t.body })

  test('113: a folder deleted in Finder (library:changed gone)', async () => {
    const bus = fakeBus()
    const toasts = createToasts()
    const store = createLibraryStore({ list: async () => [], on: bus.on, toasts })
    const stop = store.start()
    await flush()
    bus.emit('library:changed', { reason: 'gone', folderId: 'kyoto', name: 'Kyoto' })
    expect(toasts.items().map(copy)).toEqual([copy(TOAST_REMOVED)])
    stop()
  })

  const handlers = (toasts: ReturnType<typeof createToasts>, over: { add?: () => Promise<unknown>; picks?: () => Promise<unknown> }) => {
    const selected: (string | null)[] = []
    const h = createLibraryHandlers({
      library: { AddFolders: (over.add ?? (async () => [])) as never },
      system: { ShowFolderMenu: async () => {}, OpenPicksFolder: over.picks ?? (async () => {}) },
      models: { Retry: async () => {} },
      settings: { Save: async () => {} },
      toasts,
      select: (id) => selected.push(id),
      folderName: (id) => ({ 'card-a': 'Card A', lisbon: 'Lisbon' })[id],
      onSaved: () => {},
      onError: (e) => {
        throw e
      },
    })
    return { h, selected }
  }

  test('114: Add Folder picks a folder already in the Library', async () => {
    const toasts = createToasts()
    const { h, selected } = handlers(toasts, {
      add: async () => [{ id: 'lisbon', name: 'Lisbon', existing: true, relation: '' }],
    })
    await h.add()
    expect(selected).toEqual(['lisbon'])
    expect(toasts.items().map(copy)).toEqual([copy(TOAST_ALREADY)])
  })

  test('115: Open Picks on a read-only drive', async () => {
    const toasts = createToasts()
    const { h } = handlers(toasts, {
      picks: async () => {
        throw bindingError({ code: 'read_only', message: 'the drive is read-only' })
      },
    })
    await h.openPicks('card-a')
    expect(toasts.items().map(copy)).toEqual([copy(TOAST_READ_ONLY)])
  })
})
