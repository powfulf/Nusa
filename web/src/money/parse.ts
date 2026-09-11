/**
 * Reading what a person actually types into an amount field.
 *
 * "1500000", "1.5jt", "1,5 juta" and "Rp 1.500.000" all mean the same thing to
 * somebody standing at a till, and all of them have to arrive as the same
 * `bigint`. Nothing here goes through a `number`: the text is taken apart into
 * digit strings, the decimal point is moved by moving digits between two
 * strings, and `BigInt` is called once at the end.
 *
 * THERE IS NO ROUNDING IN THIS FILE, and that is the design.
 *
 * An input carrying more precision than its commodity can hold — 1,005 for a
 * currency with two decimal places — is REFUSED, not rounded. §4.6 puts
 * rounding at the last step of a calculation, and reading a field is not a
 * calculation; §4.7 makes `Rat.Round` the only half-to-even implementation
 * anywhere. Quietly turning what somebody typed into a different number is
 * also what this project does not do: it is the same reasoning that refuses a
 * NUL in a payee rather than stripping it, and that makes `NewDate` refuse
 * 30 February rather than sliding it to 2 March.
 *
 * The separator rules are ambiguous in principle — "1.500" is fifteen hundred
 * to an Indonesian reader and one-and-a-half to an American one — so the
 * reader's locale decides, and `<MoneyInput>` echoes the parsed value back
 * formatted, which is what actually protects somebody from a wrong reading.
 */

import { Money } from './money'
import { unitFor } from './format'
import type { Registry } from './commodity'

/** The text could not be read as an amount at all. */
export class MoneyParseError extends Error {
  /** Stable key for the message catalogue; §9 forbids rendering `message`. */
  readonly code = 'money.unparseable' as const

  constructor(input: string) {
    super(`cannot read an amount from ${JSON.stringify(input)}`)
    this.name = 'MoneyParseError'
  }
}

/**
 * The text was read, and carries more decimal places than the commodity has.
 *
 * A separate code from `unparseable` because the reader must be told something
 * different — "that is not a number" and "this currency has two decimal
 * places" call for different corrections. That is the test M2b Phase 2 set for
 * when a new code is justified.
 */
export class PrecisionError extends Error {
  readonly code = 'money.too_precise' as const
  readonly commodity: string
  readonly scale: number

  constructor(commodity: string, scale: number) {
    super(`${commodity} holds ${String(scale)} decimal places`)
    this.name = 'PrecisionError'
    this.commodity = commodity
    this.scale = scale
  }
}

/**
 * Shorthand multipliers, as powers of ten.
 *
 * Deliberately short. Every entry here is one a person would type without
 * being taught it, and an ambiguous abbreviation is worse than an unsupported
 * one: a wrong multiplier is a wrong amount by a factor of a thousand, and the
 * field would have accepted it without complaint.
 */
const MULTIPLIERS: ReadonlyArray<readonly [string, number]> = [
  ['ribu', 3],
  ['rb', 3],
  ['k', 3],
  ['juta', 6],
  ['jt', 6],
  ['m', 6],
  ['miliar', 9],
  ['milyar', 9],
  ['b', 9],
  ['triliun', 12],
  ['t', 12],
]

const DIGITS = /^[0-9]+$/

