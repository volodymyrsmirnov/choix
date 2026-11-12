import { describe, expect, test } from 'bun:test'
import postcss from 'postcss'
import tailwindcss from 'tailwindcss'
import config from '../tailwind.config'
import force from './postcss-force.cjs'

const FIXTURE = `
  <div class="hover:bg-bg-4"></div>
  <div class="group-hover:opacity-100"></div>
  <div class="group-hover/row:opacity-100"></div>
  <div class="peer-focus:block"></div>
  <div class="focus-visible:ring-2"></div>
  <div class="focus-within:bg-bg-3"></div>
  <div class="disabled:opacity-40 hover:bg-bg-4"></div>
`

const INPUT = `@tailwind utilities;
.x:active{}
::-webkit-scrollbar-thumb:hover{}
`

async function compile(): Promise<string[][]> {
  const result = await postcss([
    tailwindcss({ ...config, content: [{ raw: FIXTURE, extension: 'html' }] }),
    force(),
  ]).process(INPUT, { from: undefined })
  const rules: string[][] = []
  result.root.walkRules((rule) => {
    rules.push(rule.selectors)
  })
  return rules
}

const cases: [label: string, original: string, twin: string][] = [
  ['hover:', '.hover\\:bg-bg-4:hover', '.hover\\:bg-bg-4[data-force~="hover"]'],
  ['group-hover:', '.group:hover .group-hover\\:opacity-100', '.group[data-force~="hover"] .group-hover\\:opacity-100'],
  [
    'group-hover/row:',
    '.group\\/row:hover .group-hover\\/row\\:opacity-100',
    '.group\\/row[data-force~="hover"] .group-hover\\/row\\:opacity-100',
  ],
  ['peer-focus:', '.peer:focus ~ .peer-focus\\:block', '.peer[data-force~="focus"] ~ .peer-focus\\:block'],
  ['focus-visible:', '.focus-visible\\:ring-2:focus-visible', '.focus-visible\\:ring-2[data-force~="focus"]'],
  ['focus-within:', '.focus-within\\:bg-bg-3:focus-within', '.focus-within\\:bg-bg-3[data-force~="focus-within"]'],
  ['handwritten :active', '.x:active', '.x[data-force~="active"]'],
]

describe('postcss-force', () => {
  for (const [label, original, twin] of cases) {
    test(`${label} keeps the original selector and appends its data-force twin`, async () => {
      const rules = await compile()
      const rule = rules.find((selectors) => selectors.includes(original))
      expect(rule).toBeDefined()
      expect(rule).toEqual([original, twin])
    })
  }

  test('the disabled: rule still comes after the hover: rule', async () => {
    const rules = await compile()
    const hover = rules.findIndex((s) => s.includes('.hover\\:bg-bg-4:hover'))
    const disabled = rules.findIndex((s) => s.includes('.disabled\\:opacity-40:disabled'))
    expect(hover).toBeGreaterThanOrEqual(0)
    expect(disabled).toBeGreaterThan(hover)
    expect(rules[disabled]).toEqual(['.disabled\\:opacity-40:disabled'])
  })

  test('a pseudo-class after a pseudo-element gets no twin (it would invalidate the rule)', async () => {
    const rules = await compile()
    expect(rules).toContainEqual(['::-webkit-scrollbar-thumb:hover'])
  })

  test('running twice adds no duplicate twin', async () => {
    const once = await postcss([force()]).process('.x:hover{}', { from: undefined })
    const twice = await postcss([force()]).process(once.css, { from: undefined })
    expect(twice.css).toBe(once.css)
  })
})
