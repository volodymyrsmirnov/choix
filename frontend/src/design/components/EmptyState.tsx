import type { ReactNode } from 'react'
import { noOrphan } from '../format'
import { Spinner } from './Spinner'

/**
 * A centred empty or waiting state (v1 Empty.tsx): a stack of cards with a
 * glowing front card, a heading, a line of copy and optional content (a
 * stage checklist, actions). `busy` puts a spinner on the front card.
 * `level` is the heading's level: 2 inside a screen whose title bar holds
 * the page's h1.
 */
export function EmptyState({
  title,
  body,
  busy,
  art = true,
  level = 1,
  children,
}: {
  title: string
  body?: string
  busy?: boolean
  art?: boolean
  level?: 1 | 2
  children?: ReactNode
}) {
  const Heading = level === 2 ? 'h2' : 'h1'
  return (
    <div data-empty-state className="max-w-[480px] text-center p-10">
      {art && (
        <div className="relative w-[88px] h-[88px] mx-auto mb-6" aria-hidden="true">
          <div className="absolute inset-0 rounded-xl border border-line-2 bg-bg-2 rotate-[-8deg]" />
          <div className="absolute inset-0 rounded-xl border border-line-2 bg-bg-2 rotate-[4deg]" />
          <div className="absolute inset-0 rounded-xl border border-pick flex items-center justify-center text-pick text-30 bg-gradient-to-br from-art-from to-art-to shadow-art">
            {busy ? <Spinner decorative /> : '◇'}
          </div>
        </div>
      )}
      <Heading className="text-22 font-semibold m-0 tracking-tight">{noOrphan(title)}</Heading>
      {body && <p className="text-fg-2 text-13 mt-2 mb-0 leading-[1.6]">{noOrphan(body)}</p>}
      {children && <div className="mt-6">{children}</div>}
    </div>
  )
}
