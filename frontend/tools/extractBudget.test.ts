import { expect, test } from 'bun:test'
import { BUDGET_MS, CI_LIMIT_MS, budgetVerdict } from './extractBudget'

test('locally the budget is 250 ms', () => {
  expect(BUDGET_MS).toBe(250)
  expect(budgetVerdict(120, false)).toBe('pass')
  expect(budgetVerdict(250, false)).toBe('pass')
  expect(budgetVerdict(251, false)).toBe('fail')
  expect(budgetVerdict(900, false)).toBe('fail')
})

test('in CI the hard limit is 1000 ms, and 250–1000 ms is only noted as slow', () => {
  expect(CI_LIMIT_MS).toBe(1000)
  expect(budgetVerdict(120, true)).toBe('pass')
  expect(budgetVerdict(250, true)).toBe('pass')
  expect(budgetVerdict(251, true)).toBe('slow')
  expect(budgetVerdict(1000, true)).toBe('slow')
  expect(budgetVerdict(1001, true)).toBe('fail')
})
