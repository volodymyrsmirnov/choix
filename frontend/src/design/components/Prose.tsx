import { noOrphan } from '../format'

/**
 * A run of copy. Binds its last two words (noOrphan) so a wrapped block never
 * ends on one short word; `wrap` adds CSS text-wrap where the engine has it.
 */
export function Prose({
  children,
  wrap,
  as: Tag = 'p',
  className,
  id,
}: {
  children: string
  wrap?: 'balance' | 'pretty'
  as?: 'p' | 'span' | 'div'
  className?: string
  id?: string
}) {
  return (
    <Tag id={id} data-wrap={wrap} className={`m-0 ${className ?? ''}`}>
      {noOrphan(children)}
    </Tag>
  )
}
