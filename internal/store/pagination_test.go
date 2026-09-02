// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/ledger"
	"github.com/powfulf/Nusa/internal/store"
)

// Not parallel, for the reason given in idempotencyresponse_test.go: open(t)
// truncates the shared container.
//
// What these guards must cover, decided before any of them was written.
//
//  1. A page holds at most the requested number of transactions, in
//     (date, identity) order.
//  2. Walking the cursor to exhaustion returns every transaction exactly once.
//  3. Next is zero on the last page and set when rows remain — including when
//     the row count is an exact multiple of the page size, which is the case a
//     naive "fewer rows than asked for" test reports wrongly.
//  4. The tie-break is total: transactions sharing one civil date paginate
//     without skipping or repeating one.
//  5. Date bounds are inclusive at both ends.
//  6. Rows written between pages neither skip nor duplicate anything that
//     existed when the walk began.
//  7. A page does not see another transaction's uncommitted write, and does
//     see it once that transaction commits. Arranged by holding the writing
//     transaction open from here, not by starting goroutines and hoping.
//  8. Postings arrive attached to the right transaction, in ordinal order,
//     with rate and memo intact — the batch query is a new path to the domain.
//  9. An unusable page size is refused rather than quietly adjusted.
//  10. A cursor that names no position is refused.
//
// Every one of these runs against PostgreSQL. Pagination's subtlest failures
// come from row visibility under a real isolation level, which no in-memory
// stand-in has to offer (§11).

// writeSpends writes one transaction per entry, dated as given, and returns
// their identities in the order the ledger will read them back.
func writeSpends(t *testing.T, s *store.Store, b books, label string, dates ...string) []ledger.TransactionID {
	t.Helper()
	ctx := context.Background()

	ids := make([]ledger.TransactionID, 0, len(dates))
	for i, date := range dates {
		key := fmt.Sprintf("%s:%02d", label, i)
		txn := spend(t, key, date, b.groceries, b.cash, int64(10_000+i))
		_, err := s.SaveTransaction(ctx, b.write(key), txn)
		require.NoError(t, err, "write %s", key)
		ids = append(ids, txn.ID())
	}
	return ids
}

// walk reads every page and returns the identities in the order they arrived,
// with the number of pages it took.
func walk(t *testing.T, s *store.Store, q store.TransactionQuery) ([]ledger.TransactionID, int) {
	t.Helper()
	ctx := context.Background()

	var seen []ledger.TransactionID
	pages := 0
	for {
		page, err := s.TransactionsPage(ctx, q)
		require.NoError(t, err)
		pages++
		for _, txn := range page.Transactions {
			seen = append(seen, txn.ID())
		}
		if page.Next.IsZero() {
			return seen, pages
		}
		q.After = page.Next
		require.Less(t, pages, 100, "the walk is not terminating")
	}
}

func sortedIdentities(ids []ledger.TransactionID) []ledger.TransactionID {
	out := append([]ledger.TransactionID(nil), ids...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func TestAPageIsBoundedAndOrdered(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)
	writeSpends(t, s, b, "ordered",
		"2026-03-05", "2026-03-01", "2026-03-03", "2026-03-02", "2026-03-04")

	page, err := s.TransactionsPage(ctx, store.TransactionQuery{Limit: 3})
	require.NoError(t, err)
	require.Len(t, page.Transactions, 3)

	for i := 1; i < len(page.Transactions); i++ {
		before, after := page.Transactions[i-1], page.Transactions[i]
		require.LessOrEqual(t, before.Date().Compare(after.Date()), 0,
			"page is out of date order")
	}
	require.Equal(t, "2026-03-01", page.Transactions[0].Date().String())
	require.Equal(t, "2026-03-03", page.Transactions[2].Date().String())
	require.False(t, page.Next.IsZero(), "two rows remain")
}

func TestWalkingTheCursorReturnsEveryTransactionExactlyOnce(t *testing.T) {
	s := open(t)
	b := seedBooks(t, s)
	written := writeSpends(t, s, b, "walk",
		"2026-03-01", "2026-03-02", "2026-03-03", "2026-03-04",
		"2026-03-05", "2026-03-06", "2026-03-07")

	seen, pages := walk(t, s, store.TransactionQuery{Limit: 3})

	require.Equal(t, 3, pages, "7 rows at 3 a page")
	require.Len(t, seen, len(written))
	require.Equal(t, sortedIdentities(written), sortedIdentities(seen))
	require.Len(t, uniqueIdentities(seen), len(seen), "a row came back twice")
}

// The exact-multiple case. Six rows at three a page is two full pages, and an
// implementation that decides "this was the last page" by seeing fewer rows
// than it asked for reports a third page here — or, worse, reports the second
// page as final by luck and stops one row early when the count is odd.
func TestTheLastPageIsRecognisedWhenTheCountDividesExactly(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)
	writeSpends(t, s, b, "exact",
		"2026-03-01", "2026-03-02", "2026-03-03",
		"2026-03-04", "2026-03-05", "2026-03-06")

	first, err := s.TransactionsPage(ctx, store.TransactionQuery{Limit: 3})
	require.NoError(t, err)
	require.Len(t, first.Transactions, 3)
	require.False(t, first.Next.IsZero())

	second, err := s.TransactionsPage(ctx, store.TransactionQuery{Limit: 3, After: first.Next})
	require.NoError(t, err)
	require.Len(t, second.Transactions, 3)
	require.True(t, second.Next.IsZero(),
		"the second page is the last, and saying otherwise costs an empty round trip")

	seen, pages := walk(t, s, store.TransactionQuery{Limit: 3})
	require.Equal(t, 2, pages)
	require.Len(t, seen, 6)
}

