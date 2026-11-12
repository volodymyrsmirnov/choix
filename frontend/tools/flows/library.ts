// Library flows: the filled Library → hover a row → open its ⋯ menu → Escape
// → Remove from Library… → the alert → Cancel (then Remove for real); drag
// over → drop (by navigation: LibraryProps has no drag callbacks until spike
// S1); Open Picks → its toast → dismissed; every seeded toast closed.
//
// The plan's "toast → Undo" has no Library state to run on: no Library toast
// carries Undo (removal is confirmed by an alert instead). Toasts are
// dismissed here; the Focus flows cover Undo.
//
// The toolbar: ⌘F focuses the search, a search narrows the rows, ↓ moves to
// the first row, Esc clears; no match → Clear Search; the Show menu (Escape,
// then Needs attention → nothing to show → Show All Folders); the Sort menu
// (click-away, then Name: one list without month headers).
//
// Links out: the Dolomites row and its menu's Open lead to its folder page
// (s203; the other rows say they have none), the title
// bar's help and gear to the shortcut sheet (s600) and Settings (s500).
import { expect, type Page } from '@playwright/test'
import type { ProtoHandle } from '../flows.pw'
import { landOn } from './landing'

const at = (page: Page, n: number) => expect(page).toHaveURL(new RegExp(`#s${n}$`))

