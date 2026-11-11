import React from 'react'
import { AppBar, CheckIcon, Spinner, STAGE_ORDER, stageLabel } from './primitives'
import type { ProgressEvent } from '../api'

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
      {/* No picked/total meter: nothing is pickable until the first scan has
          produced clusters, and the progress total (files in the current
          stage's queue) is not the library total the meter is about. The
          bar's scan indicator is off too — the panel below shows the same
          progress in full. */}
      <AppBar folder={folder} picked={0} total={0} onSettings={onSettings} onLibrary={onLibrary}
              showMeter={false} showScanProgress={false} />

      <main className="flex-1 flex items-center justify-center">
        <div className="max-w-[480px] text-center p-10">
          <div className="relative w-[88px] h-[88px] mx-auto mb-6" aria-hidden="true">
            <div className="absolute inset-0 rounded-xl border border-line-2 bg-bg-2" style={{ transform: 'rotate(-8deg)' }} />
            <div className="absolute inset-0 rounded-xl border border-line-2 bg-bg-2" style={{ transform: 'rotate(4deg)' }} />
            <div className="absolute inset-0 rounded-xl border border-pick flex items-center justify-center text-pick text-3xl"
                 style={{
                   background: 'linear-gradient(135deg, oklch(0.30 0.04 145), oklch(0.25 0.03 230))',
                   boxShadow: '0 0 32px oklch(0.72 0.18 145 / 0.25)',
                 }}>
              {mode === 'scanning' ? <Spinner decorative /> : '◇'}
            </div>
          </div>

          <h1 className="text-[22px] font-semibold m-0 tracking-tight">
            {mode === 'scanning' ? 'Scanning your folder' : 'No photos or videos found'}
          </h1>
          {mode === 'scanning' ? (
            // Clusters are only written by the final stage, after every file
            // has been through the earlier ones — so nothing is browsable
            // until the whole scan finishes; promise exactly that.
            <p className="text-fg-2 text-[13px] mt-2 mb-6 leading-[1.6]">
              Reading EXIF, generating thumbnails and grouping similar shots.
              The library opens automatically when the scan finishes.
            </p>
          ) : (
            // choix has no folder picker or drop target: the folder is the
            // command-line argument, and it is (re)scanned on every launch.
            <p className="text-fg-2 text-[13px] mt-2 mb-0 leading-[1.6]">
              choix scanned <span className="text-fg-0 font-medium">{folder}</span> and found no supported
              photos or videos in it. Add some, then run <code className="font-mono text-fg-1">choix</code> on
              this folder again — it rescans on every launch.
            </p>
          )}

          {mode === 'scanning' && (
            <div className="p-3.5 bg-bg-2 border border-line-1 rounded-lg text-left flex flex-col gap-2.5">
              <div className="flex items-center gap-2.5">
                {/* The stage name is the live region; the counters tick per
                    file and would flood a screen reader. */}
                <span className="font-mono text-[11px] text-fg-2 flex-1">
                  <span role="status" aria-live="polite">
                    {progress ? stageLabel(progress.stage) : 'Starting…'}
                  </span>
                  {progress && total > 0 && `: ${done.toLocaleString()} / ${total.toLocaleString()}`}
                </span>
                {total > 0 && <span className="font-mono text-[11px] text-fg-1">{pct}%</span>}
              </div>
              <div className="progress-rail h-1" role="progressbar" aria-label="Scan progress"
                   aria-valuemin={0}
                   aria-valuemax={total > 0 ? 100 : undefined}
                   aria-valuenow={total > 0 ? pct : undefined}>
                <div className="fill" data-indet={total === 0 ? 'true' : undefined} style={{ width: total > 0 ? `${pct}%` : undefined }} />
              </div>
              <ol className="list-none m-0 p-0 flex flex-col gap-1">
                {STAGE_ORDER.map((stage, i) => {
                  const isDone = activeIdx >= 0 && i < activeIdx
                  const isActive = stage === activeStage
                  const tone = isDone ? 'text-pick' : isActive ? 'text-fg-0' : 'text-fg-3'
                  const status = isDone ? 'done' : isActive ? 'in progress' : 'pending'
                  return (
                    <li key={stage} className="flex items-center gap-2 text-[11px]"
                        aria-current={isActive ? 'step' : undefined}>
                      <span className={`w-3.5 h-3.5 flex items-center justify-center ${tone}`} aria-hidden="true">
                        {isDone ? <CheckIcon /> : isActive ? <Spinner small decorative /> : '·'}
                      </span>
                      <span className={`flex-1 ${isDone ? 'text-fg-2' : isActive ? 'text-fg-0' : 'text-fg-3'}`}>
                        {stageLabel(stage)}
                        <span className="sr-only"> ({status})</span>
                      </span>
                      {isActive && progress && total > 0 && (
                        <span className="font-mono text-fg-3 text-[11px]" aria-hidden="true">{done} / {total}</span>
                      )}
                    </li>
                  )
                })}
              </ol>
            </div>
          )}
        </div>
      </main>
    </div>
  )
}
