// SPDX-License-Identifier: MIT

package ledger_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// Buying USD 100,00 for Rp 1.600.000,00 at 16.000 rupiah to the dollar. In
// smallest units that is 10.000 cents and 160.000.000 sen.
func buyDollarsSpec(t *testing.T) ledger.ConversionSpec {
	t.Helper()
	return ledger.ConversionSpec{
		TradingAccount:  aid("trading"),
		SoldPostingID:   pid("p3"),
		BoughtPostingID: pid("p4"),
		Sold:            mustMoney(t, idr, -160_000_000),
		Bought:          mustMoney(t, usd, 10_000),
		Rate:            mustRate(t, usd, idr, 16_000, 1),
	}
}

// The worked example from §5.1: two user-facing postings do not balance on
// their own, and the conversion pair against equity is what makes each
// commodity sum to zero.
func TestConversionPostingsBalanceACrossCommodityTransaction(t *testing.T) {
	t.Parallel()

	tree := simpleBooks(t)

	userPostings := []ledger.Posting{
		mustPosting(t, "p1", "bank", mustMoney(t, idr, -160_000_000)),
		mustPosting(t, "p2", "wallet-usd", mustMoney(t, usd, 10_000)),
	}

	// On their own they are two unbalanced commodities.
	_, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID: tid("txn-fx"), Date: mustDate(t, "2024-03-17"), Postings: userPostings,
	})
	require.ErrorIs(t, err, ledger.ErrUnbalanced)

	conversion, err := tree.ConversionPostings(buyDollarsSpec(t))
	require.NoError(t, err)

	// Both offsets land on the trading account, mirroring the user's lines.
	require.Equal(t, aid("trading"), conversion[0].Account())
	require.Equal(t, aid("trading"), conversion[1].Account())
	requireMoney(t, mustMoney(t, idr, 160_000_000), conversion[0].Amount())
	requireMoney(t, mustMoney(t, usd, -10_000), conversion[1].Amount())

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:       tid("txn-fx"),
		Date:     mustDate(t, "2024-03-17"),
		Postings: append(userPostings, conversion[0], conversion[1]),
	})
	require.NoError(t, err)

	require.Len(t, txn.Postings(), 4)
	require.Equal(t, []ledger.CommodityCode{idr, usd}, txn.Commodities())

	sums, err := ledger.SumPostings(txn.Postings())
	require.NoError(t, err)
	require.True(t, sums.IsZero(), "each commodity must sum to zero, got %s", sums)
	require.True(t, sums.Get(idr).IsZero())
	require.True(t, sums.Get(usd).IsZero())
}

// A posting's rate prices its own commodity, so the pair carries the rate in
// both directions. Inverting a ratio is exact, so neither side is approximate.
func TestConversionPostingsCarryTheRateOnBothSides(t *testing.T) {
	t.Parallel()

	tree := simpleBooks(t)

	conversion, err := tree.ConversionPostings(buyDollarsSpec(t))
	require.NoError(t, err)

	soldSide, boughtSide := conversion[0].Rate(), conversion[1].Rate()

	require.Equal(t, idr, soldSide.Base())
	require.Equal(t, usd, soldSide.Quote())
	require.True(t, soldSide.Equal(mustRate(t, idr, usd, 1, 16_000)))

	require.Equal(t, usd, boughtSide.Base())
	require.Equal(t, idr, boughtSide.Quote())
	require.True(t, boughtSide.Equal(mustRate(t, usd, idr, 16_000, 1)))
}

// The caller may quote the rate either way round; the result is the same pair.
func TestConversionPostingsAcceptEitherQuoteDirection(t *testing.T) {
	t.Parallel()

	tree := simpleBooks(t)

	oneWay, err := tree.ConversionPostings(buyDollarsSpec(t))
	require.NoError(t, err)

	spec := buyDollarsSpec(t)
	spec.Rate = mustRate(t, idr, usd, 1, 16_000)
	otherWay, err := tree.ConversionPostings(spec)
	require.NoError(t, err)

	require.True(t, oneWay[0].Rate().Equal(otherWay[0].Rate()))
	require.True(t, oneWay[1].Rate().Equal(otherWay[1].Rate()))
}

