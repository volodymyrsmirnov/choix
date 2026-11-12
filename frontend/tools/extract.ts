// A state's fingerprint: the normalised rendered tree of one page, hashed.
//
// extractTree() and collectResources() run in the page. Playwright
// serialises them with Function#toString (ElementHandle.evaluate), so each is
// self-contained: no import and no module binding apart from DENYLIST and
// TYPED_PROPERTIES, which installExtractor() defines next to them in the page. hashTree() and the
// rest run on Node.
//
// What the tree holds, per element (script, style, template and anything
// marked data-proto-chrome are left out; so are the children of an element
// with display: none, and every shadow root, so the native controls a
// <video> draws inside its user-agent shadow tree never reach it):
//   tag, sorted classes, its own text (an element child is \u001f, so the
//   order of text and elements counts), the attributes in ATTRS, live form
//   state (value, checked, indeterminate, selectedIndex, ::placeholder
//   colour), SVG geometry, and an index into `styles`, the de-duplicated
//   records of computed values from computedStyleMap() — computed, not used
//   values, so a layout that differs by a sub-pixel between machines does not
//   move the hash — minus custom properties and DENYLIST.
// Every URL (src, srcset, poster, an SVG <image href>, a CSS url(), a font
// source) is replaced by `res:<first 16 hex of the sha256 of its bytes>`, so
// neither the server's origin nor a file path reaches the tree, and a file
// whose bytes change under the same URL still changes it. A reference to an
// element id (aria-labelledby, for, …) becomes `#<n>`, the referenced node's
// position in the tree, so generated ids never move the hash; so does an
// href or xlink:href fragment (`<use href="#id">`). A property an
// element lists in data-measured (computed from layout at run time) is left
// out (only those measured.ts allows for the id; an empty list is allowed,
// for a scroller whose position code sets), and the node's `measuredBy`
// carries `<id>:<sha256 of the files that compute it>` instead
// (src/design/measured.ts, prototype/measured.ts).
// Document-wide, the tree also holds, in document (cascade) order, what no
// element's computed style shows: every @font-face with all its descriptors;
// the rules that style pseudo-elements (scrollbars, the slider's track and
// thumb, spin buttons); the animation and transition declarations of every
// other rule (the freeze switch makes every element compute none); @keyframes
// and the other defining at-rules (@property, @counter-style…); the cascade
// layer order when any of those sits in a layer; and the <html> element's
// own style with any color-scheme <meta>. The custom properties those rules
// read enter every style record.
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import type { ElementHandle, Page } from '@playwright/test'

export interface TreeNode {
  tag: string
  cls: string[]
  text: string
  attrs: Record<string, string>
  props: Record<string, string>
  svg?: Record<string, string>
  /** Index into Tree.styles. */
  style: number
  measuredBy?: string
  children: TreeNode[]
}

/**
 * The rule lists below are document-wide, so they enter every state's hash.
 * Each keeps document order, which is the cascade order (of two equal rules
 * the later wins): nothing is sorted or de-duplicated. An entry is canonical
 * text: its context (the enclosing at-rule preludes and parent selectors,
 * `@layer` with the layer's full name), the selector, and the declarations
 * sorted (a block holds one value per property), every var() resolved
 * against the :root computed value (one :root does not define stays var(),
 * and every element records its computed value; see styles), every url() as
 * `res:`.
 */
export interface Tree {
  /** Every declared @font-face in document order: family, weight, style, the `res:` of its sources, every descriptor as declared (urls as `res:`), and its context if nested. */
  fonts: { family: string; weight: string; style: string; res: string; descriptors: Record<string, string>; context?: string }[]
  /**
   * Every style rule whose selector names a pseudo-element other than
   * ::before / ::after (scrollbars, slider track and thumb, spin buttons,
   * ::placeholder…), or that is nested in such a rule.
   */
  pseudoRules: string[]
  /** The animation* / transition* declarations of every other style rule: under freeze no element computes them. */
  motionRules: string[]
  /** @keyframes (each frame canonical) and every other at-rule that defines something (@property, @counter-style…), as text. */
  atRules: string[]
  /** Cascade layer names in the order they first appear; present only when a recorded rule or face sits in a layer. */
  layers?: string[]
  /** The <html> element: its style record, and the content of any color-scheme <meta> (joined by ` | `). */
  html: { style: number; colorScheme: string }
  styles: Record<string, string>[]
  root: TreeNode
}

/**
 * Computed properties left out of every style record. The rule: a property
 * may be dropped only if it is a logical alias of a physical property that is
 * kept, or its computed value differs by platform and a kept property still
 * changes when the design changes. Each entry has its reason and a `probe`,
 * the declaration extract.pw.ts sets to show that a change in the dropped
 * property's design intent still changes the hash.
 *
 * Measured on macOS and Linux (s109, s202 at every size, Task 6): no
 * property's computed value differed by platform once the components stopped
 * sizing by `ch` (lint-design: font-unit), so every entry is a logical alias.
 * writing-mode and direction are kept, so the physical value plus those two
 * determine the logical one. Dropping them saves ~10 % of the extraction.
 */
