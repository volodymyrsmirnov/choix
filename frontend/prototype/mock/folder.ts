// Mock data for the Folder states (200–229): the Dolomites folder of
// dolomites.ts, plus the extra clusters a few states insert.
import type { Cluster, FolderSummary, MediaURLs, ToastItem } from '../../src/design/types'
import { DOLOMITES, photo } from './data'
import { cluster, DOLOMITES_CLUSTERS, IPHONE, PHOTO_OF, XT5 } from './dolomites'

export { IPHONE, XT5 }

const strip = (cs: Cluster[]): Cluster[] => cs.map((c) => ({ ...c, members: c.members.map((m) => ({ ...m, picked: false, rejected: false })) }))

/** The folder before any review (state 202). */
export const CLUSTERS: Cluster[] = strip(DOLOMITES_CLUSTERS)
/** The folder as reviewed: the Library's row and Focus tell the same story (state 203 and most others). */
export const REVIEWED: Cluster[] = DOLOMITES_CLUSTERS

// Extra clusters for the states that show one kind of cluster near the top.
// Those states show their own counts (the title bar and the filter bar count
// what they render).

/** A fully reviewed cluster (state 217), shown second. */
export const DONE_CLUSTER: Cluster = cluster(XT5, 'Sep 2 · 05:50', [12, 10, 8, 11], 'prpr')

/** A cross-device cluster (state 211): both cameras, merged by clock skew. */
export const MERGED_CLUSTER: Cluster = (() => {
  const a = cluster(XT5, 'Sep 2 · 05:54', [11, 12, 10])
  const b = cluster(IPHONE, 'Sep 2 · 05:55', [12, 9])
  return { ...a, device: `${XT5} + ${IPHONE}`, members: [a.members[0]!, b.members[0]!, a.members[1]!, b.members[1]!, a.members[2]!] }
})()

/** A 28-shot burst, longer than any strip is wide (state 220). */
export const BURST_CLUSTER: Cluster = cluster(
  XT5,
  'Sep 2 · 05:58',
  Array.from({ length: 28 }, (_, i) => 7 + ((i * 5) % 6)),
  '--p-------r',
)

/** Inserts `extra` after the first cluster. */
export const withSecond = (cs: Cluster[], extra: Cluster): Cluster[] => [cs[0]!, extra, ...cs.slice(1)]

/** What the backend sends with `hide_rejected_photos` on (state 210). */
export const hideRejected = (cs: Cluster[]): Cluster[] =>
  cs.map((c) => ({ ...c, members: c.members.filter((m) => !m.rejected) })).filter((c) => c.members.length > 0)

/** Without quality ratings (state 212: the setting is off). */
export const unrated = (cs: Cluster[]): Cluster[] =>
  cs.map((c) => ({ ...c, members: c.members.map(({ quality: _q, ...m }) => m) }))

/** Sorted distinct single-camera labels, as FolderView.devices. */
export const devicesOf = (cs: Cluster[]): string[] =>
  [...new Set(cs.flatMap((c) => c.device.split(' + ')))].sort((a, b) => a.localeCompare(b))

/** The folder's summary, its counts taken from `cs`. */
export function summaryOf(cs: Cluster[], base: FolderSummary = DOLOMITES): FolderSummary {
  const all = cs.flatMap((c) => c.members)
  return {
    ...base,
    photos: all.filter((m) => m.kind === 'photo').length,
    videos: all.filter((m) => m.kind === 'video').length,
    clusters: cs.length,
    picked: all.filter((m) => m.picked).length,
    rejected: all.filter((m) => m.rejected).length,
  }
}

/** The Dolomites folder on an external drive (states 224 and 225). */
export const ON_DRIVE: FolderSummary = { ...DOLOMITES, parentPath: '/Volumes/Atlas/Shoots', volumeName: 'Atlas' }
export const ON_CARD: FolderSummary = { ...DOLOMITES, parentPath: '/Volumes/Card A/DCIM', volumeName: 'Card A' }

/** Thumbnails come from the mock photos. */
export const MEDIA: MediaURLs = {
  thumb: (m) => photo(PHOTO_OF.get(m.file_id) ?? 1),
  full: (m) => photo(PHOTO_OF.get(m.file_id) ?? 1),
}

export const TOAST_RECLUSTERED: ToastItem = { id: 'reclustered', tone: 'pick', title: 'Re-clustered' }

export const TOAST_RECLUSTER_FAILED: ToastItem = {
  id: 'recluster-failed',
  tone: 'reject',
  title: 'Re-cluster failed',
  body: 'Choix could not write the new clusters: the folder’s drive stopped responding.',
}
