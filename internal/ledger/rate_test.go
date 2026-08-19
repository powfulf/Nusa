// SPDX-License-Identifier: MIT

package ledger_test

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nusa-app/nusa/internal/ledger"
)

func TestNewRateValidatesItsInput(t *testing.T) {
	t.Parallel()

	_, err := ledger.NewRate("", usd, big.NewRat(1, 1))
	require.ErrorIs(t, err, ledger.ErrInvalidCommodity)

	_, err = ledger.NewRate(usd, usd, big.NewRat(1, 1))
	require.ErrorIs(t, err, ledger.ErrInvalidRate)

	_, err = ledger.NewRate(usd, idr, nil)
	require.ErrorIs(t, err, ledger.ErrInvalidRate)

	_, err = ledger.NewRate(usd, idr, new(big.Rat))
	require.ErrorIs(t, err, ledger.ErrInvalidRate)

	_, err = ledger.NewRate(usd, idr, big.NewRat(-16_000, 1))
	require.ErrorIs(t, err, ledger.ErrInvalidRate)
}

// A rate is quoted in the units people say: one dollar buys sixteen thousand
// rupiah. Converting has to cross into smallest units on both sides, and both
// sides have their own scale — which is why Convert lives on the registry.
func TestConvertCrossesBothScales(t *testing.T) {
	t.Parallel()

	reg := testRegistry(t)
	rate := mustRate(t, usd, idr, 16_000, 1)

	// USD 1,00 is 100 cents; IDR 16.000,00 is 1.600.000 sen.
	converted, err := reg.Convert(mustMoney(t, usd, 100), rate)
	require.NoError(t, err)

	rounded, err := converted.Round()
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 1_600_000), rounded)
}

// Yen has no decimal places and rupiah has two, so a conversion between them
// crosses a hundredfold difference in scale that has nothing to do with the
// rate itself. Getting this wrong is a two-decimal-place error, which is the
// kind that goes unnoticed until someone reconciles.
func TestConvertBetweenCommoditiesOfDifferentScale(t *testing.T) {
	t.Parallel()

	reg := testRegistry(t)

	// One yen buys 105 rupiah. JPY 1.000 is 1000 smallest units (scale 0);
	// the result is IDR 105.000,00, which is 10.500.000 sen.
	converted, err := reg.Convert(mustMoney(t, jpy, 1_000), mustRate(t, jpy, idr, 105, 1))
	require.NoError(t, err)

	rounded, err := converted.Round()
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 10_500_000), rounded)
}

func TestConvertRejectsAMismatchedRate(t *testing.T) {
	t.Parallel()

	reg := testRegistry(t)

	_, err := reg.Convert(mustMoney(t, idr, 100), mustRate(t, usd, idr, 16_000, 1))
	require.ErrorIs(t, err, ledger.ErrInvalidRate)

	_, err = reg.Convert(mustMoney(t, usd, 100), ledger.Rate{})
	require.ErrorIs(t, err, ledger.ErrInvalidRate)
}

func TestConvertRejectsAnUnregisteredCommodity(t *testing.T) {
	t.Parallel()

	// The standard registry has no Indonesian equities — a pack registers
	// those — so it cannot know how many decimal places a share has.
	_, err := ledger.StandardRegistry().Convert(mustMoney(t, idr, 100), mustRate(t, idr, bbca, 1, 10_000))
	require.ErrorIs(t, err, ledger.ErrUnknownCommodity)
}

// Inverting a ratio is exact, so a conversion and its reverse come back to
// precisely where they started whenever the intermediate is representable.
func TestConvertAndBackReturnsTheOriginal(t *testing.T) {
	t.Parallel()

	reg := testRegistry(t)
	rate := mustRate(t, usd, idr, 16_000, 1)
	original := mustMoney(t, usd, 12_345)

	inRupiah, err := reg.Convert(original, rate)
	require.NoError(t, err)
	require.True(t, inRupiah.IsExact(), "this rate lands on whole sen: %s", inRupiah)

	asMoney, err := inRupiah.Round()
	require.NoError(t, err)

	inverted, err := rate.Invert()
	require.NoError(t, err)
	require.Equal(t, idr, inverted.Base())
	require.Equal(t, usd, inverted.Quote())

	back, err := reg.Convert(asMoney, inverted)
	require.NoError(t, err)

	rounded, err := back.Round()
	require.NoError(t, err)
	requireMoney(t, original, rounded)
}

// A rate that does not divide evenly must produce an inexact Rat rather than a
// quietly rounded one. If Convert ever rounded on its own there would be two
// rounding sites in the codebase, and §4.6 allows one.
func TestConvertDoesNotRoundOnItsOwn(t *testing.T) {
	t.Parallel()

	reg := testRegistry(t)

	// Three rupiah split across a rate of 1/7 cannot land on whole sen.
	converted, err := reg.Convert(mustMoney(t, idr, 3), mustRate(t, idr, usd, 1, 7))
	require.NoError(t, err)
	require.False(t, converted.IsExact(), "Convert must hand back the fraction, not a decision about it")
}

func TestRateJSONRoundTripStaysExact(t *testing.T) {
	t.Parallel()

	// A rate that no finite decimal can hold, so a lossy encoding would show.
	rate := mustRate(t, usd, idr, 50_000, 3)

	encoded, err := json.Marshal(rate)
	require.NoError(t, err)
	require.JSONEq(t, `{"base":"USD","quote":"IDR","value":"50000/3"}`, string(encoded))

	var decoded ledger.Rate
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.True(t, rate.Equal(decoded), "want %s, got %s", rate, decoded)
}

func TestRateJSONHandlesTheUnsetRate(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(ledger.Rate{})
	require.NoError(t, err)
	require.Equal(t, "null", string(encoded))

	var decoded ledger.Rate
	require.NoError(t, json.Unmarshal([]byte("null"), &decoded))
	require.True(t, decoded.IsZero())
}

func TestRateJSONRejectsAnInexactValue(t *testing.T) {
	t.Parallel()

	var decoded ledger.Rate
	err := json.Unmarshal([]byte(`{"base":"USD","quote":"IDR","value":"about sixteen thousand"}`), &decoded)
	require.ErrorIs(t, err, ledger.ErrInvalidRate)
}

func TestRateCopiesItsValue(t *testing.T) {
	t.Parallel()

	source := big.NewRat(16_000, 1)
	rate, err := ledger.NewRate(usd, idr, source)
	require.NoError(t, err)

	source.SetInt64(1)
	leaked := rate.Value()
	leaked.SetInt64(2)

	require.True(t, rate.Equal(mustRate(t, usd, idr, 16_000, 1)), "got %s", rate)
}

func TestZeroRate(t *testing.T) {
	t.Parallel()

	var unset ledger.Rate

	require.True(t, unset.IsZero())
	require.Equal(t, "<no rate>", unset.String())
	require.Nil(t, unset.Value())

	_, err := unset.Invert()
	require.ErrorIs(t, err, ledger.ErrInvalidRate)

	require.True(t, unset.Equal(ledger.Rate{}))
	require.False(t, unset.Equal(mustRate(t, usd, idr, 1, 1)))
}
