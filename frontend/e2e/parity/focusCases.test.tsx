// The Focus (300–318) and Compare (400–409) cases against the approved
// scenarios, without a browser:
//   - the cases cover exactly the registered states: each driven, except
//     312, skipped with its reason; 313 declares the one image it holds
//     unanswered (its hero's full-size URL, as the app builds it) pending;
//   - FOCUS_STEPS (cases.ts, which runs on Node and cannot import the
//     scenarios) stands for exactly the props the fixture cannot carry;
//   - the app's own session, folder and Focus stores, fed each fixture and
//     run through the case's own plan (focusPlan: the keys through the
//     app's key dispatcher and Focus's action handlers, the rail clicks and
//     the drags as the screen reports them), give the Focus screen the
//     scenario's props;
//   - FOCUS_MEDIA and COMPARE_MEDIA give every member the photo the scenario
//     shows for it, and only 312's broken image has no served counterpart.
import { describe, expect, test } from 'bun:test'
import { scenarios as compareSceneList } from '../../prototype/scenarios/compare'
import { scenarios as focusSceneList } from '../../prototype/scenarios/focus'
import { BROKEN_FILE } from '../../prototype/mock/focus'
import { focusActionHandlers } from '../../src/app/focus/focusActions'
import { focusScreenProps, folderNameOf, qualityOnOf } from '../../src/app/focus/focusProps'
import { createFocusStore } from '../../src/app/focus/focusStore'
import { SHOW_INFO_KEY as APP_SHOW_INFO_KEY, showInfoPrefs } from '../../src/app/focus/prefs'
import { createFolderStore } from '../../src/app/folder/folderStore'
import type { FocusResponse as FocusResponseModel, FolderView, PickResult } from '../../src/app/shell/api'
import type { AppEvents, On } from '../../src/app/shell/events'
import { createFolderSession } from '../../src/app/shell/folderSession'
import { createKeyHandler } from '../../src/app/shell/keys'
import { ratingOf, stepZoom, toggleAction } from '../../src/design/screens/focus/logic'
import { MIN_ZOOM, PEEP_ZOOM, type FocusProps } from '../../src/design/screens/focus/props'
import type { FolderSummary, Member, SettingsBody } from '../../src/design/types'
import {
  BROKEN_IMAGE_SKIP,
  CASES,
  COMPARE_STATES,
  DRAG_STEP,
  dragSteps,
  FOCUS_STATES,
  FOCUS_STEPS,
  focusPlan,
  FOCUS_PENDING_FULL,
  photoKeys,
  shownFullUrl,
  SHOW_INFO_KEY,
  type FocusSteps,
} from './cases'
import { mediaPhoto } from './fixtures'
import { COMPARE, COMPARE_MEDIA, compareScenarioProps } from './fixtures/compare'
import { FOCUS, FOCUS_MEDIA, FOCUS_UNSERVED, focusScenarioProps, markBefore } from './fixtures/focus'
import { MEDIA } from './fixtures/table'
import { bindingError, type ParityFixture } from './mock-bindings/control'

