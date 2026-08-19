// SPDX-License-Identifier: MIT

package ledger

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// Rate is an exchange rate between two commodities, held exactly.
//
// It reads the way a rate is quoted: one unit of Base costs Value units of
// Quote, in the units people say out loud. "1 USD = 16.000 IDR" is
// Rate{base: "USD", quote: "IDR", value: 16000}, not a rate between satoshi
// and sen. Converting between smallest units is Registry.Convert's job,
// because that is where the scales are known.
//
// §5.4 requires the rate to be stored on the posting, at the moment of the
// transaction, so that last year's report says the same thing today as it did
// last year. That is why this type holds commodity codes rather than Commodity
// descriptions: a rate is data that gets written down and read back, and a
// scale written into a stored rate would be a second, staler copy of a fact
// the commodity already owns.
type Rate struct {
	base  CommodityCode
	quote CommodityCode
	value *big.Rat
}

// NewRate returns the rate at which one unit of base buys value units of
// quote. The value is copied.
//
// A rate must be positive. Zero would make a holding worthless by arithmetic
// accident, and a negative exchange rate does not mean anything.
func NewRate(base, quote CommodityCode, value *big.Rat) (Rate, error) {
	if err := validateCommodityCode(base); err != nil {
		return Rate{}, fmt.Errorf("rate base: %w", err)
	}
	if err := validateCommodityCode(quote); err != nil {
		return Rate{}, fmt.Errorf("rate quote: %w", err)
	}
	if base == quote {
		return Rate{}, fmt.Errorf("%w: %s to itself", ErrInvalidRate, base)
	}
	if value == nil {
		return Rate{}, fmt.Errorf("%w: nil value", ErrInvalidRate)
	}
	if value.Sign() <= 0 {
		return Rate{}, fmt.Errorf("%w: %s/%s must be positive, got %s",
			ErrInvalidRate, base, quote, value.RatString())
	}
	return Rate{base: base, quote: quote, value: new(big.Rat).Set(value)}, nil
}

// IsZero reports whether the rate is the unset zero value. A posting that
// needs no conversion carries one.
func (r Rate) IsZero() bool { return r.value == nil }

// Base returns the commodity being priced.
func (r Rate) Base() CommodityCode { return r.base }

// Quote returns the commodity the price is expressed in.
func (r Rate) Quote() CommodityCode { return r.quote }

// Value returns a copy of the exact rate, or nil if the rate is unset.
func (r Rate) Value() *big.Rat {
	if r.value == nil {
		return nil
	}
	return new(big.Rat).Set(r.value)
}

// Invert returns the same rate quoted the other way round. It is exact: the
// reciprocal of a ratio is a ratio, so converting there and back loses
// nothing as long as neither step is rounded.
func (r Rate) Invert() (Rate, error) {
	if r.IsZero() {
		return Rate{}, fmt.Errorf("%w: unset", ErrInvalidRate)
	}
	return Rate{
		base:  r.quote,
		quote: r.base,
		value: new(big.Rat).Inv(r.value),
	}, nil
}

// Equal reports whether two rates price the same pair at the same value.
func (r Rate) Equal(other Rate) bool {
	if r.IsZero() || other.IsZero() {
		return r.IsZero() && other.IsZero()
	}
	return r.base == other.base && r.quote == other.quote && r.value.Cmp(other.value) == 0
}

// String renders the rate for logs, for example "USD/IDR 16000".
func (r Rate) String() string {
	if r.IsZero() {
		return "<no rate>"
	}
	return string(r.base) + "/" + string(r.quote) + " " + r.value.RatString()
}

// rateJSON keeps the rate exact on the wire. The value is a string holding
// either an integer, a decimal, or an "a/b" ratio, all of which read back
// without loss — which a JSON number would not, and which is the whole reason
// historical rates are trustworthy.
type rateJSON struct {
	Base  CommodityCode `json:"base"`
	Quote CommodityCode `json:"quote"`
	Value string        `json:"value"`
}

// MarshalJSON writes the rate with its value as an exact string.
func (r Rate) MarshalJSON() ([]byte, error) {
	if r.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(rateJSON{Base: r.base, Quote: r.quote, Value: r.value.RatString()})
}

// UnmarshalJSON reads a rate, accepting null for "no conversion".
func (r *Rate) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*r = Rate{}
		return nil
	}
	var raw rateJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode rate: %w", err)
	}
	value, ok := new(big.Rat).SetString(raw.Value)
	if !ok {
		return fmt.Errorf("%w: %q is not an exact number", ErrInvalidRate, raw.Value)
	}
	parsed, err := NewRate(raw.Base, raw.Quote, value)
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

// Convert applies a rate to an amount, exactly.
//
// It lives on Registry because a rate is quoted in the units people say and an
// amount is counted in smallest units, so crossing between them needs both
// commodities' scales — and scales live on Commodity, which the registry
// resolves. Converting 1 USD at 16.000 IDR/USD means 100 cents becoming
// 1.600.000 sen, and both of those factors of a hundred come from here.
//
// The result is a Rat, never a Money: an exchange rate almost never lands on a
// whole number of smallest units, and choosing one is rounding. The caller
// rounds once, at the end, with Rat.Round.
func (reg *Registry) Convert(amount Money, rate Rate) (Rat, error) {
	if err := amount.validate(); err != nil {
		return Rat{}, err
	}
	if rate.IsZero() {
		return Rat{}, fmt.Errorf("%w: unset", ErrInvalidRate)
	}
	if amount.commodity != rate.base {
		return Rat{}, fmt.Errorf("%w: rate %s prices %s, not %s",
			ErrInvalidRate, rate, rate.base, amount.commodity)
	}

	base, err := reg.Require(rate.base)
	if err != nil {
		return Rat{}, err
	}
	quote, err := reg.Require(rate.quote)
	if err != nil {
		return Rat{}, err
	}

	// minor(quote) = minor(base) × rate × 10^quoteScale ÷ 10^baseScale
	value := new(big.Rat).SetInt(amount.amount)
	value.Mul(value, rate.value)
	value.Mul(value, new(big.Rat).SetFrac(quote.unit(), base.unit()))

	return Rat{value: value, commodity: rate.quote}, nil
}
