import { memo } from 'react'
import type { Cluster, MediaURLs } from '../types'
import { Button } from './Button'
import { ClusterStrip } from './ClusterStrip'
import { CURRENT } from './current'
import { Icon } from './Icon'

export interface ClusterSectionCounts {
  picked: number
  rejected: number
  reviewed: number
}

/**
 * One cluster on the Folder page (v1 Library's ClusterSection): the camera as
 * a heading, the time, picked / rejected / reviewed counts, Open Focus, a
 * neutral reviewed rail, and the thumbnail strip. A click anywhere on it
 * makes it the active cluster; a thumbnail opens Focus on that photo.
 *
 * role="group" rather than the implicit region: a named <section> per cluster
 * would flood the landmark list with hundreds of entries. The camera is an
 * h2, so heading navigation hops cluster to cluster.
 */
export const ClusterSection = memo(function ClusterSection({
  cluster: c,
  active,
  counts,
  media,
  qualityOn,
  openDisabled,
  onSelect,
  onOpen,
}: {
  cluster: Cluster
  active: boolean
  /** Memoised per cluster by the list, so an unchanged cluster skips rendering. */
  counts: ClusterSectionCounts
  media: MediaURLs
  qualityOn: boolean
  /** Opening Focus is unavailable (the folder's drive is offline). */
  openDisabled?: boolean
  /** Stable across renders. */
  onSelect: (id: number) => void
  /** Stable across renders; called with the cluster's id (and a file's). */
  onOpen: (id: number, fileId?: number) => void
}) {
  const n = c.members.length
  const done = n > 0 && counts.reviewed === n
  const pct = n > 0 ? (counts.reviewed / n) * 100 : 0
  const titleId = `cluster-${c.id}-device`
  const timeId = `cluster-${c.id}-time`
  return (
    <section
      data-testid="cluster"
      data-cluster-id={c.id}
      data-active-cluster={active ? 'true' : undefined}
      role="group"
      aria-labelledby={`${titleId} ${timeId}`}
      aria-current={active ? 'true' : undefined}
      onClick={() => onSelect(c.id)}
      // The current cluster is the current item's look (the Library's
      // selected row): a 1 px edge always, transparent until current, so
      // the content never moves; 7 px padding plus the edge keep it where
      // the 8 px padding put it.
      className={`rounded-md border transition-colors px-[7px] pt-[7px] -mx-2 -mt-2 ${active ? `bg-bg-2 ${CURRENT}` : 'border-transparent'}`}
    >
      <div className="mb-2">
        <div className="flex items-center gap-3 min-w-0">
          <h2 id={titleId} className="m-0 min-w-0 truncate font-semibold text-13 text-fg-0 tracking-tight">
            {c.device}
          </h2>
          <span id={timeId} className="font-mono text-11 text-fg-2 whitespace-nowrap">
            {c.time}
          </span>
          <span className="flex-1" />
          {/* Picked / rejected carry a ✓ / ✕ glyph and screen-reader text, so
              the pick-green and reject-red are reinforcement rather than the
              only cue. */}
          <div className="flex items-center gap-3 font-mono text-11 text-fg-2 whitespace-nowrap">
            <span className="inline-flex items-center gap-1" title={`${counts.picked} picked`}>
              <span className="text-pick" aria-hidden="true">
                <Icon name="check" size={11} />
              </span>
              {counts.picked}
              <span className="sr-only"> picked</span>
            </span>
            <span className="inline-flex items-center gap-1" title={`${counts.rejected} rejected`}>
              <span className="text-reject" aria-hidden="true">
                <Icon name="x" size={11} />
              </span>
              {counts.rejected}
              <span className="sr-only"> rejected</span>
            </span>
            {done ? (
              // Quiet, but enough to skim the page for clusters still to finish.
              <span className="inline-flex items-center gap-1 text-fg-1" title={`All ${n} reviewed`} data-testid="cluster-reviewed">
                <span aria-hidden="true">
                  <Icon name="check" size={11} />
                </span>
                Reviewed
              </span>
            ) : (
              <span className="text-fg-3" title={`${counts.reviewed} of ${n} reviewed`}>
                <span aria-hidden="true">
                  {counts.reviewed}/{n}
                </span>
                <span className="sr-only">
                  {counts.reviewed} of {n}
                </span>{' '}
                reviewed
              </span>
            )}
          </div>
          <Button variant="ghost" className="text-11" kbd="F" disabled={openDisabled} onClick={() => onOpen(c.id)}>
            Open Focus
          </Button>
        </div>
        {/* Neutral fill: the pick-green gradient would read as "picked". */}
        <div
          className="progress-rail mt-2 h-0.5"
          role="progressbar"
          aria-label="Reviewed"
          aria-valuemin={0}
          aria-valuemax={Math.max(1, n)}
          aria-valuenow={counts.reviewed}
          aria-valuetext={`${counts.reviewed} of ${n} reviewed`}
          data-clip="intended"
        >
          <div className="absolute inset-y-0 left-0 [border-radius:inherit] bg-fg-2" style={{ width: `${pct}%` }} />
        </div>
      </div>
      <ClusterStrip clusterId={c.id} members={c.members} media={media} qualityOn={qualityOn} openDisabled={openDisabled} onOpen={onOpen} />
    </section>
  )
})
