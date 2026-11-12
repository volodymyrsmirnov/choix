import { describe, expect, test } from 'bun:test'
import { DEFAULTS } from '../../design/screens/settings/draft'
import { FIELD_COPY } from '../../design/screens/settings/validate'
import type { SettingsBody } from '../../design/types'
import { createRouter, type HashHost, type Route } from '../shell/router'
import { createSettingsStore } from '../shell/settingsStore'
import type { ToastInput } from '../shell/toasts'
import { backLabel, createSettingsDraft, patchOf, REVEAL_SECTION, type SettingsDraftDeps } from './settingsDraft'

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

/** Saved values that differ from the defaults (invented). */
const SAVED: SettingsBody = { ...DEFAULTS, bucket_size: 900, similarity: 0.88, picks_dir: 'selects', advance_on_action: true }

/** A location hash without a browser (as router.test.ts). */
function fakeHost(initial = ''): HashHost & { hash: string } {
  const listeners = new Set<() => void>()
  const host = {
    hash: initial,
    getHash: () => host.hash,
    setHash(h: string) {
      if (h === host.hash) return
      host.hash = h
      for (const l of [...listeners]) l()
    },
    onHashChange(fn: () => void) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
  }
  return host
}

/** A rejected binding call carrying an app error, as Wails delivers it. */
const rejected = (cause: Record<string, unknown>) => Object.assign(new Error(String(cause.message)), { cause })

function setup(
  opts: {
    saved?: SettingsBody
    save?: SettingsDraftDeps['save']
    retry?: SettingsDraftDeps['retry']
    get?: () => Promise<SettingsBody>
    from?: Route
  } = {},
) {
  let body: SettingsBody = opts.saved ?? SAVED
  let gets = 0
  let clock = 0
  const reloads: string[] = []
  const settings = createSettingsStore({
    get: () => {
      gets++
      return opts.get ? opts.get() : Promise.resolve(body)
    },
  })
  const router = createRouter(fakeHost())
  router.start()
  if (opts.from) router.navigate(opts.from)
  router.navigate({ name: 'settings' })
  const saves: Record<string, unknown>[] = []
  const toasts: ToastInput[] = []
  const deps: SettingsDraftDeps = {
    save:
      opts.save ??
      ((patch) => {
        saves.push({ ...patch })
        body = { ...body, ...(patch as Partial<SettingsBody>) }
        return Promise.resolve({ saved: true, reclustered: 'bucket_size' in patch })
      }),
    retry: opts.retry ?? (() => Promise.resolve()),
    settings,
    models: {
      reload: () => {
        reloads.push('models')
        return Promise.resolve()
      },
    },
    quality: {
      reload: () => {
        reloads.push('quality')
        return Promise.resolve()
      },
    },
    router,
    toast: (t) => {
      toasts.push(t)
    },
    now: () => clock,
  }
  const ctl = createSettingsDraft(deps)
  return {
    ctl,
    router,
    settings,
    saves,
    toasts,
    reloads,
    tick: (ms: number) => {
      clock += ms
    },
    gets: () => gets,
    setBody: (b: SettingsBody) => {
      body = b
    },
  }
}

/** Starts the store and the controller and waits for the first load. */
async function started(s: ReturnType<typeof setup>) {
  const stopStore = s.settings.start()
  const stop = s.ctl.start()
  await flush()
  return () => {
    stop()
    stopStore()
  }
}

