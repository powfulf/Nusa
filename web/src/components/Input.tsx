import { forwardRef, useId, type InputHTMLAttributes, type ReactNode } from 'react'

/**
 * DESIGN.md § Inputs.
 *
 * The visible label is mandatory, and that is the whole reason this component
 * exists rather than a bare <input>. A field on a white card has a fill
 * identical to its background, so its border is doing no identifying work; the
 * label is the control's real marker (§8.2 Controls). A placeholder is not a
 * label — it disappears exactly when the reader needs it — so there is no
 * placeholder prop.
 *
 * Error is never signalled by border colour alone: when `error` is given the
 * text is rendered, associated through aria-describedby, and announced. The
 * same association carries `helper` when there is no error, so a field never
 * has two descriptions competing.
 *
 * Focus is the field's own treatment from DESIGN.md — the border darkens to
 * the primary colour, reads as 2px, and gains a 3px soft halo — rather than
 * the global outline every other control gets. The darkened border is the
 * indicator that holds 3:1; the halo is decoration. Both are in `.field` in
 * index.css, with the reason the border can appear to grow without moving a
 * single sibling.
 */
export interface InputProps
  extends Omit<InputHTMLAttributes<HTMLInputElement>, 'className' | 'placeholder' | 'id'> {
  /** Already-translated label text. Rendered visibly, always. */
  readonly label: string
  /** Already-translated helper text, shown when there is no error. */
  readonly helper?: string
  /** Already-translated error text. Its presence is what puts the field in error. */
  readonly error?: string
  /** Tabular figures and inline-end alignment, for amounts and other numbers. */
  readonly numeric?: boolean
  /** Rendered inside the field at the inline end — a unit, a code. Decorative. */
  readonly adornment?: ReactNode
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { label, helper, error, numeric = false, adornment, disabled, ...rest },
  ref,
) {
  const id = useId()
  const describedBy = error !== undefined ? `${id}-error` : helper !== undefined ? `${id}-helper` : undefined

  return (
    <div className="flex flex-col">
      <label htmlFor={id} className="mb-input-label text-body-sm font-medium text-text-primary">
        {label}
      </label>

      <div className="relative flex items-center">
        <input
          ref={ref}
          id={id}
          disabled={disabled}
          aria-invalid={error !== undefined}
          aria-describedby={describedBy}
          className={[
            // .field carries the border, hover, focus and error treatment —
            // see index.css for why it is CSS rather than utilities.
            'field w-full rounded bg-surface text-body text-text-primary',
            // 42px tall at md and above; the 44px touch minimum governs below.
            'min-h-touch md:min-h-input px-input-inline py-input-block',
            numeric ? 'numeric' : '',
            adornment !== undefined ? 'pe-2xl' : '',
          ].join(' ')}
          {...rest}
        />
        {adornment !== undefined && (
          <span
            aria-hidden="true"
            className="pointer-events-none absolute end-0 pe-input-inline text-body-sm text-text-muted"
          >
            {adornment}
          </span>
        )}
      </div>

      {error !== undefined ? (
        <p id={`${id}-error`} role="alert" className="mt-xs text-caption text-error-content">
          {error}
        </p>
      ) : (
        helper !== undefined && (
          <p id={`${id}-helper`} className="mt-xs text-caption text-text-muted">
            {helper}
          </p>
        )
      )}
    </div>
  )
})
