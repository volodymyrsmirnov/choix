// The spinner announces itself as "Loading" by default, because it can stand
// alone in an otherwise empty stage. Pass `decorative` where visible text
// already says what is happening. It carries data-indet="true": under reduced
// motion base.css swaps its rotation for a slow opacity pulse.
export function Spinner({ small, decorative, label = 'Loading' }: { small?: boolean; decorative?: boolean; label?: string }) {
  const s = small ? 12 : 32
  const a11y = decorative
    ? { 'aria-hidden': true as const, focusable: 'false' as const }
    : { role: 'img', 'aria-label': label }
  return (
    <svg width={s} height={s} viewBox="0 0 32 32" className="choix-spin flex-shrink-0" data-indet="true" {...a11y}>
      <circle cx="16" cy="16" r="13" fill="none" strokeWidth="3" className="stroke-spinner-track" />
      <path d="M16 3 a13 13 0 0 1 13 13" fill="none" strokeWidth="3" strokeLinecap="round" className="stroke-pick" />
    </svg>
  )
}
