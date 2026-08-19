// SPDX-License-Identifier: MIT

package ledger

import (
	"fmt"
	"math/big"
	"slices"
)

// MaxScale is the largest number of decimal places a commodity may declare.
// Eighteen is ether's scale, and nothing in personal finance needs more.
const MaxScale = 18

// CommodityCode identifies what a quantity is denominated in: a currency
// ("IDR"), a security ("BBCA.JK"), a cryptocurrency ("BTC"), or a physical
// commodity ("XAU_GRAM").
//
// It is a distinct type so that a commodity code can never be passed where an
// account name or a memo was meant.
type CommodityCode string

// CommodityKind classifies a commodity. It never affects arithmetic — a
// quantity is a quantity — but it decides how the rest of the application
// treats the thing: what a price means, whether lots and cost basis apply, and
// which screens it appears on.
type CommodityKind uint8

// The recognised commodity kinds.
const (
	// KindUnknown is the zero value and is never valid.
	KindUnknown CommodityKind = iota

	// KindCurrency is government-issued money.
	KindCurrency

	// KindEquity is a share in a company. Note that this is unrelated to
	// AccountEquity, which is an accounting classification.
	KindEquity

	// KindFund is a mutual fund or ETF unit.
	KindFund

	// KindCrypto is a cryptocurrency or token.
	KindCrypto

	// KindMetal is a physical commodity held by weight.
	KindMetal
)

// String returns the kind's stable machine name. It is not user-facing text.
func (k CommodityKind) String() string {
	switch k {
	case KindCurrency:
		return "currency"
	case KindEquity:
		return "equity"
	case KindFund:
		return "fund"
	case KindCrypto:
		return "crypto"
	case KindMetal:
		return "metal"
	case KindUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// Commodity describes a commodity: its code, what kind of thing it is, and how
// many decimal places separate its smallest unit from the unit people quote.
//
// Scale lives here and nowhere else. A Money value carries only an integer
// count of smallest units and a code, so there is no way for two values of the
// same commodity to disagree about where the decimal point sits.
type Commodity struct {
	code  CommodityCode
	kind  CommodityKind
	scale uint8
}

// NewCommodity describes a commodity.
//
// Scale is the number of decimal places: 2 for IDR and USD, 0 for JPY and for
// exchanges that trade whole shares, 8 for BTC, 18 for ETH, 4 for mutual fund
// units.
func NewCommodity(code CommodityCode, kind CommodityKind, scale uint8) (Commodity, error) {
	if err := validateCommodityCode(code); err != nil {
		return Commodity{}, err
	}
	if kind == KindUnknown {
		return Commodity{}, fmt.Errorf("%w: %q has no kind", ErrInvalidCommodity, code)
	}
	if scale > MaxScale {
		return Commodity{}, fmt.Errorf("%w: %q declares %d places, the maximum is %d",
			ErrInvalidScale, code, scale, MaxScale)
	}
	return Commodity{code: code, kind: kind, scale: scale}, nil
}

// Code returns the commodity's identifier.
func (c Commodity) Code() CommodityCode { return c.code }

// Kind returns what sort of thing the commodity is.
func (c Commodity) Kind() CommodityKind { return c.kind }

// Scale returns the number of decimal places between the commodity's smallest
// unit and the unit it is quoted in.
func (c Commodity) Scale() uint8 { return c.scale }

// IsZero reports whether the commodity is the unset zero value.
func (c Commodity) IsZero() bool { return c.code == "" }

// String returns the commodity code, for logs and error messages.
func (c Commodity) String() string { return string(c.code) }

// unit returns 10^scale: how many smallest units make up one quoted unit.
func (c Commodity) unit() *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(c.scale)), nil)
}

// validateCommodityCode enforces a canonical shape for codes: uppercase, at
// least one character, starting with a letter.
//
// Being strict here is deliberate. Codes are compared for equality all over
// this package, and "idr" silently failing to equal "IDR" would present as a
// balancing error a long way from its cause.
func validateCommodityCode(code CommodityCode) error {
	if code == "" {
		return fmt.Errorf("%w: empty", ErrInvalidCommodity)
	}
	if len(code) > 32 {
		return fmt.Errorf("%w: %q is longer than 32 characters", ErrInvalidCommodity, code)
	}
	for i := 0; i < len(code); i++ {
		ch := code[i]
		switch {
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9', ch == '.', ch == '_', ch == '-':
			if i == 0 {
				return fmt.Errorf("%w: %q must start with an uppercase letter", ErrInvalidCommodity, code)
			}
		default:
			return fmt.Errorf("%w: %q contains %q (allowed: A-Z 0-9 . _ -)",
				ErrInvalidCommodity, code, string(ch))
		}
	}
	return nil
}

