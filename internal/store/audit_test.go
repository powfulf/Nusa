// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
	"github.com/GaffaQ/Nusa/internal/store"
)

func TestEveryWriteRecordsWhoDidItAndWhatCausedIt(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	txn := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_000)
	w := b.write("audited")
	w.Origin = store.OriginImport

	_, err := s.SaveTransaction(ctx, w, txn)
	require.NoError(t, err)

	entries, err := s.AuditEntriesFor(ctx, "transaction", string(txn.ID()))
	require.NoError(t, err)
	require.Len(t, entries, 1)

	entry := entries[0]
	require.Equal(t, w.AuditID, entry.ID)
	require.Equal(t, b.actor, entry.ActorID)
	require.Equal(t, store.OriginImport, entry.Origin, "the origin survives exactly as given")
	require.Equal(t, "create", entry.Action)
	require.Equal(t, "transaction", entry.EntityKind)
	require.Equal(t, string(txn.ID()), entry.EntityID)
	require.Equal(t, w.OccurredAt.UTC(), entry.OccurredAt.UTC())

	// The diff carries the amounts that were written, through Money's own
	// exact encoding — so the log holds the figure, not a rendering of it.
	var diff struct {
		ID       string `json:"id"`
		Date     string `json:"date"`
		Postings []struct {
			ID     string `json:"id"`
			Amount struct {
				Amount    string `json:"amount"`
				Commodity string `json:"commodity"`
			} `json:"amount"`
		} `json:"postings"`
	}
	require.NoError(t, json.Unmarshal(entry.Diff, &diff))
	require.Equal(t, string(txn.ID()), diff.ID)
	require.Equal(t, "2026-03-01", diff.Date)
	require.Len(t, diff.Postings, 2)
	require.Equal(t, "150000", diff.Postings[0].Amount.Amount,
		"the amount is a string, at full precision")
	require.Equal(t, "IDR", diff.Postings[0].Amount.Commodity)
	require.Equal(t, "-150000", diff.Postings[1].Amount.Amount)
}

func TestEachOriginIsStoredAsItself(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	origins := []store.Origin{
		store.OriginHuman, store.OriginRule, store.OriginImport, store.OriginAI,
	}
	for i, origin := range origins {
		label := string(origin)
		txn := spend(t, label, "2026-03-0"+string(rune('1'+i)), b.groceries, b.cash, int64(1000*(i+1)))

		w := b.write("origin-" + label)
		w.Origin = origin
		_, err := s.SaveTransaction(ctx, w, txn)
		require.NoError(t, err, "origin %s", origin)

		entries, err := s.AuditEntriesFor(ctx, "transaction", string(txn.ID()))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, origin, entries[0].Origin)
	}
}

// A round trip through the database returns the transaction that was written,
// down to the order of its lines. The exhaustive version of this is the
// property test; this one is the shape of it, readable in one screen.
func TestATransactionComesBackAsItWentIn(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	txn := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_000)
	_, err := s.SaveTransaction(ctx, b.write("round-trip"), txn)
	require.NoError(t, err)

	back, err := s.LoadTransaction(ctx, txn.ID())
	require.NoError(t, err)

	require.Equal(t, txn.ID(), back.ID())
	require.Equal(t, txn.Date(), back.Date())
	require.Equal(t, txn.Payee(), back.Payee())
	require.Equal(t, txn.Memo(), back.Memo())

	original, restored := txn.Postings(), back.Postings()
	require.Len(t, restored, len(original))
	for i := range original {
		require.Equal(t, original[i].ID(), restored[i].ID(),
			"line %d came back in the position it was written", i)
		require.Equal(t, original[i].Account(), restored[i].Account())
		require.True(t, original[i].Amount().Equal(restored[i].Amount()),
			"line %d amount: wrote %s, read %s", i, original[i].Amount(), restored[i].Amount())
		require.Equal(t, original[i].Memo(), restored[i].Memo())
	}
}

func TestLoadingATransactionThatIsNotThereSaysSo(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	seedBooks(t, s)

	_, err := s.LoadTransaction(ctx, ledger.TransactionID(testID("txn:absent")))
	require.ErrorIs(t, err, store.ErrNotFound)
}
