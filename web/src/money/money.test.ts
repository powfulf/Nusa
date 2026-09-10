import fc from 'fast-check'
import { describe, expect, it } from 'vitest'

import { Registry, UnknownCommodityError } from './commodity'
import { formatDigits, formatMoney } from './format'
import { CommodityMismatchError, Money } from './money'
import { MoneyParseError, PrecisionError, parseMoney } from './parse'

/*
 * GENERATOR INVENTORY — §11 requires this, field by field, in the file.
 *
 * A property test only tests what its generator varies. A field it never fills
 * is compared perfectly and proves nothing, and unlike a generator whose cases
 * are rejected early, this failure is silent: the run is green, the timing is
 * normal, the check count is unchanged. The M2b round-trip suite promised that
 * "everything written comes back exactly" while never once setting OccurredAt.
 *
 * VARIED
 *   Money.amount     magnitudes from 0 to 10^30, spanning the 2^53 boundary a
 *                    double loses integers past — the whole reason for bigint.
 *   Money.amount     sign: positive, negative, and zero.
 *   commodity scale  0, 2, 3, 8, 18. Not a free integer: these are the scales
 *                    that actually exist (IDX whole shares, currencies, some
 *                    funds, BTC, ETH) and 3 is included because it is the one
 *                    that collides with three-digit grouping.
 *   locale           en-US (group , decimal .), id-ID and de-DE (group .
 *                    decimal ,), sv-SE (group U+00A0, minus U+2212). The last
 *                    exists to catch a parser that only strips ASCII space and
 *                    only accepts a hyphen.
 *   commodity code   three-letter (Intl finds a symbol) and longer (it does
 *                    not), so both branches of symbolFor are exercised.
 *
 * DELIBERATELY NOT VARIED, and why
 *   registry         built per case from the scale above; varying its other
 *                    entries would change nothing any assertion reads.
 *   CommodityKind    carried through untouched; no arithmetic or formatting
 *                    consults it. If that ever changes, this line is wrong.
 *   Money.mul/div    do not exist. See money.ts — their absence is the design,
 *                    so there is nothing to generate for.
 */

const SCALES = [0, 2, 3, 8, 18] as const
const LOCALES = ['en-US', 'id-ID', 'de-DE', 'sv-SE'] as const

function registryWith(scale: number, code = 'IDR'): Registry {
  return Registry.of([{ code, kind: 'currency', scale }])
}

/** Magnitudes that reach well past 2^53, where a double stops counting. */
const amounts = fc
  .tuple(fc.integer({ min: 0, max: 30 }), fc.bigInt({ min: 0n, max: 999_999_999n }))
  .map(([digits, seed]) => (10n ** BigInt(digits) + seed) % 10n ** 30n)

const signedAmounts = fc
  .tuple(amounts, fc.boolean())
  .map(([magnitude, negative]) => (negative ? -magnitude : magnitude))

const scales = fc.constantFrom(...SCALES)
const locales = fc.constantFrom(...LOCALES)

describe('Money arithmetic', () => {
  it('adds and subtracts without ever losing a unit, past 2^53', () => {
    fc.assert(
      fc.property(signedAmounts, signedAmounts, (a, b) => {
        const left = Money.of(a, 'IDR')
        const right = Money.of(b, 'IDR')
        expect(left.add(right).sub(right).equals(left)).toBe(true)
        expect(left.add(right).amount).toBe(a + b)
      }),
      { numRuns: 500 },
    )
  })

  it('is commutative and associative', () => {
    fc.assert(
      fc.property(signedAmounts, signedAmounts, signedAmounts, (a, b, c) => {
        const x = Money.of(a, 'USD')
        const y = Money.of(b, 'USD')
        const z = Money.of(c, 'USD')
        expect(x.add(y).equals(y.add(x))).toBe(true)
        expect(x.add(y).add(z).equals(x.add(y.add(z)))).toBe(true)
      }),
      { numRuns: 300 },
    )
  })

  it('negates and takes an absolute value exactly', () => {
    fc.assert(
      fc.property(signedAmounts, (a) => {
        const m = Money.of(a, 'BTC')
        expect(m.neg().neg().equals(m)).toBe(true)
        expect(m.abs().isNegative()).toBe(false)
        expect(m.abs().amount).toBe(a < 0n ? -a : a)
      }),
      { numRuns: 300 },
    )
  })

  it('orders consistently with the underlying integers', () => {
    fc.assert(
      fc.property(signedAmounts, signedAmounts, (a, b) => {
        const cmp = Money.of(a, 'IDR').cmp(Money.of(b, 'IDR'))
        expect(cmp).toBe(a < b ? -1 : a > b ? 1 : 0)
      }),
      { numRuns: 300 },
    )
  })

  it('refuses to combine two commodities', () => {
    const idr = Money.of(100n, 'IDR')
    const usd = Money.of(100n, 'USD')
    expect(() => idr.add(usd)).toThrow(CommodityMismatchError)
    expect(() => idr.sub(usd)).toThrow(CommodityMismatchError)
    expect(() => idr.cmp(usd)).toThrow(CommodityMismatchError)
    // equals is a question, not an operation, so it answers rather than throws.
    expect(idr.equals(usd)).toBe(false)
  })

  it('has no operation that could need rounding', () => {
    // §4.7 makes Rat.Round the only half-to-even implementation in the
    // codebase. This asserts the shape that keeps that true here: there is
    // nothing on Money that produces a non-integer, so nothing to round.
    const m = Money.of(100n, 'IDR') as unknown as Record<string, unknown>
    for (const name of ['mul', 'div', 'times', 'percentOf', 'allocate', 'split', 'round']) {
      expect(m[name]).toBeUndefined()
    }
  })
})

