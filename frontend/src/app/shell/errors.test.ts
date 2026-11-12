import { describe, expect, test } from 'bun:test'
import { FIELD_COPY } from '../../design/screens/settings/validate'
import { appError, errorText, fieldCopy, isQuitting } from './errors'

/** What a binding call rejects with: Wails puts the marshalled app error in `cause`. */
const rejected = (cause: unknown) => Object.assign(new Error('call failed'), { cause })

describe('errors', () => {
  test('maps picks_dir/absolute to the design copy', () => {
    const e = appError(
      rejected({ code: 'invalid', field: 'picks_dir', reason: 'absolute', message: 'picks dir /x is absolute' }),
    )
    expect(e).toEqual({ code: 'invalid', field: 'picks_dir', reason: 'absolute', message: 'picks dir /x is absolute' })
    expect(fieldCopy('picks_dir', 'absolute')).toBe(FIELD_COPY.picks_dir.absolute)
    expect(errorText(e)).toBe(FIELD_COPY.picks_dir.absolute)
    // Every reason the brief lists has its design copy.
    for (const [field, reason] of [
      ['picks_dir', 'empty'],
      ['picks_dir', 'traversal'],
      ['picks_dir', 'reserved'],
      ['picks_dir', 'has_originals'],
      ['picks_dir', 'symlink'],
      ['bucket_size', 'min'],
      ['similarity', 'range'],
    ] as const) {
      expect(fieldCopy(field, reason)).toBe((FIELD_COPY[field] as Record<string, string>)[reason])
    }
  })

  test('unknown reason falls back to the message', () => {
    const notDir = appError(rejected({ code: 'invalid', field: 'picks_dir', reason: 'not_dir', message: 'picks is a file' }))
    expect(fieldCopy('picks_dir', 'not_dir')).toBeNull()
    expect(errorText(notDir)).toBe('picks is a file')
    const unknownScreen = appError(rejected({ code: 'invalid', field: 'screen', reason: 'unknown', message: 'bad screen' }))
    expect(errorText(unknownScreen)).toBe('bad screen')
    // Not a key of the copy table, even though it is a property of every object.
    expect(fieldCopy('picks_dir', 'toString')).toBeNull()
  })

  test('reads a JSON cause, a bare app error and anything else', () => {
    expect(appError(rejected(JSON.stringify({ code: 'offline', message: 'the drive is gone' })))).toEqual({
      code: 'offline',
      message: 'the drive is gone',
    })
    expect(appError({ code: 'read_only', message: 'read-only volume' })).toEqual({
      code: 'read_only',
      message: 'read-only volume',
    })
    expect(appError(new Error('network down'))).toEqual({ code: 'internal', message: 'network down' })
    expect(appError('plain')).toEqual({ code: 'internal', message: 'plain' })
  })

  test('busy is not treated as quitting', () => {
    const busy = appError(rejected({ code: 'busy', message: 'The previous folder is still closing' }))
    expect(busy).toEqual({ code: 'busy', message: 'The previous folder is still closing' })
    expect(isQuitting(busy)).toBe(false)
    // Shown like any other error: Go's message.
    expect(errorText(busy)).toBe('The previous folder is still closing')
    // Only `unavailable` means the app is quitting.
    expect(isQuitting(appError(rejected({ code: 'unavailable', message: 'quitting' })))).toBe(true)
    expect(isQuitting(appError(rejected({ code: 'offline', message: 'the drive is gone' })))).toBe(false)
  })
})
