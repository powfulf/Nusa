// SPDX-License-Identifier: MIT

package ledger_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nusa-app/nusa/internal/ledger"
)

// spend builds a balanced two-line transaction: money out of an account, into
// a category.
func spend(t *testing.T, label, on, from, to string, minorUnits int64) ledger.Transaction {
	t.Helper()
	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:   tid(label),
		Date: mustDate(t, on),
		Postings: []ledger.Posting{
			mustPosting(t, label+"-a", from, mustMoney(t, idr, -minorUnits)),
			mustPosting(t, label+"-b", to, mustMoney(t, idr, minorUnits)),
		},
	})
	require.NoError(t, err)
	return txn
}

func marchJournal(t *testing.T) *ledger.Journal {
	t.Helper()
	j, err := ledger.NewJournal(simpleBooks(t),
		spend(t, "txn-2", "2024-03-10", "bank", "groceries", 15_000_000),
		spend(t, "txn-1", "2024-03-01", "salary", "bank", 900_000_000),
		spend(t, "txn-3", "2024-03-20", "bank", "groceries", 5_000_000),
	)
	require.NoError(t, err)
	return j
}

// §5.2: an account's balance is the sum of its postings, and nothing else.
func TestJournalBalanceIsTheSumOfPostings(t *testing.T) {
	t.Parallel()

	j := marchJournal(t)

	balance, err := j.Balance(aid("bank"))
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 880_000_000), balance.Get(idr))

	// Recomputed the long way round, through the public accessors only.
	manual, err := ledger.ZeroMoney(idr)
	require.NoError(t, err)
	for _, p := range j.Postings(aid("bank")) {
		manual, err = manual.Add(p.Amount())
		require.NoError(t, err)
	}
	requireMoney(t, manual, balance.Get(idr))
}

// Income is credit-normal, so it reads negative raw and positive once
// NormalSign is applied. The ledger stores the first; a screen shows the
// second.
func TestJournalBalanceSignsFollowTheAccountKind(t *testing.T) {
	t.Parallel()

	j := marchJournal(t)

	salary, err := j.Balance(aid("salary"))
	require.NoError(t, err)
	require.Equal(t, -1, salary.Get(idr).Sign())
	require.Equal(t, -1, ledger.AccountIncome.NormalSign())

	groceries, err := j.Balance(aid("groceries"))
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 20_000_000), groceries.Get(idr))
}

func TestJournalBalanceAsOf(t *testing.T) {
	t.Parallel()

	j := marchJournal(t)

	tests := []struct {
		on   string
		want int64
	}{
		{"2024-02-29", 0},
		{"2024-03-01", 900_000_000},
		{"2024-03-09", 900_000_000},
		{"2024-03-10", 885_000_000},
		{"2024-03-20", 880_000_000},
		{"2024-12-31", 880_000_000},
	}

	for _, tc := range tests {
		t.Run(tc.on, func(t *testing.T) {
			t.Parallel()

			balance, err := j.BalanceAsOf(aid("bank"), mustDate(t, tc.on))
			require.NoError(t, err)
			requireMoney(t, mustMoney(t, idr, tc.want), balance.Get(idr))
		})
	}
}

// §5.4's sibling: a historical answer must not move because something was
// recorded later. A balance as of March is the same before and after April
// exists.
func TestBalanceAsOfIgnoresLaterTransactions(t *testing.T) {
	t.Parallel()

	j := marchJournal(t)
	asOfMarch, err := j.BalanceAsOf(aid("bank"), mustDate(t, "2024-03-31"))
	require.NoError(t, err)

	extended, err := j.Add(spend(t, "txn-4", "2024-04-05", "bank", "groceries", 7_000_000))
	require.NoError(t, err)

	again, err := extended.BalanceAsOf(aid("bank"), mustDate(t, "2024-03-31"))
	require.NoError(t, err)
	requireMoney(t, asOfMarch.Get(idr), again.Get(idr))

	// The all-time balance did move, which is the control for the test above.
	allTime, err := extended.Balance(aid("bank"))
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 873_000_000), allTime.Get(idr))
}

