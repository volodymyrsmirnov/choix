// Fixtures of the Folder states 200–229 (prototype/scenarios/folder.tsx).
//
// Each fixture is read off the approved scenario itself: the scenario's
// render gives a FolderScreen element, and its props are what the prototype
// shows. The fixture hands the app the same data the way Go would:
//   - Catalog.Open / Clusters: the grid as a FolderView (the clusters, the
//     cameras, the failed and picks-missing counts, the picks folder);
//     'pending' while the state is still loading (200);
//   - Library.List: the folder's row (the title bar's name and counts), as
//     `scanning` with the run's progress when the state shows a scan;
//   - Settings.Get and Models.Status: what makes the page group by time only
//     (GROUPING_SOURCE), hide rejected photos or leave out the badges;
//   - Models.Quality when the title bar shows the quality worker (229);
//   - the toasts of 222 and 223, seeded (the app's own re-cluster toasts
//     leave after 4 s, which a comparison could outlast;
//     ../folderCases.test.ts holds them to the same copy);
//   - `calls`: how Catalog.Recluster answers where the state comes from a
//     re-cluster in flight (215) or one that failed on a read-only drive (225).
// What a fixture cannot say — the filter text, the camera and sort menus,
// the keyboard cursor, a run that stopped, the drive going away, pressing
// Re-cluster — is done by the state's case (cases.ts, FOLDER_STEPS), the way
// a user or Go does it.
//
// FOLDER_MEDIA maps each member's content hash to the prototype photo its
// thumbnail shows, so serve-app.mjs answers the app's /media/... URLs with
// the same bytes (media.ts).
import { ERR_CLIP } from '../../../prototype/mock/settings'
import { model } from '../../../prototype/mock/data'
import { scenarios } from '../../../prototype/scenarios/folder'
import type { Scenario, ScenarioCtx } from '../../../prototype/registry'
import { DEFAULTS } from '../../../src/design/screens/settings/draft'
import type { FolderGrouping, FolderProps } from '../../../src/design/screens/folder/props'
import type { FolderSummary, ModelStatus, SettingsBody } from '../../../src/design/types'
import type { FolderView } from '../../../bindings/github.com/volodymyrsmirnov/choix/internal/app/catalog/models'
import { mediaPhoto } from '../fixtures'
import type { AppErrorCause, ParityFixture } from '../mock-bindings/control'

/** A scenario context with an empty store: every state shows its defaults. */
const CTX: ScenarioCtx = {
  size: 'default',
  now: new Date(0),
  store: { state: {}, set: () => {}, go: () => {}, toast: () => {} },
}

/** The FolderScreen props scenario `n` renders. */
export function folderScenarioProps(n: number, list: Scenario[] = scenarios): FolderProps {
  const s = list.find((x) => x.n === n)
  if (!s) throw new Error(`parity fixtures: no folder scenario #${n}`)
  return s.render(CTX).props as FolderProps
}

/**
 * What Go reports for each way the page groups: the visual-grouping setting
 * (Settings.Get) and the CLIP model (Models.Status). The app derives the
 * grouping from these two (folderProps.ts groupingOf).
 */
export const GROUPING_SOURCE: Record<FolderGrouping, { visual_grouping: boolean; clip: ModelStatus }> = {
  visual: { visual_grouping: true, clip: model('clip', 'ready') },
  'time-downloading': { visual_grouping: true, clip: model('clip', 'downloading', 96_000_000) },
  'time-failed': { visual_grouping: true, clip: model('clip', 'failed', 0, ERR_CLIP) },
  'time-off': { visual_grouping: false, clip: model('clip', 'disabled') },
}

/** How Catalog.Recluster answers in the states that come from pressing Re-cluster. */
export const RECLUSTER_CALLS: Record<number, 'pending' | AppErrorCause> = {
  215: 'pending',
  225: { code: 'read_only', message: 'the folder’s drive is read-only' },
}

/** `value` as JSON data (keys whose value is undefined dropped), as Go's answer would be. */
const plain = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T

/** The grid as Catalog.Open and Catalog.Clusters answer it. */
export function viewOf(p: FolderProps): FolderView | 'pending' {
  if (p.clusters === null) return 'pending'
  const all = p.clusters.flatMap((c) => c.members)
  return {
    folder: p.folder.name,
    picked: all.filter((m) => m.picked).length,
    total: all.length,
    clusters: p.clusters,
    devices: p.devices,
    failed: p.failedFiles,
    picks_dir: p.picksDir,
    picks_missing: p.picksMissing,
  }
}

/** The folder's Library row: scanning, at the run's progress (still running), when the state shows a scan. */
export function rowOf(p: FolderProps): FolderSummary {
  if (!p.scan) return p.folder
  return { ...p.folder, state: 'scanning', scan: { ...p.scan, state: 'running' } }
}

/** The Folder route of `p`'s folder with `p`'s data, as Go answers it. */
export function folderFixture(p: FolderProps, n?: number): ParityFixture {
  const source = GROUPING_SOURCE[p.grouping]
  const settings: SettingsBody = {
    ...DEFAULTS,
    picks_dir: p.picksDir,
    hide_rejected_photos: p.settings.hide_rejected_photos,
    quality_rating: p.settings.quality_rating,
    visual_grouping: source.visual_grouping,
  }
  const recluster = n === undefined ? undefined : RECLUSTER_CALLS[n]
  return plain({
    route: `#/folder/${encodeURIComponent(p.folder.id)}`,
    folders: [rowOf(p)],
    models: [source.clip, model('quality', 'ready')],
    settings,
    view: viewOf(p),
    ...(p.quality ? { quality: p.quality } : {}),
    ...(p.toasts.length > 0 ? { toasts: p.toasts } : {}),
    ...(recluster !== undefined ? { calls: { 'Catalog.Recluster': recluster } } : {}),
  })
}

/** The Folder states' numbers (200–229). */
export const FOLDER_STATES: number[] = scenarios.map((s) => s.n)

export const FOLDER: Record<number, ParityFixture> = Object.fromEntries(FOLDER_STATES.map((n) => [n, folderFixture(folderScenarioProps(n), n)]))

/**
 * Content hash → the prototype photo (file name under prototype/mock/photos/)
 * that every member of every Folder state shows, thumbnail and full size
 * (the scenario's own `media`). Throws when one hash would need two photos.
 */
export const FOLDER_MEDIA: Record<string, string> = (() => {
  const out: Record<string, string> = {}
  for (const n of FOLDER_STATES) {
    const p = folderScenarioProps(n)
    for (const m of (p.clusters ?? []).flatMap((c) => c.members)) {
      for (const href of [p.media.thumb(m, 'thumb'), p.media.thumb(m, 'preview'), p.media.full(m)]) {
        const name = mediaPhoto(href)
        const had = out[m.content_hash]
        if (had !== undefined && had !== name) throw new Error(`parity fixtures: content hash ${m.content_hash} shows ${had} and ${name}`)
        out[m.content_hash] = name
      }
    }
  }
  return out
})()