const LOGICAL: [logical: string, physical: string, probe: string][] = [
  ['block-size', 'height', '37px'],
  ['inline-size', 'width', '37px'],
  ['min-block-size', 'min-height', '37px'],
  ['min-inline-size', 'min-width', '37px'],
  ['max-block-size', 'max-height', '37px'],
  ['max-inline-size', 'max-width', '37px'],
  ['margin-block-start', 'margin-top', '7px'],
  ['margin-block-end', 'margin-bottom', '7px'],
  ['margin-inline-start', 'margin-left', '7px'],
  ['margin-inline-end', 'margin-right', '7px'],
  ['padding-block-start', 'padding-top', '7px'],
  ['padding-block-end', 'padding-bottom', '7px'],
  ['padding-inline-start', 'padding-left', '7px'],
  ['padding-inline-end', 'padding-right', '7px'],
  ['inset-block-start', 'top', '7px'],
  ['inset-block-end', 'bottom', '7px'],
  ['inset-inline-start', 'left', '7px'],
  ['inset-inline-end', 'right', '7px'],
  ['border-block-start-color', 'border-top-color', 'rgb(1, 2, 3)'],
  ['border-block-start-style', 'border-top-style', 'dashed'],
  ['border-block-start-width', 'border-top-width', '5px'],
  ['border-block-end-color', 'border-bottom-color', 'rgb(1, 2, 3)'],
  ['border-block-end-style', 'border-bottom-style', 'dashed'],
  ['border-block-end-width', 'border-bottom-width', '5px'],
  ['border-inline-start-color', 'border-left-color', 'rgb(1, 2, 3)'],
  ['border-inline-start-style', 'border-left-style', 'dashed'],
  ['border-inline-start-width', 'border-left-width', '5px'],
  ['border-inline-end-color', 'border-right-color', 'rgb(1, 2, 3)'],
  ['border-inline-end-style', 'border-right-style', 'dashed'],
  ['border-inline-end-width', 'border-right-width', '5px'],
  ['border-start-start-radius', 'border-top-left-radius', '5px'],
  ['border-start-end-radius', 'border-top-right-radius', '5px'],
  ['border-end-start-radius', 'border-bottom-left-radius', '5px'],
  ['border-end-end-radius', 'border-bottom-right-radius', '5px'],
  ['corner-start-start-shape', 'corner-top-left-shape', 'bevel'],
  ['corner-start-end-shape', 'corner-top-right-shape', 'bevel'],
  ['corner-end-start-shape', 'corner-bottom-left-shape', 'bevel'],
  ['corner-end-end-shape', 'corner-bottom-right-shape', 'bevel'],
  ['contain-intrinsic-block-size', 'contain-intrinsic-height', '37px'],
  ['contain-intrinsic-inline-size', 'contain-intrinsic-width', '37px'],
  ['overflow-block', 'overflow-y', 'hidden'],
  ['overflow-inline', 'overflow-x', 'hidden'],
  ['overscroll-behavior-block', 'overscroll-behavior-y', 'contain'],
  ['overscroll-behavior-inline', 'overscroll-behavior-x', 'contain'],
  ['scroll-margin-block-start', 'scroll-margin-top', '7px'],
  ['scroll-margin-block-end', 'scroll-margin-bottom', '7px'],
  ['scroll-margin-inline-start', 'scroll-margin-left', '7px'],
  ['scroll-margin-inline-end', 'scroll-margin-right', '7px'],
  ['scroll-padding-block-start', 'scroll-padding-top', '7px'],
  ['scroll-padding-block-end', 'scroll-padding-bottom', '7px'],
  ['scroll-padding-inline-start', 'scroll-padding-left', '7px'],
  ['scroll-padding-inline-end', 'scroll-padding-right', '7px'],
]

export const DENYLIST: Record<string, { reason: string; probe: string }> = Object.fromEntries(
  LOGICAL.map(([logical, physical, value]) => [
    logical,
    { reason: `logical alias of ${physical}, which is kept (with writing-mode and direction)`, probe: `${logical}: ${value}` },
  ]),
)

/**
 * Properties read from computedStyleMap() rather than getComputedStyle():
 * CSSOM's resolved-value special cases, whose getComputedStyle value is the
 * used value (a laid-out length) instead of the computed value.
 */
export const TYPED_PROPERTIES: string[] = [
  'width', 'height', 'min-width', 'min-height', 'max-width', 'max-height',
  'margin-top', 'margin-right', 'margin-bottom', 'margin-left',
  'padding-top', 'padding-right', 'padding-bottom', 'padding-left',
  'top', 'right', 'bottom', 'left',
  'line-height', 'transform', 'translate', 'rotate', 'scale', 'transform-origin', 'perspective-origin',
  'grid-template-columns', 'grid-template-rows', 'animation-duration',
]

/**
 * Builds the tree of `root`. `resources` maps every absolute URL the page
 * uses (collectResources) to its `res:` identity, and `measured:<id>` to
 * `<id>:<sha256>`. Throws on an unknown URL or measured id, on
 * `outline-style: auto`, and on a ::before / ::after with content.
 */
