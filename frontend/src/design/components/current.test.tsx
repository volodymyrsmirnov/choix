// One look for "the current item" (the owner: the Library's blue selected
// row, reused across the board): the current folder row, the current
// cluster, the current photo on Focus' rail and the active compare pane all
// draw the blue edge and its soft blue ring. Keyboard focus on a current
// item deepens that ring; no focus outline is drawn on top of it (one
// indicator, not two). Picked and rejected stay states: a current photo
// that is picked keeps its green edge, with the blue ring around it.
import { expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { renderToStaticMarkup } from 'react-dom/server'
import type { Cluster, FolderSummary, MediaURLs, Member } from '../types'
import { ClusterSection } from './ClusterSection'
import { ComparePane } from './ComparePane'
import { CURRENT, CURRENT_FOCUS } from './current'
import { FocusRail } from './FocusRail'
import { FOCUS_RING } from './focusRing'
import { FolderRow } from './FolderRow'
import { Thumb } from './Thumb'

const noop = () => {}
const media: MediaURLs = { thumb: () => '/t.jpg', full: () => '/f.jpg' }

const css = readFileSync(join(import.meta.dir, '..', 'base.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
/** The declarations of the first rule whose selector is exactly `selector`. */
function rule(selector: string): string[] {
  const at = css.indexOf(`${selector} {`)
  if (at < 0) throw new Error(`base.css has no rule for ${selector}`)
  return css
    .slice(css.indexOf('{', at) + 1, css.indexOf('}', at))
    .split(';')
    .map((d) => d.trim().replace(/\s+/g, ' '))
    .filter(Boolean)
}

/** The class list of the first element carrying `attr` in `html`. */
function classesAt(html: string, attr: string): string[] {
  const at = html.indexOf(attr)
  if (at < 0) throw new Error(`no ${attr} in ${html.slice(0, 200)}`)
  const open = html.lastIndexOf('<', at)
  const tag = html.slice(open, html.indexOf('>', at))
  const m = /class="([^"]*)"/.exec(tag)
  return (m?.[1] ?? '').split(/\s+/).filter(Boolean)
}

const has = (classes: string[], list: string) => list.split(' ').every((c) => classes.includes(c))

test('the current look is the Library row’s blue edge and soft blue ring; its keyboard focus deepens the ring, never an outline', () => {
  expect(CURRENT).toBe('border-sel shadow-sel-ring')
  expect(CURRENT_FOCUS).not.toContain('outline-pick')
  // An arbitrary property: Tailwind reads `shadow-[var(…)]` as a shadow colour.
  expect(CURRENT_FOCUS).toContain('focus-visible:[box-shadow:var(--shadow-sel-focus)]')
  expect(FOCUS_RING).toContain('focus-visible:outline-pick')
})

const folder: FolderSummary = {
  id: 'a',
  name: 'Alpha',
  parentPath: '~/Trips',
  state: 'ready',
  month: '2026-10',
  firstCaptured: null,
  lastCaptured: null,
  photos: 10,
  videos: 0,
  clusters: 2,
  picked: 0,
  rejected: 0,
  previews: [],
}
test('a Library row: current is the blue look; keyboard focus on it draws no outline on top', () => {
  const html = (selected: boolean) =>
    renderToStaticMarkup(<FolderRow folder={folder} selected={selected} onOpen={noop} onSelect={noop} onOpenPicks={noop} onMenu={noop} />)
  // The row is one button, the first in its markup.
  const button = (selected: boolean) => classesAt(html(selected), 'type="button"')
  expect(has(button(true), CURRENT)).toBe(true)
  expect(has(button(true), CURRENT_FOCUS)).toBe(true)
  expect(button(true)).not.toContain('focus-visible:outline-pick')
  // Not current: the hairline at rest; focus selects the row, so focus draws the same current look.
  expect(button(false)).toContain('border-line-1')
  expect(has(button(false), CURRENT)).toBe(false)
  expect(has(button(false), CURRENT_FOCUS)).toBe(true)
  expect(button(false)).not.toContain('focus-visible:outline-pick')
})

const member = (file_id: number, over: Partial<Member> = {}): Member =>
  ({ file_id, path: `IMG_${file_id}.HEIC`, kind: 'photo', picked: false, rejected: false, ...over }) as Member
const cluster: Cluster = { id: 7, device: 'X-T5', time: 'Sep 3 · 10:00', members: [member(1)] } as unknown as Cluster

test('the current cluster on the Folder page: the same blue look (no grey ring)', () => {
  const html = (active: boolean) =>
    renderToStaticMarkup(
      <ClusterSection
        cluster={cluster}
        active={active}
        counts={{ picked: 0, rejected: 0, reviewed: 0 }}
        media={media}
        qualityOn={false}
        onSelect={noop}
        onOpen={noop}
      />,
    )
  const section = (active: boolean) => classesAt(html(active), 'data-testid="cluster"')
  expect(has(section(true), CURRENT)).toBe(true)
  expect(section(true)).not.toContain('ring-line-2')
  expect(has(section(false), CURRENT)).toBe(false)
  // Same box whether current or not: a transparent edge keeps the layout still.
  expect(section(false)).toContain('border-transparent')
})

const rail = (members: Member[], activeIndex: number, comparing = false, compareIdx: number[] = []) =>
  renderToStaticMarkup(
    <FocusRail members={members} activeIndex={activeIndex} comparing={comparing} compareIdx={compareIdx} rating={() => undefined} media={media} onItem={noop} />,
  )
/** Rail item `i`: its button, which is the thumbnail. */
function railItem(html: string, i: number) {
  const at = html.indexOf(`data-rail-index="${i}"`)
  const open = html.lastIndexOf('<button', at)
  const tag = html.slice(open, html.indexOf('>', at))
  return { button: (/class="([^"]*)"/.exec(tag)?.[1] ?? '').split(/\s+/), thumb: tag }
}

test('Focus’ rail: the current photo is blue (no longer a white edge); keyboard focus on it draws no outline', () => {
  const html = rail([member(1), member(2)], 0)
  const current = railItem(html, 0)
  expect(current.thumb).toContain('data-current="true"')
  expect(current.thumb).not.toContain('data-state="active"')
  // The thumbnail is the button: base.css draws its focus (the deeper ring
  // when current, the focus ring otherwise) on the thumbnail itself.
  expect(has(current.button, 'thumb thumb-btn')).toBe(true)
  const other = railItem(html, 1)
  expect(other.thumb).not.toContain('data-current')
  expect(has(other.button, 'thumb thumb-btn')).toBe(true)
  expect(rule('.thumb-btn:focus-visible')).toEqual(['outline-color: var(--color-pick)'])
})

test('Focus’ rail: a current photo that is picked (or rejected) keeps its mark and is still current', () => {
  const html = rail([member(1, { picked: true }), member(2, { rejected: true })], 0)
  expect(railItem(html, 0).thumb).toContain('data-state="picked"')
  expect(railItem(html, 0).thumb).toContain('data-current="true"')
  const rejected = rail([member(1, { picked: true }), member(2, { rejected: true })], 1)
  expect(railItem(rejected, 1).thumb).toContain('data-state="rejected"')
  expect(railItem(rejected, 1).thumb).toContain('data-current="true"')
})

test('Focus’ rail while comparing: the photos in the compare set are the selection, in the same blue look', () => {
  const html = rail([member(1), member(2, { picked: true }), member(3)], 1, true, [0, 1])
  expect(railItem(html, 0).thumb).toContain('data-current="true"')
  expect(railItem(html, 1).thumb).toContain('data-current="true"')
  expect(railItem(html, 1).thumb).toContain('data-state="picked"')
  expect(railItem(html, 2).thumb).not.toContain('data-current')
})

test('a thumbnail’s current look comes from base.css, with the mark kept and a blue ring around it', () => {
  expect(rule('.thumb[data-current="true"]')).toEqual(['@apply border-sel shadow-sel-ring'])
  expect(rule('.thumb[data-current="true"][data-state="picked"]')).toEqual(['@apply border-pick', 'box-shadow: var(--shadow-sel-mark)'])
  expect(rule('.thumb[data-current="true"][data-state="rejected"]')).toEqual(['@apply border-reject', 'box-shadow: var(--shadow-sel-mark)'])
  // The old looks are gone: no white "active" edge, no separate "selected".
  expect(css).not.toContain('.thumb[data-state="active"]')
  expect(css).not.toContain('.thumb[data-state="selected"]')
})

test('keyboard focus on a current thumbnail deepens its ring and hides the outline', () => {
  expect(rule('.thumb-btn[data-current="true"]:focus-visible')).toEqual(['outline-color: transparent', 'box-shadow: var(--shadow-sel-focus)'])
  expect(rule('.thumb-btn[data-current="true"][data-state]:focus-visible')).toEqual(['box-shadow: var(--shadow-sel-mark-focus)'])
})

test('Thumb takes `current` apart from its mark', () => {
  const html = renderToStaticMarkup(<Thumb src="/t.jpg" alt="IMG" state="picked" current />)
  expect(html).toContain('data-state="picked"')
  expect(html).toContain('data-current="true"')
  expect(renderToStaticMarkup(<Thumb src="/t.jpg" alt="IMG" />)).not.toContain('data-current')
})

test('the active compare pane keeps the blue look it already had', () => {
  const html = renderToStaticMarkup(
    <ComparePane
      member={member(1)}
      pane={0}
      index={0}
      total={2}
      active
      rating={undefined}
      qualityOn={false}
      underZoom={false}
      zoomable
      media={media}
      state="ready"
      view={{ zoom: 1, pan: { x: 0, y: 0 } }}
      zoomBy={(v) => v}
      onView={noop}
      onActivate={noop}
      onMediaState={noop}
    />,
  )
  const pane = classesAt(html, 'data-testid="compare-pane"')
  expect(pane).toContain('data-[active=true]:border-sel')
  expect(pane).toContain('data-[active=true]:shadow-sel-ring')
})
