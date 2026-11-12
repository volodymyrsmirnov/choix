// The Settings cases (500–520) against the approved scenarios, without a
// browser:
//   - the cases cover exactly the registered Settings states, each driven;
//   - SETTINGS_STEPS (cases.ts, which runs on Node and cannot import the
//     scenarios) stands for exactly the props the fixture cannot carry, and
//     the fixture's `calls`, `events` and `ui` for exactly what the state
//     shows (a save or retry in flight, a refused load, Go's later values,
//     the opening scroll to the quality field);
//   - the app's own controller and stores (createSettingsDraft, the settings,
//     models and quality stores, the real router), fed each fixture, its
//     events and the steps the way the screen reports them, give the Settings
//     screen the scenario's props;
//   - the toast the app puts up itself when Save fails carries the approved
//     copy of the toast 512 seeds.
import { describe, expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { TOAST_SAVE_FAILED } from '../../prototype/mock/settings'
import { scenarios as settingsSceneList } from '../../prototype/scenarios/settings'
import type { AppEvents, On } from '../../src/app/shell/events'
import { createModelsStore, type ModelStatusData } from '../../src/app/shell/modelsStore'
import { createQualityStore, type QualityData } from '../../src/app/shell/qualityStore'
import { createRouter, type HashHost } from '../../src/app/shell/router'
import { createSettingsStore } from '../../src/app/shell/settingsStore'
import { createToasts } from '../../src/app/shell/toasts'
import { createSettingsDraft, REVEAL_SECTION } from '../../src/app/settings/settingsDraft'
import { draftOf } from '../../src/design/screens/settings/draft'
import type { SettingsProps } from '../../src/design/screens/settings/props'
import { SettingsScreen } from '../../src/design/screens/settings/SettingsScreen'
import type { SettingsBody } from '../../src/design/types'
import { CASES, SETTINGS_STATES, SETTINGS_STEPS, type SettingsSteps, type SettingsSwitchId } from './cases'
import { SETTINGS, SETTINGS_EVENTS, settingsScenarioProps } from './fixtures/settings'
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
  const emit = (name: string, data: unknown) => {
    for (const h of [...(handlers.get(name) ?? [])]) h(data)
  }
  return { on, emit }
}

/** A location hash without a browser (as router.test.ts). */
function fakeHost(): HashHost {
  const listeners = new Set<() => void>()
  let hash = ''
  return {
    getHash: () => hash,
    setHash(h: string) {
      if (h === hash) return
      hash = h
      for (const l of [...listeners]) l()
    },
    onHashChange(fn: () => void) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
  }
}

const never = <T,>() => new Promise<T>(() => {})

/** Each switch of the form (its test id) and the setting it holds. */
const SWITCH_KEY: Record<SettingsSwitchId, keyof SettingsBody> = {
  'visual-toggle': 'visual_grouping',
  'crossdev-toggle': 'cross_device_merging',
  'advance-toggle': 'advance_on_action',
  'hide-rejected-toggle': 'hide_rejected_photos',
  'quality-toggle': 'quality_rating',
}

/** The steps the scenario's props call for (what SETTINGS_STEPS must say). */
function stepsOf(p: SettingsProps): SettingsSteps {
  const out: SettingsSteps = {}
  if (p.saved) {
    const base = draftOf(p.saved)
    if (p.draft.bucket_size !== base.bucket_size) out.bucket = p.draft.bucket_size
    if (p.draft.picks_dir !== base.picks_dir) out.picks = p.draft.picks_dir
    if (p.draft.similarity !== base.similarity) throw new Error('no step types a similarity')
    const toggle = (Object.keys(SWITCH_KEY) as SettingsSwitchId[]).filter((id) => p.draft[SWITCH_KEY[id]] !== base[SWITCH_KEY[id]])
    if (toggle.length > 0) out.toggle = toggle
  }
  // A revealed field's section comes from the opening scroll, not a click.
  if (p.activeSection !== 'grouping' && p.reveal === undefined) out.section = p.activeSection
  if (p.saving) out.save = true
  if (p.leavePrompt) out.back = true
  if (p.retrying) out.retry = true
  return out
}

