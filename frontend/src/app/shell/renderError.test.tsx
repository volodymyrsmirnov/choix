// The app's last resort (renderError.tsx): when the Library itself fails to
// render, the design's ErrorScreen (#40) fills the window, with the error's
// message, Copy Error Details (message and stacks) and Reload (the window
// again, from the Library).
import { describe, expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { ERROR_COPY } from '../../design/screens/error/props'
import { AppErrorScreen, copyText, reloadToLibrary, renderErrorDetails } from './renderError'

describe('renderErrorDetails', () => {
  test('the message, the error stack, then the component stack', () => {
    const e = new RangeError('Invalid time value')
    e.stack = 'RangeError: Invalid time value\n    at dayParts'
    expect(renderErrorDetails(e, '\n    at FolderRow\n    at LibraryScreen')).toBe(
      'Invalid time value\n\nRangeError: Invalid time value\n    at dayParts\n\nComponents:\n    at FolderRow\n    at LibraryScreen',
    )
  })
  test('without stacks, the message alone', () => {
    expect(renderErrorDetails('boom', '')).toBe('boom')
    const e = new Error('x')
    e.stack = ''
    expect(renderErrorDetails(e, '  ')).toBe('x')
  })
})

describe('AppErrorScreen', () => {
  test("renders the design's error screen with the details", () => {
    const html = renderToStaticMarkup(<AppErrorScreen details="Invalid time value" full="full" copy={() => {}} reload={() => {}} />)
    // The design keeps the last two words together (a no-break space).
    expect(html.replace(/\u00a0/g, ' ')).toContain(ERROR_COPY.title)
    expect(html).toContain('Invalid time value')
    expect(html).toContain('Copy Error Details')
    expect(html).toContain('Reload')
  })
})

describe('copyText', () => {
  test('the clipboard API when there is one', async () => {
    const written: string[] = []
    await copyText('text', { clipboard: { writeText: async (t) => void written.push(t) } }, null)
    expect(written).toEqual(['text'])
  })

  test('a selected text field and the copy command when the clipboard API refuses or is missing', async () => {
    const steps: string[] = []
    const field = {
      value: '',
      style: {} as Record<string, string>,
      setAttribute: (k: string, v: string) => steps.push(`attr ${k}=${v}`),
      select: () => steps.push('select'),
      remove: () => steps.push('remove'),
    }
    const doc = {
      createElement: (tag: string) => (steps.push(`create ${tag}`), field),
      body: { appendChild: () => steps.push('append') },
      execCommand: (cmd: string) => (steps.push(`exec ${cmd}`), true),
    }
    await copyText('text', { clipboard: { writeText: () => Promise.reject(new Error('denied')) } }, doc)
    expect(field.value).toBe('text')
    expect(steps).toEqual(['create textarea', 'attr readonly=', 'append', 'select', 'exec copy', 'remove'])
    steps.length = 0
    await copyText('again', {}, doc)
    expect(steps).toContain('exec copy')
  })
})

describe('reloadToLibrary', () => {
  test('loads the page again without its route: the Library', () => {
    const went: string[] = []
    reloadToLibrary({ pathname: '/', search: '', replace: (u) => went.push(u) })
    reloadToLibrary({ pathname: '/index.html', search: '?x=1', replace: (u) => went.push(u) })
    expect(went).toEqual(['/', '/index.html?x=1'])
  })
})
