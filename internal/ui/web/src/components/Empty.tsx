import React from 'react'
import { AppBar, FolderIcon, CheckIcon, Spinner } from './primitives'
import type { ProgressEvent } from '../api'

const STAGE_LABELS: Record<string, string> = {
  discover: 'Discover files',
  metadata: 'Read EXIF',
  thumb: 'Generate thumbs',
  analyze: 'Compute embeddings',
  cluster: 'Cluster',
}
const STAGE_ORDER = ['discover', 'metadata', 'thumb', 'analyze', 'cluster']

export const Empty: React.FC<{
  folder: string
  mode: 'scanning' | 'empty'
  progress?: ProgressEvent | null
  onSettings?: () => void
  onLibrary?: () => void
}> = ({ folder, mode, progress, onSettings, onLibrary }) => {
  const activeStage = progress?.stage ?? null
  const activeIdx = activeStage ? STAGE_ORDER.indexOf(activeStage) : -1
  const total = progress?.total ?? 0
  const done = progress?.done ?? 0
  const pct = total > 0 ? Math.round((done / total) * 100) : 0

  return (
    <div className="flex flex-col h-screen overflow-hidden bg-bg-1">
      <AppBar folder={folder} picked={0} total={total} onSettings={onSettings} onLibrary={onLibrary} />

      <div className="flex-1 flex items-center justify-center">
        <div className="max-w-[480px] text-center p-10">
          <div className="relative w-[88px] h-[88px] mx-auto mb-6">
            <div className="absolute inset-0 rounded-xl border border-line-2 bg-bg-2" style={{ transform: 'rotate(-8deg)' }} />
            <div className="absolute inset-0 rounded-xl border border-line-2 bg-bg-2" style={{ transform: 'rotate(4deg)' }} />
            <div className="absolute inset-0 rounded-xl border border-pick flex items-center justify-center text-pick text-3xl"
                 style={{
                   background: 'linear-gradient(135deg, oklch(0.30 0.04 145), oklch(0.25 0.03 230))',
                   boxShadow: '0 0 32px oklch(0.72 0.18 145 / 0.25)',
                 }}>
              {mode === 'scanning' ? <Spinner /> : '◇'}
            </div>
          </div>

          <h1 className="text-[22px] font-semibold m-0 tracking-tight">
            {mode === 'scanning' ? 'Scanning your folder' : 'No photos in this folder'}
          </h1>
          <p className="text-fg-2 text-[13px] mt-2 mb-6 leading-[1.6]">
            {mode === 'scanning'
              ? 'Reading EXIF, generating thumbnails, and grouping bursts. You can browse as soon as the first cluster lands.'
              : 'Drop a folder of photos or RAW files here, or pick one to get started.'}
          </p>

          {mode === 'scanning' && (
            <div className="p-3.5 bg-bg-2 border border-line-1 rounded-lg text-left flex flex-col gap-2.5">
              <div className="flex items-center gap-2.5">
                <span className="font-mono text-[11px] text-fg-2 flex-1">
                  {progress
                    ? `${STAGE_LABELS[progress.stage] ?? progress.stage}: ${done.toLocaleString()} / ${total.toLocaleString()}`
                    : 'Starting…'}
                </span>
                <span className="font-mono text-[11px] text-fg-1">{pct}%</span>
              </div>
              <div className="progress-rail h-1">
                <div className="fill" data-indet={total === 0 ? 'true' : undefined} style={{ width: total > 0 ? `${pct}%` : undefined }} />
              </div>
              <div className="flex flex-col gap-1">
                {STAGE_ORDER.map((stage, i) => {
                  const isDone = activeIdx >= 0 && i < activeIdx
                  const isActive = stage === activeStage
                  const tone = isDone ? 'oklch(0.72 0.18 145)' : isActive ? 'oklch(0.97 0.003 240)' : 'oklch(0.48 0.006 240)'
                  return (
                    <div key={stage} className="flex items-center gap-2 text-[11px]">
                      <span className="w-3.5 h-3.5 flex items-center justify-center" style={{ color: tone }}>
                        {isDone ? <CheckIcon /> : isActive ? <Spinner small /> : '·'}
                      </span>
                      <span className={`flex-1 ${isDone ? 'text-fg-2' : isActive ? 'text-fg-0' : 'text-fg-3'}`}>
                        {STAGE_LABELS[stage]}
                      </span>
                      {isActive && progress && (
                        <span className="font-mono text-fg-3 text-[10px]">{done} / {total}</span>
                      )}
                    </div>
                  )
                })}
              </div>
            </div>
          )}

          {mode !== 'scanning' && (
            <div className="flex gap-2 justify-center">
              <button className="btn btn-primary"><FolderIcon /> Choose folder…</button>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
