// Native replica states 700–720: HTML replicas of the macOS menu bar, the
// folder row's context menu and the "Remove from Library" alert, built from
// menus.json and dialogs.json (the files Go builds the real ones from), for
// the owner to approve what they hold.
//
// Store keys: `menu:<id>` (a menu-bar menu's overlay id; the open menu is
// the one whose key is true), and the Library's own keys (`folder-menu`,
// `remove-folder`, see libraryWiring) for the context menu and the alert.
import { FocusScreen } from '../../src/design/screens/focus/FocusScreen'
import { FolderScreen } from '../../src/design/screens/folder/FolderScreen'
import { LibraryScreen } from '../../src/design/screens/library/LibraryScreen'
import { MENUS } from '../../src/design/menus'
import { MenuBarScreen } from '../../src/design/screens/native/MenuBarScreen'
import { menuOverlay } from '../../src/design/screens/native/props'
import { SHORTCUTS_OVERLAY } from '../../src/design/screens/overlays/props'
import { ShortcutsSheet } from '../../src/design/screens/overlays/ShortcutsSheet'
import { LIBRARY_OVERLAYS } from '../../src/design/screens/library/props'
import type { FolderSummary, ScreenId } from '../../src/design/types'
import { LISBON, replace, scanAt, scanning, SIX } from '../mock/data'
import { SINGLE } from '../mock/focus'
import { REVIEWED } from '../mock/folder'
import type { Scenario, ScenarioCtx } from '../registry'
import { read } from '../store'
import { focusProps } from './focus'
import { folderProps } from './folder'
import { libraryProps } from './libraryWiring'

const MISSING = 'missing design'

/** Where a menu item leads in the prototype; any other item just closes the menu. */
const GO: Record<string, number> = {
  settings: 500,
  shortcuts: 600,
  'remove-folder': 720,
  // From Focus (705, 706), whose backdrop is the folder's second cluster:
  // Previous Cluster opens the first (s301); the third has no Focus state, so
  // Next Cluster does nothing here, as Focus's own Next does.
  'prev-cluster': 301,
  compare: 400,
  'pixel-peep': 307,
  'info-panel': 304,
}
const BACK: Partial<Record<ScreenId, number>> = { folder: 102, focus: 203 }

export interface MenuState {
  n: number
  label: string
  menu: string
  screen: ScreenId
  hasSelection: boolean
}

/** The menu-bar states: which menu is open, on which screen, with or without a target folder. */
export const MENU_STATES: MenuState[] = [
  { n: 700, label: 'Choix menu', menu: 'app', screen: 'library', hasSelection: true },
  { n: 701, label: 'File (Library, folder selected)', menu: 'file', screen: 'library', hasSelection: true },
  { n: 702, label: 'File (Folder)', menu: 'file', screen: 'folder', hasSelection: true },
  { n: 703, label: 'Edit', menu: 'edit', screen: 'folder', hasSelection: true },
  { n: 704, label: 'Photo (disabled, Library)', menu: 'photo', screen: 'library', hasSelection: true },
  { n: 705, label: 'Photo (Focus)', menu: 'photo', screen: 'focus', hasSelection: true },
  { n: 706, label: 'View', menu: 'view', screen: 'focus', hasSelection: true },
  { n: 707, label: 'Window', menu: 'window', screen: 'library', hasSelection: true },
  { n: 708, label: 'Help', menu: 'help', screen: 'library', hasSelection: true },
  { n: 709, label: 'File (Library, nothing selected)', menu: 'file', screen: 'library', hasSelection: false },
]

/** The app window behind the menu bar: the real screen the state names, with the mock data its own group uses. */
function Backdrop({ ctx, s }: { ctx: ScenarioCtx; s: MenuState }) {
  if (s.screen === 'folder') return <FolderScreen {...folderProps(ctx, { clusters: REVIEWED })} />
  // The folder's second cluster: Previous and Next Cluster both apply.
  if (s.screen === 'focus') return <FocusScreen {...focusProps(ctx, { data: SINGLE })} />
  // The selected folder is the one File ▸ Remove Folder… asks about (s720).
  return <LibraryScreen {...libraryProps(ctx, { folders: SIX, selectedId: s.hasSelection ? ALERT_FOLDER.id : null })} />
}

function MenuBarState({ ctx, s }: { ctx: ScenarioCtx; s: MenuState }) {
  const { state, set, go } = ctx.store
  const open = MENUS.find((m) => read(state, menuOverlay(m.id), m.id === s.menu))?.id ?? null
  const openMenu = (id: string | null) => set(Object.fromEntries(MENUS.map((m) => [menuOverlay(m.id), m.id === id])))
  const sheet = read(state, SHORTCUTS_OVERLAY, false)
  return (
    <>
      <MenuBarScreen
        open={open}
        screen={s.screen}
        hasSelection={s.hasSelection}
        backdrop={<Backdrop ctx={ctx} s={s} />}
        onOpen={openMenu}
        onItem={(id) => {
          openMenu(null)
          // Keyboard Shortcuts opens the sheet for the screen behind the menu:
          // s600 is the Library's; elsewhere it opens here, over this screen.
          if (id === 'shortcuts' && s.screen !== 'library') return set({ [SHORTCUTS_OVERLAY]: true })
          const to = id === 'back' ? BACK[s.screen] : GO[id]
          if (to !== undefined) go(to)
        }}
      />
      {sheet && <ShortcutsSheet screen={s.screen} onClose={() => set({ [SHORTCUTS_OVERLAY]: false })} />}
    </>
  )
}

/** The context-menu states: the row's menu open on a ready, an offline and a scanning folder. */
export interface ContextState {
  n: number
  label: string
  folder: FolderSummary
}

const OFFLINE: FolderSummary = { ...LISBON, state: 'offline', parentPath: '/Volumes/Atlas/Trips', volumeName: 'Atlas' }
const SCANNING: FolderSummary = scanning(LISBON, scanAt('analyze', 212, 420))

export const CONTEXT_STATES: ContextState[] = [
  { n: 710, label: 'Folder-row context menu', folder: LISBON },
  { n: 711, label: 'Context menu, offline folder', folder: OFFLINE },
  { n: 712, label: 'Context menu, scanning folder', folder: SCANNING },
]

/** The alert state: the folder it asks about. */
export const ALERT_FOLDER = SIX.find((f) => f.id === 'dolomites')!

export const scenarios: Scenario[] = [
  ...MENU_STATES.map(
    (s): Scenario => ({
      n: s.n,
      group: 'native',
      label: s.label,
      design: MISSING,
      overlays: [menuOverlay(s.menu)],
      subject: '[role="menu"]',
      render: (ctx) => <MenuBarState ctx={ctx} s={s} />,
    }),
  ),
  ...CONTEXT_STATES.map(
    (s): Scenario => ({
      n: s.n,
      group: 'native',
      label: s.label,
      design: MISSING,
      overlays: [LIBRARY_OVERLAYS.menu],
      subject: '[role="menu"]',
      render: (ctx) => (
        <LibraryScreen
          {...libraryProps(ctx, {
            folders: replace(SIX, s.folder),
            selectedId: s.folder.id,
            menuFor: s.folder.id,
          })}
        />
      ),
    }),
  ),
  {
    n: 720,
    group: 'native',
    label: '“Remove from Library” alert',
    design: MISSING,
    overlays: [LIBRARY_OVERLAYS.remove],
    subject: '[role="alertdialog"]',
    render: (ctx) => (
      <LibraryScreen {...libraryProps(ctx, { folders: SIX, selectedId: ALERT_FOLDER.id, removePrompt: ALERT_FOLDER.id })} />
    ),
  },
]
