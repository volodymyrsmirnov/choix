import { describe, expect, test } from 'bun:test'
import { dropSource, keepScroll } from './windowSources'

test('no online source: nothing is downloaded, so the Library does not follow the network', async () => {
  expect(Object.keys(await import('./windowSources')).sort()).toEqual(['dropSource', 'keepScroll'])
})

describe('dropSource', () => {
  test('follows the class Wails puts on the drop target while files are dragged over the window', () => {
    const classes = new Set<string>()
    const target = { classList: { contains: (c: string) => classes.has(c) } }
    let observed = null as { el: unknown; opts: unknown } | null
    let callback: (() => void) | null = null
    let disconnected = false
    class FakeObserver {
      constructor(cb: () => void) {
        callback = cb
      }
      observe(el: unknown, opts: unknown) {
        observed = { el, opts }
      }
      disconnect() {
        disconnected = true
      }
    }
    const doc = { querySelector: (sel: string) => (sel === '[data-file-drop-target]' ? target : null) }
    const src = dropSource(doc, FakeObserver)
    let changes = 0
    const stop = src.subscribe(() => changes++)
    expect(observed).toEqual({ el: target, opts: { attributes: true, attributeFilter: ['class'] } })
    expect(src.get()).toBe(false)

    classes.add('file-drop-target-active')
    callback!()
    expect(src.get()).toBe(true)
    classes.delete('file-drop-target-active')
    callback!()
    expect(src.get()).toBe(false)
    expect(changes).toBe(2)

    stop()
    expect(disconnected).toBe(true)
  })

  test('without a drop target nothing is ever active', () => {
    const src = dropSource({ querySelector: () => null }, class {
      observe() {}
      disconnect() {}
    })
    const stop = src.subscribe(() => {})
    expect(src.get()).toBe(false)
    stop()
  })
})

describe('keepScroll', () => {
  test('restores the saved offset and saves it on every scroll', () => {
    const saved: number[] = []
    let onScroll: (() => void) | null = null
    const el = {
      scrollTop: 0,
      addEventListener: (_t: string, fn: () => void) => {
        onScroll = fn
      },
      removeEventListener: () => {
        onScroll = null
      },
    }
    const stop = keepScroll(el, { offset: () => 640, save: (top) => saved.push(top) })
    expect(el.scrollTop).toBe(640)
    el.scrollTop = 900
    onScroll!()
    expect(saved).toEqual([900])
    stop()
    expect(onScroll).toBeNull()
  })
})