// A conversion moves value between commodities without the household earning
// or spending anything, which is what equity means. Routing it through an
// expense account would invent spending that never happened.
func TestConversionPostingsRequireAnEquityAccount(t *testing.T) {
	t.Parallel()

	tree := simpleBooks(t)

	for _, account := range []string{"bank", "groceries", "salary"} {
		spec := buyDollarsSpec(t)
		spec.TradingAccount = aid(account)

		_, err := tree.ConversionPostings(spec)
		require.ErrorIs(t, err, ledger.ErrNotEquityAccount, "should have refused %q", account)
	}

	spec := buyDollarsSpec(t)
	spec.TradingAccount = aid("nowhere")
	_, err := tree.ConversionPostings(spec)
	require.ErrorIs(t, err, ledger.ErrUnknownAccount)
}

func TestConversionPostingsValidateTheSpec(t *testing.T) {
	t.Parallel()

	tree := simpleBooks(t)

	tests := []struct {
		name    string
		mutate  func(*ledger.ConversionSpec)
		wantErr error
	}{
		{
			"sold is positive",
			func(s *ledger.ConversionSpec) { s.Sold = mustMoney(t, idr, 160_000_000) },
			ledger.ErrInvalidTransaction,
		},
		{
			"bought is negative",
			func(s *ledger.ConversionSpec) { s.Bought = mustMoney(t, usd, -10_000) },
			ledger.ErrInvalidTransaction,
		},
		{
			"both sides are the same commodity",
			func(s *ledger.ConversionSpec) { s.Bought = mustMoney(t, idr, 10_000) },
			ledger.ErrInvalidTransaction,
		},
		{
			"the rate prices an unrelated pair",
			func(s *ledger.ConversionSpec) { s.Rate = mustRate(t, jpy, btc, 1, 1) },
			ledger.ErrInvalidRate,
		},
		{
			"the two postings share an identity",
			func(s *ledger.ConversionSpec) { s.BoughtPostingID = s.SoldPostingID },
			ledger.ErrDuplicateID,
		},
		{
			"a posting has no identity",
			func(s *ledger.ConversionSpec) { s.SoldPostingID = "" },
			ledger.ErrInvalidTransaction,
		},
		{
			"sold was never constructed",
			func(s *ledger.ConversionSpec) { s.Sold = ledger.Money{} },
			ledger.ErrInvalidMoney,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := buyDollarsSpec(t)
			tc.mutate(&spec)

			_, err := tree.ConversionPostings(spec)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestConversionPostingsRejectARestrictedTradingAccount(t *testing.T) {
	t.Parallel()

	tree, err := ledger.NewAccountTree(
		mustAccountSpec(t, ledger.AccountSpec{
			ID: aid("trading"), Kind: ledger.AccountEquity, Name: "Trading", Commodity: idr,
		}),
	)
	require.NoError(t, err)

	_, err = tree.ConversionPostings(buyDollarsSpec(t))
	require.ErrorIs(t, err, ledger.ErrInvalidAccount)
}

// The rate is recorded, not graded. Checking that Sold times Rate equals
// Bought would need a tolerance, and a tolerance is exactly what §5.1 refuses;
// a caller who wants the two to agree derives Bought with Convert and Round.
func TestConversionPostingsDoNotGradeTheRate(t *testing.T) {
	t.Parallel()

	tree := simpleBooks(t)
	reg := testRegistry(t)

	spec := buyDollarsSpec(t)
	spec.Rate = mustRate(t, usd, idr, 15_000, 1) // not what was actually paid

	_, err := tree.ConversionPostings(spec)
	require.NoError(t, err, "the rate is evidence, not an assertion to verify")

	// The supported way to make them agree: derive the other side.
	converted, err := reg.Convert(mustMoney(t, usd, 10_000), mustRate(t, usd, idr, 16_000, 1))
	require.NoError(t, err)
	derived, err := converted.Round()
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 160_000_000), derived)
}
