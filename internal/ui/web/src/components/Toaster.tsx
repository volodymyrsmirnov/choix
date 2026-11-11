import React, { useEffect, useState, useCallback, useRef } from 'react'
import { XIcon } from './primitives'

export type ToastTone = 'ok' | 'reject' | 'warn' | 'info'

export interface Toast {
  id: number
  tone: ToastTone
  title: string
  body?: string
  // hint renders as a .kbd chip inside the action button (e.g. "⌘Z").
  action?: { label: string; onClick: () => void; hint?: string }
  // key makes a toast self-replacing: pushing another toast with the same key
  // swaps it in place (and restarts its timer) instead of stacking a new one.
  // Used for high-frequency feedback like Focus's pick/reject "Undo" toast.
  key?: string
  // duration overrides the default auto-dismiss delay (ms).
  duration?: number
}

const DEFAULT_DURATION = 4000

const TONE_COLOR: Record<ToastTone, string> = {
  ok: 'oklch(0.72 0.18 145)',
  reject: 'oklch(0.66 0.20 25)',
  warn: 'oklch(0.85 0.14 90)',
  info: 'oklch(0.72 0.13 235)',
}

const ToastView: React.FC<{ t: Toast; onDismiss: (id: number) => void }> = ({ t, onDismiss }) => (
  <div className="w-[360px] rounded-lg border border-line-2 backdrop-blur-md shadow-pop p-2.5 flex items-center gap-2.5"
       style={{ background: 'oklch(0.22 0.005 240 / 0.96)' }}>
    <div className="w-[3px] self-stretch rounded-full" style={{ background: TONE_COLOR[t.tone] }} />
    <div className="flex-1 min-w-0">
      <div className="text-xs font-semibold text-fg-0 leading-tight">{t.title}</div>
      {t.body && <div className="text-[11px] text-fg-2 mt-0.5 leading-snug">{t.body}</div>}
    </div>
    {t.action && (
      <button className="btn btn-ghost h-[22px] text-[11px] px-2" onClick={t.action.onClick}>
        {t.action.label}
        {t.action.hint && <span className="kbd">{t.action.hint}</span>}
      </button>
    )}
    <button className="btn btn-ghost h-[22px] px-1 text-fg-3" aria-label="Dismiss" onClick={() => onDismiss(t.id)}><XIcon /></button>
  </div>
)

type ToastInput = Omit<Toast, 'id' | 'tone'> & { tone?: ToastTone }
type ToastOpts = Omit<ToastInput, 'title' | 'tone'>
let _push: ((t: ToastInput) => void) | null = null
let _next = 1
export const toast = {
  ok:     (title: string, opts?: ToastOpts) => _push?.({ title, tone: 'ok',     ...opts }),
  reject: (title: string, opts?: ToastOpts) => _push?.({ title, tone: 'reject', ...opts }),
  warn:   (title: string, opts?: ToastOpts) => _push?.({ title, tone: 'warn',   ...opts }),
  info:   (title: string, opts?: ToastOpts) => _push?.({ title, tone: 'info',   ...opts }),
}

export const Toaster: React.FC = () => {
  const [items, setItems] = useState<Toast[]>([])
  // Auto-dismiss timers by toast id, plus the id currently holding each key.
  // A keyed replacement gets a fresh id, so even if the old timer fired it
  // would filter out an id that no longer exists — but we also clear it so a
  // burst of replacements doesn't leave dozens of dead timers queued.
  const timers = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map())
  const keyedIDs = useRef<Map<string, number>>(new Map())

  const forget = useCallback((id: number) => {
    const tm = timers.current.get(id)
    if (tm !== undefined) clearTimeout(tm)
    timers.current.delete(id)
    for (const [k, v] of keyedIDs.current) if (v === id) keyedIDs.current.delete(k)
  }, [])

  const dismiss = useCallback((id: number) => {
    forget(id)
    setItems(xs => xs.filter(x => x.id !== id))
  }, [forget])

  useEffect(() => {
    const timerMap = timers.current
    _push = (t) => {
      const id = _next++
      const item: Toast = { id, tone: 'info', ...t }
      if (t.key !== undefined) {
        const prevID = keyedIDs.current.get(t.key)
        if (prevID !== undefined) forget(prevID)
        keyedIDs.current.set(t.key, id)
      }
      setItems(xs => {
        const i = t.key !== undefined ? xs.findIndex(x => x.key === t.key) : -1
        if (i < 0) return [...xs, item]
        const next = xs.slice()
        next[i] = item
        return next
      })
      timerMap.set(id, setTimeout(() => dismiss(id), t.duration ?? DEFAULT_DURATION))
    }
    return () => {
      _push = null
      for (const tm of timerMap.values()) clearTimeout(tm)
      timerMap.clear()
    }
  }, [dismiss, forget])

  return (
    <div className="fixed bottom-4 right-4 flex flex-col gap-2.5 items-end z-50 pointer-events-none"
         role="status" aria-live="polite">
      {items.map(t => (
        // Keyed toasts keep a stable React key across replacements so the
        // element updates in place rather than unmounting and re-mounting.
        <div key={t.key !== undefined ? `k:${t.key}` : `i:${t.id}`} className="pointer-events-auto">
          <ToastView t={t} onDismiss={dismiss} />
        </div>
      ))}
    </div>
  )
}
