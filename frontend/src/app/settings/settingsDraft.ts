// What the Settings page does with the saved values and the user's edits,
// apart from React: the draft, its errors, Save, Revert, leaving with
// unsaved edits and the sidebar's section. Everything it talks to is passed
// in (the binding, the shell's settings store, the router, the toasts), so
// it runs under `bun test` with fakes.
//
// The rules (v1 Settings.tsx, design screens/settings):
//   - The draft is the design's draftOf(saved): numeric fields that are
//     typed keep the raw text. It follows the saved values while it holds no
//     edits, and keeps the edits otherwise.
//   - Errors are the design's validateSettings(draft), plus what Go refused
//     on the last Save (fieldCopy(field, reason), else Go's message), until
//     that field is edited again. A refusal for a field the user changed
//     while the Save was in flight is dropped: it was for the old value.
//   - Save sends only the fields that differ from the saved values (the
//     bucket compared in whole seconds), never while invalid or unchanged.
//     What it saved goes into the shell's settings store (Go sends no event
//     for settings), and the store loads again: its reload() supersedes any
//     load started before the Save, so an older answer never lands over the
//     saved values.
//   - The sidebar's section follows the scroll (v1), except right after the
//     page scrolled itself: a sidebar click, or opening on a revealed field
//     (the design's `reveal`), whose section is active even when that scroll
//     leaves the pane at its bottom, which reports the last section.
//   - Leaving with unsaved edits is held by the router's guard, which opens
//     the design's prompt; Discard leaves for the navigation the router
//     holds (router.held(): where it was going, or the Library once the
//     folder it led into has gone away).
import { dirtySections, draftOf, DEFAULTS } from '../../design/screens/settings/draft'
import type { SettingsDraft, SettingsErrors, SettingsProps, SettingsSection } from '../../design/screens/settings/props'
import { minutesTextToSeconds, validateSettings } from '../../design/screens/settings/validate'
import type { SettingsBody } from '../../design/types'
import { appError, errorText, fieldCopy } from '../shell/errors'
import type { SessionState } from '../shell/folderSession'
import type { Route, Router } from '../shell/router'
import type { Store } from '../shell/store'
import type { ToastInput } from '../shell/toasts'

/** A partial settings change, as Settings.Save takes it. */
export type SettingsPatch = Partial<SettingsBody>

/** After a sidebar click, how long scroll reports may name other sections (the smooth scroll passes them). */
export const NAV_HOLD_MS = 1000

/** The section that holds each field the page can open revealed (the design's `reveal`). */
export const REVEAL_SECTION: Record<NonNullable<SettingsProps['reveal']>, SettingsSection> = { quality: 'behaviour' }

const KEYS = Object.keys(DEFAULTS) as (keyof SettingsBody)[]

const isSettingsKey = (field: string): field is keyof SettingsBody => Object.hasOwn(DEFAULTS, field)

/** The fields of `draft` that differ from `saved`; the bucket in whole seconds. */
export function patchOf(saved: SettingsBody, draft: SettingsDraft): SettingsPatch {
  const patch: Record<string, unknown> = {}
  for (const k of KEYS) {
    if (k === 'bucket_size') {
      const sec = minutesTextToSeconds(draft.bucket_size)
      if (sec !== null && sec !== saved.bucket_size) patch.bucket_size = sec
    } else if (draft[k] !== saved[k]) {
      patch[k] = draft[k]
    }
  }
  return patch as SettingsPatch
}

const isDirty = (saved: SettingsBody | null, draft: SettingsDraft): boolean =>
  saved !== null && Object.values(dirtySections(saved, draft)).some(Boolean)

/** Back's label: "Library", or the name of the folder Settings was opened from. */
export function backLabel(from: Route, session: SessionState): string {
  if (from.name === 'library' || from.name === 'settings') return 'Library'
  if (session.folderId === from.folderId && session.view) return session.view.folder
  return 'Folder'
}

export interface SettingsDraftState {
  /** The saved values; null while loading or after a failed first load. */
  saved: SettingsBody | null
  /** Why loading failed (null while Try Again runs). */
  loadError: string | null
  draft: SettingsDraft
  errors: SettingsErrors
  activeSection: SettingsSection
  saving: boolean
  leavePrompt: boolean
}

export interface SettingsDraftDeps {
  /** Settings.Save. */
  save(patch: SettingsPatch): PromiseLike<{ reclustered: boolean }>
  /** The shell's saved settings. */
  settings: Store<SettingsBody>
  router: Pick<Router, 'navigate' | 'back' | 'settingsFrom' | 'setGuard' | 'held'>
  toast(t: ToastInput): void
  /** Milliseconds; Date.now by default. */
  now?(): number
}

export interface SettingsDraftController {
  state(): SettingsDraftState
  /** For useSyncExternalStore. */
  subscribe(listener: () => void): () => void
  /** Follows the settings store, installs the leave guard and reads the settings again; returns stop. */
  start(): () => void
  change(patch: Partial<SettingsDraft>): void
  section(id: SettingsSection, via: 'nav' | 'scroll'): void
  /**
   * The page opens scrolled to this field (the design's `reveal`; call it
   * before the page shows): its section is active, and the opening scroll's
   * reports are held as after a sidebar click, from when the form first
   * appears.
   */
  reveal(field: NonNullable<SettingsProps['reveal']>): void
  save(): Promise<void>
  /** Back to the saved values. */
  revert(): void
  /** Back and Cancel: the router decides where, the guard whether. */
  back(): void
  /** The leave prompt's answer: true discards the edits and leaves. */
  confirmLeave(leave: boolean): void
  /** Try Again after a failed load. */
  reload(): Promise<void>
}

