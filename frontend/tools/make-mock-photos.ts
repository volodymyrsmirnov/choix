// Synthetic mock photos for the prototype (the repository is public, so no
// real photo is committed). Writes, under <mock dir>:
//
//   photos/p01.jpg … p36.jpg   480×320 procedural landscapes, ≤ 40 KB each
//   photos/broken.jpg          bytes that are not an image (image-error states)
//   photos.json                { "p01.jpg": "<capture time>", … }
//
//   bun tools/make-mock-photos.ts            → prototype/mock/
//
// Output is byte-identical across runs and machines: one seeded PRNG
// (mulberry32, integer arithmetic), no Math.random, only + − × ÷ and
// Math.sqrt on doubles (exactly rounded everywhere; no sin/cos/exp, whose
// last bits differ between engines), and a pure-JS JPEG encoder.
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import jpeg from 'jpeg-js'

export const PHOTO_COUNT = 36
const W = 480
const H = 320
const QUALITY = 80

type RGB = [number, number, number]

// ── Randomness ──────────────────────────────────────────────────────────────

function mulberry32(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

/** Per-pixel grain in [-1, 1) from an integer hash of the coordinates. */
function grain(x: number, y: number, seed: number): number {
  let h = Math.imul(x, 374761393) ^ Math.imul(y, 668265263) ^ Math.imul(seed, 2246822519)
  h = Math.imul(h ^ (h >>> 13), 1274126177)
  h ^= h >>> 16
  return ((h >>> 0) / 4294967296) * 2 - 1
}

const smooth = (t: number) => t * t * (3 - 2 * t)
const clamp01 = (t: number) => (t < 0 ? 0 : t > 1 ? 1 : t)
const mix = (a: RGB, b: RGB, t: number): RGB => [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t]
const scale = (a: RGB, k: number): RGB => [a[0] * k, a[1] * k, a[2] * k]

/** 1-D fractal value noise in [0, 1]: `octaves` lattices, each twice as fine. */
function ridgeNoise(rand: () => number, octaves: number, cells: number): (x: number) => number {
  const lattices: number[][] = []
  for (let o = 0; o < octaves; o++) {
    const n = cells * 2 ** o + 2
    lattices.push(Array.from({ length: n }, () => rand()))
  }
  return (x: number) => {
    let sum = 0
    let amp = 1
    let total = 0
    for (let o = 0; o < octaves; o++) {
      const lat = lattices[o]!
      const p = x * cells * 2 ** o
      const i = Math.floor(p)
      const f = smooth(p - i)
      sum += amp * (lat[i]! + (lat[i + 1]! - lat[i]!) * f)
      total += amp
      amp *= 0.5
    }
    return sum / total
  }
}

/** 2-D value noise in [0, 1] on a gx × gy lattice (clouds). */
function cloudNoise(rand: () => number, gx: number, gy: number): (u: number, v: number) => number {
  const lat = Array.from({ length: (gx + 2) * (gy + 2) }, () => rand())
  const at = (i: number, j: number) => lat[j * (gx + 2) + i]!
  return (u: number, v: number) => {
    const px = u * gx
    const py = v * gy
    const i = Math.floor(px)
    const j = Math.floor(py)
    const fx = smooth(px - i)
    const fy = smooth(py - j)
    const top = at(i, j) + (at(i + 1, j) - at(i, j)) * fx
    const bottom = at(i, j + 1) + (at(i + 1, j + 1) - at(i, j + 1)) * fx
    return top + (bottom - top) * fy
  }
}

// ── Scenes ──────────────────────────────────────────────────────────────────

interface Palette {
  skyTop: RGB
  skyLow: RGB
  sun: RGB | null
  /** far → near */
  land: [RGB, RGB, RGB]
  sea: RGB
  cloud: RGB
  clouds: number // 0 = clear … 1 = overcast
  moon?: boolean
  stars?: boolean
}

const PALETTES: Record<string, Palette> = {
  dawn: {
    skyTop: [58, 72, 122], skyLow: [242, 172, 142], sun: [255, 228, 196],
    land: [[128, 116, 156], [84, 76, 116], [40, 38, 62]], sea: [92, 92, 132], cloud: [236, 176, 168], clouds: 0.25,
  },
  noon: {
    skyTop: [52, 112, 196], skyLow: [176, 210, 238], sun: [255, 252, 236],
    land: [[124, 152, 172], [72, 112, 88], [38, 70, 44]], sea: [36, 98, 148], cloud: [246, 248, 252], clouds: 0.3,
  },
  dusk: {
    skyTop: [44, 42, 94], skyLow: [248, 138, 72], sun: [255, 204, 128],
    land: [[116, 74, 96], [72, 46, 68], [30, 22, 36]], sea: [70, 52, 84], cloud: [200, 110, 110], clouds: 0.2,
  },
  night: {
    skyTop: [8, 12, 30], skyLow: [34, 50, 88], sun: [234, 236, 222], moon: true, stars: true,
    land: [[34, 42, 66], [22, 28, 48], [10, 14, 26]], sea: [16, 24, 44], cloud: [40, 50, 72], clouds: 0.1,
  },
  overcast: {
    skyTop: [128, 136, 146], skyLow: [194, 198, 202], sun: null,
    land: [[124, 132, 134], [84, 96, 92], [48, 60, 54]], sea: [98, 110, 118], cloud: [214, 216, 220], clouds: 0.9,
  },
  snow: {
    skyTop: [86, 138, 200], skyLow: [214, 228, 242], sun: [255, 255, 246],
    land: [[198, 208, 224], [148, 164, 186], [232, 238, 246]], sea: [96, 130, 160], cloud: [250, 250, 252], clouds: 0.35,
  },
  desert: {
    skyTop: [84, 146, 208], skyLow: [238, 216, 182], sun: [255, 246, 220],
    land: [[204, 156, 116], [182, 112, 70], [142, 82, 50]], sea: [60, 120, 160], cloud: [250, 240, 230], clouds: 0.1,
  },
}

interface Theme {
  palettes: string[]
  sea: boolean
  /** Ridge height as a fraction of the image height. */
  peaks: number
  snowcaps: boolean
  /** Shoot range for the capture times, UTC. */
  from: string
  to: string
}

// Six shoots of six photos each, in the prototype's mock-data order.
const THEMES: Theme[] = [
  { palettes: ['dusk', 'noon', 'dawn', 'dusk', 'noon', 'overcast'], sea: true, peaks: 0.12, snowcaps: false, from: '2026-10-14T07:50:00Z', to: '2026-10-18T19:05:00Z' }, // Lisbon
  { palettes: ['noon', 'dawn', 'snow', 'noon', 'dusk', 'snow'], sea: false, peaks: 0.42, snowcaps: true, from: '2026-09-02T05:45:00Z', to: '2026-09-08T18:20:00Z' }, // Dolomites
  { palettes: ['overcast', 'dawn', 'noon', 'dusk', 'overcast', 'noon'], sea: false, peaks: 0.16, snowcaps: false, from: '2026-07-03T05:40:00Z', to: '2026-07-03T19:20:00Z' }, // Kyoto
  { palettes: ['desert', 'dusk', 'desert', 'noon', 'desert', 'dawn'], sea: false, peaks: 0.1, snowcaps: false, from: '2026-04-21T08:10:00Z', to: '2026-04-26T17:55:00Z' }, // Marrakech
  { palettes: ['dawn', 'noon', 'overcast', 'dusk', 'snow', 'dawn'], sea: true, peaks: 0.38, snowcaps: true, from: '2026-05-29T10:30:00Z', to: '2026-06-14T20:15:00Z' }, // Patagonia
  { palettes: ['night', 'night', 'snow', 'dawn', 'overcast', 'night'], sea: true, peaks: 0.34, snowcaps: true, from: '2026-01-17T09:20:00Z', to: '2026-01-25T16:40:00Z' }, // Faroe Islands
]

function render(index: number): Uint8Array {
  const theme = THEMES[Math.floor(index / 6)]!
  const pal = PALETTES[theme.palettes[index % 6]!]!
  const rand = mulberry32(0x5eed + index * 7919)
  const seed = index + 1

  // Horizon low (sky-heavy) or high (land-heavy), alternating with jitter.
  const low = (index + Math.floor(rand() * 2)) % 2 === 0
  const horizon = Math.round(H * (low ? 0.6 + rand() * 0.14 : 0.4 + rand() * 0.1))
  const sunX = W * (0.15 + rand() * 0.7)
  const sunY = horizon * (pal.moon ? 0.25 + rand() * 0.2 : 0.35 + rand() * 0.45)
  const sunR = pal.moon ? 9 + rand() * 4 : 12 + rand() * 10
  const layers = theme.sea ? 2 : 3
  const ridges = Array.from({ length: layers }, (_, i) => ({
    noise: ridgeNoise(rand, 4, 3 + Math.floor(rand() * 3) + i),
    amp: H * theme.peaks * (1 - i * 0.28) * (0.75 + rand() * 0.5),
    base: theme.sea ? horizon : horizon + (H - horizon) * i * 0.3,
  }))
  const clouds = cloudNoise(rand, 6, 4)
  const streaks = Array.from({ length: H }, () => rand())
  const stars = pal.stars
    ? Array.from({ length: 90 }, () => [Math.floor(rand() * W), Math.floor(rand() * horizon * 0.85)] as const)
    : []
  const starSet = new Set(stars.map(([x, y]) => y * W + x))

  const out = new Uint8Array(W * H * 4)
  // Fractal noise clusters around 0.5; stretching it gives peaks and saddles.
  const ridgeY = ridges.map((r) => Array.from({ length: W }, (_, x) => r.base - r.amp * clamp01((r.noise(x / W) - 0.22) * 1.8)))

  for (let y = 0; y < H; y++) {
    for (let x = 0; x < W; x++) {
      // Sky, with clouds and the sun or moon.
      const t = clamp01(y / horizon)
      let c = mix(pal.skyTop, pal.skyLow, smooth(t))
      const cl = clouds(x / W, (y / H) * 1.6)
      const cover = clamp01((cl - (1 - pal.clouds)) * 2.2) * (1 - t * 0.6)
      c = mix(c, pal.cloud, cover * 0.75)
      if (pal.sun) {
        const dx = x - sunX
        const dy = y - sunY
        const d = Math.sqrt(dx * dx + dy * dy)
        if (d <= sunR) c = mix(c, pal.sun, 1 - cover * 0.6)
        else {
          const g = clamp01(1 - (d - sunR) / (sunR * (pal.moon ? 3 : 7)))
          c = mix(c, pal.sun, g * g * (pal.moon ? 0.25 : 0.55) * (1 - cover * 0.5))
        }
      }
      if (starSet.has(y * W + x)) c = mix(c, [236, 238, 250], 0.85)

      // Water in front of the far shore.
      if (theme.sea && y >= horizon) {
        const s = (y - horizon) / (H - horizon)
        c = mix(mix(pal.skyLow, pal.sea, 0.55), scale(pal.sea, 0.6), smooth(s))
        if (pal.sun) {
          const width = sunR * (1.2 + s * 5)
          const dx = Math.abs(x - sunX)
          if (dx < width) {
            const k = streaks[y]! > 0.45 ? 1 : 0.25
            c = mix(c, pal.sun, (1 - dx / width) * k * 0.55)
          }
        }
      }

      // Ridges, far to near; with the sea they stop at the horizon.
      for (let i = 0; i < layers; i++) {
        const top = ridgeY[i]![x]!
        if (y < top || (theme.sea && y >= horizon)) continue
        const haze = layers === 3 ? [0.5, 0.22, 0][i]! : [0.45, 0.12][i]!
        let land = mix(pal.land[i]!, pal.skyLow, haze)
        land = scale(land, 1 - 0.18 * clamp01((y - top) / H))
        if (theme.snowcaps && i < 2) {
          const r = ridges[i]!
          const peak = clamp01((r.base - top) / r.amp)
          if (peak > 0.5 && y - top < (peak - 0.5) * r.amp * 0.7) land = mix(land, mix([240, 244, 250], pal.skyLow, haze), 0.85)
        }
        c = land
      }

      const n = grain(x, y, seed) * 1.5
      const o = (y * W + x) * 4
      out[o] = Math.round(Math.min(255, Math.max(0, c[0] + n)))
      out[o + 1] = Math.round(Math.min(255, Math.max(0, c[1] + n)))
      out[o + 2] = Math.round(Math.min(255, Math.max(0, c[2] + n)))
      out[o + 3] = 255
    }
  }
  return out
}

function captureTime(index: number): string {
  const theme = THEMES[Math.floor(index / 6)]!
  const from = Date.parse(theme.from)
  const to = Date.parse(theme.to)
  const minute = 60_000
  const at = from + Math.round(((to - from) * (index % 6)) / 5 / minute) * minute
  return new Date(at).toISOString().replace('.000Z', 'Z')
}

export const photoName = (i: number) => `p${String(i + 1).padStart(2, '0')}.jpg`

/** Writes the photos, broken.jpg and photos.json under `mockDir`. */
export function makeMockPhotos(mockDir: string): void {
  const dir = join(mockDir, 'photos')
  mkdirSync(dir, { recursive: true })
  const times: Record<string, string> = {}
  for (let i = 0; i < PHOTO_COUNT; i++) {
    const data = render(i)
    const { data: bytes } = jpeg.encode({ data, width: W, height: H }, QUALITY)
    writeFileSync(join(dir, photoName(i)), bytes)
    times[photoName(i)] = captureTime(i)
  }
  writeFileSync(join(dir, 'broken.jpg'), 'choix mock photo: these bytes are deliberately not an image\n')
  writeFileSync(join(mockDir, 'photos.json'), `${JSON.stringify(times, null, 2)}\n`)
}

if (import.meta.main) {
  const mockDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'prototype', 'mock')
  makeMockPhotos(mockDir)
  console.log(`wrote ${PHOTO_COUNT} photos, broken.jpg and photos.json to ${mockDir}`)
}