export function extractTree(root: Element, resources: Record<string, string>): Tree {
  // The second row: attributes that change rendering without showing in a
  // computed style (a range's thumb position, a textarea's intrinsic height,
  // list numbering, table spans, the srcset candidate). `value` on a form
  // control is its live value (props); elsewhere (li, meter…) it is here.
  const ATTRS = ['role', 'type', 'disabled', 'readonly', 'required', 'placeholder', 'title', 'alt', 'href', 'controls', 'open', 'hidden', 'tabindex', 'for', 'draggable', 'dir', 'lang', 'data-state',
    'min', 'max', 'step', 'low', 'high', 'optimum', 'rows', 'cols', 'size', 'colspan', 'rowspan', 'span', 'start', 'reversed', 'sizes']
  const RESOURCE_ATTRS = ['src', 'srcset', 'poster']
  /** SVG attributes left out of `svg`: styling hooks, generated ids, and what attrs already holds. */
  const SVG_SKIP = /^(?:class|style|id|xmlns(?::.*)?|data-.*|aria-.*)$/
  const ID_REF_ATTRS = new Set(['aria-activedescendant', 'aria-controls', 'aria-describedby', 'aria-details', 'aria-errormessage', 'aria-flowto', 'aria-labelledby', 'aria-owns', 'for'])
  const SKIP = new Set(['script', 'style', 'template', 'noscript'])
  const SVG_NS = 'http://www.w3.org/2000/svg'
  const deny = new Set(Object.keys(DENYLIST))
  const URL_IN_CSS = /url\(\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|([^)\s]*))\s*\)/g

  // --- The stylesheets: @font-face rules and pseudo-element rules. ---------
  const rootStyle = getComputedStyle(document.documentElement)
  /** Custom properties a pseudo-element rule reads: every element records their computed value. */
  const pseudoVars = new Set<string>()
  const PSEUDO_ELEMENT = /::(?!before(?![\w-])|after(?![\w-]))[\w-]+/
  /** Splits at top-level occurrences of `sep` (outside parentheses and strings). */
  const splitTop = (text: string, sep: string): string[] => {
    const out: string[] = []
    let depth = 0
    let quote = ''
    let start = 0
    for (let i = 0; i < text.length; i++) {
      const c = text[i]
      if (quote) {
        if (c === '\\') i++
        else if (c === quote) quote = ''
      } else if (c === '"' || c === "'") quote = c
      else if (c === '(') depth++
      else if (c === ')') depth--
      else if (c === sep && depth === 0) {
        out.push(text.slice(start, i))
        start = i + 1
      }
    }
    out.push(text.slice(start))
    return out
  }
  const resolveVars = (value: string): string => {
    let out = ''
    let i = 0
    for (;;) {
      const at = value.indexOf('var(', i)
      if (at === -1) return out + value.slice(i)
      out += value.slice(i, at)
      let depth = 0
      let j = at + 3
      for (; j < value.length; j++) {
        if (value[j] === '(') depth++
        else if (value[j] === ')' && --depth === 0) break
      }
      const [first, ...rest] = splitTop(value.slice(at + 4, j), ',')
      const name = first.trim()
      pseudoVars.add(name)
      const fromRoot = rootStyle.getPropertyValue(name).trim()
      if (fromRoot) out += fromRoot
      else out += rest.length ? `var(${name}, ${resolveVars(rest.join(',')).trim()})` : `var(${name})`
      i = j + 1
    }
  }
  const urlsAt = (value: string, base: string, what: string) =>
    value.replace(URL_IN_CSS, (_m, a, b, c) => {
      const abs = new URL((a ?? b ?? c).replace(/\\(.)/g, '$1'), base).href
      const r = resources[abs]
      if (!r) throw new Error(`extract: no resource identity for a URL in ${what}`)
      return `url(${r})`
    })
  /** A declaration block as `prop: value` pairs (shorthands kept as written, so a var() in one survives). */
  const declarations = (cssText: string, base: string, what: string): [string, string][] =>
    splitTop(cssText, ';')
      .map((d) => d.trim())
      .filter(Boolean)
      .map((d) => {
        const colon = d.indexOf(':')
        const prop = d.slice(0, colon).trim()
        let v = d.slice(colon + 1).trim()
        if (v.includes('url(')) v = urlsAt(v, base, what)
        return [prop, v] as [string, string]
      })
  // Rule types by name: some (CSSScopeRule, CSSStartingStyleRule,
  // CSSNestedDeclarations…) are missing from older engines and from the
  // TypeScript DOM library.
  const kind = (rule: CSSRule) => rule.constructor.name
  /** Layer names in the order they first appear (the cascade order of layers); anonymous layers are numbered. */
  const layerOrder = new Set<string>()
  let anonymousLayers = 0
  const declareLayer = (parent: string, name: string): string => {
    let full = parent
    for (const part of name ? name.split('.') : [`(anonymous ${++anonymousLayers})`]) {
      full = full ? `${full}.${part}` : part
      layerOrder.add(full)
    }
    return full
  }
  /** The prelude of a grouping rule, as the context of the rules inside it. */
  const context = (rule: CSSRule, layer: string): string => {
    if (rule instanceof CSSMediaRule) return `@media ${rule.conditionText}`
    if (rule instanceof CSSSupportsRule) return `@supports ${rule.conditionText}`
    if (rule instanceof CSSLayerBlockRule) return `@layer ${layer}`
    if (rule instanceof CSSStyleRule) return rule.selectorText
    if (kind(rule) === 'CSSContainerRule') return `@container ${(rule as CSSContainerRule).conditionText}`
    if (kind(rule) === 'CSSScopeRule') {
      const { start, end } = rule as unknown as { start: string | null; end: string | null }
      return `@scope${start !== null ? ` (${start})` : ''}${end !== null ? ` to (${end})` : ''}`
    }
    // Anything else (@starting-style, a future rule): its prelude as written.
    const text = rule.cssText
    const brace = text.indexOf('{')
    return (brace === -1 ? text : text.slice(0, brace)).trim() || `@${kind(rule)}`
  }
  const fonts: Tree['fonts'] = []
  const pseudoRules: string[] = []
  const motionRules: string[] = []
  const atRules: string[] = []
  /** Whether a recorded rule sits inside a cascade layer (then the layer order is part of the tree). */
  let layered = false
  const MOTION = /^(?:animation|transition)(?:-|$)/
  const unquote = (s: string) => s.trim().replace(/^(["'])(.*)\1$/, '$2')
  /** A declaration block as `prop: value` text: var() resolved, sorted (the block holds one value per property, so order inside it cannot matter). */
  const block = (style: CSSStyleDeclaration, base: string, what: string, keep?: RegExp): string | null => {
    const decls = declarations(style.cssText, base, what).filter(([k]) => !keep || keep.test(k))
    if (decls.length === 0) return null
    for (const [k, v] of decls) {
      // A longhand of a shorthand written with var() and then partly
      // overridden serialises as empty: its value cannot be read.
      if (v === '' && !k.startsWith('--')) {
        throw new Error(`extract: ${what} { ${k} } cannot be read back (a var() in a shorthand that a later longhand overrides): write the longhand`)
      }
    }
    return decls
      .map(([k, v]) => `${k}: ${resolveVars(v)}`)
      .sort()
      .join('; ')
  }
  const record = (list: string[], ctx: string[], text: string, inLayer: boolean) => {
    list.push([...ctx, text].join(' '))
    if (inLayer) layered = true
  }
  // Every list keeps document order, which is the cascade order: of two
  // rules of equal specificity (or two @font-face, @keyframes or @property of
  // one name) the later wins. Nothing is sorted or de-duplicated. The order
  // is deterministic: document.styleSheets follows the document, and the
  // prototype has one built stylesheet.
  const walkSheet = (rules: CSSRuleList, base: string, ctx: string[], layer: string | null, pseudo: boolean) => {
    for (const rule of Array.from(rules)) {
      const inLayer = layer !== null
      if (rule instanceof CSSImportRule) {
        throw new Error('extract: a stylesheet has an @import rule (the build inlines imports; one left over would hide its rules and faces)')
      }
      if (rule instanceof CSSFontFaceRule) {
        const s = rule.style
        const descriptors: Record<string, string> = {}
        for (const [k, v] of declarations(s.cssText, base, '@font-face')) descriptors[k] = k === 'font-family' ? unquote(v) : v
        const ids = (descriptors.src ?? '').match(/res:[0-9a-f]+/g) ?? []
        fonts.push({
          family: unquote(s.getPropertyValue('font-family')),
          weight: s.getPropertyValue('font-weight') || 'normal',
          style: s.getPropertyValue('font-style') || 'normal',
          res: ids.join(' '),
          descriptors,
          ...(ctx.length > 0 ? { context: ctx.join(' ') } : {}),
        })
        if (inLayer) layered = true
        continue
      }
      if (rule instanceof CSSLayerStatementRule) {
        for (const name of rule.nameList) declareLayer(layer ?? '', name)
        continue
      }
      if (rule instanceof CSSKeyframesRule) {
        const frames = Array.from(rule.cssRules).map((f) => {
          const k = f as CSSKeyframeRule
          return `${k.keyText} { ${block(k.style, base, `@keyframes ${rule.name} ${k.keyText}`) ?? ''} }`
        })
        record(atRules, ctx, `@keyframes ${rule.name} { ${frames.join(' ')} }`, inLayer)
        continue
      }
      if (rule instanceof CSSStyleRule || kind(rule) === 'CSSNestedDeclarations') {
        // A style rule styles a pseudo-element if its selector names one or
        // it is nested in a rule that does; nested declarations take their
        // parent's selector (`&`).
        const selector = rule instanceof CSSStyleRule ? rule.selectorText : '&'
        const style = (rule as CSSStyleRule).style
        const isPseudo = pseudo || PSEUDO_ELEMENT.test(selector)
        const what = [...ctx, selector].join(' ')
        if (isPseudo) {
          record(pseudoRules, ctx, `${selector} { ${block(style, base, what) ?? ''} }`, inLayer)
        } else {
          // Under the prototype's freeze switch every element computes
          // animation and transition as none, so the rules that set them are
          // the only place a motion edit shows.
          const motion = /animation|transition/.test(style.cssText) ? block(style, base, what, MOTION) : null
          if (motion !== null) record(motionRules, ctx, `${selector} { ${motion} }`, inLayer)
        }
        if (rule instanceof CSSStyleRule && rule.cssRules.length > 0) walkSheet(rule.cssRules, base, [...ctx, selector], layer, isPseudo)
        continue
      }
      if ('cssRules' in rule && (rule as CSSGroupingRule).cssRules) {
        let inner = layer
        if (rule instanceof CSSLayerBlockRule) inner = declareLayer(layer ?? '', rule.name)
        walkSheet((rule as CSSGroupingRule).cssRules, base, [...ctx, context(rule, inner ?? '')], inner, pseudo)
        continue
      }
      // Any other at-rule (@property, @counter-style, @font-palette-values,
      // @font-feature-values, @position-try…): what it defines reaches no
      // element's computed style, so its text is recorded.
      record(atRules, ctx, urlsAt(rule.cssText, base, `@${kind(rule)}`), inLayer)
    }
  }
  for (const sheet of Array.from(document.styleSheets)) walkSheet(sheet.cssRules, sheet.href ?? document.baseURI, [], null, false)
  // A face added from script (new FontFace + document.fonts.add) has a
  // source no stylesheet names, so its bytes would never reach the tree.
  if (document.fonts.size > fonts.length) {
    throw new Error(`extract: document.fonts has ${document.fonts.size} faces but the stylesheets declare ${fonts.length}: a FontFace added from script is not fingerprinted (declare it with @font-face)`)
  }
  const extraProps = [...pseudoVars].sort()

  const describe = (el: Element) => {
    const cls = (el.getAttribute('class') ?? '').trim().split(/\s+/).filter(Boolean).slice(0, 4)
    return `<${el.localName}${cls.length ? ` class="${cls.join(' ')}…"` : ''}>`
  }
  const res = (url: string, el: Element): string => {
    let abs: string
    try {
      abs = new URL(url, document.baseURI).href
    } catch {
      throw new Error(`extract: unparsable URL "${url}" on ${describe(el)}`)
    }
    const r = resources[abs]
    if (!r) throw new Error(`extract: no resource identity for a URL used by ${describe(el)}`)
    return r
  }
  const cssUrls = (value: string, el: Element) =>
    value.replace(/url\(\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|([^)\s]*))\s*\)/g, (_m, a, b, c) => `url(${res((a ?? b ?? c).replace(/\\(.)/g, '$1'), el)})`)
  const srcset = (value: string, el: Element) =>
    value
      .split(',')
      .map((part) => part.trim())
      .filter(Boolean)
      .map((part) => {
        const [url, ...descriptor] = part.split(/\s+/)
        return [res(url, el), ...descriptor].join(' ')
      })
      .join(', ')

  const styles: Record<string, string>[] = []
  const styleIndex = new Map<string, number>()
  const ids = new Map<string, number>()
  const refs: { attrs: Record<string, string>; name: string }[] = []
  /** An href / xlink:href that is a fragment (`#id`): becomes `#<n>` like an id reference. */
  const fragments: { rec: Record<string, string>; name: string }[] = []
  let ordinal = 0

  // Reading every property through the Typed OM costs ~4× getComputedStyle,
  // whose resolved value is the computed value for every property except
  // CSSOM's special cases: those (sizes, margins, paddings, insets,
  // line-height, the transform family) resolve to used values once laid out,
  // so they are read as computed values from computedStyleMap(). (Chromium
  // also prints animation-duration differently in the two.) extract.pw.ts
  // checks that the two agree on every other property of real states.
  const TYPED = new Set(TYPED_PROPERTIES)
  const NONE = new Set<string>()
  let props: string[] | null = null
  let typed: boolean[] | null = null
  let displayAt = -1
  let outlineAt = -1

  const styleOf = (el: Element, measured: Set<string>): { index: number; display: string } => {
    const map = el.computedStyleMap()
    const resolved = getComputedStyle(el)
    if (props === null) {
      props = [...Array.from(map.keys()).filter((p) => !p.startsWith('--') && !deny.has(p)).sort(), ...extraProps]
      typed = props.map((p) => TYPED.has(p))
      displayAt = props.indexOf('display')
      outlineAt = props.indexOf('outline-style')
    }
    // The values in `props` order; a measured property is left out (null).
    const values: (string | null)[] = new Array(props.length)
    for (let i = 0; i < props.length; i++) {
      const prop = props[i]
      if (measured.size > 0 && measured.has(prop)) {
        values[i] = null
        continue
      }
      let v: string
      if (typed![i]) {
        const all = map.getAll(prop)
        v = all.length === 1 ? String(all[0]) : all.map(String).join(', ')
      } else {
        v = resolved.getPropertyValue(prop)
      }
      if (v.includes('url(')) v = cssUrls(v, el)
      values[i] = v
    }
    if (values[outlineAt] === 'auto') throw new Error(`extract: ${describe(el)} has outline-style: auto (draw an explicit focus ring)`)
    for (const pseudo of ['::before', '::after']) {
      const content = getComputedStyle(el, pseudo).getPropertyValue('content')
      if (content !== 'none' && content !== 'normal' && content !== '') {
        throw new Error(`extract: ${describe(el)}${pseudo} has content ${content} (use a real element)`)
      }
    }
    const key = values.join('\u0000') + (measured.size > 0 ? `\u0001${[...measured].sort().join(' ')}` : '')
    let index = styleIndex.get(key)
    if (index === undefined) {
      index = styles.length
      const rec: Record<string, string> = {}
      for (let i = 0; i < props.length; i++) if (values[i] !== null) rec[props[i]] = values[i] as string
      styles.push(rec)
      styleIndex.set(key, index)
    }
    return { index, display: values[displayAt] ?? '' }
  }

  const visit = (el: Element): TreeNode | null => {
    const tag = el.localName
    if (SKIP.has(tag) || el.hasAttribute('data-proto-chrome')) return null
    const id = el.getAttribute('id')
    const me = ordinal++
    if (id !== null && !ids.has(id)) ids.set(id, me)

    const list = el.getAttribute('data-measured')
    const by = el.getAttribute('data-measured-by')
    if ((list === null) !== (by === null)) {
      throw new Error(`extract: ${describe(el)} has ${list === null ? 'data-measured-by' : 'data-measured'} without ${list === null ? 'data-measured' : 'data-measured-by'}`)
    }
    const measured = list === null ? NONE : new Set(list.split(/\s+/).filter(Boolean))
    if (by !== null) {
      const allowed = resources[`measuredProps:${by}`]
      if (allowed === undefined) throw new Error(`extract: ${describe(el)} is measured by "${by}", which no measured.ts registers`)
      const may = new Set(allowed.split(' ').filter(Boolean))
      for (const p of measured) {
        if (!may.has(p)) throw new Error(`extract: ${describe(el)} drops ${p}, which measured.ts does not allow "${by}" to drop`)
      }
    }
    const { index, display } = styleOf(el, measured)

    const attrs: Record<string, string> = {}
    for (const a of Array.from(el.attributes)) {
      const name = a.name
      if (name.startsWith('aria-') || ATTRS.includes(name)) {
        if (name === 'href') {
          // An SVG <image>'s href is a resource (svg.href); a fragment is a
          // reference to an element (`#<n>`, as aria-labelledby); otherwise a
          // navigation target, without the origin.
          if (el.namespaceURI === SVG_NS && tag === 'image') continue
          if (a.value.startsWith('#')) {
            attrs.href = a.value
            fragments.push({ rec: attrs, name })
          } else {
            const u = new URL(a.value, document.baseURI)
            attrs.href = u.origin === location.origin ? u.pathname + u.search + u.hash : u.href
          }
        } else {
          attrs[name] = a.value
        }
        if (ID_REF_ATTRS.has(name)) refs.push({ attrs, name })
      } else if (RESOURCE_ATTRS.includes(name)) {
        attrs[name] = name === 'srcset' ? srcset(a.value, el) : res(a.value, el)
      } else if (name === 'value' && !(el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement)) {
        attrs.value = a.value
      }
    }

    const live: Record<string, string> = {}
    if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement) {
      live.value = el.value
      if (el instanceof HTMLSelectElement) live.selectedIndex = String(el.selectedIndex)
      if (el instanceof HTMLInputElement && (el.type === 'checkbox' || el.type === 'radio')) {
        live.checked = String(el.checked)
        live.indeterminate = String(el.indeterminate)
      }
      if (!(el instanceof HTMLSelectElement) && el.hasAttribute('placeholder')) {
        live.placeholderColor = getComputedStyle(el, '::placeholder').getPropertyValue('color')
      }
    }

    let svg: Record<string, string> | undefined
    if (el.namespaceURI === SVG_NS) {
      // Every attribute: geometry, preserveAspectRatio, pathLength, gradient
      // and filter parameters… (presentation attributes such as fill also
      // reach the computed style).
      // An <image>'s href is a resource; any other href is in attrs.
      svg = {}
      for (const a of Array.from(el.attributes)) {
        if (SVG_SKIP.test(a.name) || ATTRS.includes(a.name) || (tag === 'image' && (a.name === 'href' || a.name === 'xlink:href'))) continue
        svg[a.name] = a.value
        if (a.name === 'xlink:href' && a.value.startsWith('#')) fragments.push({ rec: svg, name: a.name })
      }
      if (tag === 'image') {
        const href = el.getAttribute('href') ?? el.getAttributeNS('http://www.w3.org/1999/xlink', 'href')
        if (href !== null) svg.href = res(href, el)
      }
    }

    const children: TreeNode[] = []
    let text = ''
    let hasText = false
    if (display !== 'none') {
      for (const child of Array.from(el.childNodes)) {
        if (child.nodeType === Node.TEXT_NODE) {
          text += (child as Text).data
          hasText = true
        } else if (child.nodeType === Node.ELEMENT_NODE) {
          const node = visit(child as Element)
          if (node) {
            children.push(node)
            text += '\u001f'
          }
        }
      }
    }
    const node: TreeNode = { tag, cls: [], text: hasText ? text : '', attrs, props: live, style: index, children }
    node.cls = Array.from(new Set((el.getAttribute('class') ?? '').split(/\s+/).filter(Boolean))).sort()
    if (svg) node.svg = svg
    if (by !== null) {
      const m = resources[`measured:${by}`]
      if (!m) throw new Error(`extract: ${describe(el)} is measured by "${by}", which no measured.ts registers`)
      node.measuredBy = m
    }
    return node
  }

  const tree = visit(root)
  if (!tree) throw new Error('extract: the root is skipped')
  for (const r of refs) {
    r.attrs[r.name] = r.attrs[r.name]
      .split(/\s+/)
      .filter(Boolean)
      .map((id) => (ids.has(id) ? `#${ids.get(id)}` : '#?'))
      .join(' ')
  }
  // A bare `#` stays as written.
  for (const f of fragments) {
    const id = f.rec[f.name].slice(1)
    if (id) f.rec[f.name] = ids.has(id) ? `#${ids.get(id)}` : '#?'
  }

  // <html> is outside the tree (it starts at <body>), but its own style
  // paints the canvas (background, color-scheme, scrollbars); a
  // color-scheme <meta> sets the page's scheme without changing any
  // computed value.
  const html = {
    style: document.documentElement === root ? tree.style : styleOf(document.documentElement, NONE).index,
    colorScheme: Array.from(document.querySelectorAll('meta[name="color-scheme" i]'), (m) => m.getAttribute('content') ?? '').join(' | '),
  }

  return { fonts, pseudoRules, motionRules, atRules, ...(layered ? { layers: [...layerOrder] } : {}), html, styles, root: tree }
}

