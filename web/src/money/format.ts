/**
 * Rendering an amount for a reader.
 *
 * The value never passes through a `number`. `Intl.NumberFormat` is consulted
 * for *locale conventions* — which separator, which minus sign, how digits are
 * grouped — and is handed a `bigint` when it is handed anything at all. Calling
 * `format(1500000.5)` would put the amount through a double on its way to the
 * screen, which is the §4.1 ban arriving at the last possible moment.
 *
 * Grouping is the reason Intl is used at all rather than a loop inserting a
 * separator every three digits: en-IN groups 3-2-2, and a hand-rolled grouper
 * silently renders the wrong thing for a reader whose locale it never
 * considered.
 *
 * DESIGN.md § Numeric typography governs the rest: the symbol sits before the
 * amount with a non-breaking space, the decimal count is the commodity's scale
 * on every row, a negative always carries an explicit sign, and nothing is
 * ever truncated — a truncated balance is a wrong balance.
 */

import type { Registry } from './commodity'
import type { Money } from './money'

/** U+00A0. Keeps the symbol and the amount on one line, per DESIGN.md. */
const NBSP = ' '

/**
 * Separator for cache keys, written as an escape rather than typed.
 *
 * U+001F cannot occur in a locale tag or a commodity code, so two different
 * pairs can never collide into one key. Spelling it out is the point: a byte
 * scan of this file found raw control characters here that a shell had
 * substituted for the spaces originally written, and an invisible character in
 * source is one an editor or a copy-paste eventually eats — after which the
 * failure looks like the cache being wrong.
 */
const SEP = '\u001F'

interface Conventions {
  readonly decimal: string
  readonly minus: string
}

const conventionsCache = new Map<string, Conventions>()

function conventionsFor(locale: string): Conventions {
  const cached = conventionsCache.get(locale)
  if (cached !== undefined) return cached

  // Both probes are bigints. 11111n with one fraction digit forces the decimal
  // separator to appear; -1n forces the minus sign, which is U+2212 rather
  // than a hyphen in several locales.
  const probe = new Intl.NumberFormat(locale, {
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).formatToParts(11111n)
  const signProbe = new Intl.NumberFormat(locale).formatToParts(-1n)

  const conventions: Conventions = {
    decimal: probe.find((p) => p.type === 'decimal')?.value ?? '.',
    minus: signProbe.find((p) => p.type === 'minusSign')?.value ?? '-',
  }
  conventionsCache.set(locale, conventions)
  return conventions
}

const symbolCache = new Map<string, string>()

/**
 * The symbol a reader expects for this commodity, or the code itself.
 *
 * Intl only knows ISO 4217 currencies, so BTC, XAU_GRAM and an exchange ticker
 * all fall through to their code. §7 requires every number to carry a unit, and
 * DESIGN.md places that unit before the amount — so the code takes the
 * symbol's position rather than becoming a suffix nothing specifies.
 */
export function symbolFor(commodity: string, locale: string): string {
  const key = locale + SEP + commodity
  const cached = symbolCache.get(key)
  if (cached !== undefined) return cached

  let symbol = commodity
  if (/^[A-Za-z]{3}$/.test(commodity)) {
    try {
      const parts = new Intl.NumberFormat(locale, {
        style: 'currency',
        currency: commodity,
        currencyDisplay: 'symbol',
      }).formatToParts(0n)
      symbol = parts.find((p) => p.type === 'currency')?.value ?? commodity
    } catch {
      // Not a currency Intl recognises. The code is the unit.
      symbol = commodity
    }
  }
  symbolCache.set(key, symbol)
  return symbol
}

export interface FormatOptions {
  /** Governs separators, grouping and the minus sign. Independent of language (§9). */
  readonly locale: string
  /** Omit the symbol where a column header already carries it. */
  readonly withSymbol?: boolean
}

/**
 * The digits alone: grouped integer part, decimal separator, exactly `scale`
 * fraction digits, and a minus sign when negative. No symbol.
 */
export function formatDigits(money: Money, scale: number, locale: string): string {
  const { decimal, minus } = conventionsFor(locale)

  const negative = money.amount < 0n
  const magnitude = negative ? -money.amount : money.amount

  // Exact integer division. `unit` is 10^scale as a bigint, so neither half of
  // the split can lose a digit the way a shift through a double would.
  const unit = 10n ** BigInt(scale)
  const whole = magnitude / unit
  const fraction = magnitude % unit

  // Intl is handed a bigint and does one job: group the integer part the way
  // this locale groups digits.
  const grouped = new Intl.NumberFormat(locale, { useGrouping: true }).format(whole)

  const digits =
    scale === 0 ? grouped : grouped + decimal + fraction.toString().padStart(scale, '0')

  return negative ? minus + digits : digits
}

/**
 * An amount as a reader sees it.
 *
 * The scale comes from the registry, which came from the server. An unknown
 * commodity throws rather than being rendered with a guessed decimal point.
 */
export function formatMoney(money: Money, registry: Registry, options: FormatOptions): string {
  const scale = registry.scaleOf(money.commodity)
  const digits = formatDigits(money, scale, options.locale)
  if (options.withSymbol === false) return digits
  return symbolFor(money.commodity, options.locale) + NBSP + digits
}
