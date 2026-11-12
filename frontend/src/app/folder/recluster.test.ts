// Re-cluster from the Folder page: the toast it puts up when it went
// through, failed, or was not run at all.
import { expect, test } from 'bun:test'
import { createToasts } from '../shell/toasts'
import { RECLUSTER_FAILED, RECLUSTERED, reclusterWithToast } from './recluster'

const failure = (cause: object) => Object.assign(new Error('failed', { cause }), { name: 'RuntimeError' })
const copy = (t: { tone: string; title: string; body?: string }) => ({ tone: t.tone, title: t.title, body: t.body })

test('a re-cluster that went through shows "Re-clustered"', async () => {
  const toasts = createToasts()
  await reclusterWithToast({ recluster: async () => true }, toasts)
  expect(toasts.items().map(copy)).toEqual([{ tone: 'pick', title: RECLUSTERED, body: undefined }])
})

test('one already running shows nothing', async () => {
  const toasts = createToasts()
  await reclusterWithToast({ recluster: async () => false }, toasts)
  expect(toasts.items()).toEqual([])
})

test('a failed re-cluster shows "Re-cluster failed" with Go’s message', async () => {
  const toasts = createToasts()
  await reclusterWithToast(
    {
      recluster: async () => {
        throw failure({ code: 'internal', message: 'the clusters could not be written' })
      },
    },
    toasts,
  )
  expect(toasts.items().map(copy)).toEqual([{ tone: 'reject', title: RECLUSTER_FAILED, body: 'the clusters could not be written' }])
})

test('the app quitting shows nothing', async () => {
  const toasts = createToasts()
  await reclusterWithToast(
    {
      recluster: async () => {
        throw failure({ code: 'unavailable', message: 'quitting' })
      },
    },
    toasts,
  )
  expect(toasts.items()).toEqual([])
})

test('a busy re-cluster shows "Re-cluster failed" with Go’s message', async () => {
  const toasts = createToasts()
  await reclusterWithToast(
    {
      recluster: async () => {
        throw failure({ code: 'busy', message: 'The previous folder is still closing' })
      },
    },
    toasts,
  )
  expect(toasts.items().map(copy)).toEqual([{ tone: 'reject', title: RECLUSTER_FAILED, body: 'The previous folder is still closing' }])
})
