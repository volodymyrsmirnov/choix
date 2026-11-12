// base.css behaves like a native window: no text selection on the app
// surface (WebKit needs the prefix), except where a native app allows it,
// and no image or link drags out of the window.
import { expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const css = readFileSync(join(import.meta.dir, 'base.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')

/** The complete declarations (`property: value`, trimmed) of the first rule whose selector is exactly `selector`. */
function rule(selector: string): string[] {
  const at = css.indexOf(`${selector} {`)
  if (at < 0) throw new Error(`base.css has no rule for ${selector}`)
  const body = css.slice(css.indexOf('{', at) + 1, css.indexOf('}', at))
  return body
    .split(';')
    .map((d) => d.trim().replace(/\s+/g, ' '))
    .filter(Boolean)
}

test('the app surface selects no text, in WebKit too (each declaration on its own)', () => {
  const root = rule('[data-app-root]')
  expect(root).toContain('-webkit-user-select: none')
  expect(root).toContain('user-select: none')
})

test('text fields and values meant to be copied stay selectable', () => {
  const r = rule('[data-app-root] :is(input, textarea, [data-selectable])')
  expect(r).toContain('-webkit-user-select: text')
  expect(r).toContain('user-select: text')
})

test('images and links are not dragged out of the window', () => {
  expect(rule('[data-app-root] :is(img, a)')).toContain('-webkit-user-drag: none')
})

test('the page never rubber-bands: html stops the elastic overscroll (shared by the app and the prototype)', () => {
  expect(rule('html')).toEqual(['overscroll-behavior: none'])
})

test('a declaration matches only itself: the prefixed one does not stand for the unprefixed one', () => {
  expect(['-webkit-user-select: none']).not.toContain('user-select: none')
})
