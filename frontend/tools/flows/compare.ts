// Compare flows: panes added and removed from the rail (at most four); the
// active pane chosen by a pane click, its label (the keyboard path) and ← →;
// pick → toast → Undo on the active pane; zoom shared by every pane; leave
// Compare and enter it again with the selection kept. Leaving Compare shows
// the Info toggle, so the Info panel is opened and closed here too.
import { expect, type Page } from '@playwright/test'
import type { ProtoHandle } from '../flows.pw'

export default async function compare(page: Page, proto: ProtoHandle): Promise<void> {
  const panes = page.getByTestId('compare-pane')
  const rail = page.getByTestId('rail-thumb')
  const active = page.locator('[data-testid="compare-pane"][data-active="true"]')

  // No panes: the hint; rail clicks add up to four panes, a fifth is refused.
  await proto.open(407)
  await expect(page.getByText(/^Click rail thumbnails to add to compare/)).toBeVisible()
  await expect(page.getByTestId('pick-btn')).toBeDisabled()
  for (const i of [1, 2, 3, 5]) await rail.nth(i).click()
  await expect(panes).toHaveCount(4)
  await expect(rail.nth(5)).toHaveAttribute('aria-pressed', 'true')
  await rail.nth(6).click()
  await expect(panes).toHaveCount(4)
  await expect(rail.nth(6)).toHaveAttribute('aria-pressed', 'false')
  // Removing the active pane activates the one that slides into its place.
  await expect(active).toContainText('02 / 08')
  await rail.nth(1).click()
  await expect(panes).toHaveCount(3)
  await expect(active).toContainText('03 / 08')

  // The active pane: a click on another pane, its label, then ← →.
  await proto.open(401)
  await expect(active).toContainText('03 / 08')
  await panes.nth(0).click({ position: { x: 40, y: 120 } })
  await expect(active).toContainText('01 / 08')
  await page.getByRole('button', { name: /^Pane 3:/ }).click()
  await expect(active).toContainText('06 / 08')
  await page.getByRole('button', { name: 'Next pane' }).click()
  await expect(active).toContainText('01 / 08')
  await page.getByRole('button', { name: 'Next pane' }).click()
  await expect(active).toContainText('03 / 08')
  await page.getByRole('button', { name: 'Previous pane' }).click()
  await expect(active).toContainText('01 / 08')

  // Pick the active pane → the toast → Undo.
  const pick = page.getByTestId('pick-btn')
  const toast = page.locator('[data-overlay="toast:focus-undo"]')
  await pick.click()
  await expect(active).toContainText('Picked')
  await toast.getByRole('button', { name: /^Undo/ }).click()
  await expect(active).not.toContainText('Picked')
  await toast.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(toast).toHaveCount(0)

  // Zoom applies to every pane at once; Pixel-peep is off while comparing.
  await expect(page.getByTestId('zoom-btn')).toBeDisabled()
  await page.getByRole('button', { name: 'Zoom in' }).click()
  await expect(page.getByTestId('zoom-level')).toHaveText('150%')
  const transforms = await panes.locator('img[data-measured-by="focus-zoom"]').evaluateAll((els) => [
    ...new Set(els.map((e) => (e as HTMLElement).style.transform)),
  ])
  expect(transforms).toEqual(['translate(0px, 0px) scale(1.5)'])
  await page.getByRole('button', { name: 'Zoom out' }).click()
  await expect(page.getByTestId('zoom-level')).toHaveText('100%')

  // Leave Compare; the photo on screen is the active pane's. Compare again
  // keeps the selection.
  const compareBtn = page.getByTestId('compare-btn')
  await compareBtn.click()
  await expect(panes).toHaveCount(0)
  await expect(page.locator('main').getByText('01 / 08')).toBeVisible()
  const info = page.locator('[data-overlay="focus-info"]')
  await page.getByRole('button', { name: /^Info/ }).click()
  await expect(info).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(info).toHaveCount(0)
  await compareBtn.click()
  await expect(panes).toHaveCount(3)

  // The video pane: native controls, nothing loads until played.
  await proto.open(405)
  await expect(panes.nth(0).locator('video')).toHaveAttribute('preload', 'none')
  await expect(panes.nth(0).locator('video')).not.toHaveAttribute('autoplay', /.*/)

  // A pick in flight on the active pane: Pick and Reject wait.
  await proto.open(409)
  await expect(pick).toBeDisabled()
  await expect(page.getByTestId('reject-btn')).toBeDisabled()
}
