// Fixtures of the Focus states 300–318 (prototype/scenarios/focus.tsx), and
// the shared builder the Compare fixtures use (compare.ts).
//
// Each fixture is read off the approved scenario itself: the scenario's
// render gives a FocusScreen element, and its props are what the prototype
// shows. The fixture hands the app the same data the way Go would:
//   - the route `#/folder/<folder>/focus/<cluster>` (the folder session
//     opens the folder, then Focus asks for the cluster);
//   - Catalog.Cluster: the scenario's cluster (`focus`); 'pending' while the
//     state is still loading (300); where the state shows a load that failed
//     (316), the fixture's `calls` makes Cluster reject with the state's
//     message, which the app shows as Go's;
//   - Catalog.Open / Clusters, Library.List, Settings.Get:
//     the folder as the approved Folder page shows it (fixtures/folder.ts,
//     state 203: the Dolomites folder with its picks and rejects), or, for
//     the one-cluster folder of 317, the same answers built for that folder;
//   - a pick in flight (315, 409): the member starts in the mark the user's
//     key takes it out of (Go's state before the pick), and `calls` keeps
//     Catalog.SetPick in flight, so the app's optimistic mark is the
//     scenario's mark and the member is busy;
//   - the ratings are the members' own (Catalog.Ratings is not asked at
//     start; ../focusCases.test.tsx holds the scenario's `ratings` prop
//     equal to them).
// What a fixture cannot say — the photo on screen, compare and its panes,
// zoom and pan, the Info panel, pressing Pick — is done by the state's case
// (cases.ts, FOCUS_STEPS), the way a user does it.
//
// FOCUS_MEDIA maps each member's content hash to the prototype photo the
// scenario shows for it, so serve-app.mjs answers the app's /media/ URLs
// with the same bytes (media.ts). A media URL Vite inlines as a data: URL
// (312's broken full-size image) has no app counterpart: FOCUS_UNSERVED
// lists it.
import { DOLOMITES } from '../../../prototype/mock/data'
import { CLUSTER_IDS, ONLY, ONLY_FOLDER } from '../../../prototype/mock/focus'
import type { Scenario, ScenarioCtx } from '../../../prototype/registry'
import { scenarios } from '../../../prototype/scenarios/focus'
import type { FocusProps } from '../../../src/design/screens/focus/props'
import type { Cluster, FocusResponse, FolderSummary, Member, SettingsBody } from '../../../src/design/types'
import type { FolderView } from '../../../bindings/github.com/volodymyrsmirnov/choix/internal/app/catalog/models'
import { PHOTOS_PATH, servedPhoto } from '../fixtures'
import type { AppErrorCause, ParityFixture } from '../mock-bindings/control'
import { FOLDER } from './folder'

/** A scenario context with an empty store: every state shows its defaults. */
const CTX: ScenarioCtx = {
  size: 'default',
  now: new Date(0),
  store: { state: {}, set: () => {}, go: () => {}, toast: () => {} },
}

/** The FocusScreen props scenario `n` of `list` renders. */
export function focusScenarioProps(n: number, list: Scenario[] = scenarios): FocusProps {
  const s = list.find((x) => x.n === n)
  if (!s) throw new Error(`parity fixtures: no focus or compare scenario #${n}`)
  return s.render(CTX).props as FocusProps
}

/** `value` as JSON data (keys whose value is undefined dropped), as Go's answer would be. */
const plain = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T

/** The id of the one-cluster folder of 317 (invented; the prototype names only the folder). */
export const ONLY_FOLDER_ID = 'seceda'

/** The Folder page's answers for the Dolomites folder (state 203, With picks and rejects). */
const DOLOMITES_FOLDER = FOLDER[203]!

/** Catalog.Open's grid of a folder of `clusters`. */
function viewFor(name: string, clusters: Cluster[]): FolderView {
  const all = clusters.flatMap((c) => c.members)
  return {
    folder: name,
    picked: all.filter((m) => m.picked).length,
    total: all.length,
    clusters,
    devices: [...new Set(clusters.map((c) => c.device))],
    failed: 0,
    picks_dir: 'picks',
    picks_missing: 0,
  }
}

/** The one-cluster folder's Library row. */
function onlyRow(): FolderSummary {
  const ms = ONLY.cluster.members
  return {
    ...DOLOMITES,
    id: ONLY_FOLDER_ID,
    name: ONLY_FOLDER,
    photos: ms.filter((m) => m.kind === 'photo').length,
    videos: ms.filter((m) => m.kind === 'video').length,
    clusters: 1,
    picked: ms.filter((m) => m.picked).length,
    rejected: ms.filter((m) => m.rejected).length,
    previews: [],
  }
}

/** The folder a Focus state shows (its Back button's name) and Go's answers for it. */
function folderOf(p: FocusProps): { id: string } & Pick<ParityFixture, 'folders' | 'settings' | 'view'> {
  if (p.folder === DOLOMITES.name) {
    const { folders, settings, view } = DOLOMITES_FOLDER
    return { id: DOLOMITES.id, folders, settings, view }
  }
  if (p.folder === ONLY_FOLDER) {
    return {
      id: ONLY_FOLDER_ID,
      folders: [onlyRow()],
      settings: DOLOMITES_FOLDER.settings,
      view: viewFor(ONLY_FOLDER, [ONLY.cluster]),
    }
  }
  throw new Error(`parity fixtures: no folder for Focus's "${p.folder}"`)
}

