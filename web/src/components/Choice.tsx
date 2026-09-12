import { Check, Minus } from 'lucide-react'
import { forwardRef, useEffect, useId, useRef, type InputHTMLAttributes } from 'react'

/**
 * DESIGN.md § Checkboxes and § Radio Buttons.
 *
 * The label is REQUIRED BY THE TYPE, not by convention. An unchecked box has a
 * fill identical to the card behind it, so its 2px border is doing all of
 * the identifying — and that is the exact case §8.2 Controls refuses. The
 * visible label is the control's second indicator, and a checkbox without one
 * is not permitted at all: DESIGN.md says to use an icon button instead. So
 * there is no prop for hiding the label and no way to omit it. That is the
 * §11 move of enforcing a rule by removing the ability to break it.
 *
 * The native input stays in the tree and is what the keyboard and the screen
 * reader talk to; the drawn box is a visual over it. Checked and indeterminate
 * are both drawn with a glyph as well as a fill, so the state survives a
 * reader who cannot tell the two fills apart.
 *
 * Below `md` the hit area is padded out to 44px; the 18px box does not grow.
 */
interface ChoiceBaseProps
  extends Omit<InputHTMLAttributes<HTMLInputElement>, 'className' | 'type' | 'id' | 'children'> {
  /** Already-translated, always visible. */
  readonly label: string
  /** Already-translated secondary line. */
  readonly description?: string
}

export interface CheckboxProps extends ChoiceBaseProps {
  /** Some but not all of a group. Set by the parent; a native input cannot express it as an attribute. */
  readonly indeterminate?: boolean
}

const hitArea = 'flex min-h-touch items-center gap-sm md:min-h-0'

export const Checkbox = forwardRef<HTMLInputElement, CheckboxProps>(function Checkbox(
  { label, description, indeterminate = false, disabled, ...rest },
  ref,
) {
  const id = useId()
  const inner = useRef<HTMLInputElement | null>(null)

  // indeterminate is a property, not an attribute, so it has to be set on the
  // element itself after render.
  useEffect(() => {
    if (inner.current !== null) inner.current.indeterminate = indeterminate
  }, [indeterminate])

  return (
    <label htmlFor={id} className={[hitArea, disabled ? 'cursor-not-allowed opacity-40' : 'cursor-pointer'].join(' ')}>
      <span className="relative inline-flex size-check shrink-0">
        <input
          ref={(el) => {
            inner.current = el
            if (typeof ref === 'function') ref(el)
            else if (ref !== null) ref.current = el
          }}
          id={id}
          type="checkbox"
          disabled={disabled}
          className="peer size-check cursor-pointer disabled:cursor-not-allowed appearance-none rounded-sm border-check border-border-strong bg-surface checked:border-0 checked:bg-control-primary indeterminate:border-0 indeterminate:bg-control-primary"
          {...rest}
        />
        <Check
          aria-hidden="true"
          size={14}
          strokeWidth={2.5}
          className="pointer-events-none absolute inset-0 m-auto hidden text-control-primary-text peer-checked:block peer-indeterminate:hidden"
        />
        <Minus
          aria-hidden="true"
          size={14}
          strokeWidth={2.5}
          className="pointer-events-none absolute inset-0 m-auto hidden text-control-primary-text peer-indeterminate:block"
        />
      </span>
      <span className="flex flex-col">
        <span className="text-body text-text-primary">{label}</span>
        {description !== undefined && (
          <span className="text-body-sm text-text-muted">{description}</span>
        )}
      </span>
    </label>
  )
})

export type RadioProps = ChoiceBaseProps

export const Radio = forwardRef<HTMLInputElement, RadioProps>(function Radio(
  { label, description, disabled, ...rest },
  ref,
) {
  const id = useId()
  return (
    <label htmlFor={id} className={[hitArea, disabled ? 'cursor-not-allowed opacity-40' : 'cursor-pointer'].join(' ')}>
      <span className="relative inline-flex size-check shrink-0">
        <input
          ref={ref}
          id={id}
          type="radio"
          disabled={disabled}
          className="peer size-check cursor-pointer disabled:cursor-not-allowed appearance-none rounded-full border-check border-border-strong bg-surface checked:border-control-primary"
          {...rest}
        />
        <span
          aria-hidden="true"
          className="pointer-events-none absolute inset-0 m-auto hidden size-radio-dot rounded-full bg-control-primary peer-checked:block"
        />
      </span>
      <span className="flex flex-col">
        <span className="text-body text-text-primary">{label}</span>
        {description !== undefined && (
          <span className="text-body-sm text-text-muted">{description}</span>
        )}
      </span>
    </label>
  )
})
