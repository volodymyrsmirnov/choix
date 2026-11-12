/**
 * A month on the Library timeline (Layout A): a mono eyebrow, e.g.
 * `OCTOBER 2026`, with a right-aligned count of what it holds.
 */
export function MonthHeader({ label, count, first }: { label: string; count: string; first?: boolean }) {
  return (
    <div data-month className={`flex items-baseline gap-2.5 mx-0.5 mb-2 ${first ? 'mt-4' : 'mt-[22px]'}`}>
      <h2 className="eyebrow m-0">{label}</h2>
      <span className="ml-auto font-mono text-11 text-fg-3 whitespace-nowrap">{count}</span>
    </div>
  )
}