export function createSettingsDraft(deps: SettingsDraftDeps): SettingsDraftController {
  const { settings, router } = deps
  const now = deps.now ?? Date.now
  const listeners = new Set<() => void>()

  let saved: SettingsBody | null = settings.get().value ?? null
  let draft: SettingsDraft = draftOf(saved ?? DEFAULTS)
  /** What Go refused on the last Save, by field. */
  let refused: SettingsErrors = {}
  let activeSection: SettingsSection = 'grouping'
  let navHold: { target: SettingsSection; until: number } | null = null
  /** The revealed field's section, until the form first appears (its opening scroll). */
  let revealHold: SettingsSection | null = null
  let saving = false
  let leavePrompt = false
  /** A load the page waits for (Try Again, or the one on opening): show Loading, not the error. */
  let reloading = 0

  const errors = (): SettingsErrors => ({ ...refused, ...validateSettings(draft) })

  const compute = (): SettingsDraftState => {
    const snap = settings.get()
    const failed = snap.status === 'error' && snap.value === undefined && reloading === 0
    return {
      saved,
      loadError: failed ? errorText(appError(snap.error)) : null,
      draft,
      errors: errors(),
      activeSection,
      saving,
      leavePrompt,
    }
  }
  let state = compute()

  const publish = () => {
    state = compute()
    for (const l of [...listeners]) l()
  }

  /** The settings store changed: a clean draft follows the new values. */
  const sync = () => {
    const next = settings.get().value ?? null
    if (next !== saved) {
      if (next && saved === null && revealHold !== null) {
        // The form appears now, and the screen scrolls to the revealed field.
        navHold = { target: revealHold, until: now() + NAV_HOLD_MS }
        revealHold = null
      }
      if (next && !isDirty(saved, draft)) {
        draft = draftOf(next)
        refused = {}
      }
      saved = next
    }
    publish()
  }

  const guard = (to: Route): boolean => {
    if (!isDirty(saved, draft)) return true
    // The router keeps `to` as its held navigation (router.held()).
    leavePrompt = true
    publish()
    return false
  }

  const reload = async (): Promise<void> => {
    reloading++
    publish()
    try {
      await settings.reload()
    } finally {
      reloading--
      sync()
    }
  }

  return {
    state: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    start() {
      const unsubscribe = settings.subscribe(sync)
      router.setGuard(guard)
      sync()
      // Values saved since the app loaded them are read again on opening.
      void reload()
      return () => {
        unsubscribe()
        router.setGuard(null)
      }
    },
    change(patch) {
      draft = { ...draft, ...patch }
      if (Object.keys(refused).length > 0) {
        const next = { ...refused }
        for (const k of Object.keys(patch)) delete next[k as keyof SettingsBody]
        refused = next
      }
      publish()
    },
    reveal(field) {
      const id = REVEAL_SECTION[field]
      if (saved === null) revealHold = id
      else navHold = { target: id, until: now() + NAV_HOLD_MS }
      if (id === activeSection) return
      activeSection = id
      publish()
    },
    section(id, via) {
      if (via === 'nav') {
        navHold = { target: id, until: now() + NAV_HOLD_MS }
      } else if (navHold) {
        const hold = navHold
        if (id !== hold.target && now() < hold.until) return
        navHold = null
      }
      if (id === activeSection) return
      activeSection = id
      publish()
    },
    async save() {
      const base = saved
      if (saving || !base || Object.keys(errors()).length > 0) return
      const patch = patchOf(base, draft)
      if (Object.keys(patch).length === 0) return
      const sent = draft
      saving = true
      publish()
      try {
        const result = await deps.save(patch)
        settings.set((prev) => ({ ...(prev ?? base), ...patch }))
        // Supersedes a load started before the Save (the store's reload: only
        // the latest load may set the value), and confirms what Go holds.
        void settings.reload()
        // The draft becomes the saved values, unless it was edited meanwhile.
        if (draft === sent && saved) {
          draft = draftOf(saved)
          refused = {}
        }
        deps.toast({
          tone: 'pick',
          title: 'Settings saved',
          ...(result.reclustered ? { body: 'Re-clustered with new parameters' } : {}),
        })
      } catch (e) {
        const err = appError(e)
        if (err.code === 'invalid' && err.field !== undefined && isSettingsKey(err.field)) {
          // The field says it, in the design's words when it has them, unless
          // it was edited meanwhile (the next Save checks the new value).
          const field = err.field
          if (draft[field] === sent[field]) {
            refused = { ...refused, [field]: fieldCopy(field, err.reason) ?? err.message }
          }
        } else if (err.code !== 'unavailable') {
          deps.toast({ tone: 'reject', title: 'Save failed', body: errorText(err) })
          // A failed re-cluster comes after the settings were written.
          void settings.reload()
        }
      } finally {
        saving = false
        publish()
      }
    },
    revert() {
      if (!saved) return
      draft = draftOf(saved)
      refused = {}
      publish()
    },
    back() {
      router.back()
    },
    confirmLeave(leave) {
      const to = router.held() ?? router.settingsFrom()
      leavePrompt = false
      publish()
      if (leave) router.navigate(to, { force: true })
    },
    reload,
  }
}
