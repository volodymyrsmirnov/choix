// `bun e2e/parity/fixtures/media.ts`: the /media/ manifest (table.ts MEDIA,
// content hash → prototype photo file name) as JSON on stdout, for
// serve-app.mjs on Node, which cannot import the prototype's mock data
// itself. The app builds a member's thumbnail and full-size URLs as Go does
// (src/app/shell/media.ts: /media/thumb|full/<folder>/<path>?…&v=<content
// hash>); serve-app.mjs answers each with the bytes of the photo the
// prototype shows for that member.
import { MEDIA } from './table'

process.stdout.write(JSON.stringify(MEDIA))
