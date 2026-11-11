// Generates every choix icon variant from one description, then renders the
// PNG sizes and the macOS .icns. Run from the repo root with `make icons`
// (needs bun, rsvg-convert from `brew install librsvg`, and macOS iconutil).
//
// Why a generator instead of hand-kept SVGs: the same artwork ships in five
// shapes (full-bleed, macOS rounded, simplified small, favicon, Icon Composer
// layers). Hand-copying the scene between them drifts; one source does not.
//
// Outputs (all committed, see README.md for which file goes where):
//   choix-icon.svg           full-bleed 1024 master (App Store, Icon Composer)
//   choix-icon-macos.svg     macOS rounded shape on the 1024 template + shadow
//   choix-icon-small.svg     simplified artwork for 16 and 32 px
//   favicon.svg              simplified artwork that fills its canvas (web UI)
//   layers/*.svg             unshadowed layers for Xcode's Icon Composer
//   png/*.png, choix.icns    rendered sizes

import { $ } from 'bun'
import { mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const OUT = new URL('.', import.meta.url).pathname
const W = 1024

// ── palette ──────────────────────────────────────────────────────────────
// The badge green is the UI's pick colour, oklch(0.72 0.18 145) ≈ #4CC157.
// The background is the UI's graphite (bg-0..bg-4), lifted a little so the
// icon doesn't disappear in a dark Dock.

interface Scene {
  sky: [string, string, string] // top, middle, horizon
  sun: [string, string] // core, rim
  far: string // far mountain range
  near: string // near hill
  water: [string, string] // top, bottom
  glint: string
}

const VIVID: Scene = {
  sky: ['#E26C8C', '#F4945C', '#FFC56E'],
  sun: ['#FFF6C2', '#FFD23F'],
  far: '#7D5A8E',
  near: '#5C4476',
  water: ['#6E5C97', '#3D3D72'],
  glint: '#FFC56E',
}
// Passed-over shots: same scene, drained of colour, so the front print reads
// as "the one you chose" even before the badge registers.
const MAUVE: Scene = {
  sky: ['#A88FA8', '#C2A2A4', '#D3B8A8'],
  sun: ['#EFE3CC', '#E2CFAE'],
  far: '#7A6886',
  near: '#665673',
  water: ['#6C6484', '#4F4C6B'],
  glint: '#D3B8A8',
}
const SLATE: Scene = {
  sky: ['#8796B0', '#9FA9BD', '#B4BAC7'],
  sun: ['#DCE0E7', '#C9CED8'],
  far: '#68718C',
  near: '#575F78',
  water: ['#646E89', '#4A516B'],
  glint: '#B4BAC7',
}

const PICK_TOP = '#6BD77A'
const PICK_BOTTOM = '#2FA046'

// ── primitives ───────────────────────────────────────────────────────────

const n = (v: number) => +v.toFixed(2)

// macOS app-icon outline: a superellipse is a close stand-in for Apple's
// continuous-corner rounded square.
function squircle(cx: number, cy: number, half: number, exp = 5, steps = 360): string {
  const pts: string[] = []
  for (let i = 0; i < steps; i++) {
    const t = (i / steps) * Math.PI * 2
    const c = Math.cos(t)
    const s = Math.sin(t)
    const x = cx + half * Math.sign(c) * Math.abs(c) ** (2 / exp)
    const y = cy + half * Math.sign(s) * Math.abs(s) ** (2 / exp)
    pts.push(`${n(x)},${n(y)}`)
  }
  return `M${pts.join('L')}Z`
}

class Defs {
  private items: string[] = []
  constructor(private prefix: string) {}
  id(name: string) {
    return `${this.prefix}-${name}`
  }
  add(svg: string) {
    this.items.push(svg)
  }
  toString() {
    return this.items.length ? `<defs>${this.items.join('')}</defs>` : ''
  }
}

function linear(defs: Defs, name: string, stops: [number, string, number?][], x2 = 0, y2 = 1) {
  const id = defs.id(name)
  const s = stops
    .map(([o, c, a]) => `<stop offset="${o}" stop-color="${c}"${a === undefined ? '' : ` stop-opacity="${a}"`}/>`)
    .join('')
  defs.add(`<linearGradient id="${id}" x1="0" y1="0" x2="${x2}" y2="${y2}">${s}</linearGradient>`)
  return `url(#${id})`
}

function radial(defs: Defs, name: string, stops: [number, string, number?][]) {
  const id = defs.id(name)
  const s = stops
    .map(([o, c, a]) => `<stop offset="${o}" stop-color="${c}"${a === undefined ? '' : ` stop-opacity="${a}"`}/>`)
    .join('')
  defs.add(`<radialGradient id="${id}">${s}</radialGradient>`)
  return `url(#${id})`
}

function dropShadow(defs: Defs, name: string, dy: number, blur: number, opacity: number) {
  const id = defs.id(name)
  defs.add(
    `<filter id="${id}" x="-30%" y="-30%" width="160%" height="170%" color-interpolation-filters="sRGB">` +
      `<feDropShadow dx="0" dy="${dy}" stdDeviation="${blur}" flood-color="#000" flood-opacity="${opacity}"/></filter>`,
  )
  return `url(#${id})`
}

function blur(defs: Defs, name: string, amount: number) {
  const id = defs.id(name)
  defs.add(`<filter id="${id}" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="${amount}"/></filter>`)
  return `url(#${id})`
}

// ── artwork pieces ───────────────────────────────────────────────────────

function background(defs: Defs): string {
  const base = linear(defs, 'bg', [
    [0, '#3C4149'],
    [1, '#16181C'],
  ])
  const lift = radial(defs, 'bg-lift', [
    [0, '#8FA3C0', 0.22],
    [1, '#8FA3C0', 0],
  ])
  return (
    `<rect width="${W}" height="${W}" fill="${base}"/>` +
    `<ellipse cx="${W / 2}" cy="420" rx="520" ry="440" fill="${lift}"/>`
  )
}

interface PrintOpts {
  size: number // outer edge length
  border: number // white paper border
  scene: Scene
  simple?: boolean // drop small details for 16/32 px
}

// A photo print centred on the origin.
function print(defs: Defs, name: string, o: PrintOpts): string {
  const { size: P, border: b, scene: s, simple } = o
  const w = P - 2 * b // photo edge
  const x0 = -P / 2 + b
  const y0 = -P / 2 + b
  const R = P * 0.07 // paper corner
  const r = Math.max(R - b * 0.6, P * 0.02) // photo corner

  const paper = linear(defs, `${name}-paper`, [
    [0, '#FBFAF7'],
    [1, '#E7E3DD'],
  ])
  const sky = linear(defs, `${name}-sky`, [
    [0, s.sky[0]],
    [0.5, s.sky[1]],
    [0.66, s.sky[2]],
  ])
  const water = linear(defs, `${name}-water`, [
    [0, s.water[0]],
    [1, s.water[1]],
  ])
  const sun = radial(defs, `${name}-sun`, [
    [0, s.sun[0]],
    [0.7, s.sun[1]],
    [1, s.sun[1]],
  ])
  const glow = radial(defs, `${name}-glow`, [
    [0, s.sun[1], 0.55],
    [1, s.sun[1], 0],
  ])
  const sheen = linear(defs, `${name}-sheen`, [
    [0, '#FFFFFF', 0.2],
    [0.45, '#FFFFFF', 0],
  ])
  const clipId = defs.id(`${name}-clip`)
  defs.add(
    `<clipPath id="${clipId}"><rect x="${n(x0)}" y="${n(y0)}" width="${n(w)}" height="${n(w)}" rx="${n(r)}"/></clipPath>`,
  )

  // Scene coordinates are fractions of the photo edge.
  const X = (f: number) => n(x0 + f * w)
  const Y = (f: number) => n(y0 + f * w)
  const horizon = 0.66
  const sunX = 0.66
  const sunY = 0.5
  const sunR = simple ? 0.14 : 0.115

  const far =
    `M${X(0)},${Y(0.63)} C${X(0.12)},${Y(0.6)} ${X(0.22)},${Y(0.45)} ${X(0.36)},${Y(0.43)} ` +
    `C${X(0.48)},${Y(0.42)} ${X(0.58)},${Y(0.55)} ${X(0.72)},${Y(0.585)} ` +
    `C${X(0.83)},${Y(0.61)} ${X(0.92)},${Y(0.58)} ${X(1)},${Y(0.6)} L${X(1)},${Y(0.7)} L${X(0)},${Y(0.7)} Z`
  const near =
    `M${X(0.46)},${Y(0.7)} C${X(0.6)},${Y(0.6)} ${X(0.76)},${Y(0.565)} ${X(0.88)},${Y(0.585)} C${X(0.94)},${Y(0.595)} ${X(0.98)},${Y(0.61)} ${X(1)},${Y(0.62)} L${X(1)},${Y(0.7)} Z`

  // Sun reflection: a column that tapers and fades towards the viewer, plus a
  // couple of soft ripple lines across it.
  const refl = linear(defs, `${name}-refl`, [
    [0, s.glint, 0.95],
    [1, s.glint, 0],
  ])
  // Three nested columns at low opacity give the column soft sides without a
  // blur filter (Icon Composer layers stay filter-free).
  const column = (rw: number, a: number) =>
    `<path d="M${X(sunX - rw / 2)},${Y(horizon)} L${X(sunX + rw / 2)},${Y(horizon)} ` +
    `L${X(sunX + rw / 5)},${Y(1)} L${X(sunX - rw / 5)},${Y(1)} Z" fill="${refl}" opacity="${a}"/>`
  const rw = simple ? 0.3 : 0.26
  const reflection =
    column(rw, 0.3) +
    column(rw * 0.66, 0.4) +
    column(rw * 0.33, 0.6) +
    (simple
      ? ''
      : [0.76, 0.86]
          .map(
            (gy) =>
              `<rect x="${X(0.08)}" y="${Y(gy)}" width="${n(0.84 * w)}" height="${n(0.008 * w)}" rx="${n(0.004 * w)}" fill="#FFFFFF" opacity="0.08"/>`,
          )
          .join(''))

  return (
    `<rect x="${n(-P / 2)}" y="${n(-P / 2)}" width="${P}" height="${P}" rx="${n(R)}" fill="${paper}"/>` +
    `<g clip-path="url(#${clipId})">` +
    `<rect x="${n(x0)}" y="${n(y0)}" width="${n(w)}" height="${n(w)}" fill="${sky}"/>` +
    `<circle cx="${X(sunX)}" cy="${Y(sunY)}" r="${n(sunR * 2 * w)}" fill="${glow}"/>` +
    `<circle cx="${X(sunX)}" cy="${Y(sunY)}" r="${n(sunR * w)}" fill="${sun}"/>` +
    `<path d="${far}" fill="${s.far}"/>` +
    (simple ? '' : `<path d="${near}" fill="${s.near}"/>`) +
    `<rect x="${n(x0)}" y="${Y(horizon)}" width="${n(w)}" height="${n((1 - horizon) * w + 1)}" fill="${water}"/>` +
    reflection +
    `<rect x="${n(x0)}" y="${n(y0)}" width="${n(w)}" height="${n(w)}" fill="${sheen}"/>` +
    `</g>` +
    // A hairline on the paper edge keeps the print crisp against the photo
    // behind it at mid sizes.
    `<rect x="${n(-P / 2 + 0.75)}" y="${n(-P / 2 + 0.75)}" width="${n(P - 1.5)}" height="${n(P - 1.5)}" rx="${n(R)}" fill="none" stroke="#000" stroke-opacity="0.08" stroke-width="1.5"/>`
  )
}

// The pick badge, centred on the origin. The white ring separates it from
// whatever is underneath, which is what keeps it legible at 32 px.
function badge(defs: Defs, R: number): string {
  const fill = linear(defs, 'pick', [
    [0, PICK_TOP],
    [1, PICK_BOTTOM],
  ])
  const shine = linear(defs, 'pick-shine', [
    [0, '#FFFFFF', 0.45],
    [1, '#FFFFFF', 0],
  ])
  const inner = R * 0.86
  const k = inner
  return (
    `<circle r="${n(R)}" fill="#FFFFFF"/>` +
    `<circle r="${n(inner)}" fill="${fill}"/>` +
    `<ellipse cy="${n(-inner * 0.42)}" rx="${n(inner * 0.72)}" ry="${n(inner * 0.46)}" fill="${shine}"/>` +
    `<path d="M${n(-0.44 * k)},${n(0.02 * k)} L${n(-0.13 * k)},${n(0.33 * k)} L${n(0.45 * k)},${n(-0.3 * k)}" ` +
    `fill="none" stroke="#FFFFFF" stroke-width="${n(0.19 * k)}" stroke-linecap="round" stroke-linejoin="round"/>`
  )
}

// ── compositions ─────────────────────────────────────────────────────────

interface Placed {
  name: string
  x: number
  y: number
  rot: number
  opts: PrintOpts
}

interface Composition {
  prints: Placed[] // back to front
  badge: { x: number; y: number; r: number }
  ground: { x: number; y: number; rx: number; ry: number }
}

const FULL: Composition = {
  prints: [
    { name: 'back', x: 454, y: 500, rot: -15, opts: { size: 520, border: 24, scene: SLATE } },
    { name: 'middle', x: 512, y: 518, rot: -7, opts: { size: 520, border: 24, scene: MAUVE } },
    { name: 'front', x: 574, y: 540, rot: 0, opts: { size: 520, border: 24, scene: VIVID } },
  ],
  badge: { x: 574 + 260 - 24 - 70 - 12, y: 540 - 260 + 24 + 70 + 12, r: 80 },
  ground: { x: 540, y: 806, rx: 260, ry: 26 },
}

// 16 and 32 px: one back print for the "stack" hint, a bigger front print,
// a bigger badge on the corner, and no fine scene detail.
const SMALL: Composition = {
  prints: [
    { name: 'back', x: 462, y: 530, rot: -12, opts: { size: 600, border: 38, scene: SLATE, simple: true } },
    { name: 'front', x: 548, y: 548, rot: 0, opts: { size: 600, border: 38, scene: VIVID, simple: true } },
  ],
  badge: { x: 548 + 300 - 70, y: 548 - 300 + 70, r: 150 },
  ground: { x: 530, y: 862, rx: 260, ry: 24 },
}

type Part = 'background' | 'back' | 'front' | 'badge'

interface ArtOpts {
  shadows: boolean
  parts?: Part[] // default: everything
}

function art(defs: Defs, c: Composition, o: ArtOpts): string {
  const want = (p: Part) => !o.parts || o.parts.includes(p)
  const printShadow = o.shadows ? dropShadow(defs, 'print-shadow', 12, 14, 0.38) : ''
  const badgeShadow = o.shadows ? dropShadow(defs, 'badge-shadow', 5, 7, 0.35) : ''
  let out = ''
  if (want('background')) out += background(defs)
  if (o.shadows && want('back')) {
    const g = c.ground
    out += `<ellipse cx="${g.x}" cy="${g.y}" rx="${g.rx}" ry="${g.ry}" fill="#000" opacity="0.32" filter="${blur(defs, 'ground', 24)}"/>`
  }
  c.prints.forEach((p, i) => {
    const isFront = i === c.prints.length - 1
    if (!want(isFront ? 'front' : 'back')) return
    const body = print(defs, p.name, p.opts)
    const placed = `<g transform="translate(${p.x} ${p.y})${p.rot ? ` rotate(${p.rot})` : ''}">${body}</g>`
    out += printShadow ? `<g filter="${printShadow}">${placed}</g>` : placed
  })
  if (want('badge')) {
    const b = c.badge
    const placed = `<g transform="translate(${n(b.x)} ${n(b.y)})">${badge(defs, b.r)}</g>`
    out += badgeShadow ? `<g filter="${badgeShadow}">${placed}</g>` : placed
  }
  return out
}

function svg(defs: Defs, body: string, title: string): string {
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} ${W}" width="${W}" height="${W}">` +
    `<title>${title}</title>${defs}${body}</svg>\n`
  )
}

