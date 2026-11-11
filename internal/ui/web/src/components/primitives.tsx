import React, { useContext } from 'react'
import { setShortcutsOpen } from './Shortcuts'
import type { QualityStatus } from '../api'

type IconProps = { className?: string; size?: number }
// Icons are always decorative: every icon-only control carries its own
// aria-label, and icons beside text would otherwise be announced as an
// unlabelled image ("image" / "group") by VoiceOver.
const Svg: React.FC<React.PropsWithChildren<IconProps>> = ({ children, className, size = 14 }) => (
  <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor"
       strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"
       aria-hidden="true" focusable="false"
       className={`flex-shrink-0 ${className ?? ''}`}>
    {children}
  </svg>
)

export const SettingsIcon = (p: IconProps) => <Svg {...p}><circle cx="8" cy="8" r="2"/><path d="M8 1.5v2M8 12.5v2M14.5 8h-2M3.5 8h-2M12.6 3.4l-1.4 1.4M4.8 11.2l-1.4 1.4M12.6 12.6l-1.4-1.4M4.8 4.8 3.4 3.4"/></Svg>
export const SearchIcon   = (p: IconProps) => <Svg {...p}><circle cx="7" cy="7" r="4.5"/><path d="m13.5 13.5-3-3"/></Svg>
export const FilterIcon   = (p: IconProps) => <Svg {...p}><path d="M2.5 4h11M4.5 8h7M6.5 12h3"/></Svg>
export const SortIcon     = (p: IconProps) => <Svg {...p}><path d="M4 3v10M4 13l-2-2M4 13l2-2M11 13V3M11 3 9 5M11 3l2 2"/></Svg>
export const CheckIcon    = (p: IconProps) => <Svg {...p}><path d="m3 8 3.5 3.5L13 5"/></Svg>
export const XIcon        = (p: IconProps) => <Svg {...p}><path d="M3.5 3.5l9 9M12.5 3.5l-9 9"/></Svg>
export const ArrowL       = (p: IconProps) => <Svg {...p}><path d="m9 3-5 5 5 5"/></Svg>
export const ArrowR       = (p: IconProps) => <Svg {...p}><path d="m7 3 5 5-5 5"/></Svg>
export const InfoIcon     = (p: IconProps) => <Svg {...p}><circle cx="8" cy="8" r="6"/><path d="M8 7v4M8 5v.01"/></Svg>
export const ChevronDown  = (p: IconProps) => <Svg {...p}><path d="m4 6 4 4 4-4"/></Svg>
export const RefreshIcon  = (p: IconProps) => <Svg {...p}><path d="M14 8a6 6 0 1 1-1.8-4.2M14 2v3.5h-3.5"/></Svg>
export const FolderIcon   = (p: IconProps) => <Svg {...p}><path d="M2 4.5A1.5 1.5 0 0 1 3.5 3h2.7l1.5 1.5h5.3A1.5 1.5 0 0 1 14.5 6v6A1.5 1.5 0 0 1 13 13.5H3a1 1 0 0 1-1-1z"/></Svg>
export const PanelIcon    = (p: IconProps) => <Svg {...p}><rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M10 3v10"/></Svg>
export const ZoomIcon     = (p: IconProps) => <Svg {...p}><circle cx="7" cy="7" r="4.5"/><path d="m13.5 13.5-3-3M5 7h4M7 5v4"/></Svg>
export const CompareIcon  = (p: IconProps) => <Svg {...p}><path d="M3 3h4v10H3zM9 5h4v8H9z"/></Svg>
export const HelpIcon     = (p: IconProps) => <Svg {...p}><circle cx="8" cy="8" r="6"/><path d="M6.2 6.3a1.9 1.9 0 1 1 2.6 1.75c-.5.22-.8.6-.8 1.15v.3M8 11.3v.01"/></Svg>

