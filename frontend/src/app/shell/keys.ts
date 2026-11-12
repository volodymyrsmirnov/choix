// The app's keyboard: v1's internal/ui/web/src/keys.ts (its isLetter and
// Tab-modality helpers, which the design already carries in
// src/design/keys.ts and are used from there) plus one rule ([call] C31):
//
//   A keydown whose combination equals an `accelerator` in menus.json is
//   ignored: the native menu owns it and sends `menu:action`. When
//   System.Info().singleKeysNative is true, so is a key a menu item lists
//   as `display` (Space, X, ←, →, ↩, ⇧↩, C, Z, I, ?).
//
// Every action therefore has exactly one dispatcher, and both end in
// runAction (actions.ts). What is left to the webview: the `display` keys
// (unless native), and v1's keys no menu item lists — Esc (Back), ↑/↓ in
// Focus, + − 0 for zoom, 1–4 for a compare pane.
//
// Keys a design screen binds itself are not bound here, so they are not
// dispatched twice: the Folder page's cluster list takes ↑ ↓ J K F ↩, and
// its filter bar ⌘F and ⌘K (design ClusterList, FolderFilterBar).
import { isLetter, trackModality } from '../../design/keys'
import { MENUS } from '../../design/menus'
import type { ScreenId } from '../../design/types'

/** Input types that do not take text (their keys stay shortcuts). */
const NON_TEXT_INPUTS: ReadonlySet<string> = new Set([
  'button',
  'checkbox',
  'color',
  'file',
  'hidden',
  'image',
  'radio',
  'range',
  'reset',
  'submit',
])

/** Whether an element takes typed text (an input, a textarea, contenteditable). Works on any object. */
export function isTextField(el: unknown): boolean {
  if (typeof el !== 'object' || el === null) return false
  const o = el as { tagName?: unknown; type?: unknown; isContentEditable?: unknown }
  if (o.isContentEditable === true) return true
  const tag = typeof o.tagName === 'string' ? o.tagName.toUpperCase() : ''
  if (tag === 'TEXTAREA') return true
  if (tag !== 'INPUT') return false
  return !NON_TEXT_INPUTS.has(typeof o.type === 'string' ? o.type.toLowerCase() : 'text')
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (isTextField(target)) return true
  const tag = (target as { tagName?: unknown } | null)?.tagName
  return typeof tag === 'string' && tag.toUpperCase() === 'SELECT'
}

/** ⌘, or ⌃ / ⌥ that is not AltGr (AltGr arrives as ⌃⌥ on some layouts and types a character). */
function isChord(e: KeyboardEvent): boolean {
  return e.metaKey || ((e.ctrlKey || e.altKey) && !e.getModifierState('AltGraph'))
}

interface Combo {
  cmd: boolean
  ctrl: boolean
  alt: boolean
  shift: boolean
  key: string
}

/** A Wails accelerator (`CmdOrCtrl+Shift+P`, `CmdOrCtrl+Plus`). macOS only: CmdOrCtrl is ⌘. */
function parseAccelerator(acc: string): Combo {
  const parts = acc.split('+')
  const key = parts[parts.length - 1]
  const mods = parts.slice(0, -1).map((m) => m.toLowerCase())
  const has = (...names: string[]) => mods.some((m) => names.includes(m))
  return {
    cmd: has('cmdorctrl', 'cmd', 'command', 'super'),
    ctrl: has('ctrl', 'control'),
    alt: has('alt', 'option', 'optionoralt'),
    shift: has('shift'),
    key,
  }
}

/** Whether a keydown is the accelerator's combination. */
export function acceleratorMatches(acc: string, e: KeyboardEvent): boolean {
  const c = parseAccelerator(acc)
  if (e.metaKey !== c.cmd || e.ctrlKey !== c.ctrl || e.altKey !== c.alt) return false
  // ⌘+ is ⇧⌘= on most layouts, and ⌘= zooms as well.
  if (c.key === 'Plus') return e.key === '+' || e.key === '='
  if (e.shiftKey !== c.shift) return false
  if (/^[A-Za-z]$/.test(c.key)) return isLetter(e, c.key)
  return e.key === c.key
}

/** Whether a keydown is a menu item's single-key `display`. */
function displayMatches(display: string, e: KeyboardEvent): boolean {
  if (isChord(e)) return false
  switch (display) {
    case 'Space':
      return e.key === ' ' || e.code === 'Space'
    case '→':
      return e.key === 'ArrowRight'
    case '←':
      return e.key === 'ArrowLeft'
    case '↩':
      return e.key === 'Enter' && !e.shiftKey
    case '⇧↩':
      return e.key === 'Enter' && e.shiftKey
    case '?':
      return e.key === '?'
    default:
      return /^[A-Z]$/.test(display) && isLetter(e, display)
  }
}

/** v1 Focus's paneDigit: 1–4 from the key, or the physical digit row unless ⌥/AltGr is held. */
function paneDigit(e: KeyboardEvent): number | null {
  if (e.key.length === 1 && e.key >= '1' && e.key <= '4') return Number(e.key)
  if (e.altKey || e.getModifierState('AltGraph')) return null
  const m = /^Digit([1-4])$/.exec(e.code)
  return m ? Number(m[1]) : null
}

