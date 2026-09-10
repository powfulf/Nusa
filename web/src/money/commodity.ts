/**
 * Commodities, and where a scale comes from.
 *
 * CLAUDE.md §4.2 puts scale on the commodity, never on an amount: two values of
 * the same commodity cannot disagree about where the decimal point sits,
 * because neither of them carries the answer.
 *
 * The frontend does not keep its own copy of the standard registry. Scales
 * arrive from `GET /api/v1/commodities`, which serves `ledger.StandardRegistry`
 * plus whatever a Country Pack has registered. A hardcoded table here would be
 * a second copy of a fact the server owns, free to drift — and a drifted scale
 * does not fail, it moves a decimal point.
 *
 * There is deliberately no default scale. An unknown commodity throws rather
 * than being formatted as if it had two decimal places.
 */

/** Mirrors `ledger.Commodity.Kind`, as served by the API. */
export type CommodityKind =
  | 'currency'
  | 'equity'
  | 'mutual_fund'
  | 'bond'
  | 'crypto'
  | 'metal'
  | 'other'

/** One commodity, exactly as `commodityDTO` in internal/api/dto.go serves it. */
export interface Commodity {
  readonly code: string
  readonly kind: CommodityKind
  /** Digits after the decimal point. IDR 2, BTC 8, ETH 18, IDX equities 0. */
  readonly scale: number
}

/** Raised when something asks for a commodity the registry has never seen. */
export class UnknownCommodityError extends Error {
  /** Stable key for the message catalogue; §9 forbids rendering `message`. */
  readonly code = 'money.unknown_commodity' as const
  readonly commodity: string

  constructor(commodity: string) {
    super(`no scale is known for commodity ${commodity}`)
    this.name = 'UnknownCommodityError'
    this.commodity = commodity
  }
}

/**
 * The commodities this session knows about.
 *
 * Immutable: `with` returns a new registry rather than mutating, so a component
 * holding one cannot have the ground move under it mid-render.
 */
export class Registry {
  readonly #byCode: ReadonlyMap<string, Commodity>

  private constructor(byCode: ReadonlyMap<string, Commodity>) {
    this.#byCode = byCode
    Object.freeze(this)
  }

  static of(commodities: readonly Commodity[]): Registry {
    const map = new Map<string, Commodity>()
    for (const c of commodities) {
      if (!Number.isInteger(c.scale) || c.scale < 0 || c.scale > 32) {
        throw new RangeError(`commodity ${c.code} has an unusable scale: ${String(c.scale)}`)
      }
      map.set(c.code, Object.freeze({ ...c }))
    }
    return new Registry(map)
  }

  static empty(): Registry {
    return Registry.of([])
  }

  with(commodities: readonly Commodity[]): Registry {
    return Registry.of([...this.#byCode.values(), ...commodities])
  }

  has(code: string): boolean {
    return this.#byCode.has(code)
  }

  /** Throws rather than guessing: a wrong scale silently misplaces a decimal. */
  get(code: string): Commodity {
    const found = this.#byCode.get(code)
    if (found === undefined) throw new UnknownCommodityError(code)
    return found
  }

  scaleOf(code: string): number {
    return this.get(code).scale
  }

  codes(): readonly string[] {
    return [...this.#byCode.keys()]
  }

  get size(): number {
    return this.#byCode.size
  }
}
