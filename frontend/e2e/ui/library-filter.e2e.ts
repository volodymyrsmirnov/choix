// The Library toolbar in the real app (round 2): the search narrows the
// rows and Esc restores them; Sort › Name gives one list without month
// headers; Show › Needs attention with no folder needing it shows the empty
// result, whose Show All Folders brings the rows back; ⌘F (the key, and
// Edit › Find from the native menu) focuses the search field; the arrows
// walk the rows the toolbar leaves on screen, in the order shown; and the
// toolbar is as the user left it after a visit to a folder.
import type { Page } from '@playwright/test'
import { expect, test } from './fixtures'
import { libraryRow, waitLibrary, waitReady } from './screens'

const field = (page: Page) => page.getByTestId('filter-input')
const rowButton = (page: Page, id: string) => libraryRow(page, id).locator(':scope > button')

/** The folder ids of the rows on screen, top to bottom. */
const shownIds = (page: Page) => page.locator('[data-folder]').evaluateAll((els) => els.map((el) => el.getAttribute('data-folder')))

async function choose(page: Page, menu: 'library-show' | 'library-order', item: string) {
  await page.getByTestId(menu).click()
  await page.getByRole('menuitemradio', { name: item }).click()
  await expect(page.getByRole('menu')).toHaveCount(0)
}

test('the Library toolbar: search, Sort, Show, ⌘F and the keys', async ({ app }) => {
  const { page, server, runtime } = app
  await waitLibrary(page)
  // Added in an order unlike their names (name order: home-beta, trip-alpha,
  // trip-gamma; added: trip-alpha, home-beta, trip-gamma), more than a second
  // apart: added_at is library.db's clock in whole seconds when the folder is
  // added. The search looks at the location too: "trip-" is in two names and
  // nowhere else.
  const nextSecond = () => new Promise((r) => setTimeout(r, 1_100))
  const alpha = await runtime.addFolderPath(server.generate('trip-alpha', 8))
  await nextSecond()
  const beta = await runtime.addFolderPath(server.generate('home-beta', 8))
  await nextSecond()
  const gamma = await runtime.addFolderPath(server.generate('trip-gamma', 8))
  expect(await runtime.waitIdle()).toBe(true)
  for (const f of [alpha, beta, gamma]) await waitReady(libraryRow(page, f.id))
  await expect(page.locator('[data-folder]')).toHaveCount(3)

  // Search: only the matching rows; Esc in the field clears it and every row is back.
  await field(page).fill('trip-')
  await expect(page.locator('[data-folder]')).toHaveCount(2)
  await expect(libraryRow(page, beta.id)).toHaveCount(0)
  await field(page).press('Escape')
  await expect(field(page)).toHaveValue('')
  await expect(page.locator('[data-folder]')).toHaveCount(3)

  // No match: the empty result and Clear Search.
  await field(page).fill('nothing like it')
  await expect(page.getByTestId('folders-empty')).toBeVisible()
  await page.getByRole('button', { name: 'Clear Search' }).click()
  await expect(page.locator('[data-folder]')).toHaveCount(3)

  // Recently added: one list, the folder added last first (Go's added_at),
  // which the name order contradicts.
  await choose(page, 'library-order', 'Recently added')
  await expect(page.locator('[data-month]')).toHaveCount(0)
  expect(await shownIds(page)).toEqual([gamma.id, beta.id, alpha.id])

  // Sort › Name: one list by name, no month headers.
  await choose(page, 'library-order', 'Name — A to Z')
  await expect(page.locator('[data-month]')).toHaveCount(0)
  expect(await shownIds(page)).toEqual([beta.id, alpha.id, gamma.id])

  // The arrows walk the rows shown, in the order shown.
  await field(page).fill('trip-')
  await field(page).blur()
  expect(await shownIds(page)).toEqual([alpha.id, gamma.id])
  await page.keyboard.press('ArrowDown')
  await expect(rowButton(page, alpha.id)).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await expect(rowButton(page, gamma.id)).toBeFocused()
  await expect(rowButton(page, gamma.id)).toHaveAttribute('aria-current', 'true')
  await page.keyboard.press('ArrowDown')
  await expect(rowButton(page, gamma.id)).toBeFocused()

  // ↓ from the search field: the first row shown.
  await field(page).focus()
  await page.keyboard.press('ArrowDown')
  await expect(rowButton(page, alpha.id)).toBeFocused()

  // A search that hides the selected row clears the selection.
  await field(page).fill('home')
  await expect(page.locator('[data-folder]')).toHaveCount(1)
  await field(page).press('Escape')
  await expect(page.locator('[data-folder]')).toHaveCount(3)
  await expect(page.locator('[data-folder] > button[aria-current="true"]')).toHaveCount(0)

  // ↩ with nothing selected opens the first row shown.
  await field(page).fill('trip-')
  await field(page).blur()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`#/folder/${alpha.id}$`))

  // Back from the folder: the toolbar is as it was left.
  await page.keyboard.press('Escape')
  await waitLibrary(page)
  await expect(field(page)).toHaveValue('trip-')
  await expect(page.getByTestId('library-order')).toContainText('Name')
  expect(await shownIds(page)).toEqual([alpha.id, gamma.id])
  await field(page).press('Escape')
  await expect(page.locator('[data-folder]')).toHaveCount(3)

  // Show › Needs attention, with every folder fine: the empty result; Show All Folders brings the rows back.
  await choose(page, 'library-show', 'Needs attention')
  await expect(page.getByTestId('folders-empty')).toContainText('No folder needs attention')
  await expect(page.locator('[data-folder]')).toHaveCount(0)
  await page.getByRole('button', { name: 'Show All Folders' }).click()
  await expect(page.locator('[data-folder]')).toHaveCount(3)

  // ⌘F focuses the search field: the key, and Edit › Find from the native menu.
  await field(page).blur()
  await expect(field(page)).not.toBeFocused()
  await page.keyboard.press('Meta+f')
  await expect(field(page)).toBeFocused()
  await field(page).blur()
  await expect(field(page)).not.toBeFocused()
  await runtime.emitMenuAction('find')
  await expect(field(page)).toBeFocused()
})
