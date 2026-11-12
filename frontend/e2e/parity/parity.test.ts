// The pure parts of the parity harness (parity.ts): the tree comparison, the
// normalisation of prototype-only machinery (pinned in both directions: what
// it lets through, and what it must still report), and which states need a
// case. The browser side is pinned by selftest.pw.ts.
import { describe, expect, test } from 'bun:test'
import type { Tree, TreeNode } from '../../tools/extract'
import {
  approvedStates,
  compareTrees,
  parseApprovers,
  NODE_FIELDS,
  subsequenceDiff,
  TREE_FIELDS,
  normaliseTree,
  parseEnabled,
  planParity,
  registeredStates,
  requireCase,
  silentGroupMessage,
  silentGroups,
  stripForceTwins,
  validateCases,
  type ParityCase,
  type RootLayout,
} from './parity'

const n = (tag: string, o: Partial<TreeNode> = {}): TreeNode => ({ tag, cls: [], text: '', attrs: {}, props: {}, style: 0, children: [], ...o })

/** main.library > ul > li × 3; style 0 is the default, style 1 the rows'. */
function tree(
  o: {
    rowPadding?: string
    third?: Partial<TreeNode>
    pseudoRules?: string[]
    motionRules?: string[]
    atRules?: string[]
    layers?: string[]
    html?: Tree['html']
    styles?: Record<string, string>[]
  } = {},
): Tree {
  const row = (text: string, extra: Partial<TreeNode> = {}) => n('li', { cls: ['row'], text, style: 1, ...extra })
  return {
    fonts: [{ family: 'Inter', weight: '400', style: 'normal', res: 'res:1', descriptors: {} }],
    pseudoRules: o.pseudoRules ?? [],
    motionRules: o.motionRules ?? [],
    atRules: o.atRules ?? [],
    ...(o.layers ? { layers: o.layers } : {}),
    // The <html> element shares style 0 (the default record).
    html: o.html ?? { style: 0, colorScheme: '' },
    styles: o.styles ?? [{ color: 'rgb(1, 2, 3)', 'padding-left': '0px' }, { color: 'rgb(1, 2, 3)', 'padding-left': o.rowPadding ?? '12px' }],
    root: n('div', {
      cls: ['flex', 'flex-col', 'overflow-hidden'],
      text: '\u001f',
      children: [
        n('main', {
          cls: ['library'],
          text: '\u001f',
          children: [n('ul', { text: '\u001f\u001f\u001f', children: [row('One'), row('Two'), row('Three', o.third)] })],
        }),
      ],
    }),
  }
}

const ROOT = 'div.flex.flex-col.overflow-hidden'
const ROW3 = `${ROOT} > main.library > ul > li.row:nth-child(3)`
const LAYOUT: RootLayout = { x: 0, y: 0, width: 1280, height: 800, documentScrolls: 'none' }

/** A tree whose root has a style record of its own (index 2). */
function rooted(root: Record<string, string>, o: { pseudoRules?: string[]; motionRules?: string[] } = {}): Tree {
  const t = tree({ ...o, styles: [...tree().styles, { color: 'rgb(1, 2, 3)', ...root }] })
  t.root.style = 2
  return t
}

