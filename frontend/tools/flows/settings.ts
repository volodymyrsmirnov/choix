// Settings flows: change a field → Save (the section marker comes and goes,
// a toast confirms); make a field invalid → its error, Save disabled → fix
// it; the leave prompt → Keep editing (button and Escape), then → Discard
// changes, back in the Library; Retry on a failed model row; the sidebar
// moves the highlight; the seeded "Save failed" toast dismissed.
import { expect, type Page } from '@playwright/test'
import type { ProtoHandle } from '../flows.pw'
import { landOn } from './landing'

const at = (page: Page, n: number) => expect(page).toHaveURL(new RegExp(`#s${n}$`))

export default async function settings(page: Page, proto: ProtoHandle): Promise<void> {
  const nav = (name: string) => page.getByRole('navigation', { name: 'Settings sections' }).getByRole('button', { name: new RegExp(`^${name}`) })
  const save = page.getByTestId('save')
  const advance = page.getByRole('switch', { name: 'Advance on action' })

  // Change a field → the Behaviour section is marked → Save → a toast.
  await proto.open(500)
  await expect(save).toBeDisabled()
  await advance.click()
  await expect(advance).toHaveAttribute('aria-checked', 'false')
  await expect(nav('Behaviour')).toHaveAccessibleName('Behaviour (unsaved changes)')
  await expect(page.getByRole('status').filter({ hasText: 'no re-cluster needed' })).toBeVisible()
  await save.click()
  await expect(nav('Behaviour')).toHaveAccessibleName('Behaviour')
  await expect(save).toBeDisabled()
  const saved = page.locator('[data-overlay="toast:saved"]')
  await expect(saved).toContainText('Settings saved')
  await saved.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(saved).toHaveCount(0)

  // Make the time bucket invalid → its error, Save disabled → fix it → Save.
  const bucket = page.getByTestId('bucket-input')
  await bucket.fill('0.5')
  await expect(page.locator('#settings-bucket-error')).toHaveText('Must be at least 1 minute.')
  await expect(bucket).toHaveAttribute('aria-invalid', 'true')
  await expect(save).toBeDisabled()
  await expect(page.getByText('Fix the highlighted field to save.')).toBeVisible()
  await bucket.fill('5')
  await expect(page.locator('#settings-bucket-error')).toHaveCount(0)
  await expect(page.getByText('Saving will re-cluster your folders (time bucket changed). Picks are kept.')).toBeVisible()
  await save.click()
  await expect(page.locator('[data-overlay="toast:saved"]')).toContainText('Re-clustered with new parameters')

  // An absolute picks folder is refused; a folder name is fine.
  await proto.open(520)
  const picks = page.getByTestId('picks-dir-input')
  await picks.fill('/Volumes/Atlas/Selects')
  await expect(page.locator('#settings-picks-dir-error')).toHaveText('Use a folder name inside the scanned folder, not an absolute path.')
  await expect(save).toBeDisabled()
  await picks.fill('selects/2026')
  await expect(page.locator('#settings-picks-dir-error')).toHaveCount(0)
  await expect(save).toBeEnabled()

  // Revert to saved and Restore defaults refill the form.
  await page.getByRole('button', { name: 'Revert to saved' }).click()
  await expect(picks).toHaveValue('selects')
  await page.getByRole('button', { name: 'Restore defaults' }).click()
  await expect(picks).toHaveValue('picks')
  await expect(bucket).toHaveValue('10')

  // The sidebar moves the highlight.
  await proto.open(500)
  await expect(nav('Grouping')).toHaveAttribute('aria-current', 'true')
  await nav('Output').click()
  await expect(nav('Output')).toHaveAttribute('aria-current', 'true')
  await expect(page.locator('#settings-output-title')).toBeFocused()
  await nav('Behaviour').click()
  await expect(nav('Behaviour')).toHaveAttribute('aria-current', 'true')

  // Back with no edits leaves at once.
  await page.getByRole('button', { name: 'Back to Library' }).click()
  await landOn(page, 102)

  // Back with edits → the prompt → Keep editing; again → Escape; again →
  // Discard changes, back in the Library.
  await proto.open(500)
  await advance.click()
  await page.getByRole('button', { name: 'Back to Library' }).click()
  const prompt = page.getByRole('alertdialog', { name: 'Discard unsaved changes?' })
  await expect(prompt).toBeVisible()
  await expect(prompt.getByRole('button', { name: 'Keep editing' })).toBeFocused()
  await prompt.getByRole('button', { name: 'Keep editing' }).click()
  await expect(prompt).toHaveCount(0)
  await expect(advance).toHaveAttribute('aria-checked', 'false')
  await page.getByRole('button', { name: 'Cancel' }).click()
  await expect(prompt).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(prompt).toHaveCount(0)
  await page.getByRole('button', { name: 'Back to Library' }).click()
  await prompt.getByRole('button', { name: 'Discard changes' }).click()
  await landOn(page, 102)

  // The prompt as a state of its own: Keep editing closes it.
  await proto.open(508)
  await expect(prompt).toBeVisible()
  await prompt.getByRole('button', { name: 'Keep editing' }).click()
  await expect(prompt).toHaveCount(0)

  // Retry on a failed model row: the quality model, then CLIP.
  await proto.open(506)
  const qualityRow = page.locator('[data-model="quality"]')
  await expect(qualityRow).toHaveAttribute('data-state', 'failed')
  await expect(page.getByTestId('quality-retry')).toBeVisible()
  await page.getByRole('button', { name: 'Retry the quality rating download' }).click()
  await expect(qualityRow).toHaveAttribute('data-state', 'downloading')

  await proto.open(517)
  const clipRow = page.locator('[data-model="clip"]')
  await expect(clipRow).toHaveAttribute('data-state', 'failed')
  // Downloads are sequential: with the quality model downloading, the
  // retried CLIP download waits in the queue.
  await expect(page.locator('[data-model="quality"]')).toHaveAttribute('data-state', 'downloading')
  await page.getByRole('button', { name: 'Retry the visual grouping download' }).click()
  await expect(clipRow).toHaveAttribute('data-state', 'queued')

  // Visual grouping off: the similarity and cross-device controls wait for it.
  await proto.open(507)
  const visual = page.getByRole('switch', { name: 'Visual grouping' })
  await expect(visual).toHaveAttribute('aria-checked', 'false')
  await expect(page.getByRole('switch', { name: 'Cross-device merge' })).toBeDisabled()
  await visual.click()
  await expect(page.getByRole('switch', { name: 'Cross-device merge' })).toBeEnabled()
  await expect(nav('Grouping')).toHaveAccessibleName('Grouping (unsaved changes)')

  // A failed load: Try Again loads the page.
  await proto.open(511)
  await page.getByRole('button', { name: 'Try Again' }).click()
  await at(page, 500)

  // The seeded "Save failed" toast: dismissed.
  await proto.open(512)
  const failed = page.locator('[data-overlay="toast:save-failed"]')
  await expect(failed).toBeVisible()
  await failed.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(failed).toHaveCount(0)

  // At every size the toast keeps the right inset toasts have on every
  // screen (16 px from the window's edge) and stays above the save bar.
  for (const size of ['min', 'default', 'large'] as const) {
    await proto.setToggles({ size })
    await proto.open(512)
    const [root, toast, save] = await Promise.all([
      page.locator('[data-app-root]').boundingBox(),
      failed.boundingBox(),
      page.getByTestId('save').boundingBox(),
    ])
    expect(root && toast && save, size).toBeTruthy()
    expect(Math.abs(root!.x + root!.width - (toast!.x + toast!.width) - 16), `${size}: right inset`).toBeLessThan(0.5)
    expect(toast!.y + toast!.height, `${size}: above the save bar`).toBeLessThanOrEqual(save!.y)
    await failed.getByRole('button', { name: /^Dismiss notification/ }).click()
    await expect(failed).toHaveCount(0)
  }
  await proto.setToggles({ size: 'default' })
}