/** The separators this locale uses, discovered from Intl with a bigint probe. */
function separatorsFor(locale: string): { group: string; decimal: string } {
  const parts = new Intl.NumberFormat(locale, {
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).formatToParts(11111n)
  return {
    group: parts.find((p) => p.type === 'group')?.value ?? ',',
    decimal: parts.find((p) => p.type === 'decimal')?.value ?? '.',
  }
}

interface Split {
  readonly whole: string
  readonly fraction: string
}

/**
 * Decides which of `.` and `,` is the decimal point in this particular string.
 *
 * The rules, in order, and each is here because the one before it leaves a
 * case unanswered:
 *
 *   1. Both characters present — the LAST one is the decimal. "1.500,25" and
 *      "1,500.25" are both unambiguous once you know that.
 *   2. One character, appearing more than once — grouping. "1.500.000".
 *   3. One character, appearing once, followed by exactly three digits — the
 *      locale decides, because this is the genuinely ambiguous case.
 *   4. Anything else — decimal. "1,5" and "1.25" have no other reading.
 */
function splitOnSeparators(text: string, locale: string): Split {
  const { group, decimal } = separatorsFor(locale)
  const dots = (text.match(/\./g) ?? []).length
  const commas = (text.match(/,/g) ?? []).length

  let decimalChar: string | null = null
  if (dots > 0 && commas > 0) {
    decimalChar = text.lastIndexOf('.') > text.lastIndexOf(',') ? '.' : ','
  } else if (dots + commas === 1) {
    const only = dots === 1 ? '.' : ','
    const after = text.slice(text.indexOf(only) + 1)
    if (after.length === 3 && DIGITS.test(after)) {
      // The ambiguous case, and the only one the locale gets to settle.
      decimalChar = only === decimal ? only : null
    } else {
      decimalChar = only
    }
  } else if (dots + commas > 1) {
    decimalChar = null
  }

  let whole = text
  let fraction = ''
  if (decimalChar !== null) {
    const at = text.lastIndexOf(decimalChar)
    whole = text.slice(0, at)
    fraction = text.slice(at + 1)
  }

  // Whatever is left in the integer part is grouping, and it has to look like
  // grouping: a stray separator means the value was assembled by something
  // that got it wrong, and skipping over it hides that.
  const separators = new Set([group, '.', ','])
  const groups = whole.split(new RegExp(`[${[...separators].map((s) => `\\${s}`).join('')}]`))
  if (groups.length > 1) {
    const first = groups[0] ?? ''
    const rest = groups.slice(1)
    if (first.length === 0 || first.length > 3) throw new MoneyParseError(text)
    if (rest.some((g) => g.length !== 3)) throw new MoneyParseError(text)
  }
  whole = groups.join('')

  if (whole.length === 0) whole = '0'
  if (!DIGITS.test(whole)) throw new MoneyParseError(text)
  if (fraction.length > 0 && !DIGITS.test(fraction)) throw new MoneyParseError(text)

  return { whole, fraction }
}

/** Moves the decimal point `places` to the right, exactly, by moving digits. */
function shift({ whole, fraction }: Split, places: number): Split {
  if (places === 0) return { whole, fraction }
  if (fraction.length <= places) {
    return { whole: whole + fraction + '0'.repeat(places - fraction.length), fraction: '' }
  }
  return { whole: whole + fraction.slice(0, places), fraction: fraction.slice(places) }
}

/**
 * Reads a typed amount into `Money`, in the commodity's smallest unit.
 *
 * Throws `MoneyParseError` when the text is not an amount, and
 * `PrecisionError` when it is an amount the commodity cannot hold.
 */
export function parseMoney(
  input: string,
  commodity: string,
  registry: Registry,
  locale: string,
): Money {
  const scale = registry.scaleOf(commodity)

  // Whitespace of every kind. JavaScript's \s covers U+00A0 and U+202F,
  // which matters twice over: formatting puts a non-breaking space between a
  // symbol and its digits, and several locales group digits with one — so a
  // value this application printed can always be pasted back into the field.
  let text = input.replace(/\s/g, '')
  if (text.length === 0) throw new MoneyParseError(input)

  /*
   * A symbol may sit on either side of the sign: DESIGN.md prints the symbol
   * first, so a negative amount reads "IDR -1", while somebody typing by hand
   * writes "-Rp 1". Both have to arrive as the same negative number.
   *
   * Reading the sign before dropping the symbol was the first version, and a
   * property test caught it on the sixth case: the symbol-stripping pattern
   * matched everything that was not a digit, so it ate the minus along with
   * the code and "IDR -1" came back positive. A sign silently dropped from a
   * money value is the worst class of bug this project has, so the sign is now
   * fenced off from the stripping on both passes.
   */
  const stripSymbol = /^[^\d.,+\-−]+/

  text = text.replace(stripSymbol, '')

  let negative = false
  if (text.startsWith('-') || text.startsWith('−')) {
    negative = true
    text = text.slice(1)
  } else if (text.startsWith('+')) {
    text = text.slice(1)
  }

  text = text.replace(stripSymbol, '')

  /*
   * A trailing unit is stripped BY NAME, before the multiplier check. Formatting
   * puts a commodity code after the digits (`12,5000 XAU_GRAM`) and several
   * locales put the currency symbol there too (`1.250,50 EUR` in de-DE), so a
   * pasted value arrives with letters on its tail — and so does "1,5 juta". The
   * only way to tell a unit from a multiplier is to know which unit this field
   * is for, so exactly that unit, and its code, are removed; anything else on
   * the tail is left for the multiplier table and then the refusal below.
   */
  const known = registry.get(commodity)
  for (const tail of [unitFor(known, locale), known.code]) {
    const t = tail.toLowerCase()
    const lower = text.toLowerCase()
    if (t.length > 0 && lower.length > t.length && lower.endsWith(t)) {
      text = text.slice(0, text.length - t.length)
      break
    }
  }

  let places = 0
  const lowered = text.toLowerCase()
  for (const [suffix, power] of MULTIPLIERS) {
    if (lowered.endsWith(suffix) && lowered.length > suffix.length) {
      places = power
      text = text.slice(0, text.length - suffix.length)
      break
    }
  }

  if (text.length === 0 || !/[0-9]/.test(text)) throw new MoneyParseError(input)
  if (/[^0-9.,]/.test(text)) throw new MoneyParseError(input)

  const shifted = shift(splitOnSeparators(text, locale), places)

  let { fraction } = shifted
  if (fraction.length > scale) {
    // Refused rather than rounded. Trailing zeros are not precision, so
    // "1,500" for a two-place currency is 1,50 and not an error.
    if (/[1-9]/.test(fraction.slice(scale))) throw new PrecisionError(commodity, scale)
    fraction = fraction.slice(0, scale)
  }
  fraction = fraction.padEnd(scale, '0')

  const minor = BigInt(shifted.whole + fraction)
  return Money.of(negative ? -minor : minor, commodity)
}
