import React, { useCallback, useContext, useEffect, useRef, useState } from 'react'
import { AppBar, QualityContext } from './primitives'
import { fetchSettings, retryQuality, saveSettings, type QualityStatus, type SettingsBody } from '../api'
import { toast } from './Toaster'

// Built-in defaults. Mirrors config.Defaults() in internal/config/config.go
// (zero-valued booleans are off; the quality rating is on by default). "Restore defaults" only fills the form with
// these; nothing is written until the user presses Save.
const DEFAULTS: SettingsBody = {
  bucket_size: 600,
  similarity: 0.92,
  picks_dir: 'picks',
  advance_on_action: false,
  hide_rejected_photos: false,
  cross_device_merging: false,
  quality_rating: true,
}

// handleSettingsPost rejects bucket_size < 60 with a 400; checking it here
// lets us explain the problem inline and keep Save disabled instead.
const MIN_BUCKET_SEC = 60

// The wire format stays in whole seconds; the form edits minutes. Two
// decimals are enough for every whole-second value to round-trip: the
// display error is at most 0.005 min = 0.3 s, which Math.round below
// absorbs (1000 s → "16.67" → 1000.2 → 1000). Number() drops trailing
// zeros so 600 s shows as "10", not "10.00".
function secondsToMinutesText(sec: number): string {
  return String(Number((sec / 60).toFixed(2)))
}

// Returns null for anything that isn't a usable number. Note that a
// type="number" input reports "" for unparseable text too, so empty and
// garbage collapse into the same case.
function minutesTextToSeconds(text: string): number | null {
  const t = text.trim()
  if (t === '') return null
  const min = Number(t)
  if (!Number.isFinite(min)) return null
  const sec = Math.round(min * 60)
  // Beyond 2^53 JSON can't carry the value exactly (and Go would refuse
  // anything past int64), so treat it as unparseable rather than sending it.
  return Number.isSafeInteger(sec) ? sec : null
}

// Mirrors validatePicksDir in internal/server/settings.go as far as the
// browser can see: empty is rejected, and a relative path must stay inside
// the scan root (filepath.IsLocal — a lexical check, replicated here by
// resolving "." and ".." segments). Absolute paths are only allowed under
// the user's home directory, which the browser can't know, so for those the
// server remains the authority and any rejection surfaces in the save toast.
function picksDirError(p: string): string | null {
  if (p === '') return 'Enter a folder for your picks.'
  if (p.startsWith('/')) return null
  const stack: string[] = []
  for (const seg of p.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..' && stack.length > 0 && stack[stack.length - 1] !== '..') stack.pop()
    else stack.push(seg)
  }
  return stack[0] === '..' ? 'Must stay inside the scanned folder — ".." would leave it.' : null
}

const SECTIONS = [
  { id: 'grouping', label: 'Grouping' },
  { id: 'behaviour', label: 'Behaviour' },
  { id: 'output', label: 'Output' },
] as const
type SectionID = typeof SECTIONS[number]['id']

const sectionDomID = (id: SectionID) => `settings-${id}`

const prefersReducedMotion = () =>
  typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches

// One labelled setting row. The visible title is a real <label> for the
// control (buttons, inputs and ranges are all labelable, so clicking a
// toggle's title flips it like a native checkbox), and the hint has a stable
// id the control references via aria-describedby.
const Field: React.FC<React.PropsWithChildren<{ controlId: string; title: string; hint: React.ReactNode }>> = ({ controlId, title, hint, children }) => (
  <div className="grid grid-cols-[200px_1fr] gap-6 items-start">
    <div>
      <label id={`${controlId}-label`} htmlFor={controlId} className="block text-[13px] font-medium text-fg-0">{title}</label>
      <p id={`${controlId}-hint`} className="m-0 text-[11px] text-fg-2 mt-[3px] leading-[1.5]">{hint}</p>
    </div>
    <div>{children}</div>
  </div>
)

