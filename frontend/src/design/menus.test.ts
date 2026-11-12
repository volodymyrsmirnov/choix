import { describe, expect, test } from 'bun:test'
import { acceleratorLabel } from './format'
import { ROLE_ITEMS } from './menuRoles'
import { MENU_MANIFEST } from './menus'
import type { MenuDef, MenuItemDef, ScreenId } from './types'

const SCREENS: ScreenId[] = ['library', 'folder', 'focus', 'compare', 'settings']

const m = MENU_MANIFEST

type Item = Extract<MenuItemDef, { id: string }>
const isItem = (x: MenuItemDef): x is Item => 'id' in x

const barItems = (): Item[] => m.menus.flatMap((menu) => menu.items.filter(isItem))

describe('menus.json', () => {
  test('has the seven menus of spec §4.4, in order', () => {
    expect(m.menus.map((x) => x.title)).toEqual(['Choix', 'File', 'Edit', 'Photo', 'View', 'Window', 'Help'])
  })

  test('menu ids are unique', () => {
    const ids = m.menus.map((x) => x.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  test('item ids are unique across the menu bar and within each context menu', () => {
    const ids = barItems().map((x) => x.id)
    expect(new Set(ids).size).toBe(ids.length)
    for (const items of Object.values(m.context)) {
      const cids = items.filter(isItem).map((x) => x.id)
      expect(new Set(cids).size).toBe(cids.length)
    }
  })

  test('accelerators are unique', () => {
    const accs = [...barItems(), ...Object.values(m.context).flat().filter(isItem)]
      .map((x) => x.accelerator)
      .filter((a): a is string => a !== undefined)
    expect(accs.length).toBeGreaterThan(0)
    expect(new Set(accs).size).toBe(accs.length)
  })

  test('shortcuts as macOS shows them are unique, role shortcuts included', () => {
    const all = [...m.menus.flatMap((x) => x.items), ...Object.values(m.context).flat()]
    const shown: string[] = []
    for (const e of all) {
      if ('id' in e && e.accelerator) shown.push(acceleratorLabel(e.accelerator))
      if ('role' in e) {
        const r = ROLE_ITEMS[e.role]
        expect(r).toBeDefined()
        if (r?.display) shown.push(r.display)
      }
    }
    expect(shown).toContain('⌘W')
    expect(shown).toContain('⌃⌘F')
    expect(new Set(shown).size).toBe(shown.length)
  })

  test('single-key shortcuts are unique among the items enabled on each screen', () => {
    for (const screen of SCREENS) {
      const keys = barItems()
        .filter((x) => x.display !== undefined && x.enabledIn.includes(screen))
        .map((x) => x.display)
      expect(new Set(keys).size).toBe(keys.length)
    }
  })

  test('File has Add Folder ⌘O and Open Picks Folder ⌘P (the owner: picks open with Cmd+P)', () => {
    const file = m.menus.find((x) => x.id === 'file')
    expect(file).toBeDefined()
    const items = (file?.items ?? []).filter(isItem)
    expect(items.find((x) => x.id === 'add-folder')?.accelerator).toBe('CmdOrCtrl+O')
    expect(items.find((x) => x.id === 'open-picks')?.accelerator).toBe('CmdOrCtrl+P')
    // ⌘P is Open Picks alone: no other item, in the bar or a context menu, takes it.
    const all = [...barItems(), ...Object.values(m.context).flat().filter(isItem)]
    expect(all.filter((x) => x.accelerator === 'CmdOrCtrl+P').map((x) => x.id)).toEqual(['open-picks'])
  })

  test('Find (⌘F) works on the Library too: it focuses the Library search', () => {
    const find = barItems().find((x) => x.id === 'find')
    expect(find?.accelerator).toBe('CmdOrCtrl+F')
    expect(find?.enabledIn).toEqual(['library', 'folder'])
  })

  test('every enabledIn value is a ScreenId', () => {
    const all = [...barItems(), ...Object.values(m.context).flat().filter(isItem)]
    for (const item of all) {
      expect(item.enabledIn.length).toBeGreaterThan(0)
      for (const s of item.enabledIn) expect(SCREENS).toContain(s)
    }
  })

  test('every entry is an item, a separator or a role', () => {
    const all = [...m.menus.flatMap((x) => x.items), ...Object.values(m.context).flat()]
    for (const e of all) {
      const kinds = ['id' in e, 'separator' in e, 'role' in e].filter(Boolean).length
      expect(kinds).toBe(1)
    }
  })

  test('every role item carries a non-empty label in the manifest', () => {
    const roles = [...m.menus.flatMap((x) => x.items), ...Object.values(m.context).flat()].filter(
      (e): e is Extract<MenuItemDef, { role: string }> => 'role' in e,
    )
    expect(roles.length).toBeGreaterThan(0)
    for (const r of roles) {
      expect(typeof r.label).toBe('string')
      expect(r.label.trim().length).toBeGreaterThan(0)
    }
    const byRole = new Map(roles.map((r) => [r.role, r.label]))
    expect(byRole.get('toggleFullscreen')).toBe('Enter Full Screen')
    expect(byRole.get('closeWindow')).toBe('Close Window')
  })

  test('the role table carries no labels; the manifest does', () => {
    for (const r of Object.values(ROLE_ITEMS)) expect('label' in r).toBe(false)
  })

  test('the folder-row context menu has the five row actions of spec §4.1', () => {
    const labels = (m.context['folder-row'] ?? []).filter(isItem).map((x) => x.label)
    expect(labels).toEqual(['Open', 'Open Picks Folder', 'Reveal in Finder', 'Rescan', 'Remove from Library…'])
  })
})
