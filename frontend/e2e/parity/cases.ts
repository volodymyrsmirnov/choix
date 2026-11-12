// The parity case of every approved state whose group is enabled
// (enabled.json): how to drive the real app into the state, or why it has no
// app counterpart (`skip`). parity.pw.ts fails with "no parity case for #N"
// for an approved state of an enabled group that has neither.
//
// Adding a case (engineering plan, Tasks 53–61): write the group's fixture
// (fixtures/<group>.ts, listed in fixtures/table.ts), add
// `{ state: n, drive: loadFixture(n) }` here — loadFixture is
// `page.evaluate((f) => window.__choixParity.load(f), fixtureFor(n))` — plus
// what reaches the state after that the way a user or Go would: an event
// (emitEvent), or `interact` where the prototype state draws a forced look
// (data-force); then add the group to enabled.json.
//
// Forced looks: the app never sets data-force, so `interact` is the only way
// a case reproduces one. A real hover also hovers every ancestor, so an
// ancestor's :hover look is a real difference: the prototype state must
// force the same ancestors (as the `group-hover` twins expect).
import { readFileSync } from 'node:fs'
import type { Page } from '@playwright/test'
import { mediaFor } from '../../src/app/shell/media'
import type { Member } from '../../src/design/types'
import { ERROR_SCREEN_STATE, RENDER_ERROR_KEY } from './errorScreen'
import { fixtureFor } from './fixtures'
import type { ParityCase } from './parity'

type Drive = NonNullable<ParityCase['drive']>

/** Loads state `n`'s fixture into the app (fixtures.ts, mock-bindings/control.ts). */
export const loadFixture =
  (n: number): Drive =>
  (page: Page) =>
    page.evaluate((f) => window.__choixParity.load(f), fixtureFor(n))

/** Sends an event as Go would (mock-bindings/runtime.ts delivers it to the app's subscribers). */
export const emitEvent = (page: Page, name: string, data: unknown): Promise<void> =>
  page.evaluate(([n, d]) => window.__choixParity.emit(n, d), [name, data] as const)

/**
 * Overlays (600–605), over the filled Library: how each is driven. The sheet
 * opens the way the native menu opens it (Help ▸ Keyboard Shortcuts sends
 * `menu:action` `shortcuts`); the toasts are the fixture's.
 */
export const OVERLAY_DRIVES: Record<number, Drive> = {
  600: async (page) => {
    await loadFixture(600)(page)
    await emitEvent(page, 'menu:action', { id: 'shortcuts' })
  },
  601: loadFixture(601),
  602: loadFixture(602),
  603: loadFixture(603),
  604: loadFixture(604),
  605: loadFixture(605),
}

/**
 * Why 600–605 are skipped. The overlay scenarios (prototype/scenarios/
 * overlays.tsx, OverLibrary) draw LibraryScreen and the sheet inside an extra
 * `div.relative.flex.flex-col.h-full.min-h-0` in AppRoot; the Library states
 * (100–122; 113–115 with a toast) draw LibraryScreen straight in AppRoot. The
 * app draws one tree for the Library with a toast (or the sheet), so it
 * cannot match both; the fix is the prototype's (the owner's). Driven by
 * OVERLAY_DRIVES with that wrapper added to the app, all six matched at every
 * size (Task 53 report): when the wrapper goes, each case becomes
 * `{ state, drive: OVERLAY_DRIVES[state] }`.
 */
export const OVERLAY_WRAPPER_SKIP =
  'prototype wraps LibraryScreen (and the sheet) in an extra div.relative.flex.flex-col.h-full.min-h-0 that the Library states (100–122) do not have; the app cannot match both'

export const OVERLAY_CASES: ParityCase[] = Object.keys(OVERLAY_DRIVES).map((n) => ({ state: Number(n), skip: OVERLAY_WRAPPER_SKIP }))

/**
 * Native surfaces (700–799): the macOS menu bar, context menus and alerts
 * are drawn by the system in the app; the prototype's replicas have no DOM
 * counterpart. (gate.py treats the native group as always satisfied.)
 */
export const NATIVE_STATES = [700, 701, 702, 703, 704, 705, 706, 707, 708, 709, 710, 711, 712, 720]

/**
 * The component gallery (800–899): a sheet of every component in every
 * look, which exists only in the prototype. (gate.py never implies the
 * gallery group.)
 */
export const GALLERY_STATES = [800, 801, 802, 803, 804, 805, 806, 807, 808, 809]

export const NATIVE_SKIP = 'native surface'
export const GALLERY_SKIP = 'component gallery: no app counterpart'