describe('compareTrees', () => {
  test('equal trees: null', () => {
    expect(compareTrees(tree(), tree())).toBeNull()
  })

  test('a style differs: the node path, style.<property>, both values', () => {
    const app = tree({ third: { style: 2 }, styles: [...tree().styles, { color: 'rgb(1, 2, 3)', 'padding-left': '13px' }] })
    expect(compareTrees(app, tree())).toEqual({ path: ROW3, field: 'style.padding-left', app: '13px', prototype: '12px' })
  })

  test('style records are compared by content, not by index', () => {
    // The same records in the other order, every index swapped to match.
    const app = tree({ styles: [{ color: 'rgb(1, 2, 3)', 'padding-left': '12px' }, { color: 'rgb(1, 2, 3)', 'padding-left': '0px' }] })
    const swap = (node: TreeNode) => {
      node.style = 1 - node.style
      node.children.forEach(swap)
    }
    swap(app.root)
    app.html.style = 1
    expect(compareTrees(app, tree())).toBeNull()
  })

  test('tag, text, class, attribute, live state, SVG geometry and measuredBy', () => {
    expect(compareTrees(tree({ third: { tag: 'div' } }), tree())).toEqual({ path: ROW3, field: 'tag', app: 'div', prototype: 'li' })
    expect(compareTrees(tree({ third: { text: 'Tree' } }), tree())).toEqual({ path: ROW3, field: 'text', app: 'Tree', prototype: 'Three' })
    expect(compareTrees(tree({ third: { cls: ['row', 'x'] } }), tree())).toMatchObject({ path: ROW3, field: 'class', app: 'row x', prototype: 'row' })
    expect(compareTrees(tree({ third: { attrs: { 'aria-label': 'Third' } } }), tree())).toEqual({ path: ROW3, field: 'attr.aria-label', app: 'Third', prototype: '(absent)' })
    expect(compareTrees(tree({ third: { props: { value: 'a' } } }), tree({ third: { props: { value: 'b' } } }))).toMatchObject({ field: 'prop.value', app: 'a', prototype: 'b' })
    expect(compareTrees(tree({ third: { svg: { d: 'M0 0L10 9' } } }), tree({ third: { svg: { d: 'M0 0L10 10' } } }))).toEqual({
      path: ROW3,
      field: 'svg.d',
      app: 'M0 0L10 9',
      prototype: 'M0 0L10 10',
    })
    expect(compareTrees(tree(), tree({ third: { svg: { viewBox: '0 0 10 10' } } }))).toMatchObject({ field: 'svg.viewBox', app: '(absent)', prototype: '0 0 10 10' })
    expect(compareTrees(tree({ third: { measuredBy: 'focus-zoom:aa' } }), tree({ third: { measuredBy: 'focus-zoom:bb' } }))).toEqual({
      path: ROW3,
      field: 'measuredBy',
      app: 'focus-zoom:aa',
      prototype: 'focus-zoom:bb',
    })
    expect(compareTrees(tree(), tree({ third: { measuredBy: 'cluster-strip:cc' } }))).toMatchObject({ field: 'measuredBy', app: '(absent)' })
  })

  test('an extra child: children.length on the parent, with the children named', () => {
    const app = tree()
    app.root.children[0].children[0].children.push(n('li', { cls: ['row'], style: 1 }))
    expect(compareTrees(app, tree())).toEqual({
      path: `${ROOT} > main.library > ul`,
      field: 'children.length',
      app: '4 (li.row, li.row, li.row, li.row)',
      prototype: '3 (li.row, li.row, li.row)',
    })
  })

  test('document fields come first: fonts, then the rule lists', () => {
    const app = tree({ third: { text: 'x' }, pseudoRules: ['.a::placeholder { color: red }'] })
    expect(compareTrees(app, tree())).toEqual({ path: 'document', field: 'pseudoRules[0]', app: '.a::placeholder { color: red }', prototype: '(absent)' })
    const fonts = tree()
    fonts.fonts[0].weight = '500'
    expect(compareTrees(fonts, tree())).toMatchObject({ path: 'document', field: 'fonts[0]' })
  })

  // The four document-wide rule lists: the app's list must be an ordered
  // subsequence of the prototype's (prototype-only extras allowed).
  const LISTS = ['pseudoRules', 'motionRules', 'atRules', 'layers'] as const
  const A = 'rule a'
  const B = 'rule b'
  const C = 'rule c'
  for (const list of LISTS) {
    const t = (rules: string[]) => tree({ [list]: rules })
    test(`${list}: equal lists, and an app list that is a subsequence in order, are equal`, () => {
      expect(compareTrees(t([A, B]), t([A, B]))).toBeNull()
      expect(compareTrees(t([A, C]), t([A, B, C]))).toBeNull()
      expect(compareTrees(t([]), t([A]))).toBeNull()
    })
    test(`${list}: an app-only rule, a swapped order and an app duplicate are differences`, () => {
      expect(compareTrees(t([A, C]), t([A, B]))).toEqual({ path: 'document', field: `${list}[1]`, app: C, prototype: B })
      expect(compareTrees(t([B, A]), t([A, B]))).toEqual({ path: 'document', field: `${list}[1]`, app: A, prototype: '(absent)' })
      expect(compareTrees(t([A, A]), t([A, B]))).toEqual({ path: 'document', field: `${list}[1]`, app: A, prototype: B })
      expect(compareTrees(t([A, B, A]), t([A, B]))).toEqual({ path: 'document', field: `${list}[2]`, app: A, prototype: '(absent)' })
    })
  }

  test('subsequenceDiff names the nearest prototype rule', () => {
    expect(subsequenceDiff('x', [A, C], [A, B, B])).toEqual({ path: 'document', field: 'x[1]', app: C, prototype: B })
    expect(subsequenceDiff('x', [A, A, A], [A, B, A])).toEqual({ path: 'document', field: 'x[2]', app: A, prototype: '(absent)' })
  })

  test('the rule lists survive normalisation in order, duplicates kept', () => {
    expect(compareTrees(normaliseTree(tree({ pseudoRules: [B, A] }), LAYOUT), normaliseTree(tree({ pseudoRules: [A, B] }), LAYOUT))).toMatchObject({ field: 'pseudoRules[1]' })
    expect(compareTrees(normaliseTree(tree({ motionRules: [A, B, A] }), LAYOUT), normaliseTree(tree({ motionRules: [A, B] }), LAYOUT))).toMatchObject({ field: 'motionRules[2]' })
  })

  test('the <html> element: its color-scheme meta, then its style by content', () => {
    expect(compareTrees(tree({ html: { style: 0, colorScheme: 'dark' } }), tree())).toEqual({ path: 'html', field: 'html.colorScheme', app: 'dark', prototype: '' })
    const own = (bg: string) => tree({ html: { style: 2, colorScheme: '' }, styles: [...tree().styles, { 'background-color': bg }] })
    expect(compareTrees(own('rgb(1, 1, 1)'), own('rgb(1, 1, 1)'))).toBeNull()
    expect(compareTrees(own('rgb(1, 1, 1)'), own('rgb(2, 2, 2)'))).toEqual({ path: 'html', field: 'html.style.background-color', app: 'rgb(1, 1, 1)', prototype: 'rgb(2, 2, 2)' })
  })

  test('fonts: in order, with their context', () => {
    const two = (contexts: (string | undefined)[]) => {
      const t = tree()
      t.fonts = contexts.map((context, i) => ({ family: 'Inter', weight: String(400 + i * 100), style: 'normal', res: `res:${i}`, descriptors: {}, ...(context ? { context } : {}) }))
      return t
    }
    expect(compareTrees(two([undefined, '@media print']), two([undefined, '@media print']))).toBeNull()
    expect(compareTrees(two([undefined, '@media print']), two([undefined, undefined]))).toMatchObject({ field: 'fonts[1]' })
    const swapped = two([undefined, undefined])
    swapped.fonts.reverse()
    expect(compareTrees(swapped, two([undefined, undefined]))).toMatchObject({ field: 'fonts[0]' })
  })

  test('a new attribute key and an SVG attribute go through attr.* and svg.*', () => {
    expect(compareTrees(tree({ third: { attrs: { colspan: '2' } } }), tree({ third: { attrs: { colspan: '1' } } }))).toEqual({ path: ROW3, field: 'attr.colspan', app: '2', prototype: '1' })
    expect(compareTrees(tree({ third: { attrs: { href: '#4' } } }), tree({ third: { attrs: { href: '#5' } } }))).toMatchObject({ field: 'attr.href' })
    expect(compareTrees(tree({ third: { svg: { preserveAspectRatio: 'none' } } }), tree({ third: { svg: { preserveAspectRatio: 'xMinYMin' } } }))).toEqual({
      path: ROW3,
      field: 'svg.preserveAspectRatio',
      app: 'none',
      prototype: 'xMinYMin',
    })
    expect(compareTrees(tree({ third: { svg: { 'xlink:href': '#3' } } }), tree())).toMatchObject({ field: 'svg.xlink:href', app: '#3', prototype: '(absent)' })
  })
})

