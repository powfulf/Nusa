import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Input } from './Input'

import { formatMoney, unitFor } from '../money/format'
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
  /**
   * Text to start with when there is no value. The error state of an input is
   * a consequence of what was typed, so a fixture or an end-to-end test that
   * needs to show it has to be able to supply the typing — a value the
   * commodity cannot hold is not a Money, so it cannot arrive through `value`.
   */
  readonly initialText?: string
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
 * The chrome — label, border, focus, error association — is `<Input>`'s, so
 * that every field in the product shares one implementation of DESIGN.md
 * § Inputs. This component owns only what is specific to money: the parser,
 * the echo, the unit adornment, and the refusal to round.
 */
export function MoneyInput({
  labelKey,
  commodity,
  value,
  onChange,
  name,
  required,
  disabled,
  initialText,
}: MoneyInputProps) {
  const { t } = useTranslation()
  const { registry, locale } = useMoneyContext()

  // The text is what the person typed; `value` is what it meant. Keeping both
  // means an in-progress "1," is not reformatted out from under them mid-word.
  const [text, setText] = useState(() =>
    value === null
      ? (initialText ?? '')
      : formatMoney(value, registry, { locale, withSymbol: false }),
  )
  const [errorKey, setErrorKey] = useState<string | null>(() =>
    value === null && initialText !== undefined ? read(initialText).errorKey : null,
  )

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

  // Shown only when the field is understood and not empty: echoing nothing, or
  // echoing a value the field just rejected, would be noise exactly when the
  // reader needs the message instead.
  const echo =
    parsed.errorKey === null && parsed.money !== null
      ? t('money.reads_as', { value: formatMoney(parsed.money, registry, { locale }) })
      : undefined

  return (
    <Input
      label={t(labelKey)}
      name={name}
      type="text"
      inputMode="decimal"
      autoComplete="off"
      required={required}
      disabled={disabled}
      numeric
      // The unit sits inside the field so the reader knows what they are
      // typing into; the echo below carries the fully formatted result.
      adornment={registry.has(commodity) ? unitFor(registry.get(commodity), locale) : commodity}
      value={text}
      onChange={(event) => handle(event.target.value)}
      error={errorKey !== null ? t(errorKey, { commodity, scale }) : undefined}
      helper={echo}
    />
  )
}