const SectionHeading: React.FC<{
  id: SectionID
  title: string
  description: string
  headingRef: (el: HTMLHeadingElement | null) => void
}> = ({ id, title, description, headingRef }) => (
  <div>
    {/* tabIndex -1 so the sidebar nav can move keyboard focus here after
        scrolling, without adding the heading to the Tab order. */}
    <h2 id={`${sectionDomID(id)}-title`} ref={headingRef} tabIndex={-1}
        className="text-[18px] m-0 font-semibold tracking-tight focus:outline-none">
      {title}
    </h2>
    <p className="m-0 mt-1 text-fg-2 text-[13px] leading-[1.5]">{description}</p>
  </div>
)

const Toggle: React.FC<{
  id: string
  labelledBy: string
  describedBy: string
  checked: boolean
  onChange: (v: boolean) => void
  testId?: string
}> = ({ id, labelledBy, describedBy, checked, onChange, testId }) => (
  <button type="button"
          id={id}
          role="switch"
          aria-checked={checked}
          aria-labelledby={labelledBy}
          aria-describedby={describedBy}
          onClick={() => onChange(!checked)}
          data-testid={testId}
          className={`relative w-9 h-5 rounded-full transition-colors border focus:outline-none focus-visible:ring-2 focus-visible:ring-pick focus-visible:ring-offset-2 focus-visible:ring-offset-bg-1 ${checked ? 'bg-pick border-pick' : 'bg-bg-3 border-line-1'}`}>
    <span className={`absolute top-0.5 ${checked ? 'left-[18px]' : 'left-0.5'} w-4 h-4 rounded-full transition-all ${checked ? 'bg-pick-ink' : 'bg-fg-1'}`} />
  </button>
)

// The switch already announces its checked state, so the visual "On/Off"
// next to it is hidden from assistive tech to avoid saying it twice.
const ToggleState: React.FC<{ on: boolean }> = ({ on }) => (
  <span aria-hidden="true" className="text-xs text-fg-2">{on ? 'On' : 'Off'}</span>
)

// One line under the quality toggle saying what the background worker is
// doing with the *saved* setting (the toggle itself may hold an unsaved edit).
function qualityStatusText(q: QualityStatus | null): string {
  if (!q) return ''
  if (!q.enabled) return 'Off: no model download, no ratings.'
  switch (q.state) {
    case 'downloading':
      return q.size > 0
        ? `Downloading the model: ${Math.floor((q.received / q.size) * 100)}% of ${Math.round(q.size / 1e6)} MB.`
        : 'Downloading the model.'
    case 'download_failed': return `Model download failed: ${q.error ?? 'unknown error'}.`
    case 'waiting': return 'Waiting for the scan to finish before rating photos.'
    case 'scoring': return `Rating photos: ${q.done.toLocaleString()} of ${q.total.toLocaleString()}.`
    case 'error': return `Rating stopped: ${q.error ?? 'unknown error'}. Restart choix to try again.`
    case 'idle': return 'Model installed; ratings are up to date.'
    default: return ''
  }
}

