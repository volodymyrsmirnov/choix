// The prototype's own chrome (not part of the app): the grouped list of
// states with search, previous/next (Alt+↑ / Alt+↓), Copy link, and the
// size, theme and OS toggles. Everything here carries data-proto-chrome.
import { useEffect, useMemo, useState } from 'react'
import { formatLocation, locationHref, shareUrl, SIZE_PX, SIZES, type Toggles } from '../location'
import { GROUPS, type Scenario } from '../registry'
import { search } from './search'

const CHROME_BUTTON =
  'h-7 px-2.5 rounded-md border border-line-1 bg-bg-2 text-12 text-fg-1 hover:bg-bg-3 hover:text-fg-0 outline outline-2 outline-offset-1 outline-transparent focus-visible:outline-sel cursor-pointer'

export function Navigator({
  scenarios,
  current,
  toggles,
  onGo,
  onToggles,
}: {
  scenarios: Scenario[]
  current: number | null
  toggles: Toggles
  onGo: (n: number) => void
  onToggles: (t: Partial<Toggles>) => void
}) {
  const [query, setQuery] = useState('')
  const [copied, setCopied] = useState<string | null>(null)
  const results = useMemo(() => search(query, scenarios), [query, scenarios])
  const href = formatLocation(current, toggles)
  const path = window.location.pathname
  const link = shareUrl(window.location.origin, path, current, toggles)

  // Alt+↓ / Alt+↑: next / previous state, in number order.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!e.altKey || (e.key !== 'ArrowDown' && e.key !== 'ArrowUp')) return
      // Leave keys the app (or a control) already handled, and keys typed
      // inside the app surface, to the app.
      if (e.defaultPrevented) return
      if (e.target instanceof Element && e.target.closest('[data-app-root]')) return
      const i = scenarios.findIndex((s) => s.n === current)
      const next = scenarios[e.key === 'ArrowDown' ? Math.min(scenarios.length - 1, i + 1) : Math.max(0, i - 1)]
      if (!next || next.n === current) return
      e.preventDefault()
      onGo(next.n)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [scenarios, current, onGo])

  // Keep the current state in view in the list.
  useEffect(() => {
    document.querySelector(`[data-proto-state="${current}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [current])

  const copy = () => {
    // Built from the current state and toggles, not read from the address bar.
    void navigator.clipboard?.writeText(link).then(() => setCopied(link))
  }

  return (
    <nav
      data-proto-chrome
      aria-label="Prototype states"
      className="w-[300px] flex-shrink-0 h-screen flex flex-col border-r border-line-1 bg-bg-0 text-fg-1 font-sans text-12"
    >
      <div data-proto-chrome className="p-3 flex flex-col gap-2.5 border-b border-line-1">
        <div className="flex items-baseline justify-between">
          <span className="text-13 font-semibold text-fg-0">Choix prototype</span>
          <span className="font-mono text-10 text-fg-3">{scenarios.length} states</span>
        </div>
        <input
          type="search"
          aria-label="Search states by word or number"
          placeholder="Search: “empty”, “102”, “s102”"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && results[0]) onGo(results[0].n)
          }}
          className="h-7 px-2 rounded-md border border-line-1 bg-bg-2 text-12 text-fg-0 placeholder:text-fg-3 outline outline-2 outline-offset-1 outline-transparent focus-visible:outline-sel"
        />
        <div className="flex items-center gap-1.5" role="group" aria-label="Window size">
          {SIZES.map((s) => (
            <button
              key={s}
              type="button"
              aria-pressed={toggles.size === s}
              onClick={() => onToggles({ size: s })}
              className={`${CHROME_BUTTON} flex-1 px-1 ${toggles.size === s ? 'bg-bg-4 text-fg-0 border-line-2' : ''}`}
              title={`${SIZE_PX[s][0]} × ${SIZE_PX[s][1]}`}
            >
              {s} <span className="font-mono text-10 text-fg-3">{SIZE_PX[s][0]}</span>
            </button>
          ))}
        </div>
        <div className="flex items-center gap-1.5">
          <label className="flex items-center gap-1 text-fg-2">
            Theme
            <select
              value={toggles.theme}
              onChange={() => onToggles({ theme: 'dark' })}
              className="h-7 rounded-md border border-line-1 bg-bg-2 text-12 text-fg-0 px-1"
            >
              <option value="dark">dark</option>
            </select>
          </label>
          <label className="flex items-center gap-1 text-fg-2">
            OS
            <select
              value={toggles.os}
              onChange={() => onToggles({ os: 'macos' })}
              className="h-7 rounded-md border border-line-1 bg-bg-2 text-12 text-fg-0 px-1"
            >
              <option value="macos">macOS</option>
            </select>
          </label>
          <button type="button" onClick={copy} className={`${CHROME_BUTTON} ml-auto`}>
            {copied === link ? 'Copied' : 'Copy link'}
          </button>
        </div>
        <div className="flex items-center gap-3 text-fg-2">
          <label className="flex items-center gap-1">
            <input type="checkbox" checked={toggles.freeze} onChange={(e) => onToggles({ freeze: e.target.checked })} />
            freeze
          </label>
          <label className="flex items-center gap-1">
            <input type="checkbox" checked={toggles.nowrapfx} onChange={(e) => onToggles({ nowrapfx: e.target.checked })} />
            no text-wrap
          </label>
          <a
            className="ml-auto text-sel hover:underline"
            href={locationHref(path, current, { ...toggles, bare: true })}
            target="_blank"
            rel="noreferrer"
          >
            bare ↗
          </a>
        </div>
        <span className="font-mono text-10 text-fg-3">Alt+↑ / Alt+↓: previous / next state · {href}</span>
      </div>
      <div data-proto-chrome className="flex-1 overflow-y-auto py-2">
        {GROUPS.map((g) => {
          const items = results.filter((s) => s.group === g.id)
          if (items.length === 0) return null
          return (
            <section key={g.id} aria-label={g.title} className="mb-2">
              <h2 className="m-0 px-3 py-1 font-mono text-10 uppercase tracking-[0.08em] text-fg-3">
                {g.title} <span className="normal-case">{g.range[0]}–{g.range[1]}</span>
              </h2>
              <ul className="m-0 p-0 list-none">
                {items.map((s) => (
                  <li key={s.n}>
                    <a
                      data-proto-state={s.n}
                      href={locationHref(path, s.n, toggles)}
                      aria-current={s.n === current ? 'page' : undefined}
                      onClick={(e) => {
                        if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
                        e.preventDefault()
                        onGo(s.n)
                      }}
                      className={`flex gap-2 px-3 py-1 no-underline leading-snug outline-none focus-visible:bg-bg-3 ${
                        s.n === current ? 'bg-sel-bg text-fg-0' : 'text-fg-1 hover:bg-bg-2'
                      }`}
                    >
                      <span className="font-mono text-11 text-fg-3 w-8 flex-shrink-0">s{s.n}</span>
                      <span>{s.label}</span>
                    </a>
                  </li>
                ))}
              </ul>
            </section>
          )
        })}
        {results.length === 0 && <p className="m-0 px-3 py-2 text-fg-3">No state matches “{query}”.</p>}
      </div>
    </nav>
  )
}
