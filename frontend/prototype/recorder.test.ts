import { expect, test } from 'bun:test'
import { createOverlayLog, FrameRecorder } from './recorder'

test('records register, open and close once each; top is the newest open overlay', () => {
  const log = createOverlayLog()
  const rec = new FrameRecorder(log)
  rec.register('a')
  rec.register('a')
  rec.register('b')
  rec.open('a')
  rec.open('b')
  expect(rec.top()).toBe('b')
  rec.close('b')
  expect(rec.top()).toBe('a')
  expect(log).toEqual({ registered: ['a', 'b'], opened: ['a', 'b'], closed: ['b'] })
})

test('a dead frame records nothing (an unmount is not a close)', () => {
  const log = createOverlayLog()
  const rec = new FrameRecorder(log)
  rec.open('menu')
  rec.dead = true
  rec.close('menu')
  rec.register('other')
  expect(log).toEqual({ registered: [], opened: ['menu'], closed: [] })
})