/**
 * Every absolute URL extractTree(root) can meet: the src, srcset, poster and
 * SVG <image href> attributes under `root` and in its inline styles
 * (`urls`), and every url() of each linked stylesheet, rules and @font-face
 * sources alike (`sheets`, by the sheet's URL; an inline <style> counts as
 * part of `urls`). A linked sheet named in `skip` is not read again (its URLs
 * are cached by the caller). Self-contained (runs in the page).
 */
export function collectResources(root: Element, skip: string[] = []): { urls: string[]; sheets: Record<string, string[]> } {
  const URL_RE = /url\(\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|([^)\s]*))\s*\)/g
  const abs = (url: string, base: string): string | null => {
    try {
      return new URL(url.replace(/\\(.)/g, '$1'), base).href
    } catch {
      return null // extractTree reports it
    }
  }
  const scan = (css: string, base: string, out: Set<string>) => {
    for (const m of css.matchAll(URL_RE)) {
      const u = abs(m[1] ?? m[2] ?? m[3], base)
      if (u) out.add(u)
    }
  }
  const walk = (rules: CSSRuleList, base: string, out: Set<string>) => {
    for (const rule of Array.from(rules)) {
      if ('cssRules' in rule) walk((rule as CSSGroupingRule).cssRules, base, out)
      else scan(rule.cssText, base, out)
    }
  }
  const urls = new Set<string>()
  const sheets: Record<string, string[]> = {}
  for (const sheet of Array.from(document.styleSheets)) {
    if (sheet.href === null) {
      walk(sheet.cssRules, document.baseURI, urls)
    } else if (!skip.includes(sheet.href)) {
      const found = new Set<string>()
      walk(sheet.cssRules, sheet.href, found)
      sheets[sheet.href] = [...found].sort()
    }
  }
  const base = document.baseURI
  const add = (url: string) => {
    const u = abs(url, base)
    if (u) urls.add(u)
  }
  for (const el of [root, ...Array.from(root.querySelectorAll('*'))]) {
    for (const name of ['src', 'poster']) {
      const v = el.getAttribute(name)
      if (v !== null) add(v)
    }
    const set = el.getAttribute('srcset')
    if (set !== null) for (const part of set.split(',')) if (part.trim()) add(part.trim().split(/\s+/)[0])
    if (el.localName === 'image') {
      const href = el.getAttribute('href') ?? el.getAttributeNS('http://www.w3.org/1999/xlink', 'href')
      if (href !== null) add(href)
    }
    const style = el.getAttribute('style')
    if (style) scan(style, base, urls)
  }
  return { urls: [...urls].sort(), sheets }
}

