import { useMemo } from 'react'
import {
  BackButton,
  Banner,
  Button,
  ClusterList,
  EmptyState,
  FolderFilterBar,
  FolderScanChecklist,
  IconButton,
  QualityIndicator,
  qualityIndicatorVisible,
  ScanIndicator,
  Spinner,
  TitleBar,
  Toaster,
} from '../../components'
import { plural } from '../../format'
import type { Cluster, FolderSummary, QualityStatus } from '../../types'
import { filterClusters } from './filter'
import { FOLDER_OVERLAYS, type FolderProps } from './props'

/** The title bar's subtitle: the folder's counts (rejects show per cluster),
 *  or where the folder is before it has any. */
function subtitleOf(f: FolderSummary): string {
  if (f.photos + f.videos === 0) return f.parentPath
  const parts = [plural(f.photos, 'photo', 'photos')]
  if (f.videos > 0) parts.push(plural(f.videos, 'video', 'videos'))
  parts.push(plural(f.clusters, 'cluster', 'clusters'))
  parts.push(`${f.picked.toLocaleString('en-US')} picked`)
  return parts.join(' · ')
}

/**
 * The quality worker as it concerns one folder: a rating pass is shown with
 * that folder's own counts (from `passes`, or done/total when they describe
 * it), and not at all when only other folders are being rated. The model
 * download and a failure concern every folder and pass through.
 */
function ownQuality(q: QualityStatus | null, folderId: string): QualityStatus | null {
  if (!q || q.state !== 'scoring') return q
  const pass = q.passes?.find((x) => x.folder_id === folderId)
  if (pass) return { ...q, folder_id: folderId, done: pass.done, total: pass.total }
  return q.folder_id === folderId ? q : null
}

/**
 * The Folder page (spec §4.3): v1's Library cluster grid under a title bar
 * with Back to the Library, the folder's name and counts, the scan indicator
 * while a scan runs, Open Picks (⌘P), the shortcut sheet and Settings.
 * Below it: the loading state, the first scan's stage checklist, "no photos
 * or videos", or the filter bar, the page's notices and the clusters. (No
 * hint row: the owner keeps keyboard help in the shortcut sheet.)
 */
export function FolderScreen(p: FolderProps) {
  const { folder, clusters } = p
  const offline = p.notice === 'offline'
  const firstScan = clusters !== null && clusters.length === 0 && p.scan !== null

  let body
  if (clusters === null) {
    body = (
      <main className="flex-1 min-h-0 flex items-center justify-center">
        <Spinner label="Loading the folder" />
      </main>
    )
  } else if (clusters.length === 0) {
    body = (
      <main className="flex-1 min-h-0 overflow-y-auto" data-scroll>
        <div className="min-h-full flex items-center justify-center">
          {firstScan ? (
            // Clusters are written only by the last stage, after every file
            // has been through the others: nothing is browsable until the
            // whole scan finishes, and the copy promises exactly that.
            <EmptyState
              level={2}
              busy
              title="Scanning your folder"
              body="Reading EXIF, generating thumbnails and grouping similar shots. The folder opens by itself when the scan finishes."
            >
              <FolderScanChecklist scan={p.scan} />
            </EmptyState>
          ) : (
            <EmptyState
              level={2}
              title="No photos or videos found"
              body={`Choix scanned “${folder.name}” and found no supported photos or videos in it. Add some in Finder, then rescan the folder with ⌘R.`}
            />
          )}
        </div>
      </main>
    )
  } else {
    body = <Grid {...p} clusters={clusters} />
  }

  return (
    <div className="relative flex flex-col h-full min-h-0">
      <TitleBar
        leading={<BackButton to="Library" onClick={p.onBack} testId="folder-back" />}
        title={folder.name}
        subtitle={subtitleOf(folder)}
        // Only this folder's work. The first scan's checklist is the
        // progress, so the bar shows nothing then; otherwise a scan of this
        // folder, or the quality worker (v1 AppBar) with this folder's own
        // rating pass only.
        indicator={(() => {
          if (firstScan) return undefined
          if (p.scan) return <ScanIndicator scan={p.scan} />
          const own = ownQuality(p.quality, folder.id)
          return qualityIndicatorVisible(own, false) ? <QualityIndicator status={own} /> : undefined
        })()}
        trailing={
          <>
            <Button variant="default" icon="folder" kbd="⌘P" disabled={offline} onClick={p.onOpenPicks} data-testid="open-picks">
              Open Picks
            </Button>
            <IconButton icon="help" label="Keyboard shortcuts" onClick={p.onHelp} aria-haspopup="dialog" />
            <IconButton icon="gear" label="Settings" onClick={p.onSettings} />
          </>
        }
      />
      {body}
      <Toaster items={p.toasts} onUndo={p.onUndoToast} onDismiss={p.onDismissToast} />
    </div>
  )
}

