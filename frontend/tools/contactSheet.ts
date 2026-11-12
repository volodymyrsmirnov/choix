// One contact sheet per group, size and browser: the group's state
// screenshots side by side with their numbers and labels, saved as a PNG.
//
// The sheet is drawn onto one <canvas> in a blank browser page (no native
// image library) and read back with toDataURL, not captured with a full-page
// screenshot: WebKit paints a page taller than the viewport tile by tile, and
// a tile below the fold could be captured before its image was painted,
// which left a blank tile (s409 on webkit/large/contact-compare.png) although
// the state's own screenshot was fine. drawImage on a decoded image is
// synchronous; each tile is checked to be fully drawn (a screenshot is
// opaque) and to be in the sheet, so a sheet can never hold a blank tile
// silently: the run fails instead.
import { readFileSync, writeFileSync } from 'node:fs'
import type { BrowserContext } from '@playwright/test'

export interface Sheet {
  title: string
  shots: { n: number; label: string; file: string }[]
  out: string
}

/** Sheet layout, in CSS px at scale 1. */
export const SHEET = { width: 1640, pad: 24, gap: 20, columns: 3, titleH: 40, captionH: 26 } as const

export async function writeContactSheet(context: BrowserContext, sheet: Sheet): Promise<void> {
  const shots = sheet.shots.map((s) => ({ n: s.n, label: s.label, src: `data:image/png;base64,${readFileSync(s.file).toString('base64')}` }))
  const page = await context.newPage()
  try {
    const result = await page.evaluate(
      async ({ title, shots, L }) => {
        const images = await Promise.all(
          shots.map(async (s) => {
            const img = new Image()
            img.src = s.src
            await img.decode()
            return img
          }),
        )
        const tileW = (L.width - 2 * L.pad - (L.columns - 1) * L.gap) / L.columns
        const rows: number[] = []
        for (let i = 0; i < images.length; i += L.columns) {
          rows.push(Math.max(...images.slice(i, i + L.columns).map((img) => Math.round((img.naturalHeight * tileW) / img.naturalWidth))))
        }
        const height = L.pad + L.titleH + rows.reduce((a, h) => a + h + 2 + L.captionH + L.gap, 0) - L.gap + L.pad
        const canvas = document.createElement('canvas')
        canvas.width = L.width
        canvas.height = Math.ceil(height)
        const g = canvas.getContext('2d')!
        g.fillStyle = '#111'
        g.fillRect(0, 0, canvas.width, canvas.height)
        g.textBaseline = 'top'
        g.fillStyle = '#fff'
        g.font = '600 16px -apple-system, system-ui, sans-serif'
        g.fillText(title, L.pad, L.pad)

        const problems: string[] = []
        let y = L.pad + L.titleH
        images.forEach((img, i) => {
          const col = i % L.columns
          const row = Math.floor(i / L.columns)
          if (col === 0 && row > 0) y += rows[row - 1]! + 2 + L.captionH + L.gap
          const x = L.pad + col * (tileW + L.gap)
          const h = Math.round((img.naturalHeight * tileW) / img.naturalWidth)
          g.strokeStyle = '#333'
          g.lineWidth = 1
          g.strokeRect(Math.round(x) + 0.5, y + 0.5, Math.round(tileW) + 1, h + 1)
          // Scale the screenshot on a scratch canvas, then copy its pixels
          // into the sheet unchanged. A screenshot is opaque, so a pixel that
          // is not fully opaque means the image was not drawn.
          const w = Math.round(tileW)
          const scratch = document.createElement('canvas')
          scratch.width = w
          scratch.height = h
          const sg = scratch.getContext('2d')!
          sg.drawImage(img, 0, 0, w, h)
          const tile = sg.getImageData(0, 0, w, h)
          g.putImageData(tile, Math.round(x) + 1, y + 1)

          // The sheet must hold the tile: compare a grid of points.
          const want = tile.data
          const got = g.getImageData(Math.round(x) + 1, y + 1, w, h).data
          let differ = 0
          let undrawn = 0
          const N = 24
          for (let gy = 0; gy < N; gy++) {
            for (let gx = 0; gx < N; gx++) {
              const px = Math.min(w - 1, Math.floor(((gx + 0.5) * w) / N))
              const py = Math.min(h - 1, Math.floor(((gy + 0.5) * h) / N))
              const o = (py * w + px) * 4
              if (want[o + 3]! < 255) undrawn++
              if (got[o] !== want[o] || got[o + 1] !== want[o + 1] || got[o + 2] !== want[o + 2]) differ++
            }
          }
          const s = shots[i]!
          if (img.naturalWidth === 0 || img.naturalHeight === 0) problems.push(`s${s.n}: its screenshot did not decode`)
          else if (undrawn > 0) problems.push(`s${s.n}: ${undrawn} of ${N * N} sampled points of its screenshot were not drawn`)
          else if (differ > 0) problems.push(`s${s.n}: ${differ} of ${N * N} sampled points differ from its screenshot`)

          g.font = '600 13px -apple-system, system-ui, sans-serif'
          g.fillStyle = '#fff'
          const tag = `s${s.n}`
          g.fillText(tag, x, y + h + 2 + 6)
          const tagW = g.measureText(tag).width
          g.font = '13px -apple-system, system-ui, sans-serif'
          g.fillStyle = '#aaa'
          g.fillText(s.label, x + tagW + 6, y + h + 2 + 6, tileW - tagW - 6)
        })
        return { png: canvas.toDataURL('image/png'), problems }
      },
      { title: sheet.title, shots, L: SHEET },
    )
    if (result.problems.length > 0) throw new Error(`contact sheet ${sheet.out}: ${result.problems.join('; ')}`)
    writeFileSync(sheet.out, Buffer.from(result.png.slice(result.png.indexOf(',') + 1), 'base64'))
  } finally {
    await page.close()
  }
}
