import { describe, expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'
import type { MediaURLs, Member } from '../types'
import { FocusMedia } from './FocusMedia'

const member: Member = {
  file_id: 7,
  path: 'Day 2/DSCF4101.RAF',
  content_hash: 'c0ffee',
  picked: false,
  rejected: false,
  kind: 'photo',
  format: 'RAF',
}
const media: MediaURLs = { thumb: (_m, tier) => `/thumb-${tier}.jpg`, full: () => '/full.jpg' }
const render = (state: 'loading' | 'ready' | 'error', variant: 'hero' | 'compare') =>
  renderToStaticMarkup(
    <FocusMedia member={member} media={media} state={state} zoom={1} pan={{ x: 0, y: 0 }} variant={variant} onState={() => {}} />,
  )

describe('FocusMedia', () => {
  for (const variant of ['hero', 'compare'] as const) {
    test(`${variant}: a spinner while loading, first (under the layers), on a transparent letterbox`, () => {
      const html = render('loading', variant)
      expect(html).toContain('data-testid="media-spinner"')
      expect(html.indexOf('media-spinner')).toBeLessThan(html.indexOf('<img'))
      // No layer paints an opaque background over the spinner.
      for (const img of html.match(/<img[^>]*>/g) ?? []) expect(img).not.toContain('bg-bg-0')
    })

    test(`${variant}: no spinner once ready or failed`, () => {
      expect(render('ready', variant)).not.toContain('media-spinner')
      expect(render('error', variant)).not.toContain('media-spinner')
    })
  }

  test('compare: the opaque background is the wrapper’s, beneath the spinner', () => {
    expect(render('loading', 'compare')).toMatch(/^<div class="absolute inset-0 bg-bg-0"><div[^>]*data-testid="media-spinner"/)
  })

  test('the placeholder is the thumb tier and both layers are keyed per file', () => {
    const html = render('loading', 'hero')
    expect(html).toContain('src="/thumb-thumb.jpg"')
    expect(html).toContain('src="/full.jpg"')
  })
})