// The instant and the zone are recorded and never consulted. Two transactions
// whose timestamps fall on different days in different zones still land on the
// civil date the user wrote down.
func TestBalancesIgnoreOccurredAtAndTimezone(t *testing.T) {
	t.Parallel()

	// Late evening UTC on the seventeenth — already the eighteenth in Jakarta.
	instant := time.Date(2024, time.March, 17, 20, 30, 0, 0, time.UTC)

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:         tid("txn-late"),
		Date:       mustDate(t, "2024-03-17"),
		OccurredAt: instant,
		Timezone:   "Asia/Jakarta",
		Postings: []ledger.Posting{
			mustPosting(t, "late-a", "bank", mustMoney(t, idr, -1_000)),
			mustPosting(t, "late-b", "groceries", mustMoney(t, idr, 1_000)),
		},
	})
	require.NoError(t, err)

	j, err := ledger.NewJournal(simpleBooks(t), txn)
	require.NoError(t, err)

	onTheSeventeenth, err := j.BalanceAsOf(aid("groceries"), mustDate(t, "2024-03-17"))
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 1_000), onTheSeventeenth.Get(idr),
		"the civil date decides, not the instant")
}

func TestJournalBalanceBetween(t *testing.T) {
	t.Parallel()

	j := marchJournal(t)

	within, err := j.BalanceBetween(aid("groceries"), mustDate(t, "2024-03-01"), mustDate(t, "2024-03-15"))
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 15_000_000), within.Get(idr))

	whole, err := j.BalanceBetween(aid("groceries"), ledger.Date{}, ledger.Date{})
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 20_000_000), whole.Get(idr))

	_, err = j.BalanceBetween(aid("groceries"), mustDate(t, "2024-03-15"), mustDate(t, "2024-03-01"))
	require.ErrorIs(t, err, ledger.ErrInvalidDate)
}

func TestJournalSubtreeBalance(t *testing.T) {
	t.Parallel()

	j := marchJournal(t)

	// Nothing posts to "assets" itself; the balance comes entirely from below.
	own, err := j.Balance(aid("assets"))
	require.NoError(t, err)
	require.True(t, own.Get(idr).IsZero())

	rolled, err := j.SubtreeBalanceAsOf(aid("assets"), ledger.Date{})
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 880_000_000), rolled.Get(idr))

	asOf, err := j.SubtreeBalanceAsOf(aid("assets"), mustDate(t, "2024-03-01"))
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 900_000_000), asOf.Get(idr))
}

// Every transaction sums to zero on its own, so any number of them still do.
func TestJournalTotalsAreAlwaysZero(t *testing.T) {
	t.Parallel()

	totals, err := marchJournal(t).TotalsByCommodity()
	require.NoError(t, err)
	require.True(t, totals.IsZero(), "the whole book must net to zero, got %s", totals)
}

// Two journals built from the same transactions in different orders must
// answer every question identically.
func TestJournalOrdersByDateThenIdentity(t *testing.T) {
	t.Parallel()

	j := marchJournal(t)

	// These three fall on different days, so the date alone decides.
	var ids []ledger.TransactionID
	for _, txn := range j.Transactions() {
		ids = append(ids, txn.ID())
	}
	require.Equal(t, []ledger.TransactionID{tid("txn-1"), tid("txn-2"), tid("txn-3")}, ids)

	// These two fall on the same day, so identity breaks the tie. The expected
	// order is derived rather than written down: identities are UUIDs and do
	// not sort the way their labels read.
	sameDay, err := ledger.NewJournal(simpleBooks(t),
		spend(t, "txn-b", "2024-03-01", "bank", "groceries", 1_000),
		spend(t, "txn-a", "2024-03-01", "bank", "groceries", 1_000),
	)
	require.NoError(t, err)

	first, second := tid("txn-a"), tid("txn-b")
	if second < first {
		first, second = second, first
	}
	require.Equal(t, first, sameDay.Transactions()[0].ID())
	require.Equal(t, second, sameDay.Transactions()[1].ID())
}

func TestNewJournalRejectsPostingsToUnknownAccounts(t *testing.T) {
	t.Parallel()

	_, err := ledger.NewJournal(simpleBooks(t), spend(t, "txn-1", "2024-03-01", "bank", "nowhere", 1_000))
	require.ErrorIs(t, err, ledger.ErrUnknownAccount)
}

// A rupiah-only account must not quietly accumulate dollars.
func TestNewJournalRejectsACommodityAnAccountDoesNotHold(t *testing.T) {
	t.Parallel()

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:   tid("txn-wrong-currency"),
		Date: mustDate(t, "2024-03-01"),
		Postings: []ledger.Posting{
			mustPosting(t, "a", "bank", mustMoney(t, usd, -100)),
			mustPosting(t, "b", "groceries", mustMoney(t, usd, 100)),
		},
	})
	require.NoError(t, err)

	_, err = ledger.NewJournal(simpleBooks(t), txn)
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)
}

