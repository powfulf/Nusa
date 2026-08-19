// SPDX-License-Identifier: MIT

package ledger_test

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nusa-app/nusa/internal/ledger"
)

func TestNewMoneyRejectsBadInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		commodity ledger.CommodityCode
		amount    *big.Int
		wantErr   error
	}{
		{"empty commodity", "", big.NewInt(1), ledger.ErrInvalidCommodity},
		{"lowercase commodity", "idr", big.NewInt(1), ledger.ErrInvalidCommodity},
		{"commodity starting with a digit", "1DR", big.NewInt(1), ledger.ErrInvalidCommodity},
		{"commodity with a space", "ID R", big.NewInt(1), ledger.ErrInvalidCommodity},
		{"nil amount", idr, nil, ledger.ErrInvalidMoney},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ledger.NewMoney(tc.commodity, tc.amount)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// The caller keeps a pointer to the big.Int it passed in. If Money held that
// same pointer, the caller could change an amount that has already been
// posted, which is §5.3 broken through the back door.
func TestMoneyCopiesItsAmountOnTheWayIn(t *testing.T) {
	t.Parallel()

	source := big.NewInt(1_500_000)
	m, err := ledger.NewMoney(idr, source)
	require.NoError(t, err)

	source.SetInt64(999)

	requireMoney(t, mustMoney(t, idr, 1_500_000), m)
}

func TestMoneyCopiesItsAmountOnTheWayOut(t *testing.T) {
	t.Parallel()

	m := mustMoney(t, idr, 1_500_000)

	leaked := m.Amount()
	leaked.SetInt64(999)

	requireMoney(t, mustMoney(t, idr, 1_500_000), m)
	require.Equal(t, "1500000", m.Amount().String())
}

func TestMoneyArithmeticStaysWithinOneCommodity(t *testing.T) {
	t.Parallel()

	rupiah := mustMoney(t, idr, 1_500_000)
	dollars := mustMoney(t, usd, 10_000)

	_, err := rupiah.Add(dollars)
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)

	_, err = rupiah.Sub(dollars)
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)

	_, err = rupiah.Cmp(dollars)
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)

	require.False(t, rupiah.Equal(dollars))
}

func TestMoneyAddSubNegAbs(t *testing.T) {
	t.Parallel()

	a := mustMoney(t, idr, 1_500_000)
	b := mustMoney(t, idr, 250_000)

	sum, err := a.Add(b)
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 1_750_000), sum)

	diff, err := a.Sub(b)
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 1_250_000), diff)

	neg, err := a.Neg()
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, -1_500_000), neg)

	abs, err := neg.Abs()
	require.NoError(t, err)
	requireMoney(t, a, abs)

	cmp, err := a.Cmp(b)
	require.NoError(t, err)
	require.Equal(t, 1, cmp)
}

// Multiplying a count of smallest units by a whole number cannot land between
// two units, so it stays Money. Twelve instalments of Rp 250.000 is an exact
// figure and rounding it would be inventing imprecision.
func TestMoneyMulIntStaysExact(t *testing.T) {
	t.Parallel()

	instalment := mustMoney(t, idr, 25_000_000)

	year, err := instalment.MulInt(12)
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 300_000_000), year)
}

// Mul and Div return Rat, not Money. This is §4.6 enforced by the type system:
// there is no signature that lets a fraction quietly become a whole number.
func TestMoneyMulAndDivReturnRat(t *testing.T) {
	t.Parallel()

	m := mustMoney(t, idr, 1_000)

	third, err := m.Div(big.NewRat(3, 1))
	require.NoError(t, err)
	require.Equal(t, "IDR 1000/3", third.String())
	require.False(t, third.IsExact())

	scaled, err := m.Mul(big.NewRat(1, 8))
	require.NoError(t, err)
	require.Equal(t, "IDR 125", scaled.String())
	require.True(t, scaled.IsExact())
}

func TestMoneyDivByZero(t *testing.T) {
	t.Parallel()

	_, err := mustMoney(t, idr, 1_000).Div(new(big.Rat))
	require.ErrorIs(t, err, ledger.ErrDivideByZero)
}

func TestMoneyZeroValueIsNotUsable(t *testing.T) {
	t.Parallel()

	var unset ledger.Money

	require.False(t, unset.IsValid())
	require.Equal(t, "<invalid money>", unset.String())
	require.Nil(t, unset.Amount())

	_, err := unset.Add(mustMoney(t, idr, 1))
	require.ErrorIs(t, err, ledger.ErrInvalidMoney)

	_, err = unset.Neg()
	require.ErrorIs(t, err, ledger.ErrInvalidMoney)

	_, err = unset.Rat()
	require.ErrorIs(t, err, ledger.ErrInvalidMoney)
}

func TestZeroMoneyKeepsItsCommodity(t *testing.T) {
	t.Parallel()

	zero, err := ledger.ZeroMoney(idr)
	require.NoError(t, err)
	require.True(t, zero.IsZero())
	require.Equal(t, idr, zero.Commodity())

	other, err := ledger.ZeroMoney(usd)
	require.NoError(t, err)

	// Zero rupiah and zero dollars are both nothing, but they are not
	// interchangeable — adding them is still a mismatch.
	require.False(t, zero.Equal(other))
	_, err = zero.Add(other)
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)
}

func TestParseMoneyRejectsAnythingButAnInteger(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "1.5", "1e6", "1_000", " 100", "abc", "0x10"} {
		_, err := ledger.ParseMoney(idr, raw)
		require.ErrorIs(t, err, ledger.ErrInvalidMoney, "should have rejected %q", raw)
	}

	parsed, err := ledger.ParseMoney(idr, "-1500000")
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, -1_500_000), parsed)
}

// §4.5: the amount crosses the wire as a string, because a JSON number is an
// IEEE 754 double to most parsers and a double cannot hold every rupiah.
func TestMoneyJSONRoundTripKeepsPrecisionBeyondDoublePrecision(t *testing.T) {
	t.Parallel()

	// 2^53 + 1: the first integer an IEEE 754 double cannot represent.
	huge, ok := new(big.Int).SetString("9007199254740993", 10)
	require.True(t, ok)

	original, err := ledger.NewMoney(idr, huge)
	require.NoError(t, err)

	encoded, err := json.Marshal(original)
	require.NoError(t, err)
	require.JSONEq(t, `{"amount":"9007199254740993","commodity":"IDR"}`, string(encoded))

	var decoded ledger.Money
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	requireMoney(t, original, decoded)
}

func TestMoneyJSONRejectsANumericAmount(t *testing.T) {
	t.Parallel()

	var decoded ledger.Money
	err := json.Unmarshal([]byte(`{"amount":1500000,"commodity":"IDR"}`), &decoded)
	require.Error(t, err)

	err = json.Unmarshal([]byte(`{"amount":"1500.00","commodity":"IDR"}`), &decoded)
	require.ErrorIs(t, err, ledger.ErrInvalidMoney)
}
