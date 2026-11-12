// Settings: a refused picks_dir is an invalid app error naming the field
// and the reason, and nothing is saved.
import { expect, test } from './fixtures'

test('a refused picks_dir is an invalid error and changes nothing', async ({ harness }) => {
  const before = await harness.call('Settings', 'Get')

  const absolute = await harness.attempt('Settings', 'Save', { picks_dir: '/tmp/x' })
  expect(absolute.ok).toBe(false)
  if (!absolute.ok) {
    expect(absolute.cause).toMatchObject({ code: 'invalid', field: 'picks_dir', reason: 'absolute' })
  }

  const dot = await harness.attempt('Settings', 'Save', { picks_dir: '.' })
  expect(dot.ok).toBe(false)
  if (!dot.ok) {
    expect(dot.cause).toMatchObject({ code: 'invalid', field: 'picks_dir', reason: 'reserved' })
  }

  expect(await harness.call('Settings', 'Get')).toEqual(before)
})
