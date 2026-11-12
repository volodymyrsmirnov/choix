// The Library's Dolomites row, its Folder page and its Focus states tell one
// story: the same counts, the same clusters, the same folder name.
import { expect, test } from 'bun:test'
import { DOLOMITES } from './data'
import { DOLOMITES_CLUSTERS } from './dolomites'
import { REVIEWED, summaryOf } from './folder'
import { FOLDER_NAME, LONG, RIDGE_CLUSTER, RIDGE_REVIEWED, RIDGE_WITH_GPS, SINGLE, WITH_VIDEO } from './focus'

const counts = (f: { photos: number; videos: number; clusters: number; picked: number; rejected: number }) => ({
  photos: f.photos,
  videos: f.videos,
  clusters: f.clusters,
  picked: f.picked,
  rejected: f.rejected,
})

test('the Library row counts what the Folder page (s203) renders', () => {
  expect(REVIEWED).toBe(DOLOMITES_CLUSTERS)
  expect(counts(DOLOMITES)).toEqual(counts(summaryOf(REVIEWED)))
})

test('each Focus cluster is the Folder cluster at its "Cluster N of M"', () => {
  for (const r of [RIDGE_CLUSTER, RIDGE_WITH_GPS, SINGLE, LONG, WITH_VIDEO]) {
    expect(r.cluster_count).toBe(DOLOMITES.clusters)
    const folderCluster = DOLOMITES_CLUSTERS[r.cluster_index - 1]!
    expect(r.cluster).toEqual(folderCluster)
    expect(r.prev_cluster_id).toBe(DOLOMITES_CLUSTERS[r.cluster_index - 2]?.id ?? 0)
    expect(r.next_cluster_id).toBe(DOLOMITES_CLUSTERS[r.cluster_index]?.id ?? 0)
  }
  // The reviewed variant (s310) is the same cluster with every frame marked.
  expect(RIDGE_REVIEWED.cluster.id).toBe(RIDGE_CLUSTER.cluster.id)
  expect(RIDGE_REVIEWED.cluster.members.map((m) => m.file_id)).toEqual(RIDGE_CLUSTER.cluster.members.map((m) => m.file_id))
})

test('Focus goes back to the folder by the Library row’s name', () => {
  expect(FOLDER_NAME).toBe(DOLOMITES.name)
})

test('the folder spans the dates the Library row shows', () => {
  const days = DOLOMITES_CLUSTERS.map((c) => Number(/Sep (\d+)/.exec(c.time)![1]))
  expect(Math.min(...days)).toBe(Number(DOLOMITES.firstCaptured!.slice(8, 10)))
  expect(Math.max(...days)).toBe(Number(DOLOMITES.lastCaptured!.slice(8, 10)))
})
