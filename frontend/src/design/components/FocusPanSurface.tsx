import { useEffect, useRef, type ReactNode } from 'react'
import type { FocusView } from '../types'

/** A press that moves less than this before release is a click, not a pan. */
const CLICK_SLOP_PX = 4

/**
 * The pointer surface of the hero or of one compare pane (v1): drag to pan
 * while zoomed, ⌘-wheel / trackpad pinch / Safari gesture to zoom, and, for a
 * pane, a click (a press that barely moved) to make it active. Wheel and
 * gesture events are taken only here, so the page itself never zooms. Only
 * the drag origin is kept, in a ref; zoom and pan are the caller's props.
 */
export function FocusPanSurface({
  view,
  zoomable,
  onView,
  zoomBy,
  onClick,
  className,
  children,
  testId,
  active,
}: {
  view: FocusView
  /** False for a video: no pan, no zoom. */
  zoomable: boolean
  onView: (v: FocusView) => void
  /** `v` zoomed by `factor`, clamped (logic.ts zoomBy). */
  zoomBy: (v: FocusView, factor: number) => FocusView
  onClick?: () => void
  className?: string
  children: ReactNode
  testId?: string
  /** A compare pane: whether it is the active one (data-active). */
  active?: boolean
}) {
  const el = useRef<HTMLDivElement | null>(null)
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null)
  const down = useRef<{ x: number; y: number } | null>(null)
  const latest = useRef({ view, zoomable, onView, zoomBy })
  latest.current = { view, zoomable, onView, zoomBy }

  useEffect(() => {
    const node = el.current
    if (!node) return
    let base = latest.current.view
    const wheel = (e: WheelEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return
      e.preventDefault()
      const l = latest.current
      // v1: a pinch tick or ⌘-wheel step scales by exp(−deltaY × 0.01).
      if (l.zoomable) l.onView(l.zoomBy(l.view, Math.exp(-e.deltaY * 0.01)))
    }
    const start = (e: Event) => {
      e.preventDefault()
      base = latest.current.view
    }
    const change = (e: Event) => {
      e.preventDefault()
      const l = latest.current
      const scale = (e as unknown as { scale: number }).scale
      if (!l.zoomable || !isFinite(scale) || scale <= 0) return
      l.onView(l.zoomBy(base, scale))
    }
    const end = (e: Event) => e.preventDefault()
    node.addEventListener('wheel', wheel, { passive: false })
    node.addEventListener('gesturestart', start)
    node.addEventListener('gesturechange', change)
    node.addEventListener('gestureend', end)
    return () => {
      node.removeEventListener('wheel', wheel)
      node.removeEventListener('gesturestart', start)
      node.removeEventListener('gesturechange', change)
      node.removeEventListener('gestureend', end)
    }
  }, [])

  const panning = zoomable && view.zoom > 1
  const cursor = panning ? 'cursor-grab active:cursor-grabbing' : onClick && !active ? 'cursor-pointer' : 'cursor-default'
  return (
    <div
      ref={el}
      className={`group select-none ${cursor} ${className ?? ''}`}
      data-testid={testId}
      data-active={active === undefined ? undefined : active}
      // A zoomed photo overflows its box by design.
      data-clip="intended"
      onPointerDown={(e) => {
        if (e.button !== 0) return
        down.current = { x: e.clientX, y: e.clientY }
        if (!panning) return
        drag.current = { x: e.clientX, y: e.clientY, px: view.pan.x, py: view.pan.y }
        e.currentTarget.setPointerCapture?.(e.pointerId)
      }}
      onPointerMove={(e) => {
        const d = drag.current
        if (!d) return
        onView({ zoom: view.zoom, pan: { x: d.px + (e.clientX - d.x), y: d.py + (e.clientY - d.y) } })
      }}
      onPointerUp={(e) => {
        const d = down.current
        down.current = null
        drag.current = null
        e.currentTarget.releasePointerCapture?.(e.pointerId)
        if (onClick && d && Math.hypot(e.clientX - d.x, e.clientY - d.y) < CLICK_SLOP_PX) onClick()
      }}
      onPointerCancel={() => {
        down.current = null
        drag.current = null
      }}
    >
      {children}
    </div>
  )
}
