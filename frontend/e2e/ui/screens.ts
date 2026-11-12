// What the UI specs share about the app's screens: the title bar's drag
// region (owner requirement: the window is draggable by the custom title
// bar on every screen), and locators for the Library, Folder and Focus.
import type { Locator, Page } from '@playwright/test'
import { expect, SCAN_MS } from './fixtures'

/** One point of the bar's empty area that something other than the bar answers for. */
export interface Covered {
  x: number
  y: number
  hit: string
}

/** A control (or an interactive element in one) whose centre something else answers for. */
export interface Obstructed {
  control: string
  hit: string
}

export interface TitleBarReport {
  bars: number
  top: number
  left: number
  width: number
  viewport: number
  /** The bar's computed padding-left: the traffic-light inset. */
  paddingLeft: string
  /** The left edge of the bar's first slot (where its content starts). */
  contentLeft: number
  drag: string
  controls: number
  /** Controls (or elements inside one) that do not compute `no-drag`. */
  draggableControls: string[]
  /** Sampled points of the bar outside every control. */
  free: number
  covered: Covered[]
  /** Controls and interactive elements in them that were hit-tested at their centre. */
  probed: number
  obstructed: Obstructed[]
}

/** What counts as interactive inside a title-bar control. */
const INTERACTIVE = 'button, a[href], input, select, textarea, [role="button"], [tabindex]:not([tabindex="-1"])'

/**
 * inspectTitleBar measures the window's drag region on the screen showing
 * (see expectDraggableTitleBar for what is asserted on it).
 */
export async function inspectTitleBar(page: Page): Promise<TitleBarReport> {
  return page.evaluate((interactive): TitleBarReport => {
    const bars = document.querySelectorAll<HTMLElement>('[data-titlebar]')
    const el = bars[0]!
    const drag = (e: Element) => getComputedStyle(e).getPropertyValue('--wails-draggable').trim()
    const describe = (e: Element | null): string => {
      if (!e) return 'nothing'
      const attrs = ['data-testid', 'data-overlay', 'aria-label', 'class']
        .map((a) => (e.getAttribute(a) ? `[${a}="${e.getAttribute(a)!.slice(0, 40)}"]` : ''))
        .join('')
      return `${e.tagName.toLowerCase()}${attrs}`
    }
    const controls = [...el.querySelectorAll<HTMLElement>('[data-titlebar-control]')]
    const draggableControls = controls
      .flatMap((c) => [c, ...c.querySelectorAll('*')])
      .filter((e) => drag(e) !== 'no-drag')
      .map(describe)

    // The controls: each control and each interactive element in it answers
    // for its own centre (it is unobstructed and can be clicked).
    const targets = controls.flatMap((c) => [c, ...c.querySelectorAll<HTMLElement>(interactive)])
    let probed = 0
    const obstructed: Obstructed[] = []
    for (const t of targets) {
      const r = t.getBoundingClientRect()
      if (r.width === 0 || r.height === 0) continue
      probed++
      const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2)
      if (!hit || !t.contains(hit)) obstructed.push({ control: describe(t), hit: describe(hit) })
    }

    // The empty area: the bar or a non-control descendant answers, computing drag.
    const rects = controls.map((c) => c.getBoundingClientRect())
    const box = el.getBoundingClientRect()
    const inControl = (x: number, y: number) => rects.some((c) => x >= c.left && x <= c.right && y >= c.top && y <= c.bottom)
    let free = 0
    const covered: Covered[] = []
    for (const y of [box.top + 2, box.top + box.height / 2, box.bottom - 2]) {
      for (let x = box.left + 1; x < box.right - 1; x += 6) {
        if (inControl(x, y)) continue
        free++
        const hit = document.elementFromPoint(x, y)
        if (!hit || !el.contains(hit) || hit.closest('[data-titlebar-control]') || drag(hit) !== 'drag') {
          covered.push({ x: Math.round(x), y: Math.round(y), hit: describe(hit) })
        }
      }
    }
    const first = el.firstElementChild
    return {
      bars: bars.length,
      top: box.top,
      left: box.left,
      width: box.width,
      viewport: window.innerWidth,
      paddingLeft: getComputedStyle(el).paddingLeft,
      contentLeft: first ? first.getBoundingClientRect().left : Number.NaN,
      drag: drag(el),
      controls: controls.length,
      draggableControls,
      free,
      covered: covered.slice(0, 10),
      probed,
      obstructed,
    }
  }, INTERACTIVE)
}

