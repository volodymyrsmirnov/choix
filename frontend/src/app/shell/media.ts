// Media URLs of one folder's files, served by Go's media handler on the
// Wails asset server (internal/app/media):
//
//   /media/thumb/<folder id>/<escaped path>?tier=thumb|preview&v=<hash>
//   /media/full/<folder id>/<escaped path>?v=<hash>
//
// Built exactly as Go builds them (media.ThumbURL / FullURL: url.PathEscape
// per path element, url.QueryEscape for the query), so a URL the frontend
// makes and one Go hands it (a Library row's previews) are the same string
// and share the webview's cache. `v` is the content hash: a changed file
// gets a new URL.
import type { Member, MediaURLs } from '../../design/types'

/** Bytes Go's url.PathEscape leaves alone in a path segment: unreserved, plus $&+:=@. */
const PATH_SAFE = /[A-Za-z0-9\-._~$&+:=@]/

function percent(ch: string): string {
  return Array.from(new TextEncoder().encode(ch), (b) => `%${b.toString(16).toUpperCase().padStart(2, '0')}`).join('')
}

/** Go's url.PathEscape. */
export function pathEscape(s: string): string {
  let out = ''
  for (const ch of s) out += PATH_SAFE.test(ch) ? ch : percent(ch)
  return out
}

/** Go's url.QueryEscape: unreserved bytes kept, space as `+`, the rest escaped. */
export function queryEscape(s: string): string {
  let out = ''
  for (const ch of s) out += /[A-Za-z0-9\-._~]/.test(ch) ? ch : ch === ' ' ? '+' : percent(ch)
  return out
}

const escapePath = (rel: string) => rel.split('/').map(pathEscape).join('/')

export function mediaFor(folderId: string): MediaURLs {
  const base = (route: 'thumb' | 'full', m: Member) => `/media/${route}/${pathEscape(folderId)}/${escapePath(m.path)}`
  return {
    thumb(m, tier = 'thumb') {
      const v = m.content_hash !== '' ? `&v=${queryEscape(m.content_hash)}` : ''
      return `${base('thumb', m)}?tier=${queryEscape(tier)}${v}`
    },
    full(m) {
      return m.content_hash !== '' ? `${base('full', m)}?v=${queryEscape(m.content_hash)}` : base('full', m)
    },
  }
}
