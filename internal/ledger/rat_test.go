// SPDX-License-Identifier: MIT

package ledger_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nusa-app/nusa/internal/ledger"
)

// The table that pins down §4.6. Half-to-even is not the rounding most people
// learn at school, and the cases that separate it from half-away-from-zero are
// exactly the ties — so every tie, on both sides of zero, is here.
func TestRoundIsHalfToEven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		num  int64
		den  int64
		want int64
	}{
		{"below the halfway mark rounds down", 2, 5, 0},
		{"above the halfway mark rounds up", 3, 5, 1},
		{"a tie at zero point five keeps the even zero", 1, 2, 0},
		{"a tie at one point five moves up to the even two", 3, 2, 2},
		{"a tie at two point five stays at the even two", 5, 2, 2},
		{"a tie at three point five moves up to the even four", 7, 2, 4},
		{"a tie at four point five stays at the even four", 9, 2, 4},

		{"below the halfway mark rounds towards zero when negative", -2, 5, 0},
		{"above the halfway mark rounds away from zero when negative", -3, 5, -1},
		{"a negative tie at minus zero point five keeps the even zero", -1, 2, 0},
		{"a negative tie at minus one point five moves to the even minus two", -3, 2, -2},
		{"a negative tie at minus two point five stays at the even minus two", -5, 2, -2},
		{"a negative tie at minus three point five moves to the even minus four", -7, 2, -4},

		{"a whole number is unchanged", 1_500_000, 1, 1_500_000},
		{"a negative whole number is unchanged", -1_500_000, 1, -1_500_000},
		{"a third rounds down", 1_000, 3, 333},
		{"two thirds round up", 2_000, 3, 667},
		{"minus two thirds round away from zero", -2_000, 3, -667},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rounded, err := mustRat(t, idr, tc.num, tc.den).Round()
			require.NoError(t, err)
			requireMoney(t, mustMoney(t, idr, tc.want), rounded)
		})
	}
}

// Half-away-from-zero drifts upward over many ties; half-to-even does not.
// Summing the ties from 0,5 to 9,5 makes the difference visible: they round to
// 0, 2, 2, 4, 4, 6, 6, 8, 8, 10, which totals 50 — the same as the unrounded
// sum. Half-away-from-zero would total 55.
func TestHalfToEvenDoesNotDriftAcrossManyTies(t *testing.T) {
	t.Parallel()

	total := int64(0)
	for n := int64(1); n <= 19; n += 2 {
		rounded, err := mustRat(t, idr, n, 2).Round()
		require.NoError(t, err)
		total += rounded.Amount().Int64()
	}

	require.Equal(t, int64(50), total, "half-to-even should not accumulate an upward bias")
}

func TestRoundKeepsTheCommodity(t *testing.T) {
	t.Parallel()

	rounded, err := mustRat(t, btc, 3, 2).Round()
	require.NoError(t, err)
	require.Equal(t, btc, rounded.Commodity())
}

// Money is already a whole number of smallest units, so widening it and
// rounding it back has to be the identity. If it is not, some calculation
// somewhere is being quietly nudged just by passing through Rat.
func TestMoneyRatRoundTripIsIdentity(t *testing.T) {
	t.Parallel()

	for _, minorUnits := range []int64{0, 1, -1, 1_500_000, -1_500_000, 999_999_999} {
		original := mustMoney(t, idr, minorUnits)

		widened, err := original.Rat()
		require.NoError(t, err)

		rounded, err := widened.Round()
		require.NoError(t, err)

		requireMoney(t, original, rounded)
	}
}

func TestRatArithmeticStaysExact(t *testing.T) {
	t.Parallel()

	// A third of a rupiah, three times, is a whole rupiah — but only if
	// nothing rounds in between. This is the entire argument for Rat.
	third, err := mustMoney(t, idr, 1).Div(big.NewRat(3, 1))
	require.NoError(t, err)

	sum := third
	for i := 0; i < 2; i++ {
		sum, err = sum.Add(third)
		require.NoError(t, err)
	}

	require.True(t, sum.IsExact())
	rounded, err := sum.Round()
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 1), rounded)

	// Rounding each third first loses a rupiah, which is what the type is
	// there to stop happening by accident.
	roundedThird, err := third.Round()
	require.NoError(t, err)
	require.Equal(t, int64(0), roundedThird.Amount().Int64())
}

func TestRatArithmeticStaysWithinOneCommodity(t *testing.T) {
	t.Parallel()

	rupiah := mustRat(t, idr, 1, 3)
	dollars := mustRat(t, usd, 1, 3)

	_, err := rupiah.Add(dollars)
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)

	_, err = rupiah.Sub(dollars)
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)

	_, err = rupiah.Cmp(dollars)
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)

	require.False(t, rupiah.Equal(dollars))
	require.True(t, rupiah.Equal(mustRat(t, idr, 2, 6)))
}

func TestRatNegAbsAndSign(t *testing.T) {
	t.Parallel()

	r := mustRat(t, idr, -1_000, 3)
	require.Equal(t, -1, r.Sign())

	neg, err := r.Neg()
	require.NoError(t, err)
	require.Equal(t, 1, neg.Sign())

	abs, err := r.Abs()
	require.NoError(t, err)
	require.True(t, abs.Equal(neg))

	zero := mustRat(t, idr, 0, 1)
	require.True(t, zero.IsZero())
	require.Equal(t, 0, zero.Sign())
}

func TestRatMulAndDiv(t *testing.T) {
	t.Parallel()

	r := mustRat(t, idr, 100, 1)

	doubled, err := r.Mul(big.NewRat(2, 1))
	require.NoError(t, err)
	require.True(t, doubled.Equal(mustRat(t, idr, 200, 1)))

	halved, err := r.Div(big.NewRat(2, 1))
	require.NoError(t, err)
	require.True(t, halved.Equal(mustRat(t, idr, 50, 1)))

	_, err = r.Div(new(big.Rat))
	require.ErrorIs(t, err, ledger.ErrDivideByZero)
}

func TestRatCopiesItsValueOnTheWayInAndOut(t *testing.T) {
	t.Parallel()

	source := big.NewRat(1, 3)
	r, err := ledger.NewRat(idr, source)
	require.NoError(t, err)

	source.SetInt64(99)
	leaked := r.Value()
	leaked.SetInt64(42)

	require.True(t, r.Equal(mustRat(t, idr, 1, 3)), "got %s", r)
}

func TestRatZeroValueIsNotUsable(t *testing.T) {
	t.Parallel()

	var unset ledger.Rat

	require.False(t, unset.IsValid())
	require.Equal(t, "<invalid rat>", unset.String())
	require.Nil(t, unset.Value())

	_, err := unset.Round()
	require.ErrorIs(t, err, ledger.ErrInvalidMoney)

	_, err = unset.Add(mustRat(t, idr, 1, 1))
	require.ErrorIs(t, err, ledger.ErrInvalidMoney)
}

func TestNewRatRejectsBadInput(t *testing.T) {
	t.Parallel()

	_, err := ledger.NewRat("", big.NewRat(1, 1))
	require.ErrorIs(t, err, ledger.ErrInvalidCommodity)

	_, err = ledger.NewRat(idr, nil)
	require.ErrorIs(t, err, ledger.ErrInvalidMoney)
}
