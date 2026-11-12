import { Dot } from './Dot'
import { FOCUS_RING } from './focusRing'

export interface SettingsNavItem {
  id: string
  label: string
  /** The section holds unsaved edits: a pick dot, announced as text. */
  dirty: boolean
}

/**
 * Settings' sidebar (v1 Settings.tsx): an eyebrow, then one button per
 * section with the active one highlighted and a dot on sections with unsaved
 * edits. Buttons rather than #hash links, so a click never touches history.
 * `controls(id)` is the DOM id of the section a button scrolls to.
 */
export function SettingsNav({
  items,
  active,
  controls,
  onSelect,
}: {
  items: SettingsNavItem[]
  active: string
  controls: (id: string) => string
  onSelect: (id: string) => void
}) {
  return (
    <aside className="w-[200px] flex-shrink-0 py-5 px-3 border-r border-line-1">
      <div className="eyebrow px-2 mb-2" aria-hidden="true">
        Settings
      </div>
      <nav aria-label="Settings sections">
        <ul className="m-0 p-0 list-none flex flex-col gap-1">
          {items.map((s) => {
            const isActive = active === s.id
            return (
              <li key={s.id}>
                <button
                  type="button"
                  aria-current={isActive ? 'true' : undefined}
                  aria-controls={controls(s.id)}
                  data-section={s.id}
                  onClick={() => onSelect(s.id)}
                  className={`flex items-center gap-2 w-full text-left px-2 py-1.5 rounded-md text-12 font-medium border-0 cursor-pointer transition-colors ${FOCUS_RING} ${
                    isActive ? 'text-fg-0 bg-bg-3' : 'text-fg-1 bg-transparent hover:bg-bg-2 hover:text-fg-0'
                  }`}
                >
                  <span className="flex-1">{s.label}</span>
                  {s.dirty && (
                    <>
                      <span className="flex" data-testid="section-dirty">
                        <Dot tone="pick" />
                      </span>
                      <span className="sr-only"> (unsaved changes)</span>
                    </>
                  )}
                </button>
              </li>
            )
          })}
        </ul>
      </nav>
    </aside>
  )
}
