// The extraction time budget of extract.pw.ts's s109 test, as a pure rule.
//
// Locally the fastest of five warm runs must take at most BUDGET_MS. Shared
// CI runners are slower and noisier, so there the hard limit is CI_LIMIT_MS
// and a result between the two is reported as slow (a test annotation)
// instead of failing.

export const BUDGET_MS = 250
export const CI_LIMIT_MS = 1000

export type BudgetVerdict = 'pass' | 'slow' | 'fail'

export function budgetVerdict(fastestMs: number, ci: boolean): BudgetVerdict {
  if (fastestMs <= BUDGET_MS) return 'pass'
  if (ci && fastestMs <= CI_LIMIT_MS) return 'slow'
  return 'fail'
}