type Mark = Pick<Member, 'picked' | 'rejected'>

/**
 * Go's mark of a member before the key that leaves it in `shown` (a pick in
 * flight shows the optimistic mark): Space un-picks a picked photo into an
 * unmarked one, picks an unmarked one; X rejects an unmarked one.
 */
export function markBefore(shown: Mark): { mark: Mark; key: 'pick' | 'reject' } {
  if (shown.rejected) return { mark: { picked: false, rejected: false }, key: 'reject' }
  if (shown.picked) return { mark: { picked: false, rejected: false }, key: 'pick' }
  return { mark: { picked: true, rejected: false }, key: 'pick' }
}

/** `members` with each of `ids` in its mark before the pick in flight. */
function before(members: Member[], ids: number[]): Member[] {
  return members.map((m) => (ids.includes(m.file_id) ? { ...m, ...markBefore(m).mark } : m))
}

/** The cluster as Catalog.Cluster answers it before the pick in flight. */
function clusterBefore(data: FocusResponse, busy: number[]): FocusResponse {
  if (busy.length === 0) return data
  return { ...data, cluster: { ...data.cluster, members: before(data.cluster.members, busy) } }
}

/** The grid with the same members in their marks before the pick in flight. */
function viewBefore(view: ParityFixture['view'], busy: number[]): ParityFixture['view'] {
  if (busy.length === 0 || typeof view !== 'object') return view
  const clusters = view.clusters.map((c) => ({ ...c, members: before(c.members as Member[], busy) }))
  const all = clusters.flatMap((c) => c.members)
  return { ...view, clusters, picked: all.filter((m) => m.picked).length }
}

/** The Focus route of `p`'s cluster with `p`'s data, as Go answers it. */
export function focusFixture(p: FocusProps): ParityFixture {
  const folder = folderOf(p)
  const busy = p.busyFileIds
  const settings = { ...(folder.settings as SettingsBody), quality_rating: p.qualityOn }
  const calls: Record<string, 'pending' | AppErrorCause> = {}
  if (busy.length > 0) calls['Catalog.SetPick'] = 'pending'
  if (p.data === null && p.loadError !== null) calls['Catalog.Cluster'] = { code: 'internal', message: p.loadError }
  return plain({
    route: `#/folder/${encodeURIComponent(folder.id)}/focus/${p.data?.cluster.id ?? CLUSTER_IDS.ridge}`,
    folders: folder.folders,
    settings,
    view: viewBefore(folder.view, busy),
    ...(p.data !== null ? { focus: clusterBefore(p.data, busy) } : p.loadError === null ? { focus: 'pending' as const } : {}),
    ...(p.toasts.length > 0 ? { toasts: p.toasts } : {}),
    ...(Object.keys(calls).length > 0 ? { calls } : {}),
  })
}

/** The Focus states' numbers (300–318; there is no 309). */
export const FOCUS_STATES: number[] = scenarios.map((s) => s.n)

export const FOCUS: Record<number, ParityFixture> = Object.fromEntries(FOCUS_STATES.map((n) => [n, focusFixture(focusScenarioProps(n))]))

/** A member media URL the app's /media/ URLs cannot stand for (a data: URL), by state. */
export interface Unserved {
  state: number
  fileId: number
  tier: 'thumb' | 'preview' | 'full'
}

/**
 * Content hash → the prototype photo (file name under prototype/mock/photos/)
 * the members of `states` show at every tier (the scenario's own `media`),
 * plus the URLs that are no served photo (`unserved`). Throws when one hash
 * would need two photos.
 */
export function mediaOf(states: number[], list: Scenario[]): { media: Record<string, string>; unserved: Unserved[] } {
  const media: Record<string, string> = {}
  const unserved: Unserved[] = []
  for (const n of states) {
    const p = focusScenarioProps(n, list)
    for (const m of p.data?.cluster.members ?? []) {
      const tiers = { thumb: p.media.thumb(m, 'thumb'), preview: p.media.thumb(m, 'preview'), full: p.media.full(m) }
      for (const [tier, href] of Object.entries(tiers) as [Unserved['tier'], string][]) {
        const served = servedPhoto(href)
        if (!served.startsWith(PHOTOS_PATH)) {
          unserved.push({ state: n, fileId: m.file_id, tier })
          continue
        }
        const name = served.slice(PHOTOS_PATH.length)
        const had = media[m.content_hash]
        if (had !== undefined && had !== name) throw new Error(`parity fixtures: content hash ${m.content_hash} shows ${had} and ${name}`)
        media[m.content_hash] = name
      }
    }
  }
  return { media, unserved }
}

const focusMedia = mediaOf(FOCUS_STATES, scenarios)
export const FOCUS_MEDIA: Record<string, string> = focusMedia.media
/** 312's broken full-size image (BROKEN_FILE), a data: URL in the prototype build. */
export const FOCUS_UNSERVED: Unserved[] = focusMedia.unserved
