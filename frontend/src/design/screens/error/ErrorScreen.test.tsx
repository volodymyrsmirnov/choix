import { expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { ERROR_COPY } from './props'
import { ErrorScreen } from './ErrorScreen'

const DETAILS = "TypeError: Cannot read properties of undefined (reading 'members')"
const html = renderToStaticMarkup(<ErrorScreen details={DETAILS} onReload={() => {}} onCopyDetails={() => {}} />)

test('the title bar carries the brand mark and the app’s name', () => {
  expect(html).toMatch(/^<div[^>]*><header data-titlebar/)
  expect(html).toContain('>Choix</h1>')
  expect(html.indexOf('<img')).toBeLessThan(html.indexOf('>Choix</h1>'))
})

test('says what happened and that picks are safe', () => {
  expect(ERROR_COPY.body).toContain('picks are safe')
  expect(html).toContain(ERROR_COPY.title.replace(/ (\S+)$/, ' $1'))
  expect(html).toContain(ERROR_COPY.body.replace(/ (\S+)$/, ' $1'))
})

test('the details are shown selectable; the actions are Copy Error Details, then Reload (primary, last)', () => {
  expect(html).toMatch(/<p data-selectable="true"[^>]*>TypeError: Cannot read properties of undefined \(reading &#x27;members&#x27;\)<\/p>/)
  const copy = html.indexOf('Copy Error Details')
  const primary = html.indexOf('btn btn-primary')
  expect(copy).toBeGreaterThan(0)
  expect(html.indexOf('btn btn-primary', primary + 1)).toBe(-1)
  expect(primary).toBeGreaterThan(copy)
  expect(html.slice(primary)).toMatch(/^btn btn-primary[^>]*>.*Reload</)
})
