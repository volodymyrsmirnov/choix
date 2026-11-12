// The scenario registry: pure, so `bun test` can load it. Every prototype
// state is a Scenario with a permanent number inside its group's range.
// scenarios/index.ts (Vite only) collects the group files and calls
// defineScenarios once.
import type React from 'react'
import type { ProtoStore } from './store'

export type GroupId = 'gallery' | 'welcome' | 'library' | 'folder' | 'focus' | 'compare' | 'settings' | 'overlays' | 'native'
export interface Group { id: GroupId; title: string; range: [number, number] }
export interface ScenarioCtx { size: 'min' | 'default' | 'large'; store: ProtoStore; now: Date }
export interface Scenario {
  n: number
  group: GroupId
  label: string
  design: string
  overlays?: string[]
  /** A CSS selector (inside the app root) for what the label names; the
   *  checks require it fully in view without scrolling, at every size. */
  subject?: string
  render(ctx: ScenarioCtx): React.ReactElement
}

/** Groups in navigator order (which is also number order). */
export const GROUPS: Group[] = [
  { id: 'welcome', title: 'Welcome and shell', range: [10, 49] },
  { id: 'library', title: 'Library', range: [100, 199] },
  { id: 'folder', title: 'Folder', range: [200, 299] },
  { id: 'focus', title: 'Focus', range: [300, 399] },
  { id: 'compare', title: 'Compare', range: [400, 449] },
  { id: 'settings', title: 'Settings', range: [500, 599] },
  { id: 'overlays', title: 'Overlays', range: [600, 699] },
  { id: 'native', title: 'Native replicas', range: [700, 799] },
  { id: 'gallery', title: 'Gallery', range: [800, 899] },
]

export class RegistryError extends Error {
  code: 'duplicate' | 'out-of-range' | 'retired'
  constructor(code: RegistryError['code'], message: string) {
    super(message)
    this.name = 'RegistryError'
    this.code = code
  }
}

/**
 * Checks the scenarios and returns them ordered by number. Throws a
 * RegistryError for a number used twice, a number outside its group's range,
 * or a retired number (docs/design/retired.json): numbers are permanent once
 * a snapshot is committed, so a retired one is never reused.
 */
export function defineScenarios(all: Scenario[], retired: number[]): Scenario[] {
  const seen = new Set<number>()
  const gone = new Set(retired)
  for (const s of all) {
    if (seen.has(s.n)) throw new RegistryError('duplicate', `s${s.n} is defined twice`)
    seen.add(s.n)
    const group = GROUPS.find((g) => g.id === s.group)
    if (!group || s.n < group.range[0] || s.n > group.range[1]) {
      const range = group ? `${group.range[0]}–${group.range[1]}` : 'none'
      throw new RegistryError('out-of-range', `s${s.n} is outside the ${s.group} range (${range})`)
    }
    if (gone.has(s.n)) throw new RegistryError('retired', `s${s.n} is retired and cannot be reused`)
  }
  return [...all].sort((a, b) => a.n - b.n)
}