test('the cases cover exactly the registered Settings states, each driven', () => {
  expect(SETTINGS_STATES).toEqual(settingsSceneList.map((s) => s.n))
  expect(Object.keys(SETTINGS).map(Number)).toEqual(SETTINGS_STATES)
  for (const n of SETTINGS_STATES) {
    const c = CASES.find((x) => x.state === n)
    expect([n, c?.skip, typeof c?.drive, c?.pendingImages]).toEqual([n, undefined, 'function', undefined])
  }
  for (const n of Object.keys(SETTINGS_STEPS).map(Number)) expect(SETTINGS_STATES).toContain(n)
})

test('every switch a step flips is the form’s switch for its setting', () => {
  const p = settingsScenarioProps(500)
  const html = renderToStaticMarkup(<SettingsScreen {...p} />)
  for (const [id, key] of Object.entries(SWITCH_KEY)) {
    const m = new RegExp(`<button[^>]*role="switch"[^>]*aria-checked="(true|false)"[^>]*data-testid="${id}"`).exec(html)
    expect([id, m?.[1]]).toEqual([id, String(p.draft[key])])
  }
})

describe('each step, call, event and ui value stands for what the scenario shows, and only where it shows', () => {
  for (const n of SETTINGS_STATES) {
    test(`#${n}`, () => {
      const p = settingsScenarioProps(n)
      const f: ParityFixture = SETTINGS[n]!
      const s = SETTINGS_STEPS[n] ?? {}
      expect(s).toEqual(stepsOf(p))
      expect(f.route).toBe('#/settings')
      expect(f.settings).toEqual(p.saved ?? 'pending')
      expect(f.calls ?? {}).toEqual({
        ...(p.saved === null && p.loadError !== null ? { 'Settings.Get': { code: 'internal', message: p.loadError } } : {}),
        ...(p.saving ? { 'Settings.Save': 'pending' } : {}),
        ...(p.retrying ? { 'Models.Retry': 'pending' } : {}),
      })
      expect(f.ui).toEqual(p.reveal === undefined ? undefined : { 'settings.reveal': p.reveal })
      // The revealed field's section is the one the state highlights.
      if (p.reveal !== undefined) expect(REVEAL_SECTION[p.reveal]).toBe(p.activeSection)
      expect(f.toasts).toEqual(p.toasts.length > 0 ? p.toasts : undefined)
      const ev = SETTINGS_EVENTS[n]
      if (ev) {
        // What Go reported before differs from what the state shows; the events bring it.
        if (ev.before.models) expect(ev.before.models).not.toEqual(p.models)
        if (ev.before.quality) expect(ev.before.quality).not.toEqual(p.quality)
        expect(f.events).toEqual(JSON.parse(JSON.stringify(ev.events(p))))
      } else {
        expect([f.models, f.quality, f.events]).toEqual([p.models, p.quality ?? undefined, undefined])
      }
    })
  }
})

/** The app's controller and stores for fixture `f`, as the shell and SettingsContainer wire them. */
function appFor(f: ParityFixture) {
  const bus = fakeBus()
  const calls = f.calls ?? {}
  const answer = <T,>(binding: string, value: T | 'pending' | 'error' | undefined): Promise<T> => {
    const how = calls[binding]
    if (how === 'pending') return never<T>()
    if (how !== undefined) return Promise.reject(bindingError(how))
    if (value === undefined || value === 'pending' || value === 'error') return never<T>()
    return Promise.resolve(structuredClone(value))
  }
  const settings = createSettingsStore({ get: () => answer('Settings.Get', f.settings) })
  const models = createModelsStore({ status: () => answer('Models.Status', f.models as ModelStatusData[] | undefined), on: bus.on })
  const quality = createQualityStore({ quality: () => answer('Models.Quality', f.quality as QualityData | undefined), on: bus.on })
  const router = createRouter(fakeHost())
  router.start()
  router.navigate({ name: 'settings' })
  const toasts = createToasts()
  const saves: unknown[] = []
  const ctl = createSettingsDraft({
    save: (patch) => {
      saves.push(patch)
      return answer('Settings.Save', { saved: true, reclustered: false })
    },
    retry: () => answer<void>('Models.Retry', undefined as unknown as void),
    settings,
    models,
    quality,
    router,
    toast: (t) => {
      toasts.push(t)
    },
    now: () => 0,
  })
  const reveal = f.ui?.['settings.reveal'] as SettingsProps['reveal']
  if (reveal !== undefined) ctl.reveal(reveal)
  const stops = [settings.start(), models.start(), quality.start(), ctl.start()]
  return { ctl, bus, models, quality, router, toasts, saves, stop: () => stops.forEach((s) => s()) }
}