/**
 * The time-only notice explains what the user did not choose: the model is
 * still on its way, or its download failed. Visual grouping switched off in
 * Settings is the user's choice, so the page says nothing about it (the
 * owner: not put in their face).
 */
const TIME_ONLY: Partial<Record<FolderProps['grouping'], string>> = {
  'time-downloading': 'The visual grouping model is still downloading. When it is ready, Choix regroups this folder so similar shots share a cluster.',
  'time-failed': 'The visual grouping model could not be downloaded, so similar shots taken minutes apart can land in separate clusters. Retry the download in Settings.',
}

function Grid(p: FolderProps & { clusters: Cluster[] }) {
  // On the three fields (v1), not on the filter object's identity.
  const { query, device, sort } = p.filter
  const visible = useMemo(() => filterClusters(p.clusters, { query, device, sort }), [p.clusters, query, device, sort])
  const all = p.clusters.flatMap((c) => c.members)
  const videos = all.filter((m) => m.kind === 'video').length
  const qualityOn = p.settings.quality_rating
  const offline = p.notice === 'offline'
  const drive = p.folder.volumeName ?? p.folder.name

  const notices = [
    offline && (
      <Banner
        key="offline"
        testId="notice-offline"
        tone="warn"
        icon="drive"
        title={`Drive “${drive}” is not connected`}
        body="Choix keeps showing what it already loaded. Reconnect the drive to open photos, pick or re-cluster."
      />
    ),
    p.notice === 'read-only' && (
      <Banner
        key="read-only"
        testId="notice-read-only"
        tone="warn"
        icon="alert"
        title={`“${p.folder.name}” is on a read-only drive`}
        body="Choix can show and review it, but cannot copy picks into it."
      />
    ),
    p.picksMissing > 0 && (
      <Banner
        key="picks-missing"
        testId="notice-picks-missing"
        tone="info"
        icon="folder"
        title={`${plural(p.picksMissing, 'pick is', 'picks are')} missing from “${p.picksDir}”`}
        body="Their copies were moved or deleted outside Choix. The photos stay picked; Choix never copies them back on its own."
        actions={
          <Button variant="default" disabled={offline} onClick={p.onOpenPicks}>
            Open Picks
          </Button>
        }
      />
    ),
    TIME_ONLY[p.grouping] !== undefined && (
      <Banner
        key="time-only"
        testId="notice-time-only"
        tone={p.grouping === 'time-failed' ? 'warn' : 'info'}
        icon={p.grouping === 'time-failed' ? 'alert' : 'info'}
        title="Grouped by capture time only"
        body={TIME_ONLY[p.grouping]}
        actions={
          p.grouping === 'time-failed' ? (
            <Button variant="default" onClick={p.onSettings}>
              Settings
            </Button>
          ) : undefined
        }
      />
    ),
  ].filter(Boolean)

  return (
    <>
      <FolderFilterBar
        filter={p.filter}
        devices={p.devices}
        openMenu={p.openMenu}
        menuIds={FOLDER_OVERLAYS}
        visible={visible.length}
        clusters={p.clusters.length}
        photos={all.length - videos}
        videos={videos}
        failed={p.failedFiles}
        reclustering={p.reclustering}
        reclusterDisabled={offline}
        onFilter={p.onFilter}
        onOpenMenu={p.onOpenMenu}
        onRecluster={p.onRecluster}
      />
      {/* `relative` makes this scroller the containing block of the sr-only
          labels inside it; without it they sit below the viewport and make
          the document scroll past the last cluster (v1). */}
      <main
        className="relative flex-1 min-h-0 overflow-y-auto"
        data-scroll
        data-measured=""
        data-measured-by="cluster-list-scroll"
        data-testid="clusters"
      >
        <div className="px-3.5 pt-4 pb-10 flex flex-col gap-6">
          {notices.length > 0 && <div className="flex flex-col gap-2">{notices}</div>}
          <ClusterList
            clusters={visible}
            activeId={p.selectedClusterId}
            media={p.media}
            qualityOn={qualityOn}
            emptyText="No clusters match your filters."
            openDisabled={offline}
            onSelect={p.onSelectCluster}
            onOpen={p.onOpenCluster}
          />
        </div>
      </main>
    </>
  )
}
