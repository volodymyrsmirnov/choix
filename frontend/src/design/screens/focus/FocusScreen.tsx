import {
  BackButton,
  Button,
  ComparePane,
  FocusChip,
  FocusChipRule,
  FocusDonePill,
  FocusInfo,
  FocusInfoToggle,
  FocusMedia,
  FocusNotice,
  FocusPanSurface,
  FocusPathLabel,
  FocusQuality,
  FocusRail,
  FocusZoom,
  Icon,
  IconButton,
  Kbd,
  Prose,
  TitleBar,
  Toaster,
} from '../../components'
import type { FocusResponse, Member } from '../../types'
import {
  actionTarget,
  compareGrid,
  exifRows,
  gpsOf,
  position,
  ratingOf,
  reviewedCount,
  stepIndex,
  stepZoom,
  toggleAction,
  viewAt,
  zoomBy,
} from './logic'
import { FOCUS_OVERLAYS, MAX_ZOOM, MIN_ZOOM, PEEP_ZOOM, type FocusProps } from './props'

/**
 * Focus and Compare (v1 Focus.tsx, spec §4.3): the title bar (back to the
 * folder, the cluster's position and time, previous / next cluster), the rail,
 * the stage (one photo or up to four compare panes) with its chips and the
 * Info panel, and the footer of photo actions.
 */
export function FocusScreen(p: FocusProps) {
  const member = p.data?.cluster.members[p.activeIndex]
  const data = member ? p.data : null

  return (
    <div className="relative flex flex-col h-full min-h-0">
      <TitleBar
        leading={<BackButton to={p.folder} onClick={p.onBack} />}
        title={data ? `Cluster ${data.cluster_index} of ${data.cluster_count}` : 'Cluster'}
        subtitle={
          data
            ? `${data.cluster.device} · ${data.cluster.time} · ${reviewedCount(data.cluster.members)} of ${data.cluster.members.length} reviewed`
            : undefined
        }
        trailing={
          <>
            {data && <ClusterNav {...p} data={data} />}
            <IconButton icon="help" label="Keyboard shortcuts" onClick={p.onHelp} aria-haspopup="dialog" />
            <IconButton icon="gear" label="Settings" onClick={p.onSettings} />
          </>
        }
      />
      {data && member ? (
        <Loaded {...p} data={data} member={member} />
      ) : (
        <main className="flex-1 min-h-0 flex items-center justify-center bg-bg-1">
          {p.loadError ? (
            <FocusNotice
              title="This cluster could not be loaded"
              body={p.loadError}
              actions={
                <>
                  <Button variant="default" icon="refresh" onClick={p.onRetry}>
                    Try Again
                  </Button>
                  <Button variant="ghost" onClick={p.onBack}>
                    Back to {p.folder}
                  </Button>
                </>
              }
            />
          ) : (
            <div className="text-fg-2 text-14" role="status" aria-live="polite">
              Loading…
            </div>
          )}
        </main>
      )}
      <Toaster items={p.toasts} onUndo={p.onUndoToast} onDismiss={p.onDismissToast} />
    </div>
  )
}

const clusterDone = (data: FocusResponse) => {
  const ms = data.cluster.members
  return ms.length > 0 && reviewedCount(ms) === ms.length
}

function ClusterNav(p: FocusProps & { data: FocusResponse }) {
  const { data } = p
  return (
    <>
      <Button
        variant="ghost"
        kbd="⇧↩"
        disabled={!data.prev_cluster_id}
        onClick={() => p.onCluster(data.prev_cluster_id)}
        data-testid="prev-cluster"
        aria-keyshortcuts="Shift+Enter"
      >
        Prev cluster
      </Button>
      <Button
        variant={clusterDone(data) && data.next_cluster_id ? 'primary' : 'ghost'}
        kbd="↩"
        disabled={!data.next_cluster_id}
        onClick={() => p.onCluster(data.next_cluster_id)}
        data-testid="next-cluster"
        aria-keyshortcuts="Enter"
      >
        Next cluster
      </Button>
    </>
  )
}