// Exhaustive by construction: one change per key of Tree and TreeNode. The
// mapped types ({ [K in keyof …]-?: … }) make a field added to either type
// without a row here fail `bun run check`; each row must then make
// compareTrees report a difference, so a field compareTrees ignores fails.
describe('compareTrees covers every field', () => {
  const TREE_CHANGES: { [K in keyof Tree]-?: (t: Tree) => void } = {
    fonts: (t) => {
      t.fonts[0].res = 'res:2'
    },
    pseudoRules: (t) => {
      t.pseudoRules = ['.a::placeholder { color: red }']
    },
    motionRules: (t) => {
      t.motionRules = ['.a { transition-duration: 1s }']
    },
    atRules: (t) => {
      t.atRules = ['@keyframes k { 0% { opacity: 0 } }']
    },
    layers: (t) => {
      t.layers = ['base']
    },
    html: (t) => {
      t.html = { ...t.html, colorScheme: 'light' }
    },
    styles: (t) => {
      t.styles = t.styles.map((s, i) => (i === 1 ? { ...s, 'padding-left': '99px' } : s))
    },
    root: (t) => {
      t.root = { ...t.root, tag: 'section' }
    },
  }
  const NODE_CHANGES: { [K in keyof TreeNode]-?: (n: TreeNode, t: Tree) => void } = {
    tag: (node) => {
      node.tag = 'p'
    },
    cls: (node) => {
      node.cls = ['row', 'extra']
    },
    text: (node) => {
      node.text = 'Changed'
    },
    attrs: (node) => {
      node.attrs = { colspan: '2' }
    },
    props: (node) => {
      node.props = { value: 'x' }
    },
    svg: (node) => {
      node.svg = { pathLength: '10' }
    },
    style: (node, t) => {
      t.styles.push({ color: 'rgb(9, 9, 9)' })
      node.style = t.styles.length - 1
    },
    measuredBy: (node) => {
      node.measuredBy = 'focus-zoom:aa'
    },
    children: (node) => {
      node.children = [n('span')]
    },
  }

  test('the field tables of parity.ts name every key', () => {
    expect(Object.keys(TREE_FIELDS).sort()).toEqual(Object.keys(TREE_CHANGES).sort())
    expect(Object.keys(NODE_FIELDS).sort()).toEqual(Object.keys(NODE_CHANGES).sort())
  })

  for (const [key, change] of Object.entries(TREE_CHANGES) as [keyof Tree, (t: Tree) => void][]) {
    test(`Tree.${key}`, () => {
      const app = tree()
      change(app)
      expect(compareTrees(app, tree())).not.toBeNull()
    })
  }
  for (const [key, change] of Object.entries(NODE_CHANGES) as [keyof TreeNode, (n: TreeNode, t: Tree) => void][]) {
    test(`TreeNode.${key}`, () => {
      const app = tree()
      change(app.root.children[0].children[0].children[2], app)
      expect(compareTrees(app, tree())).not.toBeNull()
    })
  }
})

