export type DotTone = 'neutral' | 'pick' | 'reject' | 'warn' | 'active'

/** A 6 px status dot. Decorative: the text beside it carries the meaning. */
export function Dot({ tone = 'neutral' }: { tone?: DotTone }) {
  return <span className="dot" data-tone={tone === 'neutral' ? undefined : tone} aria-hidden="true" />
}