// ── Welcome (30–37) and Library (100–131) ──────────────────────────────────
//
// The fixtures (fixtures/welcome.ts, fixtures/library.ts) carry each state's
// data as Go answers it. What the prototype passes as a prop and the app
// takes from the user or the window is done here, the real way:
//   - the selected row: keyboard focus on the row selects it (FolderRow's
//     onFocus); where the state shows the selection without the focus, the
//     focus then leaves the row, as when the user clicks elsewhere;
//   - a row's hover or focus look (forceRow): a real hover or focus;
//   - a drag over the window (101): the class Wails' runtime puts on the
//     drop target (#root) while files are over the window, which the
//     container follows (library/windowSources.ts);
//   - a run that stopped (32): Go's events for a failed job;
//   - the toolbar (124–130): the search typed into the field, which then
//     loses focus; a filter or an order chosen with its menu (the button
//     clicked, then the item); a menu left open (126, 127): its button
//     clicked.
// The toasts of 113–115 are the fixture's, seeded (shell/parityUi.ts): the
// mock bindings answer neither AddFolders nor OpenPicksFolder with the
// result that puts them up, and a toast the app pushes leaves after 4 s,
// which a comparison could outlast.
// libraryCases.test.ts holds LIBRARY_STEPS to the scenarios' props.

/** What a welcome or Library state needs beyond its fixture; each runs after the load, in this order. */
export interface LibrarySteps {
  /** The scanning folder's job fails, with this row text (Go's events). */
  stopRun?: string
  /** Files are dragged over the window. */
  dragOver?: true
  /** This row is selected (focused, then the focus leaves it). */
  select?: string
  /** This row is hovered (a forced look: `interact`). */
  hover?: string
  /** This row has keyboard focus, and so is selected (a forced look: `interact`). */
  focus?: string
  /** This text is typed into the toolbar's search field. */
  query?: string
  /** This filter is chosen in the Show menu (its item's label). */
  show?: string
  /** This order is chosen in the Sort menu (its item's label). */
  order?: string
  /** This toolbar menu is left open. */
  menu?: 'order' | 'show'
}

/** The states that need more than their fixture. */
export const LIBRARY_STEPS: Record<number, LibrarySteps> = {
  32: { stopRun: 'Scan stopped. Rescan to finish it.' },
  101: { dragOver: true },
  103: { hover: 'dolomites' },
  104: { focus: 'kyoto' },
  114: { select: 'lisbon' },
  115: { select: 'card-a' },
  124: { query: 'is' },
  125: { query: 'harbour' },
  126: { menu: 'order' },
  127: { menu: 'show' },
  128: { order: 'Name — A to Z' },
  129: { show: 'Needs attention' },
  130: { show: 'Needs attention' },
}

/** The toolbar's menu buttons (LibraryFilterBar). */
const LIBRARY_MENU_BUTTON = { order: '[data-testid="library-order"]', show: '[data-testid="library-show"]' } as const

/** A real click on the element, without the pointer over it or focus on it. */
const clickLibrary = async (page: Page, selector: string, hasText?: string) => {
  const el = page.locator(selector, hasText === undefined ? {} : { hasText }).first()
  await el.waitFor()
  await el.evaluate((node: HTMLElement) => node.click())
}

/** A Library row's control (the row is one button). */
const rowButton = (folderId: string) => `[data-folder="${folderId}"] > button`

/** Keyboard focus on a row, as Tab gives it (without scrolling: the prototype states show the list unscrolled). */
const focusRow =
  (folderId: string): Drive =>
  async (page) => {
    await page.locator(rowButton(folderId)).evaluate((el: HTMLElement) => el.focus({ preventScroll: true }))
  }

/** The row selected: focused (which selects it), then the focus leaves it. */
const selectRow =
  (folderId: string): Drive =>
  async (page) => {
    await focusRow(folderId)(page)
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  }

const hoverRow =
  (folderId: string): Drive =>
  async (page) => {
    await page.hover(rowButton(folderId))
  }

/** What Wails' runtime does to the drop target (#root) while files are dragged over the window. */
const dragOverWindow: Drive = async (page) => {
  await page.evaluate(() => {
    document.querySelector('[data-file-drop-target]')?.classList.add('file-drop-target-active')
  })
}

/**
 * The fixture lists the folder scanning; then Go's events for its job
 * failing: the run's last progress with state `stopped` (phase `failed`,
 * session/queue.go emitFinalLocked), then scan:state `error` with `text`.
 */
const stopRun =
  (n: number, text: string): Drive =>
  async (page) => {
    const run = (fixtureFor(n).folders as { id: string; state: string; scan?: object }[]).find((f) => f.state === 'scanning')
    if (!run?.scan) throw new Error(`parity: #${n} fixture lists no scanning folder`)
    await page.locator(`[data-folder="${run.id}"][data-folder-state="scanning"]`).waitFor()
    await emitEvent(page, 'scan:progress', { folderId: run.id, progress: { ...run.scan, state: 'stopped' }, phase: 'failed' })
    await emitEvent(page, 'scan:state', { folderId: run.id, state: 'error', error: text })
  }

/** Runs `steps` in order. */
const inOrder =
  (steps: Drive[]): Drive =>
  async (page) => {
    for (const step of steps) await step(page)
  }

