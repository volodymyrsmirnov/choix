// The current item: one look for "the one the keys act on" everywhere (the
// owner's rule): the current folder row in the Library, the current cluster
// on the Folder page, the current photo on Focus' rail (and the compare set
// while comparing), the active compare pane. It is the Library's selected
// row: the blue edge with its soft blue ring. Thumbnails draw it in base.css
// (`.thumb[data-current="true"]`), from the same tokens.
//
// Keyboard focus on a current item deepens the ring rather than drawing the
// focus outline on top of it: one indicator, not two. Controls that are not
// items (buttons, fields, switches) keep FOCUS_RING.

/** The current item's look, on a box with a 1 px border. */
export const CURRENT = 'border-sel shadow-sel-ring'

/**
 * Keyboard focus on an item that focus makes current (a Library row: focus
 * selects it): the current edge with the deeper ring, and no outline.
 */
export const CURRENT_FOCUS = 'outline-none focus-visible:border-sel focus-visible:[box-shadow:var(--shadow-sel-focus)]'