describe("the app's controller and stores, fed the fixture, give the screen the scenario's props", () => {
  for (const n of SETTINGS_STATES) {
    test(`#${n}`, async () => {
      const p = settingsScenarioProps(n)
      const f: ParityFixture = SETTINGS[n]!
      const s = SETTINGS_STEPS[n] ?? {}
      const app = appFor(f)
      // Models.Retry answers at once unless the fixture keeps it in flight.
      await flush()
      for (const ev of f.events ?? []) app.bus.emit(ev.name as keyof AppEvents, ev.data)
      // The screen's opening scroll, when a field is revealed: the pane at its bottom reports the last section.
      if (f.ui?.['settings.reveal'] !== undefined) app.ctl.section('output', 'scroll')
      if (s.bucket !== undefined) app.ctl.change({ bucket_size: s.bucket })
      if (s.picks !== undefined) app.ctl.change({ picks_dir: s.picks })
      for (const id of s.toggle ?? []) {
        const key = SWITCH_KEY[id]
        app.ctl.change({ [key]: !app.ctl.state().draft[key] })
      }
      if (s.section !== undefined) app.ctl.section(s.section, 'nav')
      if (s.save) void app.ctl.save()
      if (s.back) app.ctl.back()
      if (s.retry) void app.ctl.retry('quality')
      await flush()

      const st = app.ctl.state()
      expect({
        saved: st.saved,
        loadError: st.loadError,
        draft: st.draft,
        errors: st.errors,
        activeSection: st.activeSection,
        saving: st.saving,
        retrying: st.retrying,
        leavePrompt: st.leavePrompt,
        models: app.models.get().value ?? [],
        quality: app.quality.get().value ?? null,
      }).toEqual({
        saved: p.saved,
        loadError: p.loadError,
        draft: p.draft,
        errors: p.errors,
        activeSection: p.activeSection,
        saving: p.saving,
        retrying: p.retrying,
        leavePrompt: p.leavePrompt,
        models: p.models,
        quality: p.quality,
      })
      // The steps leave no toast of the app's own (512's is the fixture's).
      expect(app.toasts.items()).toEqual([])
      // Back from the leave prompt stays on Settings until the user answers.
      expect(app.router.current().name).toBe('settings')
      app.stop()
    })
  }
})

test("a save Go refuses puts up the app's own toast with 512's approved copy", async () => {
  const p = settingsScenarioProps(512)
  const f: ParityFixture = {
    ...SETTINGS[512]!,
    toasts: undefined,
    calls: { 'Settings.Save': { code: 'internal', message: TOAST_SAVE_FAILED.body! } },
  }
  const app = appFor(f)
  await flush()
  const s = SETTINGS_STEPS[512]!
  app.ctl.change({ bucket_size: s.bucket! })
  for (const id of s.toggle ?? []) app.ctl.change({ [SWITCH_KEY[id]]: !app.ctl.state().draft[SWITCH_KEY[id]] })
  await app.ctl.save()
  await flush()
  expect(app.saves.length).toBe(1)
  const shown = app.toasts.items().map((t) => ({ tone: t.tone, title: t.title, body: t.body }))
  expect(shown).toEqual([{ tone: TOAST_SAVE_FAILED.tone, title: TOAST_SAVE_FAILED.title, body: TOAST_SAVE_FAILED.body }])
  // The edits stay, as the state shows them.
  expect(app.ctl.state().draft).toEqual(p.draft)
  app.stop()
})
