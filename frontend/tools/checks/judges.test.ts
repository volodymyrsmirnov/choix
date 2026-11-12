import { describe, expect, test } from 'bun:test'
import { judgeAlign } from './align'
import { judgeCentred } from './centred'
import { judgePointerOnly, judgeRing } from './focus'
import { isLocal } from './network'
import { judgeOrphans } from './orphans'
import { judgeSubject } from './subject'

describe('judgeOrphans', () => {
  const block = (lines: [number, number][]) => ({ selector: 'p', lines: lines.map(([width, words]) => ({ width, words })) })

  test('a one-word last line under 30 % of the widest line is an orphan', () => {
    expect(judgeOrphans([block([[300, 6], [89, 1]])]).map((r) => r.check)).toEqual(['orphan'])
  })

  test('at 30 % or wider, or with two words, it is not', () => {
    expect(judgeOrphans([block([[300, 6], [90, 1]])])).toEqual([])
    expect(judgeOrphans([block([[300, 6], [40, 2]])])).toEqual([])
  })

  test('a single line is never an orphan', () => {
    expect(judgeOrphans([block([[20, 1]])])).toEqual([])
  })

  test('the widest line counts, not the first', () => {
    expect(judgeOrphans([block([[100, 3], [400, 8], [110, 1]])]).length).toBe(1)
  })
})

describe('judgeAlign', () => {
  const group = (axis: 'x' | 'y', values: number[]) => ({
    axis,
    name: 'g',
    items: values.map((value, i) => ({ selector: `#e${i}`, value })),
  })

  test('within 0.5 px is aligned', () => {
    expect(judgeAlign([group('y', [24, 24.5]), group('x', [18, 18.25, 18.5])])).toEqual([])
  })

  test('beyond 0.5 px fails and names the group and the axis', () => {
    const [r] = judgeAlign([group('y', [24, 24.6])])
    expect(r?.check).toBe('align')
    expect(r?.message).toContain('data-align-y="g"')
    expect(judgeAlign([group('x', [18, 22])])[0]?.message).toContain('left edges')
  })

  test('a group of one is never misaligned', () => {
    expect(judgeAlign([group('y', [3])])).toEqual([])
  })
})

describe('isLocal', () => {
  test('localhost and 127.0.0.1 on any port, and data/blob URLs, are local', () => {
    expect(isLocal('http://127.0.0.1:53211/assets/a.js')).toBe(true)
    expect(isLocal('http://localhost:8811/')).toBe(true)
    expect(isLocal('data:image/png;base64,AAAA')).toBe(true)
    expect(isLocal('blob:http://127.0.0.1:1/x')).toBe(true)
  })

  test('any other host is not', () => {
    expect(isLocal('https://fonts.gstatic.com/x.woff2')).toBe(false)
    expect(isLocal('http://example.invalid/fixture.png')).toBe(false)
    expect(isLocal('http://127.0.0.2/')).toBe(false)
    expect(isLocal('not a url')).toBe(false)
  })
})

describe('judgeRing', () => {
  const ring = (over: Partial<import('./dom').Ring> = {}) => ({
    outlineStyle: 'solid',
    outlineWidth: 2,
    outlineColor: 'rgba(0, 0, 0, 0)',
    boxShadow: 'none',
    border: '1px solid rgb(1, 2, 3)',
    ...over,
  })

  test('an outline that becomes drawn is an indicator', () => {
    expect(judgeRing(ring(), ring({ outlineColor: 'rgb(76, 200, 80)' }))).toBeNull()
    expect(judgeRing(ring({ outlineStyle: 'none', outlineWidth: 0 }), ring({ outlineColor: 'oklch(0.7 0.2 140)' }))).toBeNull()
  })

  test('a transparent or zero-width outline is not', () => {
    expect(judgeRing(ring(), ring({ outlineColor: 'transparent' }))).toContain('no visible focus indicator')
    expect(judgeRing(ring(), ring({ outlineColor: 'oklch(0.7 0.2 140 / 0)' }))).toContain('no visible focus indicator')
    expect(judgeRing(ring(), ring({ outlineWidth: 0, outlineColor: 'rgb(76, 200, 80)' }))).toContain('no visible focus indicator')
  })

  test('a new box-shadow or a changed border is an indicator; an unchanged one is not', () => {
    expect(judgeRing(ring(), ring({ boxShadow: 'rgb(76, 200, 80) 0px 0px 0px 3px' }))).toBeNull()
    expect(judgeRing(ring({ boxShadow: 'x 0px 0px 3px' }), ring({ boxShadow: 'x 0px 0px 3px' }))).toContain('no visible')
    expect(judgeRing(ring(), ring({ border: '1px solid rgb(76, 200, 80)' }))).toBeNull()
  })

  test('the platform ring is a finding of its own', () => {
    expect(judgeRing(ring(), ring({ outlineStyle: 'auto', outlineColor: 'rgb(0, 95, 204)' }))).toContain('outline-style: auto')
  })
})

