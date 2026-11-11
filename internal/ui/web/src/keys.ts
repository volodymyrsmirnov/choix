// Layout-independent letter shortcuts.
//
// `e.key` is the character the active layout produces, so with a Cyrillic
// (or Greek, Hebrew, …) layout pressing the X key yields 'ч', not 'x'.
// `e.code` is the physical key ('KeyX') regardless of layout. We prefer
// `e.key` when it's a Latin letter — so Dvorak/AZERTY users get the letter
// printed on their keycap — and fall back to `e.code` otherwise.
//
// The fallback is skipped while Alt/Option or AltGr is held: those chords
// type a different character on purpose (Option+X → '≈' on macOS, AltGr+K
// on AZERTY; browsers report AltGr as Ctrl+Alt), so they aren't shortcuts.
export function isLetter(e: KeyboardEvent, letter: string): boolean {
  const want = letter.toLowerCase()
  const key = e.key.length === 1 ? e.key.toLowerCase() : ''
  if (key >= 'a' && key <= 'z') return key === want
  if (e.altKey || e.getModifierState('AltGraph')) return false
  return e.code === `Key${want.toUpperCase()}`
}

// Whether the user is currently moving focus with the keyboard.
//
// Library and Focus bind Space / Enter as window-level shortcuts (pick, open,
// next cluster). A mouse click also leaves focus on whatever button was
// clicked, and the shortcuts must keep working after that: click "Compare",
// press Space, and Space still picks. But a button the user reached with Tab
// has a visible focus ring, and there Space / Enter must activate that button
// like anywhere else on the web, or keyboard-only users can't press Undo,
// Help, Settings and so on without changing a pick.
//
// `:focus-visible` can't tell the two apart reliably (browsers may start
// matching it for a mouse-focused element once any key is pressed), so we
// track the modality ourselves: Tab turns it on, any pointer press turns it
// off.
let tabbing = false
if (typeof window !== 'undefined') {
  window.addEventListener('keydown', e => { if (e.key === 'Tab') tabbing = true }, true)
  window.addEventListener('pointerdown', () => { tabbing = false }, true)
}

// keyboardFocusedControl reports whether a key event targets a button or link
// that got its focus from keyboard navigation, in which case its native
// Enter / Space activation should win over the app's shortcuts.
export function keyboardFocusedControl(e: KeyboardEvent): boolean {
  if (!tabbing) return false
  const t = e.target instanceof Element ? e.target : null
  return !!t?.closest('button, a[href], [role="button"]')
}
