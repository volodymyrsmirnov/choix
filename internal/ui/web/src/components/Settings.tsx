import React, { useEffect, useState } from 'react'
import { AppBar } from './primitives'
import { fetchSettings, saveSettings, type SettingsBody } from '../api'
import { toast } from './Toaster'

const Section: React.FC<React.PropsWithChildren<{ title: string; hint?: string }>> = ({ title, hint, children }) => (
  <div className="grid grid-cols-[200px_1fr] gap-6 items-start">
    <div>
      <div className="text-[13px] font-medium text-fg-0">{title}</div>
      {hint && <div className="text-[11px] text-fg-2 mt-[3px] leading-[1.5]">{hint}</div>}
    </div>
    <div>{children}</div>
  </div>
)

const Toggle: React.FC<{ id?: string; label: string; checked: boolean; onChange: (v: boolean) => void; testId?: string }> = ({ id, label, checked, onChange, testId }) => (
  <button type="button"
          id={id}
          role="switch"
          aria-checked={checked}
          aria-label={label}
          onClick={() => onChange(!checked)}
          data-testid={testId}
          className={`relative w-9 h-5 rounded-full transition-colors border focus:outline-none focus-visible:ring-2 focus-visible:ring-pick focus-visible:ring-offset-2 focus-visible:ring-offset-bg-1 ${checked ? 'bg-pick border-pick' : 'bg-bg-3 border-line-1'}`}>
    <span className={`absolute top-0.5 ${checked ? 'left-[18px]' : 'left-0.5'} w-4 h-4 rounded-full transition-all ${checked ? 'bg-pick-ink' : 'bg-fg-1'}`} />
  </button>
)

