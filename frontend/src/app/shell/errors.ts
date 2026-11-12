// What a failed binding call means. Go's services return an *apperr.Error,
// which Wails marshals into the rejected call's `cause` as
// `{code, message, field?, reason?}`; no TypeScript model is generated for
// it, so the shape is declared here by hand.
//
// For a refused setting the words come from the design (FIELD_COPY, the
// copy validateSettings shows), never from Go: the user reads the same
// sentence whoever refused the value. A reason the design has no copy for
// (`not_dir`, `unknown`, …) falls back to Go's message.
import { FIELD_COPY } from '../../design/screens/settings/validate'

export interface AppError {
  /**
   * not_found | not_open | offline | access_lost | read_only | invalid |
   * busy (not yet: the previous session is still closing, or native dialogs
   * are not ready; shown like any error) | unavailable (the app is quitting:
   * shown nowhere) | internal
   */
  code: string
  /** The refused input (a settings key such as `picks_dir`), for `invalid`. */
  field?: string
  /** Why it was refused, as a code the design has copy for. */
  reason?: string
  message: string
}

function fromObject(v: unknown): AppError | null {
  if (typeof v === 'string') {
    try {
      return fromObject(JSON.parse(v))
    } catch {
      return null
    }
  }
  if (typeof v !== 'object' || v === null) return null
  const o = v as Record<string, unknown>
  if (typeof o.code !== 'string') return null
  const out: AppError = { code: o.code, message: typeof o.message === 'string' ? o.message : o.code }
  if (typeof o.field === 'string' && o.field !== '') out.field = o.field
  if (typeof o.reason === 'string' && o.reason !== '') out.reason = o.reason
  return out
}

/** The app error a binding call rejected with; anything else is `internal` with its text. */
export function appError(e: unknown): AppError {
  if (typeof e === 'object' && e !== null && 'cause' in e) {
    const fromCause = fromObject((e as { cause: unknown }).cause)
    if (fromCause) return fromCause
  }
  const bare = typeof e === 'object' ? fromObject(e) : null
  if (bare) return bare
  return { code: 'internal', message: e instanceof Error ? e.message : String(e) }
}

/** The design's copy for a refused field, or null when it has none for that reason. */
export function fieldCopy(field: string | undefined, reason: string | undefined): string | null {
  if (field === undefined || reason === undefined) return null
  if (!Object.hasOwn(FIELD_COPY, field)) return null
  const copy = FIELD_COPY[field as keyof typeof FIELD_COPY] as Record<string, string>
  return Object.hasOwn(copy, reason) ? copy[reason] : null
}

/** What to show for an error: the design's copy for a refused field, else Go's message. */
export function errorText(e: AppError): string {
  return fieldCopy(e.field, e.reason) ?? e.message
}

/** The app is quitting (`unavailable`): a failure then is shown nowhere. Every other code, `busy` included, is shown. */
export const isQuitting = (e: AppError): boolean => e.code === 'unavailable'
