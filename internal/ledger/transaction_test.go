// SPDX-License-Identifier: MIT

package ledger_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// groceries is the simplest real transaction: money leaves the bank, spending
// appears. Rp 150.000,00 is 15.000.000 sen.
func groceriesSpec(t *testing.T) ledger.TransactionSpec {
	t.Helper()
	return ledger.TransactionSpec{
		ID:    tid("txn-groceries"),
		Date:  mustDate(t, "2024-03-17"),
		Payee: "Pasar Modern",
		Postings: []ledger.Posting{
			mustPosting(t, "p1", "bank", mustMoney(t, idr, -15_000_000)),
			mustPosting(t, "p2", "groceries", mustMoney(t, idr, 15_000_000)),
		},
	}
}

func TestNewTransactionAcceptsABalancedPair(t *testing.T) {
	t.Parallel()

	txn, err := ledger.NewTransaction(groceriesSpec(t))
	require.NoError(t, err)

	require.Equal(t, tid("txn-groceries"), txn.ID())
	require.Equal(t, "2024-03-17", txn.Date().String())
	require.Equal(t, "Pasar Modern", txn.Payee())
	require.Len(t, txn.Postings(), 2)
	require.Equal(t, []ledger.CommodityCode{idr}, txn.Commodities())

	sums, err := ledger.SumPostings(txn.Postings())
	require.NoError(t, err)
	require.True(t, sums.IsZero())
}

// §5.1, and the reason it is stated as an absolute: one sen out is still out.
// There is no tolerance to fall inside.
func TestNewTransactionRejectsAnythingUnbalanced(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		short int64
	}{
		{"short by a single sen", 1},
		{"short by a rupiah", 100},
		{"short by a large amount", 1_000_000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := groceriesSpec(t)
			spec.Postings = []ledger.Posting{
				mustPosting(t, "p1", "bank", mustMoney(t, idr, -15_000_000)),
				mustPosting(t, "p2", "groceries", mustMoney(t, idr, 15_000_000-tc.short)),
			}

			_, err := ledger.NewTransaction(spec)
			require.ErrorIs(t, err, ledger.ErrUnbalanced)
		})
	}
}

// Each commodity balances on its own. Two halves that would net to zero if you
// pretended a dollar were a rupiah are still two unbalanced commodities, and
// nothing is synthesised to close the gap.
func TestNewTransactionRequiresEachCommodityToBalanceSeparately(t *testing.T) {
	t.Parallel()

	spec := groceriesSpec(t)
	spec.Postings = []ledger.Posting{
		mustPosting(t, "p1", "bank", mustMoney(t, idr, -1_600_000)),
		mustPosting(t, "p2", "wallet-usd", mustMoney(t, usd, 1_600_000)),
	}

	_, err := ledger.NewTransaction(spec)
	require.ErrorIs(t, err, ledger.ErrUnbalanced)
	require.Contains(t, err.Error(), "IDR")
	require.Contains(t, err.Error(), "USD")
}

func TestNewTransactionValidatesItsShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*ledger.TransactionSpec)
		wantErr error
	}{
		{
			"no id",
			func(s *ledger.TransactionSpec) { s.ID = "" },
			ledger.ErrInvalidTransaction,
		},
		{
			"no date",
			func(s *ledger.TransactionSpec) { s.Date = ledger.Date{} },
			ledger.ErrInvalidTransaction,
		},
		{
			"a single posting",
			func(s *ledger.TransactionSpec) { s.Postings = s.Postings[:1] },
			ledger.ErrInvalidTransaction,
		},
		{
			"no postings",
			func(s *ledger.TransactionSpec) { s.Postings = nil },
			ledger.ErrInvalidTransaction,
		},
		{
			"an unconstructed posting",
			func(s *ledger.TransactionSpec) { s.Postings = []ledger.Posting{{}, {}} },
			ledger.ErrInvalidTransaction,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := groceriesSpec(t)
			tc.mutate(&spec)

			_, err := ledger.NewTransaction(spec)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// A lot or an audit entry points at a posting by identity, so two postings
// sharing one identity would make that pointer ambiguous.
func TestNewTransactionRejectsDuplicatePostingIdentities(t *testing.T) {
	t.Parallel()

	spec := groceriesSpec(t)
	spec.Postings = []ledger.Posting{
		mustPosting(t, "p1", "bank", mustMoney(t, idr, -15_000_000)),
		mustPosting(t, "p1", "groceries", mustMoney(t, idr, 15_000_000)),
	}

	_, err := ledger.NewTransaction(spec)
	require.ErrorIs(t, err, ledger.ErrDuplicateID)
}

func TestNewPostingValidatesItsInput(t *testing.T) {
	t.Parallel()

	_, err := ledger.NewPosting(ledger.PostingSpec{Account: aid("bank"), Amount: mustMoney(t, idr, 1)})
	require.ErrorIs(t, err, ledger.ErrInvalidTransaction)

	_, err = ledger.NewPosting(ledger.PostingSpec{ID: pid("p1"), Amount: mustMoney(t, idr, 1)})
	require.ErrorIs(t, err, ledger.ErrInvalidTransaction)

	_, err = ledger.NewPosting(ledger.PostingSpec{ID: pid("p1"), Account: aid("bank")})
	require.ErrorIs(t, err, ledger.ErrInvalidMoney)
}

// A posting's rate prices the posting's own commodity. A rupiah line carrying
// a rate that prices dollars would be read backwards by any report.
func TestNewPostingRequiresItsRateToPriceItsOwnCommodity(t *testing.T) {
	t.Parallel()

	_, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      pid("p1"),
		Account: aid("bank"),
		Amount:  mustMoney(t, idr, -1_600_000),
		Rate:    mustRate(t, usd, idr, 16_000, 1),
	})
	require.ErrorIs(t, err, ledger.ErrInvalidRate)

	ok, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      pid("p1"),
		Account: aid("bank"),
		Amount:  mustMoney(t, idr, -1_600_000),
		Rate:    mustRate(t, idr, usd, 1, 16_000),
	})
	require.NoError(t, err)
	require.False(t, ok.Rate().IsZero())
}

