// Static server for the built prototype.
//
//   node tools/serve.mjs [--port N]     default 8811; --port 0 picks a free port
//
// Serves frontend/dist-prototype/ on 127.0.0.1 and answers /__build.json with
// { "sourceDigest": string } from dist-prototype/build.json ("" when absent).
// Prints `serving http://127.0.0.1:<port>` once listening.
import { createReadStream, existsSync, readFileSync, realpathSync, statSync } from 'node:fs'
import { createServer } from 'node:http'
import { dirname, extname, join, normalize, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIST = join(dirname(fileURLToPath(import.meta.url)), '..', 'dist-prototype')

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
  '.woff2': 'font/woff2',
  '.ico': 'image/x-icon',
  '.map': 'application/json; charset=utf-8',
}

function sourceDigest(root) {
  try {
    const build = JSON.parse(readFileSync(join(root, 'build.json'), 'utf8'))
    return typeof build.sourceDigest === 'string' ? build.sourceDigest : ''
  } catch {
    return ''
  }
}

function send(res, status, body, type = 'text/plain; charset=utf-8') {
  res.writeHead(status, { 'Content-Type': type, 'Cache-Control': 'no-store' })
  res.end(body)
}

/** Starts the server; resolves with the listening http.Server. */
export function serve({ port = 8811, root = DIST } = {}) {
  const server = createServer((req, res) => {
    if (req.method !== 'GET' && req.method !== 'HEAD') return send(res, 405, 'method not allowed')
    let path
    try {
      path = decodeURIComponent(new URL(req.url ?? '/', 'http://127.0.0.1').pathname)
    } catch {
      return send(res, 400, 'bad request')
    }
    if (path === '/__build.json') {
      return send(res, 200, JSON.stringify({ sourceDigest: sourceDigest(root) }), TYPES['.json'])
    }
    if (path.endsWith('/')) path += 'index.html'
    const file = normalize(join(root, path))
    if (file !== root && !file.startsWith(root + sep)) return send(res, 403, 'forbidden')
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
  if (i === -1) return 8811
  const port = Number(argv[i + 1])
  if (!Number.isInteger(port) || port < 0 || port > 65535) {
    console.error(`serve: invalid --port ${argv[i + 1] ?? ''}`)
    process.exit(2)
  }
  return port
}

if (process.argv[1] && fileURLToPath(import.meta.url) === realpathSync(process.argv[1])) {
  if (!existsSync(join(DIST, 'index.html'))) {
    console.error('serve: dist-prototype/index.html is missing; run `bun run proto:build` first')
    process.exit(1)
  }
  const wanted = parsePort(process.argv.slice(2))
  const server = await serve({ port: wanted }).catch((err) => {
    console.error(`serve: cannot listen on 127.0.0.1:${wanted}: ${err.code ?? err.message}`)
    process.exit(1)
  })
  const { port } = server.address()
  console.log(`serving http://127.0.0.1:${port}`)
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => server.close(() => process.exit(0)))
}
