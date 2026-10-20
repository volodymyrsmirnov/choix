// Layout-independent letter shortcuts.
//
// `e.key` is the character the active layout produces, so with a Cyrillic
// (or Greek, Hebrew, …) layout pressing the X key yields 'ч', not 'x'.
// `e.code` is the physical key ('KeyX') regardless of layout. We prefer
// `e.key` when it's a Latin letter — so Dvorak/AZERTY users get the letter
// printed on their keycap — and fall back to `e.code` otherwise.
export function isLetter(e: KeyboardEvent, letter: string): boolean {
  const want = letter.toLowerCase()
  const key = e.key.length === 1 ? e.key.toLowerCase() : ''
  if (key >= 'a' && key <= 'z') return key === want
  return e.code === `Key${want.toUpperCase()}`
}