// Registry resolves a commodity code to its description. It is immutable once
// built; With returns a new registry rather than mutating the receiver, so a
// registry handed to a plugin cannot be altered underneath its owner.
type Registry struct {
	byCode map[CommodityCode]Commodity
}

// NewRegistry builds a registry from the given commodities. Declaring the same
// code twice is an error rather than a last-one-wins overwrite: two different
// scales for one code is exactly the kind of disagreement that corrupts money.
func NewRegistry(commodities ...Commodity) (*Registry, error) {
	r := &Registry{byCode: make(map[CommodityCode]Commodity, len(commodities))}
	return r.With(commodities...)
}

// With returns a copy of the registry extended with the given commodities.
func (r *Registry) With(commodities ...Commodity) (*Registry, error) {
	out := &Registry{byCode: make(map[CommodityCode]Commodity, len(r.byCode)+len(commodities))}
	for code, c := range r.byCode {
		out.byCode[code] = c
	}
	for _, c := range commodities {
		if c.IsZero() {
			return nil, fmt.Errorf("%w: unset commodity", ErrInvalidCommodity)
		}
		if existing, ok := out.byCode[c.code]; ok && existing != c {
			return nil, fmt.Errorf("%w: %q is already registered with a different description",
				ErrDuplicateID, c.code)
		}
		out.byCode[c.code] = c
	}
	return out, nil
}

// Lookup returns the description of a code.
func (r *Registry) Lookup(code CommodityCode) (Commodity, bool) {
	c, ok := r.byCode[code]
	return c, ok
}

// Require returns the description of a code, or an error naming the code that
// is missing. Use it where a missing commodity is a bug rather than a branch.
func (r *Registry) Require(code CommodityCode) (Commodity, error) {
	c, ok := r.byCode[code]
	if !ok {
		return Commodity{}, fmt.Errorf("%w: %q", ErrUnknownCommodity, code)
	}
	return c, nil
}

// Codes returns every registered code in sorted order, so that anything built
// from a registry — a report, a dropdown, a test — is deterministic.
func (r *Registry) Codes() []CommodityCode {
	codes := make([]CommodityCode, 0, len(r.byCode))
	for code := range r.byCode {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	return codes
}

// Len reports how many commodities the registry describes.
func (r *Registry) Len() int { return len(r.byCode) }

// currencyScales lists the ISO 4217 minor units for the currencies core ships
// with. Most currencies use two decimal places; the exceptions are what this
// table exists to record.
//
// This is deliberately not the full ISO table. Core carries the currencies a
// self-hoster is likely to hold, and a Country Pack registers the rest along
// with its own securities — an exchange's share scale and a fund's unit scale
// are country-specific facts, and §6 keeps those out of core.
var currencyScales = map[CommodityCode]uint8{
	"AED": 2, "AUD": 2, "BHD": 3, "BRL": 2, "CAD": 2, "CHF": 2, "CNY": 2,
	"DKK": 2, "EUR": 2, "GBP": 2, "HKD": 2, "IDR": 2, "INR": 2, "JOD": 3,
	"JPY": 0, "KRW": 0, "KWD": 3, "MXN": 2, "MYR": 2, "NOK": 2, "NZD": 2,
	"OMR": 3, "PHP": 2, "PLN": 2, "SAR": 2, "SEK": 2, "SGD": 2, "THB": 2,
	"TRY": 2, "TWD": 2, "USD": 2, "VND": 0, "ZAR": 2,
}

// StandardRegistry returns the commodities core knows about without any
// Country Pack loaded: the currencies in currencyScales, plus bitcoin and
// ether because their scales are properties of the chains themselves rather
// than of any country.
func StandardRegistry() *Registry {
	r := &Registry{byCode: make(map[CommodityCode]Commodity, len(currencyScales)+2)}
	for code, scale := range currencyScales {
		r.byCode[code] = Commodity{code: code, kind: KindCurrency, scale: scale}
	}
	r.byCode["BTC"] = Commodity{code: "BTC", kind: KindCrypto, scale: 8}
	r.byCode["ETH"] = Commodity{code: "ETH", kind: KindCrypto, scale: 18}
	return r
}
