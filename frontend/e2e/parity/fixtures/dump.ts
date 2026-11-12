// `bun e2e/parity/fixtures/dump.ts`: the fixture table (table.ts) as JSON on
// stdout, for fixtures.ts on Node (Playwright), which cannot import the
// prototype's mock data itself.
import { TABLE } from './table'

process.stdout.write(JSON.stringify(TABLE))
