import { forwardRef, type ButtonHTMLAttributes } from 'react'
import { Icon, type IconName } from './Icon'

export interface IconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children' | 'onClick'> {
  icon: IconName
  /** Accessible name; also the tooltip unless `title` is given. */
  label: string
  onClick: () => void
  disabled?: boolean
  variant?: 'ghost' | 'default'
  /** Prototype only: `hover`, `focus`, `active` rendered as data-force. */
  force?: string
}

/** An icon-only button (v1 `btn btn-ghost px-2`). */
export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { icon, label, onClick, disabled, variant = 'ghost', force, className, title, type = 'button', ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={`btn btn-icon ${variant === 'ghost' ? 'btn-ghost' : ''} ${className ?? ''}`}
      data-force={force}
      disabled={disabled}
      aria-label={label}
      title={title ?? label}
      onClick={onClick}
      {...rest}
    >
      <Icon name={icon} />
    </button>
  )
})
