// The title-bar helper's own test (screens.ts): on the real Library it
// passes, and it bites — a transparent overlay over the whole bar is caught
// both over the empty area and over the controls; one over a single control
// is caught as an obstructed control; one over part of the empty area only
// as a covered point. Each overlay is removed again, and the helper passes.
// And the traffic-light inset: 76 px on the server build (no window to
// measure), following --titlebar-inset when the app sets it on #root; and
// the traffic lights' vertical centre: none on the server build, so the app
// sets no --titlebar-center (the design keeps its own alignment).
import type { Page } from '@playwright/test'
import { expect, test } from './fixtures'
import { expectDraggableTitleBar, inspectTitleBar, titleBarButton, waitLibrary } from './screens'

/** overlay adds a transparent fixed element over the given viewport box and returns its remover. */
async function overlay(page: Page, box: { x: number; y: number; width: number; height: number }): Promise<() => Promise<void>> {
  await page.evaluate((b) => {
    const d = document.createElement('div')
    d.id = 'e2e-overlay'
    d.style.cssText = `position:fixed;left:${b.x}px;top:${b.y}px;width:${b.width}px;height:${b.height}px;z-index:2147483647;background:transparent`
    document.body.appendChild(d)
  }, box)
  return () => page.evaluate(() => document.getElementById('e2e-overlay')?.remove())
}

test('the title-bar helper passes on the Library and fails under an overlay', async ({ app }) => {
  const { page } = app
  await waitLibrary(page)
  await expectDraggableTitleBar(page, 'Library')
  const bar = (await page.locator('[data-titlebar]').boundingBox())!
  const settings = (await titleBarButton(page, 'Settings').boundingBox())!

  // The whole bar: the empty area and every control are covered.
  let remove = await overlay(page, bar)
  let r = await inspectTitleBar(page)
  expect(r.covered.length, 'covered points of the empty area').toBeGreaterThan(0)
  expect(r.covered.every((c) => c.hit.startsWith('div')), 'the overlay answers').toBe(true)
  expect(r.obstructed.length, 'obstructed controls').toBe(r.probed)
  expect(r.obstructed.map((o) => o.control)).toContainEqual(expect.stringContaining('aria-label="Settings"'))
  await expect(expectDraggableTitleBar(page, 'overlay over the bar')).rejects.toThrow()
  await remove()
  await expectDraggableTitleBar(page, 'Library (overlay removed)')

  // One control only: the empty area is fine, the control is obstructed.
  remove = await overlay(page, settings)
  r = await inspectTitleBar(page)
  expect(r.covered, 'covered points of the empty area').toEqual([])
  expect(r.obstructed.map((o) => o.control)).toEqual([expect.stringContaining('aria-label="Settings"')])
  await expect(expectDraggableTitleBar(page, 'overlay over Settings')).rejects.toThrow(/answered by something else at their centre/)
  await remove()

  // Part of the empty area only (just right of the traffic-light inset, left of the leading control).
  remove = await overlay(page, { x: bar.x + 4, y: bar.y, width: 40, height: bar.height })
  r = await inspectTitleBar(page)
  expect(r.covered.length, 'covered points of the empty area').toBeGreaterThan(0)
  expect(r.obstructed, 'obstructed controls').toEqual([])
  await expect(expectDraggableTitleBar(page, 'overlay over the empty area')).rejects.toThrow(/empty area answered by something else/)
  await remove()

  await expectDraggableTitleBar(page, 'Library (overlays removed)')
})

test("the title bar's inset is the design's on the server build and follows --titlebar-inset on #root", async ({ app }) => {
  const { page, runtime } = app
  await waitLibrary(page)
  const info = await runtime.systemInfo()
  expect(info.windowButtonsRight, 'System.Info().windowButtonsRight on the server build').toBe(0)
  expect(info.windowFullScreen, 'System.Info().windowFullScreen on the server build').toBe(false)
  const rootInset = () => page.evaluate(() => document.getElementById('root')!.style.getPropertyValue('--titlebar-inset'))
  expect(await rootInset(), 'the app sets no --titlebar-inset when the buttons are unknown').toBe('')
  const before = await inspectTitleBar(page)
  expect(before.paddingLeft).toBe('76px')

  // What the desktop app sets once System.Info() reports buttons ending at x = 79.
  await page.evaluate(() => document.getElementById('root')!.style.setProperty('--titlebar-inset', '91px'))
  const after = await inspectTitleBar(page)
  expect(after.paddingLeft, 'the inset follows the property').toBe('91px')
  expect(after.contentLeft - before.contentLeft, "the bar's content moves with it").toBe(15)
  expect(after.covered, 'the empty area is still the drag region').toEqual([])

  await page.evaluate(() => document.getElementById('root')!.style.removeProperty('--titlebar-inset'))
  expect((await inspectTitleBar(page)).paddingLeft).toBe('76px')
  await expectDraggableTitleBar(page, 'Library (property removed)')
})

test("the title bar's vertical centre: the server build measures none and the app sets no --titlebar-center", async ({ app }) => {
  const { page, runtime } = app
  await waitLibrary(page)
  // fixtures.ts types only the fields its own tests read; the binding answers the whole Info.
  const info = (await runtime.systemInfo()) as { windowButtonsRight: number; windowButtonsCenterY?: unknown }
  expect(info.windowButtonsCenterY, 'System.Info().windowButtonsCenterY on the server build').toBe(0)
  const rootCenter = () => page.evaluate(() => document.getElementById('root')!.style.getPropertyValue('--titlebar-center'))
  expect(await rootCenter(), 'the app sets no --titlebar-center when the buttons are unknown').toBe('')
  // The window "shown" again makes the app ask again: still none.
  await runtime.emit('common:WindowShow')
  await page.waitForTimeout(500)
  expect(await rootCenter()).toBe('')
  await expectDraggableTitleBar(page, 'Library')
})
