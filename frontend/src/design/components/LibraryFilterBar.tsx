import { useEffect, useRef, type KeyboardEvent } from 'react'
import { isLetter } from '../keys'
import type { LibraryFilter } from '../types'
import { Dropdown, type DropdownOption } from './Dropdown'
import { Kbd } from './Kbd'
import { TextInput } from './TextInput'

export type LibraryMenu = 'order' | 'show'

/** The Sort menu's items. */
export const ORDER_OPTIONS: DropdownOption<LibraryFilter['order']>[] = [
  { value: 'date-desc', label: 'Capture date — newest first' },
  { value: 'date-asc', label: 'Capture date — oldest first' },
  { value: 'name', label: 'Name — A to Z' },
  { value: 'added', label: 'Recently added' },
]
const ORDER_LABEL: Record<LibraryFilter['order'], string> = {
  'date-desc': 'Newest first',
  'date-asc': 'Oldest first',
  name: 'Name',
  added: 'Recently added',
}

/** The Show menu's items. */
export const SHOW_OPTIONS: DropdownOption<LibraryFilter['show']>[] = [
  { value: 'all', label: 'All folders' },
  { value: 'unreviewed', label: 'Not reviewed yet' },
  { value: 'started', label: 'Review started' },
  { value: 'attention', label: 'Needs attention' },
]
const SHOW_LABEL: Record<LibraryFilter['show'], string> = {
  all: 'All folders',
  unreviewed: 'Not reviewed yet',
  started: 'Review started',
  attention: 'Needs attention',
}

/**
 * The Library's toolbar, in the spirit of the Folder page's filter bar: the
 * search field (⌘F focuses and selects it; ↓ moves to the first folder; Esc
 * clears it), which folders to show, and their order. It sits at the right
 * end of the Library's summary line, so the folder rows stay where they
 * were, and its menus open leftwards from there. Controlled: the caller owns
 * the filter and which menu is open.
 */
export function LibraryFilterBar({
  filter,
  openMenu,
  menuIds,
  onFilter,
  onOpenMenu,
  onFirstRow,
}: {
  filter: LibraryFilter
  openMenu: LibraryMenu | null
  /** Overlay ids of the two menus. */
  menuIds: { order: string; show: string }
  onFilter: (f: LibraryFilter) => void
  onOpenMenu: (m: LibraryMenu | null) => void
  /** ↓ in the search field: move to the first folder shown. */
  onFirstRow: () => void
}) {
  const searchRef = useRef<HTMLInputElement>(null)

  // ⌘F (Edit › Find, when the menu does not take it) focuses the search
  // field, as on the Folder page. Not while a dialog is open, nor when
  // another handler took the key, nor while the page sits inert.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.defaultPrevented) return
      if (!e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
      if (!isLetter(e, 'f')) return
      if (document.querySelector('[role="dialog"], [role="alertdialog"]')) return
      if (searchRef.current?.closest('[inert]')) return
      e.preventDefault()
      searchRef.current?.focus()
      searchRef.current?.select()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      onFirstRow()
    } else if (e.key === 'Escape' && filter.query !== '') {
      // Esc clears the search; with nothing typed it is left to the page.
      e.preventDefault()
      e.stopPropagation()
      onFilter({ ...filter, query: '' })
    }
  }

  return (
    <div data-testid="library-toolbar" className="flex items-center gap-2 min-w-0">
      <TextInput
        ref={searchRef}
        size="sm"
        icon="search"
        className="w-[220px] min-w-[150px] flex-shrink"
        value={filter.query}
        onChange={(query) => onFilter({ ...filter, query })}
        onKeyDown={onKeyDown}
        placeholder="Search folders"
        label="Search folders by name or location"
        aria-keyshortcuts="Meta+F"
        data-testid="filter-input"
        trailing={<Kbd hidden>⌘F</Kbd>}
      />
      <Dropdown
        id={menuIds.show}
        testId="library-show"
        icon="filter"
        srPrefix="Show:"
        label={SHOW_LABEL[filter.show]}
        menuLabel="Show"
        align="right"
        options={SHOW_OPTIONS}
        value={filter.show}
        open={openMenu === 'show'}
        onOpenChange={(open) => onOpenMenu(open ? 'show' : null)}
        onSelect={(show) => onFilter({ ...filter, show })}
      />
      <Dropdown
        id={menuIds.order}
        testId="library-order"
        icon="sort"
        srPrefix="Sort:"
        label={ORDER_LABEL[filter.order]}
        menuLabel="Sort folders"
        align="right"
        options={ORDER_OPTIONS}
        value={filter.order}
        open={openMenu === 'order'}
        onOpenChange={(open) => onOpenMenu(open ? 'order' : null)}
        onSelect={(order) => onFilter({ ...filter, order })}
      />
    </div>
  )
}

/** The filter's name, as its menu shows it (the empty state quotes it). */
export function showLabel(show: LibraryFilter['show']): string {
  return SHOW_LABEL[show]
}
