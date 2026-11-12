// Mock data for the Settings states (500–520). Values and messages are
// invented.
import { DEFAULTS } from '../../src/design/screens/settings/draft'
import type { SettingsBody, ToastItem } from '../../src/design/types'

/** Saved values that differ from the defaults, so Restore defaults is live. */
export const SAVED: SettingsBody = {
  ...DEFAULTS,
  bucket_size: 900,
  similarity: 0.88,
  picks_dir: 'selects',
  advance_on_action: true,
}

export const SAVED_NO_VISUAL: SettingsBody = { ...SAVED, visual_grouping: false }

export const ERR_LOAD = 'The settings file could not be read: permission denied'

export const TOAST_SAVE_FAILED: ToastItem = {
  id: 'save-failed',
  tone: 'reject',
  title: 'Save failed',
  body: 'The settings file could not be written: the disk is full.',
}