export default async function library(page: Page, proto: ProtoHandle): Promise<void> {
  // Filled; hovering a row shows its actions.
  await proto.open(102)
  const row = page.locator('[data-folder="dolomites"]')
  const more = page.getByRole('button', { name: 'More actions for Dolomites' })
  await row.hover()
  await expect(page.getByRole('button', { name: 'Open Picks of Dolomites' })).toBeVisible()
  await expect.poll(() => more.evaluate((el) => getComputedStyle(el.parentElement!).opacity)).toBe('1')

  // The row menu, closed with Escape.
  await more.click()
  const menu = page.getByRole('menu', { name: 'Actions for Dolomites' })
  await expect(menu).toBeVisible()
  await expect(more).toHaveAttribute('aria-expanded', 'true')
  await page.keyboard.press('Escape')
  await expect(menu).toHaveCount(0)

  // Remove → the alert → Cancel keeps the folder.
  await more.click()
  await menu.getByRole('menuitem', { name: 'Remove from Library…' }).click()
  await expect(menu).toHaveCount(0)
  const alert = page.getByRole('alertdialog', { name: 'Remove “Dolomites” from Choix?' })
  await expect(alert).toBeVisible()
  await alert.getByRole('button', { name: 'Cancel' }).click()
  await expect(alert).toHaveCount(0)
  await expect(row).toBeVisible()

  // Remove → the alert → Remove forgets it.
  await more.click()
  await menu.getByRole('menuitem', { name: 'Remove from Library…' }).click()
  await alert.getByRole('button', { name: 'Remove' }).click()
  await expect(alert).toHaveCount(0)
  await expect(row).toHaveCount(0)

  // Open Picks shows a toast; its Dismiss closes it.
  await proto.open(102)
  await page.locator('[data-folder="kyoto"]').hover()
  await page.getByRole('button', { name: 'Open Picks of Kyoto' }).click()
  const picksToast = page.locator('[data-overlay="toast:picks-kyoto"]')
  await expect(picksToast).toBeVisible()
  await picksToast.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(picksToast).toHaveCount(0)

  // Drag over → drop: the empty Library, the drop target, then the new
  // folder scanning beside the others.
  await proto.open(100)
  await expect(page.getByRole('heading', { name: 'Add a folder of photos' })).toBeVisible()
  await proto.open(101)
  await expect(page.getByRole('heading', { name: 'Drop to add the folder' })).toBeVisible()
  await proto.open(105)
  await expect(page.locator('[data-folder="lisbon"]')).toHaveAttribute('data-folder-state', 'scanning')
  await expect(page.locator('[data-folder="kyoto"]')).toHaveAttribute('data-folder-state', 'queued')

  // Add Folder on a filled Library leads to the same scanning state.
  await proto.open(102)
  await page.getByRole('button', { name: /^Add Folder/ }).click()
  await at(page, 105)

  // The seeded toasts: dismissed, closed with Escape, dismissed.
  await proto.open(113)
  const removed = page.locator('[data-overlay="toast:removed-kyoto"]')
  await removed.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(removed).toHaveCount(0)

  await proto.open(114)
  const already = page.locator('[data-overlay="toast:already-lisbon"]')
  await expect(already).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(already).toHaveCount(0)

  await proto.open(115)
  const readOnly = page.locator('[data-overlay="toast:read-only-card"]')
  await readOnly.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(readOnly).toHaveCount(0)

  // The toolbar's search: ⌘F, a match, ↓ to the first row, Esc clears.
  await proto.open(102)
  const rows = page.locator('[data-folder]')
  const search = page.getByTestId('filter-input')
  await page.keyboard.press('Meta+f')
  await expect(search).toBeFocused()
  await search.fill('is')
  await expect(rows).toHaveCount(2)
  await expect(page.getByRole('heading', { name: '2 of 6 folders' })).toBeVisible()
  await page.keyboard.press('ArrowDown')
  await expect(page.locator('[data-folder="lisbon"] > button')).toBeFocused()
  await expect(page.locator('[data-folder="lisbon"] > button')).toHaveAttribute('aria-current', 'true')
  await search.focus()
  await page.keyboard.press('Escape')
  await expect(search).toHaveValue('')
  await expect(rows).toHaveCount(6)
  // No match, and the way back.
  await search.fill('harbour')
  await expect(page.getByTestId('folders-empty')).toBeVisible()
  await expect(rows).toHaveCount(0)
  await page.getByRole('button', { name: 'Clear Search' }).click()
  await expect(rows).toHaveCount(6)

  // The Show menu: closed with Escape, then Needs attention (none here) and back.
  const show = page.getByTestId('library-show')
  const showMenu = page.getByRole('menu', { name: 'Show' })
  await show.click()
  await expect(showMenu).toBeVisible()
  await expect(show).toHaveAttribute('aria-expanded', 'true')
  await page.keyboard.press('Escape')
  await expect(showMenu).toHaveCount(0)
  await expect(show).toBeFocused()
  await show.click()
  await showMenu.getByRole('menuitemradio', { name: 'Needs attention' }).click()
  await expect(showMenu).toHaveCount(0)
  await expect(page.getByTestId('folders-empty')).toContainText('No folder needs attention')
  await page.getByRole('button', { name: 'Show All Folders' }).click()
  await expect(rows).toHaveCount(6)
  await expect(show).toContainText('All folders')

  // The Sort menu: closed by a click elsewhere, then Name: one list, no months.
  const sort = page.getByTestId('library-order')
  const sortMenu = page.getByRole('menu', { name: 'Sort folders' })
  await sort.click()
  await expect(sortMenu).toBeVisible()
  await page.getByRole('heading', { name: '6 folders' }).click()
  await expect(sortMenu).toHaveCount(0)
  await sort.click()
  await sortMenu.getByRole('menuitemradio', { name: 'Name — A to Z' }).click()
  await expect(sortMenu).toHaveCount(0)
  await expect(sort).toContainText('Name')
  await expect(rows.first()).toHaveAttribute('data-folder', 'dolomites')
  await expect(page.locator('[data-month]')).toHaveCount(0)

  // The toolbar states open on their own too.
  await proto.open(126)
  await expect(sortMenu).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(sortMenu).toHaveCount(0)
  await proto.open(127)
  await expect(showMenu).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(showMenu).toHaveCount(0)

  // Links out of the Library land on the other groups' real states.
  await proto.open(102)
  await page.locator('[data-folder="dolomites"] > button').click()
  await landOn(page, 203)
  await proto.open(102)
  await page.locator('[data-folder="dolomites"]').hover()
  await page.getByRole('button', { name: 'More actions for Dolomites' }).click()
  await page.getByRole('menu', { name: 'Actions for Dolomites' }).getByRole('menuitem', { name: 'Open', exact: true }).click()
  await landOn(page, 203)
  // A folder without a page in the prototype says so; its toast is dismissed.
  await proto.open(102)
  await page.locator('[data-folder="kyoto"] > button').click()
  const noPage = page.locator('[data-overlay="toast:no-page-kyoto"]')
  await expect(noPage).toBeVisible()
  await noPage.getByRole('button', { name: /^Dismiss notification/ }).click()
  await expect(noPage).toHaveCount(0)
  await proto.open(102)
  await page.getByRole('button', { name: 'Keyboard shortcuts' }).click()
  await landOn(page, 600)
  await proto.open(102)
  await page.getByRole('button', { name: 'Settings' }).click()
  await landOn(page, 500)
}
