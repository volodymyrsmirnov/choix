import type { ReactNode } from 'react'
import { noOrphan } from '../format'
import { Icon, type IconName } from './Icon'

export type BannerTone = 'info' | 'warn' | 'error'

const BAR: Record<BannerTone, string> = { info: 'bg-sel', warn: 'bg-warn', error: 'bg-reject' }
const ICON_TONE: Record<BannerTone, string> = { info: 'text-sel', warn: 'text-warn', error: 'text-reject' }

/**
 * An in-page notice (a compact model download, time-only grouping, a folder
 * that went offline): a tone bar, an optional icon, a title, an optional line
 * of body text, optional extra content such as a progress rail, and actions.
 */
export function Banner({
  tone,
  title,
  body,
  icon,
  actions,
  children,
  live,
  className,
  testId,
}: {
  tone: BannerTone
  title: string
  body?: string
  icon?: IconName
  actions?: ReactNode
  children?: ReactNode
  /** Announce changes politely (role="status"). Off by default: most banners
   *  are static notices, and a page full of live regions is noise. */
  live?: boolean
  className?: string
  testId?: string
}) {
  return (
    <div
      role={live ? 'status' : undefined}
      data-tone={tone}
      data-testid={testId}
      className={`flex items-center gap-3 rounded-lg border border-line-1 bg-bg-2 p-2.5 ${className ?? ''}`}
    >
      <div className={`w-[3px] self-stretch rounded-full ${BAR[tone]}`} aria-hidden="true" />
      {icon && <Icon name={icon} className={ICON_TONE[tone]} />}
      <div className="flex-1 min-w-0">
        <div className="text-12 font-semibold text-fg-0">{noOrphan(title)}</div>
        {body && <p className="m-0 mt-0.5 text-11 text-fg-2 leading-snug">{noOrphan(body)}</p>}
        {children}
      </div>
      {actions && <div className="flex items-center gap-2 flex-shrink-0">{actions}</div>}
    </div>
  )
}
