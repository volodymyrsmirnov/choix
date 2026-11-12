// Static server for the app's parity build (the app on the mock bindings).
//
//   node e2e/parity/serve-app.mjs [--port N]     default 8801; --port 0 picks a free port
//
// Serves, on 127.0.0.1 only:
//   /mock/photos/<name>   frontend/prototype/mock/photos/ — the prototype's
//                         mock photos, byte for byte (fixtures.ts points the
//                         fixtures' photo URLs here);
//   /media/thumb|full/…   a member's thumbnail and full-size image, as the app
//                         asks Go's media handler for them
//                         (src/app/shell/media.ts: …?v=<content hash>): the
//                         bytes of the prototype photo that member shows,
//                         looked up by `v` in the manifest
//                         `bun e2e/parity/fixtures/media.ts` prints; 404 for
//                         a hash the manifest does not have;
//   everything else       frontend/.design/dist-parity/ (`bun run build:parity`),
//                         `/` being its index.html.
// Prints `serving http://127.0.0.1:<port>` once listening; exits 1 when the
// build is missing or the port is taken (it never takes over another
// server's port).
import { spawnSync } from 'node:child_process'
import { createReadStream, existsSync, realpathSync, statSync } from 'node:fs'
import { createServer } from 'node:http'
import { dirname, extname, join, normalize, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const FRONTEND = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
export const DIST = join(FRONTEND, '.design', 'dist-parity')
export const PHOTOS = join(FRONTEND, 'prototype', 'mock', 'photos')
const PHOTOS_PATH = '/mock/photos/'
const MEDIA_PATHS = ['/media/thumb/', '/media/full/']
export const MEDIA_SCRIPT = join(FRONTEND, 'e2e', 'parity', 'fixtures', 'media.ts')

/**
 * The /media/ manifest (content hash → photo file name), from
 * `bun fixtures/media.ts`: the prototype's mock data is evaluated by bun only
 * (see fixtures.ts). Throws when bun fails.
 * @returns {Record<string, string>}
 */
export function loadMedia(script = MEDIA_SCRIPT) {
  const r = spawnSync('bun', [script], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
  if (r.error || r.status !== 0) throw new Error(`\`bun ${script}\` failed (${r.error?.message ?? `exit ${r.status}`}):\n${r.stderr ?? ''}`)
  return JSON.parse(r.stdout)
}

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.webp': 'image/webp',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ico': 'image/x-icon',
  '.map': 'application/json; charset=utf-8',
}

function send(res, status, body) {
  res.writeHead(status, { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'no-store' })
  res.end(body)
}

/** `rel` inside `root`, or null when it would leave it. */
function inside(root, rel) {
  const file = normalize(join(root, rel))
  return file === root || file.startsWith(root + sep) ? file : null
}

/** Starts the server; resolves with the listening http.Server. */
export function serve({ port = 8801, dist = DIST, photos = PHOTOS, media = {} } = {}) {
  const server = createServer((req, res) => {
    if (req.method !== 'GET' && req.method !== 'HEAD') return send(res, 405, 'method not allowed')
    let path
    let url
    try {
      url = new URL(req.url ?? '/', 'http://127.0.0.1')
      path = decodeURIComponent(url.pathname)
    } catch {
      return send(res, 400, 'bad request')
    }
    let file
    if (MEDIA_PATHS.some((p) => path.startsWith(p))) {
      const hash = url.searchParams.get('v') ?? ''
      const name = Object.hasOwn(media, hash) ? media[hash] : undefined
      if (name === undefined) return send(res, 404, 'not found')
      file = inside(photos, name)
    } else if (path.startsWith(PHOTOS_PATH)) {
      file = inside(photos, path.slice(PHOTOS_PATH.length))
    } else {
      if (path.endsWith('/')) path += 'index.html'
      file = inside(dist, path)
    }
    if (file === null) return send(res, 403, 'forbidden')
    if (!existsSync(file) || !statSync(file).isFile()) return send(res, 404, 'not found')
    res.writeHead(200, {
      'Content-Type': TYPES[extname(file).toLowerCase()] ?? 'application/octet-stream',
      'Cache-Control': 'no-store',
    })
    if (req.method === 'HEAD') return res.end()
    createReadStream(file).pipe(res)
  })
  return new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(port, '127.0.0.1', () => resolve(server))
  })
}

function parsePort(argv) {
  const i = argv.indexOf('--port')
  if (i === -1) return 8801
  const port = Number(argv[i + 1])
  if (!Number.isInteger(port) || port < 0 || port > 65535) {
    console.error(`serve-app: invalid --port ${argv[i + 1] ?? ''}`)
    process.exit(2)
  }
  return port
}

if (process.argv[1] && fileURLToPath(import.meta.url) === realpathSync(process.argv[1])) {
  if (!existsSync(join(DIST, 'index.html'))) {
    console.error('serve-app: .design/dist-parity/index.html is missing; run `bun run build:parity` first')
    process.exit(1)
  }
  const wanted = parsePort(process.argv.slice(2))
  let media
  try {
    media = loadMedia()
  } catch (err) {
    console.error(`serve-app: cannot read the /media/ manifest: ${err.message}`)
    process.exit(1)
  }
  const server = await serve({ port: wanted, media }).catch((err) => {
    console.error(`serve-app: cannot listen on 127.0.0.1:${wanted}: ${err.code ?? err.message}`)
    process.exit(1)
  })
  const { port } = server.address()
  console.log(`serving http://127.0.0.1:${port}`)
  // Kept-alive browser connections would hold close() open: drop them too.
  for (const signal of ['SIGINT', 'SIGTERM']) {
    process.on(signal, () => {
      server.close(() => process.exit(0))
      server.closeAllConnections()
    })
  }
}
