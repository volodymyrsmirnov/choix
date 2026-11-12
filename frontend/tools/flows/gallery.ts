// Gallery flows: every overlay the gallery registers is opened and closed.
// s803's open dropdown closes with Escape and reopens from its trigger;
// s806's open menu, context menus and alerts close with Escape, top first,
// then each menu-bar menu is opened from its title and closed with Escape.
import { expect, type Page } from '@playwright/test'
import type { ProtoHandle } from '../flows.pw'

export default async function gallery(page: Page, proto: ProtoHandle): Promise<void> {
  await proto.open(803)
  const dropdown = page.getByRole('menu', { name: 'Camera' })
  await expect(dropdown).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dropdown).toHaveCount(0)
  // The open dropdown's trigger, by its accessible name inside the cell its caption names.
  const trigger = page
    .getByText('Open, X-T5 hovered', { exact: true })
    .locator('..')
    .getByRole('button', { name: /^Camera:/ })
  await trigger.click()
  await expect(dropdown).toBeVisible()
  await dropdown.getByRole('menuitemradio', { name: 'X-T5' }).click()
  await expect(dropdown).toHaveCount(0)
  await expect(trigger).toContainText('X-T5')

  await proto.open(806)
  const open = () => page.locator('[role="menu"], [role="alertdialog"]')
  const count = await open().count()
  expect(count).toBe(7)
  for (let left = count; left > 0; left--) {
    await page.keyboard.press('Escape')
    await expect(open()).toHaveCount(left - 1)
  }
  const titles = page.locator('nav[aria-label="Menu bar"] > div > button')
  for (let i = 0; i < (await titles.count()); i++) {
    await titles.nth(i).click()
    await expect(page.getByRole('menu')).toHaveCount(1)
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menu')).toHaveCount(0)
  }
}