export const Settings: React.FC<{
  folder: string
  totalPicked: number
  totalCount: number
  onSettings: () => void
  onLibrary: () => void
  onSaved: (s: SettingsBody) => Promise<void> | void
}> = ({ folder, totalPicked, totalCount, onSettings, onLibrary, onSaved }) => {
  const [data, setData] = useState<SettingsBody | null>(null)
  const [bucket, setBucket] = useState(600)
  const [sim, setSim] = useState(0.92)
  const [picksDir, setPicksDir] = useState('picks')
  const [advance, setAdvance] = useState(false)
  const [hideRejected, setHideRejected] = useState(false)
  const [crossDevice, setCrossDevice] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    fetchSettings().then(s => {
      setData(s)
      setBucket(s.bucket_size)
      setSim(s.similarity)
      setPicksDir(s.picks_dir)
      setAdvance(!!s.advance_on_action)
      setHideRejected(!!s.hide_rejected_photos)
      setCrossDevice(!!s.cross_device_merging)
    }).catch(e => toast.reject('Load settings failed', { body: String(e) }))
  }, [])

  const dirty = data ? (
    bucket !== data.bucket_size ||
    sim !== data.similarity ||
    picksDir !== data.picks_dir ||
    advance !== !!data.advance_on_action ||
    hideRejected !== !!data.hide_rejected_photos ||
    crossDevice !== !!data.cross_device_merging
  ) : false

  const save = async () => {
    setBusy(true)
    try {
      const r = await saveSettings({
        bucket_size: bucket,
        similarity: sim,
        picks_dir: picksDir,
        advance_on_action: advance,
        hide_rejected_photos: hideRejected,
        cross_device_merging: crossDevice,
      })
      const fresh = await fetchSettings()
      setData(fresh)
      await onSaved(fresh)
      toast.ok('Settings saved', { body: r.reclustered ? 'Re-clustered with new parameters' : undefined })
    } catch (e) {
      toast.reject('Save failed', { body: String(e) })
    } finally {
      setBusy(false)
    }
  }

  const reset = () => {
    if (!data) return
    setBucket(data.bucket_size)
    setSim(data.similarity)
    setPicksDir(data.picks_dir)
    setAdvance(!!data.advance_on_action)
    setHideRejected(!!data.hide_rejected_photos)
    setCrossDevice(!!data.cross_device_merging)
  }

  return (
    <div className="flex flex-col h-screen overflow-hidden bg-bg-1">
      <AppBar folder={folder} picked={totalPicked} total={totalCount} onSettings={onSettings} onLibrary={onLibrary} />

      <div className="flex-1 flex overflow-hidden">
        <aside className="w-[200px] py-5 px-3 border-r border-line-1">
          <div className="eyebrow px-2 mb-2">Settings</div>
          <div className="block w-full text-left px-2 py-1.5 rounded-md text-xs font-medium text-fg-0 bg-bg-3">
            Grouping
          </div>
          <div className="block w-full text-left px-2 py-1.5 rounded-md text-xs font-medium text-fg-1 mt-1">
            Behaviour
          </div>
          <div className="block w-full text-left px-2 py-1.5 rounded-md text-xs font-medium text-fg-1 mt-1">
            Output
          </div>
        </aside>

        <div className="flex-1 overflow-y-auto">
          <div className="max-w-[640px] px-8 pt-8 pb-16 flex flex-col gap-7">
            <div>
              <h1 className="text-[22px] m-0 font-semibold tracking-tight">Grouping</h1>
              <p className="mt-1 text-fg-2 text-[13px]">
                How Choix forms clusters from your folder. Saving a change re-clusters automatically.
              </p>
            </div>

            <Section title="Time bucket" hint="Photos within this window are considered a possible burst.">
              <div className="flex items-center gap-2.5">
                <input className="input font-mono w-24 text-right" type="number" data-testid="bucket-input"
                       min={60} value={bucket} onChange={e => setBucket(+e.target.value)} />
                <span className="text-fg-2 text-xs">seconds</span>
                <span className="flex-1" />
                <span className="font-mono text-[11px] text-fg-3">≈ {(bucket / 60).toFixed(1)} min</span>
              </div>
            </Section>

            <Section title="Visual similarity" hint="Higher = stricter (only near-identical frames cluster).">
              <div className="flex items-center gap-3.5">
                <input type="range" min={0.05} max={1} step={0.01} value={sim} data-testid="sim-input"
                       onChange={e => setSim(+e.target.value)}
                       className="flex-1" style={{ accentColor: 'oklch(0.72 0.18 145)' }} />
                <span className="font-mono text-xs w-12 text-right text-fg-0">{sim.toFixed(2)}</span>
              </div>
            </Section>

            <Section title="Cross-device merge"
                     hint="Detect when two cameras shot the same scene; merge their clusters using clock-skew detection. Off by default.">
              <div className="flex items-center gap-3">
                <Toggle id="crossdev-toggle" label="Cross-device merge" checked={crossDevice} onChange={setCrossDevice} testId="crossdev-toggle" />
                <span className="text-xs text-fg-2">{crossDevice ? 'On' : 'Off'}</span>
              </div>
            </Section>

            <h2 className="text-base m-0 mt-3 font-semibold">Behaviour</h2>
            <Section title="Advance on action"
                     hint="Cycle to the next photo in the cluster after a Pick or Reject. Wraps from the last photo back to the first.">
              <div className="flex items-center gap-3">
                <Toggle id="advance-toggle" label="Advance on action" checked={advance} onChange={setAdvance} testId="advance-toggle" />
                <span className="text-xs text-fg-2">{advance ? 'On' : 'Off'}</span>
              </div>
            </Section>

            <Section title="Hide rejected photos"
                     hint="Hide rejected photos from the Library. Clusters with no remaining photos are hidden too. Focus mode still shows everything.">
              <div className="flex items-center gap-3">
                <Toggle id="hide-rejected-toggle" label="Hide rejected photos" checked={hideRejected} onChange={setHideRejected} testId="hide-rejected-toggle" />
                <span className="text-xs text-fg-2">{hideRejected ? 'On' : 'Off'}</span>
              </div>
            </Section>

            <h2 className="text-base m-0 mt-3 font-semibold">Output</h2>
            <Section title="Picks destination" hint="Relative to the source folder. Created automatically if missing.">
              <input className="input font-mono w-full" data-testid="picks-dir-input"
                     value={picksDir} onChange={e => setPicksDir(e.target.value)} />
            </Section>

            <div className="flex gap-2 pt-4 border-t border-line-1 sticky bottom-0 bg-bg-1">
              <button className="btn btn-ghost" onClick={reset} disabled={!dirty || busy}>Reset</button>
              <span className="flex-1" />
              <button className="btn btn-ghost" onClick={onLibrary}>Cancel</button>
              <button className="btn btn-primary" onClick={save} disabled={!dirty || busy} data-testid="save">
                {busy ? 'Saving…' : 'Save changes'}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