func TestNewJournalRejectsDuplicateIdentities(t *testing.T) {
	t.Parallel()

	_, err := ledger.NewJournal(simpleBooks(t),
		spend(t, "txn-1", "2024-03-01", "bank", "groceries", 1_000),
		spend(t, "txn-1", "2024-03-02", "bank", "groceries", 2_000),
	)
	require.ErrorIs(t, err, ledger.ErrDuplicateID)

	// Posting identities are unique across the whole journal, not just within
	// a transaction, because a lot points at one and must find exactly one.
	first, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:   tid("txn-1"),
		Date: mustDate(t, "2024-03-01"),
		Postings: []ledger.Posting{
			mustPosting(t, "shared", "bank", mustMoney(t, idr, -1_000)),
			mustPosting(t, "other", "groceries", mustMoney(t, idr, 1_000)),
		},
	})
	require.NoError(t, err)
	second, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:   tid("txn-2"),
		Date: mustDate(t, "2024-03-02"),
		Postings: []ledger.Posting{
			mustPosting(t, "shared", "bank", mustMoney(t, idr, -2_000)),
			mustPosting(t, "another", "groceries", mustMoney(t, idr, 2_000)),
		},
	})
	require.NoError(t, err)

	_, err = ledger.NewJournal(simpleBooks(t), first, second)
	require.ErrorIs(t, err, ledger.ErrDuplicateID)
}

func TestNewJournalNeedsAnAccountTree(t *testing.T) {
	t.Parallel()

	_, err := ledger.NewJournal(nil)
	require.ErrorIs(t, err, ledger.ErrInvalidAccount)

	_, err = ledger.NewJournal(simpleBooks(t), ledger.Transaction{})
	require.ErrorIs(t, err, ledger.ErrInvalidTransaction)
}

// Add returns a new journal and leaves the original alone.
func TestJournalAddDoesNotMutate(t *testing.T) {
	t.Parallel()

	j := marchJournal(t)
	require.Equal(t, 3, j.Len())

	extended, err := j.Add(spend(t, "txn-4", "2024-04-05", "bank", "groceries", 7_000_000))
	require.NoError(t, err)

	require.Equal(t, 3, j.Len())
	require.Equal(t, 4, extended.Len())

	_, found := j.Transaction(tid("txn-4"))
	require.False(t, found)
	_, found = extended.Transaction(tid("txn-4"))
	require.True(t, found)
}

func TestJournalQueriesRejectUnknownAccounts(t *testing.T) {
	t.Parallel()

	j := marchJournal(t)

	_, err := j.Balance(aid("nowhere"))
	require.ErrorIs(t, err, ledger.ErrUnknownAccount)

	_, err = j.BalanceBetween(aid("nowhere"), ledger.Date{}, ledger.Date{})
	require.ErrorIs(t, err, ledger.ErrUnknownAccount)

	_, err = j.SubtreeBalanceAsOf(aid("nowhere"), ledger.Date{})
	require.ErrorIs(t, err, ledger.ErrUnknownAccount)

	require.Empty(t, j.Postings(aid("nowhere")))
	require.Equal(t, simpleBooks(t).Len(), j.Accounts().Len())
}

// A journal holding several commodities reports them side by side rather than
// adding them, because adding them would need a rate and a date and that is a
// reporting decision made above this package.
func TestJournalKeepsCommoditiesApart(t *testing.T) {
	t.Parallel()

	tree := simpleBooks(t)

	conversion, err := tree.ConversionPostings(buyDollarsSpec(t))
	require.NoError(t, err)

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:   tid("txn-fx"),
		Date: mustDate(t, "2024-03-17"),
		Postings: []ledger.Posting{
			mustPosting(t, "p1", "bank", mustMoney(t, idr, -160_000_000)),
			mustPosting(t, "p2", "wallet-usd", mustMoney(t, usd, 10_000)),
			conversion[0], conversion[1],
		},
	})
	require.NoError(t, err)

	j, err := ledger.NewJournal(tree, txn)
	require.NoError(t, err)

	assets, err := j.SubtreeBalanceAsOf(aid("assets"), ledger.Date{})
	require.NoError(t, err)
	require.Equal(t, []ledger.CommodityCode{idr, usd}, assets.Codes())
	requireMoney(t, mustMoney(t, idr, -160_000_000), assets.Get(idr))
	requireMoney(t, mustMoney(t, usd, 10_000), assets.Get(usd))

	trading, err := j.Balance(aid("trading"))
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 160_000_000), trading.Get(idr))
	requireMoney(t, mustMoney(t, usd, -10_000), trading.Get(usd))

	totals, err := j.TotalsByCommodity()
	require.NoError(t, err)
	require.True(t, totals.IsZero())
}
