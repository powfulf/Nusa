import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { formatMoney } from '../money/format'
import { Money as MoneyValue } from '../money/money'
import { MoneyParseError, PrecisionError, parseMoney } from '../money/parse'
import { useMoneyContext } from '../money/context'

export interface MoneyInputProps {
  /** Message-catalogue key for the visible label. Never a literal string (§9). */
  readonly labelKey: string
  readonly commodity: string
  readonly value: MoneyValue | null
  readonly onChange: (value: MoneyValue | null) => void
  readonly name?: string
  readonly required?: boolean
  readonly disabled?: boolean
}

interface Parsed {
  readonly money: MoneyValue | null
  readonly errorKey: string | null
}

/**
 * An amount field that accepts what a person actually types.
 *
 * "1500000", "1.5jt", "1,5 juta" and a pasted "Rp 1.500.000" all mean the same
 * thing, and all of them arrive as the same `bigint` of minor units. The typed
 * text is never converted through a `number` on the way (§4.1) and is never
 * rounded (§4.6): more decimal places than the commodity holds is refused, not
 * silently changed into a different amount.
 *
 * THE ECHO IS THE SAFETY NET, and it is why the separator rules are allowed to
 * be locale-dependent at all. "1.500" is fifteen hundred to an Indonesian
 * reader and one and a half to an American one; both readings are defensible
 * and the locale picks. What makes that safe rather than alarming is that the
 * field prints back what it understood, formatted, underneath — so a wrong
 * reading is visible before it is saved rather than discovered in a balance.
 *
 * DESIGN.md § Inputs governs the chrome. The visible label is mandatory: the
 * field's fill is identical to the card behind it, so its border does no
 * identifying work and the label is the control's real marker. A placeholder
 * is not a label — it disappears exactly when it is needed.
 *
 * The error is never signalled by border colour alone; the error text is
 * present and associated for assistive technology whenever the field is in
 * error.
 */
export function MoneyInput({
  labelKey,
  commodity,
  value,
  onChange,
  name,
  required,
  disabled,
}: MoneyInputProps) {
  const { t } = useTranslation()
  const { registry, locale } = useMoneyContext()
  const id = useId()

  // The text is what the person typed; `value` is what it meant. Keeping both
  // means an in-progress "1," is not reformatted out from under them mid-word.
  const [text, setText] = useState(() =>
    value === null ? '' : formatMoney(value, registry, { locale, withSymbol: false }),
  )
  const [errorKey, setErrorKey] = useState<string | null>(null)

  function read(next: string): Parsed {
    if (next.trim() === '') return { money: null, errorKey: null }
    try {
      return { money: parseMoney(next, commodity, registry, locale), errorKey: null }
    } catch (error) {
      // Errors carry a stable code and the component renders from that. §9
      // forbids showing `message`, which is developer-facing English.
      if (error instanceof PrecisionError || error instanceof MoneyParseError) {
        return { money: null, errorKey: error.code }
      }
      throw error
    }
  }

  function handle(next: string) {
    setText(next)
    const { money, errorKey: nextError } = read(next)
    setErrorKey(nextError)
    onChange(money)
  }

  const parsed = read(text)
  const scale = registry.has(commodity) ? registry.scaleOf(commodity) : 0
  const errorId = `${id}-error`
  const echoId = `${id}-echo`

  // Shown only when the field is understood and not empty: echoing nothing, or
  // echoing a value the field just rejected, would be noise exactly when the
  // reader needs the message instead.
  const echo =
    parsed.errorKey === null && parsed.money !== null
      ? formatMoney(parsed.money, registry, { locale })
      : null

  return (
    <div className="flex flex-col">
      <label htmlFor={id} className="mb-xs text-body-sm font-medium text-text-primary">
        {t(labelKey)}
      </label>

      <input
        id={id}
        name={name}
        type="text"
        inputMode="decimal"
        autoComplete="off"
        required={required}
        disabled={disabled}
        value={text}
        onChange={(event) => handle(event.target.value)}
        aria-invalid={errorKey !== null}
        aria-describedby={errorKey !== null ? errorId : echo !== null ? echoId : undefined}
        className={[
          'numeric min-h-touch rounded border bg-surface px-md py-sm text-body text-text-primary md:min-h-0',
          'disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:opacity-40',
          errorKey !== null ? 'border-error-content' : 'border-border-subtle',
        ].join(' ')}
      />

      {errorKey !== null && (
        <p id={errorId} role="alert" className="mt-xs text-caption text-error-content">
          {t(errorKey, { commodity, scale })}
        </p>
      )}

      {errorKey === null && echo !== null && (
        <p id={echoId} className="mt-xs text-caption text-text-muted">
          {t('money.reads_as', { value: echo })}
        </p>
      )}
    </div>
  )
}