// Full-bleed art, square corners. Icon Composer and the App Store mask it.
function fullBleed(prefix: string, c: Composition): string {
  const defs = new Defs(prefix)
  return svg(defs, art(defs, c, { shadows: true }), 'choix')
}

// Art clipped to the macOS rounded shape. `body` is the shape's edge length on
// the 1024 canvas: 824 is Apple's macOS template (100 px margin for the drop
// shadow); the favicon uses nearly the whole canvas.
function rounded(prefix: string, c: Composition, body: number, withShadow: boolean): string {
  const defs = new Defs(prefix)
  const shape = squircle(W / 2, W / 2, W / 2)
  const clipId = defs.id('shape')
  defs.add(`<clipPath id="${clipId}"><path d="${shape}"/></clipPath>`)
  const scale = body / W
  const off = (W - body) / 2
  const inner = `<g clip-path="url(#${clipId})">${art(defs, c, { shadows: true })}</g>`
  // Inner edge highlight, like Apple's own icons: a faint light rim on top.
  const rim = linear(defs, 'rim', [
    [0, '#FFFFFF', 0.28],
    [0.5, '#FFFFFF', 0.04],
    [1, '#FFFFFF', 0.1],
  ])
  const edge = `<path d="${shape}" fill="none" stroke="${rim}" stroke-width="5"/>`
  let content = `<g transform="translate(${off} ${off}) scale(${n(scale * 1000) / 1000})">${inner}${edge}</g>`
  if (withShadow) {
    const sh = dropShadow(defs, 'icon-shadow', 10, 12, 0.32)
    content = `<g filter="${sh}">${content}</g>`
  }
  return svg(defs, content, 'choix')
}

