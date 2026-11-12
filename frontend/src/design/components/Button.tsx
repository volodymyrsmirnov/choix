import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { Icon, type IconName } from './Icon'
import { Kbd } from './Kbd'
import { Spinner } from './Spinner'

export type ButtonVariant = 'default' | 'ghost' | 'primary' | 'pick' | 'reject'

// v1's .btn family (base.css). 'pick' and 'reject' are the toggled looks of
// Focus's Pick / Reject buttons (v1 .btn-pick-active / .btn-reject-active).
const VARIANT: Record<ButtonVariant, string> = {
  default: 'btn',
  ghost: 'btn btn-ghost',
  primary: 'btn btn-primary',
  pick: 'btn btn-pick-active',
  reject: 'btn btn-reject-active',
}

export interface ButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children' | 'onClick'> {
  variant: ButtonVariant
  icon?: IconName
  /** Shortcut hint shown as a key cap after the label, e.g. `⌘P`. */
  kbd?: string
  disabled?: boolean
  /** Work in flight: a spinner replaces the icon and clicks are ignored. The
   *  button keeps focus (aria-disabled, not disabled). */
  busy?: boolean
  onClick: () => void
  children: ReactNode
  /** Prototype only: `hover`, `focus`, `active` rendered as data-force. */
  force?: string
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant, icon, kbd, disabled, busy, onClick, children, force, className, type = 'button', ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={`${VARIANT[variant]} ${className ?? ''}`}
      data-force={force}
      disabled={disabled}
      aria-busy={busy ? 'true' : undefined}
      aria-disabled={busy ? 'true' : undefined}
      onClick={busy ? undefined : onClick}
      {...rest}
    >
      {busy ? <Spinner small decorative /> : icon && <Icon name={icon} />}
      {children}
      {kbd && <Kbd hidden>{kbd}</Kbd>}
    </button>
  )
})
