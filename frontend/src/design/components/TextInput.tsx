import { forwardRef, type InputHTMLAttributes, type ReactNode } from 'react'
import { Icon, type IconName } from './Icon'

export interface TextInputProps
  extends Omit<InputHTMLAttributes<HTMLInputElement>, 'onChange' | 'value' | 'size' | 'type' | 'className'> {
  value: string
  onChange: (value: string) => void
  type?: 'text' | 'number' | 'search'
  /** A leading icon inside the box (the Folder page's search field). */
  icon?: IconName
  /** Content after the input inside the box (the search field's ⌘F key cap). */
  trailing?: ReactNode
  invalid?: boolean
  disabled?: boolean
  mono?: boolean
  align?: 'left' | 'right'
  size?: 'sm' | 'md'
  /** Classes for the box (width, margins). */
  className?: string
  /** Accessible name when no <label> points at the input. */
  label?: string
  /** Prototype only: `hover`, `focus-within` rendered as data-force on the box. */
  force?: string
}

/**
 * A text field (v1 `.input`). The bordered box is a wrapper so a leading icon
 * shares its border; focus shows as a pick border and a 3 px pick ring on that
 * wrapper (`:focus-within`), which therefore carries `data-focus-ring`.
 */
export const TextInput = forwardRef<HTMLInputElement, TextInputProps>(function TextInput(
  {
  value,
  onChange,
  type = 'text',
  icon,
  trailing,
  invalid,
  disabled,
  mono,
  align = 'left',
  size = 'md',
  className,
  label,
  force,
  ...rest
  },
  ref,
) {
  return (
    <div
      className={`input ${size === 'sm' ? 'input-sm' : ''} ${className ?? ''}`}
      data-focus-ring
      data-invalid={invalid ? 'true' : undefined}
      data-disabled={disabled ? 'true' : undefined}
      data-force={force}
    >
      {icon && <Icon name={icon} className="text-fg-3" />}
      {/* data-clip: a text field scrolls a value longer than its box, as a
          native field does; that is intended, so the clip check lets it be. */}
      <input
        ref={ref}
        data-clip="intended"
        type={type}
        value={value}
        disabled={disabled}
        aria-label={label}
        aria-invalid={invalid ? true : undefined}
        onChange={(e) => onChange(e.target.value)}
        className={`${mono ? 'font-mono' : ''} ${align === 'right' ? 'text-right' : ''}`}
        spellCheck={false}
        autoCapitalize="off"
        autoCorrect="off"
        {...rest}
      />
      {trailing}
    </div>
  )
})