/** The registered welcome and Library states drawn by the Library screen (the welcome group's 40 is the error screen, below). */
export const WELCOME_STATES = [30, 31, 32, 33, 34, 35, 36, 37]
/** 100–131; 112 and 120 (the model-download banner) are retired. */
export const LIBRARY_STATES = Array.from({ length: 32 }, (_, i) => 100 + i).filter((n) => n !== 112 && n !== 120)

/** State `n`'s case: its fixture, then its steps (LIBRARY_STEPS). */
export function libraryCase(n: number): ParityCase {
  const s = LIBRARY_STEPS[n] ?? {}
  const drive: Drive[] = [loadFixture(n)]
  if (s.stopRun !== undefined) drive.push(stopRun(n, s.stopRun))
  if (s.dragOver) drive.push(dragOverWindow)
  if (s.select !== undefined) drive.push(selectRow(s.select))
  if (s.query !== undefined) {
    const query = s.query
    drive.push(async (page) => {
      const field = page.locator('[data-testid="filter-input"]')
      await field.fill(query)
      await field.evaluate((el: HTMLElement) => el.blur())
    })
  }
  for (const [menu, label] of [['show', s.show], ['order', s.order]] as const) {
    if (label === undefined) continue
    drive.push(async (page) => {
      await clickLibrary(page, LIBRARY_MENU_BUTTON[menu])
      await clickLibrary(page, '[role="menuitemradio"]', label)
    })
  }
  if (s.menu) {
    const button = LIBRARY_MENU_BUTTON[s.menu]
    drive.push(async (page) => {
      await clickLibrary(page, button)
      await page.locator('[role="menu"]').waitFor()
    })
  }
  const interact: Drive[] = []
  if (s.hover !== undefined) interact.push(hoverRow(s.hover))
  if (s.focus !== undefined) interact.push(focusRow(s.focus))
  return { state: n, drive: inOrder(drive), ...(interact.length > 0 ? { interact: inOrder(interact) } : {}) }
}

export const LIBRARY_CASES: ParityCase[] = [...WELCOME_STATES, ...LIBRARY_STATES].map(libraryCase)

// ── The error screen (40) ───────────────────────────────────────────────────
//
// The welcome group's one state that is not the Library: the full-window
// error screen the app shows when a screen failed to render and returning to
// the Library failed too. No binding answer, event or input reaches it
// (forcing a real render error would test the bug, not the screen), so the
// fixture hands the app the error the screen shows through the parity
// channel: `ui['shell.renderError']`, the error's message (fixtures/
// welcome.ts). The shell (src/app/shell) reads it with uiOverride and, when it
// is a string, shows the error screen with it in place of the routed screen.

export { ERROR_SCREEN_STATE, RENDER_ERROR_KEY }

export const SHELL_CASES: ParityCase[] = [{ state: ERROR_SCREEN_STATE, drive: loadFixture(ERROR_SCREEN_STATE) }]

// ── Folder (200–231) ────────────────────────────────────────────────────────
//
// The fixtures (fixtures/folder.ts) carry each state's data as Go answers it:
// the grid (Catalog.Open), the folder's Library row, the settings, the
// quality worker, the toasts of 222 and
// 223 (seeded, see the fixture's header), and how Catalog.Recluster answers
// in 215 and 225. What the prototype passes as a prop and the app keeps as
// the page's own state, or hears from Go later, is done here, the real way:
//   - the filter text: typed into the filter field, which then loses focus;
//   - a camera or sort chosen: the menu opened with its button and the item
//     clicked; a menu left open (206, 218): its button clicked;
//   - the keyboard cursor (207): ↓ pressed;
//   - a run that stopped (209): Go's events for a failed job;
//   - the drive going away while the folder is open (224): Go's
//     library:changed `offline` for the folder;
//   - a re-cluster in flight (215): Re-cluster pressed, Recluster not
//     answering yet; the read-only notice (225): Re-cluster pressed,
//     Recluster refused with `read_only` (the one way the app learns of a
//     read-only drive), then the "Re-cluster failed" toast dismissed.
// Buttons and items are clicked with element.click(): a real click event
// without moving the pointer over them or focusing them, so no hover or
// focus look the prototype state does not show is left behind.
// folderCases.test.ts holds FOLDER_STEPS to the scenarios' props.

/** What a Folder state needs beyond its fixture; each runs after the grid (or the run) shows, in this order. */
export interface FolderSteps {
  /** The scanning folder's job fails, with this row text (Go's events). */
  stopRun?: string
  /** The folder's drive goes away (library:changed offline). */
  offline?: true
  /** Re-cluster is pressed (the fixture's `calls` says how Recluster answers). */
  recluster?: true
  /** The toast the step before put up is dismissed. */
  dismissToast?: true
  /** This text is typed into the filter field. */
  query?: string
  /** This camera is chosen in the camera menu. */
  device?: string
  /** This order is chosen in the sort menu (its item's label). */
  sort?: string
  /** This menu is left open. */
  menu?: 'device' | 'sort'
  /** ↓ is pressed this many times (the keyboard cursor). */
  down?: number
}

