import { describe, expect, test } from 'bun:test'
import { noteText } from '../../components'
import { menuKey, shortcutColumns, shortcutGroups } from './shortcuts'

const allKeys = () =>
  Object.values(shortcutGroups())
    .flatMap((g) => g.rows)
    .flatMap((r) => r.keys.flat())

describe('shortcut sheet content', () => {
  test('has the five sections of 2.0', () => {
    expect(Object.values(shortcutGroups()).map((g) => g.title)).toEqual(['Library', 'Folder', 'Focus', 'Compare', 'Everywhere'])
  })

  test('lists every modifier shortcut spec §4.4 adds, and ⌘K for search', () => {
    const keys = allKeys()
    for (const k of ['⌘O', '⌘P', '⇧⌘R', '⌘R', '⌘⌫', '⌘[', '⌘F', '⌘,', '⌘K']) expect(keys).toContain(k)
  })

  test('the Library lists ⌘F for its search, and Open Picks is ⌘P', () => {
    const lib = shortcutGroups().library.rows
    expect(lib.find((r) => r.label === 'Search folders')?.keys).toEqual([['⌘F']])
    expect(lib.find((r) => r.label === 'Open its picks folder')?.keys).toEqual([['⌘P']])
    expect(allKeys()).not.toContain('⇧⌘P')
  })

  test('keeps every v1 single key', () => {
    const keys = allKeys()
    for (const k of ['↑', '↓', 'J', 'K', 'F', '↩', '←', '→', 'Space', 'X', 'C', 'Z', '+', '−', '0', 'I', '⇧↩', 'Esc', '1–4', '?']) {
      expect(keys).toContain(k)
    }
  })

  test('keys are macOS glyphs, never Ctrl or Cmd spelled out', () => {
    for (const k of allKeys()) expect(k).not.toMatch(/Ctrl|Cmd|Alt|Shift|Option/)
  })

  test('modifier shortcuts come from menus.json', () => {
    expect(menuKey('add-folder')).toBe('⌘O')
    expect(menuKey('remove-folder')).toBe('⌘⌫')
    expect(menuKey('back')).toBe('⌘[')
    expect(() => menuKey('nope')).toThrow(/nope/)
  })

  test('rows are unique within a section', () => {
    for (const g of Object.values(shortcutGroups())) {
      const labels = g.rows.map((r) => r.label)
      expect(new Set(labels).size).toBe(labels.length)
    }
  })
})

describe('section order', () => {
  test("the current screen's column comes first, its section on top", () => {
    expect(shortcutColumns('library')).toEqual([
      ['library', 'folder'],
      ['focus', 'compare'],
    ])
    expect(shortcutColumns('folder')).toEqual([
      ['folder', 'library'],
      ['focus', 'compare'],
    ])
    expect(shortcutColumns('focus')).toEqual([
      ['focus', 'compare'],
      ['library', 'folder'],
    ])
    expect(shortcutColumns('compare')).toEqual([
      ['compare', 'focus'],
      ['library', 'folder'],
    ])
  })

  test('Settings keeps the natural order', () => {
    expect(shortcutColumns('settings')).toEqual([
      ['library', 'folder'],
      ['focus', 'compare'],
    ])
  })
})

describe('row notes', () => {
  test('a short note never breaks and stays with the label', () => {
    expect(noteText('Up to 4')).toBe(' — Up to 4')
  })

  test('a long note may wrap, but not before its last two words', () => {
    expect(noteText('Un-zooms or leaves compare first')).toBe(' — Un-zooms or leaves compare first')
  })
})
