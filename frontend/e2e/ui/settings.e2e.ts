// Settings on the real app: a 1-minute time bucket, saved, re-clusters the
// folder (back on its page there are more clusters); an absolute picks
// folder shows the design's field error and Save stays disabled.
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { FIELD_COPY } from '../../src/design/screens/settings/validate'
import { CONFIG_TOML, expect, test } from './fixtures'
import {
  clusters,
  expectDraggableTitleBar,
  libraryRow,
  openRow,
  titleBarButton,
  titleOf,
  waitFolder,
  waitLibrary,
  waitReady,
} from './screens'

test('a 1-minute bucket, saved, re-clusters the folder into more clusters', async ({ app }) => {
  const { page, server, runtime } = app
  // genfolder: 24 photos, 2 cameras alternating, 3 bursts of 10 minutes, so
  // one camera's consecutive shots are 2.5 minutes apart. The default
  // 10-minute bucket keeps each camera's burst together; 1 minute splits it.
  const folder = server.generate('trip')
  await waitLibrary(page)
  const { id } = await runtime.addFolderPath(folder)
  const row = libraryRow(page, id)
  await waitReady(row)

  await openRow(row)
  await waitFolder(page, 'trip')
  await expectDraggableTitleBar(page, 'Folder')
  const before = await clusters(page).count()
  expect(before).toBeGreaterThan(0)
  await expect(titleOf(page).locator('xpath=following-sibling::p')).toContainText(`${before} clusters`)

  // Settings from the folder's title bar.
  await titleBarButton(page, 'Settings').click()
  await expect(page).toHaveURL(/#\/settings$/)
  await expect(titleOf(page)).toHaveText('Settings')
  const bucket = page.getByTestId('bucket-input')
  await expect(bucket).toHaveValue('10')
  await expectDraggableTitleBar(page, 'Settings')

  const save = page.getByTestId('save')
  await expect(save).toBeDisabled()
  await bucket.fill('1')
  await expect(page.locator('#settings-bucket-error')).toHaveCount(0)
  await expect(save).toBeEnabled()
  await save.click()
  const saved = page.getByTestId('toast').filter({ hasText: 'Settings saved' })
  await expect(saved).toBeVisible()
  await expect(saved).toContainText('Re-clustered with new parameters')
  await expect(save).toBeDisabled()
  expect(readFileSync(join(server.data, 'config.toml'), 'utf8')).toMatch(/bucket_size_sec\s*=\s*60\b/)

  // Back to the folder: more clusters than before.
  await titleBarButton(page, 'Back to trip').click()
  await waitFolder(page, 'trip')
  await expect.poll(() => clusters(page).count(), { message: 'the cluster count grew' }).toBeGreaterThan(before)
  const after = await clusters(page).count()
  await expect(titleOf(page).locator('xpath=following-sibling::p')).toContainText(`${after} clusters`)
  await expectDraggableTitleBar(page, 'Folder (after Settings)')
  test.info().annotations.push({ type: 'clusters', description: `${before} → ${after}` })
})

test('an absolute picks folder shows the field error and Save stays disabled', async ({ app }) => {
  const { page, server } = app
  await waitLibrary(page)
  await expectDraggableTitleBar(page, 'Library')

  await titleBarButton(page, 'Settings').click()
  await expect(page).toHaveURL(/#\/settings$/)
  const picks = page.getByTestId('picks-dir-input')
  await expect(picks).toHaveValue('picks')
  await expectDraggableTitleBar(page, 'Settings')

  await picks.fill('/picks')
  const error = page.locator('#settings-picks-dir-error')
  await expect(error).toHaveText(FIELD_COPY.picks_dir.absolute)
  await expect(picks).toHaveAttribute('aria-describedby', /settings-picks-dir-error/)
  const save = page.getByTestId('save')
  await expect(save).toBeDisabled()
  // Enter in the field does not save either.
  await picks.press('Enter')
  await expect(save).toBeDisabled()
  await expectDraggableTitleBar(page, 'Settings (with the error)')

  // A relative folder clears the error and makes Save available again.
  await picks.fill('selects')
  await expect(error).toHaveCount(0)
  await expect(save).toBeEnabled()

  // Nothing was saved.
  expect(readFileSync(join(server.data, 'config.toml'), 'utf8')).toBe(CONFIG_TOML)
})
