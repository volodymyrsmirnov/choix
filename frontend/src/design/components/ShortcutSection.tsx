import { Fragment } from 'react'
import { noOrphan } from '../format'
import { Kbd } from './Kbd'

/**
 * One shortcut: `keys` are alternatives (joined with "or"), each a set of
 * related keys shown side by side with "/" (e.g. ← / →). A row with no keys
 * describes a mouse action.
 */
export interface ShortcutRow {
  keys: string[][]
  label: string
  note?: string
}

export interface ShortcutGroup {
  id: string
  title: string
  rows: ShortcutRow[]
}

const NBSP = ' '

/**
 * A row's note, after its label: " — note". The dash stays with the label's
 * last word; a short note (three words or fewer) never breaks, so a wrapped
 * row does not end on "to 4".
 */
export function noteText(note: string): string {
  const short = note.split(' ').length <= 3
  return short ? `${NBSP}—${NBSP}${note.replace(/ /g, NBSP)}` : `${NBSP}— ${noOrphan(note)}`
}

function Keys({ keys }: { keys: string[][] }) {
  return (
    <span className="flex items-center gap-1 flex-shrink-0 text-11 text-fg-3">
      {keys.map((alt, i) => (
        <Fragment key={i}>
          {i > 0 && <span className="px-0.5">or</span>}
          {alt.map((k, j) => (
            <Fragment key={k}>
              {j > 0 && <span aria-hidden="true">/</span>}
              <Kbd>{k}</Kbd>
            </Fragment>
          ))}
        </Fragment>
      ))}
    </span>
  )
}

/**
 * A titled list of shortcuts in the shortcut sheet (v1 Shortcuts.tsx
 * GroupView). The current screen's group is tinted and says so.
 */
export function ShortcutSection({ group, current, nested }: { group: ShortcutGroup; current: boolean; nested?: boolean }) {
  const titleId = `shortcuts-group-${group.id}`
  return (
    <section aria-labelledby={titleId} className={nested ? 'mt-3' : ''} data-shortcuts-group={group.id}>
      <h3 id={titleId} className={`eyebrow m-0 mb-1.5 flex items-center gap-2 ${current ? 'text-pick' : ''}`}>
        {group.title}
        {current && <span className="normal-case tracking-normal font-sans font-normal text-fg-2">· this screen</span>}
      </h3>
      <ul className="list-none m-0 p-0 flex flex-col">
        {group.rows.map((r) => (
          <li key={r.label} className="flex items-center gap-3 py-1 border-b border-line-1 last:border-b-0">
            <span className="flex-1 min-w-0 text-12 text-fg-1">
              {noOrphan(r.label)}
              {r.note && <span className="text-11 text-fg-3">{noteText(r.note)}</span>}
            </span>
            {r.keys.length > 0 && <Keys keys={r.keys} />}
          </li>
        ))}
      </ul>
    </section>
  )
}
