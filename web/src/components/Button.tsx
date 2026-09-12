import { Loader2 } from 'lucide-react'
import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from 'react'

/**
 * DESIGN.md § Buttons.
 *
 * Four variants and three sizes, each a whole class list rather than a name
 * assembled from props, so Tailwind sees every utility in the source.
 *
 * Destructive fills with the error CONTENT value, not the fill value: white on
 * the fill is 3.76:1 and fails; on the content value it is 6.47:1. That is the
 * two-role rule applied to a surface that carries a label.
 *
 * The focus ring on the two dark fills is the inverse ring — the standard one
 * would sit at 1:1 against `#0F172A` and vanish exactly where a keyboard user
 * needs it most (§8.2 Focus).
 *
 * A loading button stays the same size: the spinner replaces the leading icon
 * slot rather than being added to it, so a form does not jump when submitted.
 * It is also disabled, because a second submission of a mutation is the thing
 * idempotency keys exist to survive and the thing a person never means.
 */
export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'destructive'
export type ButtonSize = 'sm' | 'md' | 'lg'

const variantClass: Readonly<Record<ButtonVariant, string>> = {
  primary:
    'bg-control-primary text-control-primary-text hover:bg-control-primary-hover focus-inverse',
  secondary:
    'border border-text-primary bg-transparent text-text-primary hover:bg-control-secondary-hover',
  ghost: 'bg-transparent text-text-muted hover:bg-surface-sunken',
  destructive:
    'bg-control-destructive text-control-primary-text hover:bg-control-destructive-hover focus-inverse',
}

// Below md every size meets the 44px touch minimum by padding its hit area,
// not by growing visually — so min-h-touch here and the true height at md.
const sizeClass: Readonly<Record<ButtonSize, string>> = {
  sm: 'min-h-touch md:min-h-btn-sm px-btn-sm-inline py-btn-sm-block text-body-sm',
  md: 'min-h-touch md:min-h-btn-md px-btn-md-inline py-btn-md-block text-body-sm',
  lg: 'min-h-touch md:min-h-btn-lg px-btn-lg-inline py-btn-lg-block text-body',
}

export interface ButtonProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'className' | 'children'> {
  readonly variant?: ButtonVariant
  readonly size?: ButtonSize
  /** Already-translated label. Always visible; an icon-only button is not this component. */
  readonly children: ReactNode
  /** A 24px icon before the label. Decorative; the label carries the meaning. */
  readonly icon?: ReactNode
  /** Replaces the icon slot with a spinner and disables the button. */
  readonly loading?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'primary', size = 'md', children, icon, loading = false, disabled, type = 'button', ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={[
        // leading-control, not the step's own: DESIGN.md § Buttons makes the
        // heights the specification and derives the label's leading from them.
        // The height is a minimum — a wrapped label grows the button rather
        // than being ellipsised — which is why every size uses min-h.
        'inline-flex items-center justify-center gap-sm rounded font-medium leading-control',
        'transition-colors duration-instant ease-standard',
        'disabled:cursor-not-allowed disabled:opacity-40',
        variantClass[variant],
        sizeClass[size],
      ].join(' ')}
      {...rest}
    >
      {loading ? (
        // Pulses rather than spins: the pulse is the one motion DESIGN.md
        // specifies, it runs on --motion-pulse, and both reduced-motion routes
        // collapse it to a still icon. aria-busy and the disabled state carry
        // the meaning; the motion is only company.
        <Loader2 aria-hidden="true" size={size === 'lg' ? 24 : 20} strokeWidth={2} className="animate-pulse" />
      ) : (
        icon !== undefined && (
          <span aria-hidden="true" className="inline-flex">
            {icon}
          </span>
        )
      )}
      {children}
    </button>
  )
})