describe('the wire shape (§4.5)', () => {
  it('round-trips through JSON exactly', () => {
    fc.assert(
      fc.property(signedAmounts, (a) => {
        const m = Money.of(a, 'ETH')
        expect(Money.fromJSON(JSON.parse(JSON.stringify(m))).equals(m)).toBe(true)
      }),
      { numRuns: 300 },
    )
  })

  it('serialises the amount as a string, never a number', () => {
    const encoded = JSON.stringify(Money.of(1_500_000n, 'IDR'))
    expect(encoded).toBe('{"amount":"1500000","commodity":"IDR"}')
  })

  it('refuses a JSON number, because its precision is already gone', () => {
    expect(() => Money.fromJSON({ amount: 1500000, commodity: 'IDR' })).toThrow(TypeError)
    // The value below is not representable as a double: it arrives as
    // 9007199254740993 -> 9007199254740992. Accepting it would launder that.
    expect(() => Money.fromJSON({ amount: 9007199254740993, commodity: 'IDR' })).toThrow(TypeError)
  })

  it('refuses a non-integer amount string', () => {
    expect(() => Money.fromJSON({ amount: '15.00', commodity: 'IDR' })).toThrow(TypeError)
    expect(() => Money.fromJSON({ amount: '', commodity: 'IDR' })).toThrow(TypeError)
    expect(() => Money.fromJSON({ amount: '007', commodity: 'IDR' })).toThrow(TypeError)
  })

  it('refuses a number in of(), which is the one door a double could use', () => {
    expect(() => Money.of(1500 as unknown as bigint, 'IDR')).toThrow(TypeError)
  })
})

describe('formatting', () => {
  it('shows exactly the commodity scale, on every value', () => {
    fc.assert(
      fc.property(signedAmounts, scales, locales, (a, scale, locale) => {
        const text = formatDigits(Money.of(a, 'IDR'), scale, locale)
        const { decimal } = separators(locale)
        if (scale === 0) {
          expect(text).not.toContain(decimal)
        } else {
          const after = text.slice(text.lastIndexOf(decimal) + 1)
          expect(after).toHaveLength(scale)
        }
      }),
      { numRuns: 400 },
    )
  })

  it('always carries an explicit minus, never colour alone', () => {
    fc.assert(
      // Repaired rather than filtered. A generator that discards cases ends
      // them before the assertions run, so the check count stops saying how
      // often the subject was exercised (§11). Mapping zero to one keeps every
      // case reaching the assertion.
      fc.property(amounts.map((a) => (a === 0n ? 1n : a)), scales, locales, (a, scale, locale) => {
        const negative = formatDigits(Money.of(-a, 'IDR'), scale, locale)
        expect(negative.startsWith('-') || negative.startsWith('−')).toBe(true)
      }),
      { numRuns: 200 },
    )
  })

  it('never truncates — a truncated balance is a wrong balance', () => {
    const huge = Money.of(10n ** 29n, 'IDR')
    const text = formatDigits(huge, 2, 'id-ID')
    expect(text).not.toContain('…')
    expect(text.replace(/[^0-9]/g, '')).toHaveLength(30)
  })

  it('puts the symbol before the amount with a non-breaking space', () => {
    const text = formatMoney(Money.of(150_000_000n, 'IDR'), registryWith(2), { locale: 'id-ID' })
    expect(text).toBe('Rp 1.500.000,00')
  })

  it('uses the code as the unit for a commodity Intl does not know', () => {
    const registry = Registry.of([{ code: 'XAU_GRAM', kind: 'metal', scale: 4 }])
    const text = formatMoney(Money.of(12_5000n, 'XAU_GRAM'), registry, { locale: 'en-US' })
    expect(text).toBe('XAU_GRAM 12.5000')
  })

  it('refuses to render a commodity whose scale it does not know', () => {
    expect(() => formatMoney(Money.of(1n, 'ZZZ'), Registry.empty(), { locale: 'en-US' })).toThrow(
      UnknownCommodityError,
    )
  })
})

