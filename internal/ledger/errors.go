// SPDX-License-Identifier: MIT

package ledger

import "errors"

// Every error leaving this package wraps one of these sentinels, so callers
// match on behaviour with errors.Is rather than on message text. The messages
// themselves are for developers and logs; nothing here is ever shown to a user
// without translation.
var (
	// ErrCommodityMismatch reports arithmetic or comparison between two
	// different commodities. Nusa never converts silently — a conversion is
	// always an explicit, recorded act.
	ErrCommodityMismatch = errors.New("commodity mismatch")

	// ErrInvalidCommodity reports a commodity code that is empty or malformed.
	ErrInvalidCommodity = errors.New("invalid commodity code")

	// ErrInvalidScale reports a commodity scale outside the supported range.
	ErrInvalidScale = errors.New("invalid commodity scale")

	// ErrUnknownCommodity reports a code that no registry entry describes.
	ErrUnknownCommodity = errors.New("unknown commodity")

	// ErrInvalidMoney reports a Money value that was never constructed, or
	// was constructed from a nil amount.
	ErrInvalidMoney = errors.New("invalid money value")

	// ErrDivideByZero reports division by a zero divisor.
	ErrDivideByZero = errors.New("divide by zero")

	// ErrInvalidRate reports an exchange rate that is zero, negative, or whose
	// commodities do not match the value being converted.
	ErrInvalidRate = errors.New("invalid exchange rate")

	// ErrInvalidDate reports a calendar date that does not exist.
	ErrInvalidDate = errors.New("invalid date")

	// ErrInvalidAccount reports an account whose identity, kind, or placement
	// in the tree is not usable.
	ErrInvalidAccount = errors.New("invalid account")

	// ErrUnknownAccount reports a reference to an account the tree does not
	// contain.
	ErrUnknownAccount = errors.New("unknown account")

	// ErrAccountCycle reports a parent chain that loops back on itself.
	ErrAccountCycle = errors.New("account cycle")

	// ErrDuplicateID reports two entities claiming the same identity. IDs come
	// from outside this package, so collisions are the caller's to prevent and
	// this package's to refuse.
	ErrDuplicateID = errors.New("duplicate id")

	// ErrInvalidID reports an identity that is not a canonical UUIDv7. It is
	// returned alongside the sentinel for whichever entity was being built, so
	// either can be matched.
	ErrInvalidID = errors.New("invalid id")

	// ErrInvalidTransaction reports a transaction that is malformed before its
	// postings are even summed — no date, too few postings, empty identity.
	ErrInvalidTransaction = errors.New("invalid transaction")

	// ErrUnbalanced reports postings that do not sum to zero in some
	// commodity. There is no tolerance and no rounding allowance: a
	// transaction balances exactly or it is not a transaction.
	ErrUnbalanced = errors.New("postings do not sum to zero")

	// ErrNotEquityAccount reports a conversion routed through an account that
	// is not equity. Cross-commodity balancing moves value between commodities
	// rather than in or out of the household, which is what equity means.
	ErrNotEquityAccount = errors.New("conversion account is not equity")

	// ErrInvalidLot reports a lot with a non-positive quantity, a cost in the
	// same commodity as the asset, or inconsistent original and remaining
	// quantities.
	ErrInvalidLot = errors.New("invalid lot")

	// ErrInsufficientLots reports a disposal larger than the quantity the
	// available lots hold.
	ErrInsufficientLots = errors.New("insufficient lot quantity")
)
