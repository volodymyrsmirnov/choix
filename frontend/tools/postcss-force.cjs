// PostCSS plugin, run after Tailwind: gives every interaction selector a
// `data-force` twin so a hover / focus / press look can be rendered as a
// static state. The twin is appended to the same rule, so source order and
// specificity (an attribute selector weighs the same as a pseudo-class) are
// unchanged.
//
//   .a:hover            → .a:hover, .a[data-force~="hover"]
//   .g:hover .b         → .g:hover .b, .g[data-force~="hover"] .b
//   :focus, :focus-visible → [data-force~="focus"]
//   :focus-within       → [data-force~="focus-within"]
//   :active             → [data-force~="active"]
//
// A pseudo-class that follows a pseudo-element (`::-webkit-scrollbar-thumb:hover`)
// gets no twin: an attribute selector there is invalid and would drop the rule.

const PSEUDO = /(?<!\\):(hover|focus-visible|focus-within|focus|active)(?![\w-])/g

const FORCE = {
  hover: 'hover',
  'focus-visible': 'focus',
  'focus-within': 'focus-within',
  focus: 'focus',
  active: 'active',
}

/** True when the compound selector ending at `index` already holds a pseudo-element. */
function afterPseudoElement(selector, index) {
  const compound = selector.slice(0, index).split(/[\s>+~]/).pop() || ''
  return compound.includes('::')
}

/** The data-force twin of `selector`, or null when it has none. */
function twin(selector) {
  let found = false
  let invalid = false
  const out = selector.replace(PSEUDO, (match, name, offset) => {
    if (afterPseudoElement(selector, offset)) invalid = true
    found = true
    return `[data-force~="${FORCE[name]}"]`
  })
  return found && !invalid ? out : null
}

/** @type {import('postcss').PluginCreator<void>} */
module.exports = () => ({
  postcssPlugin: 'choix-force',
  Once(root) {
    root.walkRules((rule) => {
      const selectors = rule.selectors
      const twins = []
      for (const selector of selectors) {
        const t = twin(selector)
        if (t && !selectors.includes(t) && !twins.includes(t)) twins.push(t)
      }
      if (twins.length > 0) rule.selectors = [...selectors, ...twins]
    })
  },
})
module.exports.postcss = true
