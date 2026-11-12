// The shortcut sheet's content (v1 Shortcuts.tsx groups(), plus the
// shortcuts 2.0 adds in spec §4.4). Every row that has a menu item reads its
// key from menus.json (an accelerator, or the `display` of a single key the
// webview handles), so the sheet and the menu bar cannot disagree; only keys
// with no menu item (J/K, ↑/↓, 1–4, ⌘K) are listed here; the zoom row's note
// reads View ▸ Zoom's accelerators from the manifest. Labels are macOS glyphs, never derived from the user agent.
import type { ShortcutGroup } from '../../components'
import { menuKey } from '../../menus'
import type { ScreenId } from '../../types'

export type ShortcutSectionId = 'library' | 'folder' | 'focus' | 'compare' | 'global'

export { menuKey }

/** View ▸ Zoom In / Out / to Fit: their ⌘ shortcuts are the zoom row's note. */
const ZOOM_MENU = ['zoom-in', 'zoom-out', 'zoom-fit']

/** Every section, in natural order. Mirrors the screens' key handlers. */
export function shortcutGroups(): Record<ShortcutSectionId, ShortcutGroup> {
  return {
    library: {
      id: 'library',
      title: 'Library',
      rows: [
        { keys: [[menuKey('find')]], label: 'Search folders' },
        { keys: [['↩']], label: 'Open the selected folder' },
        { keys: [[menuKey('open-picks')]], label: 'Open its picks folder' },
        { keys: [[menuKey('reveal-folder')]], label: 'Reveal it in Finder' },
        { keys: [[menuKey('rescan-folder')]], label: 'Rescan it' },
        { keys: [[menuKey('remove-folder')]], label: 'Remove it from the Library', note: 'Asks first' },
      ],
    },
    folder: {
      id: 'folder',
      title: 'Folder',
      rows: [
        { keys: [['↑', '↓'], ['J', 'K']], label: 'Move between clusters' },
        { keys: [[menuKey('open-focus')], ['↩']], label: 'Open cluster in Focus' },
        { keys: [[menuKey('find')], ['⌘K']], label: 'Search' },
        { keys: [[menuKey('undo')]], label: 'Undo last pick or reject' },
        { keys: [[menuKey('open-picks')]], label: 'Open the picks folder' },
        { keys: [[menuKey('back')], ['Esc']], label: 'Back to the Library' },
      ],
    },
    focus: {
      id: 'focus',
      title: 'Focus',
      rows: [
        { keys: [[menuKey('prev-photo'), menuKey('next-photo')], ['↑', '↓']], label: 'Previous / next photo' },
        { keys: [[menuKey('pick')]], label: 'Pick', note: 'Press again to unpick' },
        { keys: [[menuKey('reject')]], label: 'Reject', note: 'Press again to unreject' },
        { keys: [[menuKey('undo')]], label: 'Undo last pick or reject' },
        { keys: [[menuKey('compare')]], label: 'Compare' },
        { keys: [[menuKey('pixel-peep')]], label: 'Pixel-peep', note: 'Photos only' },
        { keys: [['+', '−', '0']], label: 'Zoom in / out / fit', note: `Also ${ZOOM_MENU.map(menuKey).join(' ')}` },
        { keys: [[menuKey('info-panel')]], label: 'Info panel' },
        { keys: [[menuKey('next-cluster')]], label: 'Next cluster' },
        { keys: [[menuKey('prev-cluster')]], label: 'Previous cluster' },
        { keys: [[menuKey('back')], ['Esc']], label: 'Back to the folder', note: 'Un-zooms or leaves compare first' },
      ],
    },
    compare: {
      id: 'compare',
      title: 'Compare',
      rows: [
        { keys: [['1–4'], [menuKey('prev-photo'), menuKey('next-photo')]], label: 'Choose the active pane' },
        { keys: [[menuKey('pick')], [menuKey('reject')]], label: 'Pick / reject the active pane' },
        { keys: [], label: 'Click rail thumbnails to add or remove panes', note: 'Up to 4' },
      ],
    },
    global: {
      id: 'global',
      title: 'Everywhere',
      rows: [
        { keys: [[menuKey('add-folder')]], label: 'Add a folder' },
        { keys: [[menuKey('settings')]], label: 'Settings' },
        { keys: [[menuKey('shortcuts')]], label: 'Show or hide this help' },
      ],
    },
  }
}

/** The section a screen's shortcuts live in; Settings has none of its own. */
export function sectionOf(screen: ScreenId): ShortcutSectionId | null {
  return screen === 'settings' ? null : screen
}

/**
 * The sheet's two columns, each a list of sections: Library and Folder, then
 * Focus and Compare. The current screen's column comes first, with its
 * section on top; Settings keeps the natural order. "Everywhere" sits below
 * both columns.
 */
export function shortcutColumns(screen: ScreenId): ShortcutSectionId[][] {
  const columns: ShortcutSectionId[][] = [
    ['library', 'folder'],
    ['focus', 'compare'],
  ]
  const current = sectionOf(screen)
  if (current === null) return columns
  const mine = columns.find((c) => c.includes(current))!
  const other = columns.find((c) => c !== mine)!
  return [[current, ...mine.filter((s) => s !== current)], other]
}
