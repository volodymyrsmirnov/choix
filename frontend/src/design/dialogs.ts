// Native alert copy (dialogs.json). The approved copy is the shipped copy:
// the screens render AlertReplica from this manifest, and Go's
// internal/app/dialogs shows the real NSAlert from the same file.
import manifest from './dialogs.json'

export interface DialogButton { label: string; role: 'default' | 'cancel' | 'destructive' }
export interface DialogDef { title: string; message: string; buttons: DialogButton[] }

export const DIALOGS = manifest as Record<string, DialogDef>

/** A dialog's copy with `{key}` placeholders filled from `vars`. */
export function dialog(id: string, vars: Record<string, string> = {}): DialogDef {
  const d = DIALOGS[id]
  if (!d) throw new Error(`dialogs.json has no dialog "${id}"`)
  const fill = (s: string) => s.replace(/\{(\w+)\}/g, (m, k: string) => vars[k] ?? m)
  return { title: fill(d.title), message: fill(d.message), buttons: d.buttons.map((b) => ({ ...b })) }
}