describe('normaliseTree', () => {
  // The selectorText values a page's forceTwinSelectors() would report for these rules.
  const MIXED = '.a:hover::placeholder, .a[data-force~="hover"]::placeholder'
  const ONLY_TWIN = '.b[data-force~="hover"]::placeholder'
  const A_TWIN = '.a[data-force~="hover"]::placeholder'
  const TWINS = [MIXED, ONLY_TWIN, A_TWIN]

  test('data-force twins leave the pseudo-element rules', () => {
    expect(stripForceTwins(`${MIXED} { color: red }`, TWINS)).toBe('.a:hover::placeholder { color: red }')
    expect(stripForceTwins('@media (min-width: 1px), print .a:hover::-webkit-scrollbar { width: 1px }', TWINS)).toBe(
      '@media (min-width: 1px), print .a:hover::-webkit-scrollbar { width: 1px }',
    )
    expect(stripForceTwins('[data-force~="focus"]::placeholder { color: red }', ['[data-force~="focus"]::placeholder'])).toBeNull()
    const nested = '.b:is(.c, [data-x="1,2"])::placeholder, .b[data-force~="active"]::placeholder'
    expect(stripForceTwins(`${nested} { color: red }`, [nested])).toBe('.b:is(.c, [data-x="1,2"])::placeholder { color: red }')
    expect(stripForceTwins('@layer base, components;', TWINS)).toBe('@layer base, components;')
  })

  test('only the selector list is split, never the context before it', () => {
    // The context holds a top-level comma; the selector list is one twin.
    expect(stripForceTwins('@media (min-width: 1px), print .a[data-force~="hover"]::placeholder { color: red }', TWINS)).toBeNull()
    // A mixed list under such a context keeps the context and its own non-twin selector.
    expect(stripForceTwins(`@media (min-width: 1px), print @supports (color: red) ${MIXED} { color: red }`, TWINS)).toBe(
      '@media (min-width: 1px), print @supports (color: red) .a:hover::placeholder { color: red }',
    )
    // The longest selectorText that ends the head is the selector list.
    const outer = `.g ${A_TWIN}`
    expect(stripForceTwins(`.g:hover ::placeholder, ${outer} { color: red }`, [A_TWIN, `.g:hover ::placeholder, ${outer}`])).toBe(
      '.g:hover ::placeholder { color: red }',
    )
    // A head that ends with no reported selectorText is left alone (and so still differs).
    expect(stripForceTwins(`${MIXED} { color: red }`, [])).toBe(`${MIXED} { color: red }`)
  })

  test('an app build without twins equals a prototype build with them, rule for rule and in place', () => {
    const first = '.z::placeholder { color: green }'
    const last = '.y::-webkit-scrollbar { width: 1px }'
    const proto = tree({
      pseudoRules: [
        first,
        `${MIXED} { color: red }`,
        // A rule made only of twins disappears.
        `${ONLY_TWIN} { color: blue }`,
        last,
      ],
    })
    const app = tree({ pseudoRules: [first, '.a:hover::placeholder { color: red }', last] })
    const normal = normaliseTree(proto, LAYOUT, TWINS)
    // The mixed rule keeps its non-twin selector and its position.
    expect(normal.pseudoRules).toEqual([first, '.a:hover::placeholder { color: red }', last])
    expect(compareTrees(normaliseTree(app, LAYOUT, []), normal)).toBeNull()
    // The twin-only rule disappears on the app side too, if the app build has it.
    const appWithTwins = tree({ pseudoRules: [first, '.a:hover::placeholder { color: red }', `${ONLY_TWIN} { color: blue }`, last] })
    expect(compareTrees(normaliseTree(appWithTwins, LAYOUT, [ONLY_TWIN]), normal)).toBeNull()
  })

  test('a pseudo-rule difference that is not a twin is still reported', () => {
    const proto = tree({ pseudoRules: [`${MIXED} { color: red }`] })
    const app = tree({ pseudoRules: ['.a:hover::placeholder { color: blue }'] })
    expect(compareTrees(normaliseTree(app, LAYOUT, []), normaliseTree(proto, LAYOUT, TWINS))).toEqual({
      path: 'document',
      field: 'pseudoRules[0]',
      app: '.a:hover::placeholder { color: blue }',
      prototype: '.a:hover::placeholder { color: red }',
    })
    // Stripping removes twins only: a rule whose selector list differs in a
    // non-twin selector is another rule, so it is an app-only rule.
    const wider = tree({ pseudoRules: ['.a:hover::placeholder, .c::placeholder { color: red }'] })
    expect(compareTrees(normaliseTree(wider, LAYOUT, []), normaliseTree(proto, LAYOUT, TWINS))).toMatchObject({
      field: 'pseudoRules[0]',
      app: '.a:hover::placeholder, .c::placeholder { color: red }',
      prototype: '.a:hover::placeholder { color: red }',
    })
    // An app build whose only copy of a rule is its twin has no rule left: the
    // prototype's rule is then a prototype-side extra, which is allowed (header).
    const twinOnly = tree({ pseudoRules: [`${A_TWIN} { color: red }`] })
    expect(normaliseTree(twinOnly, LAYOUT, [A_TWIN]).pseudoRules).toEqual([])
  })

  // Motion rules carry :hover / :active selectors, so they have twins too.
  const M_MIXED = '.btn:hover, .btn[data-force~="hover"]'
  const M_ONLY_TWIN = '.g[data-force~="active"] .x'
  const M_TWINS = [M_MIXED, M_ONLY_TWIN]

  test('data-force twins leave the motion rules, which keep their places', () => {
    const first = '.a { transition-duration: 0.15s }'
    const proto = tree({ motionRules: [first, `${M_MIXED} { transition-duration: 0.2s }`, `${M_ONLY_TWIN} { transition-duration: 0s }`, '.z { animation: k 1s }'] })
    const normal = normaliseTree(proto, LAYOUT, M_TWINS)
    expect(normal.motionRules).toEqual([first, '.btn:hover { transition-duration: 0.2s }', '.z { animation: k 1s }'])
    const app = tree({ motionRules: [first, '.btn:hover { transition-duration: 0.2s }', '.z { animation: k 1s }'] })
    expect(compareTrees(normaliseTree(app, LAYOUT, []), normal)).toBeNull()
    // A context with a top-level comma is never split.
    expect(stripForceTwins(`@media (min-width: 1px), print ${M_MIXED} { transition-duration: 0.2s }`, M_TWINS)).toBe(
      '@media (min-width: 1px), print .btn:hover { transition-duration: 0.2s }',
    )
  })

  test('a motion-rule difference that is not a twin is still reported', () => {
    const proto = normaliseTree(tree({ motionRules: [`${M_MIXED} { transition-duration: 0.2s }`] }), LAYOUT, M_TWINS)
    const app = tree({ motionRules: ['.btn:hover { transition-duration: 0.3s }'] })
    expect(compareTrees(normaliseTree(app, LAYOUT, []), proto)).toEqual({
      path: 'document',
      field: 'motionRules[0]',
      app: '.btn:hover { transition-duration: 0.3s }',
      prototype: '.btn:hover { transition-duration: 0.2s }',
    })
    // A rule without a reported twin selector is left exactly as it is.
    expect(stripForceTwins(`${M_MIXED} { transition-duration: 0.2s }`, [])).toBe(`${M_MIXED} { transition-duration: 0.2s }`)
  })

  test("the root's width and height are its laid-out size", () => {
    const proto = rooted({ width: '1280px', height: '800px' })
    const app = rooted({ width: 'auto', height: 'auto' })
    expect(compareTrees(normaliseTree(app, LAYOUT), normaliseTree(proto, LAYOUT))).toBeNull()
    expect(compareTrees(normaliseTree(app, { ...LAYOUT, width: 1200 }), normaliseTree(proto, LAYOUT))).toEqual({
      path: ROOT,
      field: 'style.width',
      app: '1200px',
      prototype: '1280px',
    })
  })

  test('a shifted or clipped root, or a scrolling document, is reported', () => {
    const proto = normaliseTree(rooted({ width: '1280px', height: '800px' }), LAYOUT)
    const app = rooted({ width: 'auto', height: 'auto' })
    // Padding on #root: same size, moved.
    expect(compareTrees(normaliseTree(app, { ...LAYOUT, x: 8, y: 8 }), proto)).toEqual({ path: ROOT, field: 'style.root:x', app: '8px', prototype: '0px' })
    expect(compareTrees(normaliseTree(app, { ...LAYOUT, y: 4 }), proto)).toMatchObject({ field: 'style.root:y', app: '4px', prototype: '0px' })
    // A document taller than the window.
    expect(compareTrees(normaliseTree(app, { ...LAYOUT, documentScrolls: 'y' }), proto)).toEqual({
      path: ROOT,
      field: 'style.root:document-scrolls',
      app: 'y',
      prototype: 'none',
    })
  })

  test("every other root property is still compared (min-height)", () => {
    const proto = rooted({ width: '1280px', height: '800px', 'min-height': '0px' })
    const app = rooted({ width: 'auto', height: 'auto', 'min-height': '100vh' })
    expect(compareTrees(normaliseTree(app, LAYOUT), normaliseTree(proto, LAYOUT))).toEqual({
      path: ROOT,
      field: 'style.min-height',
      app: '100vh',
      prototype: '0px',
    })
  })

  test("the root gets its own record: the input and a descendant sharing the record are untouched", () => {
    const shared = tree({ styles: [{ color: 'rgb(1, 2, 3)', width: 'auto', height: 'auto' }, ...tree().styles.slice(1)] })
    const out = normaliseTree(shared, LAYOUT)
    expect(out.styles[out.root.style]).toMatchObject({ width: '1280px', height: '800px', 'root:x': '0px', 'root:y': '0px', 'root:document-scrolls': 'none' })
    expect(out.styles[out.root.children[0].style].width).toBe('auto')
    expect(shared.root.style).toBe(0)
    expect(shared.styles[0].width).toBe('auto')
  })
})

