// Focus flows: the Info panel opened and closed by its toggle, its close
// button and Escape; pick → toast → Undo (and reject → toast dismissed);
// moving through the rail and with ← →; pixel-peep and the zoom steps; enter
// and leave Compare; the cluster-done pill and previous / next cluster; Try
// Again after a failed load; Back, Help and Settings leave Focus.
import { expect, type Page } from '@playwright/test'
import type { ProtoHandle } from '../flows.pw'
import { landOn } from './landing'

const at = (page: Page, n: number) => expect(page).toHaveURL(new RegExp(`#s${n}$`))

export default async function focus(page: Page, proto: ProtoHandle): Promise<void> {
  await proto.open(301)
  const info = page.locator('[data-overlay="focus-info"]')
  const infoToggle = page.getByRole('button', { name: /^Info/ })
  const counter = page.locator('main').getByText(/^\d\d \/ 08$/)

  // The Info panel: the toggle opens it, its close button closes it; the
  // toggle again, then Escape; the toggle twice.
  await expect(info).toHaveCount(0)
  await infoToggle.click()
  await expect(info).toBeVisible()
  await expect(info).toContainText('Fujifilm X-T5')
  await info.getByRole('button', { name: 'Close info panel' }).click()
  await expect(info).toHaveCount(0)
  await infoToggle.click()
  await expect(infoToggle).toHaveAttribute('aria-pressed', 'true')
  await page.keyboard.press('Escape')
  await expect(info).toHaveCount(0)
  await infoToggle.click()
  await infoToggle.click()
  await expect(info).toHaveCount(0)

  // Pick → the toast → Undo puts the mark back; the toast says so and is dismissed.
  const pick = page.getByTestId('pick-btn')
  const reject = page.getByTestId('reject-btn')
  const toast = page.locator('[data-overlay="toast:focus-undo"]')
  await pick.click()
  await expect(pick).toHaveAttribute('aria-pressed', 'true')
  await expect(pick).toContainText('Picked')
  await expect(toast).toContainText('Picked')
  await expect(page.locator('[data-titlebar]')).toContainText('3 of 8 reviewed')
  await toast.getByRole('button', { name: /^Undo/ }).click()
  await expect(pick).toHaveAttribute('aria-pressed', 'false')
  await expect(toast).toContainText('Undone')
  await expect(toast.getByRole('button', { name: /^Undo/ })).toHaveCount(0)
  await expect(page.locator('[data-titlebar]')).toContainText('2 of 8 reviewed')
  await toast.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(toast).toHaveCount(0)

  // Reject → its toast, closed with Escape; Reject again clears it.
  await reject.click()
  await expect(reject).toHaveAttribute('aria-pressed', 'true')
  await expect(toast).toContainText('Rejected')
  await page.keyboard.press('Escape')
  await expect(toast).toHaveCount(0)
  await reject.click()
  await expect(reject).toHaveAttribute('aria-pressed', 'false')
  await expect(toast).toContainText('Cleared')
  await toast.getByRole('button', { name: /^Dismiss notification/ }).click()

  // The rail and ← → move through the cluster, wrapping.
  await page.getByTestId('rail-thumb').nth(3).click()
  await expect(counter).toHaveText('04 / 08')
  await expect(page.getByTestId('rail-thumb').nth(3)).toHaveAttribute('aria-current', 'true')
  await page.getByRole('button', { name: 'Next photo' }).click()
  await expect(counter).toHaveText('05 / 08')
  await page.getByTestId('rail-thumb').nth(0).click()
  await page.getByRole('button', { name: 'Previous photo' }).click()
  await expect(counter).toHaveText('08 / 08')

  // Pixel-peep, the zoom steps, back to fit.
  const level = page.getByTestId('zoom-level')
  const peep = page.getByTestId('zoom-btn')
  await peep.click()
  await expect(level).toHaveText('200%')
  await page.getByRole('button', { name: 'Zoom in' }).click()
  await expect(level).toHaveText('300%')
  await page.getByRole('button', { name: 'Zoom out' }).click()
  await page.getByRole('button', { name: 'Zoom out' }).click()
  await expect(level).toHaveText('133%')
  await peep.click()
  await expect(level).toHaveText('100%')
  await expect(page.getByRole('button', { name: 'Zoom out' })).toBeDisabled()

  // Enter Compare: the photo on screen is the first pane; a rail click adds
  // a second; Compare again leaves.
  const compare = page.getByTestId('compare-btn')
  const panes = page.getByTestId('compare-pane')
  await compare.click()
  await expect(compare).toHaveAttribute('aria-pressed', 'true')
  await expect(panes).toHaveCount(1)
  await page.getByTestId('rail-thumb').nth(1).click()
  await expect(panes).toHaveCount(2)
  await compare.click()
  await expect(panes).toHaveCount(0)
  await expect(counter).toHaveText('08 / 08')

  // The seeded Info panels: closed by the close button and by Escape.
  await proto.open(304)
  await expect(info).toBeVisible()
  await info.getByRole('button', { name: 'Close info panel' }).click()
  await expect(info).toHaveCount(0)
  await proto.open(305)
  await expect(info.getByRole('link', { name: /open in Google Maps/ })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(info).toHaveCount(0)

  // Pick in flight: Pick and Reject wait.
  await proto.open(315)
  await expect(pick).toBeDisabled()
  await expect(reject).toBeDisabled()

  // The cluster-done pill leads to the next cluster (the folder's second, a
  // single frame); previous comes back to the first, whose previous is off.
  await proto.open(310)
  await expect(page.getByRole('button', { name: /^Next cluster/ })).toHaveClass(/btn-primary/)
  await page.getByTestId('cluster-done').click()
  await at(page, 311)
  await expect(page.getByRole('button', { name: 'Next photo' })).toBeDisabled()
  await page.getByTestId('prev-cluster').click()
  await at(page, 301)
  await expect(page.getByTestId('prev-cluster')).toBeDisabled()

  // The folder's last cluster: a clip, never preloaded, no pixel-peep; its
  // next cluster is off.
  await proto.open(306)
  await expect(page.getByTestId('hero-video')).toHaveAttribute('preload', 'none')
  await expect(page.getByTestId('zoom-btn')).toHaveCount(0)
  await expect(page.getByTestId('next-cluster')).toBeDisabled()

  // A long cluster: the rail keeps the active frame in view, on opening and
  // after moving to the other end.
  await proto.open(318)
  const rail = page.getByTestId('rail')
  const inRail = async (i: number) => {
    const [r, t] = await Promise.all([rail.boundingBox(), page.locator(`[data-rail-index="${i}"]`).boundingBox()])
    return !!r && !!t && t.y >= r.y - 0.5 && t.y + t.height <= r.y + r.height + 0.5
  }
  await expect.poll(() => inRail(31)).toBe(true)
  await page.getByTestId('rail-thumb').nth(32).click()
  await page.getByRole('button', { name: 'Next photo' }).click()
  await page.getByRole('button', { name: 'Next photo' }).click()
  await page.getByRole('button', { name: 'Next photo' }).click()
  await page.getByRole('button', { name: 'Next photo' }).click()
  await page.getByRole('button', { name: 'Next photo' }).click()
  await page.getByRole('button', { name: 'Next photo' }).click()
  await page.getByRole('button', { name: 'Next photo' }).click()
  await page.getByRole('button', { name: 'Next photo' }).click()
  // 32 + 8 wraps to the first frame.
  await expect(page.locator('main').getByText(/^01 \/ 40$/)).toBeVisible()
  await expect.poll(() => inRail(0)).toBe(true)
  expect(await inRail(31)).toBe(false)

  // The only cluster: nowhere to go.
  await proto.open(317)
  await expect(page.getByTestId('prev-cluster')).toBeDisabled()
  await expect(page.getByTestId('next-cluster')).toBeDisabled()

  // A failed load: Try Again loads the cluster.
  await proto.open(316)
  await expect(page.getByRole('alert')).toContainText('This cluster could not be loaded')
  await page.getByRole('button', { name: /^Try Again/ }).click()
  await at(page, 301)

  // Leaving Focus: Back (the title bar's and the footer's), Help, Settings.
  await page.getByRole('button', { name: 'Back to Dolomites' }).click()
  await landOn(page, 203)
  await proto.open(301)
  await page.getByRole('button', { name: /^Back/ }).last().click()
  await landOn(page, 203)
  await proto.open(301)
  await page.getByRole('button', { name: 'Keyboard shortcuts' }).click()
  await landOn(page, 600)
  await proto.open(301)
  await page.getByRole('button', { name: 'Settings' }).click()
  await landOn(page, 500)
}
