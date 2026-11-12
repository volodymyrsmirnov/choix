// Settings validation (v1 Settings.tsx, moved out of the component), and the
// single source of the copy for every field error: the app shows FIELD_COPY
// for the reason code Go returns too (engine plan C13), so the words a user
// sees for a refused value never depend on who refused it.
import type { SettingsDraft, SettingsErrors } from './props'

/** Go refuses a bucket under one minute (`min`). */
export const MIN_BUCKET_SEC = 60

export const FIELD_COPY = {
  bucket_size: {
    nan: 'Enter the gap in minutes, e.g. 10 or 2.5.',
    min: 'Must be at least 1 minute.',
  },
  similarity: {
    range: 'Must be more than 0 and at most 1.',
  },
  picks_dir: {
    empty: 'Enter a folder for your picks.',
    absolute: 'Use a folder name inside the scanned folder, not an absolute path.',
    traversal: 'Must stay inside the scanned folder — ".." would leave it.',
    reserved: 'Use a folder inside the scanned folder, not the folder itself or .choix.',
    has_originals: 'This folder holds photos Choix scanned as originals. Choose another one.',
    symlink: 'This folder is a symbolic link. Use a real folder inside the scanned folder.',
  },
} as const

export type PicksDirReason = keyof typeof FIELD_COPY.picks_dir

// The wire format stays in whole seconds; the form edits minutes. Two
// decimals are enough for every whole-second value to round-trip: the
// display error is at most 0.005 min = 0.3 s, which Math.round below
// absorbs (1000 s → "16.67" → 1000.2 → 1000). Number() drops trailing
// zeros so 600 s shows as "10", not "10.00".
export function secondsToMinutesText(sec: number): string {
  return String(Number((sec / 60).toFixed(2)))
}

/** Whole seconds, or null for anything that is not a usable number. */
export function minutesTextToSeconds(text: string): number | null {
  const t = text.trim()
  if (t === '') return null
  const min = Number(t)
  if (!Number.isFinite(min)) return null
  const sec = Math.round(min * 60)
  // Beyond 2^53 JSON can't carry the value exactly (and Go would refuse
  // anything past int64), so treat it as unparseable rather than sending it.
  return Number.isSafeInteger(sec) ? sec : null
}

/**
 * Mirrors Go's picks.ValidateDir, lexically: empty; absolute (2.0 is
 * relative-only: a sandboxed app can write only inside the folder it was
 * given); not local (resolving "." and ".." segments, as filepath.IsLocal);
 * the scanned folder itself or anything under `.choix`.
 */
export function picksDirReason(p: string): Exclude<PicksDirReason, 'has_originals' | 'symlink'> | null {
  if (p === '') return 'empty'
  if (p.startsWith('/')) return 'absolute'
  const stack: string[] = []
  for (const seg of p.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..' && stack.length > 0 && stack[stack.length - 1] !== '..') stack.pop()
    else stack.push(seg)
  }
  if (stack[0] === '..') return 'traversal'
  if (stack.length === 0 || stack[0].toLowerCase() === '.choix') return 'reserved'
  return null
}

/** Every field error of a draft; `{}` when it can be saved. */
export function validateSettings(draft: SettingsDraft): SettingsErrors {
  const errors: SettingsErrors = {}
  const sec = minutesTextToSeconds(draft.bucket_size)
  if (sec === null) errors.bucket_size = FIELD_COPY.bucket_size.nan
  else if (sec < MIN_BUCKET_SEC) errors.bucket_size = FIELD_COPY.bucket_size.min
  if (!(draft.similarity > 0 && draft.similarity <= 1)) errors.similarity = FIELD_COPY.similarity.range
  const picks = picksDirReason(draft.picks_dir)
  if (picks) errors.picks_dir = FIELD_COPY.picks_dir[picks]
  return errors
}