describe('parsing what a person types', () => {
  it('reads the shapes the field promises to accept', () => {
    const idr = registryWith(2)
    const cases: ReadonlyArray<readonly [string, string, bigint]> = [
      ['1500000', 'id-ID', 150_000_000n],
      ['1.5jt', 'id-ID', 150_000_000n],
      ['1,5 juta', 'id-ID', 150_000_000n],
      ['1.500.000', 'id-ID', 150_000_000n],
      ['Rp 1.500.000', 'id-ID', 150_000_000n],
      ['15rb', 'id-ID', 1_500_000n],
      ['15 ribu', 'id-ID', 1_500_000n],
      ['1,5k', 'en-US', 150_000n],
      ['1,500.25', 'en-US', 150_025n],
      ['-1.500,25', 'id-ID', -150_025n],
      ['1.5m', 'en-US', 150_000_000n],
    ]
    for (const [text, locale, expected] of cases) {
      expect(parseMoney(text, 'IDR', idr, locale).amount, text).toBe(expected)
    }
  })

  it('lets the locale settle the one genuinely ambiguous shape', () => {
    // "1.500" is fifteen hundred to an Indonesian reader and one-and-a-half to
    // an American one. Both readings are correct; the locale picks.
    const idr = registryWith(2)
    expect(parseMoney('1.500', 'IDR', idr, 'id-ID').amount).toBe(150_000n)
    expect(parseMoney('1.500', 'IDR', idr, 'en-US').amount).toBe(150n)
  })

  it('REFUSES more precision than the commodity holds, and never rounds', () => {
    const idr = registryWith(2)

    // Each case is written in the locale where the separator IS the decimal
    // point. The first draft of this test used "1,005" under en-US, where the
    // comma is the group separator and the value is a thousand and five — so
    // the assertion was wrong and the parser was right. A precision test has
    // to be certain it produced a fraction before asserting what happens to
    // one.
    expect(() => parseMoney('1.005', 'IDR', idr, 'en-US')).toThrow(PrecisionError)
    expect(() => parseMoney('1,005', 'IDR', idr, 'id-ID')).toThrow(PrecisionError)
    expect(() => parseMoney('0.125', 'IDR', idr, 'en-US')).toThrow(PrecisionError)
    expect(() => parseMoney('0,125', 'IDR', idr, 'de-DE')).toThrow(PrecisionError)

    // A commodity with room for them accepts the same digits.
    expect(parseMoney('1.005', 'IDR', registryWith(3), 'en-US').amount).toBe(1005n)

    // Trailing zeros are not precision, so these are not refused.
    expect(parseMoney('1.500', 'IDR', idr, 'en-US').amount).toBe(150n)
    expect(parseMoney('1,5000', 'IDR', idr, 'id-ID').amount).toBe(150n)
  })

  it('refuses text that is not an amount', () => {
    const idr = registryWith(2)
    for (const bad of ['', '   ', 'abc', 'jt', 'Rp', '1..5', '1,2,3', '12.3456.7', '1e5', '--1']) {
      expect(() => parseMoney(bad, 'IDR', idr, 'en-US'), bad).toThrow(MoneyParseError)
    }
  })

  it('refuses a commodity whose scale it does not know', () => {
    expect(() => parseMoney('1', 'ZZZ', Registry.empty(), 'en-US')).toThrow(UnknownCommodityError)
  })
})

describe('format and parse are inverses', () => {
  /*
   * The central property. If these two ever disagree, a person edits a value
   * their own screen showed them and gets a different number back — which is
   * the failure that destroys trust permanently, and the one nothing but a
   * round trip catches.
   */
  it('round-trips every amount, scale and locale', () => {
    fc.assert(
      fc.property(signedAmounts, scales, locales, (a, scale, locale) => {
        const registry = registryWith(scale)
        const money = Money.of(a, 'IDR')

        const digits = formatDigits(money, scale, locale)
        expect(parseMoney(digits, 'IDR', registry, locale).equals(money)).toBe(true)

        const withSymbol = formatMoney(money, registry, { locale })
        expect(parseMoney(withSymbol, 'IDR', registry, locale).equals(money)).toBe(true)
      }),
      { numRuns: 1000 },
    )
  })

  it('round-trips a commodity with no Intl symbol too', () => {
    fc.assert(
      fc.property(signedAmounts, locales, (a, locale) => {
        const registry = Registry.of([{ code: 'XAU_GRAM', kind: 'metal', scale: 4 }])
        const money = Money.of(a, 'XAU_GRAM')
        const text = formatDigits(money, 4, locale)
        expect(parseMoney(text, 'XAU_GRAM', registry, locale).equals(money)).toBe(true)
      }),
      { numRuns: 300 },
    )
  })
})

/** Reads a locale's separators the same way the modules under test do. */
function separators(locale: string): { group: string; decimal: string } {
  const parts = new Intl.NumberFormat(locale, {
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).formatToParts(11111n)
  return {
    group: parts.find((p) => p.type === 'group')?.value ?? ',',
    decimal: parts.find((p) => p.type === 'decimal')?.value ?? '.',
  }
}
