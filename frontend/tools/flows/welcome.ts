// Welcome and shell flows: first launch → add a folder → the Library with
// that folder scanning; a failed model download retried; a model switched
// off and on again. Welcome states declare no overlays.
import { expect, type Page } from '@playwright/test'
import type { ProtoHandle } from '../flows.pw'
import { operateMenus } from './landing'

const at = (page: Page, n: number) => expect(page).toHaveURL(new RegExp(`#s${n}$`))

export default async function welcome(page: Page, proto: ProtoHandle): Promise<void> {
  // First launch, then Add Your First Folder: the folder is added and scans.
  await proto.open(11)
  await expect(page.getByRole('heading', { name: 'Welcome to Choix' })).toBeVisible()
  await page.getByRole('button', { name: /Add Your First Folder/ }).click()
  await at(page, 30)
  // The scan shows in the folder's row, not the title bar (no duplicate status).
  await expect(page.locator('[data-folder="lisbon"]')).toHaveAttribute('data-folder-state', 'scanning')
  await expect(page.locator('[data-folder="lisbon"]')).toContainText('Looking for photos')
  await expect(page.locator('[data-titlebar]')).not.toContainText('Lisbon')
  // The Library's toolbar menus, now that it lists a folder.
  await operateMenus(page)

  // A failed download: Retry starts it again.
  await proto.open(12)
  const clip = page.locator('[data-model="clip"]')
  await expect(clip).toHaveAttribute('data-state', 'failed')
  await page.getByRole('button', { name: 'Retry the visual grouping download' }).click()
  await expect(clip).toHaveAttribute('data-state', 'downloading')

  // Visual grouping off, then on again.
  await proto.open(15)
  const toggle = page.getByRole('switch', { name: 'Visual grouping' })
  await expect(toggle).toHaveAttribute('aria-checked', 'false')
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-checked', 'true')
  await expect(clip).toHaveAttribute('data-state', 'queued')
}
