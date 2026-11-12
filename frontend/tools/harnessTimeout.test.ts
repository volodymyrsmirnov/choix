// The prototype harness (prototype/harness.ts) gives a state this long to
// render before open() rejects. Shared CI runners are slow, so it is 10 s.
// (This test lives in tools/ because a new file under prototype/ would be a
// digest input.)
import { expect, test } from 'bun:test'
import { OPEN_TIMEOUT_MS } from '../prototype/harness'

test('open() waits up to 10 s for a state to render', () => {
  expect(OPEN_TIMEOUT_MS).toBe(10_000)
})
