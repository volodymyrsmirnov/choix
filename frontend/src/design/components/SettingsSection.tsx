import type { ReactNode } from 'react'
import { Prose } from './Prose'

/**
 * One Settings section (v1 Settings.tsx): a heading, a line saying what the
 * section's fields do, then its fields. The section is `id`, the heading
 * `${id}-title`; the heading takes focus (tabIndex -1, outside the Tab
 * order) when the sidebar moves to the section.
 */
export function SettingsSection({
  id,
  title,
  description,
  children,
}: {
  id: string
  title: string
  description: string
  children: ReactNode
}) {
  return (
    <section id={id} aria-labelledby={`${id}-title`} data-settings-section className="flex flex-col gap-7">
      <div>
        <h2 id={`${id}-title`} tabIndex={-1} className="m-0 text-18 font-semibold tracking-tight outline-none">
          {title}
        </h2>
        <Prose className="mt-1 max-w-[576px] text-fg-2 text-13 leading-[1.5]">{description}</Prose>
      </div>
      {children}
    </section>
  )
}
