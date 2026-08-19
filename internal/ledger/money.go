// SPDX-License-Identifier: MIT

package ledger

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// Money is an exact quantity of one commodity, counted in that commodity's
// smallest unit.
//
// There is no floating point anywhere near this type and there never will be.
// Rp 15.000,00 is Money{amount: 1500000, commodity: "IDR"} because IDR has two
// decimal places; 0.5 BTC is 50000000 because BTC has eight. The scale itself
// lives on Commodity, never here, so two values of the same commodity cannot
// disagree about where the decimal point sits.
//
// The fields are unexported and the amount is copied on the way in and on the
// way out. A Money value that has been handed to something else can never be
// changed by it, which is what makes §5's immutability rule hold through a
// pointer rather than merely by convention.
//
// The zero Money is not a valid amount — it has no commodity. Use ZeroMoney to
// get a genuine zero.
type Money struct {
	amount    *big.Int
	commodity CommodityCode
}

// NewMoney returns amount of commodity, counted in the commodity's smallest
// unit. The amount is copied, so later changes to the argument do not reach
// the returned value.
func NewMoney(commodity CommodityCode, amount *big.Int) (Money, error) {
	if err := validateCommodityCode(commodity); err != nil {
		return Money{}, err
	}
	if amount == nil {
		return Money{}, fmt.Errorf("%w: nil amount for %q", ErrInvalidMoney, commodity)
	}
	return Money{amount: new(big.Int).Set(amount), commodity: commodity}, nil
}

// MoneyFromInt returns a whole number of the commodity's smallest units.
func MoneyFromInt(commodity CommodityCode, minorUnits int64) (Money, error) {
	return NewMoney(commodity, big.NewInt(minorUnits))
}

// ZeroMoney returns zero of the given commodity. A zero still has a commodity:
// "nothing" is only meaningful once you know nothing of what.
func ZeroMoney(commodity CommodityCode) (Money, error) {
	return NewMoney(commodity, new(big.Int))
}

// ParseMoney reads an amount written as a base-ten integer of the commodity's
// smallest units, which is how money crosses the wire (§4.5). It rejects
// anything a decimal point or an exponent could hide in.
func ParseMoney(commodity CommodityCode, minorUnits string) (Money, error) {
	amount, ok := new(big.Int).SetString(minorUnits, 10)
	if !ok {
		return Money{}, fmt.Errorf("%w: %q is not an integer count of %q's smallest unit",
			ErrInvalidMoney, minorUnits, commodity)
	}
	return NewMoney(commodity, amount)
}

// IsValid reports whether the value came from a constructor. Operations on an
// invalid Money return an error rather than treating it as zero, because a
// forgotten commodity is a bug and silently reading it as zero hides it.
func (m Money) IsValid() bool { return m.amount != nil && m.commodity != "" }

func (m Money) validate() error {
	if !m.IsValid() {
		return fmt.Errorf("%w: uninitialised", ErrInvalidMoney)
	}
	return nil
}

// Amount returns a copy of the quantity in the commodity's smallest unit, or
// nil if the value was never constructed. The copy is what keeps the receiver
// immutable.
func (m Money) Amount() *big.Int {
	if m.amount == nil {
		return nil
	}
	return new(big.Int).Set(m.amount)
}

// Commodity returns what the quantity is denominated in.
func (m Money) Commodity() CommodityCode { return m.commodity }

// Sign returns -1, 0 or +1. An invalid Money reports 0, which is why Sign is
// for presentation and branching, not for validity checks.
func (m Money) Sign() int {
	if m.amount == nil {
		return 0
	}
	return m.amount.Sign()
}

// IsZero reports whether the quantity is exactly zero.
func (m Money) IsZero() bool { return m.amount != nil && m.amount.Sign() == 0 }

// Add returns m + other. Adding across commodities is an error, never an
// implicit conversion (§4.3).
func (m Money) Add(other Money) (Money, error) {
	if err := m.sameCommodity(other, "add"); err != nil {
		return Money{}, err
	}
	return Money{amount: new(big.Int).Add(m.amount, other.amount), commodity: m.commodity}, nil
}

// Sub returns m - other.
func (m Money) Sub(other Money) (Money, error) {
	if err := m.sameCommodity(other, "subtract"); err != nil {
		return Money{}, err
	}
	return Money{amount: new(big.Int).Sub(m.amount, other.amount), commodity: m.commodity}, nil
}

// Neg returns -m.
func (m Money) Neg() (Money, error) {
	if err := m.validate(); err != nil {
		return Money{}, err
	}
	return Money{amount: new(big.Int).Neg(m.amount), commodity: m.commodity}, nil
}

