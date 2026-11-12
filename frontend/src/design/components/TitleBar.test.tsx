import { describe, expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { TitleBar } from './TitleBar'

const header = (html: string) => html.match(/^<header[^>]*>/)?.[0] ?? ''

describe('TitleBar', () => {
  test('in a window, the bar is twice --titlebar-center tall plus its border, 48 px by default, so every slot is centred on the buttons', () => {
    const h = header(renderToStaticMarkup(<TitleBar title="Library" />))
    expect(h).toContain('height:calc(2 * var(--titlebar-center, 23.5px) + 1px)')
    expect(h).toContain('padding-left:var(--titlebar-inset, 76px)')
    // The approved classes stay: the fingerprint records classes, not inline styles.
    expect(h).toContain('h-12')
    expect(h).toContain('items-center')
  })

  test('a long title or subtitle yields first: the indicator never shrinks, the title column does', () => {
    const html = renderToStaticMarkup(<TitleBar title="Library" subtitle="A long subtitle" indicator={<span>Scanning</span>} />)
    expect(html).toMatch(/<div data-align-y="titlebar" class="min-w-0 flex flex-col">/)
    expect(html).toMatch(/<div data-titlebar-control="true" data-align-y="titlebar" class="flex items-center flex-shrink-0"><span>Scanning<\/span>/)
  })

  test('a hidden title (the Library): no visible title or divider, the name only as a visually hidden heading', () => {
    const html = renderToStaticMarkup(<TitleBar title="Library" titleHidden indicator={<span>Scanning</span>} trailing={<span>add</span>} />)
    expect(html).toContain('<h1 class="sr-only">Library</h1>')
    expect(html).not.toContain('w-px h-[18px]')
    expect(html).not.toContain('min-w-0 flex flex-col')
    expect(html).toContain('<span>Scanning</span>')
    expect(html).toContain('<span>add</span>')
  })

  test('in full screen there are no buttons: the 48 px bar and the 12 px inset, whatever the properties say', () => {
    const h = header(renderToStaticMarkup(<TitleBar fullScreen title="Library" />))
    expect(h).not.toContain('style=')
    expect(h).toContain('h-12')
    expect(h).toContain('pl-3')
  })
})
