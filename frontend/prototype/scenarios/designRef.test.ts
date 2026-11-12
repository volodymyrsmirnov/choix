import { expect, test } from 'bun:test'
import { designRefProblem } from './designRef'

test('a v1 reference is a historical path under internal/ui/web/src', () => {
  expect(designRefProblem('v1:components/Toaster.tsx')).toBeNull()
  expect(designRefProblem('v1:keys.ts')).toBeNull()
  // The v1 sources are gone from the tree; the reference is not checked on disk.
  expect(designRefProblem('v1:components/RemovedLongAgo.tsx')).toBeNull()
  expect(designRefProblem('v1:never/existed/Nope.tsx')).toBeNull()
  expect(designRefProblem('v1:components')).toContain('is not')
  expect(designRefProblem('v1:../main.go')).toContain('leaves internal/ui/web/src')
  expect(designRefProblem('v1:components/../../main.go')).toContain('leaves internal/ui/web/src')
  expect(designRefProblem('v1:..')).not.toBeNull()
})

test('the other forms', () => {
  expect(designRefProblem('mockup-2026-11-11#s-a')).toBeNull()
  expect(designRefProblem('missing design')).toBeNull()
  expect(designRefProblem('Toaster.tsx')).toContain('is not')
  expect(designRefProblem('mockup#s-a')).toContain('is not')
})
