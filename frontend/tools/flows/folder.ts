// Folder flows: the filled page → the camera menu (Escape, then a camera) →
// the sort menu (click-away, then latest first) → the search field (⌘F, a
// match, no match) → the keyboard cursor (↓, F opens Focus) → a thumbnail
// and Open Focus open Focus → the +N pill pages the strip → Open Picks and
// its toast → Re-cluster → the seeded toasts closed → Back, Settings and the
// shortcut sheet.
import { expect, type Page } from '@playwright/test'
import type { ProtoHandle } from '../flows.pw'
import { landOn } from './landing'

const at = (page: Page, n: number) => expect(page).toHaveURL(new RegExp(`#s${n}$`))

export default async function folder(page: Page, proto: ProtoHandle): Promise<void> {
  const counts = page.getByTestId('cluster-counts')
  const clusters = page.locator('[data-testid="cluster"]')

  // The camera menu: opened, closed with Escape, then a camera chosen.
  await proto.open(202)
  const camera = page.getByTestId('device-filter')
  const cameraMenu = page.getByRole('menu', { name: 'Camera' })
  await camera.click()
  await expect(cameraMenu).toBeVisible()
  await expect(camera).toHaveAttribute('aria-expanded', 'true')
  await page.keyboard.press('Escape')
  await expect(cameraMenu).toHaveCount(0)
  await expect(camera).toBeFocused()
  await camera.click()
  await cameraMenu.getByRole('menuitemradio', { name: 'iPhone 15 Pro' }).click()
  await expect(cameraMenu).toHaveCount(0)
  await expect(camera).toContainText('iPhone 15 Pro')
  await expect(counts).toContainText('4 of 14 clusters')

  // The sort menu: closed by a click elsewhere, then latest first chosen.
  const sort = page.getByTestId('sort-toggle')
  const sortMenu = page.getByRole('menu', { name: 'Sort clusters' })
  await sort.click()
  await expect(sortMenu).toBeVisible()
  await page.locator('[data-titlebar] h1').click()
  await expect(sortMenu).toHaveCount(0)
  await sort.click()
  await sortMenu.getByRole('menuitemradio', { name: 'Capture time — latest first' }).click()
  await expect(sortMenu).toHaveCount(0)
  await expect(sort).toContainText('Capture time ↓')
  await expect(clusters.first()).toContainText('Sep 6 · 12:30')

  // The search field: ⌘F focuses it; a time matches, a place does not.
  await proto.open(202)
  const search = page.getByTestId('filter-input')
  await page.keyboard.press('Meta+f')
  await expect(search).toBeFocused()
  await search.fill('Sep 3')
  await expect(counts).toContainText('3 of 14 clusters')
  await expect(clusters).toHaveCount(3)
  await search.fill('harbour')
  await expect(page.getByText('No clusters match your filters.')).toBeVisible()
  await expect(clusters).toHaveCount(0)

  // The keyboard cursor: ↓ moves it, F opens that cluster in Focus.
  await proto.open(202)
  await expect(clusters.nth(0)).toHaveAttribute('data-active-cluster', 'true')
  await page.keyboard.press('ArrowDown')
  await expect(clusters.nth(1)).toHaveAttribute('data-active-cluster', 'true')
  await expect(clusters.nth(0)).not.toHaveAttribute('data-active-cluster', 'true')
  await page.keyboard.press('j')
  await expect(clusters.nth(2)).toHaveAttribute('data-active-cluster', 'true')
  await page.keyboard.press('ArrowUp')
  await expect(clusters.nth(1)).toHaveAttribute('data-active-cluster', 'true')
  await page.keyboard.press('f')
  await landOn(page, 311)

  // A click on a cluster makes it active; a thumbnail or Open Focus opens it.
  await proto.open(203)
  await clusters.nth(1).locator('h2').click()
  await expect(clusters.nth(1)).toHaveAttribute('data-active-cluster', 'true')
  await page.keyboard.press('Enter')
  await landOn(page, 311)
  // A cluster with no Focus state in the prototype says so.
  await proto.open(203)
  await clusters.nth(3).getByRole('button', { name: /^Open Focus/ }).click()
  const noFocus = page.locator('[data-overlay="toast:no-focus"]')
  await expect(noFocus).toBeVisible()
  await noFocus.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(noFocus).toHaveCount(0)
  await proto.open(203)
  await clusters.nth(0).getByTestId('thumb').nth(2).click()
  await landOn(page, 301)
  await proto.open(203)
  await clusters.nth(0).getByRole('button', { name: /^Open Focus/ }).click()
  await landOn(page, 301)

  // A long strip: Tab reaches every thumbnail, the row scrolling with focus;
  // the +N pill is a pointer-only paging control (out of the Tab order) and a
  // click on it pages the row forward.
  await proto.open(220)
  const burst = clusters.nth(1)
  const thumbs = burst.getByTestId('thumb')
  const n = await thumbs.count()
  await thumbs.first().focus()
  for (let i = 1; i < n; i++) {
    await page.keyboard.press('Tab')
    await expect(thumbs.nth(i)).toBeFocused()
  }
  await expect(thumbs.nth(n - 1)).toBeInViewport()
  await proto.open(220)
  const more = clusters.nth(1).getByTestId('strip-more')
  await expect(more).toHaveAttribute('tabindex', '-1')
  await expect(more).toHaveAttribute('aria-hidden', 'true')
  const before = await more.textContent()
  await more.click()
  await expect(more).not.toHaveText(before ?? '')

  // Open Picks shows a toast; its Dismiss closes it.
  await proto.open(203)
  await page.getByTestId('open-picks').click()
  const picksToast = page.locator('[data-overlay="toast:picks"]')
  await expect(picksToast).toBeVisible()
  await picksToast.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(picksToast).toHaveCount(0)

  // Re-cluster leads to the busy state.
  await proto.open(203)
  await page.getByTestId('recluster').click()
  await at(page, 215)
  await expect(page.getByTestId('recluster')).toHaveAttribute('aria-busy', 'true')

  // The seeded toasts: dismissed, and closed with Escape.
  await proto.open(222)
  const done = page.locator('[data-overlay="toast:reclustered"]')
  await done.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(done).toHaveCount(0)
  await proto.open(223)
  const failed = page.locator('[data-overlay="toast:recluster-failed"]')
  await expect(failed).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(failed).toHaveCount(0)

  // Offline: the drive notice, Open Picks and Re-cluster unavailable.
  await proto.open(224)
  await expect(page.getByText('Drive “Atlas” is not connected')).toBeVisible()
  await expect(page.getByTestId('open-picks')).toBeDisabled()
  await expect(page.getByTestId('recluster')).toBeDisabled()
  // ...and opening Focus: the thumbnails, Open Focus, F and ↩.
  await expect(clusters.nth(0).getByTestId('thumb').first()).toBeDisabled()
  await expect(clusters.nth(0).getByRole('button', { name: /^Open Focus/ })).toBeDisabled()
  await page.keyboard.press('f')
  await page.keyboard.press('Enter')
  await at(page, 224)

  // The title bar leads out of the page.
  await proto.open(202)
  await page.getByRole('button', { name: 'Back to Library' }).click()
  await landOn(page, 102)
  await proto.open(202)
  await page.getByRole('button', { name: 'Settings' }).click()
  await landOn(page, 500)
  await proto.open(202)
  await page.getByRole('button', { name: 'Keyboard shortcuts' }).click()
  await landOn(page, 600)
}
