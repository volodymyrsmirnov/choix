// The Folder page's filter: pure, moved out of v1's Library component so the
// page and its tests share one implementation.
import type { Cluster, FolderFilter } from '../../types'

/** How a cross-device cluster names its cameras: `'X-T5 + iPhone 15'`. */
const MERGED_SEPARATOR = ' + '

function cameraMatches(c: Cluster, device: string): boolean {
  if (c.device === device) return true
  // The camera menu lists single cameras; a merged cluster belongs to each.
  return c.device.includes(MERGED_SEPARATOR) && c.device.split(MERGED_SEPARATOR).includes(device)
}

/**
 * The clusters the page shows for `filter`, with v1's matching rules: the
 * camera (exact, or one of a merged cluster's cameras), then a query that
 * matches the camera, the time label or any member's file path (trimmed,
 * case-insensitive); `time-desc` reverses the backend's earliest-first
 * order. Returns a new array; `clusters` is never changed.
 */
export function filterClusters(clusters: Cluster[], filter: FolderFilter): Cluster[] {
  const q = filter.query.trim().toLowerCase()
  let out = clusters
  if (filter.device) out = out.filter((c) => cameraMatches(c, filter.device))
  if (q) {
    out = out.filter(
      (c) =>
        c.device.toLowerCase().includes(q) ||
        c.time.toLowerCase().includes(q) ||
        c.members.some((m) => m.path.toLowerCase().includes(q)),
    )
  }
  return filter.sort === 'time-desc' ? [...out].reverse() : [...out]
}
