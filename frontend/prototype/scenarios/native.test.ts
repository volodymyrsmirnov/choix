// The native replica states against the manifests: a state shows exactly what
// menus.json / dialogs.json say the native surface will hold. The states are
// rendered to markup and read back, so the test checks what is drawn.
import { describe, expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { visibleEntries } from '../../src/design/components'
import { dialog, type DialogDef } from '../../src/design/dialogs'
import { ROLE_ITEMS } from '../../src/design/menuRoles'
import { FOLDER_ROW_MENU, folderFlags, menuById } from '../../src/design/menus'
import type { MenuDef, MenuItemDef, ScreenId } from '../../src/design/types'
import { NOW } from '../clock'
import type { ScenarioCtx } from '../registry'
import { designRefProblem } from './designRef'
import { ALERT_FOLDER, CONTEXT_STATES, MENU_STATES, scenarios } from './native'

// Oracles: the manifest rules, written out independently of the replicas.

/**
 * The ids of a menu's enabled entries (role items by role name) on `screen`:
 * an item is enabled when its `enabledIn` names the screen and, if it
 * `needsSelection`, there is a target folder; a role item unless the role
 * table disables it.
 */
function enabledIds(menu: MenuDef, screen: ScreenId, hasSelection: boolean): string[] {
  const out: string[] = []
  for (const e of menu.items) {
    if ('role' in e) {
      if (ROLE_ITEMS[e.role]?.enabled ?? true) out.push(e.role)
    } else if ('id' in e && e.enabledIn.includes(screen) && (!e.needsSelection || hasSelection)) {
      out.push(e.id)
    }
  }
  return out
}

/** The labels a context menu shows for a folder with these flags. */
function contextLabels(items: MenuItemDef[], flags: string[]): string[] {
  return visibleEntries(items, flags).flatMap((e) => ('label' in e ? [e.label] : []))
}

/** The "Remove from Library" alert for a folder, from dialogs.json. */
function removeAlert(name: string): DialogDef {
  return dialog('remove-folder', { name })
}

const ctx = (): ScenarioCtx => ({
  size: 'default',
  now: NOW,
  store: { state: {}, set() {}, go() {}, toast() {} },
})
const render = (n: number) => renderToStaticMarkup(scenarios.find((s) => s.n === n)!.render(ctx()))

const decode = (s: string) => s.replace(/&amp;/g, '&').replace(/&quot;/g, '"').replace(/&#x27;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>')

/** The menu items drawn in the one open menu: label and enabled. */
function drawnItems(html: string): { label: string; enabled: boolean }[] {
  expect(html.split('role="menu"').length - 1).toBe(1)
  return [...html.slice(html.indexOf('role="menu"')).matchAll(/<button([^>]*)role="menuitem"([^>]*)><span[^>]*>([^<]*)<\/span>/g)].map((m) => ({
    label: decode(m[3]!),
    enabled: !/\bdisabled=""/.test(m[1]! + m[2]!),
  }))
}

/** A menu entry's id (role name for a role item) by its label. */
function idOf(items: MenuItemDef[], label: string): string {
  const e = items.find((x) => 'label' in x && x.label === label)
  if (!e) throw new Error(`no item labelled ${label}`)
  return 'id' in e ? e.id : 'role' in e ? e.role : ''
}

test('native holds exactly the inventory states 700–712 and 720', () => {
  const want = [700, 701, 702, 703, 704, 705, 706, 707, 708, 709, 710, 711, 712, 720]
  expect(scenarios.map((s) => s.n).sort((a, b) => a - b)).toEqual(want)
  expect(scenarios.every((s) => s.group === 'native')).toBe(true)
  for (const s of scenarios) expect(designRefProblem(s.design)).toBeNull()
})

describe('menu-bar states', () => {
  test('every menu-bar menu is open in at least one state', () => {
    expect([...new Set(MENU_STATES.map((s) => s.menu))].sort()).toEqual(['app', 'edit', 'file', 'help', 'photo', 'view', 'window'])
  })

  test('each state draws its menu with the manifest’s items, in order, enabled by the manifest’s rules', () => {
    for (const s of MENU_STATES) {
      const menu = menuById(s.menu)
      const drawn = drawnItems(render(s.n))
      const labels = menu.items.flatMap((e) => ('label' in e ? [e.label] : []))
      expect(drawn.map((d) => d.label)).toEqual(labels)
      const enabled = drawn.filter((d) => d.enabled).map((d) => idOf(menu.items, d.label))
      expect(enabled).toEqual(enabledIds(menu, s.screen, s.hasSelection))
    }
  })

  // What the manifest rules give, written out: the owner approves these sets.
  const WANT: Record<number, string[]> = {
    701: ['add-folder', 'open-picks', 'reveal-folder', 'rescan-folder', 'remove-folder', 'closeWindow'],
    702: ['add-folder', 'open-picks', 'reveal-folder', 'rescan-folder', 'closeWindow'],
    704: [],
    705: ['pick', 'reject', 'next-photo', 'prev-photo', 'next-cluster', 'prev-cluster'],
    709: ['add-folder', 'closeWindow'],
  }
  for (const [n, want] of Object.entries(WANT)) {
    test(`s${n}: the enabled items are ${want.length ? want.join(', ') : 'none'}`, () => {
      const s = MENU_STATES.find((x) => x.n === Number(n))!
      const menu = menuById(s.menu)
      const drawn = drawnItems(render(s.n))
      expect(drawn.filter((d) => d.enabled).map((d) => idOf(menu.items, d.label))).toEqual(want)
      expect(enabledIds(menu, s.screen, s.hasSelection)).toEqual(want)
    })
  }

  test('s701 and s709 differ only in the items that need a selected folder', () => {
    const file = menuById('file')
    const a = enabledIds(file, 'library', true)
    const b = enabledIds(file, 'library', false)
    const needs = file.items.flatMap((e) => ('id' in e && e.needsSelection ? [e.id] : []))
    expect(a.filter((id) => !b.includes(id)).sort()).toEqual(needs.filter((id) => a.includes(id)).sort())
  })

  test('role items follow the role table', () => {
    const edit = menuById('edit')
    const ids = enabledIds(edit, 'folder', true)
    for (const role of ['cut', 'copy', 'paste']) expect(ROLE_ITEMS[role]?.enabled).toBe(false)
    expect(ids).not.toContain('cut')
    expect(ids).toContain('selectAll')
  })
})

describe('context-menu states', () => {
  test('710, 711 and 712 are a ready, an offline and a scanning folder', () => {
    expect(CONTEXT_STATES.map((s) => [s.n, s.folder.state])).toEqual([
      [710, 'ready'],
      [711, 'offline'],
      [712, 'scanning'],
    ])
  })

  test('each draws the folder-row menu minus what its flags hide', () => {
    for (const s of CONTEXT_STATES) {
      const drawn = drawnItems(render(s.n))
      expect(drawn.map((d) => d.label)).toEqual(contextLabels(FOLDER_ROW_MENU, folderFlags(s.folder.state)))
      expect(drawn.every((d) => d.enabled)).toBe(true)
    }
  })

  test('the variants differ exactly by the items their flag hides', () => {
    const [ready, offline, scanning] = CONTEXT_STATES.map((s) => drawnItems(render(s.n)).map((d) => d.label))
    const hiddenBy = (flag: string) =>
      FOLDER_ROW_MENU.flatMap((e) => ('id' in e && (e.hiddenWhen ?? []).includes(flag) ? [e.label] : []))
    expect(ready).toEqual(FOLDER_ROW_MENU.flatMap((e) => ('label' in e ? [e.label] : [])))
    expect(ready!.filter((l) => !offline!.includes(l))).toEqual(hiddenBy('offline'))
    expect(ready!.filter((l) => !scanning!.includes(l))).toEqual(hiddenBy('scanning'))
    expect(offline).toEqual(['Remove from Library…'])
    expect(scanning).toEqual(['Open', 'Open Picks Folder', 'Reveal in Finder', 'Remove from Library…'])
  })
})

describe('the Remove alert (720)', () => {
  test('its title names the mock folder; its buttons are default and cancel', () => {
    const html = decode(render(720))
    const title = /<h2 id="remove-folder-title"[^>]*>([^<]*)<\/h2>/.exec(html)?.[1] ?? ''
    expect(title.replace(/ /g, ' ')).toContain(ALERT_FOLDER.name)
    expect(title.replace(/ /g, ' ')).toBe(removeAlert(ALERT_FOLDER.name).title)
    expect(removeAlert(ALERT_FOLDER.name).buttons.map((b) => b.role)).toEqual(['default', 'cancel'])
    const buttons = [...html.matchAll(/<div role="alertdialog"[\s\S]*$/g)][0]![0]
    expect([...buttons.matchAll(/<button[^>]*>([^<]*)<\/button>/g)].map((m) => m[1])).toEqual(['Cancel', 'Remove'])
    // NSAlert's two buttons sit side by side, the default (Remove) trailing.
    const message = /<[a-z]+ id="remove-folder-desc"[^>]*>([^<]*)</.exec(html)?.[1] ?? ''
    expect(message.replace(/\u00a0/g, ' ')).toBe(removeAlert(ALERT_FOLDER.name).message)
  })
})
