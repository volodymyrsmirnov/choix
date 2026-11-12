// Fixtures of the Settings states 500–520 (prototype/scenarios/settings.tsx).
//
// Each fixture is read off the approved scenario itself: the scenario's
// render gives a SettingsScreen element, and its props are what the
// prototype shows. The fixture hands the app the same data the way Go would:
//   - Settings.Get: the saved values; never answering while the state is
//     still loading (510); refused with the state's message (511, `calls`);
//   - Models.Status and Models.Quality: both downloads and the quality
//     worker, as the state shows them, except where the state's values reach
//     the page as Go's events (SETTINGS_EVENTS): there the bindings answer
//     what Go reported before, and `events` holds the events, which the case
//     emits once the page shows those earlier values;
//   - Library.List: no folder (the Settings page shows none);
//   - `calls`: Settings.Save kept in flight where the state shows a save
//     (504), Models.Retry kept in flight where it shows a retry (515);
//   - the "Save failed" toast of 512, seeded (the app's own toast leaves
//     after 4 s, which a comparison could outlast; ../settingsCases.test.tsx
//     holds the app's own failed save to the same copy);
//   - `ui['settings.reveal']`: the opening scroll to the quality field of the
//     quality states (505, 506, 509, 513–515), which no binding, event, route
//     or input can give the app (SettingsContainer.tsx reads it through
//     uiOverride).
// What the user does — typing into a field, flipping a switch, choosing a
// section in the sidebar, pressing Save, Back or Retry — is done by the
// state's case (cases.ts, SETTINGS_STEPS).
import { model, quality } from '../../../prototype/mock/data'
import type { Scenario, ScenarioCtx } from '../../../prototype/registry'
import { scenarios } from '../../../prototype/scenarios/settings'
import type { SettingsProps } from '../../../src/design/screens/settings/props'
import type { ModelStatus, QualityStatus } from '../../../src/design/types'
import type { ParityEvent, ParityFixture } from '../mock-bindings/control'

/** A scenario context with an empty store: every state shows its defaults. */
const CTX: ScenarioCtx = {
  size: 'default',
  now: new Date(0),
  store: { state: {}, set: () => {}, go: () => {}, toast: () => {} },
}

/** The SettingsScreen props scenario `n` renders. */
export function settingsScenarioProps(n: number, list: Scenario[] = scenarios): SettingsProps {
  const s = list.find((x) => x.n === n)
  if (!s) throw new Error(`parity fixtures: no settings scenario #${n}`)
  return s.render(CTX).props as SettingsProps
}

/**
 * The states whose models or quality worker reach the page as Go's events,
 * and what Go reported before them (the bindings' answers):
 *   - 509: the worker was waiting for a scan; `quality:status` says it is
 *     rating photos;
 *   - 516: both downloads were queued; `models:progress` says CLIP started
 *     (a state change, with its first bytes).
 */
export const SETTINGS_EVENTS: Record<number, { before: { models?: ModelStatus[]; quality?: QualityStatus }; events: (p: SettingsProps) => ParityEvent[] }> = {
  509: {
    before: { quality: quality({ state: 'waiting' }) },
    events: (p) => [{ name: 'quality:status', data: p.quality }],
  },
  516: {
    before: { models: [model('clip', 'queued'), model('quality', 'queued')] },
    events: (p) => [{ name: 'models:progress', data: p.models.find((m) => m.kind === 'clip') }],
  },
}

/** `value` as JSON data (keys whose value is undefined dropped), as Go's answer would be. */
const plain = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T

/** The Settings route with `p`'s data, as Go answers it (`n`: the state, for its events). */
export function settingsFixture(p: SettingsProps, n?: number): ParityFixture {
  const ev = n === undefined ? undefined : SETTINGS_EVENTS[n]
  const calls: NonNullable<ParityFixture['calls']> = {}
  if (p.saved === null && p.loadError !== null) calls['Settings.Get'] = { code: 'internal', message: p.loadError }
  if (p.saving) calls['Settings.Save'] = 'pending'
  if (p.retrying) calls['Models.Retry'] = 'pending'
  return plain({
    route: '#/settings',
    folders: [],
    settings: p.saved ?? 'pending',
    models: ev?.before.models ?? p.models,
    ...(p.quality ? { quality: ev?.before.quality ?? p.quality } : {}),
    ...(p.toasts.length > 0 ? { toasts: p.toasts } : {}),
    ...(p.reveal !== undefined ? { ui: { 'settings.reveal': p.reveal } } : {}),
    ...(Object.keys(calls).length > 0 ? { calls } : {}),
    ...(ev ? { events: ev.events(p) } : {}),
  })
}

/** The Settings states' numbers (500–520). */
export const SETTINGS_STATES: number[] = scenarios.map((s) => s.n)

export const SETTINGS: Record<number, ParityFixture> = Object.fromEntries(SETTINGS_STATES.map((n) => [n, settingsFixture(settingsScenarioProps(n), n)]))
