import { formatMoney } from '../money/format'
import type { Money as MoneyValue } from '../money/money'
import { useMoneyContext } from '../money/context'

export interface MoneyProps {
  readonly value: MoneyValue
  /** Omit the symbol where a column header already carries it. */
  readonly withSymbol?: boolean
  /** Overrides the context locale. Rare — a report pinned to one locale. */
  readonly locale?: string
}

/**
 * An amount, rendered the way DESIGN.md § Numeric typography requires.
 *
 * Four rules are carried here so that no caller has to remember them, because
 * a caller that forgets one produces a number that is quietly harder to read
 * rather than an error anybody notices:
 *
 *   - Tabular figures, so a column of amounts lines up. This is a font feature
 *     rather than a font swap: DM Sans with tabular figures aligns exactly as a
 *     monospace face would and stays the body typeface.
 *   - Aligned to the inline end, because a left-aligned money column cannot be
 *     scanned.
 *   - Never wrapped and never truncated. A truncated balance is a wrong
 *     balance, so the layout gives way instead of the digits.
 *   - A negative always carries an explicit minus, produced by the formatter.
 *     Colour may accompany the sign; it never replaces it, because a red figure
 *     with no sign is invisible to a reader who cannot distinguish it and
 *     ambiguous once printed.
 *
 * The `.numeric` class in index.css is where the first three live.
 */
export function Money({ value, withSymbol = true, locale }: MoneyProps) {
  const context = useMoneyContext()
  const text = formatMoney(value, context.registry, {
    locale: locale ?? context.locale,
    withSymbol,
  })

  return (
    <span className="numeric" data-negative={value.isNegative() ? 'true' : undefined}>
      {text}
    </span>
  )
}
