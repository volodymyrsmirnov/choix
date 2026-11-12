// Gone: a listed folder whose directory was deleted is dropped by the next
// presence check, with library:changed "gone" naming it.
import { rmSync } from 'node:fs'
import { expect, folderPath, test } from './fixtures'

const path = folderPath('gone')

test('a deleted folder is dropped from the Library by CheckNow', async ({ harness }) => {
  await harness.resetLibrary()
  const { id, name } = await harness.addAndWait(path)
  expect((await harness.list()).map((f) => f.id)).toEqual([id])
  await harness.clearEvents()

  rmSync(path, { recursive: true, force: true })
  await harness.call('Library', 'CheckNow')

  expect((await harness.list()).map((f) => f.id)).not.toContain(id)
  await expect
    .poll(async () =>
      (await harness.events()).some(
        (e) => e.name === 'library:changed' && e.data?.reason === 'gone' && e.data?.folderId === id && e.data?.name === name,
      ),
    )
    .toBe(true)
})
