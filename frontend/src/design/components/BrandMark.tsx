import markUrl from '../assets/mark.svg?url'

/** The app mark (assets/icon/favicon.svg). Decorative: text beside it names the app. */
export function BrandMark({ size = 20 }: { size?: number }) {
  return (
    <img
      src={markUrl}
      alt=""
      aria-hidden="true"
      width={size}
      height={size}
      className="block flex-shrink-0"
      draggable={false}
    />
  )
}

export { markUrl }