/**
 * expectDraggableTitleBar asserts the window's drag region on the screen
 * showing:
 * - exactly one `[data-titlebar]`, at the top of the window and as wide as
 *   it, computing `--wails-draggable: drag` (what Wails' runtime reads on
 *   mousedown, drag.js);
 * - its left inset the design's 76 px: the UI specs run the server build,
 *   whose System.Info() answers windowButtonsRight 0 (no window), so the app
 *   sets no --titlebar-inset (screens.e2e.ts checks both);
 * - every `[data-titlebar-control]` and everything in it computing `no-drag`;
 * - the controls unobstructed: at the centre of each control and of each
 *   interactive element in one, `document.elementFromPoint` resolves to
 *   that element or one of its descendants;
 * - the empty area uncovered: at every sampled point of the bar outside the
 *   controls (three rows, every 6 px), `document.elementFromPoint` resolves
 *   to the bar or one of its non-control descendants, which computes `drag`.
 */
export async function expectDraggableTitleBar(page: Page, screen: string): Promise<void> {
  await expect(page.locator('[data-titlebar]'), `${screen}: the title bar`).toBeVisible()
  const r = await inspectTitleBar(page)
  expect(r.bars, `${screen}: one title bar`).toBe(1)
  expect({ top: r.top, left: r.left, width: r.width }, `${screen}: the title bar spans the top of the window`).toEqual({
    top: 0,
    left: 0,
    width: r.viewport,
  })
  expect(r.paddingLeft, `${screen}: the title bar's traffic-light inset (windowButtonsRight 0: the design's)`).toBe('76px')
  expect(r.drag, `${screen}: the title bar's --wails-draggable`).toBe('drag')
  expect(r.controls, `${screen}: the title bar has controls`).toBeGreaterThan(0)
  expect(r.draggableControls, `${screen}: title bar controls that do not compute no-drag`).toEqual([])
  expect(r.probed, `${screen}: hit-tested controls`).toBeGreaterThan(r.controls)
  expect(r.obstructed, `${screen}: title bar controls answered by something else at their centre`).toEqual([])
  expect(r.free, `${screen}: sampled points of the bar's empty area`).toBeGreaterThan(20)
  expect(r.covered, `${screen}: points of the bar's empty area answered by something else`).toEqual([])
}

/** The title bar's heading (the screen's title). */
export function titleOf(page: Page): Locator {
  return page.locator('[data-titlebar] h1')
}

/** A button of the title bar, by accessible name. */
export function titleBarButton(page: Page, name: string | RegExp): Locator {
  return page.locator('[data-titlebar]').getByRole('button', { name })
}

/** The Library row of a folder (FolderRow's data-folder), or every row. */
export function libraryRow(page: Page, folderId?: string): Locator {
  return page.locator(folderId === undefined ? '[data-folder]' : `[data-folder="${folderId}"]`)
}

/** waitReady waits until the row shows the folder `ready` (its scan finished). */
export async function waitReady(row: Locator): Promise<void> {
  await expect(row).toHaveAttribute('data-folder-state', 'ready', { timeout: SCAN_MS })
}

/** openRow opens the row's folder by clicking the row (the row is one button). */
export async function openRow(row: Locator): Promise<void> {
  await row.locator(':scope > button').click()
}

/** The Folder page's clusters (ClusterSection). */
export function clusters(page: Page): Locator {
  return page.getByTestId('cluster')
}

/** A thumbnail on the Folder page, by file id. */
export function folderThumb(page: Page, fileId: string): Locator {
  return page.locator(`[data-testid="thumb"][data-file-id="${fileId}"]`)
}

/** waitFolder waits for the Folder page of `name` with its clusters shown. */
export async function waitFolder(page: Page, name: string): Promise<void> {
  await expect(page).toHaveURL(/#\/folder\/[^/]+$/)
  await expect(titleOf(page)).toHaveText(name)
  await expect(clusters(page).first()).toBeVisible({ timeout: SCAN_MS })
}

/**
 * waitLibrary waits for the Library screen. Its title bar shows no title
 * (the design keeps only a visually hidden heading), so the page still has
 * "Library" as its level-1 heading for assistive technology.
 */
export async function waitLibrary(page: Page): Promise<void> {
  await expect(page).toHaveURL(/#\/$|\/$/)
  await expect(titleOf(page)).toHaveText('Library')
  await expect(page.getByRole('heading', { level: 1, name: 'Library' })).toHaveCount(1)
}
