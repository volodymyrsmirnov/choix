// Events: adding a folder emits library:changed "added", the job's
// scan:state sequence and throttled scan:progress for every stage; folders
// added together scan two at a time, each with its own state sequence.
import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { dirs, expect, folderPath, test, type RecordedEvent } from './fixtures'

const path = folderPath('events')

/** The job slots of the queue (session.DefaultSlots). */
const SLOTS = 2
/** Photos per folder of the slots test: enough that the scans overlap. */
const PHOTOS = 300

/** perSecond counts the events in each wall-clock second. */
function perSecond(events: RecordedEvent[]): Map<number, number> {
  const counts = new Map<number, number>()
  for (const e of events) {
    const s = Math.floor(e.at / 1000)
    counts.set(s, (counts.get(s) ?? 0) + 1)
  }
  return counts
}

test('adding a folder emits its library, state and progress events', async ({ harness }) => {
  await harness.resetLibrary()
  await harness.clearEvents()
  const { id } = await harness.addAndWait(path)

  // The last events may still be on the socket when WaitIdle answers.
  await expect
    .poll(async () => (await harness.events()).some((e) => e.name === 'scan:state' && e.data?.folderId === id && e.data?.state === 'ready'))
    .toBe(true)
  const events = await harness.events()
  const mine = events.filter((e) => e.data?.folderId === id)

  expect(mine.some((e) => e.name === 'library:changed' && e.data.reason === 'added')).toBe(true)

  const states = mine.filter((e) => e.name === 'scan:state').map((e) => e.data.state as string)
  const queued = states.indexOf('queued')
  const scanning = states.indexOf('scanning')
  const ready = states.lastIndexOf('ready')
  expect(queued, `scan:state sequence ${states.join(', ')}`).toBeGreaterThanOrEqual(0)
  expect(scanning, `scan:state sequence ${states.join(', ')}`).toBeGreaterThan(queued)
  expect(ready, `scan:state sequence ${states.join(', ')}`).toBeGreaterThan(scanning)

  const progress = mine.filter((e) => e.name === 'scan:progress')
  const stages = new Set(progress.map((e) => e.data.progress?.stage as string))
  for (const stage of ['metadata', 'thumb', 'analyze', 'cluster']) {
    expect(stages, `scan:progress stages ${[...stages].join(', ')}`).toContain(stage)
  }

  // The cluster stage counts like the others: its end carries done of total
  // files, not an indeterminate 0 of 0.
  const clusterEnd = progress.filter((e) => e.data.progress?.stage === 'cluster' && e.data.phase === 'done').at(-1)
  const clustered = clusterEnd?.data.progress
  expect(clustered?.total, `the cluster stage reports a count (${JSON.stringify(clustered)})`).toBeGreaterThan(0)
  expect(clustered?.done, `the cluster stage ends at its total (${JSON.stringify(clustered)})`).toBe(clustered?.total)

  const busiest = Math.max(...perSecond(events.filter((e) => e.name === 'scan:progress')).values())
  expect(busiest, 'scan:progress events in the busiest second').toBeLessThanOrEqual(12)

  await harness.resetLibrary()
})

test('folders added together scan two at a time', async ({ harness }) => {
  // Folders of their own, generated here: the other specs' folders keep
  // their fresh state.
  const names = ['events-slots-a', 'events-slots-b', 'events-slots-c']
  const paths = names.map((n) => folderPath(n))
  for (const p of paths) {
    if (!existsSync(p)) {
      execFileSync(dirs.genfolder, ['-out', p, '-n', String(PHOTOS), '-cameras', '2', '-bursts', '3'])
    }
  }
  await harness.resetLibrary()
  await harness.clearEvents()

  // All three at once, in one page round trip, so they are queued together.
  const ids = await harness.page.evaluate(
    async (paths) => (await Promise.all(paths.map((p) => window.choix.TestSupport.AddFolderPath(p)))).map((r) => r.id),
    paths,
  )
  expect(await harness.waitIdle(), 'the job queue drains').toBe(true)
  await expect
    .poll(async () => {
      const ready = (await harness.events()).filter((e) => e.name === 'scan:state' && e.data?.state === 'ready')
      return ids.every((id) => ready.some((e) => e.data.folderId === id))
    })
    .toBe(true)

  // Walk the scan:state events in order: how many folders scanned at once.
  const states = (await harness.events()).filter((e) => e.name === 'scan:state' && ids.includes(e.data?.folderId))
  const scanning = new Set<string>()
  let most = 0
  for (const e of states) {
    if (e.data.state === 'scanning') {
      scanning.add(e.data.folderId)
    } else {
      scanning.delete(e.data.folderId)
    }
    most = Math.max(most, scanning.size)
  }
  const trace = states.map((e) => `${ids.indexOf(e.data.folderId)}:${e.data.state}`).join(', ')
  expect(most, `folders scanning at once (${trace})`).toBe(SLOTS)

  // Each folder's own sequence is whole: queued, scanning, ready.
  for (const id of ids) {
    const mine = states.filter((e) => e.data.folderId === id).map((e) => e.data.state as string)
    expect(mine, `scan:state of folder ${ids.indexOf(id)}`).toEqual(['queued', 'scanning', 'ready'])
  }
  for (const id of ids) {
    expect(await harness.folder(id)).toMatchObject({ state: 'ready', photos: PHOTOS })
  }

  await harness.resetLibrary()
})