const ConfirmLeave: React.FC<{ onDiscard: () => void; onKeep: () => void }> = ({ onDiscard, onKeep }) => {
  const discardRef = useRef<HTMLButtonElement>(null)
  const keepRef = useRef<HTMLButtonElement>(null)

  // The non-destructive choice gets initial focus so a reflexive Enter
  // keeps the user's edits.
  useEffect(() => { keepRef.current?.focus() }, [])

  const onKeyDown = (e: React.KeyboardEvent) => {
    // The dialog is modal: keep every key away from window-level shortcut
    // handlers (e.g. "?" opening the shortcuts overlay on top of it).
    e.stopPropagation()
    if (e.key === 'Escape') {
      e.preventDefault()
      onKeep()
      return
    }
    if (e.key === 'Tab') {
      // Two buttons; the page behind is inert, so cycling between them is
      // the whole focus trap.
      const first = discardRef.current
      const last = keepRef.current
      if (!first || !last) return
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-6"
         style={{ background: 'oklch(0 0 0 / 0.55)' }}
         onMouseDown={e => { if (e.target === e.currentTarget) onKeep() }}>
      {/* tabIndex -1: clicking the dialog's text focuses the panel instead
          of <body>, so Esc and the Tab trap keep working. */}
      <div role="alertdialog" aria-modal="true" tabIndex={-1}
           aria-labelledby="settings-leave-title" aria-describedby="settings-leave-desc"
           onKeyDown={onKeyDown}
           className="w-full max-w-[380px] rounded-lg border border-line-2 bg-bg-2 shadow-pop p-5 focus:outline-none">
        <h2 id="settings-leave-title" className="m-0 text-[15px] font-semibold tracking-tight">Discard unsaved changes?</h2>
        <p id="settings-leave-desc" className="m-0 mt-2 text-[13px] text-fg-2 leading-[1.5]">
          You've changed settings on this page without saving. Leaving now throws those edits away.
        </p>
        <div className="flex justify-end gap-2 mt-5">
          <button ref={discardRef} type="button" className="btn text-reject" onClick={onDiscard}>Discard changes</button>
          <button ref={keepRef} type="button" className="btn btn-primary" onClick={onKeep}>Keep editing</button>
        </div>
      </div>
    </div>
  )
}

export const Settings: React.FC<{
  folder: string
  totalPicked: number
  totalCount: number
  onSettings: () => void
  onLibrary: () => void
  onSaved: (s: SettingsBody) => Promise<void> | void
}> = ({ folder, totalPicked, totalCount, onSettings, onLibrary, onSaved }) => {
  const [data, setData] = useState<SettingsBody | null>(null)
  // Raw text of the minutes field, kept separately from the parsed value so
  // an empty or half-typed field stays as typed instead of collapsing to 0.
  const [bucketText, setBucketText] = useState(secondsToMinutesText(DEFAULTS.bucket_size))
  const [sim, setSim] = useState(DEFAULTS.similarity)
  const [picksDir, setPicksDir] = useState(DEFAULTS.picks_dir)
  const [advance, setAdvance] = useState(DEFAULTS.advance_on_action)
  const [hideRejected, setHideRejected] = useState(DEFAULTS.hide_rejected_photos)
  const [crossDevice, setCrossDevice] = useState(DEFAULTS.cross_device_merging)
  const [qualityRating, setQualityRating] = useState(DEFAULTS.quality_rating)
  const [busy, setBusy] = useState(false)
  const qualityCtx = useContext(QualityContext)
  const [retrying, setRetrying] = useState(false)
  const retryDownload = async () => {
    setRetrying(true)
    try {
      await retryQuality()
      qualityCtx.refresh()
    } catch (e) {
      toast.reject('Retry failed', { body: String(e) })
    } finally {
      setRetrying(false)
    }
  }

  const applyForm = useCallback((s: SettingsBody) => {
    setBucketText(secondsToMinutesText(s.bucket_size))
    setSim(s.similarity)
    setPicksDir(s.picks_dir)
    setAdvance(!!s.advance_on_action)
    setHideRejected(!!s.hide_rejected_photos)
    setCrossDevice(!!s.cross_device_merging)
    setQualityRating(!!s.quality_rating)
  }, [])

  useEffect(() => {
    fetchSettings().then(s => {
      setData(s)
      applyForm(s)
    }).catch(e => toast.reject('Load settings failed', { body: String(e) }))
  }, [applyForm])

  const bucketSec = minutesTextToSeconds(bucketText)
  const bucketError =
    bucketSec === null ? 'Enter the gap in minutes, e.g. 10 or 2.5.'
    : bucketSec < MIN_BUCKET_SEC ? 'Must be at least 1 minute.'
    : null
  const picksError = picksDirError(picksDir)
  const invalid = bucketError !== null || picksError !== null

  // Dirty compares whole seconds, not the text, so "10", "10.0" and "10.004"
  // all match a saved 600 s and never show phantom unsaved changes.
  const groupingDirty = data ? (
    bucketSec !== data.bucket_size ||
    sim !== data.similarity ||
    crossDevice !== !!data.cross_device_merging
  ) : false
  const behaviourDirty = data ? (
    advance !== !!data.advance_on_action ||
    hideRejected !== !!data.hide_rejected_photos ||
    qualityRating !== !!data.quality_rating
  ) : false
  const outputDirty = data ? picksDir !== data.picks_dir : false
  const dirty = groupingDirty || behaviourDirty || outputDirty
  const sectionDirty: Record<SectionID, boolean> = { grouping: groupingDirty, behaviour: behaviourDirty, output: outputDirty }

  // The same three fields handleSettingsPost compares to decide whether a
  // save re-clusters (bucket size, similarity, cross-device merging).
  const reclusterFields = data ? [
    bucketSec !== data.bucket_size ? 'time bucket' : null,
    sim !== data.similarity ? 'similarity' : null,
    crossDevice !== !!data.cross_device_merging ? 'cross-device merge' : null,
  ].filter((f): f is string => f !== null) : []

  const atDefaults =
    bucketSec === DEFAULTS.bucket_size &&
    sim === DEFAULTS.similarity &&
    picksDir === DEFAULTS.picks_dir &&
    advance === DEFAULTS.advance_on_action &&
    hideRejected === DEFAULTS.hide_rejected_photos &&
    crossDevice === DEFAULTS.cross_device_merging &&
    qualityRating === DEFAULTS.quality_rating

  const save = async () => {
    // Save is disabled while invalid; this guard only narrows the type.
    if (bucketSec === null || invalid) return
    setBusy(true)
    try {
      const r = await saveSettings({
        bucket_size: bucketSec,
        similarity: sim,
        picks_dir: picksDir,
        advance_on_action: advance,
        hide_rejected_photos: hideRejected,
        cross_device_merging: crossDevice,
        quality_rating: qualityRating,
      })
      const fresh = await fetchSettings()
      setData(fresh)
      await onSaved(fresh)
      toast.ok('Settings saved', { body: r.reclustered ? 'Re-clustered with new parameters' : undefined })
    } catch (e) {
      toast.reject('Save failed', { body: String(e) })
    } finally {
      setBusy(false)
    }
  }

  const revertToSaved = () => { if (data) applyForm(data) }
  const restoreDefaults = () => applyForm(DEFAULTS)

  // ── Leaving with unsaved edits ────────────────────────────────────────
  // Cancel and the AppBar's brand/Library links route through requestLeave.
  // The AppBar settings button is passed through untouched: it re-navigates
  // to /settings, which keeps this component mounted and the edits intact.
  // Browser back/forward is App's popstate handler and isn't guarded here.
  const pageRef = useRef<HTMLDivElement>(null)
  const pendingLeaveRef = useRef<(() => void) | null>(null)
  const returnFocusRef = useRef<HTMLElement | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)

  const requestLeave = (go: () => void) => {
    if (!dirty) { go(); return }
    pendingLeaveRef.current = go
    returnFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    setConfirmOpen(true)
  }
  const keepEditing = () => {
    pendingLeaveRef.current = null
    setConfirmOpen(false)
  }
  const discardAndLeave = () => {
    const go = pendingLeaveRef.current
    pendingLeaveRef.current = null
    returnFocusRef.current = null
    setConfirmOpen(false)
    go?.()
  }

  // Make the page behind the dialog inert (no clicks, no Tab stops, hidden
  // from AT). Focus can only return to the trigger once inert is lifted,
  // which is why the restore lives here rather than in keepEditing.
  useEffect(() => {
    const page = pageRef.current
    if (!page) return
    if (confirmOpen) {
      page.setAttribute('inert', '')
      return
    }
    page.removeAttribute('inert')
    const el = returnFocusRef.current
    returnFocusRef.current = null
    if (el && el.isConnected) el.focus()
  }, [confirmOpen])

  // Tab close / reload. Browsers show their own generic prompt; the custom
  // text in returnValue is ignored but setting it is still required by
  // older Safari.
  useEffect(() => {
    if (!dirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty])

  // ── Sidebar section nav ───────────────────────────────────────────────
  const scrollRef = useRef<HTMLDivElement>(null)
  const sectionRefs = useRef<Record<SectionID, HTMLElement | null>>({ grouping: null, behaviour: null, output: null })
  const headingRefs = useRef<Record<SectionID, HTMLHeadingElement | null>>({ grouping: null, behaviour: null, output: null })
  const [active, setActive] = useState<SectionID>('grouping')
  // While a nav click's scroll is in flight the highlight stays on the
  // clicked item. Without this, clicking "Behaviour" on a page that bottoms
  // out before Behaviour reaches the top would flip the highlight to
  // "Output" via the at-bottom rule. The lock clears 150 ms after the last
  // scroll event (scrollend isn't available in every Safari we support).
  const navLockTimer = useRef<number | null>(null)

  const releaseNavLockSoon = useCallback(() => {
    if (navLockTimer.current !== null) window.clearTimeout(navLockTimer.current)
    navLockTimer.current = window.setTimeout(() => { navLockTimer.current = null }, 150)
  }, [])

  useEffect(() => {
    const root = scrollRef.current
    if (!root) return
    const update = () => {
      if (navLockTimer.current !== null) { releaseNavLockSoon(); return }
      // A page that fits without scrolling says nothing about position;
      // keep whatever was last clicked (initially the first section).
      if (root.scrollHeight - root.clientHeight < 2) return
      // The last section is usually too short to reach the top, so being
      // scrolled to the bottom counts as being on it.
      if (root.scrollTop + root.clientHeight >= root.scrollHeight - 2) {
        setActive(SECTIONS[SECTIONS.length - 1].id)
        return
      }
      // Otherwise: the last section whose top has passed a line a little
      // below the top of the viewport.
      const probe = root.getBoundingClientRect().top + Math.min(120, root.clientHeight / 3)
      let current: SectionID = SECTIONS[0].id
      for (const s of SECTIONS) {
        const el = sectionRefs.current[s.id]
        if (el && el.getBoundingClientRect().top <= probe) current = s.id
      }
      setActive(current)
    }
    root.addEventListener('scroll', update, { passive: true })
    window.addEventListener('resize', update)
    return () => {
      root.removeEventListener('scroll', update)
      window.removeEventListener('resize', update)
      if (navLockTimer.current !== null) window.clearTimeout(navLockTimer.current)
    }
  }, [releaseNavLockSoon])

  const goToSection = (id: SectionID) => {
    const root = scrollRef.current
    const el = sectionRefs.current[id]
    if (!root || !el) return
    setActive(id)
    releaseNavLockSoon()
    // Scroll the settings pane itself (not scrollIntoView, which may also
    // nudge the overflow-hidden ancestors). 24 px keeps the heading off the
    // very edge, matching the pane's top padding.
    const top = root.scrollTop + el.getBoundingClientRect().top - root.getBoundingClientRect().top - 24
    root.scrollTo({ top: Math.max(0, top), behavior: prefersReducedMotion() ? 'auto' : 'smooth' })
    // Move keyboard/AT focus along with the view; preventScroll so it
    // doesn't fight the (possibly smooth) scroll above.
    headingRefs.current[id]?.focus({ preventScroll: true })
  }

  const sectionProps = (id: SectionID) => ({
    id: sectionDomID(id),
    'aria-labelledby': `${sectionDomID(id)}-title`,
    ref: (el: HTMLElement | null) => { sectionRefs.current[id] = el },
    className: 'flex flex-col gap-7',
  })
  const headingRef = (id: SectionID) => (el: HTMLHeadingElement | null) => { headingRefs.current[id] = el }

  // ── Save bar status ───────────────────────────────────────────────────
  let saveNote: string | null = null
  if (dirty) {
    if (invalid) saveNote = 'Fix the highlighted field to save.'
    else if (reclusterFields.length > 0) saveNote = `Saving will re-cluster the library (${reclusterFields.join(', ')} changed). Picks are kept.`
    else saveNote = 'Saving applies immediately — no re-cluster needed.'
  }

  const simIsDefault = sim === DEFAULTS.similarity
  const simInputRef = useRef<HTMLInputElement>(null)
  const errorBorder = 'border-reject focus:border-reject'

  return (
    <>
      <div ref={pageRef} className="flex flex-col h-screen overflow-hidden bg-bg-1">
        <AppBar folder={folder} picked={totalPicked} total={totalCount}
                onSettings={onSettings} onLibrary={() => requestLeave(onLibrary)} />

        <div className="flex-1 flex overflow-hidden">
          <aside className="w-[200px] py-5 px-3 border-r border-line-1">
            <div className="eyebrow text-[11px] px-2 mb-2" aria-hidden="true">Settings</div>
            {/* Buttons rather than #hash links: a fragment change would push
                history entries and fire App's popstate route handler. */}
            <nav aria-label="Settings sections">
              <ul className="m-0 p-0 list-none flex flex-col gap-1">
                {SECTIONS.map(s => {
                  const isActive = active === s.id
                  return (
                    <li key={s.id}>
                      <button type="button"
                              aria-current={isActive ? 'true' : undefined}
                              aria-controls={sectionDomID(s.id)}
                              onClick={() => goToSection(s.id)}
                              className={`flex items-center w-full text-left px-2 py-1.5 rounded-md text-xs font-medium border-0 cursor-pointer transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-pick ${isActive ? 'text-fg-0 bg-bg-3' : 'text-fg-1 bg-transparent hover:bg-bg-2 hover:text-fg-0'}`}>
                        <span className="flex-1">{s.label}</span>
                        {sectionDirty[s.id] && (
                          <>
                            <span className="dot" data-tone="pick" aria-hidden="true" />
                            <span className="sr-only"> (unsaved changes)</span>
                          </>
                        )}
                      </button>
                    </li>
                  )
                })}
              </ul>
            </nav>
          </aside>

          {/* `relative`: containing block for `sr-only` labels, so they
              cannot stretch the document (see Library's scroller). */}
          <main ref={scrollRef} className="relative flex-1 overflow-y-auto">
            <div className="max-w-[640px] px-8 pt-8 pb-16 flex flex-col gap-10">
              <h1 className="sr-only">Settings</h1>

              <section {...sectionProps('grouping')}>
                <SectionHeading id="grouping" title="Grouping" headingRef={headingRef('grouping')}
                                description="How choix forms clusters from your folder. Saving a change here re-clusters the library; your picks are kept." />

                <Field controlId="settings-bucket" title="Time bucket"
                       hint="The longest pause allowed between consecutive shots from the same camera for them to stay in one group. A longer pause starts a new group, so a steady run of shots can span more than this.">
                  <div className="flex items-center gap-2.5">
                    <input id="settings-bucket" className={`input font-mono w-24 text-right ${bucketError ? errorBorder : ''}`}
                           type="number" inputMode="decimal" min={1} step={0.5} data-testid="bucket-input"
                           value={bucketText} onChange={e => setBucketText(e.target.value)}
                           aria-labelledby="settings-bucket-label settings-bucket-unit"
                           aria-describedby={`settings-bucket-hint${bucketError ? ' settings-bucket-error' : ''}`}
                           aria-invalid={bucketError ? true : undefined} />
                    <span id="settings-bucket-unit" className="text-fg-2 text-xs">minutes</span>
                    <span className="flex-1" />
                    {bucketSec !== null && (
                      <span className="font-mono text-[11px] text-fg-3">= {bucketSec} s</span>
                    )}
                  </div>
                  {bucketError && (
                    <p id="settings-bucket-error" className="m-0 mt-1.5 text-[11px] text-reject">{bucketError}</p>
                  )}
                </Field>

                <Field controlId="settings-sim" title="Visual similarity"
                       hint="How alike two shots in the same time group must look (CLIP similarity) to share a cluster. Only takes effect with the CLIP model installed; without it, each time group is one cluster.">
                  <div className="flex items-center gap-3.5">
                    <input id="settings-sim" ref={simInputRef} type="range" min={0.05} max={1} step={0.01} value={sim} data-testid="sim-input"
                           onChange={e => setSim(+e.target.value)}
                           aria-describedby="settings-sim-hint"
                           aria-valuetext={`${sim.toFixed(2)}${simIsDefault ? ', default' : ''}`}
                           className="flex-1" style={{ accentColor: 'oklch(0.72 0.18 145)' }} />
                    {/* The range's aria-valuetext already announces the value. */}
                    <span aria-hidden="true" className="font-mono text-xs w-10 text-right text-fg-0">{sim.toFixed(2)}</span>
                  </div>
                  {/* Right margin = readout width (w-10) + row gap (gap-3.5),
                      so the end labels line up with the ends of the track. */}
                  <div aria-hidden="true" className="flex justify-between gap-4 mt-1 mr-[54px] text-[11px] text-fg-3">
                    <span>Looser — more photos per cluster</span>
                    <span className="text-right">Stricter — near-identical only</span>
                  </div>
                  <div className="flex items-center gap-2 mt-2 text-[11px] text-fg-2">
                    <span>Default <span className="font-mono">{DEFAULTS.similarity.toFixed(2)}</span>{simIsDefault ? ' (in use)' : ''}</span>
                    {!simIsDefault && (
                      // The button unmounts once the default is in use, so
                      // hand focus to the slider rather than drop it on <body>.
                      <button type="button" className="btn btn-ghost h-6 px-2 text-[11px]"
                              onClick={() => { setSim(DEFAULTS.similarity); simInputRef.current?.focus() }}>
                        Use default
                      </button>
                    )}
                  </div>
                </Field>

                <Field controlId="crossdev-toggle" title="Cross-device merge"
                       hint="Detect when two cameras shot the same scene and merge their clusters, correcting for clock differences between devices. Needs the CLIP model. Off by default.">
                  <div className="flex items-center gap-3">
                    <Toggle id="crossdev-toggle" labelledBy="crossdev-toggle-label" describedBy="crossdev-toggle-hint"
                            checked={crossDevice} onChange={setCrossDevice} testId="crossdev-toggle" />
                    <ToggleState on={crossDevice} />
                  </div>
                </Field>
              </section>

              <section {...sectionProps('behaviour')}>
                <SectionHeading id="behaviour" title="Behaviour" headingRef={headingRef('behaviour')}
                                description="How the Library and Focus views respond while you cull. Applies as soon as you save, without re-clustering." />

                <Field controlId="advance-toggle" title="Advance on action"
                       hint="Cycle to the next photo in the cluster after a Pick or Reject. Wraps from the last photo back to the first.">
                  <div className="flex items-center gap-3">
                    <Toggle id="advance-toggle" labelledBy="advance-toggle-label" describedBy="advance-toggle-hint"
                            checked={advance} onChange={setAdvance} testId="advance-toggle" />
                    <ToggleState on={advance} />
                  </div>
                </Field>

                <Field controlId="hide-rejected-toggle" title="Hide rejected photos"
                       hint="Hide rejected photos from the Library. Clusters with no remaining photos are hidden too. Focus mode still shows everything.">
                  <div className="flex items-center gap-3">
                    <Toggle id="hide-rejected-toggle" labelledBy="hide-rejected-toggle-label" describedBy="hide-rejected-toggle-hint"
                            checked={hideRejected} onChange={setHideRejected} testId="hide-rejected-toggle" />
                    <ToggleState on={hideRejected} />
                  </div>
                </Field>

                <Field controlId="quality-toggle" title="Quality rating"
                       hint="Show a 0–10 technical-quality rating (sharpness, motion blur, noise) on every photo, to tell near-identical frames apart. Advisory only: it never picks, rejects or reorders. Downloads a 177 MB model once. The model is licensed for non-commercial use (CC BY-NC-SA 4.0); turn this off for paid work.">
                  <div className="flex items-center gap-3">
                    <Toggle id="quality-toggle" labelledBy="quality-toggle-label" describedBy="quality-toggle-hint"
                            checked={qualityRating} onChange={setQualityRating} testId="quality-toggle" />
                    <ToggleState on={qualityRating} />
                  </div>
                  <div className="mt-2 flex items-center gap-2 min-h-[22px]">
                    <p role="status" data-testid="quality-status"
                       className={`m-0 text-[11px] leading-[1.5] ${qualityCtx.status?.state === 'download_failed' || qualityCtx.status?.state === 'error' ? 'text-reject' : 'text-fg-2'}`}>
                      {qualityStatusText(qualityCtx.status)}
                    </p>
                    {qualityCtx.status?.enabled && qualityCtx.status.state === 'download_failed' && (
                      <button type="button" className="btn h-[22px] px-2 text-[11px]" onClick={retryDownload}
                              disabled={retrying} data-testid="quality-retry">
                        Retry
                      </button>
                    )}
                  </div>
                </Field>
              </section>

              <section {...sectionProps('output')}>
                <SectionHeading id="output" title="Output" headingRef={headingRef('output')}
                                description="Where picks are copied. Originals are never moved or modified. Applies as soon as you save." />

                <Field controlId="settings-picks-dir" title="Picks destination"
                       hint="Relative to the scanned folder, or an absolute path inside your home folder. Created automatically if missing.">
                  <input id="settings-picks-dir" className={`input font-mono w-full ${picksError ? errorBorder : ''}`}
                         data-testid="picks-dir-input" spellCheck={false} autoCapitalize="off" autoCorrect="off"
                         value={picksDir} onChange={e => setPicksDir(e.target.value)}
                         aria-describedby={`settings-picks-dir-hint${picksError ? ' settings-picks-dir-error' : ''}`}
                         aria-invalid={picksError ? true : undefined} />
                  {picksError && (
                    <p id="settings-picks-dir-error" className="m-0 mt-1.5 text-[11px] text-reject">{picksError}</p>
                  )}
                </Field>
              </section>

              <div className="flex flex-col gap-2 pt-4 border-t border-line-1 sticky bottom-0 bg-bg-1">
                {/* Polite live region: it only changes when the save outcome
                    category changes, not on every keystroke. */}
                <p role="status" className={`m-0 text-[11px] min-h-[1.5em] ${invalid && dirty ? 'text-reject' : 'text-fg-2'}`}>
                  {saveNote}
                </p>
                <div className="flex gap-2">
                  <button type="button" className="btn btn-ghost" onClick={restoreDefaults} disabled={atDefaults || busy}
                          title="Fill the form with the built-in defaults. Nothing changes until you save.">
                    Restore defaults
                  </button>
                  <button type="button" className="btn btn-ghost" onClick={revertToSaved} disabled={!dirty || busy}
                          title="Undo unsaved edits and go back to the last saved values">
                    Revert to saved
                  </button>
                  <span className="flex-1" />
                  <button type="button" className="btn btn-ghost" onClick={() => requestLeave(onLibrary)}>Cancel</button>
                  <button type="button" className="btn btn-primary" onClick={save} disabled={!dirty || invalid || busy} data-testid="save">
                    {busy ? 'Saving…' : 'Save changes'}
                  </button>
                </div>
              </div>
            </div>
          </main>
        </div>
      </div>

      {confirmOpen && <ConfirmLeave onDiscard={discardAndLeave} onKeep={keepEditing} />}
    </>
  )
}
