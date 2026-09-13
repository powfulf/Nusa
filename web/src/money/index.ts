/**
 * The money layer. CLAUDE.md §4 on the client.
 *
 * Nothing outside this directory does arithmetic on an amount, and nothing
 * inside it uses a `number` for one.
 */

export { Money, CommodityMismatchError } from './money'
export type { MoneyJSON } from './money'

export { Registry, UnknownCommodityError } from './commodity'
export type { Commodity, CommodityKind } from './commodity'

export { formatMoney, formatDigits, unitFor, formatMagnitude, directionOf } from './format'
export type { FormatOptions, Magnitude, Direction } from './format'

export { parseMoney, MoneyParseError, PrecisionError } from './parse'
