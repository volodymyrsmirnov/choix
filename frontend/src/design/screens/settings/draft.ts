// What Settings derives from the saved values and the draft (v1
// Settings.tsx): the defaults, the unsaved markers, the save bar's note and
// the quality status line. Pure; the screen and the app's container share it.
import type { QualityStatus, SettingsBody } from '../../types'
import type { SettingsDraft, SettingsSection } from './props'
import { minutesTextToSeconds, secondsToMinutesText } from './validate'

/** Built-in defaults: config.Defaults() in Go. Restore defaults only fills
 *  the form with these; nothing is written until Save. */
export const DEFAULTS: SettingsBody = {
  bucket_size: 600,
  similarity: 0.92,
  picks_dir: 'picks',
  advance_on_action: false,
  hide_rejected_photos: false,
  cross_device_merging: false,
  quality_rating: true,
  visual_grouping: true,
}

/** The form for saved values: the bucket as minutes text. */
export function draftOf(s: SettingsBody): SettingsDraft {
  return { ...s, bucket_size: secondsToMinutesText(s.bucket_size) }
}

const same = (a: SettingsBody, d: SettingsDraft, keys: (keyof SettingsBody)[]) =>
  keys.every((k) => (k === 'bucket_size' ? minutesTextToSeconds(d.bucket_size) === a.bucket_size : d[k] === a[k]))

const SECTION_KEYS: Record<SettingsSection, (keyof SettingsBody)[]> = {
  grouping: ['visual_grouping', 'bucket_size', 'similarity', 'cross_device_merging'],
  behaviour: ['advance_on_action', 'hide_rejected_photos', 'quality_rating'],
  output: ['picks_dir'],
}

/**
 * Which sections hold unsaved edits. The bucket compares whole seconds, not
 * text, so "10", "10.0" and "10.004" all match a saved 600 s.
 */
export function dirtySections(saved: SettingsBody | null, d: SettingsDraft): Record<SettingsSection, boolean> {
  const dirty = (s: SettingsSection) => (saved ? !same(saved, d, SECTION_KEYS[s]) : false)
  return { grouping: dirty('grouping'), behaviour: dirty('behaviour'), output: dirty('output') }
}

export const atDefaults = (d: SettingsDraft): boolean => same(DEFAULTS, d, Object.keys(DEFAULTS) as (keyof SettingsBody)[])

// The fields whose change re-clusters (Go: session.ClusterParams), named as
// the save note says them.
const RECLUSTER: [keyof SettingsBody, string][] = [
  ['visual_grouping', 'visual grouping'],
  ['bucket_size', 'time bucket'],
  ['similarity', 'similarity'],
  ['cross_device_merging', 'cross-device merge'],
]

export function reclusterFields(saved: SettingsBody, d: SettingsDraft): string[] {
  return RECLUSTER.filter(([k]) => !same(saved, d, [k])).map(([, name]) => name)
}

/** The save bar's note: null while nothing is unsaved. */
export function saveNote(saved: SettingsBody | null, d: SettingsDraft, invalid: boolean): string | null {
  const dirty = dirtySections(saved, d)
  if (!saved || !(dirty.grouping || dirty.behaviour || dirty.output)) return null
  if (invalid) return 'Fix the highlighted field to save.'
  const fields = reclusterFields(saved, d)
  if (fields.length > 0) return `Saving will re-cluster your folders (${fields.join(', ')} changed). Picks are kept.`
  return 'Saving applies immediately — no re-cluster needed.'
}

/**
 * The line under the quality switch: what the worker is doing with the
 * *saved* setting (the switch may hold an unsaved edit). The model download
 * and its failure are shown by the model row above it, so they say nothing
 * here.
 */
export function qualityStatusText(q: QualityStatus | null): string {
  if (!q) return ''
  if (!q.enabled) return 'Off: no model download, no ratings.'
  switch (q.state) {
    case 'waiting':
      return 'Waiting for the scan to finish before rating photos.'
    case 'scoring':
      return `Rating photos: ${q.done.toLocaleString('en-US')} of ${q.total.toLocaleString('en-US')}.`
    case 'error':
      return `Rating stopped: ${q.error ?? 'unknown error'}. Restart Choix to try again.`
    case 'idle':
      return 'Ratings are up to date.'
    default:
      return ''
  }
}
