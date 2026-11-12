import { describe, expect, test } from 'bun:test'
import type { Member } from '../../design/types'
import { mediaFor } from './media'

const ID = '0123456789abcdef'
const member = (path: string, content_hash: string): Member => ({
  file_id: 1,
  path,
  content_hash,
  picked: false,
  rejected: false,
  kind: 'photo',
  format: 'jpeg',
})

// The same vectors as internal/app/media/urls_test.go, so the URLs the
// frontend builds are byte for byte the ones Go builds (FolderSummary.previews).
describe('mediaFor', () => {
  test('builds the Go media URLs', () => {
    const m = mediaFor(ID)
    expect(m.thumb(member('a b/ü#1.jpg', 'h'))).toBe(`/media/thumb/${ID}/a%20b/%C3%BC%231.jpg?tier=thumb&v=h`)
    expect(m.thumb(member('Day1/A.JPG', 'abc'), 'preview')).toBe(`/media/thumb/${ID}/Day1/A.JPG?tier=preview&v=abc`)
    expect(m.thumb(member('A.JPG', ''))).toBe(`/media/thumb/${ID}/A.JPG?tier=thumb`)
    expect(m.full(member('Day 1/clip.mov', 'h1'))).toBe(`/media/full/${ID}/Day%201/clip.mov?v=h1`)
    expect(m.full(member('clip.mov', ''))).toBe(`/media/full/${ID}/clip.mov`)
    expect(m.full(member('Trip ?/100%/a&b.jpg', 'x y'))).toBe(`/media/full/${ID}/Trip%20%3F/100%25/a&b.jpg?v=x+y`)
  })

  test('decodes back to the relative path', () => {
    for (const rel of ['a b/ü#1.jpg', 'Trip ?/100%/a&b+c.jpg', "x;y/z:w@v (1)!*'.HEIC", 'plain.jpg']) {
      const u = new URL(mediaFor(ID).full(member(rel, 'h')), 'http://localhost')
      expect(decodeURIComponent(u.pathname)).toBe(`/media/full/${ID}/${rel}`)
      expect(u.searchParams.get('v')).toBe('h')
    }
  })
})