function Loaded(p: FocusProps & { data: FocusResponse; member: Member }) {
  const { data, member, comparing, compareIdx, activeIndex } = p
  const members = data.cluster.members
  const view = { zoom: p.zoom, pan: p.pan }
  const rating = (m: Member) => ratingOf(m, p.ratings, p.qualityOn)
  const stateOf = (m: Member) => p.mediaState[m.file_id] ?? 'ready'
  const video = member.kind === 'video'

  const exif = data.exif[String(member.file_id)] ?? {}
  const heroRating = rating(member)
  const rows = exifRows(exif, heroRating)
  const gps = gpsOf(exif)
  const infoVisible = p.showInfo && (rows.length > 0 || gps !== null) && !comparing

  const panes = compareIdx.flatMap((i) => (members[i] ? [{ m: members[i]!, i }] : []))
  const grid = compareGrid(panes.length)
  const target = actionTarget(members, activeIndex, comparing, compareIdx)
  const busy = target ? p.busyFileIds.includes(target.file_id) : false
  const lifted = comparing ? panes.some((x) => x.m.kind === 'video') : video
  const next = data.next_cluster_id

  const step = (dir: 1 | -1) => {
    const i = stepIndex(members.length, activeIndex, dir, comparing, compareIdx)
    if (i !== null) p.onSelect(i)
  }
  const mark = (k: 'picked' | 'rejected') => {
    if (target) p.onPick(target.file_id, toggleAction(target, k))
  }
  const stepDisabled = comparing ? panes.length <= 1 : members.length <= 1

  return (
    <>
      <div className="flex-1 flex min-h-0 relative">
        <FocusRail
          members={members}
          activeIndex={activeIndex}
          comparing={comparing}
          compareIdx={compareIdx}
          rating={rating}
          media={p.media}
          onItem={(i) => (comparing ? p.onCompareSelect(i) : p.onSelect(i))}
        />
        <main className="flex-1 min-w-0 relative bg-bg-0 overflow-hidden" data-clip="intended">
          {comparing ? (
            <div
              className={`absolute inset-0 grid p-3 gap-3 ${grid.cols === 1 ? 'grid-cols-1' : 'grid-cols-2'} ${grid.rows === 1 ? 'grid-rows-1' : 'grid-rows-2'}`}
            >
              {panes.length === 0 && (
                <div className="col-span-full row-span-full flex items-center justify-center px-6 text-center text-fg-2 text-12" data-testid="compare-empty">
                  <Prose wrap="balance" className="max-w-[440px]">
                    Click rail thumbnails to add to compare (up to 4). Then 1–4 or ←/→ choose a pane; Space / X mark it.
                  </Prose>
                </div>
              )}
              {panes.map(({ m, i }, pos) => (
                <ComparePane
                  key={m.file_id}
                  member={m}
                  pane={pos}
                  index={i}
                  total={members.length}
                  active={i === activeIndex}
                  rating={rating(m)}
                  qualityOn={p.qualityOn}
                  underZoom={!video && pos === grid.cols - 1}
                  zoomable={!video}
                  media={p.media}
                  state={stateOf(m)}
                  view={view}
                  zoomBy={zoomBy}
                  onView={p.onView}
                  onActivate={() => p.onSelect(i)}
                  onMediaState={(s) => p.onMediaState(m.file_id, s)}
                />
              ))}
            </div>
          ) : (
            <FocusPanSurface view={view} zoomable={!video} zoomBy={zoomBy} onView={p.onView} className="absolute inset-0 p-6">
              <FocusMedia
                member={member}
                media={p.media}
                state={stateOf(member)}
                zoom={p.zoom}
                pan={p.pan}
                variant="hero"
                onState={(s) => p.onMediaState(member.file_id, s)}
              />
            </FocusPanSurface>
          )}

          {/* The one photo-position counter; while comparing every pane carries its own. */}
          {!comparing && (
            <FocusChip
              className={`absolute top-3 left-3 px-2.5 py-1.5 ${p.qualityOn ? 'max-w-[max(160px,calc(100%-390px))]' : 'max-w-[max(160px,calc(100%-280px))]'}`}
            >
              <span className="flex-shrink-0 font-mono text-11 text-fg-0">{position(activeIndex, members.length)}</span>
              <FocusChipRule />
              <FocusPathLabel path={member.path} className="font-mono text-11 text-fg-1" />
            </FocusChip>
          )}

          <div className="absolute top-3 right-3 flex items-center gap-1.5" data-testid="stage-controls">
            {!comparing && heroRating !== undefined && <FocusQuality value={heroRating} />}
            {!video && <FocusZoom zoom={p.zoom} min={MIN_ZOOM} max={MAX_ZOOM} onStep={(dir) => p.onView(stepZoom(view, dir))} />}
            {!comparing && <FocusInfoToggle open={p.showInfo} controls={FOCUS_OVERLAYS.info} onToggle={() => p.onInfo(!p.showInfo)} />}
          </div>

          {!comparing && (
            <FocusInfo
              id={FOCUS_OVERLAYS.info}
              open={infoVisible}
              rows={rows}
              gps={gps}
              onClose={() => p.onInfo(false)}
              className="absolute top-14 right-3 bottom-3"
            />
          )}

          <FocusDonePill
            show={clusterDone(data)}
            last={!next}
            lifted={lifted}
            besideInfo={infoVisible}
            onClick={() => (next ? p.onCluster(next) : p.onBack())}
          />
        </main>
      </div>

      <footer className="h-12 flex-shrink-0 px-3 border-t border-line-1 bg-bg-2 flex items-center gap-1.5">
        <Button
          variant="ghost"
          onClick={() => step(-1)}
          disabled={stepDisabled}
          aria-label={comparing ? 'Previous pane' : 'Previous photo'}
          aria-keyshortcuts="ArrowLeft"
        >
          <Icon name="arrow-left" />
          <Kbd hidden>←</Kbd>
        </Button>
        <Button
          variant="ghost"
          onClick={() => step(1)}
          disabled={stepDisabled}
          aria-label={comparing ? 'Next pane' : 'Next photo'}
          aria-keyshortcuts="ArrowRight"
        >
          <Kbd hidden>→</Kbd>
          <Icon name="arrow-right" />
        </Button>
        <div className="w-px h-[22px] bg-line-1 mx-1.5" aria-hidden="true" />
        <Button
          variant={target?.picked ? 'pick' : 'default'}
          icon="check"
          kbd="Space"
          onClick={() => mark('picked')}
          disabled={!target || busy}
          aria-pressed={!!target?.picked}
          aria-keyshortcuts="Space"
          data-testid="pick-btn"
        >
          {target?.picked ? 'Picked' : 'Pick'}
        </Button>
        <Button
          variant={target?.rejected ? 'reject' : 'default'}
          icon="x"
          kbd="X"
          onClick={() => mark('rejected')}
          disabled={!target || busy}
          aria-pressed={!!target?.rejected}
          aria-keyshortcuts="X"
          data-testid="reject-btn"
        >
          {target?.rejected ? 'Rejected' : 'Reject'}
        </Button>
        <div className="w-px h-[22px] bg-line-1 mx-1.5" aria-hidden="true" />
        <Button
          variant={comparing ? 'pick' : 'ghost'}
          icon="compare"
          kbd="C"
          onClick={() => p.onCompare(!comparing)}
          aria-pressed={comparing}
          aria-keyshortcuts="C"
          data-testid="compare-btn"
        >
          Compare
        </Button>
        {!video && (
          <Button
            variant={p.zoom > 1 ? 'pick' : 'ghost'}
            icon="zoom"
            kbd="Z"
            onClick={() => p.onView(p.zoom > 1 ? viewAt(MIN_ZOOM, p.pan) : viewAt(PEEP_ZOOM, p.pan))}
            disabled={comparing}
            aria-pressed={p.zoom > 1}
            aria-keyshortcuts="Z"
            data-testid="zoom-btn"
          >
            Pixel-peep
          </Button>
        )}
        <span className="flex-1" />
        <Button variant="ghost" icon="arrow-left" kbd="Esc" onClick={p.onBack} aria-keyshortcuts="Escape">
          Back
        </Button>
      </footer>
    </>
  )
}
