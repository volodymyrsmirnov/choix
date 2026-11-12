import type { ReactNode } from 'react'
import { baseName, imageLabel, noOrphan } from '../format'
import type { MediaState, MediaURLs, Member } from '../types'
import { Icon } from './Icon'
import { Spinner } from './Spinner'

/**
 * One member's media on the Focus stage or in a compare pane (v1
 * CrossfadeImage). A photo stacks the thumb tier and the full image in the
 * same box, both object-contain so they letterbox alike. While `loading`, a
 * spinner lies under them, on the pane's opaque background, so it shows
 * until the thumbnail paints over it; once `ready` it is gone. The full image is mounted from
 * the start at opacity 0: its load and decode report through `onState`
 * (`ready`, `error`), and on `ready` it fades in over the thumbnail. `error`
 * keeps the thumbnail, dimmed, under a notice.
 *
 * The layers are keyed by file id and content hash (v1), so moving to
 * another photo replaces them instead of animating one photo into the next.
 *
 * Zoom and pan apply to both layers as one transform. The transform is a
 * measured value (data-measured, src/design/measured.ts): pinch and ⌘-wheel
 * produce any zoom from 1 to 8 and a drag any pan.
 *
 * A video is a <video preload="none"> with its keyframe as the poster and
 * native controls; it never autoplays, never zooms, and stays out of the Tab
 * order (v1: Space and the arrows belong to Focus, not to the player).
 */
export function FocusMedia({
  member,
  media,
  state,
  zoom,
  pan,
  variant,
  onState,
}: {
  member: Member
  media: MediaURLs
  state: MediaState
  zoom: number
  pan: { x: number; y: number }
  variant: 'hero' | 'compare'
  onState: (state: Exclude<MediaState, 'loading'>) => void
}) {
  const name = imageLabel(member)
  const key = `${member.file_id}-${member.content_hash}`
  // The images are transparent where they letterbox: the opaque background
  // (a compare pane's) is the wrapper's, beneath the spinner.
  const fill = 'absolute inset-0 w-full h-full object-contain'
  // v1 CrossfadeImage's placeholder layer is the thumb tier.
  const thumb = media.thumb(member, 'thumb')
  const spinner = state === 'loading' && (
    <div className="absolute inset-0 flex items-center justify-center pointer-events-none" aria-hidden="true" data-testid="media-spinner">
      <Spinner decorative />
    </div>
  )

  let layers: ReactNode
  if (member.kind === 'video') {
    layers = (
      <>
        {spinner}
        <img key={`thumb-${key}`} src={thumb} alt="" aria-hidden="true" draggable={false} className={`${fill} pointer-events-none`} />
        <video
          key={`video-${key}`}
          src={media.full(member)}
          poster={media.thumb(member, 'preview')}
          preload="none"
          controls
          playsInline
          draggable={false}
          tabIndex={-1}
          aria-label={name}
          onFocus={(e) => e.currentTarget.blur()}
          className={fill}
          data-testid={variant === 'hero' ? 'hero-video' : undefined}
        />
      </>
    )
  } else {
    const transform = `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`
    const layer = `${fill} pointer-events-none origin-center group-active:duration-0`
    const ready = state === 'ready'
    layers = (
      <>
        {spinner}
        <img
          key={`thumb-${key}`}
          src={thumb}
          alt={ready ? '' : name}
          draggable={false}
          data-testid={variant === 'hero' ? 'hero-thumb' : undefined}
          className={`${layer} transition-[transform,opacity] duration-fast`}
          style={{ transform, opacity: ready ? 0 : state === 'error' ? 0.35 : 1 }}
          data-measured="transform"
          data-measured-by="focus-zoom"
        />
        <img
          key={`full-${key}`}
          src={media.full(member)}
          alt={ready ? name : ''}
          aria-hidden={ready ? undefined : true}
          draggable={false}
          onLoad={() => onState('ready')}
          onError={() => onState('error')}
          className={`${layer} transition-opacity duration-base`}
          style={{ transform, opacity: ready ? 1 : 0 }}
          data-measured="transform"
          data-measured-by="focus-zoom"
          data-testid={variant === 'hero' ? 'hero' : undefined}
        />
        {state === 'error' && <MediaError member={member} compact={variant === 'compare'} />}
      </>
    )
  }

  if (variant === 'hero') return <div className="relative w-full h-full">{layers}</div>
  return <div className="absolute inset-0 bg-bg-0">{layers}</div>
}

function MediaError({ member, compact }: { member: Member; compact: boolean }) {
  return (
    <div className="absolute inset-0 flex items-center justify-center p-6 pointer-events-none" data-testid="media-error">
      <div
        role="status"
        className={`flex items-start gap-2.5 rounded-lg border border-line-2 bg-toast shadow-pop ${compact ? 'max-w-[260px] p-2.5' : 'max-w-[360px] p-3'}`}
      >
        <Icon name="alert" className="text-warn mt-px" />
        <div className="min-w-0">
          <p className="m-0 text-12 font-semibold text-fg-0">{noOrphan('The full-size image could not be shown')}</p>
          <p className="m-0 mt-0.5 text-11 text-fg-2 leading-snug break-words">
            {noOrphan(`${baseName(member.path)} did not decode, so this is its thumbnail. The original file is untouched.`)}
          </p>
        </div>
      </div>
    </div>
  )
}
