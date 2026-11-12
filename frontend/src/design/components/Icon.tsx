import type { ReactElement } from 'react'

// v1's inline icons (primitives.tsx), one 16-unit grid, stroked in
// currentColor, plus the ones Choix 2.0's screens add (plus, dots, back,
// drive, play, alert). Icons are always decorative: every icon-only control
// carries its own aria-label.
const PATHS = {
  gear: (
    <>
      <circle cx="8" cy="8" r="2" />
      <path d="M8 1.5v2M8 12.5v2M14.5 8h-2M3.5 8h-2M12.6 3.4l-1.4 1.4M4.8 11.2l-1.4 1.4M12.6 12.6l-1.4-1.4M4.8 4.8 3.4 3.4" />
    </>
  ),
  search: (
    <>
      <circle cx="7" cy="7" r="4.5" />
      <path d="m13.5 13.5-3-3" />
    </>
  ),
  filter: <path d="M2.5 4h11M4.5 8h7M6.5 12h3" />,
  sort: <path d="M4 3v10M4 13l-2-2M4 13l2-2M11 13V3M11 3 9 5M11 3l2 2" />,
  check: <path d="m3 8 3.5 3.5L13 5" />,
  x: <path d="M3.5 3.5l9 9M12.5 3.5l-9 9" />,
  'arrow-left': <path d="m9 3-5 5 5 5" />,
  'arrow-right': <path d="m7 3 5 5-5 5" />,
  info: (
    <>
      <circle cx="8" cy="8" r="6" />
      <path d="M8 7v4M8 5v.01" />
    </>
  ),
  'chevron-down': <path d="m4 6 4 4 4-4" />,
  refresh: <path d="M14 8a6 6 0 1 1-1.8-4.2M14 2v3.5h-3.5" />,
  folder: (
    <path d="M2 4.5A1.5 1.5 0 0 1 3.5 3h2.7l1.5 1.5h5.3A1.5 1.5 0 0 1 14.5 6v6A1.5 1.5 0 0 1 13 13.5H3a1 1 0 0 1-1-1z" />
  ),
  panel: (
    <>
      <rect x="2" y="3" width="12" height="10" rx="1.5" />
      <path d="M10 3v10" />
    </>
  ),
  zoom: (
    <>
      <circle cx="7" cy="7" r="4.5" />
      <path d="m13.5 13.5-3-3M5 7h4M7 5v4" />
    </>
  ),
  compare: <path d="M3 3h4v10H3zM9 5h4v8H9z" />,
  help: (
    <>
      <circle cx="8" cy="8" r="6" />
      <path d="M6.2 6.3a1.9 1.9 0 1 1 2.6 1.75c-.5.22-.8.6-.8 1.15v.3M8 11.3v.01" />
    </>
  ),
  plus: <path d="M8 3v10M3 8h10" />,
  dots: <path d="M3.5 8v.01M8 8v.01M12.5 8v.01" />,
  back: <path d="m10 3.5-4.5 4.5 4.5 4.5" />,
  drive: (
    <>
      <rect x="2" y="4" width="12" height="8" rx="1.5" />
      <path d="M2 9h12M11 10.5v.01M12.5 10.5v.01" />
    </>
  ),
  play: <path d="M5 3.5v9l7-4.5z" />,
  alert: <path d="M8 2.5 14 13H2zM8 6.5v3M8 11.5v.01" />,
} satisfies Record<string, ReactElement>

export type IconName = keyof typeof PATHS
export const ICON_NAMES = Object.keys(PATHS) as IconName[]

export function Icon({ name, size = 14, className }: { name: IconName; size?: number; className?: string }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill={name === 'play' ? 'currentColor' : 'none'}
      stroke="currentColor"
      strokeWidth={name === 'dots' ? 2.2 : 1.4}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      className={`flex-shrink-0 ${className ?? ''}`}
    >
      {PATHS[name]}
    </svg>
  )
}
