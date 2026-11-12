import { expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { Field } from './Field'
import { SettingsModelCard } from './SettingsModelCard'
import { SettingsStatus } from './SettingsStatus'

const order = (html: string, ...parts: string[]) => parts.map((p) => html.indexOf(p))

test('one column: the name, then the control on its own line, then the description', () => {
  const html = renderToStaticMarkup(
    <Field controlId="f" title="Title" hint="A hint">
      <span>control</span>
    </Field>,
  )
  expect(html).toMatch(/^<div class="flex flex-col gap-2">/)
  const [name, control, hint] = order(html, 'id="f-label"', '<span>control</span>', 'id="f-hint"')
  expect(name).toBeGreaterThan(0)
  expect(control).toBeGreaterThan(name)
  expect(hint).toBeGreaterThan(control)
})

test('a switch sits on the name’s line, at its right end; the description follows', () => {
  const html = renderToStaticMarkup(<Field controlId="s" title="Switch" hint="What it does" aside={<span>switch</span>} />)
  expect(html).toMatch(/<div class="flex items-center gap-4[^"]*"><label id="s-label"[^>]*class="[^"]*flex-1[^"]*"[^>]*>Switch<\/label><span>switch<\/span><\/div>/)
  const [sw, hint] = order(html, '<span>switch</span>', 'id="s-hint"')
  expect(hint).toBeGreaterThan(sw)
})

test('one spacing for every row: the parts are 8 px apart by the column’s gap, with no margins of their own', () => {
  const html = renderToStaticMarkup(
    <Field controlId="f" title="Title" hint="A hint" error="Wrong" aside={<span>switch</span>} status={<p>status</p>}>
      <span>control</span>
    </Field>,
  )
  expect(html).toMatch(/^<div class="flex flex-col gap-2">/)
  // No part adds a margin, so name→control, control→status, →error and →description are all the gap.
  expect(html).not.toMatch(/class="[^"]*\bm[ty]-/)
  const [name, control, status, error, hint] = order(html, 'id="f-label"', '<span>control</span>', '<p>status</p>', 'id="f-error"', 'id="f-hint"')
  expect([name < control, control < status, status < error, error < hint]).toEqual([true, true, true, true])
})

test('a model card and a status line add no margin of their own; an empty status line takes no room', () => {
  const card = renderToStaticMarkup(
    <SettingsModelCard model={{ kind: 'quality', state: 'ready', received: 0, size: 177_000_000 }} onRetry={() => {}} />,
  )
  expect(card).toMatch(/^<div class="rounded-lg border border-line-1 bg-bg-2">/)
  const empty = renderToStaticMarkup(<SettingsStatus text="" />)
  expect(empty).toMatch(/^<div class="sr-only"><p role="status"/)
  const shown = renderToStaticMarkup(<SettingsStatus text="Ratings are up to date." />)
  // As wide as the control above it (a model card), not the whole column.
  expect(shown).toMatch(/^<div class="flex flex-col gap-1.5 max-w-\[480px\]"><p role="status"/)
})

test('the control keeps a sensible width and the description a readable measure', () => {
  const html = renderToStaticMarkup(
    <Field controlId="f" title="Title" hint="A hint" error="Wrong">
      <span>control</span>
    </Field>,
  )
  expect(html).toMatch(/<div class="[^"]*max-w-\[480px\][^"]*"><span>control<\/span><\/div>/)
  expect(html).toMatch(/<p id="f-hint" class="[^"]*max-w-\[560px\]/)
  // The error belongs to the control: right under it, before the description.
  const [control, error, hint] = order(html, '<span>control</span>', 'id="f-error"', 'id="f-hint"')
  expect(error).toBeGreaterThan(control)
  expect(hint).toBeGreaterThan(error)
})
