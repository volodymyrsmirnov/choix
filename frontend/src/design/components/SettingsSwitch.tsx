import { Switch } from './Switch'

/**
 * A Settings field's switch (v1 Settings.tsx: Toggle and ToggleState): the
 * switch, named by the field's label `${id}-label` and described by its hint
 * `${id}-hint`, then a visual "On" / "Off" hidden from assistive tech (the
 * switch already announces its state).
 */
export function SettingsSwitch({
  id,
  checked,
  onChange,
  disabled,
  testId,
}: {
  id: string
  checked: boolean
  onChange: (on: boolean) => void
  disabled?: boolean
  testId?: string
}) {
  return (
    <div className="flex items-center gap-3">
      <Switch
        id={id}
        labelledBy={`${id}-label`}
        describedBy={`${id}-hint`}
        checked={checked}
        onChange={onChange}
        disabled={disabled}
        testId={testId}
      />
      <span aria-hidden="true" className={`text-12 text-fg-2 ${disabled ? 'opacity-40' : ''}`}>
        {checked ? 'On' : 'Off'}
      </span>
    </div>
  )
}
