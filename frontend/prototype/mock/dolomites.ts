// The one Dolomites folder every linked state shows: the Library's row
// (data.ts DOLOMITES, whose counts dolomites.test.ts holds equal to these
// clusters), the Folder page (folder.ts) and Focus (focus.ts: "Cluster N of
// M" is a cluster's place in this list). An invented shoot, early September
// 2026, on an X-T5 and an iPhone; mock photos 7–12 (data.ts), file names,
// times and ratings invented and deterministic.
import type { Cluster, Member } from '../../src/design/types'

export const XT5 = 'X-T5'
export const IPHONE = 'iPhone 15 Pro'

/** Which mock photo (1–36) each file id shows. */
export const PHOTO_OF = new Map<number, number>()

type Mark = 'p' | 'r' | '-'

/** A member with an explicit file id and path (the Focus clusters). */
export function file(file_id: number, ph: number, path: string, extra: Partial<Member> = {}): Member {
  PHOTO_OF.set(file_id, ph)
  const ext = path.slice(path.lastIndexOf('.') + 1).toUpperCase()
  return {
    file_id,
    path,
    content_hash: `c0ffee${file_id.toString(16).padStart(4, '0')}`,
    picked: false,
    rejected: false,
    kind: ext === 'MOV' ? 'video' : 'photo',
    format: ext,
    ...extra,
  }
}

let nextId = 1
let nextFuji = 4200
let nextPhone = 2231

/** A generated member: the next file name of the camera. */
export function shot(cam: string, ph: number, mark: Mark = '-', video = false): Member {
  const file_id = nextId++
  const fuji = cam === XT5
  const path = fuji ? `Day ${dayOf(ph)}/DSCF${nextFuji++}.${video ? 'MOV' : 'RAF'}` : `iPhone/IMG_${nextPhone++}.${video ? 'MOV' : 'HEIC'}`
  return {
    ...file(file_id, ph, path),
    picked: mark === 'p',
    rejected: mark === 'r',
    ...(video ? {} : { quality: Math.round(30 + ((file_id * 37) % 59)) / 10 }),
  }
}
// The folder a Fuji file sits in: a day of the shoot (cosmetic).
const dayOf = (ph: number) => 2 + (ph % 5)

let nextCluster = 1
/** A cluster with the next id; `marks` per member: p picked, r rejected. */
export function cluster(cam: string, time: string, photos: number[], marks = '', video = false): Cluster {
  return {
    id: nextCluster++,
    device: cam,
    time,
    members: photos.map((p, i) => shot(cam, p, (marks[i] as Mark | undefined) ?? '-', video)),
  }
}
const withId = (c: Omit<Cluster, 'id'>): Cluster => ({ ...c, id: nextCluster++ })

// ── The clusters Focus opens ────────────────────────────────────────────────

/** Ratings of the ridge frames (4106 and 4108 not rated yet). */
export const RIDGE_RATINGS: Record<number, number> = { 4101: 6.8, 4102: 7.4, 4103: 7.1, 4104: 3.9, 4105: 6.2, 4107: 5.6 }
const rated = (m: Member): Member => (RIDGE_RATINGS[m.file_id] !== undefined ? { ...m, quality: RIDGE_RATINGS[m.file_id] } : m)

/** Cluster 1: eight frames of one ridge at dawn, one picked and one rejected. */
export const RIDGE = withId({
  device: XT5,
  time: 'Sep 2 · 05:45',
  members: [
    file(4101, 7, 'Day 2/DSCF4101.RAF'),
    file(4102, 8, 'Day 2/DSCF4102.RAF', { picked: true }),
    file(4103, 9, 'Day 2/DSCF4103.RAF'),
    file(4104, 10, 'Day 2/DSCF4104.RAF', { rejected: true }),
    file(4105, 11, 'Day 2/Seceda ridge, second attempt after the clouds lifted/DSCF4105.RAF'),
    file(4106, 12, 'Day 2/DSCF4106.RAF'),
    file(4107, 7, 'Day 2/DSCF4107.RAF'),
    file(4108, 9, 'Day 2/DSCF4108.RAF'),
  ].map(rated),
})

/** Cluster 2: one frame. */
export const SINGLE = withId({ device: XT5, time: 'Sep 2 · 06:02', members: [file(4120, 10, 'Day 2/DSCF4120.RAF', { quality: 6.9 })] })

// ── The rest of the folder ──────────────────────────────────────────────────

const MIDDLE: Cluster[] = [
  cluster(IPHONE, 'Sep 2 · 06:10', [8, 9]),
  cluster(XT5, 'Sep 2 · 08:32', [9, 10, 11, 12, 10, 11, 9], '-p-rr--'),
  cluster(XT5, 'Sep 3 · 13:04', [10, 11, 12], 'p--'),
  cluster(IPHONE, 'Sep 3 · 13:20', [11, 12, 7, 8], '-r--'),
  cluster(XT5, 'Sep 3 · 19:48', [12, 7, 8, 9, 10, 11], '--p---'),
  cluster(XT5, 'Sep 4 · 06:02', [7, 9, 11]),
  cluster(IPHONE, 'Sep 4 · 20:23', [9, 12, 10, 8, 7], 'p---r'),
]

/** A 40-shot burst, long enough to scroll Focus' rail (state 318). */
export const LONG = cluster(
  XT5,
  'Sep 5 · 07:15',
  Array.from({ length: 40 }, (_, i) => 7 + ((i * 5) % 6)),
  '----p--------r------------p',
)

const LATE: Cluster[] = [
  cluster(XT5, 'Sep 6 · 03:42', [10, 7, 12, 11, 8, 9], 'rp----'),
  cluster(IPHONE, 'Sep 6 · 12:30', [11, 9, 7]),
  cluster(XT5, 'Sep 7 · 11:01', [12, 8, 10, 7, 9]),
]

/** The last cluster: a clip and two stills of the same scene (states 306, 405). */
export const WITH_VIDEO = withId({
  device: XT5,
  time: 'Sep 8 · 09:12',
  members: [
    file(4090, 8, 'Day 8/DSCF4090.MOV'),
    file(4091, 11, 'Day 8/DSCF4091.RAF', { quality: 6.4 }),
    file(4092, 12, 'Day 8/DSCF4092.RAF', { quality: 7.0 }),
  ],
})

/** The folder's clusters in capture order, as reviewed (Library row, s203). */
export const DOLOMITES_CLUSTERS: Cluster[] = [RIDGE, SINGLE, ...MIDDLE, LONG, ...LATE, WITH_VIDEO]

/** Every quality rating of the folder, by file id. */
export const RATINGS: Record<number, number> = Object.fromEntries(
  DOLOMITES_CLUSTERS.flatMap((c) => c.members.flatMap((m) => (m.quality !== undefined ? [[m.file_id, m.quality]] : []))),
)
