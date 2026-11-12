// The Library's keyboard (libraryKeys.ts): from a cold start, with nothing
// selected and nothing focused, ↓ / ↑ choose a row and ↩ opens it.
import { describe, expect, test } from 'bun:test'
import { createLibraryKeyHandler, nextRow, type LibraryKeyDeps } from './libraryKeys'

describe('nextRow', () => {
  const ids = ['a', 'b', 'c']
  test('no rows: nothing', () => {
    expect(nextRow([], null, 1)).toBeNull()
    expect(nextRow([], 'a', -1)).toBeNull()
  })
  test('nothing selected (cold start): either arrow chooses the first row', () => {
    expect(nextRow(ids, null, 1)).toBe('a')
    expect(nextRow(ids, null, -1)).toBe('a')
  })
  test('a selection that left the list counts as none', () => {
    expect(nextRow(ids, 'gone', 1)).toBe('a')
  })
  test('↓ and ↑ move one row and stop at the ends', () => {
    expect(nextRow(ids, 'a', 1)).toBe('b')
    expect(nextRow(ids, 'c', 1)).toBe('c')
    expect(nextRow(ids, 'b', -1)).toBe('a')
    expect(nextRow(ids, 'a', -1)).toBe('a')
  })
})

interface FakeKey {
  key: string
  metaKey?: boolean
  ctrlKey?: boolean
  altKey?: boolean
  shiftKey?: boolean
  isComposing?: boolean
  defaultPrevented?: boolean
  target?: unknown
}

function setup(over: Partial<LibraryKeyDeps> = {}) {
  let selected: string | null = null
  const calls: string[] = []
  const deps: LibraryKeyDeps = {
    rows: () => ['a', 'b', 'c'],
    selected: () => selected,
    select: (id) => {
      selected = id
      calls.push(`select ${id}`)
    },
    reveal: (id) => calls.push(`reveal ${id}`),
    open: (id) => calls.push(`open ${id}`),
    canOpen: () => true,
    blocked: () => false,
    ...over,
  }
  const handler = createLibraryKeyHandler(deps)
  const press = (k: FakeKey) => {
    let prevented = false
    const e = {
      metaKey: false,
      ctrlKey: false,
      altKey: false,
      shiftKey: false,
      isComposing: false,
      defaultPrevented: false,
      target: { tagName: 'BODY' },
      ...k,
      preventDefault: () => {
        prevented = true
      },
    }
    handler(e as unknown as KeyboardEvent)
    return prevented
  }
  return { calls, press, set: (id: string | null) => (selected = id) }
}

describe('createLibraryKeyHandler', () => {
  test('cold start: ↓ selects and reveals the first row, ↩ opens it', () => {
    const s = setup()
    expect(s.press({ key: 'ArrowDown' })).toBe(true)
    expect(s.press({ key: 'ArrowDown' })).toBe(true)
    expect(s.press({ key: 'ArrowUp' })).toBe(true)
    expect(s.press({ key: 'Enter' })).toBe(true)
    expect(s.calls).toEqual(['select a', 'reveal a', 'select b', 'reveal b', 'select a', 'reveal a', 'open a'])
  })

  test('cold start: ↩ with nothing selected selects and opens the first row', () => {
    const s = setup()
    expect(s.press({ key: 'Enter' })).toBe(true)
    expect(s.calls).toEqual(['select a', 'open a'])
  })

  test('a selection that left the list counts as none: ↩ opens the first row', () => {
    const s = setup()
    s.set('gone')
    expect(s.press({ key: 'Enter' })).toBe(true)
    expect(s.calls).toEqual(['select a', 'open a'])
  })

  test('↩ with nothing selected and no rows does nothing', () => {
    const s = setup({ rows: () => [] })
    expect(s.press({ key: 'Enter' })).toBe(false)
    expect(s.calls).toEqual([])
  })

  test('↩ on a focused control is left to the control (the row button opens itself)', () => {
    const s = setup()
    s.set('b')
    expect(s.press({ key: 'Enter', target: { tagName: 'BUTTON' } })).toBe(false)
    expect(s.calls).toEqual([])
  })

  test('↩ on a folder that cannot be opened (offline) does nothing', () => {
    const s = setup({ canOpen: (id) => id !== 'b' })
    s.set('b')
    expect(s.press({ key: 'Enter' })).toBe(false)
    expect(s.calls).toEqual([])
  })

  test('keys with ⌘, ⌃ or ⌥, while composing, already handled, in a text field or while blocked are left alone', () => {
    const s = setup()
    expect(s.press({ key: 'ArrowDown', metaKey: true })).toBe(false)
    expect(s.press({ key: 'ArrowDown', ctrlKey: true })).toBe(false)
    expect(s.press({ key: 'ArrowDown', altKey: true })).toBe(false)
    expect(s.press({ key: 'ArrowDown', isComposing: true })).toBe(false)
    expect(s.press({ key: 'ArrowDown', defaultPrevented: true })).toBe(false)
    expect(s.press({ key: 'ArrowDown', target: { tagName: 'INPUT', type: 'text' } })).toBe(false)
    expect(setup({ blocked: () => true }).press({ key: 'ArrowDown' })).toBe(false)
    expect(s.calls).toEqual([])
  })

  test('no rows: the arrows do nothing', () => {
    const s = setup({ rows: () => [] })
    expect(s.press({ key: 'ArrowDown' })).toBe(false)
    expect(s.calls).toEqual([])
  })
})
