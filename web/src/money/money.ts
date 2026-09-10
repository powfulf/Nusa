/**
 * Money on the client. CLAUDE.md §4 in TypeScript.
 *
 * An amount is a `bigint` count of the commodity's smallest unit, and never a
 * `number`: a JavaScript number is an IEEE 754 double and loses integers above
 * 2^53, which is roughly Rp 90 trillion in sen — a figure an Indonesian
 * household portfolio does not reach, but one a business ledger, a crypto
 * holding at 18 decimal places, or a hyperinflated currency reaches easily.
 * The rule is absolute rather than sized to today's use (§4.1).
 *
 * WHAT THIS CLASS DELIBERATELY DOES NOT HAVE, and why its absence is the
 * design rather than an omission:
 *
 *   There is no `mul`, no `div`, no `percentOf`, and no `allocate`.
 *
 * §4.6 puts rounding at the last step only, and §4.7 makes `ledger.Rat` the
 * single exact intermediate with `Rat.Round` the only half-to-even
 * implementation in the codebase — "a second rounding helper anywhere is a
 * bug". Every operation here is exact integer arithmetic on whole minor units,
 * so no rounding is needed and none exists to get wrong.
 *
 * That is the §11 move of enforcing a rule by removing the ability to break it:
 * a component cannot round halfway through a calculation, because there is
 * nothing here that rounds. Splitting an amount N ways is already deferred to
 * M4, and it arrives together with the TypeScript counterpart of `Rat` — one
 * rounding implementation, in one place, with a consumer to test it against.
 */

/** Raised by any operation spanning two commodities. §4.3: never convert silently. */
export class CommodityMismatchError extends Error {
  /** Stable key for the message catalogue; §9 forbids rendering `message`. */
  readonly code = 'money.commodity_mismatch' as const
  readonly left: string
  readonly right: string

  constructor(left: string, right: string) {
    super(`cannot combine ${left} with ${right}`)
    this.name = 'CommodityMismatchError'
    this.left = left
    this.right = right
  }
}

/** The wire shape from §4.5. `amount` is a string, always. */
export interface MoneyJSON {
  readonly amount: string
  readonly commodity: string
}

const INTEGER = /^-?(?:0|[1-9][0-9]*)$/

export class Money {
  /** Count of the commodity's smallest unit. Never a `number`. */
  readonly amount: bigint
  readonly commodity: string

  private constructor(amount: bigint, commodity: string) {
    this.amount = amount
    this.commodity = commodity
    Object.freeze(this)
  }

  static of(amount: bigint, commodity: string): Money {
    if (typeof amount !== 'bigint') {
      // Reachable from untyped callers — a JSON payload, a test fixture — and
      // this is the one place a double could enter the system unnoticed.
      throw new TypeError('a money amount must be a bigint of minor units, never a number')
    }
    if (commodity.length === 0) {
      throw new TypeError('a money amount must name its commodity')
    }
    return new Money(amount, commodity)
  }

  static zero(commodity: string): Money {
    return Money.of(0n, commodity)
  }

  /**
   * Reads the wire shape.
   *
   * The amount is required to be a string of digits. A JSON *number* is
   * refused rather than coerced: by the time it reaches here the parser has
   * already put it through a double, so the precision is gone and coercing
   * would launder the loss into something that looks exact.
   */
  static fromJSON(value: unknown): Money {
    if (typeof value !== 'object' || value === null) {
      throw new TypeError('a money value must be an object')
    }
    const { amount, commodity } = value as Record<string, unknown>
    if (typeof amount !== 'string') {
      throw new TypeError(
        'a money amount must cross the wire as a string; a JSON number has already lost precision',
      )
    }
    if (!INTEGER.test(amount)) {
      throw new TypeError(`a money amount must be an integer of minor units, got ${amount}`)
    }
    if (typeof commodity !== 'string' || commodity.length === 0) {
      throw new TypeError('a money value must name its commodity')
    }
    return Money.of(BigInt(amount), commodity)
  }

  toJSON(): MoneyJSON {
    return { amount: this.amount.toString(), commodity: this.commodity }
  }

  #sameCommodity(other: Money): void {
    if (this.commodity !== other.commodity) {
      throw new CommodityMismatchError(this.commodity, other.commodity)
    }
  }

  add(other: Money): Money {
    this.#sameCommodity(other)
    return new Money(this.amount + other.amount, this.commodity)
  }

  sub(other: Money): Money {
    this.#sameCommodity(other)
    return new Money(this.amount - other.amount, this.commodity)
  }

  neg(): Money {
    return new Money(-this.amount, this.commodity)
  }

  abs(): Money {
    return this.amount < 0n ? this.neg() : this
  }

  cmp(other: Money): -1 | 0 | 1 {
    this.#sameCommodity(other)
    if (this.amount < other.amount) return -1
    if (this.amount > other.amount) return 1
    return 0
  }

  equals(other: Money): boolean {
    return this.commodity === other.commodity && this.amount === other.amount
  }

  isZero(): boolean {
    return this.amount === 0n
  }

  isNegative(): boolean {
    return this.amount < 0n
  }

  /** Developer-facing only. Never rendered — see format.ts for what a reader sees. */
  toString(): string {
    return `${this.amount.toString()} ${this.commodity}`
  }
}