function layer(prefix: string, parts: Part[]): string {
  const defs = new Defs(prefix)
  return svg(defs, art(defs, FULL, { shadows: false, parts }), `choix — ${parts.join(', ')}`)
}

// ── write ────────────────────────────────────────────────────────────────

const files: Record<string, string> = {
  'choix-icon.svg': fullBleed('ci', FULL),
  'choix-icon-macos.svg': rounded('cm', FULL, 824, true),
  'choix-icon-small.svg': rounded('cs', SMALL, 824, true),
  'favicon.svg': rounded('fv', SMALL, 1000, false),
  'layers/1-background.svg': layer('l1', ['background']),
  'layers/2-back-prints.svg': layer('l2', ['back']),
  'layers/3-front-print.svg': layer('l3', ['front']),
  'layers/4-badge.svg': layer('l4', ['badge']),
}

mkdirSync(join(OUT, 'layers'), { recursive: true })
for (const [name, body] of Object.entries(files)) {
  writeFileSync(join(OUT, name), body)
}

// ── render ───────────────────────────────────────────────────────────────
// .icns slots: 16 and 32 px come from the simplified artwork, everything
// larger from the detailed one. Both use the 824-on-1024 macOS template so
// the outline stays the same size across slots.

const PNG = join(OUT, 'png')
const ICONSET = join(OUT, 'choix.iconset')
rmSync(PNG, { recursive: true, force: true })
rmSync(ICONSET, { recursive: true, force: true })
mkdirSync(PNG, { recursive: true })
mkdirSync(ICONSET, { recursive: true })

