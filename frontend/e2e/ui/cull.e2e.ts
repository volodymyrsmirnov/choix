// Cull (spec §11's flow) on the real app: Add Folder through the picker,
// wait for the row to be ready, open the folder and its first cluster,
// pick with Space (the badge, and the copy in picks/), Space again (both
// gone), pick again, Escape back to the folder, Edit ▸ Undo from the menu
// (the folder session and its undo stack survived the navigation, so the
// pick is undone), Escape back to the Library.
import { existsSync, readFileSync } from 'node:fs'
import { basename, join } from 'node:path'
import { expect, test } from './fixtures'
import {
  clusters,
  expectDraggableTitleBar,
  folderThumb,
  libraryRow,
  openRow,
  titleBarButton,
  waitFolder,
  waitLibrary,
  waitReady,
} from './screens'

test('cull: pick, unpick, pick, back, undo from the menu, back to the Library', async ({ app }) => {
  const { page, server, runtime } = app
  const folder = server.generate('cull')
  const picks = join(folder, 'picks')

  // Library: Add Folder answers with the folder the picker "picks".
  await waitLibrary(page)
  await expectDraggableTitleBar(page, 'Library (empty)')
  await runtime.setPickerPaths([folder])
  await titleBarButton(page, /^Add Folder/).click()
  const row = libraryRow(page)
  await expect(row).toHaveCount(1)
  await waitReady(row)
  const folderId = await row.getAttribute('data-folder')
  expect(folderId).toMatch(/^[0-9a-f]{16}$/)
  await expect(row.locator(':scope > button')).toContainText('cull')
  await expectDraggableTitleBar(page, 'Library')

  // Folder: the first cluster's first photo.
  await openRow(row)
  await waitFolder(page, 'cull')
  await expectDraggableTitleBar(page, 'Folder')
  const first = clusters(page).first()
  const thumb = first.getByTestId('thumb').first()
  const fileId = (await thumb.getAttribute('data-file-id'))!
  const path = (await thumb.getAttribute('title'))!
  expect(fileId).toMatch(/^\d+$/)
  expect(path).toMatch(/^photo-\d{4}\.jpg$/)
  const copy = join(picks, basename(path))
  await expect(thumb).not.toHaveAttribute('data-state', 'picked')

  // Focus opens on the cluster's first photo.
  await first.getByRole('button', { name: /^Open Focus/ }).click()
  await expect(page).toHaveURL(new RegExp(`#/folder/${folderId}/focus/\\d+$`))
  const pick = page.getByTestId('pick-btn')
  await expect(pick).toBeEnabled()
  const current = page.locator('[data-testid="rail-thumb"][aria-current="true"]')
  await expect(current).toHaveAttribute('data-file-id', fileId)
  await expectDraggableTitleBar(page, 'Focus')

  // Space picks: the badge at once (the mark is optimistic), then the copy
  // in picks/ (byte for byte). While Go has not answered, the photo is busy:
  // the Pick button is disabled and Space on it does nothing
  // (folderStore.pick ignores a file with a pending mark), so every next
  // press waits for the button to be enabled again — the answer.
  const press = async () => {
    await expect(pick, 'the previous action was answered').toBeEnabled()
    await page.keyboard.press('Space')
  }
  await press()
  await expect(pick).toHaveAttribute('aria-pressed', 'true')
  await expect(pick).toHaveText(/Picked/)
  await expect(current).toHaveAttribute('aria-label', /\(picked\)$/)
  await expect.poll(() => existsSync(copy), { message: `${basename(path)} is copied into picks/` }).toBe(true)
  expect(readFileSync(copy).equals(readFileSync(join(folder, path)))).toBe(true)

  // Space again un-marks: badge and copy gone.
  await press()
  await expect(pick).toHaveAttribute('aria-pressed', 'false')
  await expect(pick).toHaveText(/^Pick/)
  await expect(current).not.toHaveAttribute('aria-label', /\(picked\)/)
  await expect.poll(() => existsSync(copy), { message: `${basename(path)} is gone from picks/` }).toBe(false)

  // Pick again.
  await press()
  await expect(pick).toHaveAttribute('aria-pressed', 'true')
  await expect.poll(() => existsSync(copy), { message: `${basename(path)} is copied into picks/ again` }).toBe(true)
  await expect(pick).toBeEnabled()

  // Escape: back on the Folder page, the thumbnail carries the pick.
  await page.keyboard.press('Escape')
  await waitFolder(page, 'cull')
  await expect(page).toHaveURL(new RegExp(`#/folder/${folderId}$`))
  await expect(folderThumb(page, fileId)).toHaveAttribute('data-state', 'picked')
  await expectDraggableTitleBar(page, 'Folder (after Focus)')

  // Edit ▸ Undo from the native menu: the folder's undo stack survived the
  // navigation, so the last pick is undone.
  await runtime.emitMenuAction('undo', '')
  await expect(folderThumb(page, fileId)).not.toHaveAttribute('data-state', 'picked')
  await expect(page.getByTestId('toast').filter({ hasText: 'Undone' })).toBeVisible()
  await expect.poll(() => existsSync(copy), { message: `${basename(path)} is gone from picks/ after the undo` }).toBe(false)
  await expect(page).toHaveURL(new RegExp(`#/folder/${folderId}$`))

  // Escape: back to the Library, the row still there.
  await page.keyboard.press('Escape')
  await waitLibrary(page)
  await expect(libraryRow(page, folderId!)).toHaveAttribute('data-folder-state', 'ready')
  await expectDraggableTitleBar(page, 'Library (after the folder)')
})
