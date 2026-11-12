import type { ModelStatus, QualityStatus, SettingsBody, ToastItem } from '../../types'

/** Overlay ids Settings registers; each is also the key that opens it. */
export const SETTINGS_OVERLAYS = {
  /** "Discard unsaved changes?" — leaving with unsaved edits (a Prompt). */
  leave: 'settings-leave',
} as const

/** The sidebar's sections, in page order. */
export type SettingsSection = 'grouping' | 'behaviour' | 'output'

/**
 * The form as the user is editing it. Numeric fields that are typed hold the
 * raw text, so an empty or half-typed field stays as typed instead of
 * collapsing to 0: `bucket_size` is the time bucket in **minutes**, as typed
 * (the saved value, `SettingsBody.bucket_size`, is whole seconds). The
 * similarity slider can only produce numbers, so it stays a number.
 */
export type SettingsDraft = Omit<SettingsBody, 'bucket_size'> & { bucket_size: string }

/** One field's validation errors, keyed like SettingsBody (see validateSettings). */
export type SettingsErrors = Partial<Record<keyof SettingsBody, string>>

/**
 * Settings (v1 Settings.tsx; spec §4.5: an in-window page): a sidebar of
 * sections — Grouping, Behaviour, Output — with unsaved markers, the form,
 * and a save bar. Machine-wide settings; saving the grouping fields
 * re-clusters.
 */
export interface SettingsProps {
  /** The last saved values; null while loading (or after a failed load). */
  saved: SettingsBody | null
  /** Why loading failed; the page shows it with Try Again. */
  loadError: string | null
  draft: SettingsDraft
  /** From validateSettings(draft); the screen only displays them. */
  errors: SettingsErrors
  /** The highlighted sidebar item. The app derives it from the scroll
   *  position (as v1); a state that opens on another section is scrolled to
   *  it when the page mounts. */
  activeSection: SettingsSection
  /** A page opened to show one field starts scrolled so that field is in
   *  view (e.g. the quality indicator opening Settings at the quality
   *  rating). Takes precedence over `activeSection` for the first scroll. */
  reveal?: 'quality'
  /** A save is in flight. */
  saving: boolean
  /** The quality worker (saved setting), for the status line under the switch. */
  quality: QualityStatus | null
  /** The quality model's Retry is in flight. */
  retrying: boolean
  /** Both model downloads, [clip, quality]; never both `downloading`. */
  models: ModelStatus[]
  /** The "Discard unsaved changes?" prompt is open. */
  leavePrompt: boolean
  toasts: ToastItem[]
  /** Restore defaults sends every field; a single control sends one. */
  onChange(patch: Partial<SettingsDraft>): void
  /** `nav`: the sidebar was clicked (the page has scrolled there and moved
   *  focus to the heading). `scroll`: the user scrolled into the section, or
   *  a window resize changed which section the pane shows (v1). */
  onSection(id: SettingsSection, via: 'nav' | 'scroll'): void
  onSave(): void
  /** Revert to saved. */
  onRevert(): void
  /** Where Back leads, as its label names it ("Library", or a folder's name
   *  when Settings was opened from a folder or Focus). */
  backTo: string
  /** Back in the title bar and Cancel: the app opens the leave prompt when
   *  the draft has unsaved changes, else goes back to where Settings was
   *  opened from. */
  onBack(): void
  onHelp(): void
  /** Load again after a failed load. */
  onReload(): void
  /** The quality model's Retry (v1's retry from the quality status). */
  onRetryQuality(): void
  /** Another model's Retry (CLIP). */
  onRetryModel(kind: ModelStatus['kind']): void
  /** The leave prompt's answer: true discards the edits and leaves. */
  onConfirmLeave(leave: boolean): void
  onUndoToast(id: string): void
  onDismissToast(id: string): void
}