// ---------------------------------------------------------------------------
// Node side.

const sha256 = (data: string | Uint8Array) => createHash('sha256').update(data).digest('hex')

/**
 * JSON with sorted object keys at every level. (A replacer that rebuilds each
 * object with sorted keys: native, so ~5× faster than a hand-written
 * serialiser. JS would order integer-like keys first; a Tree has none.)
 */
export function canonicalJson(value: unknown): string {
  return JSON.stringify(value, (_k, v: unknown) => {
    if (v === null || typeof v !== 'object' || Array.isArray(v)) return v
    const obj = v as Record<string, unknown>
    const out: Record<string, unknown> = {}
    for (const k of Object.keys(obj).sort()) out[k] = obj[k]
    return out
  })
}

/** sha256 hex of the tree's canonical JSON (sorted keys). */
export function hashTree(t: Tree): string {
  return sha256(canonicalJson(t))
}

/** `res:<first 16 hex of the sha256 of the bytes>`. */
export const resourceId = (bytes: Uint8Array | string) => `res:${sha256(bytes).slice(0, 16)}`

/**
 * The `res:` identity of every URL: a data: URL by its own text, a local
 * URL by the bytes the server returns for it. Anything else throws (a state
 * must not depend on another host).
 */
export async function resolveResources(urls: string[], cache?: Map<string, string>): Promise<Record<string, string>> {
  const out: Record<string, string> = {}
  await Promise.all(
    urls.map(async (url) => {
      const known = cache?.get(url)
      if (known) {
        out[url] = known
        return
      }
      const u = new URL(url)
      if (u.protocol === 'data:') {
        out[url] = resourceId(url)
      } else if (u.protocol !== 'http:' || !['127.0.0.1', 'localhost'].includes(u.hostname)) {
        throw new Error(`extract: a state uses a resource on another host (${u.protocol}//${u.hostname})`)
      } else {
        const r = await fetch(url)
        if (!r.ok) throw new Error(`extract: ${u.pathname} answered ${r.status}`)
        out[url] = resourceId(new Uint8Array(await r.arrayBuffer()))
      }
      cache?.set(url, out[url])
    }),
  )
  return out
}

