import type { ReactNode } from 'react'
import { noOrphan } from '../format'
import { Icon } from './Icon'

/**
 * A centred message on the Focus stage when there is no cluster to show:
 * the cluster failed to load (with actions). v1 only raised a toast and kept
 * showing "Loading…"; Choix 2.0 says so in place.
 */
export function FocusNotice({ title, body, actions }: { title: string; body?: string; actions?: ReactNode }) {
  return (
    <div role="alert" className="max-w-[420px] flex flex-col items-center text-center gap-2 p-6">
      <Icon name="alert" size={20} className="text-reject" />
      <h2 className="m-0 text-15 font-semibold text-fg-0">{noOrphan(title)}</h2>
      {body && <p className="m-0 text-12 text-fg-2 break-words">{noOrphan(body)}</p>}
      {actions && <div className="mt-3 flex items-center gap-2">{actions}</div>}
    </div>
  )
}
