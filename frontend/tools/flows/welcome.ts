// Welcome and shell flows: an empty Library → Add Folder → the Library with
// that folder scanning; a folder job through its stages, each in the
// folder's row and never in the title bar; a stopped scan; one and two
// rating passes in their rows; the error screen → Reload → the Library.
// Welcome states declare no overlays.
import { expect, type Page } from '@playwright/test'
import type { ProtoHandle } from '../flows.pw'
import { landOn, operateMenus } from './landing'

const at = (page: Page, n: number) => expect(page).toHaveURL(new RegExp(`#s${n}$`))

export default async function welcome(page: Page, proto: ProtoHandle): Promise<void> {
  const bar = page.locator('[data-titlebar]')
  const lisbon = page.locator('[data-folder="lisbon"]')

  // An empty Library is the drop zone; Add Folder adds a folder, which scans.
  await proto.open(100)
  await expect(page.locator('[data-drop-zone]')).toBeVisible()
  await page.locator('main').getByRole('button', { name: /Add Folder/ }).first().click()
  await at(page, 30)
  // The scan shows in the folder's row, not the title bar (no duplicate status).
  await expect(lisbon).toHaveAttribute('data-folder-state', 'scanning')
  await expect(lisbon).toContainText('Looking for photos')
  await expect(bar).not.toContainText('Lisbon')
  await expect(bar.getByTestId('scan-progress')).toHaveCount(0)
  // The Library's toolbar menus, now that it lists a folder.
  await operateMenus(page)

  // The job's stages, each in the row, none in the bar.
  for (const [n, text] of [
    [33, 'Reading metadata'],
    [34, 'Generating thumbnails'],
    [35, 'Analyzing'],
    [36, 'Grouping'],
  ] as const) {
    await proto.open(n)
    await expect(lisbon, `#${n}`).toHaveAttribute('data-folder-state', 'scanning')
    await expect(lisbon, `#${n}`).toContainText(text)
    await expect(bar, `#${n}`).not.toContainText(text)
  }
  // Failed files show in the row that has them.
  await proto.open(34)
  await expect(lisbon).toContainText('7 failed')

  // A stopped scan: the row says so and how to finish it.
  await proto.open(32)
  await expect(lisbon).toHaveAttribute('data-folder-state', 'error')
  await expect(lisbon).toContainText('Scan stopped. Rescan to finish it.')

  // One rating pass, then two at once: each in its own row.
  const dolomites = page.locator('[data-folder="dolomites"]')
  await proto.open(31)
  await expect(dolomites).toContainText('Rating quality')
  await expect(lisbon).not.toContainText('Rating quality')
  await expect(bar.getByTestId('quality-progress')).toHaveCount(0)
  await proto.open(37)
  await expect(dolomites).toContainText('Rating quality')
  await expect(lisbon).toContainText('Rating quality')
  await expect(bar.getByTestId('quality-progress')).toHaveCount(0)

  // The error screen: Reload leads back to the Library.
  await proto.open(40)
  await expect(page.getByTestId('app-error')).toBeVisible()
  await page.getByRole('button', { name: 'Reload' }).click()
  await landOn(page, 102)
}
