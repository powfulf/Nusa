// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/ledger"
	"github.com/powfulf/Nusa/internal/store"
)

// The repository refuses an unbalanced transaction because the domain does,
// and the domain refuses it because NewTransaction will not build one. These
// tests are about what happens when someone does not come through that door.
//
// The importer, the rule engine and psql will all write to these tables one
// day. An invariant guarded in one layer is one the next layer breaks.

func TestTheDatabaseRefusesAnUnbalancedTransactionWrittenDirectly(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	tx, err := s.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`INSERT INTO transactions (id, txn_date, payee) VALUES ($1, $2, $3)`,
		testID("txn:direct"), "2026-03-01", "written straight to the table")
	require.NoError(t, err)

	// 150000 in, 50000 out. Nothing in the domain was consulted.
	for _, line := range []struct {
		id      string
		ordinal int
		account ledger.AccountID
		amount  int64
	}{
		{testID("posting:direct:debit"), 0, b.groceries, 150_000},
		{testID("posting:direct:credit"), 1, b.cash, -50_000},
	} {
		_, err = tx.Exec(ctx,
			`INSERT INTO postings (id, transaction_id, txn_date, ordinal, account_id, amount, commodity_code)
			 VALUES ($1, $2, $3, $4, $5, $6, 'IDR')`,
			line.id, testID("txn:direct"), "2026-03-01", line.ordinal, string(line.account), line.amount)
		require.NoError(t, err, "the rows go in; the constraint is deferred to COMMIT")
	}

	err = tx.Commit(ctx)
	require.Error(t, err, "COMMIT is where the transaction is judged")
	require.Contains(t, err.Error(), "is short by 100000 IDR",
		"the error says what is missing, not merely that something is")

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, count, "the refused commit left nothing behind")
}

func TestTheDatabaseRefusesATransactionWithASingleLine(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	tx, err := s.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`INSERT INTO transactions (id, txn_date) VALUES ($1, $2)`,
		testID("txn:lonely"), "2026-03-01")
	require.NoError(t, err)
	_, err = tx.Exec(ctx,
		`INSERT INTO postings (id, transaction_id, txn_date, ordinal, account_id, amount, commodity_code)
		 VALUES ($1, $2, $3, 0, $4, 0, 'IDR')`,
		testID("posting:lonely"), testID("txn:lonely"), "2026-03-01", string(b.cash))
	require.NoError(t, err)

	err = tx.Commit(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "needs at least 2",
		"a single line that happens to be zero still is not a transaction")
}

func TestTheDatabaseRefusesAPostingDatedApartFromItsTransaction(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	tx, err := s.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`INSERT INTO transactions (id, txn_date) VALUES ($1, $2)`,
		testID("txn:drift"), "2026-03-01")
	require.NoError(t, err)

	// The denormalised date is what makes a balance one index-only scan. This
	// is the constraint that stops it becoming a lie.
	_, err = tx.Exec(ctx,
		`INSERT INTO postings (id, transaction_id, txn_date, ordinal, account_id, amount, commodity_code)
		 VALUES ($1, $2, $3, 0, $4, 1, 'IDR')`,
		testID("posting:drift"), testID("txn:drift"), "2026-03-09", string(b.cash))
	require.Error(t, err, "the row cannot even be inserted; this one is not deferred")
	require.Contains(t, err.Error(), "postings_carry_their_transactions_date")
}

func TestTheDatabaseRefusesToMoveADateOutFromUnderItsPostings(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	txn := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_000)
	_, err := s.SaveTransaction(ctx, b.write("dated"), txn)
	require.NoError(t, err)

	_, err = s.Pool().Exec(ctx,
		`UPDATE transactions SET txn_date = '2026-04-01' WHERE id = $1`, string(txn.ID()))
	require.Error(t, err, "a transaction date is not editable while postings carry a copy of it")
	require.Contains(t, err.Error(), "postings_carry_their_transactions_date")
}

func TestTheDatabaseRefusesAnIdentityThatIsNotAUUIDv7(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	seedBooks(t, s)

	_, err := s.Pool().Exec(ctx,
		`INSERT INTO transactions (id, txn_date) VALUES ('01920000-0000-4000-8000-0000000000cf', '2026-03-01')`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transactions_id_is_uuid_v7",
		"the domain demands UUIDv7 and so does the column")
}

// Bulk loading is the one place the deferred constraint has an operational
// consequence: headers and lines must arrive inside one database transaction,
// or the headers are judged alone and correctly refused. The importer in a
// later milestone has to know this, so it is pinned down here.
func TestHeadersWrittenWithoutTheirLinesAreRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	seedBooks(t, s)

	_, err := s.Pool().Exec(ctx,
		`INSERT INTO transactions (id, txn_date) VALUES ($1, '2026-03-01')`,
		testID("txn:headerless"))
	require.Error(t, err, "a header committed on its own has no lines and cannot balance")
	require.Contains(t, err.Error(), "has 0 posting(s)")
}

// The whole-book total is the check on the check: every transaction sums to
// zero on its own, so any number of them still do.
func TestTheBookAlwaysNetsToZero(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	for i, label := range []string{"one", "two", "three"} {
		txn := spend(t, label, "2026-03-0"+string(rune('1'+i)), b.groceries, b.cash, int64(1000*(i+1)))
		_, err := s.SaveTransaction(ctx, b.write("key-"+label), txn)
		require.NoError(t, err)
	}

	totals, err := s.BookTotals(ctx)
	require.NoError(t, err)
	require.Len(t, totals, 1)
	require.True(t, totals[0].IsZero(), "the book nets to zero, got %s", totals[0])
}

func TestUnbalancedTransactionsCannotBeBuiltAtAll(t *testing.T) {
	// Not an integration test in spirit, but it belongs beside the others: the
	// database guard exists for writers that skip this one, and it is worth
	// showing here that the ordinary path never reaches it.
	debit, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(testID("posting:x")),
		Account: ledger.AccountID(testID("account:cash")),
		Amount:  mustMoney(t, "IDR", 150_000),
	})
	require.NoError(t, err)
	credit, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(testID("posting:y")),
		Account: ledger.AccountID(testID("account:groceries")),
		Amount:  mustMoney(t, "IDR", -50_000),
	})
	require.NoError(t, err)

	_, err = ledger.NewTransaction(ledger.TransactionSpec{
		ID:       ledger.TransactionID(testID("txn:never")),
		Date:     mustDate(t, "2026-03-01"),
		Postings: []ledger.Posting{debit, credit},
	})
	require.ErrorIs(t, err, ledger.ErrUnbalanced)

	var _ = store.ErrUnbalanced // the store sentinel exists for the other door
}
