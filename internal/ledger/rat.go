// SPDX-License-Identifier: MIT

package ledger

import (
	"fmt"
	"math/big"
)

// Rat is an exact but not-yet-representable quantity of one commodity,
// measured in that commodity's smallest unit.
//
// Multiplication, division and currency conversion all produce values that no
// number of rupiah or satoshi can express: a third of Rp 1.000 is 333⅓ rupiah,
// not 333 and not 334. Rat holds that ⅓ exactly, as a ratio of two integers,
// for as long as the calculation lasts.
//
// This type exists so that §4.6 — round once, at the last step — is enforced
// by the compiler rather than by discipline. Every operation that cannot stay
// whole returns a Rat, a Rat cannot be stored or sent anywhere, and the only
// way back to Money is Round. A calculation that rounds halfway through has to
// be written deliberately, by calling Round in the middle, where a reviewer
// can see it.
//
// Like Money, its fields are unexported and its value is copied in and out.
type Rat struct {
	value     *big.Rat
	commodity CommodityCode
}

// NewRat returns an exact quantity of commodity, measured in that commodity's
// smallest unit. The value is copied.
func NewRat(commodity CommodityCode, value *big.Rat) (Rat, error) {
	if err := validateCommodityCode(commodity); err != nil {
		return Rat{}, err
	}
	if value == nil {
		return Rat{}, fmt.Errorf("%w: nil value for %q", ErrInvalidMoney, commodity)
	}
	return Rat{value: new(big.Rat).Set(value), commodity: commodity}, nil
}

// IsValid reports whether the value came from a constructor.
func (r Rat) IsValid() bool { return r.value != nil && r.commodity != "" }

func (r Rat) validate() error {
	if !r.IsValid() {
		return fmt.Errorf("%w: uninitialised", ErrInvalidMoney)
	}
	return nil
}

// Value returns a copy of the exact quantity in smallest units, or nil if the
// value was never constructed.
func (r Rat) Value() *big.Rat {
	if r.value == nil {
		return nil
	}
	return new(big.Rat).Set(r.value)
}

// Commodity returns what the quantity is denominated in.
func (r Rat) Commodity() CommodityCode { return r.commodity }

// Sign returns -1, 0 or +1.
func (r Rat) Sign() int {
	if r.value == nil {
		return 0
	}
	return r.value.Sign()
}

// IsZero reports whether the quantity is exactly zero.
func (r Rat) IsZero() bool { return r.value != nil && r.value.Sign() == 0 }

// IsExact reports whether the quantity is already a whole number of smallest
// units, so that Round would not change it. Useful for asserting that a
// calculation happened to come out even, never for skipping Round.
func (r Rat) IsExact() bool { return r.value != nil && r.value.IsInt() }

// Add returns r + other.
func (r Rat) Add(other Rat) (Rat, error) {
	if err := r.sameCommodity(other, "add"); err != nil {
		return Rat{}, err
	}
	return Rat{value: new(big.Rat).Add(r.value, other.value), commodity: r.commodity}, nil
}

// Sub returns r - other.
func (r Rat) Sub(other Rat) (Rat, error) {
	if err := r.sameCommodity(other, "subtract"); err != nil {
		return Rat{}, err
	}
	return Rat{value: new(big.Rat).Sub(r.value, other.value), commodity: r.commodity}, nil
}

// Neg returns -r.
func (r Rat) Neg() (Rat, error) {
	if err := r.validate(); err != nil {
		return Rat{}, err
	}
	return Rat{value: new(big.Rat).Neg(r.value), commodity: r.commodity}, nil
}

// Abs returns |r|.
func (r Rat) Abs() (Rat, error) {
	if err := r.validate(); err != nil {
		return Rat{}, err
	}
	return Rat{value: new(big.Rat).Abs(r.value), commodity: r.commodity}, nil
}

// Mul multiplies by an exact fraction, staying exact.
func (r Rat) Mul(factor *big.Rat) (Rat, error) {
	if err := r.validate(); err != nil {
		return Rat{}, err
	}
	if factor == nil {
		return Rat{}, fmt.Errorf("%w: nil factor", ErrInvalidMoney)
	}
	return Rat{value: new(big.Rat).Mul(r.value, factor), commodity: r.commodity}, nil
}