describe('judgeCentred', () => {
  const item = (axes: ('x' | 'y')[], dx: number, dy: number) => ({ selector: '#thumb', axes, dx, dy })

  test('within 0.5 px on the marked axes is centred', () => {
    expect(judgeCentred([item(['y'], 3, 0.5), item(['x', 'y'], -0.5, 0.25)])).toEqual([])
  })

  test('only the marked axis counts', () => {
    expect(judgeCentred([item(['y'], 4, 0)])).toEqual([])
    expect(judgeCentred([item(['x'], 4, 0)])[0]?.message).toContain('horizontally by 4px')
  })

  test('1 px off fails and says which way', () => {
    const [r] = judgeCentred([item(['y'], 0, 1)])
    expect(r?.check).toBe('centred')
    expect(r?.message).toContain('vertically by 1px')
  })

  test('an element with no box to centre in is a finding', () => {
    expect(judgeCentred([item(['y'], Number.NaN, Number.NaN)])[0]?.message).toContain('no box')
  })
})

describe('judgeSubject', () => {
  const item = (out: Partial<Record<'top' | 'right' | 'bottom' | 'left', number>>, hidden = false) => ({
    selector: 'p',
    hidden,
    out: { top: -1, right: -1, bottom: -1, left: -1, ...out },
  })

  test('a subject inside its visible box passes; half a pixel over is tolerated', () => {
    expect(judgeSubject({ items: [item({}), item({ bottom: 0.5 })], missing: null })).toEqual([])
  })

  test('a subject past an edge, unrendered, or a selector that matches nothing fails', () => {
    const r = judgeSubject({ items: [item({ bottom: 40 }), item({}, true)], missing: '.gone' })
    expect(r.map((x) => x.check)).toEqual(['subject', 'subject', 'subject'])
    expect(r.map((x) => x.message).join('\n')).toContain('40px past the bottom')
    expect(r.map((x) => x.message).join('\n')).toContain('".gone"')
  })

  test('a subject that is not seen (opacity 0, visibility hidden) fails', () => {
    const r = judgeSubject({ items: [{ ...item({}), unseen: true }], missing: null })
    expect(r.map((x) => x.message)).toEqual(['the state subject is not visible (opacity 0 or visibility hidden)'])
  })
})

describe('judgePointerOnly', () => {
  const it0 = { selector: 'button', reason: 'paging: thumbnails are Tab stops', tabIndex: -1, ariaHidden: 'true' }

  test('a reason, tabIndex < 0 and aria-hidden: no finding', () => {
    expect(judgePointerOnly([it0])).toEqual([])
  })

  test('focusable, without a reason, or exposed to assistive technology: one finding each', () => {
    expect(judgePointerOnly([{ ...it0, tabIndex: 0 }]).map((r) => r.message)).toEqual([expect.stringContaining('must not be focusable')])
    expect(judgePointerOnly([{ ...it0, reason: ' ' }]).map((r) => r.message)).toEqual([expect.stringContaining('needs a reason')])
    expect(judgePointerOnly([{ ...it0, ariaHidden: null }]).map((r) => r.message)).toEqual([expect.stringContaining('aria-hidden')])
  })
})
