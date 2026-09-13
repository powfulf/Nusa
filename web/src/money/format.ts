/**
 * Rendering an amount for a reader.
 *
 * The value never passes through a `number`. `Intl.NumberFormat` is consulted
 * for *locale conventions* — which separator, which minus sign, how digits are
 * grouped, where the currency symbol sits — and is handed a `bigint` when it is
 * handed anything at all. Calling `format(1500000.5)` would put the amount
 * through a double on its way to the screen, which is the §4.1 ban arriving at
 * the last possible moment.
 *
 * Grouping is the reason Intl is used at all rather than a loop inserting a
 * separator every three digits: en-IN groups 3-2-2, and a hand-rolled grouper
 * silently renders the wrong thing for a reader whose locale it never
 * considered.
 *
 * DESIGN.md § Numeric typography and § Units govern the rest. The unit rule is
 * keyed on the commodity's KIND, never on whether a symbol happens to exist:
 *
 *   - A currency's symbol is part of the reader's typography, so its position
 *     and spacing come from the locale. `$1,250.50`, `Rp 1.250,50`,
 *     `1.250,50 €` — all three are the same rule applied.
 *   - A commodity code is a unit of measure and follows the number, with a
 *     non-breaking space, in every locale. `0,15 BTC`, `12,5000 XAU_GRAM`.
 *
 * An earlier version switched on whether the code was three letters long. That
 * is a test of the symptom (Intl knows it) rather than the cause (it is a
 * currency), and it is exactly the shape DESIGN.md now forbids.
 */

import type { Commodity, Registry } from './commodity'
import type { Money } from './money'

/** U+00A0. Keeps a unit and its amount on one line, per DESIGN.md. */
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

/**
 * Where a currency's symbol sits in this locale, and what surrounds it.
 *
 * Learned from Intl's own parts for a bigint zero: everything before the first
 * numeric part is the prefix, everything after the last is the suffix, and the
 * digits in between are discarded so that ours can take their place. Only the
 * layout is read, never a value.
 */
interface CurrencyLayout {
  readonly prefix: string
  readonly suffix: string
}

const layoutCache = new Map<string, CurrencyLayout>()

function currencyLayoutFor(code: string, locale: string): CurrencyLayout | null {
  const key = locale + SEP + code
  const cached = layoutCache.get(key)
  if (cached !== undefined) return cached

  let parts: Intl.NumberFormatPart[]
  try {
    parts = new Intl.NumberFormat(locale, {
      style: 'currency',
      currency: code,
      currencyDisplay: 'symbol',
    }).formatToParts(0n)
  } catch {
    // Intl refuses the code outright — not "no symbol for it", which Intl
    // handles by showing the code, but a code it will not accept at all. There
    // is then no locale convention to follow, and the caller falls back to the
    // unit-of-measure rule rather than inventing a currency layout.
    return null
  }

  const numeric = new Set(['integer', 'group', 'decimal', 'fraction', 'minusSign', 'plusSign'])
  const first = parts.findIndex((p) => numeric.has(p.type))
  let last = -1
  for (let i = parts.length - 1; i >= 0; i -= 1) {
    if (numeric.has(parts[i]?.type ?? '')) {
      last = i
      break
    }
  }
  if (first === -1 || last === -1) return null

  const layout: CurrencyLayout = {
    prefix: parts
      .slice(0, first)
      .map((p) => p.value)
      .join(''),
    suffix: parts
      .slice(last + 1)
      .map((p) => p.value)
      .join(''),
  }
  layoutCache.set(key, layout)
  return layout
}

/**
 * The unit text the reader will see for this commodity in this locale — the
 * locale's currency symbol, or the commodity code. Exposed so that parsing can
 * strip exactly what formatting produced.
 */
export function unitFor(commodity: Commodity, locale: string): string {
  if (commodity.kind === 'currency') {
    const layout = currencyLayoutFor(commodity.code, locale)
    if (layout !== null) return (layout.prefix + layout.suffix).trim()
  }
  return commodity.code
}

export interface FormatOptions {
  /** Governs separators, grouping, the minus sign and where a symbol sits. Independent of language (§9). */
  readonly locale: string
  /** Omit the unit where a column header already carries it. */
  readonly withSymbol?: boolean
}

/**
 * The digits alone: grouped integer part, decimal separator, exactly `scale`
 * fraction digits, and a minus sign when negative. No unit.
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
 * The scale and the kind come from the registry, which came from the server.
 * An unknown commodity throws rather than being rendered with a guessed
 * decimal point.
 *
 * THE MINUS SIGN STAYS WITH THE DIGITS, and this is a deliberate departure from
 * the locale, stated here because it is the one place this file ignores a
 * convention it follows everywhere else. en-US writes `-$1,250.50`; Nusa
 * writes `$-1,250.50`. The reason is alignment: in a column, a sign that
 * sometimes precedes a symbol and sometimes the digits does not line up, and
 * alignment is most of what a numeric column is for. DESIGN.md § Units records
 * the decision; `money.test.ts` "keeps the minus beside the digits, not the
 * symbol" is the test that would go red if this were ever changed to follow
 * the locale.
 */
export function formatMoney(money: Money, registry: Registry, options: FormatOptions): string {
  const commodity = registry.get(money.commodity)
  const digits = formatDigits(money, commodity.scale, options.locale)
  if (options.withSymbol === false) return digits

  if (commodity.kind === 'currency') {
    const layout = currencyLayoutFor(commodity.code, options.locale)
    if (layout !== null) return layout.prefix + digits + layout.suffix
  }

  // A unit of measure follows the number. This is also where a currency whose
  // code Intl refuses lands, because with no locale convention available the
  // only honest presentation is the code as a unit.
  return digits + NBSP + commodity.code
}

/**
 * A formatted amount that carries no sign. Nothing but `formatMagnitude`
 * produces one, so a slot typed `Magnitude` cannot be handed a signed string.
 *
 * DESIGN.md § Explain: in a sentence that says which way a value went — more
 * or less, into or out of — the direction is carried by the message's
 * structure and the number is absolute, or "less -Rp 1.250.000" ends up on
 * somebody's screen with the sign doubled. The brand is what turns that rule
 * from a convention into a compile error.
 */
export type Magnitude = string & { readonly [magnitudeBrand]: true }
declare const magnitudeBrand: unique symbol

export function formatMagnitude(money: Money, registry: Registry, options: FormatOptions): Magnitude {
  return formatMoney(money.abs(), registry, options) as Magnitude
}

/** The direction a directional sentence needs, decided once, beside the magnitude it pairs with. */
export type Direction = 'up' | 'down' | 'flat'

export function directionOf(money: Money): Direction {
  if (money.isZero()) return 'flat'
  return money.isNegative() ? 'down' : 'up'
}
