// Library on the real app: Remove from the folder menu (the native alert
// answered yes) drops the row and leaves the folder on disk, quietly; a
// folder deleted on disk leaves the Library with the "removed" toast when
// the window comes back to the front.
import { existsSync, readdirSync, rmSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from './fixtures'
import { expectDraggableTitleBar, libraryRow, waitLibrary, waitReady } from './screens'

test('remove-folder from the menu drops the row and keeps the folder on disk', async ({ app }) => {
  const { page, server, runtime } = app
  const folder = server.generate('remove')
  await waitLibrary(page)

  const { id, name } = await runtime.addFolderPath(folder)
  expect(name).toBe('remove')
  const row = libraryRow(page, id)
  await waitReady(row)
  expect(await runtime.waitIdle(), 'the job queue drains').toBe(true)
  await expectDraggableTitleBar(page, 'Library')
  const before = readdirSync(folder).sort()

  // The folder menu's Remove, confirmed in the native alert.
  await runtime.setConfirmAnswer(true)
  await runtime.emitMenuAction('remove-folder', id)
  await expect(row).toHaveCount(0)
  await expect(libraryRow(page)).toHaveCount(0)
  // The user's own removal says nothing (only a folder gone in Finder is toasted).
  await expect(page.getByTestId('toast')).toHaveCount(0)
  await expectDraggableTitleBar(page, 'Library (empty)')

  // Nothing on disk was touched: the photos, the cache, no picks folder.
  expect(statSync(folder).isDirectory()).toBe(true)
  expect(readdirSync(folder).sort()).toEqual(before)
  expect(existsSync(join(folder, 'photo-0001.jpg'))).toBe(true)
  expect(statSync(join(folder, '.choix')).isDirectory()).toBe(true)
})

test('a folder deleted on disk leaves with the removed toast when the window is focused', async ({ app }) => {
  const { page, server, runtime } = app
  const folder = server.generate('gone')
  await waitLibrary(page)

  const { id } = await runtime.addFolderPath(folder)
  const row = libraryRow(page, id)
  await waitReady(row)
  expect(await runtime.waitIdle(), 'the job queue drains').toBe(true)

  rmSync(folder, { recursive: true, force: true })
  // Nothing has checked yet: the periodic presence check runs every 30 s,
  // and this server started seconds ago.
  await expect(row).toHaveCount(1)
  await expect(page.getByTestId('toast')).toHaveCount(0)

  // The window becomes key. The server build has no window, so Wails never
  // sends `common:WindowFocus` itself; the event is sent through the
  // runtime's Events.Emit, which the server broadcasts to the page as the
  // desktop build delivers it to the window. The app answers with
  // Library.CheckNow.
  await runtime.emit('common:WindowFocus')
  const toast = page.getByTestId('toast').filter({ hasText: '“gone” left the Library' })
  await expect(toast).toBeVisible()
  await expect(toast).toContainText('The folder was deleted or moved to the Trash in Finder.')
  await expect(toast).toHaveAttribute('data-tone', 'info')
  await expect(row).toHaveCount(0)
  await expectDraggableTitleBar(page, 'Library (with the toast)')
})
