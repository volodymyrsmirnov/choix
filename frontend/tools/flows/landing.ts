// Not a group flow (flows.pw.ts loads tools/flows/<group>.ts only): the step
// a flow takes after a click leads into ANOTHER group's state.
//
// The flow-coverage rule counts every overlay id that registers while a flow
// runs, so landing on a real Folder or Focus page obliges the visiting flow
// to open and close what that page registers. landOn() checks where the
// click landed, that it is a real state, and then operates those overlays by
// user actions: the Folder page's camera and sort menus, the Library
// toolbar's Show and Sort menus, Focus's Info panel, the shortcut sheet and
// the Remove alert. It finds them in the page, so it
// follows the screens if they change.
import { expect, type Page } from '@playwright/test'

/**
 * Opens and closes (Escape) every filter menu the page shows: the Folder
 * page's camera and sort menus, the Library toolbar's Show and Sort menus.
 * A flow that only opens a state drawing one of those pages (not landing on
 * it by a click) calls it too, on a state where the menus are not inert.
 */
export async function operateMenus(page: Page): Promise<void> {
  for (const [testId, menu] of [
    ['device-filter', 'Camera'],
    ['sort-toggle', 'Sort clusters'],
    ['library-show', 'Show'],
    ['library-order', 'Sort folders'],
  ] as const) {
    const button = page.getByTestId(testId)
    if ((await button.count()) === 0) continue
    await button.click()
    await expect(page.getByRole('menu', { name: menu })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menu', { name: menu })).toHaveCount(0)
  }
}

export async function landOn(page: Page, n: number): Promise<void> {
  await expect(page).toHaveURL(new RegExp(`#s${n}$`))
  await expect(page.locator('[data-proto-missing]')).toHaveCount(0)

  const info = page.locator('[data-overlay="focus-info"]')
  const infoToggle = page.getByRole('button', { name: /^Info/ })
  if ((await info.count()) === 0 && (await infoToggle.count()) > 0) {
    await infoToggle.click()
    await expect(info).toBeVisible()
  }
  if ((await info.count()) > 0) {
    await page.keyboard.press('Escape')
    await expect(info).toHaveCount(0)
  }

  const sheet = page.getByRole('dialog', { name: 'Keyboard shortcuts' })
  if ((await sheet.count()) > 0) {
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
  }

  const alert = page.getByRole('alertdialog')
  if ((await alert.count()) > 0) {
    await alert.getByRole('button', { name: 'Cancel' }).click()
    await expect(alert).toHaveCount(0)
  }

  // Last: a sheet or an alert over the page leaves it inert until closed.
  await operateMenus(page)
}
