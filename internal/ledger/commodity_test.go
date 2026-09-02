// SPDX-License-Identifier: MIT

package ledger_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/ledger"
)

func TestNewCommodityValidatesItsInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		code    ledger.CommodityCode
		kind    ledger.CommodityKind
		scale   uint8
		wantErr error
	}{
		{"no code", "", ledger.KindCurrency, 2, ledger.ErrInvalidCommodity},
		{"no kind", idr, ledger.KindUnknown, 2, ledger.ErrInvalidCommodity},
		{"scale beyond ether's", idr, ledger.KindCurrency, 19, ledger.ErrInvalidScale},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ledger.NewCommodity(tc.code, tc.kind, tc.scale)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestCommodityCodesAllowSecurityAndMetalNotation(t *testing.T) {
	t.Parallel()

	for _, code := range []ledger.CommodityCode{"IDR", "BBCA.JK", "XAU_GRAM", "SP-500"} {
		c, err := ledger.NewCommodity(code, ledger.KindEquity, 0)
		require.NoError(t, err, "should have accepted %q", code)
		require.Equal(t, code, c.Code())
	}
}

// The scales §4.2 names, checked against the registry the application starts
// with. A wrong scale here would misplace a decimal point everywhere at once.
func TestStandardRegistryScales(t *testing.T) {
	t.Parallel()

	reg := ledger.StandardRegistry()

	tests := []struct {
		code  ledger.CommodityCode
		kind  ledger.CommodityKind
		scale uint8
	}{
		{idr, ledger.KindCurrency, 2},
		{usd, ledger.KindCurrency, 2},
		{jpy, ledger.KindCurrency, 0},
		{"KWD", ledger.KindCurrency, 3},
		{btc, ledger.KindCrypto, 8},
		{"ETH", ledger.KindCrypto, 18},
	}

	for _, tc := range tests {
		c, ok := reg.Lookup(tc.code)
		require.True(t, ok, "%s should be in the standard registry", tc.code)
		require.Equal(t, tc.scale, c.Scale(), "%s scale", tc.code)
		require.Equal(t, tc.kind, c.Kind(), "%s kind", tc.code)
	}
}

// Country-specific commodities are not in core (§6). An Indonesian equity is
// unknown until a pack registers it.
func TestStandardRegistryHasNoCountrySpecificCommodities(t *testing.T) {
	t.Parallel()

	_, ok := ledger.StandardRegistry().Lookup(bbca)
	require.False(t, ok, "an exchange's share scale belongs to a Country Pack")

	_, err := ledger.StandardRegistry().Require(bbca)
	require.ErrorIs(t, err, ledger.ErrUnknownCommodity)
}

func TestRegistryWithReturnsACopy(t *testing.T) {
	t.Parallel()

	base := ledger.StandardRegistry()
	baseSize := base.Len()

	extended, err := base.With(mustCommodity(t, bbca, ledger.KindEquity, 0))
	require.NoError(t, err)

	require.Equal(t, baseSize, base.Len(), "the original registry must not change")
	require.Equal(t, baseSize+1, extended.Len())

	_, ok := base.Lookup(bbca)
	require.False(t, ok)
}

// Two descriptions of one code is how a scale disagreement gets in, so it is
// refused rather than resolved.
func TestRegistryRejectsAConflictingRedefinition(t *testing.T) {
	t.Parallel()

	_, err := ledger.StandardRegistry().With(mustCommodity(t, idr, ledger.KindCurrency, 0))
	require.ErrorIs(t, err, ledger.ErrDuplicateID)

	// Registering the identical description again is harmless.
	_, err = ledger.StandardRegistry().With(mustCommodity(t, idr, ledger.KindCurrency, 2))
	require.NoError(t, err)
}

func TestRegistryCodesAreSorted(t *testing.T) {
	t.Parallel()

	reg, err := ledger.NewRegistry(
		mustCommodity(t, usd, ledger.KindCurrency, 2),
		mustCommodity(t, idr, ledger.KindCurrency, 2),
		mustCommodity(t, btc, ledger.KindCrypto, 8),
	)
	require.NoError(t, err)

	require.Equal(t, []ledger.CommodityCode{btc, idr, usd}, reg.Codes())
}

func TestCommodityKindNames(t *testing.T) {
	t.Parallel()

	require.Equal(t, "currency", ledger.KindCurrency.String())
	require.Equal(t, "equity", ledger.KindEquity.String())
	require.Equal(t, "fund", ledger.KindFund.String())
	require.Equal(t, "crypto", ledger.KindCrypto.String())
	require.Equal(t, "metal", ledger.KindMetal.String())
	require.Equal(t, "unknown", ledger.KindUnknown.String())
}
