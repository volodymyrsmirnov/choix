// Settings states 500–520 (v1 Settings.tsx), wired to the prototype store so
// the form is editable: fields change the draft, Save stores it (with a
// toast), Revert and Restore defaults refill it, Back with unsaved edits
// opens the leave prompt. 505, 506 and 514–517, the model download and
// quality error states, are retired: nothing is downloaded any more.
//
// Store keys: `draft` (the edited form), `saved`, `activeSection`, the
// overlay id `settings-leave`, and `toast:<id>`.
import { DEFAULTS, dirtySections, draftOf, reclusterFields } from '../../src/design/screens/settings/draft'
import { SETTINGS_OVERLAYS, type SettingsDraft, type SettingsProps, type SettingsSection } from '../../src/design/screens/settings/props'
import { SettingsScreen } from '../../src/design/screens/settings/SettingsScreen'
import { minutesTextToSeconds, validateSettings } from '../../src/design/screens/settings/validate'
import type { QualityStatus, SettingsBody, ToastItem } from '../../src/design/types'
import { quality } from '../mock/data'
import { ERR_LOAD, SAVED, SAVED_NO_VISUAL, TOAST_SAVE_FAILED } from '../mock/settings'
import type { Scenario, ScenarioCtx } from '../registry'
import { read, toastsOf } from '../store'

const V1 = 'v1:components/Settings.tsx'
const NEW = 'missing design'

// Where a click leads in the prototype.
const GO = { back: 102, shortcuts: 600, loaded: 500 }

interface SettingsState {
  saved?: SettingsBody | null
  loadError?: string | null
  draft?: Partial<SettingsDraft>
  activeSection?: SettingsSection
  saving?: boolean
  quality?: QualityStatus | null
  leavePrompt?: boolean
  toasts?: ToastItem[]
  reveal?: SettingsProps['reveal']
}

function settingsProps(ctx: ScenarioCtx, s: SettingsState): SettingsProps {
  const { state, set, go, toast } = ctx.store
  const seeded = s.saved === undefined ? SAVED : s.saved
  const saved = read<SettingsBody | null>(state, 'saved', seeded)
  const draft = read<SettingsDraft>(state, 'draft', { ...(seeded ? draftOf(seeded) : draftOf(DEFAULTS)), ...s.draft })
  const errors = validateSettings(draft)
  const dirty = Object.values(dirtySections(saved, draft)).some(Boolean)

  return {
    saved,
    loadError: s.loadError ?? null,
    draft,
    errors,
    activeSection: read(state, 'activeSection', s.activeSection ?? 'grouping'),
    reveal: s.reveal,
    backTo: 'Library',
    saving: s.saving ?? false,
    quality: s.quality === undefined ? quality({ active: false }) : s.quality,
    leavePrompt: !!read(state, SETTINGS_OVERLAYS.leave, s.leavePrompt ?? false),
    toasts: toastsOf(state, s.toasts),
    onChange: (patch) => set({ draft: { ...draft, ...patch } }),
    // Scroll-driven changes are the app's (it debounces them against a nav
    // click's own scroll); the prototype keeps the state's section.
    onSection: (id, via) => {
      if (via === 'nav') set({ activeSection: id })
    },
    onSave: () => {
      const sec = minutesTextToSeconds(draft.bucket_size)
      if (!saved || sec === null || Object.keys(errors).length > 0) return
      const next: SettingsBody = { ...draft, bucket_size: sec }
      const reclustered = reclusterFields(saved, draft).length > 0
      set({ saved: next, draft: draftOf(next) })
      toast({ id: 'saved', tone: 'pick', title: 'Settings saved', body: reclustered ? 'Re-clustered with new parameters' : undefined })
    },
    onRevert: () => saved && set({ draft: draftOf(saved) }),
    onBack: () => (dirty ? set({ [SETTINGS_OVERLAYS.leave]: true }) : go(GO.back)),
    onHelp: () => go(GO.shortcuts),
    onReload: () => go(GO.loaded),
    onConfirmLeave: (leave) => (leave ? go(GO.back) : set({ [SETTINGS_OVERLAYS.leave]: false })),
    onUndoToast: (id) => set({ [`toast:${id}`]: false }),
    onDismissToast: (id) => set({ [`toast:${id}`]: false }),
  }
}

const st = (n: number, label: string, s: SettingsState, design = V1, overlays?: string[]): Scenario => ({
  n,
  group: 'settings',
  label,
  design,
  overlays,
  subject: SUBJECT[n],
  render: (ctx) => <SettingsScreen {...settingsProps(ctx, s)} />,
})

/** Unsaved edits in two sections: the bucket (re-clusters) and a behaviour switch. */
const EDITED: Partial<SettingsDraft> = { bucket_size: '5', advance_on_action: false }

const BEHAVIOUR = { activeSection: 'behaviour' as const }
/** The quality states open on Behaviour, scrolled to the quality field. */
const QUALITY = { ...BEHAVIOUR, reveal: 'quality' as const }
const QUALITY_STATUS = '[data-testid="quality-status"]'

/** What each state's label names, which must be in view at every size. */
const SUBJECT: Record<number, string> = {
  500: 'nav [aria-current="true"], #settings-grouping-title',
  501: '[data-testid="section-dirty"]',
  502: '#settings-bucket-error',
  503: '#settings-picks-dir-error',
  504: '[data-testid="save"]',
  507: '#visual-toggle',
  508: '[role="alertdialog"]',
  509: QUALITY_STATUS,
  511: '[data-empty-state]',
  512: '[data-overlay="toast:save-failed"]',
  513: QUALITY_STATUS,
  510: '[aria-label="Loading settings"]',
  519: 'nav [aria-current="true"], #settings-behaviour-title',
  520: 'nav [aria-current="true"], #settings-output-title',
}

export const scenarios: Scenario[] = [
  st(500, 'Saved values, Grouping section active', {}),
  st(501, 'Unsaved changes (section marked)', { draft: EDITED }),
  st(502, 'Bucket validation error', { draft: { bucket_size: '0.5' } }),
  st(503, 'Picks-folder validation error', { draft: { picks_dir: '/Volumes/Atlas/Selects' }, activeSection: 'output' }),
  st(504, 'Saving', { draft: EDITED, saving: true }),
  st(507, 'Visual grouping off', { saved: SAVED_NO_VISUAL }, NEW),
  st(508, 'Leave-without-saving prompt', { draft: EDITED, leavePrompt: true }, V1, [SETTINGS_OVERLAYS.leave]),
  st(509, 'Quality scoring progress', { ...QUALITY, quality: quality({ state: 'scoring', done: 1234, total: 2849 }) }),
  st(510, 'Loading', { saved: null }, NEW),
  st(511, 'Failed to load', { saved: null, loadError: ERR_LOAD }, NEW),
  st(512, 'Save failed toast', { draft: EDITED, toasts: [TOAST_SAVE_FAILED] }, V1, [`toast:${TOAST_SAVE_FAILED.id}`]),
  st(513, 'Quality waiting for scan', { ...QUALITY, quality: quality({ state: 'waiting' }) }),
  st(518, 'At defaults', { saved: DEFAULTS }),
  st(519, 'Behaviour section active', BEHAVIOUR),
  st(520, 'Output section active', { activeSection: 'output' }),
]
