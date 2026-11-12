// Native replica flows. Each menu-bar state opens with its menu showing: the
// arrow keys move through its enabled items, Escape closes it, a click on
// its title opens it again and Escape closes it again; on s701 every menu is
// opened from its title and closed with Escape. The row's context menu
// closes with Escape and reopens from the row's ⋯ button. The Remove alert
// closes with Cancel, then (reopened from the row's menu) with Remove. The
// items that open another screen (Settings…, Keyboard Shortcuts, Remove
// Folder…, Back, the Focus commands) land on that screen's real state.
import { expect, type Page } from '@playwright/test'
import type { ProtoHandle } from '../flows.pw'
import { landOn } from './landing'

const MENU_STATES: [n: number, title: string][] = [
  [700, 'Choix'],
  [701, 'File'],
  [702, 'File'],
  [703, 'Edit'],
  [704, 'Photo'],
  [705, 'Photo'],
  [706, 'View'],
  [707, 'Window'],
  [708, 'Help'],
  [709, 'File'],
]


/** Menu items that lead to another state: [state, menu title, item, where it lands]. */
const LINKS: [n: number, title: string, item: string, to: number][] = [
  [700, 'Choix', 'Settings…', 500],
  [708, 'Help', 'Keyboard Shortcuts', 600],
  [701, 'File', 'Remove Folder from Library…', 720],
  [706, 'View', 'Back', 203],
  [706, 'View', 'Compare', 400],
  [706, 'View', 'Info Panel', 304],
  [706, 'View', 'Pixel-Peep', 307],
  [705, 'Photo', 'Previous Cluster', 301],
]

const activeLabel = (page: Page) => page.evaluate(() => document.activeElement?.textContent ?? '')

export default async function native(page: Page, proto: ProtoHandle): Promise<void> {
  const bar = page.getByRole('navigation', { name: 'Menu bar' })
  const menu = page.getByRole('menu')

  for (const [n, title] of MENU_STATES) {
    await proto.open(n)
    await expect(menu).toHaveCount(1)
    await expect(menu).toHaveAttribute('aria-label', title)

    const enabled = menu.locator('[role="menuitem"]:not(:disabled)')
    const count = await enabled.count()
    if (count > 0) {
      // Tab from the open menu's title lands on its first enabled item.
      await bar.getByRole('button', { name: title, exact: true }).focus()
      await page.keyboard.press('Tab')
      await expect(enabled.first()).toBeFocused()
      const seen = [await activeLabel(page)]
      for (let i = 1; i < count; i++) {
        await page.keyboard.press('ArrowDown')
        seen.push(await activeLabel(page))
      }
      expect(new Set(seen).size).toBe(count)
      await page.keyboard.press('ArrowDown')
      await expect(enabled.first()).toBeFocused()
      await page.keyboard.press('ArrowUp')
      await expect(enabled.last()).toBeFocused()
    }
    await page.keyboard.press('Escape')
    await expect(menu).toHaveCount(0)

    await bar.getByRole('button', { name: title, exact: true }).click()
    await expect(menu).toHaveAttribute('aria-label', title)
    await page.keyboard.press('Escape')
    await expect(menu).toHaveCount(0)
  }

  // Every menu of the bar, opened from its title and closed with Escape.
  await proto.open(701)
  await page.keyboard.press('Escape')
  const titles = bar.locator(':scope > div > button')
  for (let i = 0; i < (await titles.count()); i++) {
    await titles.nth(i).click()
    await expect(menu).toHaveCount(1)
    await page.keyboard.press('Escape')
    await expect(menu).toHaveCount(0)
  }

  // Context menus: Escape closes; the ⋯ button reopens; Escape again.
  for (const n of [710, 711, 712]) {
    await proto.open(n)
    const rowMenu = page.getByRole('menu', { name: 'Actions for Lisbon' })
    await expect(rowMenu).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(rowMenu).toHaveCount(0)
    await page.getByRole('button', { name: 'More actions for Lisbon' }).click()
    await expect(rowMenu).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(rowMenu).toHaveCount(0)

    // Opened by a right-click on the row; the arrow keys walk its items and
    // wrap. (An offline row is aria-disabled for opening, not for its menu,
    // so the click skips Playwright's enabled wait.)
    await page.locator('[data-folder="lisbon"] > button').click({ button: 'right', force: true })
    await expect(rowMenu).toBeVisible()
    const items = rowMenu.getByRole('menuitem')
    const count = await items.count()
    await items.first().focus()
    const seen = [await activeLabel(page)]
    for (let i = 1; i < count; i++) {
      await page.keyboard.press('ArrowDown')
      seen.push(await activeLabel(page))
    }
    expect(new Set(seen).size).toBe(count)
    await page.keyboard.press('ArrowDown')
    await expect(items.first()).toBeFocused()
    await page.keyboard.press('ArrowUp')
    await expect(items.last()).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(rowMenu).toHaveCount(0)
  }

  // Help ▸ Keyboard Shortcuts over Focus opens the sheet for Focus, in place.
  await proto.open(706)
  await page.keyboard.press('Escape')
  await bar.getByRole('button', { name: 'Help', exact: true }).click()
  await page.getByRole('menu', { name: 'Help' }).getByRole('menuitem', { name: 'Keyboard Shortcuts' }).click()
  const sheet = page.getByRole('dialog', { name: 'Keyboard shortcuts' })
  await expect(sheet).toBeVisible()
  await expect(sheet.locator('[data-shortcuts-group="focus"]')).toContainText('this screen')
  await page.keyboard.press('Escape')
  await expect(sheet).toHaveCount(0)

  // The alert: Cancel keeps the folder; reopened from the row's menu, Remove forgets it.
  await proto.open(720)
  const alert = page.getByRole('alertdialog', { name: 'Remove “Dolomites” from Choix?' })
  const row = page.locator('[data-folder="dolomites"]')
  await expect(alert).toBeVisible()
  await alert.getByRole('button', { name: 'Cancel' }).click()
  await expect(alert).toHaveCount(0)
  await expect(row).toBeVisible()
  await page.getByRole('button', { name: 'More actions for Dolomites' }).click()
  await page.getByRole('menu', { name: 'Actions for Dolomites' }).getByRole('menuitem', { name: 'Remove from Library…' }).click()
  await expect(alert).toBeVisible()
  await alert.getByRole('button', { name: 'Remove' }).click()
  await expect(alert).toHaveCount(0)
  await expect(row).toHaveCount(0)

  // Menu items that open another screen land on its real state.
  for (const [n, title, item, to] of LINKS) {
    await proto.open(n)
    await page.getByRole('menu', { name: title }).getByRole('menuitem', { name: item, exact: true }).click()
    await landOn(page, to)
  }

  // Over Focus on the folder's second cluster, Next Cluster leads to the
  // third, which has no Focus state: the menu closes and nothing else happens.
  await proto.open(705)
  await page.getByRole('menu', { name: 'Photo' }).getByRole('menuitem', { name: 'Next Cluster', exact: true }).click()
  await expect(page.getByRole('menu')).toHaveCount(0)
  await expect(page).toHaveURL(/#s705$/)
}
