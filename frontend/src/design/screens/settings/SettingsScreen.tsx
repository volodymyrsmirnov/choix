import { useEffect, useLayoutEffect, useRef, type RefObject } from 'react'
import {
  BackButton,
  Button,
  EmptyState,
  Field,
  IconButton,
  Prompt,
  SettingsNav,
  SettingsSaveBar,
  SettingsSection,
  SettingsStatus,
  SettingsSwitch,
  Slider,
  Spinner,
  TextInput,
  TitleBar,
  Toaster,
} from '../../components'
import { noOrphan, plural } from '../../format'
import { prefersReducedMotion } from '../../motion'
import { atDefaults, DEFAULTS, dirtySections, draftOf, qualityStatusText, saveNote } from './draft'
import { SETTINGS_OVERLAYS, type SettingsProps, type SettingsSection as SectionId } from './props'
import { minutesTextToSeconds } from './validate'

const SECTIONS: { id: SectionId; label: string }[] = [
  { id: 'grouping', label: 'Grouping' },
  { id: 'behaviour', label: 'Behaviour' },
  { id: 'output', label: 'Output' },
]
const sectionDomID = (id: string) => `settings-${id}`
/** The quality rating field (switch, status line), for `reveal`. */
const QUALITY_FIELD = 'settings-quality-field'


/** Where the pane must scroll to bring a section's heading 24 px under its top. */
function sectionTop(pane: HTMLElement, id: SectionId): number | null {
  const el = document.getElementById(sectionDomID(id))
  if (!el) return null
  return Math.max(0, pane.scrollTop + el.getBoundingClientRect().top - pane.getBoundingClientRect().top - 24)
}

/** The section the pane is showing (v1): scrolled to the bottom counts as the
 *  last one; otherwise the last whose top has passed a line near the top. */
function sectionInView(pane: HTMLElement): SectionId | null {
  if (pane.scrollHeight - pane.clientHeight < 2) return null
  if (pane.scrollTop + pane.clientHeight >= pane.scrollHeight - 2) return SECTIONS[SECTIONS.length - 1].id
  const probe = pane.getBoundingClientRect().top + Math.min(120, pane.clientHeight / 3)
  let current = SECTIONS[0].id
  for (const s of SECTIONS) {
    const el = document.getElementById(sectionDomID(s.id))
    if (el && el.getBoundingClientRect().top <= probe) current = s.id
  }
  return current
}

/**
 * Settings (v1 Settings.tsx): the title bar (Back, help), the sidebar of
 * sections, the form and the save bar; the leave-without-saving prompt over
 * it. Loading and a failed load replace the sidebar and the form.
 */
export function SettingsScreen(p: SettingsProps) {
  const pane = useRef<HTMLElement>(null)
  const loaded = p.saved !== null

  // A page opened on another section starts scrolled to it (the app opens
  // on Grouping; the prototype's section states do not).
  useLayoutEffect(() => {
    const el = pane.current
    if (!el) return
    if (p.reveal === 'quality') {
      const field = document.getElementById(QUALITY_FIELD)
      // 16 px: less than the 28 px between fields, so no sliver of the
      // field above shows cut at the pane's top edge.
      if (field) el.scrollTop = Math.max(0, el.scrollTop + field.getBoundingClientRect().top - el.getBoundingClientRect().top - 16)
      return
    }
    if (p.activeSection === 'grouping') return
    const top = sectionTop(el, p.activeSection)
    if (top !== null) el.scrollTop = top
    // Only when the form first appears: later section changes come from the
    // user scrolling or clicking, which already moved the pane.
  }, [loaded])

  let body
  if (p.saved) {
    body = <Form {...p} saved={p.saved} pane={pane} />
  } else if (p.loadError) {
    body = (
      <div className="flex-1 h-full overflow-y-auto" data-scroll>
        <div className="min-h-full flex items-center justify-center">
          <EmptyState art={false} level={2} title="Settings could not be loaded" body={p.loadError}>
            <Button variant="default" icon="refresh" onClick={p.onReload}>
              Try Again
            </Button>
          </EmptyState>
        </div>
      </div>
    )
  } else {
    body = (
      <div className="flex-1 flex items-center justify-center">
        <Spinner label="Loading settings" />
      </div>
    )
  }

  return (
    <div className="relative flex flex-col h-full min-h-0">
      <TitleBar
        leading={<BackButton to={p.backTo} onClick={p.onBack} />}
        title="Settings"
        trailing={<IconButton icon="help" label="Keyboard shortcuts" onClick={p.onHelp} aria-haspopup="dialog" />}
      />
      <div className="flex-1 min-h-0 flex">{body}</div>
      {/* With the form shown, the toasts sit over its pane instead (Form). */}
      {!p.saved && <Toaster items={p.toasts} onUndo={p.onUndoToast} onDismiss={p.onDismissToast} />}
      {p.leavePrompt && (
        <Prompt
          id={SETTINGS_OVERLAYS.leave}
          title="Discard unsaved changes?"
          message="You've changed settings on this page without saving. Leaving now throws those edits away."
          buttons={[
            { label: 'Discard changes', role: 'destructive', onClick: () => p.onConfirmLeave(true) },
            { label: 'Keep editing', role: 'default', onClick: () => p.onConfirmLeave(false) },
          ]}
        />
      )}
    </div>
  )
}

