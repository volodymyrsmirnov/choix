// Every group's fixtures in one table, by state. Evaluated by bun only (the
// prototype's mock data imports JSON and Vite assets, which Node refuses):
// bun tests import it directly, and on Node fixtures.ts reads it through
// dump.ts. A group's later parity task adds its file here.
//
// MEDIA: the /media/ manifest (content hash → prototype photo file name) of
// every group whose fixtures hand the app members (media.ts prints it for
// serve-app.mjs).
import type { ParityFixture } from '../mock-bindings/control'
import { COMPARE, COMPARE_MEDIA } from './compare'
import { FOCUS, FOCUS_MEDIA } from './focus'
import { FOLDER, FOLDER_MEDIA } from './folder'
import { LIBRARY } from './library'
import { OVERLAYS } from './overlays'
import { SETTINGS } from './settings'
import { WELCOME } from './welcome'

const GROUPS: Record<string, Record<number, ParityFixture>> = {
  welcome: WELCOME,
  library: LIBRARY,
  folder: FOLDER,
  focus: FOCUS,
  compare: COMPARE,
  settings: SETTINGS,
  overlays: OVERLAYS,
}

const MEDIA_GROUPS: Record<string, Record<string, string>> = {
  folder: FOLDER_MEDIA,
  focus: FOCUS_MEDIA,
  compare: COMPARE_MEDIA,
}

function merge(groups: Record<string, Record<number, ParityFixture>>): Record<number, ParityFixture> {
  const out: Record<number, ParityFixture> = {}
  const owner = new Map<number, string>()
  for (const [group, table] of Object.entries(groups)) {
    for (const [key, fixture] of Object.entries(table)) {
      const n = Number(key)
      const other = owner.get(n)
      if (other !== undefined) throw new Error(`parity fixtures: #${n} is in both ${other} and ${group}`)
      owner.set(n, group)
      out[n] = fixture
    }
  }
  return out
}

/** The groups' media manifests in one; a content hash two groups show as different photos throws. */
export function mergeMedia(groups: Record<string, Record<string, string>>): Record<string, string> {
  const out: Record<string, string> = {}
  const owner = new Map<string, string>()
  for (const [group, table] of Object.entries(groups)) {
    for (const [hash, name] of Object.entries(table)) {
      if (out[hash] !== undefined && out[hash] !== name) {
        throw new Error(`parity fixtures: content hash ${hash} is ${out[hash]} in ${owner.get(hash)} and ${name} in ${group}`)
      }
      owner.set(hash, group)
      out[hash] = name
    }
  }
  return out
}

export const TABLE: Record<number, ParityFixture> = merge(GROUPS)

export const MEDIA: Record<string, string> = mergeMedia(MEDIA_GROUPS)
