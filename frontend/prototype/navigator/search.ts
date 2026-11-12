// Navigator search: a number (`102`, `s102`) lists the states whose number
// starts with it; words must all appear, in any order and any case, in a
// state's label or its group's title.
import { GROUPS, type GroupId } from '../registry'

export interface StateEntry { n: number; group: GroupId; label: string }

export function search<T extends StateEntry>(query: string, states: T[]): T[] {
  const sorted = [...states].sort((a, b) => a.n - b.n)
  const q = query.trim().toLowerCase()
  if (q === '') return sorted
  const num = /^s?(\d+)$/.exec(q)
  if (num) return sorted.filter((s) => String(s.n).startsWith(num[1]!))
  const words = q.split(/\s+/)
  return sorted.filter((s) => {
    const title = GROUPS.find((g) => g.id === s.group)?.title ?? s.group
    const hay = `${s.label} ${title} ${s.group}`.toLowerCase()
    return words.every((w) => hay.includes(w))
  })
}
