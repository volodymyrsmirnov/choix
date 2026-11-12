// run.mjs (`task parity`): its options, the busy-port check that keeps it
// from touching a server it did not start, and the start / stop of a server.
import { expect, test } from 'bun:test'
import { createServer } from 'node:net'
import { parseArgs, portBusy, startServer, stop } from './run.mjs'

test('ports default to 8801 (app) and 8811 (prototype); Playwright arguments follow --', () => {
  expect(parseArgs([])).toEqual({ appPort: 8801, protoPort: 8811, playwright: [] })
  expect(parseArgs(['--app-port', '9001', '--proto-port', '9011', '--', '--grep', 'overlays'])).toEqual({
    appPort: 9001,
    protoPort: 9011,
    playwright: ['--grep', 'overlays'],
  })
  expect(parseArgs(['--', '--app-port', 'x'])).toEqual({ appPort: 8801, protoPort: 8811, playwright: ['--app-port', 'x'] })
})

test('a bad option, a bad port or one port for both is refused', () => {
  expect(() => parseArgs(['--grep', 'overlays'])).toThrow('unknown option --grep')
  expect(() => parseArgs(['--app-port', '0'])).toThrow('invalid --app-port 0')
  expect(() => parseArgs(['--proto-port'])).toThrow('invalid --proto-port')
  expect(() => parseArgs(['--app-port', '8811'])).toThrow('two ports')
})

test('portBusy sees a listener on 127.0.0.1 and nothing once it is gone', async () => {
  const server = createServer()
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const port = (server.address() as { port: number }).port
  expect(await portBusy(port)).toBe(true)
  await new Promise((resolve) => server.close(resolve))
  expect(await portBusy(port)).toBe(false)
})

test('portBusy also sees a listener on the IPv6 loopback only (a Vite dev server on localhost)', async () => {
  const server = createServer()
  const ok = await new Promise<boolean>((resolve) => {
    server.once('error', () => resolve(false))
    server.listen(0, '::1', () => resolve(true))
  })
  if (!ok) return // no IPv6 loopback on this machine
  const port = (server.address() as { port: number }).port
  expect(await portBusy(port)).toBe(true)
  await new Promise((resolve) => server.close(resolve))
})

test('a server is ready once it says it serves on its port, and stop ends it', async () => {
  const s = startServer('a server', ['-e', 'console.log("serving http://127.0.0.1:18801"); setInterval(() => {}, 1000)'], 18801)
  await s.ready
  await stop(s.child)
  expect(s.child.exitCode !== null || s.child.signalCode !== null).toBe(true)
})

test('a server that exits before serving fails with its output', async () => {
  const s = startServer('a server', ['-e', 'console.error("cannot listen"); process.exit(1)'], 18801)
  await expect(s.ready).rejects.toThrow(/a server exited \(1\) before serving:\s+cannot listen/)
})
