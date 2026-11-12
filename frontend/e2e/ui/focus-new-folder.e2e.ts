// The owner's path to a blank window (round 1, item 5): a folder added in
// this run, opened, and a photo clicked while the folder's job runs — Focus
// must show its stage. Then again once the job has ended (the case that
// worked after a restart). The fixtures fail the test on any console error
// or uncaught error on the page.
//
// The job is made to run for a while on purpose: once the folder's first
// scan has finished (so the grid has photos to click), a few thousand short
// videos (copies of the checked-in testdata clip) are put in a subfolder,
// and opening the folder queues its full job at the front, which has to
// read and thumbnail all of them. The test waits for the Folder page's scan
// progress before it clicks, and fails (it does not skip) when the job was
// no longer running once Focus showed.
import { copyFileSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { repoRoot } from '../global-setup'
import { expect, test } from './fixtures'
import { libraryRow, openRow, titleOf, waitLibrary, waitReady } from './screens'

/** How many videos keep the job busy (about 10 ms each to read and thumbnail on an M-series Mac). */
const VIDEOS = 2000

/** Focus shows its stage: the cluster title, the hero image, Pick, and the clicked photo current in the rail. */
async function expectFocusStage(page: Page, folderId: string, fileId: string): Promise<void> {
  await expect(page).toHaveURL(new RegExp(`#/folder/${folderId}/focus/\\d+$`))
  await expect(titleOf(page)).toHaveText(/^Cluster \d+ of \d+$/)
  await expect(page.getByTestId('hero')).toBeAttached()
  await expect(page.getByTestId('pick-btn')).toBeEnabled()
  await expect(page.locator('[data-testid="rail-thumb"][aria-current="true"]')).toHaveAttribute('data-file-id', fileId)
  await expect(page.locator('[data-app-root]')).not.toBeEmpty()
}

test('a folder added in this run: a photo opens Focus while its job runs, and after it ended', async ({ app }) => {
  const { page, server, runtime } = app
  await waitLibrary(page)
  const folder = server.generate('new-trip', 24)
  const added = await runtime.addFolderPath(folder)
  const row = libraryRow(page, added.id)
  await waitReady(row)
  expect(await runtime.waitIdle()).toBe(true)

  // New files for the next job: it runs as soon as the folder is opened.
  const more = join(folder, 'more')
  mkdirSync(more)
  const clip = join(repoRoot, 'testdata', 'media', 'clip.mov')
  for (let i = 1; i <= VIDEOS; i++) copyFileSync(clip, join(more, `clip-${String(i).padStart(4, '0')}.mov`))

  await openRow(row)
  await expect(page).toHaveURL(new RegExp(`#/folder/${added.id}$`))
  // The job is running: the Folder page's title bar shows its progress.
  await expect(page.locator('[data-titlebar] [data-testid="scan-progress"][data-state="running"]')).toBeVisible({ timeout: 30_000 })

  const first = page.getByTestId('thumb').first()
  await expect(first).toBeVisible()
  const fileId = (await first.getAttribute('data-file-id'))!
  await first.click()
  await expectFocusStage(page, added.id, fileId)
  expect(await runtime.testSupport<boolean>('WaitIdle', 0), 'the job was still running when Focus showed').toBe(false)

  // The job ends (new clusters, a scan state change) while Focus shows: still Focus.
  expect(await runtime.waitIdle(120_000)).toBe(true)
  await expectFocusStage(page, added.id, fileId)

  // Back to the folder, then the same photo once nothing runs.
  await page.keyboard.press('Escape')
  await expect(page).toHaveURL(new RegExp(`#/folder/${added.id}$`))
  await page.locator(`[data-testid="thumb"][data-file-id="${fileId}"]`).click()
  await expectFocusStage(page, added.id, fileId)
})
