// The prototype's URL contract: `#s102` names the state; the query holds the
// toggles, which survive a state change:
//   ?size=min|default|large  ?theme=dark  ?os=macos  ?freeze  ?nowrapfx  ?bare
// Flags count by presence (`?freeze`), and `=0` / `=false` switch them off.
import gate from '../../docs/design/gate.json'

export type Size = 'min' | 'default' | 'large'
export interface Toggles {
  size: Size
  theme: 'dark'
  os: 'macos'
  /** No animation or transition; running animations are cancelled on open. */
  freeze: boolean
  /** CSS text-wrap switched off (the orphan check's second pass). */
  nowrapfx: boolean
  /** Only the app surface, at the exact size, without the navigator. */
  bare: boolean
}

export const SIZES: Size[] = ['min', 'default', 'large']
/** Window sizes under review, [width, height] in px (docs/design/gate.json). */
export const SIZE_PX = gate.sizes as Record<Size, [number, number]>
export const DEFAULT_TOGGLES: Toggles = {
  size: 'default',
  theme: 'dark',
  os: 'macos',
  freeze: false,
  nowrapfx: false,
  bare: false,
}

const flag = (q: URLSearchParams, name: string): boolean => {
  if (!q.has(name)) return false
  const v = q.get(name)
  return v !== '0' && v !== 'false'
}

export function parseLocation(search: string, hash: string): { n: number | null; toggles: Toggles } {
  const q = new URLSearchParams(search)
  const size = q.get('size')
  const m = /^#s(\d+)$/.exec(hash)
  return {
    n: m ? Number(m[1]) : null,
    toggles: {
      size: SIZES.includes(size as Size) ? (size as Size) : DEFAULT_TOGGLES.size,
      theme: 'dark',
      os: 'macos',
      freeze: flag(q, 'freeze'),
      nowrapfx: flag(q, 'nowrapfx'),
      bare: flag(q, 'bare'),
    },
  }
}

/** `?bare&size=large&freeze#s803`; default values are left out. */
export function formatLocation(n: number | null, t: Toggles): string {
  const parts: string[] = []
  if (t.bare) parts.push('bare')
  if (t.size !== DEFAULT_TOGGLES.size) parts.push(`size=${t.size}`)
  if (t.theme !== DEFAULT_TOGGLES.theme) parts.push(`theme=${t.theme}`)
  if (t.os !== DEFAULT_TOGGLES.os) parts.push(`os=${t.os}`)
  if (t.freeze) parts.push('freeze')
  if (t.nowrapfx) parts.push('nowrapfx')
  return (parts.length ? `?${parts.join('&')}` : '') + (n === null ? '' : `#s${n}`)
}

/**
 * The URL to push or replace: path, query and hash. Always a path-relative
 * reference, never a fragment alone: `#s102` would keep the current query,
 * so a toggle switched off would stay in the address bar.
 */
export function locationHref(pathname: string, n: number | null, t: Toggles): string {
  return pathname + formatLocation(n, t)
}

/** The absolute link Copy link puts on the clipboard: exactly the current state and toggles. */
export function shareUrl(origin: string, pathname: string, n: number | null, t: Toggles): string {
  return origin + locationHref(pathname, n, t)
}
