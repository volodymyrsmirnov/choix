// Keyboard only, from a cold start (round 1, item 6): the app is loaded
// and nothing is clicked. ↩ opens the first Library row and Esc comes back;
// ↓ / ↑ choose a row, ↩ opens the folder, ↩ opens Focus on its first cluster, → moves to the next photo,
// Esc goes back to the folder and Esc again to the Library, where the
// folder's row is still selected and has the keyboard focus.
import { expect, test } from './fixtures'
import { libraryRow, waitLibrary, waitReady } from './screens'

test('keyboard only: Library → Folder → Focus and back', async ({ app }) => {
  const { page, server, runtime } = app
  await waitLibrary(page)
  const first = await runtime.addFolderPath(server.generate('keys-a', 12))
  const second = await runtime.addFolderPath(server.generate('keys-b', 12))
  expect(await runtime.waitIdle()).toBe(true)
  await waitReady(libraryRow(page, first.id))
  await waitReady(libraryRow(page, second.id))

  // Cold start: nothing selected, nothing focused.
  await expect(page.locator('[data-folder] > button[aria-current="true"]')).toHaveCount(0)
  expect(await page.evaluate(() => document.activeElement === document.body)).toBe(true)

  const rows = page.locator('[data-folder]')
  const top = (await rows.nth(0).getAttribute('data-folder'))!
  const next = (await rows.nth(1).getAttribute('data-folder'))!

  // ↩ with nothing selected opens the first row on screen; Esc comes back
  // to the Library with that row selected and focused.
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`#/folder/${top}$`))
  await page.keyboard.press('Escape')
  await waitLibrary(page)
  await expect(libraryRow(page, top).locator(':scope > button')).toHaveAttribute('aria-current', 'true')
  await expect(libraryRow(page, top).locator(':scope > button')).toBeFocused()

  // ↓ ↑ move the selection and the keyboard focus.
  await page.keyboard.press('ArrowDown')
  await expect(libraryRow(page, next).locator(':scope > button')).toHaveAttribute('aria-current', 'true')
  await page.keyboard.press('ArrowUp')
  await expect(libraryRow(page, top).locator(':scope > button')).toBeFocused()

  // ↩ opens the folder.
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`#/folder/${top}$`))
  await expect(page.getByTestId('cluster').first()).toBeVisible()

  // ↩ opens Focus on the active (first) cluster; → is the next photo (when it has one).
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`#/folder/${top}/focus/\\d+$`))
  const current = page.locator('[data-testid="rail-thumb"][aria-current="true"]')
  await expect(current).toBeVisible()
  const photo = await current.getAttribute('data-file-id')
  if ((await page.getByTestId('rail-thumb').count()) > 1) {
    await page.keyboard.press('ArrowRight')
    await expect(current).not.toHaveAttribute('data-file-id', photo!)
  }

  // Esc: the folder; Esc: the Library, the row still selected and focused.
  await page.keyboard.press('Escape')
  await expect(page).toHaveURL(new RegExp(`#/folder/${top}$`))
  await page.keyboard.press('Escape')
  await waitLibrary(page)
  await expect(libraryRow(page, top).locator(':scope > button')).toHaveAttribute('aria-current', 'true')
  await expect(libraryRow(page, top).locator(':scope > button')).toBeFocused()

  // And on from there: ↓ is the next row.
  await page.keyboard.press('ArrowDown')
  await expect(libraryRow(page, next).locator(':scope > button')).toBeFocused()
})
