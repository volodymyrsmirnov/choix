// Remove: a declined confirmation keeps the folder; a confirmed one drops it
// from the Library and leaves the folder and its .choix on disk.
import { existsSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { expect, folderPath, test } from './fixtures'

const path = folderPath('remove')

test('Remove asks first and never touches the folder', async ({ harness }) => {
  await harness.resetLibrary()
  const { id } = await harness.addAndWait(path)

  await harness.call('TestSupport', 'SetConfirmAnswer', false)
  expect(await harness.call<boolean>('Library', 'Remove', id)).toBe(false)
  expect((await harness.list()).map((f) => f.id)).toEqual([id])

  await harness.call('TestSupport', 'SetConfirmAnswer', true)
  try {
    expect(await harness.call<boolean>('Library', 'Remove', id)).toBe(true)
  } finally {
    await harness.call('TestSupport', 'SetConfirmAnswer', false)
  }
  expect(await harness.list()).toEqual([])

  expect(statSync(path).isDirectory()).toBe(true)
  expect(statSync(join(path, '.choix')).isDirectory()).toBe(true)
  expect(existsSync(join(path, 'photo-0001.jpg'))).toBe(true)
})