// Abs returns |m|.
func (m Money) Abs() (Money, error) {
	if err := m.validate(); err != nil {
		return Money{}, err
	}
	return Money{amount: new(big.Int).Abs(m.amount), commodity: m.commodity}, nil
}

// MulInt returns m multiplied by a whole number. It stays Money because
// multiplying a count of smallest units by an integer is exact — twelve
// instalments of Rp 250.000 is an exact amount, not a rounded one.
func (m Money) MulInt(factor int64) (Money, error) {
	if err := m.validate(); err != nil {
		return Money{}, err
	}
	return Money{
		amount:    new(big.Int).Mul(m.amount, big.NewInt(factor)),
		commodity: m.commodity,
	}, nil
}

// Mul multiplies by an exact fraction and returns a Rat, not a Money.
//
// The result is almost never a whole number of smallest units, and choosing a
// whole one is rounding. This signature is how §4.6's "round once, at the last
// step" stops being a habit and becomes something the compiler insists on:
// there is no way back to Money except Rat.Round.
func (m Money) Mul(factor *big.Rat) (Rat, error) {
	if err := m.validate(); err != nil {
		return Rat{}, err
	}
	if factor == nil {
		return Rat{}, fmt.Errorf("%w: nil factor", ErrInvalidMoney)
	}
	value := new(big.Rat).Mul(new(big.Rat).SetInt(m.amount), factor)
	return Rat{value: value, commodity: m.commodity}, nil
}

// Div divides by an exact fraction and returns a Rat, for the same reason Mul
// does.
func (m Money) Div(divisor *big.Rat) (Rat, error) {
	if err := m.validate(); err != nil {
		return Rat{}, err
	}
	if divisor == nil {
		return Rat{}, fmt.Errorf("%w: nil divisor", ErrInvalidMoney)
	}
	if divisor.Sign() == 0 {
		return Rat{}, ErrDivideByZero
	}
	value := new(big.Rat).Quo(new(big.Rat).SetInt(m.amount), divisor)
	return Rat{value: value, commodity: m.commodity}, nil
}

// Rat widens the value to the exact intermediate type, so it can take part in
// a longer calculation without being rounded on the way in.
func (m Money) Rat() (Rat, error) {
	if err := m.validate(); err != nil {
		return Rat{}, err
	}
	return Rat{value: new(big.Rat).SetInt(m.amount), commodity: m.commodity}, nil
}

// Cmp compares two amounts of the same commodity, returning -1, 0 or +1.
func (m Money) Cmp(other Money) (int, error) {
	if err := m.sameCommodity(other, "compare"); err != nil {
		return 0, err
	}
	return m.amount.Cmp(other.amount), nil
}

// Equal reports whether two values are the same amount of the same commodity.
// Unlike Cmp it does not report an error, because "is this the same" has a
// sensible answer across commodities and that answer is no.
func (m Money) Equal(other Money) bool {
	if !m.IsValid() || !other.IsValid() {
		return false
	}
	return m.commodity == other.commodity && m.amount.Cmp(other.amount) == 0
}

func (m Money) sameCommodity(other Money, op string) error {
	if err := m.validate(); err != nil {
		return err
	}
	if err := other.validate(); err != nil {
		return err
	}
	if m.commodity != other.commodity {
		return fmt.Errorf("%w: cannot %s %s and %s", ErrCommodityMismatch, op, m.commodity, other.commodity)
	}
	return nil
}

// String renders the value for logs and test failures as a count of smallest
// units, for example "IDR 1500000".
//
// It is not a user-facing format and must never be shown to anyone: rendering
// an amount needs the commodity's scale and the reader's locale, both of which
// live outside this package.
func (m Money) String() string {
	if !m.IsValid() {
		return "<invalid money>"
	}
	return string(m.commodity) + " " + m.amount.String()
}

// moneyJSON is the wire shape from §4.5. The amount is a string because a JSON
// number is an IEEE 754 double to most parsers, and a double stops being able
// to count whole rupiah above about nine quadrillion smallest units.
type moneyJSON struct {
	Amount    string        `json:"amount"`
	Commodity CommodityCode `json:"commodity"`
}

// MarshalJSON writes the amount as a decimal string.
//
// The encoding lives on the type rather than in an api DTO on purpose. §4.5 is
// a money rule, not a transport preference, and putting it here means no
// handler anywhere can accidentally serialise an amount as a JSON number.
func (m Money) MarshalJSON() ([]byte, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(moneyJSON{Amount: m.amount.String(), Commodity: m.commodity})
}

// UnmarshalJSON reads the wire shape, rejecting anything that is not an
// integer count of smallest units.
func (m *Money) UnmarshalJSON(data []byte) error {
	var raw moneyJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode money: %w", err)
	}
	parsed, err := ParseMoney(raw.Commodity, raw.Amount)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