interface Binding {
  id: string
  data?: string
  screens: readonly ScreenId[]
  /** `accelerator`: the native menu owns it; `single`: a menu item's display; `webview`: no menu item. */
  kind: 'accelerator' | 'single' | 'webview'
  match(e: KeyboardEvent): boolean
}

/** Every accelerator in menus.json: the combinations the native menu owns. */
export const MENU_ACCELERATORS: readonly string[] = MENUS.flatMap((m) =>
  m.items.flatMap((it) => ('id' in it && it.accelerator ? [it.accelerator] : [])),
)

/** Menu items whose key a design screen binds itself (see the header). */
const DESIGN_OWNED: ReadonlySet<string> = new Set(['open-focus'])

/** Screens a key works on beyond its menu item's `enabledIn` (the sheet: ←/→ choose the compare pane). */
const KEY_SCREENS: Record<string, ScreenId[]> = {
  'next-photo': ['compare'],
  'prev-photo': ['compare'],
}

const FOCUS_SCREENS: ScreenId[] = ['focus', 'compare']
const plain = (pred: (e: KeyboardEvent) => boolean) => (e: KeyboardEvent) => !isChord(e) && pred(e)

const BINDINGS: Binding[] = [
  ...MENUS.flatMap((menu) =>
    menu.items.flatMap((it): Binding[] => {
      if (!('id' in it) || DESIGN_OWNED.has(it.id)) return []
      const screens = [...it.enabledIn, ...(KEY_SCREENS[it.id] ?? [])]
      const acc = it.accelerator
      if (acc) return [{ id: it.id, screens, kind: 'accelerator', match: (e) => acceleratorMatches(acc, e) }]
      const display = it.display
      if (display) return [{ id: it.id, screens, kind: 'single', match: (e) => displayMatches(display, e) }]
      return []
    }),
  ),
  { id: 'back', screens: ['folder', ...FOCUS_SCREENS], kind: 'webview', match: plain((e) => e.key === 'Escape') },
  { id: 'prev-photo', screens: FOCUS_SCREENS, kind: 'webview', match: plain((e) => e.key === 'ArrowUp') },
  { id: 'next-photo', screens: FOCUS_SCREENS, kind: 'webview', match: plain((e) => e.key === 'ArrowDown') },
  { id: 'zoom-in', screens: FOCUS_SCREENS, kind: 'webview', match: plain((e) => e.key === '+' || e.key === '=') },
  { id: 'zoom-out', screens: FOCUS_SCREENS, kind: 'webview', match: plain((e) => e.key === '-' || e.key === '_') },
  { id: 'zoom-fit', screens: FOCUS_SCREENS, kind: 'webview', match: plain((e) => e.key === '0') },
  ...[1, 2, 3, 4].map(
    (n): Binding => ({
      id: 'pane',
      data: String(n),
      screens: ['compare'],
      kind: 'webview',
      match: (e) => !e.metaKey && !e.ctrlKey && paneDigit(e) === n,
    }),
  ),
]

export interface KeyOptions {
  /** runAction. */
  run(id: string, data?: string): void
  /** The screen showing (`compare` while Focus compares). */
  screen(): ScreenId
  /** System.Info().singleKeysNative. */
  singleKeysNative(): boolean
  /** The combinations the native menu owns; menus.json's accelerators by default. */
  accelerators?: readonly string[]
  /** A dialog is open (the shortcut sheet, a prompt): it owns the keyboard. */
  dialogOpen?(): boolean
  /** The open dialog is the shortcut sheet, which "?" closes again. */
  sheetOpen?(): boolean
  /** Enter / Space on a control reached with Tab belong to that control (trackModality). */
  keyboardFocusedControl?(e: KeyboardEvent): boolean
}

/** The window keydown handler; pure, so it runs without a DOM. */
export function createKeyHandler(o: KeyOptions): (e: KeyboardEvent) => void {
  const owned = o.accelerators ?? MENU_ACCELERATORS
  return (e) => {
    if (e.defaultPrevented || e.isComposing) return
    if (owned.some((acc) => acceleratorMatches(acc, e))) return
    const native = o.singleKeysNative()
    if (o.dialogOpen?.()) {
      if (!native && !e.repeat && o.sheetOpen?.() && displayMatches('?', e)) {
        e.preventDefault()
        o.run('shortcuts')
      }
      return
    }
    if (isEditableTarget(e.target)) return
    const screen = o.screen()
    for (const b of BINDINGS) {
      if (b.kind === 'single' && native) continue
      if (!b.screens.includes(screen) || !b.match(e)) continue
      if ((e.key === 'Enter' || e.key === ' ') && o.keyboardFocusedControl?.(e)) return
      if (b.id === 'shortcuts' && e.repeat) return
      e.preventDefault()
      o.run(b.id, b.data)
      return
    }
  }
}

/**
 * Installs the handler on the window (bubble phase: a dialog, menu or
 * dropdown that handles a key first stops or prevents it). Returns stop.
 */
export function installKeys(o: Omit<KeyOptions, 'keyboardFocusedControl'>): () => void {
  const modality = trackModality()
  const handler = createKeyHandler({ ...o, keyboardFocusedControl: modality.keyboardFocusedControl })
  window.addEventListener('keydown', handler)
  return () => {
    window.removeEventListener('keydown', handler)
    modality.stop()
  }
}