describe('settingsDraft', () => {
  test('draft keeps raw text for numeric fields', async () => {
    const s = setup()
    // Loading: no saved values yet, no error.
    expect(s.ctl.state().saved).toBeNull()
    expect(s.ctl.state().loadError).toBeNull()
    await started(s)

    const st = () => s.ctl.state()
    expect(st().saved).toEqual(SAVED)
    // The bucket is minutes as text; the saved value stays whole seconds.
    expect(st().draft.bucket_size).toBe('15')
    expect(st().errors).toEqual({})

    for (const raw of ['', '2.', '10.0', ' 7 ', 'abc']) {
      s.ctl.change({ bucket_size: raw })
      expect(st().draft.bucket_size).toBe(raw)
      expect(st().saved?.bucket_size).toBe(900)
    }
    // The design's validateSettings decides, with the design's copy.
    s.ctl.change({ bucket_size: '' })
    expect(st().errors.bucket_size).toBe(FIELD_COPY.bucket_size.nan)
    s.ctl.change({ bucket_size: '0.5' })
    expect(st().errors.bucket_size).toBe(FIELD_COPY.bucket_size.min)
    s.ctl.change({ bucket_size: '15.0' })
    expect(st().errors).toEqual({})
    // The same whole seconds as saved: nothing unsaved, so leaving needs no prompt.
    expect(s.router.navigate({ name: 'library' })).toBe(true)
  })

  test('save sends only changed fields', async () => {
    // The pure diff: the bucket compares whole seconds, so "15.0" is not a change.
    expect(patchOf(SAVED, { ...SAVED, bucket_size: '15.0' })).toEqual({})
    expect(patchOf(SAVED, { ...SAVED, bucket_size: '5', advance_on_action: false })).toEqual({
      bucket_size: 300,
      advance_on_action: false,
    })

    const s = setup()
    await started(s)
    s.ctl.change({ bucket_size: '5', advance_on_action: false, picks_dir: SAVED.picks_dir })
    await s.ctl.save()
    expect(s.saves).toEqual([{ bucket_size: 300, advance_on_action: false }])

    // The shell's settings store holds what was saved, and the draft is clean again.
    const next = { ...SAVED, bucket_size: 300, advance_on_action: false }
    expect(s.settings.get().value).toEqual(next)
    expect(s.ctl.state().saved).toEqual(next)
    expect(s.ctl.state().draft.bucket_size).toBe('5')
    expect(s.ctl.state().saving).toBe(false)
    // v1's toast; a re-cluster is named in its body.
    expect(s.toasts).toEqual([{ tone: 'pick', title: 'Settings saved', body: 'Re-clustered with new parameters' }])

    // An invalid draft never reaches Go (validateSettings runs first).
    s.ctl.change({ picks_dir: '/Volumes/Elsewhere' })
    expect(s.ctl.state().errors.picks_dir).toBe(FIELD_COPY.picks_dir.absolute)
    await s.ctl.save()
    expect(s.saves).toHaveLength(1)

    // Nothing changed: nothing is sent.
    s.ctl.revert()
    await s.ctl.save()
    expect(s.saves).toHaveLength(1)
  })

  test("invalid from Go maps to the field's design copy", async () => {
    let answer: Error = rejected({
      code: 'invalid',
      field: 'picks_dir',
      reason: 'has_originals',
      message: 'settings: go text the user never reads',
    })
    const s = setup({ save: () => Promise.reject(answer) })
    await started(s)

    s.ctl.change({ picks_dir: 'photos' })
    await s.ctl.save()
    expect(s.ctl.state().errors.picks_dir).toBe(FIELD_COPY.picks_dir.has_originals)
    // The field says it; no toast, and Go's message is not shown.
    expect(s.toasts).toEqual([])
    expect(s.ctl.state().saving).toBe(false)
    // Editing the refused field clears Go's refusal (it was for the old value).
    s.ctl.change({ picks_dir: 'photos2' })
    expect(s.ctl.state().errors.picks_dir).toBeUndefined()
    s.ctl.change({ picks_dir: 'photos' })
    expect(s.ctl.state().errors.picks_dir).toBeUndefined()

    // A reason the design has no copy for falls back to Go's message.
    answer = rejected({ code: 'invalid', field: 'picks_dir', reason: 'not_dir', message: 'settings: not a folder' })
    await s.ctl.save()
    expect(s.ctl.state().errors.picks_dir).toBe('settings: not a folder')

    // Any other failure is the "Save failed" toast, and the saved values are
    // read again (a re-cluster failure comes after the settings were saved).
    const before = s.gets()
    s.ctl.change({ picks_dir: 'keepers' })
    answer = rejected({ code: 'internal', message: 'the settings file could not be written' })
    await s.ctl.save()
    await flush()
    expect(s.toasts).toEqual([{ tone: 'reject', title: 'Save failed', body: 'the settings file could not be written' }])
    expect(s.gets()).toBe(before + 1)
  })

  test('leaving with changes opens the prompt', async () => {
    const s = setup({ from: { name: 'folder', folderId: 'f1' } })
    const stop = await started(s)

    // Nothing unsaved: Back goes where Settings was opened from.
    s.ctl.back()
    expect(s.router.current()).toEqual({ name: 'folder', folderId: 'f1' })
    s.router.navigate({ name: 'settings' })

    // Unsaved edits: Back (and any navigation, e.g. the menu) opens the prompt instead.
    s.ctl.change({ advance_on_action: false })
    s.ctl.back()
    expect(s.router.current()).toEqual({ name: 'settings' })
    expect(s.ctl.state().leavePrompt).toBe(true)
    // Keep editing: the prompt closes, the edits stay.
    s.ctl.confirmLeave(false)
    expect(s.ctl.state().leavePrompt).toBe(false)
    expect(s.ctl.state().draft.advance_on_action).toBe(false)

    expect(s.router.navigate({ name: 'library' })).toBe(false)
    expect(s.ctl.state().leavePrompt).toBe(true)
    // Discard: leaves for where that navigation was going.
    s.ctl.confirmLeave(true)
    expect(s.ctl.state().leavePrompt).toBe(false)
    expect(s.router.current()).toEqual({ name: 'library' })

    // Once stopped, the guard is gone.
    stop()
    s.router.navigate({ name: 'settings' })
    s.ctl.change({ advance_on_action: true, hide_rejected_photos: true })
    expect(s.router.navigate({ name: 'library' })).toBe(true)
  })

  test('saved values changed elsewhere follow a clean draft, not an edited one', async () => {
    const s = setup()
    await started(s)
    // Another writer (the Library's model switches) put new saved values.
    s.settings.set({ ...SAVED, quality_rating: false })
    expect(s.ctl.state().draft.quality_rating).toBe(false)
    // With an edit in progress the edit stays; untouched fields stay as typed too.
    s.ctl.change({ bucket_size: '20' })
    s.settings.set({ ...SAVED, quality_rating: true })
    expect(s.ctl.state().draft.bucket_size).toBe('20')
    expect(s.ctl.state().draft.quality_rating).toBe(false)
    expect(s.ctl.state().saved?.quality_rating).toBe(true)
  })

  test('a nav click ignores scroll reports until the pane arrives', async () => {
    const s = setup()
    await started(s)
    expect(s.ctl.state().activeSection).toBe('grouping')
    s.ctl.section('output', 'nav')
    expect(s.ctl.state().activeSection).toBe('output')
    // The smooth scroll passes Behaviour on its way.
    s.ctl.section('behaviour', 'scroll')
    expect(s.ctl.state().activeSection).toBe('output')
    s.ctl.section('output', 'scroll')
    s.ctl.section('behaviour', 'scroll')
    expect(s.ctl.state().activeSection).toBe('behaviour')
    // A scroll that never arrives stops holding the reports after a while.
    s.ctl.section('grouping', 'nav')
    s.ctl.section('behaviour', 'scroll')
    expect(s.ctl.state().activeSection).toBe('grouping')
    s.tick(1500)
    s.ctl.section('behaviour', 'scroll')
    expect(s.ctl.state().activeSection).toBe('behaviour')
  })

  test("a page opened on a revealed field makes the field's section active, and the opening scroll does not move it", async () => {
    expect(REVEAL_SECTION).toEqual({ quality: 'behaviour' })
    const s = setup()
    s.ctl.reveal('quality')
    expect(s.ctl.state().activeSection).toBe('behaviour')
    await started(s)
    expect(s.ctl.state().activeSection).toBe('behaviour')
    // The opening scroll brings the field in view; the pane may be at its
    // bottom then, which reports the last section (v1).
    s.ctl.section('output', 'scroll')
    expect(s.ctl.state().activeSection).toBe('behaviour')
    // Once the user scrolls, the scroll leads again.
    s.ctl.section('behaviour', 'scroll')
    s.ctl.section('output', 'scroll')
    expect(s.ctl.state().activeSection).toBe('output')
  })

  test('the hold of a revealed field starts when the form first appears, not before', async () => {
    let answer: (b: SettingsBody) => void = () => {}
    const s = setup({
      get: () =>
        new Promise<SettingsBody>((resolve) => {
          answer = resolve
        }),
    })
    s.ctl.reveal('quality')
    const stop = await started(s)
    expect(s.ctl.state().saved).toBeNull()
    // A slow load: the hold would have run out by the time the form appears.
    s.tick(5000)
    answer(SAVED)
    await flush()
    expect(s.ctl.state().saved).toEqual(SAVED)
    s.ctl.section('output', 'scroll')
    expect(s.ctl.state().activeSection).toBe('behaviour')
    // And it still runs out when the opening scroll never reports.
    s.tick(1500)
    s.ctl.section('output', 'scroll')
    expect(s.ctl.state().activeSection).toBe('output')
    stop()
  })

  test('retry asks Go, then reads both statuses again', async () => {
    let release: () => void = () => {}
    const kinds: string[] = []
    const s = setup({
      retry: (kind) => {
        kinds.push(kind)
        return new Promise<void>((resolve) => {
          release = resolve
        })
      },
    })
    await started(s)
    const done = s.ctl.retry('quality')
    expect(s.ctl.state().retrying).toBe(true)
    // A second press while one is in flight sends nothing.
    void s.ctl.retry('quality')
    expect(kinds).toEqual(['quality'])
    release()
    await done
    expect(s.ctl.state().retrying).toBe(false)
    expect(s.reloads.sort()).toEqual(['models', 'quality'])
    // CLIP's Retry has no busy look of its own.
    const clip = s.ctl.retry('clip')
    expect(s.ctl.state().retrying).toBe(false)
    release()
    await clip
    expect(kinds).toEqual(['quality', 'clip'])
    expect(s.toasts).toEqual([])
  })

  test('a failed retry shows the Retry failed toast', async () => {
    const s = setup({ retry: () => Promise.reject(rejected({ code: 'internal', message: 'the queue is closed' })) })
    await started(s)
    await s.ctl.retry('quality')
    expect(s.ctl.state().retrying).toBe(false)
    expect(s.toasts).toEqual([{ tone: 'reject', title: 'Retry failed', body: 'the queue is closed' }])
  })

  test('a failed load shows the error, and Try Again shows loading', async () => {
    let answer: () => Promise<SettingsBody> = () => Promise.reject(rejected({ code: 'internal', message: 'unreadable' }))
    const s = setup({ get: () => answer() })
    await started(s)
    expect(s.ctl.state().saved).toBeNull()
    expect(s.ctl.state().loadError).toBe('unreadable')

    let resolve: (b: SettingsBody) => void = () => {}
    answer = () =>
      new Promise<SettingsBody>((r) => {
        resolve = r
      })
    const again = s.ctl.reload()
    expect(s.ctl.state().loadError).toBeNull()
    expect(s.ctl.state().saved).toBeNull()
    resolve(SAVED)
    await again
    expect(s.ctl.state().saved).toEqual(SAVED)
    expect(s.ctl.state().draft.bucket_size).toBe('15')
    expect(s.ctl.state().loadError).toBeNull()
  })
  test('discard after the folder disappears goes to the library', async () => {
    const s = setup({ from: { name: 'focus', folderId: 'f1', clusterId: 2 } })
    await started(s)
    s.ctl.change({ advance_on_action: false })
    s.ctl.back()
    expect(s.ctl.state().leavePrompt).toBe(true)
    // While the prompt is open the folder goes away: the session retargets
    // Settings to the Library (App.tsx's settingsToLibrary).
    s.router.retargetSettings({ name: 'library' })
    s.ctl.confirmLeave(true)
    expect(s.router.current()).toEqual({ name: 'library' })
  })

  test('a load started before a save does not overwrite the saved values', async () => {
    let body: SettingsBody = SAVED
    const loads: { resolve(b: SettingsBody): void; at: SettingsBody }[] = []
    let deferred = false
    const s = setup({
      get: () => {
        if (!deferred) return Promise.resolve(body)
        return new Promise<SettingsBody>((resolve) => loads.push({ resolve, at: body }))
      },
      save: (patch) => {
        body = { ...body, ...(patch as Partial<SettingsBody>) }
        return Promise.resolve({ reclustered: false })
      },
    })
    const stopStore = s.settings.start()
    await flush()
    // The load on opening Settings is slow; it answers with what Go had then.
    deferred = true
    const stop = s.ctl.start()
    expect(loads).toHaveLength(1)
    expect(s.ctl.state().saved).toEqual(SAVED)

    s.ctl.change({ quality_rating: false })
    await s.ctl.save()
    expect(s.settings.get().value?.quality_rating).toBe(false)
    // The older load answers after the save: it must not win.
    loads[0].resolve(loads[0].at)
    await flush()
    expect(s.settings.get().value?.quality_rating).toBe(false)
    expect(s.ctl.state().saved?.quality_rating).toBe(false)
    expect(s.ctl.state().draft.quality_rating).toBe(false)
    // Any load started after the save carries the saved values.
    for (const l of loads.slice(1)) l.resolve(l.at)
    await flush()
    expect(s.settings.get().value?.quality_rating).toBe(false)
    expect(s.ctl.state().draft.quality_rating).toBe(false)
    stop()
    stopStore()
  })

  test('an invalid answer for a field edited during the save is dropped', async () => {
    let refuse: (e: Error) => void = () => {}
    const s = setup({
      save: () =>
        new Promise((_, reject) => {
          refuse = reject
        }),
    })
    await started(s)
    s.ctl.change({ picks_dir: 'photos' })
    const saving = s.ctl.save()
    expect(s.ctl.state().saving).toBe(true)
    // The user corrects the field while Go is still answering.
    s.ctl.change({ picks_dir: 'keepers' })
    refuse(rejected({ code: 'invalid', field: 'picks_dir', reason: 'has_originals', message: 'settings: refused' }))
    await saving
    expect(s.ctl.state().errors.picks_dir).toBeUndefined()
    expect(s.ctl.state().saving).toBe(false)
    expect(s.toasts).toEqual([])
    // The corrected value can be saved.
    expect(s.ctl.state().draft.picks_dir).toBe('keepers')
  })
})

describe('backLabel', () => {
  const view = (folder: string) => ({ folder }) as never
  test('names the place Settings returns to', () => {
    const closed = { folderId: null, status: 'closed' as const, view: null, error: null }
    expect(backLabel({ name: 'library' }, closed)).toBe('Library')
    const open = { folderId: 'f1', status: 'open' as const, view: view('Lake trip'), error: null }
    expect(backLabel({ name: 'folder', folderId: 'f1' }, open)).toBe('Lake trip')
    expect(backLabel({ name: 'focus', folderId: 'f1', clusterId: 4 }, open)).toBe('Lake trip')
    // No view of that folder (it failed to open, or another one is open).
    expect(backLabel({ name: 'folder', folderId: 'f2' }, open)).toBe('Folder')
    expect(backLabel({ name: 'folder', folderId: 'f1' }, { ...open, status: 'error', view: null })).toBe('Folder')
  })
})
