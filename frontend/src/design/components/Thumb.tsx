import { qualityLabel } from '../format'
import { Icon } from './Icon'
import { QualityBadge } from './QualityBadge'

/** A thumbnail's mark. Being the current photo is apart from it (`current`). */
export type ThumbState = 'picked' | 'rejected'

/** The play mark at a video thumbnail's top-right corner (also on Focus' rail). */
export function VideoMark() {
  return (
    <span
      className="absolute top-1 right-1 w-5 h-5 rounded-sm bg-scrim text-fg-0 flex items-center justify-center pointer-events-none"
      aria-hidden="true"
    >
      <Icon name="play" size={10} />
    </span>
  )
}

/**
 * A photo or video thumbnail (v1 .thumb). With `onClick` it is a button whose
 * accessible name is `alt` plus kind, rating and pick state; without, a plain
 * image. Size it with `size` (square, px) or with `className`.
 */
export function Thumb({
  src,
  alt,
  state,
  current,
  quality,
  video,
  size,
  onClick,
  className,
  testId,
  force,
  fileId,
  title,
  disabled,
}: {
  src: string
  alt: string
  state?: ThumbState
  /** The current photo (components/current.ts): the blue look, around the mark if any. */
  current?: boolean
  /** Advisory rating; ignored for videos, which are never rated. */
  quality?: number
  video?: boolean
  size?: number
  onClick?: () => void
  className?: string
  testId?: string
  /** Prototype only: `hover`, `focus`, `active` rendered as data-force. */
  force?: string
  /** v1's data-file-id. */
  fileId?: number
  /** The tooltip: the file's full path (v1). */
  title?: string
  /** A button that cannot be pressed (opening Focus is unavailable). */
  disabled?: boolean
}) {
  const q = !video && quality !== undefined ? quality : undefined
  const overlay = (
    <>
      {video && <VideoMark />}
      {q !== undefined && (
        <QualityBadge value={q} standalone={!onClick} className="absolute top-1 right-1 pointer-events-none" />
      )}
      {state === 'picked' && (
        <span
          className="absolute bottom-1.5 left-1.5 px-1.5 py-0.5 rounded-sm text-11 font-semibold flex items-center gap-1 bg-pick text-pick-ink pointer-events-none"
          aria-hidden="true"
        >
          <Icon name="check" size={10} /> Pick
        </span>
      )}
      {state === 'rejected' && (
        <span
          className="absolute bottom-1.5 left-1.5 px-1.5 py-0.5 rounded-sm text-11 font-semibold bg-reject-badge text-reject-ink pointer-events-none"
          aria-hidden="true"
        >
          Reject
        </span>
      )}
    </>
  )
  const style = size !== undefined ? { width: size, height: size } : undefined

  if (!onClick) {
    return (
      <div
        className={`thumb flex-shrink-0 ${className ?? ''}`}
        data-state={state}
        data-current={current ? 'true' : undefined}
        data-testid={testId}
        data-file-id={fileId}
        title={title}
        style={style}
      >
        <img src={src} alt={alt} draggable={false} />
        {overlay}
      </div>
    )
  }
  const name =
    alt +
    (video ? ', video' : '') +
    (q !== undefined ? `, ${qualityLabel(q)}` : '') +
    (state === 'picked' ? ', picked' : state === 'rejected' ? ', rejected' : '')
  return (
    <button
      type="button"
      onClick={onClick}
      className={`thumb thumb-btn flex-shrink-0 ${className ?? ''}`}
      data-state={state}
      data-current={current ? 'true' : undefined}
      data-testid={testId}
      data-force={force}
      data-file-id={fileId}
      title={title}
      disabled={disabled}
      aria-label={name}
      style={style}
    >
      <img src={src} alt="" aria-hidden="true" draggable={false} />
      {overlay}
    </button>
  )
}