// Every transaction on one day. Without the identity in the sort key the order
// within that day is undefined, and a page boundary inside it drops a row and
// repeats another — which is invisible in any test whose rows all have
// distinct dates.
func TestTransactionsSharingADatePaginateWithoutLoss(t *testing.T) {
	s := open(t)
	b := seedBooks(t, s)

	sameDay := make([]string, 9)
	for i := range sameDay {
		sameDay[i] = "2026-03-01"
	}
	written := writeSpends(t, s, b, "sameday", sameDay...)

	for _, limit := range []int{1, 2, 4, 9} {
		seen, _ := walk(t, s, store.TransactionQuery{Limit: limit})
		require.Equal(t, sortedIdentities(written), sortedIdentities(seen),
			"limit %d lost or repeated a row", limit)
		require.Len(t, uniqueIdentities(seen), len(seen), "limit %d repeated a row", limit)
	}
}

func TestDateBoundsAreInclusiveAtBothEnds(t *testing.T) {
	s := open(t)
	b := seedBooks(t, s)
	writeSpends(t, s, b, "bounds",
		"2026-02-28", "2026-03-01", "2026-03-02", "2026-03-03", "2026-03-04")

	seen, _ := walk(t, s, store.TransactionQuery{
		From:  mustDate(t, "2026-03-01"),
		To:    mustDate(t, "2026-03-03"),
		Limit: 2,
	})
	require.Len(t, seen, 3, "both bounds are counted")
}

// The property this whole design exists for, stated as narrowly as it is true:
// everything that existed when the walk began comes back exactly once. It is
// not a snapshot, and the test says which of the new rows it expects and why.
func TestRowsWrittenBetweenPagesNeitherSkipNorDuplicate(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	original := writeSpends(t, s, b, "original",
		"2026-03-02", "2026-03-04", "2026-03-06", "2026-03-08", "2026-03-10", "2026-03-12")

	q := store.TransactionQuery{Limit: 2}
	first, err := s.TransactionsPage(ctx, q)
	require.NoError(t, err)
	require.Len(t, first.Transactions, 2)
	require.False(t, first.Next.IsZero())

	// Written after the walk began: one before the cursor, one after it. With
	// OFFSET the first of these would shift every remaining page by one and
	// hand back a row already seen.
	before := writeSpends(t, s, b, "inserted-before", "2026-03-01")
	after := writeSpends(t, s, b, "inserted-after", "2026-03-07")

	seen := []ledger.TransactionID{}
	for _, txn := range first.Transactions {
		seen = append(seen, txn.ID())
	}
	q.After = first.Next
	rest, _ := walk(t, s, q)
	seen = append(seen, rest...)

	require.Len(t, uniqueIdentities(seen), len(seen), "a row was returned twice")
	for _, id := range original {
		require.Contains(t, seen, id, "a row that existed at the start was skipped")
	}
	require.Contains(t, seen, after[0],
		"a row written after the cursor sorts into the remaining pages and is returned")
	require.NotContains(t, seen, before[0],
		"a row written behind the cursor is not returned, and that is correct: "+
			"the caller asked for what follows a position, not for a snapshot")
}