/**
 * For every id of src/design/measured.ts and prototype/measured.ts:
 * `measured:<id>` → `<id>:<sha256>`, the sha256 over the files both name for
 * the id (paths relative to frontend/), in sorted path order, each as
 * `path\0sha256(content)\n`; and `measuredProps:<id>` → the space-separated
 * properties an element measured by that id may leave out (possibly none).
 */
export function measuredDigests(frontend: string, registries: Record<string, { files: string[]; props?: string[] }>[]): Record<string, string> {
  const files = new Map<string, Set<string>>()
  const props = new Map<string, Set<string>>()
  for (const reg of registries) {
    for (const [id, entry] of Object.entries(reg)) {
      const f = files.get(id) ?? new Set<string>()
      for (const x of entry.files) f.add(x)
      files.set(id, f)
      const p = props.get(id) ?? new Set<string>()
      for (const x of entry.props ?? []) p.add(x)
      props.set(id, p)
    }
  }
  const out: Record<string, string> = {}
  for (const [id, set] of files) {
    const body = [...set]
      .sort()
      .map((f) => `${f}\0${sha256(readFileSync(join(frontend, f)))}\n`)
      .join('')
    out[`measured:${id}`] = `${id}:${sha256(body)}`
    out[`measuredProps:${id}`] = [...(props.get(id) ?? [])].sort().join(' ')
  }
  return out
}