const flush = async () => {
  for (let i = 0; i < 6; i++) await new Promise<void>((resolve) => setTimeout(resolve, 0))
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

const ALL = [...FOCUS_STATES, ...COMPARE_STATES]
const SKIPS: Record<number, string> = { 312: BROKEN_IMAGE_SKIP }
const propsOf = (n: number): FocusProps => (n >= 400 ? compareScenarioProps(n) : focusScenarioProps(n))
const fixtureOf = (n: number): ParityFixture => (n >= 400 ? COMPARE[n]! : FOCUS[n]!)

/** The KeyboardEvent a Playwright key name gives (US layout). */
function keyEvent(name: string): KeyboardEvent {
  const map: Record<string, { key: string; code: string }> = {
    ArrowRight: { key: 'ArrowRight', code: 'ArrowRight' },
    ArrowLeft: { key: 'ArrowLeft', code: 'ArrowLeft' },
    Equal: { key: '=', code: 'Equal' },
    Space: { key: ' ', code: 'Space' },
  }
  const init = map[name] ?? (/^[a-z]$/.test(name) ? { key: name, code: `Key${name.toUpperCase()}` } : /^[1-4]$/.test(name) ? { key: name, code: `Digit${name}` } : null)
  if (!init) throw new Error(`no key event for ${name}`)
  const e = {
    ...init,
    metaKey: false,
    ctrlKey: false,
    altKey: false,
    shiftKey: false,
    repeat: false,
    isComposing: false,
    defaultPrevented: false,
    target: null,
    getModifierState: () => false,
    preventDefault() {
      e.defaultPrevented = true
    },
  }
  return e as unknown as KeyboardEvent
}

/** The steps the scenario's props call for (what FOCUS_STEPS must say). */
function stepsOf(p: FocusProps): FocusSteps {
  const out: FocusSteps = {}
  if (p.showInfo) out.info = true
  const start = p.comparing ? (p.compareIdx[0] ?? p.activeIndex) : p.activeIndex
  if (start !== 0) out.photo = start
  if (p.comparing) out.compare = { panes: p.compareIdx, active: p.activeIndex }
  if (p.zoom !== MIN_ZOOM) {
    if (p.zoom === PEEP_ZOOM && !p.comparing) out.peep = true
    else {
      let v = { zoom: MIN_ZOOM, pan: { x: 0, y: 0 } }
      let k = 0
      while (v.zoom < p.zoom && k < 20) {
        v = stepZoom(v, 1)
        k++
      }
      out.zoomIn = k
    }
  }
  if (p.pan.x !== 0 || p.pan.y !== 0) out.pan = p.pan
  if (p.busyFileIds.length > 0) {
    const m = p.data!.cluster.members.find((x) => x.file_id === p.busyFileIds[0])!
    out.mark = markBefore(m).key
  }
  return out
}

test('the cases cover exactly the registered Focus and Compare states: each driven, 312 skipped, 313 with its full-size image pending', () => {
  expect(FOCUS_STATES).toEqual(focusSceneList.map((s) => s.n))
  expect(COMPARE_STATES).toEqual(compareSceneList.map((s) => s.n))
  expect(Object.keys(FOCUS).map(Number)).toEqual(FOCUS_STATES)
  expect(Object.keys(COMPARE).map(Number)).toEqual(COMPARE_STATES)
  for (const n of ALL) {
    const c = CASES.find((x) => x.state === n)
    if (SKIPS[n] !== undefined) expect([n, c?.skip, c?.drive]).toEqual([n, SKIPS[n], undefined])
    else expect([n, c?.skip, typeof c?.drive]).toEqual([n, undefined, 'function'])
    // Only a state whose scenario shows the full-size image still loading declares it pending.
    const loading = n < 400 && Object.values(focusScenarioProps(n).mediaState ?? {}).includes('loading')
    expect([n, c?.pendingImages]).toEqual([n, loading ? [shownFullUrl(n)] : undefined])
    if (loading) {
      // The URL is the full size of the member the scenario shows loading.
      const p = focusScenarioProps(n)
      const ids = Object.entries(p.mediaState).filter(([, v]) => v === 'loading').map(([k]) => Number(k))
      const m = p.data!.cluster.members.find((x) => x.file_id === ids[0])!
      expect([ids.length, m.file_id]).toEqual([1, p.data!.cluster.members[p.activeIndex]!.file_id])
      expect(decodeURIComponent(shownFullUrl(n))).toBe(`/media/full/dolomites/${m.path}?v=${m.content_hash}`)
    }
  }
  expect(FOCUS_PENDING_FULL).toEqual([313])
  for (const n of Object.keys(FOCUS_STEPS).map(Number)) expect(ALL).toContain(n)
  expect(SHOW_INFO_KEY).toBe(APP_SHOW_INFO_KEY)
})

test('photoKeys takes the shorter way round, and dragSteps add up to the pan in short whole-pixel drags', () => {
  expect(photoKeys(0, 2, 8)).toEqual(['ArrowRight', 'ArrowRight'])
  expect(photoKeys(0, 7, 8)).toEqual(['ArrowLeft'])
  expect(photoKeys(0, 31, 40)).toEqual(Array(9).fill('ArrowLeft'))
  for (const pan of [
    { x: -960, y: 440 },
    { x: -140, y: 60 },
    { x: 7, y: -301 },
  ]) {
    const ds = dragSteps(pan)
    expect(ds.reduce((a, d) => ({ x: a.x + d.x, y: a.y + d.y }), { x: 0, y: 0 })).toEqual(pan)
    for (const d of ds) {
      expect(Number.isInteger(d.x) && Number.isInteger(d.y)).toBe(true)
      expect(Math.abs(d.x) <= DRAG_STEP.x && Math.abs(d.y) <= DRAG_STEP.y).toBe(true)
    }
  }
})

describe('each step stands for the scenario prop the fixture cannot carry, and only where it shows', () => {
  for (const n of ALL) {
    test(`#${n}`, () => {
      expect(FOCUS_STEPS[n] ?? {}).toEqual(stepsOf(propsOf(n)))
    })
  }
})

describe("the app's stores, fed the fixture and run through the case's plan, give the screen the scenario's props", () => {
  for (const n of ALL) {
    test(`#${n}`, async () => {
      const p = propsOf(n)
      const f = fixtureOf(n)
      const s = FOCUS_STEPS[n] ?? {}
      const m = /^#\/folder\/([^/]+)\/focus\/(\d+)$/.exec(f.route)
      expect(m).not.toBeNull()
      const folderId = decodeURIComponent(m![1]!)
      const clusterId = Number(m![2])
      if (p.data) expect(clusterId).toBe(p.data.cluster.id)
      const bus = fakeBus()
      const view = () => (typeof f.view === 'object' ? Promise.resolve(structuredClone(f.view) as FolderView) : never<FolderView>())
      const callOf = (name: string) => f.calls?.[name]
      const cluster = (): Promise<FocusResponseModel> => {
        const how = callOf('Catalog.Cluster')
        if (how === 'pending') return never()
        if (how !== undefined) return Promise.reject(bindingError(how))
        return typeof f.focus === 'object' ? Promise.resolve(structuredClone(f.focus) as unknown as FocusResponseModel) : never()
      }
      const STATE: Record<string, string> = { pick: 'picked', reject: 'rejected', unpick: 'unmarked', unreject: 'unmarked' }
      const setPicks: string[] = []
      const setPick = (_f: string, fileId: number, action: string): Promise<PickResult> => {
        setPicks.push(`${action} ${fileId}`)
        return callOf('Catalog.SetPick') === 'pending' ? never() : Promise.resolve({ file_id: fileId, state: STATE[action]! } as PickResult)
      }
      const storage = new Map<string, string>([[SHOW_INFO_KEY, s.info ? '1' : '0']])

      const session = createFolderSession({ open: view, close: async () => {}, on: bus.on, toLibrary: () => {}, settingsToLibrary: () => {} })
      const folder = createFolderStore({
        folderId,
        session,
        on: bus.on,
        clusters: view,
        ratings: async () => ({}),
        setPick,
        undo: never,
        recluster: async () => {},
        hideRejected: () => false,
      })
      const store = createFocusStore({
        folderId,
        clusterId,
        session,
        cluster: () => cluster(),
        folder,
        on: bus.on,
        advanceOnAction: () => (f.settings as SettingsBody).advance_on_action,
        prefs: showInfoPrefs(() => ({ getItem: (k) => storage.get(k) ?? null, setItem: (k, v) => void storage.set(k, v) })),
      })
      const handlers = focusActionHandlers(store, {
        mark: (k) => store.mark(k).then(() => {}),
        undo: async () => {},
        toCluster: () => {},
        toFolder: () => {},
      })
      const press = createKeyHandler({
        run: (id, data) => void handlers[id]?.(data),
        screen: () => (store.get().comparing ? 'compare' : 'focus'),
        singleKeysNative: () => false,
      })
      const stops = [session.start(), folder.start(), store.start()]
      session.route({ name: 'focus', folderId, clusterId })
      await flush()

      // The case's plan, as the page runs it.
      for (const a of focusPlan(s, p.data?.cluster.members.length ?? 0)) {
        if ('key' in a) press(keyEvent(a.key))
        else if ('rail' in a) {
          // FocusRail's onItem: a pane added or removed while comparing, else the photo.
          if (store.get().comparing) store.compareSelect(a.rail)
          else store.select(a.rail)
        } else {
          // FocusPanSurface's drag: the pan moves by the pointer's offset at the same zoom.
          const v = store.get().view
          store.setView({ zoom: v.zoom, pan: { x: v.pan.x + a.drag.x, y: v.pan.y + a.drag.y } })
        }
        await flush()
      }
      // The full-size images on screen report as the scenario's show them (the img's load or error event).
      const snap0 = store.get()
      const members = snap0.data?.cluster.members ?? []
      const shown = snap0.comparing ? snap0.compareIdx.map((i) => members[i]!) : members[snap0.activeIndex] ? [members[snap0.activeIndex]!] : []
      const stateOf = (props: Pick<FocusProps, 'mediaState'>, x: Member) => props.mediaState[x.file_id] ?? 'ready'
      for (const x of shown) {
        if (x.kind === 'video') continue // no load callback (the design's player)
        const want = stateOf(p, x)
        if (want !== 'loading') store.setMediaState(x.file_id, want)
      }
      await flush()

      const snap = store.get()
      const rows = (f.folders ?? []) as FolderSummary[]
      const got = focusScreenProps({
        snap,
        folderName: folderNameOf(folderId, folder.get().value?.name, rows, session.get().view),
        qualityOn: qualityOnOf(f.settings as SettingsBody),
        media: p.media,
        toasts: { toasts: f.toasts ?? [], onUndoToast: () => {}, onDismissToast: () => {} },
        handlers: {} as never,
      })
      for (const key of ['folder', 'data', 'loadError', 'activeIndex', 'zoom', 'pan', 'showInfo', 'comparing', 'compareIdx', 'busyFileIds', 'qualityOn', 'toasts'] as const) {
        expect([key, got[key]]).toEqual([key, p[key]])
      }
      for (const x of shown) expect([x.file_id, stateOf(got, x)]).toEqual([x.file_id, stateOf(p, x)])
      for (const x of members) expect([x.file_id, ratingOf(x, got.ratings, got.qualityOn)]).toEqual([x.file_id, ratingOf(x, p.ratings, p.qualityOn)])
      // A pick in flight went to Go once, as the key's action on the busy file.
      expect(setPicks).toEqual(
        p.busyFileIds.map((id) => {
          const b = markBefore(p.data!.cluster.members.find((x) => x.file_id === id)!)
          return `${toggleAction(b.mark, b.key === 'pick' ? 'picked' : 'rejected')} ${id}`
        }),
      )
      for (const stop of stops) stop()
    })
  }
})

test('FOCUS_MEDIA and COMPARE_MEDIA give each member the photo the scenario shows for it; only 312’s broken image has no served counterpart', () => {
  for (const [n, p] of [...FOCUS_STATES.map((k) => [k, focusScenarioProps(k)] as const), ...COMPARE_STATES.map((k) => [k, compareScenarioProps(k)] as const)]) {
    for (const x of p.data?.cluster.members ?? []) {
      const table = n >= 400 ? COMPARE_MEDIA : FOCUS_MEDIA
      expect([x.content_hash, table[x.content_hash]]).toEqual([x.content_hash, mediaPhoto(p.media.thumb(x))])
      expect(MEDIA[x.content_hash]).toBe(table[x.content_hash]!)
    }
  }
  expect(FOCUS_UNSERVED).toEqual([{ state: 312, fileId: BROKEN_FILE, tier: 'full' }])
  const r = Bun.spawnSync(['bun', `${import.meta.dir}/fixtures/media.ts`])
  expect(r.exitCode).toBe(0)
  expect(JSON.parse(r.stdout.toString())).toEqual(MEDIA)
})
