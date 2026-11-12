// Overlay flows: the shortcut sheet closed with Escape, reopened from the
// title bar's Keyboard shortcuts button, closed with its Close button and
// with a click on the backdrop; each toast kind dismissed; Undo on the pick
// toast swaps it for "Undone"; the three stacked toasts closed with Escape,
// newest first.
import { expect, type Page } from '@playwright/test'
import type { ProtoHandle } from '../flows.pw'
import { operateMenus } from './landing'

const dismiss = async (page: Page, id: string) => {
  const t = page.locator(`[data-overlay="toast:${id}"]`)
  await expect(t).toBeVisible()
  await t.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(t).toHaveCount(0)
}

export default async function overlays(page: Page, proto: ProtoHandle): Promise<void> {
  // 600: the sheet takes focus; Escape closes it.
  await proto.open(600)
  const sheet = page.getByRole('dialog', { name: 'Keyboard shortcuts' })
  await expect(sheet).toBeVisible()
  await expect(sheet).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(sheet).toHaveCount(0)
  // The Library under it: its toolbar menus.
  await operateMenus(page)

  // Reopened from its button; Close closes it and gives focus back.
  const help = page.getByRole('button', { name: 'Keyboard shortcuts' })
  await help.click()
  await expect(sheet).toBeVisible()
  await expect(sheet.locator('[data-shortcuts-group="library"]')).toContainText('this screen')
  await sheet.getByRole('button', { name: /^Close/ }).click()
  await expect(sheet).toHaveCount(0)
  await expect(help).toBeFocused()

  // A click on the backdrop closes it too.
  await help.click()
  await expect(sheet).toBeVisible()
  await page.mouse.click(8, 200)
  await expect(sheet).toHaveCount(0)

  // 601: Undo swaps "Picked" for "Undone"; that one is dismissed.
  await proto.open(601)
  const picked = page.locator('[data-overlay="toast:picked"]')
  await picked.getByRole('button', { name: /^Undo/ }).click()
  await expect(picked).toHaveCount(0)
  await expect(page.locator('[data-overlay="toast:undone"]')).toContainText('Undone')
  await dismiss(page, 'undone')

  // 602, 604, 605: each kind dismissed with its button.
  await proto.open(602)
  await dismiss(page, 'rescan-failed')
  await proto.open(604)
  await dismiss(page, 'unreadable')
  await proto.open(605)
  await dismiss(page, 'left-library')

  // 603: Escape closes the newest toast first.
  await proto.open(603)
  const toasts = page.locator('[data-testid="toast"]')
  await expect(toasts).toHaveCount(3)
  for (const [left, gone] of [
    [2, 'picked'],
    [1, 'left-library'],
    [0, 'unreadable'],
  ] as const) {
    await page.keyboard.press('Escape')
    await expect(toasts).toHaveCount(left)
    await expect(page.locator(`[data-overlay="toast:${gone}"]`)).toHaveCount(0)
  }
}
