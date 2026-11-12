// Overlay coverage of a group's flows. The ids the flows must both open and
// close are the union of the `overlays` declared by the group's states and
// every id that registered while the flows ran: replicas and toasts register
// only while open, so `registered` alone under-reports.
export interface OverlayLog {
  registered: string[]
  opened: string[]
  closed: string[]
}

/** One message per id that was not both opened and closed; declared ids first. */
export function overlayCoverage(declared: string[], log: OverlayLog): string[] {
  const ids = [...new Set([...declared, ...log.registered])]
  const out: string[] = []
  for (const id of ids) {
    const opened = log.opened.includes(id)
    const closed = log.closed.includes(id)
    if (opened && closed) continue
    if (opened) out.push(`overlay "${id}" was opened but never closed`)
    else if (closed) out.push(`overlay "${id}" was closed but never opened`)
    else out.push(`overlay "${id}" was never opened or closed`)
  }
  return out
}
