import { Fragment } from 'react'
import { FOCUS_RING } from './focusRing'
import { IconButton } from './IconButton'
import { Panel } from './Panel'

/**
 * Focus' Info panel (v1): the photo's EXIF rows, its quality rating and, when
 * the file has them, its GPS coordinates linking to a map. A Panel, so it
 * registers as an overlay; the caller places it over the stage.
 */
export function FocusInfo({
  id,
  open,
  rows,
  gps,
  onClose,
  className,
}: {
  id: string
  open: boolean
  rows: [string, string][]
  gps: { label: string; href: string } | null
  onClose: () => void
  className?: string
}) {
  return (
    <Panel id={id} open={open} label="Photo info" className={className}>
      <div className="px-3 py-2.5 border-b border-line-1 flex items-center gap-2">
        <span className="eyebrow">EXIF</span>
        <span className="flex-1" />
        <IconButton icon="x" label="Close info panel" title="Close info panel (I)" className="h-[22px] px-1.5" onClick={onClose} />
      </div>
      <div className="p-3 overflow-y-auto flex-1" data-scroll>
        <dl className="m-0 grid grid-cols-[1fr_auto] gap-x-2.5 gap-y-1 text-11">
          {rows.map(([k, v]) => (
            <Fragment key={k}>
              <dt className="text-fg-2">{k}</dt>
              <dd className="m-0 text-fg-0 font-mono text-11">{v}</dd>
            </Fragment>
          ))}
          {gps && (
            <>
              <dt className="text-fg-2">Coords</dt>
              <dd className="m-0 text-fg-0 font-mono text-11">
                <a
                  href={gps.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  title="Open in Google Maps (new tab)"
                  aria-label={`${gps.label}, open in Google Maps (opens in a new tab)`}
                  className={`text-pick hover:underline rounded-sm ${FOCUS_RING}`}
                >
                  {gps.label}
                </a>
              </dd>
            </>
          )}
        </dl>
      </div>
    </Panel>
  )
}
