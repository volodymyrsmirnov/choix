// Mock data for the Focus (300–318) and Compare (400–409) states: clusters of
// the Dolomites folder (dolomites.ts), so "Cluster N of M", the counts and
// the Back label agree with the Library row and the Folder page; their EXIF;
// and the MediaURLs that map members to the synthetic photos. Invented, like
// everything in mock/ (privacy.test.ts).
import type { Cluster, ExifPayload, FocusResponse, MediaURLs } from '../../src/design/types'
import { BROKEN_PHOTO, DOLOMITES, photo } from './data'
import { DOLOMITES_CLUSTERS, file, LONG as LONG_CLUSTER, PHOTO_OF, RATINGS, RIDGE, SINGLE as SINGLE_CLUSTER, WITH_VIDEO as VIDEO_CLUSTER } from './dolomites'

export { RIDGE_RATINGS } from './dolomites'

export const FOLDER_NAME = DOLOMITES.name

/** The camera EXIF names (the cluster's device is the short label). */
const CAMERA: Record<string, string> = { 'X-T5': 'Fujifilm X-T5', 'iPhone 15 Pro': 'Apple iPhone 15 Pro' }

/** "Sep 2 · 05:45" + seconds → "Sep 2 2026 · 05:45:12". */
const capturedOf = (time: string, second: number) => time.replace(' · ', ' 2026 · ') + `:${String(second).padStart(2, '0')}`

function exifOf(c: Cluster, extra: (i: number) => Partial<ExifPayload> = () => ({})): Record<string, ExifPayload> {
  return Object.fromEntries(
    c.members.map((m, i) => [
      String(m.file_id),
      {
        camera: CAMERA[c.device] ?? c.device,
        aperture: 'f/8.0',
        shutter: '1/250',
        iso: '160',
        focal_length: '23mm',
        captured: capturedOf(c.time, (12 + i * 2) % 60),
        size: '7728 × 5152',
        ...(m.kind === 'video' ? { size: '3840 × 2160', aperture: undefined, shutter: undefined, iso: undefined } : {}),
        ...extra(i),
      },
    ]),
  )
}

/** Cluster ids: each one opens a state of its own (see focus.tsx). */
export const CLUSTER_IDS = { ridge: RIDGE.id, single: SINGLE_CLUSTER.id, long: LONG_CLUSTER.id, video: VIDEO_CLUSTER.id, only: 9001 } as const

/** What Focus loads for a cluster of the Dolomites folder. */
function response(c: Cluster, ex: Record<string, ExifPayload> = exifOf(c)): FocusResponse {
  const i = DOLOMITES_CLUSTERS.findIndex((x) => x.id === c.id)
  if (i < 0) throw new Error(`cluster ${c.id} is not in the Dolomites folder`)
  return {
    cluster: c,
    cluster_index: i + 1,
    cluster_count: DOLOMITES_CLUSTERS.length,
    exif: ex,
    prev_cluster_id: DOLOMITES_CLUSTERS[i - 1]?.id ?? 0,
    next_cluster_id: DOLOMITES_CLUSTERS[i + 1]?.id ?? 0,
  }
}

export const RIDGE_CLUSTER = response(RIDGE)

/** Every frame marked: the cluster-done pill (state 310). */
export const RIDGE_REVIEWED = response({
  ...RIDGE,
  members: RIDGE.members.map((m, i) => ({ ...m, picked: i === 1 || i === 2 || i === 6, rejected: !(i === 1 || i === 2 || i === 6) })),
})

/** Coordinates for the GPS state (a ridge in the mountains, invented). */
export const RIDGE_GPS = { gps_lat: 46.60412, gps_lon: 11.72589 }

/** The ridge with GPS on every frame (state 305). */
export const RIDGE_WITH_GPS: FocusResponse = response(RIDGE, exifOf(RIDGE, () => RIDGE_GPS))

/** A cluster of one frame (state 311). */
export const SINGLE = response(SINGLE_CLUSTER, exifOf(SINGLE_CLUSTER, () => ({ shutter: '1/500' })))

/** A 40-shot burst, its rail scrolled to the active frame (state 318). */
export const LONG = response(LONG_CLUSTER)

/** A clip and two stills of the same scene (states 306 and 405). */
export const WITH_VIDEO = response(VIDEO_CLUSTER)

/** Every rating of the folder, by file id (the `ratings` prop). */
export { RATINGS }

/** The one-cluster folder of state 317 (not in the Library mocks). */
export const ONLY_FOLDER = 'Seceda'
const ONLY_CLUSTER: Cluster = {
  id: CLUSTER_IDS.only,
  device: 'X-T5',
  time: 'Aug 30 · 18:40',
  members: [
    file(9101, 7, 'DSCF9101.RAF', { quality: 6.1 }),
    file(9102, 9, 'DSCF9102.RAF', { quality: 7.2 }),
    file(9103, 11, 'DSCF9103.RAF', { picked: true, quality: 7.8 }),
    file(9104, 12, 'DSCF9104.RAF', { rejected: true, quality: 4.4 }),
  ],
}
/** The only cluster of a one-cluster folder (state 317). */
export const ONLY: FocusResponse = {
  cluster: ONLY_CLUSTER,
  cluster_index: 1,
  cluster_count: 1,
  exif: exifOf(ONLY_CLUSTER),
  prev_cluster_id: 0,
  next_cluster_id: 0,
}
export const ONLY_RATINGS: Record<number, number> = Object.fromEntries(ONLY_CLUSTER.members.map((m) => [m.file_id, m.quality!]))

/** The file whose full-size image does not decode (state 312). */
export const BROKEN_FILE = 4103

/** Media URLs: every tier of a member is its mock photo. */
export const MEDIA: MediaURLs = {
  thumb: (m) => photo(PHOTO_OF.get(m.file_id) ?? 1),
  full: (m) => photo(PHOTO_OF.get(m.file_id) ?? 1),
}

/** MEDIA, with BROKEN_FILE's full-size image a file that loads but does not decode. */
export const MEDIA_BROKEN: MediaURLs = {
  thumb: MEDIA.thumb,
  full: (m) => (m.file_id === BROKEN_FILE ? BROKEN_PHOTO : MEDIA.full(m)),
}

export const LOAD_ERROR = 'The folder’s database could not be read: disk I/O error'
