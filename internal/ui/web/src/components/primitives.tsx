import React from 'react'

type IconProps = { className?: string; size?: number }
const Svg: React.FC<React.PropsWithChildren<IconProps>> = ({ children, className, size = 14 }) => (
  <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor"
       strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"
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

export const Spinner: React.FC<{ small?: boolean }> = ({ small }) => {
  const s = small ? 12 : 32
  return (
    <svg width={s} height={s} viewBox="0 0 32 32" className="choix-spin">
      <circle cx="16" cy="16" r="13" fill="none" stroke="oklch(0.72 0.18 145 / 0.18)" strokeWidth="3" />
      <path d="M16 3 a13 13 0 0 1 13 13" fill="none" stroke="oklch(0.72 0.18 145)" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}

export const BrandMark: React.FC = () => (
  <div className="relative w-[18px] h-[18px] rounded-[4px]"
       style={{ background: 'linear-gradient(135deg, oklch(0.72 0.18 145) 0%, oklch(0.5 0.14 200) 100%)' }}>
    <div className="absolute inset-1 bg-bg-1 rounded-[1px]" />
  </div>
)

export interface AppBarProps {
  folder: string
  picked: number
  total: number
  onSettings?: () => void
  onLibrary?: () => void
  rightSlot?: React.ReactNode
}

export const AppBar: React.FC<AppBarProps> = ({ folder, picked, total, onSettings, onLibrary, rightSlot }) => {
  const pct = total > 0 ? (picked / total) * 100 : 0
  return (
    <header className="flex items-center gap-3 h-11 px-3.5 border-b border-line-1"
            style={{ background: 'linear-gradient(to bottom, oklch(0.22 0.005 240), oklch(0.19 0.004 240))' }}>
      <button onClick={onLibrary} className="flex items-center gap-2 font-semibold tracking-tight cursor-pointer bg-transparent border-0 text-fg-0 p-0">
        <BrandMark />
        <span>choix</span>
      </button>
      <div className="w-px h-[18px] bg-line-1" />
      <nav className="flex items-center gap-1.5 text-xs text-fg-1">
        <button className="bg-transparent border-0 p-0 cursor-pointer text-fg-1 hover:text-fg-0" onClick={onLibrary}>Library</button>
        <span className="text-fg-3">/</span>
        <span className="text-fg-0 font-medium">{folder}</span>
      </nav>
      <div className="flex-1" />
      {rightSlot}
      <div className="flex items-center gap-2.5 ml-2 font-mono text-[11px] text-fg-2">
        <span>
          <span className="text-pick">{picked}</span>
          <span className="text-fg-3"> / </span>
          <span>{total}</span>
        </span>
        <div className="w-20 h-1 bg-bg-3 rounded-full overflow-hidden">
          <div className="h-full bg-pick rounded-[inherit]" style={{ width: `${pct}%` }} />
        </div>
      </div>
      <div className="w-px h-[18px] bg-line-1 ml-1" />
      <button className="btn btn-ghost px-2" title="Settings" onClick={onSettings}><SettingsIcon /></button>
    </header>
  )
}
