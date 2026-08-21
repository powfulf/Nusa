// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
	"github.com/GaffaQ/Nusa/internal/store"
)

// books is the small fixture most tests write against: one actor and three
// accounts, two of them in IDR and one in USD so a cross-commodity case has
// somewhere to land.
type books struct {
	actor     string
	cash      ledger.AccountID
	groceries ledger.AccountID
	dollars   ledger.AccountID
	trading   ledger.AccountID
}

func seedBooks(t *testing.T, s *store.Store) books {
	t.Helper()
	ctx := context.Background()

	b := books{
		actor:     testID("user:owner"),
		cash:      ledger.AccountID(testID("account:cash")),
		groceries: ledger.AccountID(testID("account:groceries")),
		dollars:   ledger.AccountID(testID("account:dollars")),
		trading:   ledger.AccountID(testID("account:trading")),
	}

	require.NoError(t, s.SaveUser(ctx, b.actor))

	for _, spec := range []ledger.AccountSpec{
		{ID: b.cash, Kind: ledger.AccountAsset, Name: "Cash", Commodity: "IDR"},
		{ID: b.groceries, Kind: ledger.AccountExpense, Name: "Groceries", Commodity: "IDR"},
		{ID: b.dollars, Kind: ledger.AccountAsset, Name: "Dollars", Commodity: "USD"},
		{ID: b.trading, Kind: ledger.AccountEquity, Name: "Trading", Commodity: ""},
	} {
		account, err := ledger.NewAccount(spec)
		require.NoError(t, err)
		require.NoError(t, s.SaveAccount(ctx, account), "save account %s", spec.Name)
	}
	return b
}

// write returns a Write for a given key, with identities derived from the key
// so a replay of the same logical request carries the same audit identity too.
func (b books) write(key string) store.Write {
	return store.Write{
		ActorID:        b.actor,
		Origin:         store.OriginHuman,
		IdempotencyKey: key,
		AuditID:        testID("audit:" + key),
		OccurredAt:     time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
	}
}

func mustDate(t *testing.T, s string) ledger.Date {
	t.Helper()
	d, err := ledger.ParseDate(s)
	require.NoError(t, err)
	return d
}

func mustMoney(t *testing.T, code ledger.CommodityCode, minorUnits int64) ledger.Money {
	t.Helper()
	m, err := ledger.MoneyFromInt(code, minorUnits)
	require.NoError(t, err)
	return m
}

// spend builds a balanced two-line transaction: value into one account, out of
// another, in one commodity.
func spend(t *testing.T, label, date string, into, outOf ledger.AccountID, minorUnits int64) ledger.Transaction {
	t.Helper()

	debit, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(testID("posting:" + label + ":debit")),
		Account: into,
		Amount:  mustMoney(t, "IDR", minorUnits),
		Memo:    "the debit line",
	})
	require.NoError(t, err)

	credit, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(testID("posting:" + label + ":credit")),
		Account: outOf,
		Amount:  mustMoney(t, "IDR", -minorUnits),
	})
	require.NoError(t, err)

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:       ledger.TransactionID(testID("txn:" + label)),
		Date:     mustDate(t, date),
		Payee:    "Warung " + label,
		Memo:     "weekly shop",
		Postings: []ledger.Posting{debit, credit},
	})
	require.NoError(t, err)
	return txn
}
