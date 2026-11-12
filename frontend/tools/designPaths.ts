// Paths and settings the browser-driven design tools share. Node-safe: no
// Vite-only API, no JSON import (gate.json is read from disk).
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

export type Size = 'min' | 'default' | 'large'

export const FRONTEND = join(dirname(fileURLToPath(import.meta.url)), '..')
export const DESIGN_DIR = join(FRONTEND, '.design')
export const PROTO_DIST = join(FRONTEND, 'dist-prototype')
export const FIXTURES_DIST = join(DESIGN_DIR, 'fixtures-dist')
export const SCREENSHOTS = join(DESIGN_DIR, 'screenshots')
/** One folder per run (`<run>/<kind>/<name>.json`), so concurrent runs never wipe each other's results. */
export const RESULTS = join(DESIGN_DIR, 'results')

const gate = JSON.parse(readFileSync(join(FRONTEND, '..', 'docs', 'design', 'gate.json'), 'utf8')) as {
  sizes: Record<Size, [number, number]>
  themes: string[]
  os: string[]
}

/** The themes and OS looks under review (gate.json). */
export const THEMES: string[] = gate.themes
export const OSES: string[] = gate.os

/** The review sizes in order, with their window size in px (docs/design/gate.json). */
export const SIZES: Size[] = ['min', 'default', 'large']
export const SIZE_PX: Record<Size, [number, number]> = gate.sizes

/** Every scripted render uses this instant (shared-context: fixed clock). */
export const FIXED_NOW = new Date('2026-11-11T10:00:00Z')

/** Set by the global setup: where the prototype and the fixture page are served. */
export function protoUrl(): string {
  const url = process.env.CHOIX_PROTO_URL
  if (!url) throw new Error('CHOIX_PROTO_URL is not set: run through playwright.design.config.ts (its global setup serves the prototype)')
  return url
}

/** This run's result folder, set by the global setup. */
export function resultsDir(): string {
  const dir = process.env.CHOIX_RESULTS_DIR
  if (!dir) throw new Error('CHOIX_RESULTS_DIR is not set: run through playwright.design.config.ts')
  return dir
}

export function fixturesUrl(): string {
  const url = process.env.CHOIX_FIXTURES_URL
  if (!url) throw new Error('CHOIX_FIXTURES_URL is not set: run through playwright.design.config.ts')
  return url
}
