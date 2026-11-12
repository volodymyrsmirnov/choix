import { afterEach, describe, expect, test } from 'bun:test'
import { isLetter, trackModality } from './keys'

/** The fields isLetter and trackModality read from a KeyboardEvent. */
function key(k: string, code: string, opts: { alt?: boolean; altGraph?: boolean; target?: unknown } = {}): KeyboardEvent {
  return {
    key: k,
    code,
    altKey: !!opts.alt,
    getModifierState: (m: string) => m === 'AltGraph' && !!opts.altGraph,
    target: opts.target ?? null,
  } as unknown as KeyboardEvent
}

describe('isLetter (v1 keys.ts)', () => {
  test('a Latin e.key wins: Dvorak and AZERTY get the letter on their keycap', () => {
    expect(isLetter(key('j', 'KeyC'), 'j')).toBe(true)
    expect(isLetter(key('J', 'KeyJ'), 'j')).toBe(true)
    expect(isLetter(key('q', 'KeyA'), 'a')).toBe(false)
  })

  test('a non-Latin layout falls back to the physical key', () => {
    expect(isLetter(key('о', 'KeyJ'), 'j')).toBe(true)
    expect(isLetter(key('ч', 'KeyX'), 'x')).toBe(true)
    expect(isLetter(key('ч', 'KeyX'), 'j')).toBe(false)
  })

  test('Option and AltGr chords are characters, not shortcuts', () => {
    expect(isLetter(key('≈', 'KeyX', { alt: true }), 'x')).toBe(false)
    expect(isLetter(key('∆', 'KeyJ', { altGraph: true }), 'j')).toBe(false)
  })

  test('named keys never match a letter', () => {
    expect(isLetter(key('Enter', 'Enter'), 'f')).toBe(false)
  })
})

describe('trackModality', () => {
  const g = globalThis as { window?: unknown; Element?: unknown }
  const real = { window: g.window, Element: g.Element }
  afterEach(() => {
    g.window = real.window
    g.Element = real.Element
  })

  /** Stand-ins for window and Element (bun test has no DOM). */
  class FakeElement {
    constructor(private readonly inButton: boolean) {}
    closest(): FakeElement | null {
      return this.inButton ? this : null
    }
  }

  test('Tab turns keyboard modality on, a pointer press turns it off', () => {
    const win = new EventTarget()
    g.window = win
    g.Element = FakeElement
    const send = (type: string, init: Record<string, unknown> = {}) => win.dispatchEvent(Object.assign(new Event(type), init))
    const enter = (inButton: boolean) => key('Enter', 'Enter', { target: new FakeElement(inButton) })

    const m = trackModality()
    expect(m.keyboardFocusedControl(enter(true))).toBe(false)
    send('keydown', { key: 'Tab' })
    expect(m.keyboardFocusedControl(enter(true))).toBe(true)
    expect(m.keyboardFocusedControl(enter(false))).toBe(false)
    send('pointerdown')
    expect(m.keyboardFocusedControl(enter(true))).toBe(false)
    m.stop()
    send('keydown', { key: 'Tab' })
    expect(m.keyboardFocusedControl(enter(true))).toBe(false)
  })

  test('stop() removes the listeners it added', () => {
    const win = new EventTarget()
    const added: string[] = []
    const removed: string[] = []
    win.addEventListener = ((t: string) => void added.push(t)) as EventTarget['addEventListener']
    win.removeEventListener = ((t: string) => void removed.push(t)) as EventTarget['removeEventListener']
    g.window = win
    trackModality().stop()
    expect(added.sort()).toEqual(['keydown', 'pointerdown'])
    expect(removed.sort()).toEqual(added)
  })
})
