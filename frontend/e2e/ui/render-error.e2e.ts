// A screen that throws while rendering, in the real app (RouteBoundary and
// clientErrors.ts): the window is never left empty, the route above takes
// over, the error toast says what happened, and choix.log gets one
// "frontend error" line. Nothing in the app makes a screen throw on
// purpose: the test corrupts one binding answer on its way to the page
// (page.route on the runtime endpoint, the request @wailsio/runtime makes),
// as a malformed payload from Go would.
//
//   Focus: one member of Catalog.Cluster's answer gets a number for a path,
//   which the rail and the stage cannot render. Focus fails, the Folder page
//   shows with the toast.
//   The Library: every row of Library.List gets a capture date that is not
//   a date. The Library fails and has no route above it: the design's error
//   screen (#40) fills the window, once, not in a loop; Copy Error Details
//   and Reload work.
import type { Page, Route } from '@playwright/test'
import { expect, test } from './fixtures'
import { libraryRow, openRow, waitLibrary, waitReady } from './screens'

/** The generated bindings' method ids (frontend/bindings/…/service.ts). */
const CATALOG_CLUSTER = 1831917792
const LIBRARY_LIST = 3873699910

/** Rewrites the JSON answer of one binding method on its way to the page. */
async function corrupt(page: Page, methodID: number, edit: (answer: unknown) => unknown): Promise<void> {
  await page.route('**/wails/runtime', async (route: Route) => {
    const body = route.request().postDataJSON() as { args?: { methodID?: number } } | null
    if (body?.args?.methodID !== methodID) return route.fallback()
    const response = await route.fetch()
    return route.fulfill({ response, json: edit(await response.json()) })
  })
}

const failedLines = (output: string, where: string) =>
  output.split('\n').filter((l) => l.includes('msg="frontend error"') && l.includes(`where=${where}`))

test('Focus fails to render: the Folder page, the error toast and a line in the log', async ({ app }) => {
  const { page, server, runtime } = app
  app.expectConsoleErrors(/screen failed to render|is not a function/)
  await waitLibrary(page)
  const added = await runtime.addFolderPath(server.generate('broken-focus', 12))
  await waitReady(libraryRow(page, added.id))

  await corrupt(page, CATALOG_CLUSTER, (answer) => {
    const a = answer as { cluster: { members: { path: unknown }[] } }
    a.cluster.members[0]!.path = 42
    return a
  })
  await openRow(libraryRow(page, added.id))
  await page.getByTestId('thumb').first().click()

  await expect(page).toHaveURL(new RegExp(`#/folder/${added.id}$`))
  await expect(page.getByTestId('cluster').first()).toBeVisible()
  // One failure, one toast (React may catch the failed render more than once).
  const toast = page.getByTestId('toast').filter({ hasText: 'Action failed' })
  await expect(toast).toHaveCount(1)
  await expect(toast).toBeVisible()
  await expect(toast).toContainText('is not a function')
  await expect.poll(() => failedLines(server.output(), 'render:focus').length).toBe(1)
})

test('the Library fails to render: the error screen, Copy Error Details, Reload, and no loop', async ({ app }) => {
  const { page, server, runtime } = app
  const failures: string[] = []
  page.on('console', (m) => {
    if (m.type() === 'error' && m.text().includes('screen failed to render')) failures.push(m.text())
  })
  app.expectConsoleErrors(/screen failed to render|Invalid time value/)
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], { origin: new URL(page.url()).origin })
  await waitLibrary(page)
  const added = await runtime.addFolderPath(server.generate('broken-library', 6))
  await waitReady(libraryRow(page, added.id))

  await corrupt(page, LIBRARY_LIST, (answer) =>
    (answer as Record<string, unknown>[]).map((row) => ({ ...row, firstCaptured: 'not a date', lastCaptured: 'not a date' })),
  )
  await page.reload()

  // The design's error screen fills the window with the error's message; no toast over it.
  const screen = page.getByTestId('app-error')
  await expect(screen).toBeVisible()
  await expect(screen).toContainText('Invalid time value')
  await expect(page.getByRole('heading', { name: /could not show this screen/ })).toBeVisible()
  await expect(page.getByTestId('toast')).toHaveCount(0)
  await expect(page.locator('[data-app-root]')).toHaveCount(1)
  expect(new URL(page.url()).hash).toMatch(/^(#\/)?$/)

  // No loop: later updates (a rescan's events, a store reload) do not render it again.
  await runtime.emitMenuAction('rescan-folder', added.id)
  expect(await runtime.waitIdle()).toBe(true)
  await page.waitForTimeout(1_000)
  expect(failures).toHaveLength(1)
  await expect(screen).toBeVisible()
  await expect.poll(() => failedLines(server.output(), 'render:library').length).toBe(1)

  // Copy Error Details: the message, the error's stack and the component stack.
  await page.getByRole('button', { name: 'Copy Error Details' }).click()
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied).toMatch(/^Invalid time value\n\nRangeError: Invalid time value/)
  // The production bundle's component names are minified: only the frames' shape is checked.
  expect(copied).toMatch(/\n\nComponents:\n\s+at /)

  // Reload: the window again, from the Library (which still fails here, so the same screen, once).
  await page.getByRole('button', { name: 'Reload' }).click()
  await expect(page.locator('[data-folder]')).toHaveCount(0)
  await expect(screen).toBeVisible()
  await expect.poll(() => failures.length).toBe(2)
  expect(new URL(page.url()).hash).toMatch(/^(#\/)?$/)
})