export const FOLDER_STEPS: Record<number, FolderSteps> = {
  204: { query: 'Sep 3' },
  205: { query: 'harbour' },
  206: { menu: 'device' },
  207: { down: 1 },
  209: { stopRun: 'Scan stopped. Rescan to finish it.' },
  213: { sort: 'Capture time — latest first' },
  215: { recluster: true },
  218: { menu: 'sort' },
  219: { device: 'iPhone 15 Pro' },
  224: { offline: true },
  225: { recluster: true, dismissToast: true },
}

const GRID = '[data-testid="clusters"] [data-testid="cluster"]'
const MENU_BUTTON = { device: '[data-testid="device-filter"]', sort: '[data-testid="sort-toggle"]' } as const

/** A real click on the element, without the pointer over it or focus on it. */
const clickEl = async (page: Page, selector: string, hasText?: string) => {
  const el = page.locator(selector, hasText === undefined ? {} : { hasText }).first()
  await el.waitFor()
  await el.evaluate((node: HTMLElement) => node.click())
}

/** The folder of state `n`'s fixture (its route's id). */
const folderOf = (n: number): string => decodeURIComponent(fixtureFor(n).route.replace(/^#\/folder\//, ''))

/** Go's events for the folder's job failing (as stopRun does for a Library row). */
const stopFolderRun =
  (n: number, text: string): Drive =>
  async (page) => {
    const run = (fixtureFor(n).folders as { id: string; scan?: object }[])[0]
    if (!run?.scan) throw new Error(`parity: #${n} fixture's folder is not scanning`)
    await page.locator('[data-testid="scan-progress"]').waitFor()
    await emitEvent(page, 'scan:progress', { folderId: run.id, progress: { ...run.scan, state: 'stopped' }, phase: 'failed' })
    await emitEvent(page, 'scan:state', { folderId: run.id, state: 'error', error: text })
  }

/** State `n`'s case: its fixture, then its steps (FOLDER_STEPS). */
export function folderCase(n: number): ParityCase {
  const s = FOLDER_STEPS[n] ?? {}
  const drive: Drive[] = [loadFixture(n)]
  const needsGrid = s.offline || s.recluster || s.query !== undefined || s.device !== undefined || s.sort !== undefined || s.menu || s.down
  if (needsGrid) drive.push((page) => page.locator(GRID).first().waitFor())
  if (s.stopRun !== undefined) drive.push(stopFolderRun(n, s.stopRun))
  if (s.offline) {
    drive.push(async (page) => {
      await emitEvent(page, 'library:changed', { reason: 'offline', folderId: folderOf(n) })
      await page.locator('[data-testid="notice-offline"]').waitFor()
    })
  }
  if (s.recluster) drive.push((page) => clickEl(page, '[data-testid="recluster"]'))
  if (s.dismissToast) drive.push((page) => clickEl(page, '[data-testid="toast"] button[title="Dismiss"]'))
  if (s.query !== undefined) {
    const query = s.query
    drive.push(async (page) => {
      const field = page.locator('[data-testid="filter-input"]')
      await field.fill(query)
      await field.evaluate((el: HTMLElement) => el.blur())
    })
  }
  if (s.device !== undefined) {
    const device = s.device
    drive.push(async (page) => {
      await clickEl(page, MENU_BUTTON.device)
      await clickEl(page, '[role="menuitemradio"]', device)
    })
  }
  if (s.sort !== undefined) {
    const sort = s.sort
    drive.push(async (page) => {
      await clickEl(page, MENU_BUTTON.sort)
      await clickEl(page, '[role="menuitemradio"]', sort)
    })
  }
  if (s.menu) {
    const button = MENU_BUTTON[s.menu]
    drive.push(async (page) => {
      await clickEl(page, button)
      await page.locator('[role="menu"]').waitFor()
    })
  }
  if (s.down) {
    const times = s.down
    drive.push(async (page) => {
      for (let i = 0; i < times; i++) await page.keyboard.press('ArrowDown')
    })
  }
  return { state: n, drive: inOrder(drive) }
}

/** The registered Folder states: 200–231; 216 and 228 (the notices of a model download) are retired. */
export const FOLDER_STATES = Array.from({ length: 32 }, (_, i) => 200 + i).filter((n) => n !== 216 && n !== 228)

export const FOLDER_CASES: ParityCase[] = FOLDER_STATES.map(folderCase)

// ── Focus (300–318) and Compare (400–409) ──────────────────────────────────
//
// The fixtures (fixtures/focus.ts, fixtures/compare.ts) carry each state's
// data as Go answers it: the cluster (Catalog.Cluster: pending in 300,
// refused with the state's message in 316), the folder as the Folder page
// shows it, and a pick kept in flight (315, 409). What the prototype passes
// as a prop and the app keeps as the page's own state is done here, the
// real way, through the app's one key dispatcher and the screen's controls:
//   - the Info panel: the stored preference (localStorage
//     "choix.focus.showInfo"), closed ("0") unless the state shows it open
//     ("1"), set before the fixture opens Focus, as a returning user has it;
//   - the photo on screen: → or ← pressed (whichever is fewer; both wrap);
//   - compare: C pressed on the first pane's photo, the other panes' rail
//     thumbnails clicked in pane order (a click on the first one's removes
//     it: no panes, 407), then the active pane's digit key;
//   - zoom: Z (Pixel-peep, 2×) or = pressed (×1.5 a step, up to 8×);
//   - the pan: the photo dragged with the mouse (a few short drags from the
//     middle of the stage that add up to the state's offset), the pointer
//     then rested at that middle, over the photo, where nothing has a hover
//     look. The photo's transform is a measured value (src/design/
//     measured.ts, focus-zoom), so the offset itself is not compared;
//   - a pick in flight: Space or X pressed, Catalog.SetPick not answering.
// Rail thumbnails are clicked with element.click() (no hover, no focus).
// The media come from serve-app.mjs (/media/ by content hash), so the hero's
// full-size image really loads and the app reports it `ready` from its own
// load event; in 313 the browser's request for it is held, and the case
// declares that URL pending (pendingFullCase). 312 is skipped
// (BROKEN_IMAGE_SKIP), its drive kept in FOCUS_KEPT_DRIVES.
// focusCases.test.tsx holds FOCUS_STEPS to the scenarios' props.

/** What a Focus or Compare state needs beyond its fixture; each runs after the cluster shows, in this order. */
export interface FocusSteps {
  /** The Info panel is open (the stored preference); closed otherwise. */
  info?: true
  /** The photo on screen (member index), reached with → / ←. */
  photo?: number
  /** Compare entered on `photo`, these panes (member indices) in order, this one active. */
  compare?: { panes: number[]; active: number }
  /** Z pressed: Pixel-peep. */
  peep?: true
  /** = pressed this many times. */
  zoomIn?: number
  /** The photo dragged by this offset, in px. */
  pan?: { x: number; y: number }
  /** Space (`pick`) or X (`reject`) pressed on the photo or active pane; SetPick stays in flight. */
  mark?: 'pick' | 'reject'
}

export const FOCUS_STEPS: Record<number, FocusSteps> = {
  302: { photo: 1 },
  303: { photo: 3 },
  304: { info: true },
  305: { info: true, photo: 2 },
  307: { peep: true },
  308: { zoomIn: 6, pan: { x: -960, y: 440 } },
  310: { photo: 7 },
  312: { photo: 2 },
  313: { photo: 4 },
  314: { photo: 5 },
  315: { mark: 'pick' },
  318: { photo: 31 },
  400: { compare: { panes: [0, 2], active: 0 } },
  401: { compare: { panes: [0, 2, 5], active: 2 } },
  402: { compare: { panes: [0, 2, 5, 4], active: 0 } },
  403: { compare: { panes: [0, 1], active: 1 } },
  404: { compare: { panes: [0, 2, 6], active: 0 }, zoomIn: 2, pan: { x: -140, y: 60 } },
  405: { compare: { panes: [0, 1], active: 1 } },
  406: { photo: 2, compare: { panes: [2], active: 2 } },
  407: { compare: { panes: [], active: 0 } },
  408: { photo: 2, compare: { panes: [2, 3], active: 3 } },
  409: { compare: { panes: [0, 2, 5], active: 2 }, mark: 'pick' },
}

/** The Info panel's stored preference (focus/prefs.ts). */
export const SHOW_INFO_KEY = 'choix.focus.showInfo'

/** The longest drag, per axis, that stays on the stage at every review size. */
export const DRAG_STEP = { x: 160, y: 100 }

/**
 * The drags that add up to `pan`: the fewest whose steps stay within
 * DRAG_STEP, in whole pixels (the first ones one pixel longer where the
 * offset does not divide evenly).
 */
export function dragSteps(pan: { x: number; y: number }): { x: number; y: number }[] {
  const n = Math.max(1, Math.ceil(Math.abs(pan.x) / DRAG_STEP.x), Math.ceil(Math.abs(pan.y) / DRAG_STEP.y))
  const split = (total: number) => {
    const base = Math.trunc(total / n)
    const rest = total - base * n
    return Array.from({ length: n }, (_, i) => base + (i < Math.abs(rest) ? Math.sign(rest) : 0))
  }
  const xs = split(pan.x)
  const ys = split(pan.y)
  return xs.map((x, i) => ({ x, y: ys[i]! }))
}

/** The keys that move from photo `from` to `to` in a cluster of `n` (both directions wrap; the fewer presses). */
export function photoKeys(from: number, to: number, n: number): string[] {
  const forward = (to - from + n) % n
  return forward <= n - forward ? Array(forward).fill('ArrowRight') : Array(n - forward).fill('ArrowLeft')
}

/** One thing the case does to the app, in order (focusPlan). */
export type FocusAction =
  /** A key pressed (Playwright's key name), through the app's key dispatcher. */
  | { key: string }
  /** A rail thumbnail (member index) clicked. */
  | { rail: number }
  /** The photo (or the active pane) dragged by this many px. */
  | { drag: { x: number; y: number } }

/** Playwright's key for each step. */
export const FOCUS_KEYS = { compare: 'c', peep: 'z', zoomIn: 'Equal', pick: 'Space', reject: 'x' } as const

/** What `s` does after the cluster shows, for a cluster of `members` (pure: focusCases.test.tsx runs the same plan). */
export function focusPlan(s: FocusSteps, members: number): FocusAction[] {
  const out: FocusAction[] = []
  const keys = (list: string[]) => {
    for (const key of list) out.push({ key })
  }
  if (s.photo !== undefined) keys(photoKeys(0, s.photo, members))
  if (s.compare) {
    const { panes, active } = s.compare
    keys([FOCUS_KEYS.compare])
    // No panes: the photo compare started on is clicked off again.
    for (const i of panes.length === 0 ? [s.photo ?? 0] : panes.slice(1)) out.push({ rail: i })
    const pane = panes.indexOf(active)
    if (pane > 0) keys([String(pane + 1)])
  }
  if (s.peep) keys([FOCUS_KEYS.peep])
  if (s.zoomIn) keys(Array(s.zoomIn).fill(FOCUS_KEYS.zoomIn))
  if (s.pan) for (const d of dragSteps(s.pan)) out.push({ drag: d })
  if (s.mark) keys([FOCUS_KEYS[s.mark]])
  return out
}

const STAGE = '[data-testid="stage-controls"]'

/** The number of members of state `n`'s cluster (its fixture's Catalog.Cluster answer); 0 without one. */
const membersOf = (n: number): number => {
  const focus = fixtureFor(n).focus as { cluster: { members: unknown[] } } | 'pending' | undefined
  return typeof focus === 'object' ? focus.cluster.members.length : 0
}

/**
 * Runs `plan` in the page. A key is pressed; after C, compare is waited for
 * (the rail clicks that follow add panes only once it shows); after Space or
 * X, the busy Pick button. Drags start at the middle of the stage (or the
 * active pane) once the zoom has rendered (the pan surface's grab cursor),
 * and the pointer is then rested there, over the photo.
 */
const runPlan =
  (plan: FocusAction[]): Drive =>
  async (page) => {
    let dragFrom: { x: number; y: number } | null = null
    for (const a of plan) {
      if ('key' in a) {
        await page.keyboard.press(a.key)
        if (a.key === FOCUS_KEYS.compare) await page.locator('[data-testid="compare-btn"][aria-pressed="true"]').waitFor()
        if (a.key === FOCUS_KEYS.pick || a.key === FOCUS_KEYS.reject) await page.locator('[data-testid="pick-btn"][disabled]').waitFor()
      } else if ('rail' in a) {
        await clickEl(page, `[data-rail-index="${a.rail}"]`)
      } else {
        if (!dragFrom) {
          const comparing = (await page.locator('[data-testid="compare-pane"]').count()) > 0
          const surface = comparing ? '[data-testid="compare-pane"][data-active="true"]' : 'main'
          await page.locator(comparing ? `${surface}.cursor-grab` : 'main .cursor-grab').first().waitFor()
          const box = await page.locator(surface).first().boundingBox()
          if (!box) throw new Error('parity: the photo to drag is not laid out')
          dragFrom = { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + box.height / 2) }
        }
        await page.mouse.move(dragFrom.x, dragFrom.y)
        await page.mouse.down()
        await page.mouse.move(dragFrom.x + a.drag.x, dragFrom.y + a.drag.y, { steps: 4 })
        await page.mouse.up()
      }
    }
    if (dragFrom) await page.mouse.move(dragFrom.x, dragFrom.y)
  }

/** State `n`'s case: the Info preference, its fixture, then its steps (FOCUS_STEPS, as focusPlan). */
export function focusCase(n: number): ParityCase {
  const s = FOCUS_STEPS[n] ?? {}
  const info = s.info ? '1' : '0'
  const drive: Drive[] = [
    (page) => page.evaluate(([k, v]) => window.localStorage.setItem(k, v), [SHOW_INFO_KEY, info] as const),
    loadFixture(n),
  ]
  const plan = focusPlan(s, membersOf(n))
  if (plan.length > 0) drive.push((page) => page.locator(STAGE).waitFor(), runPlan(plan))
  return { state: n, drive: inOrder(drive) }
}

/** The registered Focus (no 309) and Compare states. */
export const FOCUS_STATES = [300, 301, 302, 303, 304, 305, 306, 307, 308, 310, 311, 312, 313, 314, 315, 316, 317, 318]
export const COMPARE_STATES = Array.from({ length: 10 }, (_, i) => 400 + i)

/**
 * Why 312 is skipped. The prototype shows the broken full-size image as a
 * data: URL: mock/photos/broken.jpg is under Vite's inline limit, so the
 * prototype build inlines it, and the extractor identifies a data: URL by
 * its text (`res:` of the URL) and an http URL by the bytes it serves. Every
 * URL the app builds is Go's /media/full/… (shell/media.ts), so the hero's
 * `src` cannot carry the prototype's identity, whatever the bytes. The fix
 * is the prototype's (a broken image above the inline limit, or one served
 * by URL), and it needs the owner (312's hash moves).
 */
export const BROKEN_IMAGE_SKIP =
  'the prototype inlines the broken full-size image (mock/photos/broken.jpg, under Vite’s 4096-byte inline limit) as a data: URL, which the extractor identifies by its text; the app’s image is Go’s /media/full/… URL, identified by its bytes, so the hero’s src can never match'

export const FOCUS_SKIPS: Record<number, string> = { 312: BROKEN_IMAGE_SKIP }

/** The member on screen in state `n` (its step's photo, else the first). */
const shownMember = (n: number): Member => {
  const focus = fixtureFor(n).focus as { cluster: { members: Member[] } }
  return focus.cluster.members[FOCUS_STEPS[n]?.photo ?? 0]!
}

/** The full-size image URL the app sets for the member on screen in state `n` (shell/media.ts, the folder of the fixture's route). */
export const shownFullUrl = (n: number): string => {
  const folderId = decodeURIComponent(/^#\/folder\/([^/]+)\//.exec(fixtureFor(n).route)![1]!)
  return mediaFor(folderId).full(shownMember(n))
}

/** The browser's request for the full-size image of the member at `path` (shell/media.ts: /media/full/<folder>/<path>). */
const fullImageOf = (path: string) => (u: URL) => u.pathname.startsWith('/media/full/') && decodeURIComponent(u.pathname).endsWith(`/${path}`)

/**
 * 312 is skipped (above); this is its drive, kept for when the blocker is
 * lifted. The full-size request is answered for the browser itself
 * (Playwright's page.route), as no other state serves that file that way,
 * with the bytes of the prototype's broken image, which do not decode (the
 * app's own error event). Driven so, its first difference is the hero's src
 * (the data: URL).
 */
export const FOCUS_KEPT_DRIVES: Record<number, Drive> = {
  312: async (page) => {
    const body = readFileSync(new URL('../../prototype/mock/photos/broken.jpg', import.meta.url))
    await page.route(fullImageOf(shownMember(312).path), (route) => route.fulfill({ status: 200, contentType: 'image/jpeg', body }))
    await focusCase(312).drive!(page)
  },
}

/**
 * 313, the hero's thumbnail shown while its full-size image still loads.
 * The app reports `ready` from the image's own load event, so the browser's
 * request for exactly that URL (shownFullUrl) is held unanswered
 * (page.route, before the fixture opens Focus), and the case declares it in
 * `pendingImages`: settling waits for every other image to decode, but not
 * for this one, whose decode() would never settle. What is compared is
 * unchanged: the extractor records the image like any other (its URL's
 * bytes, fetched by Node from serve-app).
 */
export function pendingFullCase(n: number): ParityCase {
  const url = shownFullUrl(n)
  return {
    state: n,
    drive: async (page) => {
      await page.route((u) => u.pathname + u.search === url, () => {})
      await focusCase(n).drive!(page)
    },
    pendingImages: [url],
  }
}

/** The states whose full-size image is still loading (pendingFullCase). */
export const FOCUS_PENDING_FULL = [313]

export const FOCUS_CASES: ParityCase[] = [...FOCUS_STATES, ...COMPARE_STATES].map((n) =>
  FOCUS_SKIPS[n] !== undefined ? { state: n, skip: FOCUS_SKIPS[n] } : FOCUS_PENDING_FULL.includes(n) ? pendingFullCase(n) : focusCase(n),
)

// ── Settings (500–520) ──────────────────────────────────────────────────────
//
// The fixtures (fixtures/settings.ts) carry each state's data as Go answers
// it: the saved values (Settings.Get: pending in 510, refused with the
// state's message in 511), the quality worker (Catalog.QualityStatus),
// how Save answers (`calls`: kept in flight in 504),
// the "Save failed" toast of 512 (seeded), Go's event that brings 509's
// value, and the quality states' opening
// scroll (`ui`, the one value with no real path). What the prototype passes
// as a prop and the app keeps as the page's own state is done here, the
// real way:
//   - unsaved edits: the text typed into the field (raw, as the user types
//     it), which then loses focus; a switch flipped with a click;
//   - the section: its sidebar item clicked; in the quality states, the
//     opening scroll to the quality field reports the section it shows;
//   - a save in flight: Save pressed; the leave prompt: Back pressed with
//     unsaved edits (the router's guard holds the navigation);
//   - 509: Go's event (quality:status), emitted once the page shows the
//     value Go reported before it.
// Controls are clicked with element.click() (no hover, no focus).
// settingsCases.test.tsx holds SETTINGS_STEPS to the scenarios' props.

/** The switches of the Settings form (their test ids). */
export type SettingsSwitchId = 'visual-toggle' | 'crossdev-toggle' | 'advance-toggle' | 'hide-rejected-toggle' | 'quality-toggle'

/** What a Settings state needs beyond its fixture; each runs after the form (or the page) shows, in this order. */
export interface SettingsSteps {
  /** This raw text is typed into the time bucket (minutes). */
  bucket?: string
  /** This text is typed into the picks destination. */
  picks?: string
  /** These switches are flipped. */
  toggle?: SettingsSwitchId[]
  /** This section's sidebar item is clicked. */
  section?: 'grouping' | 'behaviour' | 'output'
  /** Save is pressed (the fixture's `calls` keeps it in flight). */
  save?: true
  /** Back is pressed, with unsaved edits: the leave prompt opens. */
  back?: true
}

export const SETTINGS_STEPS: Record<number, SettingsSteps> = {
  501: { bucket: '5', toggle: ['advance-toggle'] },
  502: { bucket: '0.5' },
  503: { picks: '/Volumes/Atlas/Selects', section: 'output' },
  504: { bucket: '5', toggle: ['advance-toggle'], save: true },
  508: { bucket: '5', toggle: ['advance-toggle'], back: true },
  512: { bucket: '5', toggle: ['advance-toggle'] },
  519: { section: 'behaviour' },
  520: { section: 'output' },
}

/** The form is on screen (the saved values have loaded). */
const SETTINGS_FORM = '[data-testid="save"]'

/** Types `text` into the field, as the user does, then the field loses focus. */
const typeInto =
  (testId: string, text: string): Drive =>
  async (page) => {
    const field = page.locator(`[data-testid="${testId}"]`)
    await field.fill(text)
    await field.evaluate((el: HTMLElement) => el.blur())
  }

/**
 * The page shows the value Go reported before `ev` (the bindings' answer):
 * the quality status line for `quality:status`. Emitting earlier could race
 * the load, whose answer would then win.
 */
const beforeShown = async (page: Page, ev: { name: string; data: unknown }) => {
  if (ev.name === 'quality:status') {
    await page.waitForFunction(() => (document.querySelector('[data-testid="quality-status"]')?.textContent ?? '') !== '')
  } else {
    throw new Error(`parity: no proof of the earlier value for ${ev.name}`)
  }
}

/** State `n`'s case: its fixture, then its events (the fixture's `events`), then its steps (SETTINGS_STEPS). */
export function settingsCase(n: number): ParityCase {
  const s = SETTINGS_STEPS[n] ?? {}
  const events = fixtureFor(n).events ?? []
  const drive: Drive[] = [loadFixture(n)]
  const needsForm = events.length > 0 || Object.keys(s).length > 0
  if (needsForm) drive.push((page) => page.locator(SETTINGS_FORM).waitFor())
  for (const ev of events) {
    drive.push(async (page) => {
      await beforeShown(page, ev)
      await emitEvent(page, ev.name, ev.data)
    })
  }
  if (s.bucket !== undefined) drive.push(typeInto('bucket-input', s.bucket))
  if (s.picks !== undefined) drive.push(typeInto('picks-dir-input', s.picks))
  for (const id of s.toggle ?? []) drive.push((page) => clickEl(page, `[data-testid="${id}"]`))
  if (s.section !== undefined) {
    const section = s.section
    drive.push(async (page) => {
      await clickEl(page, `nav [data-section="${section}"]`)
      await page.locator(`nav [data-section="${section}"][aria-current="true"]`).waitFor()
    })
  }
  if (s.save) {
    drive.push(async (page) => {
      await clickEl(page, SETTINGS_FORM)
      await page.locator(`${SETTINGS_FORM}[aria-busy="true"]`).waitFor()
    })
  }
  if (s.back) {
    drive.push(async (page) => {
      await clickEl(page, 'button[aria-label^="Back to"]')
      await page.locator('[role="alertdialog"]').waitFor()
    })
  }
  return { state: n, drive: inOrder(drive) }
}

/** The registered Settings states: 500–520; 505, 506 and 514–517 (the model downloads and the quality error) are retired. */
export const SETTINGS_STATES = Array.from({ length: 21 }, (_, i) => 500 + i).filter((n) => ![505, 506, 514, 515, 516, 517].includes(n))

export const SETTINGS_CASES: ParityCase[] = SETTINGS_STATES.map(settingsCase)

export const CASES: ParityCase[] = [
  ...LIBRARY_CASES,
  ...SHELL_CASES,
  ...FOLDER_CASES,
  ...FOCUS_CASES,
  ...SETTINGS_CASES,
  ...OVERLAY_CASES,
  ...NATIVE_STATES.map((state) => ({ state, skip: NATIVE_SKIP })),
  ...GALLERY_STATES.map((state) => ({ state, skip: GALLERY_SKIP })),
]