/**
 * Defines DENYLIST, TYPED_PROPERTIES and window.__choixExtract (extractTree)
 * in the page. Idempotent.
 */
export async function installExtractor(page: Page): Promise<void> {
  await page.evaluate(
    `window.DENYLIST = ${JSON.stringify(DENYLIST)}; window.TYPED_PROPERTIES = ${JSON.stringify(TYPED_PROPERTIES)}; window.__choixExtract = (${extractTree.toString()});`,
  )
}

export interface ExtractOptions {
  /** `measured:<id>` → `<id>:<sha256>` (measuredDigests). */
  measured?: Record<string, string>
  /** Resource identities by URL, kept across pages: only for a server whose files do not change (one build). */
  cache?: Map<string, string>
  /** Default: <body>. */
  root?: ElementHandle<Element>
  /** Filled with the milliseconds of each phase (collect, resolve, extract, parse). */
  timings?: Record<string, number>
}

/**
 * Extracts a ready page. The tree crosses to Node as one JSON string:
 * Playwright's structured transfer of ~1 MB of style records costs more than
 * the extraction itself.
 */
export async function extractPage(page: Page, o: ExtractOptions = {}): Promise<Tree> {
  let t = performance.now()
  const lap = (phase: string) => {
    const now = performance.now()
    if (o.timings) o.timings[phase] = Math.round(now - t)
    t = now
  }
  await installExtractor(page)
  const el = o.root ?? ((await page.$('body')) as ElementHandle<Element>)
  const known = o.cache ? [...o.cache.keys()].filter((k) => k.startsWith('sheet:')).map((k) => k.slice(6)) : []
  const found = await el.evaluate(collectResources, known)
  const urls = new Set(found.urls)
  for (const [href, list] of Object.entries(found.sheets)) {
    o.cache?.set(`sheet:${href}`, JSON.stringify(list))
    for (const u of list) urls.add(u)
  }
  for (const href of known) for (const u of JSON.parse(o.cache!.get(`sheet:${href}`)!) as string[]) urls.add(u)
  lap('collect')
  const resources = { ...(await resolveResources([...urls].sort(), o.cache)), ...o.measured }
  lap('resolve')
  // The style records cross as value arrays over one key list (half the
  // bytes: ~1 MB of repeated property names otherwise).
  const [inPage, json] = await el.evaluate((node, r) => {
    const started = performance.now()
    const tree = (window as unknown as { __choixExtract: typeof extractTree }).__choixExtract(node, r)
    const ms = performance.now() - started
    const keys = Array.from(new Set(tree.styles.flatMap((s) => Object.keys(s))))
    const packed = { ...tree, keys, styles: tree.styles.map((s) => keys.map((k) => (k in s ? s[k] : null))) }
    return [ms, JSON.stringify(packed)] as const
  }, resources)
  if (o.timings) o.timings.inPage = Math.round(inPage)
  lap('extract')
  const packed = JSON.parse(json) as Omit<Tree, 'styles'> & { keys: string[]; styles: (string | null)[][] }
  const styles = packed.styles.map((values) => {
    const rec: Record<string, string> = {}
    values.forEach((v, i) => {
      if (v !== null) rec[packed.keys[i]] = v
    })
    return rec
  })
  const { keys: _keys, styles: _packed, ...rest } = packed
  const tree: Tree = { ...rest, styles }
  lap('parse')
  return tree
}