// §5.3. The postings slice a transaction hands out is a copy, so a caller
// cannot rewrite history by writing into it.
func TestTransactionPostingsCannotBeAlteredFromOutside(t *testing.T) {
	t.Parallel()

	txn, err := ledger.NewTransaction(groceriesSpec(t))
	require.NoError(t, err)

	leaked := txn.Postings()
	leaked[0] = mustPosting(t, "tampered", "groceries", mustMoney(t, idr, 999))

	require.Equal(t, pid("p1"), txn.Postings()[0].ID())
	requireMoney(t, mustMoney(t, idr, -15_000_000), txn.Postings()[0].Amount())
}

// The spec's slice belongs to the caller, and a caller that keeps writing to
// it after constructing a transaction must not be able to reach inside.
func TestTransactionCopiesTheSpecSlice(t *testing.T) {
	t.Parallel()

	spec := groceriesSpec(t)
	txn, err := ledger.NewTransaction(spec)
	require.NoError(t, err)

	spec.Postings[0] = mustPosting(t, "tampered", "groceries", mustMoney(t, idr, 999))

	require.Equal(t, pid("p1"), txn.Postings()[0].ID())
}

// §D3 of the design decisions: the instant and the zone are recorded and never
// consulted. The date is what a calculation reads.
func TestOccurredAtIsCarriedButNeverReplacesTheDate(t *testing.T) {
	t.Parallel()

	// Half past eight in the evening UTC is already the eighteenth in Jakarta,
	// but the user wrote down the seventeenth and that is what the ledger uses.
	instant := time.Date(2024, time.March, 17, 20, 30, 0, 0, time.UTC)

	spec := groceriesSpec(t)
	spec.OccurredAt = instant
	spec.Timezone = "Asia/Jakarta"

	txn, err := ledger.NewTransaction(spec)
	require.NoError(t, err)

	require.Equal(t, "2024-03-17", txn.Date().String())
	require.True(t, instant.Equal(txn.OccurredAt()))
	require.Equal(t, "Asia/Jakarta", txn.Timezone())
}

func TestTransactionPostingsFor(t *testing.T) {
	t.Parallel()

	txn, err := ledger.NewTransaction(groceriesSpec(t))
	require.NoError(t, err)

	require.Len(t, txn.PostingsFor(aid("bank")), 1)
	require.Empty(t, txn.PostingsFor(aid("nowhere")))
}

func TestZeroTransactionAndPosting(t *testing.T) {
	t.Parallel()

	var txn ledger.Transaction
	require.False(t, txn.IsValid())
	require.Equal(t, "<invalid transaction>", txn.String())

	var posting ledger.Posting
	require.False(t, posting.IsValid())
	require.Equal(t, "<invalid posting>", posting.String())

	_, err := ledger.SumPostings([]ledger.Posting{{}})
	require.ErrorIs(t, err, ledger.ErrInvalidTransaction)
}

func TestBalancesReadEmptyCommoditiesAsZero(t *testing.T) {
	t.Parallel()

	sums, err := ledger.SumPostings([]ledger.Posting{
		mustPosting(t, "p1", "bank", mustMoney(t, idr, -100)),
		mustPosting(t, "p2", "groceries", mustMoney(t, idr, 100)),
	})
	require.NoError(t, err)

	// "No dollars" and "zero dollars" are the same fact.
	neverSeen := sums.Get(usd)
	require.True(t, neverSeen.IsZero())
	require.Equal(t, usd, neverSeen.Commodity())

	require.Equal(t, []ledger.CommodityCode{idr}, sums.Codes())
	require.Empty(t, sums.Nonzero())
	require.Equal(t, 1, sums.Len())
	require.Equal(t, "IDR 0", sums.String())
}