function Form(p: SettingsProps & { saved: NonNullable<SettingsProps['saved']>; pane: RefObject<HTMLElement> }) {
  const { draft, errors, saved } = p
  const dirtyBy = dirtySections(saved, draft)
  const dirty = dirtyBy.grouping || dirtyBy.behaviour || dirtyBy.output
  const invalid = Object.keys(errors).length > 0
  const bucketSec = minutesTextToSeconds(draft.bucket_size)
  const simIsDefault = draft.similarity === DEFAULTS.similarity
  const visual = draft.visual_grouping
  const q = p.quality
  const describedBy = (id: string, err?: string) => `${id}-hint${err ? ` ${id}-error` : ''}`

  // v1 recomputed the section in view on resize as well as on scroll.
  const report = useRef(p.onSection)
  report.current = p.onSection
  const activeRef = useRef(p.activeSection)
  activeRef.current = p.activeSection
  useEffect(() => {
    const onResize = () => {
      const el = p.pane.current
      const id = el && sectionInView(el)
      if (id && id !== activeRef.current) report.current(id, 'scroll')
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [p.pane])

  const goToSection = (id: SectionId) => {
    const el = p.pane.current
    const top = el ? sectionTop(el, id) : null
    if (el && top !== null) el.scrollTo({ top, behavior: prefersReducedMotion() ? 'auto' : 'smooth' })
    // Keyboard and screen-reader focus follow the view; preventScroll so it
    // does not fight the (possibly smooth) scroll above.
    document.getElementById(`${sectionDomID(id)}-title`)?.focus({ preventScroll: true })
    p.onSection(id, 'nav')
  }

  return (
    <>
      <SettingsNav
        items={SECTIONS.map((s) => ({ ...s, dirty: dirtyBy[s.id] }))}
        active={p.activeSection}
        controls={sectionDomID}
        onSelect={(id) => goToSection(id as SectionId)}
      />
      <div className="flex-1 min-w-0 flex flex-col">
        {/* The pane and the toasts' containing block: the toast stack sits at
            the bottom-right of the pane, above the save bar, so a toast never
            covers Save (v1's window-level toasts did at 960 px). */}
        <div className="relative flex-1 min-h-0 flex flex-col">
          {/* `relative`: containing block for sr-only text, so it cannot
              stretch the document. */}
          <main
            ref={p.pane}
            className="relative flex-1 min-h-0 overflow-y-auto"
            data-scroll
            data-measured=""
            data-measured-by="settings-scroll"
            onScroll={(e) => {
              const id = sectionInView(e.currentTarget)
              if (id && id !== p.activeSection) p.onSection(id, 'scroll')
            }}
          >
            <div className="w-full max-w-[784px] mx-auto px-8 pt-8 pb-16 flex flex-col gap-10">
              <SettingsSection
                id={sectionDomID('grouping')}
                title="Grouping"
                description="How Choix forms clusters in each folder. Saving a change here re-clusters your folders; your picks are kept."
              >
                <Field
                  controlId="visual-toggle"
                  title="Visual grouping"
                  hint="Group shots that look alike, using Apple’s on-device image analysis. Nothing is downloaded and nothing leaves this Mac. Off: folders are grouped by time only."
                  aside={<SettingsSwitch id="visual-toggle" checked={visual} onChange={(v) => p.onChange({ visual_grouping: v })} testId="visual-toggle" />}
                />

                <Field
                  controlId="settings-bucket"
                  title="Time bucket"
                  hint="The longest pause allowed between consecutive shots from the same camera for them to stay in one group. A longer pause starts a new group, so a steady run of shots can span more than this."
                  error={errors.bucket_size}
                >
                  <div className="flex items-center gap-2.5">
                    <TextInput
                      id="settings-bucket"
                      type="number"
                      inputMode="decimal"
                      min={1}
                      step={0.5}
                      mono
                      align="right"
                      className="w-24"
                      data-testid="bucket-input"
                      value={draft.bucket_size}
                      onChange={(v) => p.onChange({ bucket_size: v })}
                      invalid={!!errors.bucket_size}
                      aria-labelledby="settings-bucket-label settings-bucket-unit"
                      aria-describedby={describedBy('settings-bucket', errors.bucket_size)}
                    />
                    <span id="settings-bucket-unit" className="text-fg-2 text-12">
                      minutes
                    </span>
                    <span className="flex-1" />
                    {bucketSec !== null && <span className="font-mono text-11 text-fg-3">= {bucketSec} s</span>}
                  </div>
                </Field>

                <Field
                  controlId="settings-sim"
                  title="Visual similarity"
                  hint="How alike two shots in the same time group must look to share a cluster. Only takes effect with visual grouping on; without it, each time group is one cluster."
                  error={errors.similarity}
                >
                  <Slider
                    id="settings-sim"
                    label="Visual similarity"
                    value={draft.similarity}
                    min={0.05}
                    max={1}
                    step={0.01}
                    format={(v) => v.toFixed(2)}
                    valueText={(v) => `${v.toFixed(2)}${v === DEFAULTS.similarity ? ', default' : ''}`}
                    disabled={!visual}
                    testId="sim-input"
                    describedBy={describedBy('settings-sim', errors.similarity)}
                    onChange={(v) => p.onChange({ similarity: v })}
                  />
                  {/* Right margin = readout width (w-10) + row gap (gap-3.5), so
                      the end labels line up with the ends of the track. */}
                  <div aria-hidden="true" className={`flex justify-between gap-4 mt-1 mr-[54px] text-11 text-fg-3 ${visual ? '' : 'opacity-40'}`}>
                    <span>{noOrphan('Looser — more photos per cluster')}</span>
                    <span className="text-right">{noOrphan('Stricter — near-identical only')}</span>
                  </div>
                  <div className="flex items-center gap-2 mt-2 text-11 text-fg-2 min-h-6">
                    <span>
                      Default <span className="font-mono">{DEFAULTS.similarity.toFixed(2)}</span>
                      {simIsDefault ? ' (in use)' : ''}
                    </span>
                    {!simIsDefault && (
                      <Button
                        variant="ghost"
                        className="h-6 px-2 text-11"
                        disabled={!visual}
                        onClick={() => {
                          p.onChange({ similarity: DEFAULTS.similarity })
                          // The button goes away once the default is in use, so
                          // hand focus to the slider rather than drop it on <body>.
                          document.getElementById('settings-sim')?.focus()
                        }}
                      >
                        Use default
                      </Button>
                    )}
                  </div>
                </Field>

                <Field
                  controlId="crossdev-toggle"
                  title="Cross-device merge"
                  hint="Detect when two cameras shot the same scene and merge their clusters, correcting for clock differences between devices. Needs visual grouping. Off by default."
                  aside={
                    <SettingsSwitch
                      id="crossdev-toggle"
                      checked={draft.cross_device_merging}
                      disabled={!visual}
                      onChange={(v) => p.onChange({ cross_device_merging: v })}
                      testId="crossdev-toggle"
                    />
                  }
                />
              </SettingsSection>

              <SettingsSection
                id={sectionDomID('behaviour')}
                title="Behaviour"
                description="How the Folder and Focus views respond while you cull. Applies as soon as you save, without re-clustering."
              >
                <Field
                  controlId="advance-toggle"
                  title="Advance on action"
                  hint="Cycle to the next photo in the cluster after a Pick or Reject. Wraps from the last photo back to the first."
                  aside={
                    <SettingsSwitch
                      id="advance-toggle"
                      checked={draft.advance_on_action}
                      onChange={(v) => p.onChange({ advance_on_action: v })}
                      testId="advance-toggle"
                    />
                  }
                />

                <Field
                  controlId="hide-rejected-toggle"
                  title="Hide rejected photos"
                  hint="Hide rejected photos from a folder's clusters. Clusters with no remaining photos are hidden too. Focus mode still shows everything."
                  aside={
                    <SettingsSwitch
                      id="hide-rejected-toggle"
                      checked={draft.hide_rejected_photos}
                      onChange={(v) => p.onChange({ hide_rejected_photos: v })}
                      testId="hide-rejected-toggle"
                    />
                  }
                />

                <div id={QUALITY_FIELD}>
                  <Field
                    controlId="quality-toggle"
                    title="Quality rating"
                    hint="Show a 0–10 technical-quality rating (sharpness, noise, exposure) on every photo, to tell near-identical frames apart. Advisory only: it never picks, rejects or reorders. It compares frames of the same scene; it does not rank different scenes."
                    aside={
                      <SettingsSwitch
                        id="quality-toggle"
                        checked={draft.quality_rating}
                        onChange={(v) => p.onChange({ quality_rating: v })}
                        testId="quality-toggle"
                      />
                    }
                    status={
                      <SettingsStatus
                        testId="quality-status"
                        text={qualityStatusText(q)}
                        progress={
                          q?.enabled && q.state === 'scoring' && q.total > 0
                            ? {
                                value: Math.min(1, q.done / q.total),
                                label: 'Quality rating progress',
                                valueText: `${q.done.toLocaleString('en-US')} of ${plural(q.total, 'photo', 'photos')}`,
                              }
                            : undefined
                        }
                      />
                    }
                  />
                </div>
              </SettingsSection>

              <SettingsSection
                id={sectionDomID('output')}
                title="Output"
                description="Where picks are copied. Originals are never moved or modified. Applies as soon as you save."
              >
                <Field
                  controlId="settings-picks-dir"
                  title="Picks destination"
                  hint="A folder name inside the scanned folder, e.g. picks."
                  error={errors.picks_dir}
                >
                  <TextInput
                    id="settings-picks-dir"
                    mono
                    className="w-full"
                    data-testid="picks-dir-input"
                    value={draft.picks_dir}
                    onChange={(v) => p.onChange({ picks_dir: v })}
                    invalid={!!errors.picks_dir}
                    aria-describedby={describedBy('settings-picks-dir', errors.picks_dir)}
                  />
                </Field>
              </SettingsSection>
            </div>
          </main>
          <Toaster items={p.toasts} onUndo={p.onUndoToast} onDismiss={p.onDismissToast} />
        </div>
        <div className="w-full max-w-[784px] mx-auto px-8 pb-4">
          <SettingsSaveBar
            note={saveNote(saved, draft, invalid)}
            invalid={invalid}
            dirty={dirty}
            atDefaults={atDefaults(draft)}
            saving={p.saving}
            onRestoreDefaults={() => p.onChange(draftOf(DEFAULTS))}
            onRevert={p.onRevert}
            onCancel={p.onBack}
            onSave={p.onSave}
          />
        </div>
      </div>
    </>
  )
}
