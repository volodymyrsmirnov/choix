// Typed wrappers around the choix Go HTTP API.

export interface Member {
  file_id: number
  path: string
  content_hash: string
  picked: boolean
  rejected: boolean
  kind: 'photo' | 'video'
  format: string
  // Advisory technical-quality rating, 0–10 with one decimal (0 = worst).
  // Absent for videos, unrated photos and when the feature is off.
  quality?: number
}

// thumbURL / fullURL build path-keyed media URLs with a content-hash cache
// buster. The path itself is stable per file so cluster rebuilds don't
// invalidate the browser cache; when the file's bytes change the API
// hands back a new content_hash and the URL changes.
function encodePath(p: string): string {
  return p.split('/').map(encodeURIComponent).join('/')
}
export function thumbURL(m: Member, tier: 'thumb' | 'preview' = 'thumb'): string {
  const q = new URLSearchParams({ tier })
  if (m.content_hash) q.set('v', m.content_hash)
  return `/thumb/${encodePath(m.path)}?${q.toString()}`
}
export function fullURL(m: Member): string {
  const q = m.content_hash ? `?v=${encodeURIComponent(m.content_hash)}` : ''
  return `/full/${encodePath(m.path)}${q}`
}

export interface Cluster {
  id: number
  device: string
  time: string
  members: Member[]
}

export interface LibraryResponse {
  folder: string
  picked: number
  total: number
  clusters: Cluster[]
}

export interface ExifPayload {
  shutter?: string
  aperture?: string
  iso?: string
  focal_length?: string
  camera?: string
  captured?: string
  size?: string
  gps_lat?: number
  gps_lon?: number
}

export interface FocusResponse {
  cluster: Cluster
  cluster_index: number
  cluster_count: number
  exif: Record<string, ExifPayload>
  next_cluster_id: number
  prev_cluster_id: number
}

export interface SettingsBody {
  bucket_size: number
  similarity: number
  picks_dir: string
  advance_on_action: boolean
  hide_rejected_photos: boolean
  cross_device_merging: boolean
  quality_rating: boolean
}

// Snapshot of the background quality worker (GET /api/quality).
export type QualityState =
  | 'disabled' | 'downloading' | 'download_failed' | 'waiting' | 'scoring' | 'idle' | 'error'
export interface QualityStatus {
  enabled: boolean
  state: QualityState
  // Keep polling while true: work is in flight or queued, or a scan is
  // running that will hand the worker photos. Set synchronously server-side,
  // so one fetch right after a trigger is enough to know.
  active: boolean
  received: number // model download, bytes
  size: number     // 0 while unknown
  done: number     // photos rated in the current pass
  total: number
  // Photos dealt with since choix started; only grows. A change means there
  // are ratings the page has not fetched yet.
  rated: number
  error?: string
}

export interface ProgressEvent {
  stage: string
  done: number
  total: number
  failed: number
  phase?: string
}

// Requests the page makes by itself (status polling, automatic refreshes)
// carry this header so the server does not count them as user activity and
// idle shutdown still happens under an unattended tab. Mirrors
// BackgroundRequestHeader in internal/server/server.go.
const BACKGROUND: RequestInit = { headers: { 'X-Choix-Background': '1' } }

// `background` marks a refresh nobody asked for (ratings arriving).
export async function fetchLibrary(background = false): Promise<LibraryResponse> {
  const r = await fetch('/api/library', background ? BACKGROUND : undefined)
  if (!r.ok) throw new Error(`library: ${r.status}`)
  return r.json()
}

export async function fetchCluster(id: number, signal?: AbortSignal, background = false): Promise<FocusResponse> {
  const r = await fetch(`/api/clusters/${id}`, { signal, ...(background ? BACKGROUND : {}) })
  if (!r.ok) throw new Error(`cluster ${id}: ${r.status}`)
  return r.json()
}

export async function fetchSettings(): Promise<SettingsBody> {
  const r = await fetch('/api/settings')
  if (!r.ok) throw new Error(`settings: ${r.status}`)
  return r.json()
}

export async function saveSettings(body: Partial<SettingsBody>): Promise<{ saved: boolean; reclustered: boolean }> {
  const r = await fetch('/api/settings', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!r.ok) throw new Error(`save settings: ${r.status} ${await r.text()}`)
  return r.json()
}

export async function fetchQuality(): Promise<QualityStatus> {
  const r = await fetch('/api/quality', BACKGROUND)
  if (!r.ok) throw new Error(`quality: ${r.status}`)
  return r.json()
}

export async function retryQuality(): Promise<QualityStatus> {
  const r = await fetch('/api/quality/retry', { method: 'POST' })
  if (!r.ok) throw new Error(`quality retry: ${r.status} ${await r.text()}`)
  return r.json()
}

export async function postPick(file_id: number, action: 'pick' | 'unpick' | 'reject' | 'unreject'): Promise<{ state: string }> {
  const r = await fetch('/api/picks', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ file_id, action }),
  })
  if (!r.ok) throw new Error(`pick: ${r.status} ${await r.text()}`)
  return r.json()
}

export async function postRecluster(): Promise<void> {
  const r = await fetch('/api/recluster', { method: 'POST' })
  if (!r.ok) throw new Error(`recluster: ${r.status} ${await r.text()}`)
}

export function subscribeProgress(onEvent: (ev: ProgressEvent) => void): () => void {
  const es = new EventSource('/api/progress')
  es.onmessage = (m) => {
    try { onEvent(JSON.parse(m.data) as ProgressEvent) } catch { /* ignore */ }
  }
  return () => es.close()
}