// Spinner announces itself as "Loading" by default, because Focus shows it
// alone in an otherwise empty photo stage. Pass `decorative` where visible
// text already says what is happening (Empty's stage list, the AppBar scan
// indicator) so screen readers don't hear "Loading" on top of that text.
export const Spinner: React.FC<{ small?: boolean; decorative?: boolean; label?: string }> = ({ small, decorative, label = 'Loading' }) => {
  const s = small ? 12 : 32
  const a11y = decorative
    ? { 'aria-hidden': true as const, focusable: 'false' as const }
    : { role: 'img', 'aria-label': label }
  return (
    <svg width={s} height={s} viewBox="0 0 32 32" className="choix-spin" {...a11y}>
      <circle cx="16" cy="16" r="13" fill="none" stroke="oklch(0.72 0.18 145 / 0.18)" strokeWidth="3" />
      <path d="M16 3 a13 13 0 0 1 13 13" fill="none" stroke="oklch(0.72 0.18 145)" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}

// The app icon (assets/icon/favicon.svg). Its hashed URL is injected at
// build time by scripts/build.ts.
export const BrandMark: React.FC = () => (
  <img src={__CHOIX_ICON__} alt="" aria-hidden="true" width={20} height={20} className="block w-5 h-5" draggable={false} />
)

// Pipeline stage names (the `stage` field of SSE progress events) → labels.
// Shared by Empty's stage checklist and the AppBar scan indicator.
export const STAGE_LABELS: Record<string, string> = {
  discover: 'Discover files',
  metadata: 'Read EXIF',
  thumb: 'Generate thumbs',
  analyze: 'Compute embeddings',
  cluster: 'Cluster',
}
export const STAGE_ORDER = ['discover', 'metadata', 'thumb', 'analyze', 'cluster']
export const stageLabel = (stage: string): string => STAGE_LABELS[stage] ?? stage

// Scan progress, provided by App and read by AppBar. A context rather than an
// AppBar prop because AppBar is rendered inside every screen (Library, Focus,
// Settings, Empty); threading progress through each screen's props would
// re-render and couple all of them to the SSE stream. null = no indicator.
export interface ScanProgress {
  stage: string
  done: number
  total: number
  failed: number
  // 'stopped': the pipeline aborted (a stage emitted phase "failed" —
  // cancellation, a stuck stage, a cluster error). Nothing will advance until
  // choix is restarted, so the indicator says so instead of spinning forever.
  state: 'running' | 'stopped'
}
export const ScanProgressContext = React.createContext<ScanProgress | null>(null)

const ScanIndicator: React.FC<{ scan: ScanProgress }> = ({ scan }) => {
  const { stage, done, total, failed, state } = scan
  const running = state === 'running'
  const label = stageLabel(stage)
  const pct = total > 0 ? Math.min(100, (done / total) * 100) : 0
  // Size the counter for the largest value this stage can show. `total` is
  // fixed for the whole stage and the font is monospaced, so the counter
  // never changes width tick-to-tick and the bar beside it never jitters.
  const totalText = total.toLocaleString()
  const counterWidth = `${totalText.length * 2 + 3}ch`
  return (
    <div data-testid="scan-progress" data-state={state}
         className="flex items-center gap-2 min-w-0 text-[11px]">
      {running
        ? <Spinner small decorative />
        : <span className="dot" data-tone="reject" aria-hidden="true" />}
      {/* Only the stage label sits in the live region: it changes a handful
          of times per scan, whereas the counter below ticks once per file
          and would flood a screen reader. */}
      <span role="status" aria-live="polite" className={`truncate ${running ? 'text-fg-1' : 'text-reject'}`}>
        <span className="sr-only">{running ? 'Scanning: ' : 'Scan stopped during '}</span>
        {label}
        {!running && <span aria-hidden="true"> · stopped</span>}
      </span>
      {total > 0 && (
        <span className="font-mono tabular-nums text-fg-2 text-right flex-shrink-0" style={{ width: counterWidth }}
              aria-hidden="true">
          {done.toLocaleString()} / {totalText}
        </span>
      )}
      {running && (
        <div className="progress-rail w-20 flex-shrink-0"
             role="progressbar" aria-label={`${label} progress`}
             aria-valuemin={0}
             aria-valuemax={total > 0 ? total : undefined}
             aria-valuenow={total > 0 ? done : undefined}
             aria-valuetext={total > 0 ? `${done.toLocaleString()} of ${totalText} files` : undefined}>
          <div className="fill" data-indet={total === 0 ? 'true' : undefined}
               style={{ width: total > 0 ? `${pct}%` : undefined }} />
        </div>
      )}
      {failed > 0 && (
        <span className="font-mono tabular-nums text-reject flex-shrink-0">
          {failed.toLocaleString()} failed
        </span>
      )}
    </div>
  )
}

// ── Advisory quality rating ────────────────────────────────────────────────
// A 0–10 technical-quality score (focus, motion blur, noise) per photo. It is
// information only: nothing sorts, picks or rejects by it.

export const formatQuality = (q: number): string => q.toFixed(1)
// Spoken form, appended to a thumbnail button's accessible name or used as
// the badge's own label where the badge stands alone.
export const qualityLabel = (q: number): string => `quality ${formatQuality(q)} of 10`

// The badge. `standalone` gives it its own accessible name (Focus hero,
// Compare panes); inside a labelled button (Library and rail thumbnails) a
// nested label is never announced, so there the badge is hidden from
// assistive tech and the button's name carries the rating instead.
// The value is always text — colour is never the only signal.
export const QualityBadge: React.FC<{ value: number; standalone?: boolean; className?: string }> = ({ value, standalone, className }) => {
  const a11y = standalone
    ? { role: 'img', 'aria-label': `Quality ${formatQuality(value)} of 10` }
    : { 'aria-hidden': true as const }
  return (
    <span {...a11y} data-testid="quality-badge"
          title={`Technical quality ${formatQuality(value)} of 10 (sharpness, noise). Compare within a cluster.`}
          className={`inline-flex items-center justify-center px-1 h-[18px] min-w-[26px] rounded-sm font-mono tabular-nums text-[11px] leading-none font-medium text-fg-0 ${className ?? ''}`}
          style={{ background: 'oklch(0 0 0 / 0.65)' }}>
      {formatQuality(value)}
    </span>
  )
}

// Quality worker state, provided by App (which polls GET /api/quality) and
// read by AppBar and Settings. `refresh` re-polls right away, e.g. after
// pressing Retry.
export interface QualityContextValue {
  status: QualityStatus | null
  refresh: () => void
}
export const QualityContext = React.createContext<QualityContextValue>({ status: null, refresh: () => {} })

// AppBar indicator for the quality worker, in the same visual language as
// the scan indicator: model download, rating pass, or a failure notice.
// Renders nothing when there is nothing to say.
const QualityIndicator: React.FC<{ status: QualityStatus; scanShown: boolean }> = ({ status, scanShown }) => {
  const { state } = status
  if (state === 'download_failed' || state === 'error') {
    const text = state === 'download_failed' ? 'Quality model download failed' : 'Quality rating stopped'
    return (
      <div data-testid="quality-progress" data-state={state} role="status"
           title={`${status.error ?? ''}${state === 'download_failed' ? ' — retry from Settings' : ' — restart choix to retry'}`}
           className="flex items-center gap-2 min-w-0 text-[11px] text-reject">
        <span className="dot" data-tone="reject" aria-hidden="true" />
        <span className="truncate">{text}</span>
      </div>
    )
  }
  if (state === 'downloading') {
    const known = status.size > 0
    const pct = known ? Math.min(100, (status.received / status.size) * 100) : 0
    const mb = (n: number) => Math.round(n / 1e6).toLocaleString()
    return (
      <div data-testid="quality-progress" data-state={state}
           title="Downloading the model for the quality rating (one-time). Turn it off in Settings."
           className="flex items-center gap-2 min-w-0 text-[11px]">
        <Spinner small decorative />
        <span role="status" className="truncate text-fg-1">Quality model</span>
        <span className="font-mono tabular-nums text-fg-2 text-right flex-shrink-0 w-[4ch]" aria-hidden="true">
          {known ? `${Math.floor(pct)}%` : ''}
        </span>
        <div className="progress-rail w-20 flex-shrink-0"
             role="progressbar" aria-label="Quality model download"
             aria-valuemin={0} aria-valuemax={known ? 100 : undefined}
             aria-valuenow={known ? Math.floor(pct) : undefined}
             aria-valuetext={known ? `${mb(status.received)} of ${mb(status.size)} MB` : undefined}>
          <div className="fill" data-indet={known ? undefined : 'true'}
               style={{ width: known ? `${pct}%` : undefined }} />
        </div>
      </div>
    )
  }
  // The rating pass only runs while no scan is, but the two snapshots are
  // polled separately, so never show both at once.
  if (state === 'scoring' && !scanShown && status.total > 0) {
    const totalText = status.total.toLocaleString()
    const pct = Math.min(100, (status.done / status.total) * 100)
    return (
      <div data-testid="quality-progress" data-state={state}
           className="flex items-center gap-2 min-w-0 text-[11px]">
        <Spinner small decorative />
        <span role="status" className="truncate text-fg-1">Rate quality</span>
        <span className="font-mono tabular-nums text-fg-2 text-right flex-shrink-0"
              style={{ width: `${totalText.length * 2 + 3}ch` }} aria-hidden="true">
          {status.done.toLocaleString()} / {totalText}
        </span>
        <div className="progress-rail w-20 flex-shrink-0"
             role="progressbar" aria-label="Quality rating progress"
             aria-valuemin={0} aria-valuemax={status.total} aria-valuenow={status.done}
             aria-valuetext={`${status.done.toLocaleString()} of ${totalText} photos`}>
          <div className="fill" style={{ width: `${pct}%` }} />
        </div>
      </div>
    )
  }
  return null
}
const qualityIndicatorVisible = (q: QualityStatus | null, scanShown: boolean): q is QualityStatus =>
  !!q && q.enabled && (
    q.state === 'download_failed' || q.state === 'error' || q.state === 'downloading'
    || (q.state === 'scoring' && !scanShown && q.total > 0)
  )

export interface AppBarProps {
  folder: string
  picked: number
  total: number
  onSettings?: () => void
  onLibrary?: () => void
  rightSlot?: React.ReactNode
  // showMeter=false hides the picked/total meter (Empty: there is nothing
  // to pick yet, so "0 / 0" would only be noise).
  showMeter?: boolean
  // showScanProgress=false suppresses the scan indicator where the screen
  // already renders a full progress panel (Empty's scanning state).
  showScanProgress?: boolean
}

export const AppBar: React.FC<AppBarProps> = ({
  folder, picked, total, onSettings, onLibrary, rightSlot,
  showMeter = true, showScanProgress = true,
}) => {
  const scan = useContext(ScanProgressContext)
  const quality = useContext(QualityContext).status
  const scanShown = showScanProgress && !!scan
  const pct = total > 0 ? (picked / total) * 100 : 0
  const meterLabel = `${picked.toLocaleString()} of ${total.toLocaleString()} picked`
  return (
    <header className="flex items-center gap-3 h-11 px-3.5 border-b border-line-1"
            style={{ background: 'linear-gradient(to bottom, oklch(0.22 0.005 240), oklch(0.19 0.004 240))' }}>
      <button onClick={onLibrary} className="flex items-center gap-2 font-semibold tracking-tight cursor-pointer bg-transparent border-0 text-fg-0 p-0">
        <BrandMark />
        <span>choix</span>
      </button>
      <div className="w-px h-[18px] bg-line-1" />
      <nav className="flex items-center gap-1.5 text-xs text-fg-1 min-w-0" aria-label="Breadcrumb">
        <button className="bg-transparent border-0 p-0 cursor-pointer text-fg-1 hover:text-fg-0" onClick={onLibrary}>Library</button>
        <span className="text-fg-3" aria-hidden="true">/</span>
        <span className="text-fg-0 font-medium truncate">{folder}</span>
      </nav>
      {/* Left of the spacer on purpose: appearing, disappearing or changing
          width only resizes the spacer, so the right-hand controls never move. */}
      {showScanProgress && scan && (
        <>
          <div className="w-px h-[18px] bg-line-1 flex-shrink-0" />
          <ScanIndicator scan={scan} />
        </>
      )}
      {qualityIndicatorVisible(quality, scanShown) && (
        <>
          <div className="w-px h-[18px] bg-line-1 flex-shrink-0" />
          <QualityIndicator status={quality} scanShown={scanShown} />
        </>
      )}
      <div className="flex-1" />
      {rightSlot}
      {showMeter && (
        <div className="flex items-center gap-2.5 ml-2 font-mono text-[11px] text-fg-2" title={meterLabel}>
          <span className="sr-only">{meterLabel}</span>
          <span aria-hidden="true">
            <span className="text-pick">{picked}</span>
            <span className="text-fg-3"> / </span>
            <span>{total}</span>
          </span>
          <div className="w-20 h-1 bg-bg-3 rounded-full overflow-hidden" aria-hidden="true">
            <div className="h-full bg-pick rounded-[inherit]" style={{ width: `${pct}%` }} />
          </div>
        </div>
      )}
      <div className="w-px h-[18px] bg-line-1 ml-1" />
      <button className="btn btn-ghost px-2" title="Keyboard shortcuts (?)" aria-label="Keyboard shortcuts"
              aria-haspopup="dialog" data-testid="shortcuts-btn"
              onClick={() => setShortcutsOpen(true)}>
        <HelpIcon />
      </button>
      <button className="btn btn-ghost px-2 -ml-2" title="Settings" aria-label="Settings" onClick={onSettings}><SettingsIcon /></button>
    </header>
  )
}
