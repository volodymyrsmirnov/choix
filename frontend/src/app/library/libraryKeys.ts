// The Library's keyboard, which works from a cold start: the keys are
// taken on the window, so nothing has to be focused or clicked first. With
// nothing selected, ↓ or ↑ chooses the first row; then they move one row
// (stopping at the ends), and ↩ opens the selected folder, or the first row
// when nothing is selected yet. Moving puts the
// keyboard focus on the row (`reveal`), which is how a row is selected and
// shown as keyboard-focused in the design (FolderRow selects itself on
// focus), and scrolls it into view.
//
// Left alone: keys with ⌘, ⌃ or ⌥ (the native menu's), a key a control
// already handled, typing in a text field, ↩ on a focused control (a row
// button opens its folder itself), and anything while a dialog or menu is
// open (`blocked`).
import { isTextField } from '../shell/keys'

/** The row after (`1`) or before (`-1`) `selected`; the first row when nothing (listed) is selected. */
export function nextRow(ids: readonly string[], selected: string | null, dir: 1 | -1): string | null {
  if (ids.length === 0) return null
  const i = selected === null ? -1 : ids.indexOf(selected)
  if (i < 0) return ids[0] ?? null
  return ids[Math.min(ids.length - 1, Math.max(0, i + dir))] ?? null
}

export interface LibraryKeyDeps {
  /** The rows' folder ids, in the order the timeline shows them. */
  rows(): string[]
  selected(): string | null
  select(folderId: string): void
  /** Puts the keyboard focus on the row (and so into view). */
  reveal(folderId: string): void
  open(folderId: string): void
  /** Whether the folder can be opened (an offline one cannot). */
  canOpen(folderId: string): boolean
  /** A dialog, a menu or the shortcut sheet owns the keyboard. */
  blocked(): boolean
}

/** Whether the key's target is a control that acts on ↩ itself. */
function isControl(target: unknown): boolean {
  const tag = (target as { tagName?: unknown } | null)?.tagName
  if (typeof tag !== 'string') return false
  return ['BUTTON', 'A', 'INPUT', 'SELECT', 'TEXTAREA', 'SUMMARY'].includes(tag.toUpperCase())
}

/** The window keydown handler of the Library; pure, so it runs without a DOM. */
export function createLibraryKeyHandler(d: LibraryKeyDeps): (e: KeyboardEvent) => void {
  return (e) => {
    if (e.defaultPrevented || e.isComposing) return
    if (e.metaKey || e.ctrlKey || e.altKey) return
    if (isTextField(e.target) || d.blocked()) return
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      const id = nextRow(d.rows(), d.selected(), e.key === 'ArrowDown' ? 1 : -1)
      if (id === null) return
      e.preventDefault()
      d.select(id)
      d.reveal(id)
      return
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      if (isControl(e.target)) return
      const rows = d.rows()
      const selected = d.selected()
      // Cold start (nothing, or nothing listed, selected): the first row.
      const id = selected !== null && rows.includes(selected) ? selected : (rows[0] ?? null)
      if (id === null || !d.canOpen(id)) return
      e.preventDefault()
      if (id !== selected) d.select(id)
      d.open(id)
    }
  }
}