// Visibility, arranged rather than hoped for. The writing transaction is held
// open by this test, so the reader is provably looking while the write is
// uncommitted — a goroutine racing a writer would pass whether or not the
// isolation level did anything.
func TestAPageDoesNotSeeAnUncommittedWrite(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)
	writeSpends(t, s, b, "committed", "2026-03-01", "2026-03-02")

	tx, err := s.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// A balanced pair, written straight to the tables. The balance trigger is
	// deferred to COMMIT, so these rows exist inside this transaction and
	// nowhere else until then.
	hidden := testID("txn:uncommitted")
	_, err = tx.Exec(ctx,
		`INSERT INTO transactions (id, txn_date, payee) VALUES ($1, '2026-03-03', 'not yet')`, hidden)
	require.NoError(t, err)
	for _, line := range []struct {
		id      string
		ordinal int
		account ledger.AccountID
		amount  int64
	}{
		{testID("posting:uncommitted:debit"), 0, b.groceries, 5_000},
		{testID("posting:uncommitted:credit"), 1, b.cash, -5_000},
	} {
		_, err = tx.Exec(ctx,
			`INSERT INTO postings (id, transaction_id, txn_date, ordinal, account_id, amount, commodity_code)
			 VALUES ($1, $2, '2026-03-03', $3, $4, $5, 'IDR')`,
			line.id, hidden, line.ordinal, string(line.account), line.amount)
		require.NoError(t, err)
	}

	// The reader uses the pool, so it is a different session from the one
	// holding the write.
	during, _ := walk(t, s, store.TransactionQuery{Limit: 10})
	require.Len(t, during, 2, "an uncommitted row was visible to another session")
	require.NotContains(t, during, ledger.TransactionID(hidden))

	require.NoError(t, tx.Commit(ctx))

	after, _ := walk(t, s, store.TransactionQuery{Limit: 10})
	require.Len(t, after, 3, "the committed row is visible to the next walk")
	require.Contains(t, after, ledger.TransactionID(hidden))
}

func TestAPageCarriesItsPostings(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	// Two transactions, so the batch query has to attribute lines to the right
	// header rather than handing every posting to the first one.
	writeSpends(t, s, b, "carries", "2026-03-01", "2026-03-02")

	page, err := s.TransactionsPage(ctx, store.TransactionQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Transactions, 2)

	for _, txn := range page.Transactions {
		postings := txn.Postings()
		require.Len(t, postings, 2, "transaction %s lost a line", txn.ID())

		sum := ledger.Money{}
		for i, p := range postings {
			require.NotEmpty(t, p.ID())
			if i == 0 {
				sum = p.Amount()
				continue
			}
			sum, err = sum.Add(p.Amount())
			require.NoError(t, err)
		}
		require.Zero(t, sum.Sign(), "the page rebuilt an unbalanced transaction")
	}

	// Against the single-transaction reader, which is a different query path
	// only in its cardinality. If the two ever disagree, one of them is
	// dropping a field.
	one, err := s.LoadTransaction(ctx, page.Transactions[0].ID())
	require.NoError(t, err)
	require.Equal(t, page.Transactions[0].String(), one.String())
}

func TestAnUnusablePageSizeIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	seedBooks(t, s)

	_, err := s.TransactionsPage(ctx, store.TransactionQuery{Limit: -1})
	require.ErrorIs(t, err, store.ErrInvalidWrite)

	// Refused rather than reduced to the maximum. A caller that asked for a
	// thousand rows and received two hundred, with nothing saying so, will
	// believe it has the whole set.
	_, err = s.TransactionsPage(ctx, store.TransactionQuery{Limit: store.MaxPageSize + 1})
	require.ErrorIs(t, err, store.ErrInvalidWrite)
	require.Contains(t, err.Error(), "above the maximum")

	_, err = s.TransactionsPage(ctx, store.TransactionQuery{Limit: store.MaxPageSize})
	require.NoError(t, err)
}

func TestACursorThatNamesNoPositionIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	seedBooks(t, s)

	_, err := s.TransactionsPage(ctx, store.TransactionQuery{
		After: store.TransactionCursor{Date: mustDate(t, "2026-03-01"), ID: "not-a-uuid"},
	})
	require.ErrorIs(t, err, store.ErrInvalidWrite)

	// An identity with no date has no position in an ordering whose first
	// column is the date, and guessing one would silently move the caller.
	_, err = s.TransactionsPage(ctx, store.TransactionQuery{
		After: store.TransactionCursor{ID: ledger.TransactionID(testID("txn:whatever"))},
	})
	require.ErrorIs(t, err, store.ErrInvalidWrite)
	require.Contains(t, err.Error(), "no date")
}

func uniqueIdentities(ids []ledger.TransactionID) []ledger.TransactionID {
	seen := map[ledger.TransactionID]bool{}
	out := make([]ledger.TransactionID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
