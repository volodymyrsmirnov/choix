// Vite only (import.meta.glob): collects every scenarios/<group>.tsx and
// checks the set against docs/design/retired.json. Never import this from a
// *.test.ts file; tests import the group files directly.
import retired from '../../../docs/design/retired.json'
import { defineScenarios, type Scenario } from '../registry'

const modules = import.meta.glob<{ scenarios: Scenario[] }>('./*.tsx', { eager: true })

const retiredNumbers = (retired as Array<number | { n: number }>).map((r) => (typeof r === 'number' ? r : r.n))

export const SCENARIOS = defineScenarios(
  Object.values(modules).flatMap((m) => m.scenarios),
  retiredNumbers,
)