describe('which states need a case', () => {
  const snapshot = {
    states: {
      600: { hash: 'a', label: 'Shortcut sheet' },
      601: { hash: 'b', label: 'Pick toast' },
      602: { hash: 'c', label: 'Error toast' },
      700: { hash: 'd', label: 'Choix menu' },
      701: { hash: 'f', label: 'File' },
      100: { hash: 'e', label: 'Empty' },
      101: { hash: 'g', label: 'One folder' },
      500: { hash: 'h', label: 'General' },
    },
  }
  const by = 'owner@example.com'
  const ledger = {
    states: [
      { state: 600, hash: 'a', by },
      { state: 601, hash: 'b', by },
      { state: 602, hash: 'stale', by },
      { state: 700, hash: 'd', by },
      { state: 100, hash: 'e', by },
      { state: 101, hash: 'g', by: 'agent@example.com' },
    ],
  }
  const approvers = parseApprovers(`# Design approvers: one git email per line.\n${by}\n`)
  const approved = approvedStates(snapshot, ledger, approvers)
  const registered = registeredStates(snapshot)

  test('approved means the ledger hash is the current hash, in an entry made by an approver', () => {
    expect(approved.map((s) => s.n)).toEqual([100, 600, 601, 700])
    expect(approvedStates(snapshot, null, approvers)).toEqual([])
    expect(registered).toEqual([100, 101, 500, 600, 601, 602, 700, 701])
  })

  test('an entry whose `by` is not in approvers.txt counts for nothing, as in gate.trusted_ledger', () => {
    expect(approved.map((s) => s.n)).not.toContain(101)
    // Emails compare case-insensitively, ignoring surrounding space.
    const upper = { states: [{ state: 101, hash: 'g', by: ' Owner@Example.COM ' }] }
    expect(approvedStates(snapshot, upper, approvers).map((s) => s.n)).toEqual([101])
    // An entry with no `by` is not trusted.
    expect(approvedStates(snapshot, { states: [{ state: 101, hash: 'g' }] }, approvers)).toEqual([])
    // No approvers.txt: no entry is trusted.
    expect(approvedStates(snapshot, ledger, null)).toEqual([])
    expect(approvedStates(snapshot, ledger, parseApprovers(null))).toEqual([])
  })

  test('approvers.txt: one email per line, # comments and blank lines skipped, lower-cased', () => {
    expect(parseApprovers(null)).toBeNull()
    const text = '# c\n\n  A@B.example  \n# x@y\nc@d.example\n'
    expect([...(parseApprovers(text) ?? [])]).toEqual(['a@b.example', 'c@d.example'])
  })

  test('nothing enabled: nothing planned', () => {
    expect(planParity(approved, [], [])).toEqual([])
    expect(silentGroups(registered, [], [], [])).toEqual([])
  })

  test('an enabled group plans its approved states; a state without a case fails with "no parity case for #N"', () => {
    const cases: ParityCase[] = [{ state: 600, drive: async () => {} }]
    const plan = planParity(approved, ['overlays'], cases)
    expect(plan.map((s) => [s.n, s.group, s.case !== null])).toEqual([
      [600, 'overlays', true],
      [601, 'overlays', false],
    ])
    expect(requireCase(plan[0])).toBe(cases[0])
    expect(() => requireCase(plan[1])).toThrow(/^no parity case for #601$/)
  })

  test('a skip entry is a case', () => {
    const plan = planParity(approved, ['native'], [{ state: 700, skip: 'native surface' }])
    expect(requireCase(plan[0]).skip).toBe('native surface')
  })

  test('an enabled group with nothing to compare gets a sentinel; a group of skip entries does not', () => {
    const skips: ParityCase[] = [
      { state: 700, skip: 'native surface' },
      { state: 701, skip: 'native surface' },
    ]
    const cases: ParityCase[] = [...skips, { state: 500, drive: async () => {} }, { state: 600, drive: async () => {} }]
    const enabled = ['settings', 'native', 'library', 'overlays'] as const
    const plan = planParity(approved, [...enabled], cases)
    // settings: #500 has a case but is not approved. native: skip entries only.
    // library: #100 is approved and has no case, so it fails for that. overlays: #600 runs.
    expect(silentGroups(registered, plan, [...enabled], cases)).toEqual(['settings'])
    // A group whose approved states are all skipped while others are not: nothing runs.
    const partly: ParityCase[] = [{ state: 600, skip: 'not drivable' }, { state: 601, skip: 'not drivable' }]
    expect(silentGroups(registered, planParity(approved, ['overlays'], partly), ['overlays'], partly)).toEqual(['overlays'])
    // Nothing approved at all.
    expect(silentGroups(registered, planParity([], ['library', 'native'], skips), ['library', 'native'], skips)).toEqual(['library'])
    expect(silentGroupMessage('overlays')).toBe(
      'parity group "overlays" is enabled but no approved state of it has a case to compare (none approved at its current hash, or every approved one skipped)',
    )
  })

  test('enabled.json holds known group ids only', () => {
    expect(parseEnabled('[]')).toEqual([])
    expect(parseEnabled('["library", "overlays"]')).toEqual(['library', 'overlays'])
    expect(() => parseEnabled('["libary"]')).toThrow(/unknown group "libary"/)
    expect(() => parseEnabled('{"library": true}')).toThrow(/array of group ids/)
  })

  test('the case table: one case per state, a skip carries no code and has a reason', () => {
    expect(() => validateCases([{ state: 600 }, { state: 600 }])).toThrow(/#600 has two cases/)
    expect(() => validateCases([{ state: 700, skip: 'native surface', drive: async () => {} }])).toThrow(/#700 is skipped but also drives/)
    expect(() => validateCases([{ state: 700, skip: ' ' }])).toThrow(/without a reason/)
  })
})