const render = (src: string, px: number, dst: string) =>
  $`rsvg-convert -w ${px} -h ${px} ${join(OUT, src)} -o ${dst}`.quiet()

const slots: [string, number][] = [
  ['icon_16x16', 16],
  ['icon_16x16@2x', 32],
  ['icon_32x32', 32],
  ['icon_32x32@2x', 64],
  ['icon_128x128', 128],
  ['icon_128x128@2x', 256],
  ['icon_256x256', 256],
  ['icon_256x256@2x', 512],
  ['icon_512x512', 512],
  ['icon_512x512@2x', 1024],
]
for (const [slot, px] of slots) {
  await render(px <= 32 ? 'choix-icon-small.svg' : 'choix-icon-macos.svg', px, join(ICONSET, `${slot}.png`))
}
for (const px of [16, 32, 64, 128, 256, 512, 1024]) {
  await render(px <= 32 ? 'choix-icon-small.svg' : 'choix-icon-macos.svg', px, join(PNG, `choix-${px}.png`))
}
// The App Store / Icon Composer master: full-bleed, square, opaque.
await render('choix-icon.svg', 1024, join(PNG, 'choix-1024-fullbleed.png'))

await $`iconutil -c icns ${ICONSET} -o ${join(OUT, 'choix.icns')}`.quiet()
rmSync(ICONSET, { recursive: true, force: true })

console.log('icons written to', OUT)