// Div divides by an exact fraction, staying exact.
func (r Rat) Div(divisor *big.Rat) (Rat, error) {
	if err := r.validate(); err != nil {
		return Rat{}, err
	}
	if divisor == nil {
		return Rat{}, fmt.Errorf("%w: nil divisor", ErrInvalidMoney)
	}
	if divisor.Sign() == 0 {
		return Rat{}, ErrDivideByZero
	}
	return Rat{value: new(big.Rat).Quo(r.value, divisor), commodity: r.commodity}, nil
}

// Cmp compares two exact quantities of the same commodity.
func (r Rat) Cmp(other Rat) (int, error) {
	if err := r.sameCommodity(other, "compare"); err != nil {
		return 0, err
	}
	return r.value.Cmp(other.value), nil
}

// Equal reports whether two quantities are exactly equal in the same
// commodity.
func (r Rat) Equal(other Rat) bool {
	if !r.IsValid() || !other.IsValid() {
		return false
	}
	return r.commodity == other.commodity && r.value.Cmp(other.value) == 0
}

func (r Rat) sameCommodity(other Rat, op string) error {
	if err := r.validate(); err != nil {
		return err
	}
	if err := other.validate(); err != nil {
		return err
	}
	if r.commodity != other.commodity {
		return fmt.Errorf("%w: cannot %s %s and %s", ErrCommodityMismatch, op, r.commodity, other.commodity)
	}
	return nil
}

// Round collapses the exact quantity to a whole number of the commodity's
// smallest units, using banker's rounding, and returns it as Money.
//
// This is the only conversion from Rat to Money and the only place in Nusa
// where rounding happens. Everything else composes.
//
// The value is already counted in smallest units, so no scale is needed here:
// a Rat of "333⅓" IDR is a third of a rupiah short of 333 rupiah, and Round
// answers which whole rupiah that is.
func (r Rat) Round() (Money, error) {
	if err := r.validate(); err != nil {
		return Money{}, err
	}
	return Money{amount: roundHalfToEven(r.value), commodity: r.commodity}, nil
}

// String renders the quantity for logs and test failures, for example
// "IDR 1000/3". It is never user-facing.
func (r Rat) String() string {
	if !r.IsValid() {
		return "<invalid rat>"
	}
	return string(r.commodity) + " " + r.value.RatString()
}

// roundHalfToEven rounds a ratio to the nearest integer, and to the nearest
// even integer when it sits exactly halfway.
//
// Half-to-even ("banker's rounding") is §4.6's choice because half-away-from-
// zero has a bias: over many transactions it drifts upward, so a column of
// rounded figures stops matching the unrounded total in a way that grows with
// the number of rows. Half-to-even sends ties up and down in equal measure, so
// the drift cancels instead of accumulating.
//
// This function is unexported and called from exactly one place. Rounding
// having a single implementation is the point; a second one would be a second
// answer to "what is half a rupiah".
func roundHalfToEven(v *big.Rat) *big.Int {
	num, den := v.Num(), v.Denom()
	if v.IsInt() {
		return new(big.Int).Set(num)
	}

	// QuoRem truncates toward zero, so quo is the neighbour on the zero side
	// and rem carries the sign of the value.
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))

	// Compare twice the distance from quo against one whole step, which
	// answers "is the remainder below, above, or exactly at the halfway mark"
	// without leaving the integers.
	twiceRem := new(big.Int).Abs(rem)
	twiceRem.Lsh(twiceRem, 1)

	away := num.Sign() // the direction of the neighbour away from zero
	switch twiceRem.Cmp(den) {
	case -1:
		return quo
	case 1:
		return quo.Add(quo, big.NewInt(int64(away)))
	default:
		// Exactly halfway: keep the even neighbour. Bit(0) is the low bit of
		// the two's complement representation, so it reads oddness correctly
		// for negative values too.
		if quo.Bit(0) == 1 {
			return quo.Add(quo, big.NewInt(int64(away)))
		}
		return quo
	}
}
