// Keyboard helpers shared by every screen that binds window-level keys (the
// Folder page's cluster list and filter bar now; the app's Focus and Compare
// containers later), ported from v1's keys.ts. v1 installed its Tab tracker
// at import time; here it is mounted and stopped by its user.

/**
 * A layout-independent letter shortcut. `e.key` is the character the active
 * layout produces (with a Cyrillic layout the J key yields 'о'); `e.code` is
 * the physical key. `e.key` wins when it is a Latin letter, so Dvorak and
 * AZERTY users get the letter printed on their keycap; otherwise `e.code`,
 * except while Option or AltGr is held (those chords type a character on
 * purpose).
 */
export function isLetter(e: KeyboardEvent, letter: string): boolean {
  const want = letter.toLowerCase()
  const key = e.key.length === 1 ? e.key.toLowerCase() : ''
  if (key >= 'a' && key <= 'z') return key === want
  if (e.altKey || e.getModifierState('AltGraph')) return false
  return e.code === `Key${want.toUpperCase()}`
}

/**
 * Tracks whether the user is moving focus with the keyboard: Tab turns it on,
 * any pointer press turns it off. Enter on a control reached with Tab belongs
 * to that control; on a control that is focused only because it was clicked,
 * Enter stays the page's shortcut. (`:focus-visible` cannot tell the two
 * apart reliably.) Returns a stop function.
 */
export function trackModality(): { keyboardFocusedControl(e: KeyboardEvent): boolean; stop(): void } {
  let tabbing = false
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Tab') tabbing = true
  }
  const onPointer = () => {
    tabbing = false
  }
  window.addEventListener('keydown', onKey, true)
  window.addEventListener('pointerdown', onPointer, true)
  return {
    keyboardFocusedControl(e) {
      if (!tabbing) return false
      const t = e.target instanceof Element ? e.target : null
      return !!t?.closest('button, a[href], [role="button"]')
    },
    stop() {
      window.removeEventListener('keydown', onKey, true)
      window.removeEventListener('pointerdown', onPointer, true)
    },
  }
}
