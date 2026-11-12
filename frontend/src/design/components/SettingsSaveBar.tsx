import { noOrphan } from '../format'
import { Button } from './Button'

/**
 * Settings' save bar (v1 Settings.tsx): a note on what saving will do (a
 * polite live region: it changes when the outcome changes, not on every
 * keystroke), then Restore defaults, Revert to saved, Cancel and Save.
 * Restore defaults only fills the form; nothing is written until Save.
 */
export function SettingsSaveBar({
  note,
  invalid,
  dirty,
  atDefaults,
  saving,
  onRestoreDefaults,
  onRevert,
  onCancel,
  onSave,
}: {
  note: string | null
  invalid: boolean
  dirty: boolean
  atDefaults: boolean
  saving: boolean
  onRestoreDefaults: () => void
  onRevert: () => void
  onCancel: () => void
  onSave: () => void
}) {
  return (
    <div className="flex flex-col gap-2 pt-4 border-t border-line-1">
      <p role="status" className={`m-0 text-11 leading-[1.5] min-h-[1.5em] ${invalid && dirty ? 'text-reject' : 'text-fg-2'}`}>
        {note ? noOrphan(note) : null}
      </p>
      <div className="flex gap-2">
        <Button
          variant="ghost"
          onClick={onRestoreDefaults}
          disabled={atDefaults || saving}
          title="Fill the form with the built-in defaults. Nothing changes until you save."
        >
          Restore defaults
        </Button>
        <Button
          variant="ghost"
          onClick={onRevert}
          disabled={!dirty || saving}
          title="Undo unsaved edits and go back to the last saved values"
        >
          Revert to saved
        </Button>
        <span className="flex-1" />
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="primary" onClick={onSave} disabled={!dirty || invalid} busy={saving} data-testid="save">
          {saving ? 'Saving…' : 'Save changes'}
        </Button>
      </div>
    </div>
  )
}
