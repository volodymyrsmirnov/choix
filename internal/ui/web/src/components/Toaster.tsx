import React, { useEffect, useState, useCallback } from 'react'
import { XIcon } from './primitives'

export type ToastTone = 'ok' | 'reject' | 'warn' | 'info'

export interface Toast {
  id: number
  tone: ToastTone
  title: string
  body?: string
  action?: { label: string; onClick: () => void }
}

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
      <button className="btn btn-ghost h-[22px] text-[11px] px-2" onClick={t.action.onClick}>{t.action.label}</button>
    )}
    <button className="btn btn-ghost h-[22px] px-1 text-fg-3" onClick={() => onDismiss(t.id)}><XIcon /></button>
  </div>
)

type ToastInput = Omit<Toast, 'id' | 'tone'> & { tone?: ToastTone }
let _push: ((t: ToastInput) => void) | null = null
let _next = 1
export const toast = {
  ok:     (title: string, opts?: Omit<ToastInput, 'title' | 'tone'>) => _push?.({ title, tone: 'ok',     ...opts }),
  reject: (title: string, opts?: Omit<ToastInput, 'title' | 'tone'>) => _push?.({ title, tone: 'reject', ...opts }),
  warn:   (title: string, opts?: Omit<ToastInput, 'title' | 'tone'>) => _push?.({ title, tone: 'warn',   ...opts }),
  info:   (title: string, opts?: Omit<ToastInput, 'title' | 'tone'>) => _push?.({ title, tone: 'info',   ...opts }),
}

export const Toaster: React.FC = () => {
  const [items, setItems] = useState<Toast[]>([])
  const dismiss = useCallback((id: number) => setItems(xs => xs.filter(x => x.id !== id)), [])
  useEffect(() => {
    _push = (t) => {
      const id = _next++
      setItems(xs => [...xs, { id, tone: 'info', ...t }])
      setTimeout(() => setItems(xs => xs.filter(x => x.id !== id)), 4000)
    }
    return () => { _push = null }
  }, [])
  return (
    <div className="fixed bottom-4 right-4 flex flex-col gap-2.5 items-end z-50 pointer-events-none">
      {items.map(t => (
        <div key={t.id} className="pointer-events-auto">
          <ToastView t={t} onDismiss={dismiss} />
        </div>
      ))}
    </div>
  )
}
