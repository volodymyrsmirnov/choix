import type { ReactNode } from 'react'

/**
 * The window's own toolbar under a hidden-inset title bar (spec §4.6).
 * The bar is the window's drag region (`--wails-draggable: drag`); its control
 * slots opt out (`no-drag`). The 76 px left inset clears the traffic lights,
 * except in full screen where there are none. The app may widen it at run
 * time to where the window's buttons really end, through the inherited
 * custom property --titlebar-inset (its default is the approved 76 px; the
 * class stays as the approved look, the inline style reads the property).
 *
 * Every slot is centred on the traffic lights. The bar is `items-center`, so
 * its slots sit at the middle of the area above its 1 px bottom border, and
 * that area is twice the inherited custom property --titlebar-center tall:
 * the y coordinate (CSS px from the top of the window) of the middle of the
 * window's buttons, which the app measures at run time, so the bar stays
 * symmetric around the buttons like a native unified toolbar. Its default,
 * 23.5 px, is the approved 48 px bar (2 × 23.5 + 1). Full screen has no
 * buttons and keeps the 48 px bar.
 *
 * Layout, left to right: leading controls (e.g. Back), title and subtitle,
 * an indicator (scan or quality progress), a spacer, trailing controls. The
 * indicator sits left of the spacer so that appearing, disappearing or
 * changing width only resizes the spacer, and trailing controls never move.
 * When the bar is short of room the title and subtitle yield first (they
 * truncate); the indicator never shrinks, so its label always shows.
 * Every slot carries data-align-y="titlebar" (or `alignGroup`): their
 * vertical centres must agree. A page showing several bars at once (the
 * gallery) gives each its own group.
 */
export function TitleBar({
  leading,
  title,
  subtitle,
  indicator,
  trailing,
  fullScreen,
  titleHidden,
  alignGroup = 'titlebar',
}: {
  leading?: ReactNode
  /** The page's name: the bar's heading, visible unless `titleHidden`. */
  title: string
  /** The page names itself in the bar only for assistive tech (the Library:
   *  its name says nothing the window does not); no subtitle, no divider. */
  titleHidden?: boolean
  subtitle?: string
  indicator?: ReactNode
  trailing?: ReactNode
  fullScreen?: boolean
  /** The data-align-y group of the bar's slots. */
  alignGroup?: string
}) {
  return (
    <header
      data-titlebar
      className={`relative flex items-center gap-3 h-12 pr-3 flex-shrink-0 border-b border-line-1 bg-gradient-to-b from-bg-2 to-bg-1 select-none ${fullScreen ? 'pl-3' : 'pl-[76px]'}`}
      style={
        fullScreen
          ? undefined
          : { paddingLeft: 'var(--titlebar-inset, 76px)', height: 'calc(2 * var(--titlebar-center, 23.5px) + 1px)' }
      }
    >
      {leading && (
        <div data-titlebar-control data-align-y={alignGroup} className="flex items-center gap-1 flex-shrink-0">
          {leading}
        </div>
      )}
      {titleHidden ? (
        <h1 className="sr-only">{title}</h1>
      ) : (
        <div data-align-y={alignGroup} className="min-w-0 flex flex-col">
          <h1 className="m-0 text-13 font-semibold text-fg-0 leading-tight truncate">{title}</h1>
          {subtitle && <p className="m-0 text-11 text-fg-2 leading-tight truncate">{subtitle}</p>}
        </div>
      )}
      {indicator && (
        <>
          {!titleHidden && <div className="w-px h-[18px] bg-line-1 flex-shrink-0" aria-hidden="true" />}
          <div data-titlebar-control data-align-y={alignGroup} className="flex items-center flex-shrink-0">
            {indicator}
          </div>
        </>
      )}
      <div className="flex-1" />
      {trailing && (
        <div data-titlebar-control data-align-y={alignGroup} className="flex items-center gap-1.5 flex-shrink-0">
          {trailing}
        </div>
      )}
    </header>
  )
}
