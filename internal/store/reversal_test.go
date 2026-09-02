// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/ledger"
	"github.com/powfulf/Nusa/internal/store"
)

// §5.3 as it reaches the database. A correction and a deletion are both new
// transactions that undo an old one; nothing is mutated and no row is removed,
// so the only way to tell what happened is the link and its kind.

// reversalOf builds the reversing transaction for one already written.
func reversalOf(t *testing.T, source ledger.Transaction, label string, kind ledger.ReversalKind) ledger.Transaction {
	t.Helper()

	lines := make(map[ledger.PostingID]ledger.PostingID, len(source.Postings()))
	for i, p := range source.Postings() {
		lines[p.ID()] = ledger.PostingID(testID(fmt.Sprintf("posting:%s:%d", label, i)))
	}

	reversal, err := ledger.Reverse(source, ledger.ReversalSpec{
		ID:       ledger.TransactionID(testID("txn:" + label)),
		Kind:     kind,
		Date:     mustDate(t, "2026-04-02"),
		Memo:     "reversed",
		Postings: lines,
	})
	require.NoError(t, err)
	return reversal
}

func TestAReversalComesBackWithItsLinksIntact(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	source := spend(t, "reversed-original", "2026-03-01", b.groceries, b.cash, 150_000_00)
	_, err := s.SaveTransaction(ctx, b.write("original"), source)
	require.NoError(t, err)

	reversal := reversalOf(t, source, "correction", ledger.Correction)
	_, err = s.SaveTransaction(ctx, b.write("correction"), reversal)
	require.NoError(t, err)

	back, err := s.LoadTransaction(ctx, reversal.ID())
	require.NoError(t, err)

	require.True(t, back.IsReversal())
	require.Equal(t, source.ID(), back.Reverses())
	require.Equal(t, ledger.Correction, back.ReversalKind())

	// Line-level provenance survives too, which is the half a transaction-level
	// link cannot supply.
	byReversed := make(map[ledger.PostingID]ledger.Posting, len(back.Postings()))
	for _, p := range back.Postings() {
		require.NotEmpty(t, p.Reverses(), "posting %s lost the line it undoes", p.ID())
		byReversed[p.Reverses()] = p
	}
	for _, p := range source.Postings() {
		answer, ok := byReversed[p.ID()]
		require.True(t, ok, "nothing came back answering posting %s", p.ID())
		negated, err := p.Amount().Neg()
		require.NoError(t, err)
		require.True(t, negated.Equal(answer.Amount()))
	}

	// The original is exactly as it was. A deletion is an append.
	original, err := s.LoadTransaction(ctx, source.ID())
	require.NoError(t, err)
	require.False(t, original.IsReversal())
	require.Equal(t, ledger.NotReversal, original.ReversalKind())

	// And the account is left where it started, which is the accounting claim
	// the whole mechanism exists to make.
	balances, err := s.Balance(ctx, b.groceries)
	require.NoError(t, err)
	for _, m := range balances {
		require.True(t, m.IsZero(), "the reversal did not cancel the entry: %s", m)
	}
}

func TestATombstoneIsStoredAsADeletionAndRemovesNothing(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	source := spend(t, "deleted-original", "2026-03-01", b.groceries, b.cash, 40_000_00)
	_, err := s.SaveTransaction(ctx, b.write("original"), source)
	require.NoError(t, err)

	tombstone := reversalOf(t, source, "tombstone", ledger.Deletion)
	_, err = s.SaveTransaction(ctx, b.write("tombstone"), tombstone)
	require.NoError(t, err)

	back, err := s.LoadTransaction(ctx, tombstone.ID())
	require.NoError(t, err)
	require.Equal(t, ledger.Deletion, back.ReversalKind())

	// Both transactions are still there. Nothing was deleted by the deletion.
	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, count, "a deletion adds a row, it never removes one")

	_, err = s.LoadTransaction(ctx, source.ID())
	require.NoError(t, err, "the tombstoned transaction is still readable")
}

// The UNIQUE on reverses_id is what stops a history in which one entry was
// undone twice, leaving the book short by its amount with two apparently
// legitimate reversals to explain it.
func TestNothingCanBeReversedTwice(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	source := spend(t, "twice-original", "2026-03-01", b.groceries, b.cash, 10_000_00)
	_, err := s.SaveTransaction(ctx, b.write("original"), source)
	require.NoError(t, err)

	first := reversalOf(t, source, "first-reversal", ledger.Correction)
	_, err = s.SaveTransaction(ctx, b.write("first"), first)
	require.NoError(t, err)

	second := reversalOf(t, source, "second-reversal", ledger.Deletion)
	_, err = s.SaveTransaction(ctx, b.write("second"), second)
	require.ErrorIs(t, err, store.ErrAlreadyReversed)
	require.Contains(t, err.Error(), "already been reversed",
		"the error says what happened, not which index was violated")

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, count, "the refused reversal left nothing behind")
}

func TestReversingSomethingThatIsNotInTheBookSaysSo(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	// Built but never written, so the link points at nothing.
	absent := spend(t, "never-written", "2026-03-01", b.groceries, b.cash, 5_000_00)
	reversal := reversalOf(t, absent, "orphan", ledger.Correction)

	_, err := s.SaveTransaction(ctx, b.write("orphan"), reversal)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.Contains(t, err.Error(), "not in the book")
}

// §5.6. A fingerprint blind to the reversal link would answer the second
// correction with the first one's result — a correction silently not applied.
//
// Isolating that claim takes care. Two reversals built by Reverse always differ
// in their per-line links as well, so such a test passes even with the
// transaction-level field removed from the fingerprint — which is exactly what
// happened when this guard was first broken on purpose. The two reversals here
// are therefore built by hand, carrying no line-level links at all, so that
// what they undo is the *only* thing that differs between them.
func TestTwoDifferentCorrectionsAreNotTakenForAReplay(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	// Identical in every field a fingerprint reads. Only their identities, and
	// their postings' identities, differ.
	first := spend(t, "fp-first", "2026-03-01", b.groceries, b.cash, 20_000_00)
	second := spend(t, "fp-second", "2026-03-01", b.groceries, b.cash, 20_000_00)
	_, err := s.SaveTransaction(ctx, b.write("fp-original-1"), first)
	require.NoError(t, err)
	_, err = s.SaveTransaction(ctx, b.write("fp-original-2"), second)
	require.NoError(t, err)

	// One set of reversing lines, used for both. NewTransaction permits a
	// transaction-level link without line-level ones, which is what makes the
	// two candidate requests byte-identical apart from what they reverse.
	debit, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(testID("posting:fp-reversal:debit")),
		Account: b.groceries,
		Amount:  mustMoney(t, "IDR", -20_000_00),
	})
	require.NoError(t, err)
	credit, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(testID("posting:fp-reversal:credit")),
		Account: b.cash,
		Amount:  mustMoney(t, "IDR", 20_000_00),
	})
	require.NoError(t, err)

	undo := func(t *testing.T, target ledger.Transaction) ledger.Transaction {
		t.Helper()
		txn, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID:           ledger.TransactionID(testID("txn:fp-reversal")),
			Date:         mustDate(t, "2026-04-02"),
			Payee:        "Warung fp",
			Postings:     []ledger.Posting{debit, credit},
			Reverses:     target.ID(),
			ReversalKind: ledger.Correction,
		})
		require.NoError(t, err)
		return txn
	}

	_, err = s.SaveTransaction(ctx, b.write("shared-key"), undo(t, first))
	require.NoError(t, err)

	_, err = s.SaveTransaction(ctx, b.write("shared-key"), undo(t, second))
	require.ErrorIs(t, err, store.ErrIdempotencyConflict,
		"the two undo different transactions, so they are different requests")

	// And a genuine replay of the first one still replays, so the check has
	// not simply become "always refuse".
	result, err := s.SaveTransaction(ctx, b.write("shared-key"), undo(t, first))
	require.NoError(t, err)
	require.True(t, result.Replayed)
}

// The same, one level down: two reversals identical except for which line each
// one answers.
func TestAChangedPostingLevelLinkIsNotTakenForAReplay(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	source := spend(t, "line-original", "2026-03-01", b.groceries, b.cash, 30_000_00)
	_, err := s.SaveTransaction(ctx, b.write("line-original"), source)
	require.NoError(t, err)

	reversal := reversalOf(t, source, "line-reversal", ledger.Correction)
	_, err = s.SaveTransaction(ctx, b.write("line-key"), reversal)
	require.NoError(t, err)

	// Rebuild the same reversal with the two lines answering each other's
	// originals. Every amount, account and identity is unchanged.
	lines := reversal.Postings()
	swapped := make([]ledger.Posting, 0, len(lines))
	for i, p := range lines {
		other := lines[len(lines)-1-i]
		next, err := ledger.NewPosting(ledger.PostingSpec{
			ID: p.ID(), Account: p.Account(), Amount: p.Amount(), Memo: p.Memo(),
			Reverses: other.Reverses(),
		})
		require.NoError(t, err)
		swapped = append(swapped, next)
	}
	rebuilt, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID: reversal.ID(), Date: reversal.Date(), Payee: reversal.Payee(),
		Memo: reversal.Memo(), Postings: swapped,
		Reverses: reversal.Reverses(), ReversalKind: reversal.ReversalKind(),
	})
	require.NoError(t, err)

	_, err = s.SaveTransaction(ctx, b.write("line-key"), rebuilt)
	require.ErrorIs(t, err, store.ErrIdempotencyConflict,
		"which line answers which is part of what the request says")
}

// §10. The audit log has to answer "who deleted this, and when" by filtering,
// not by joining the ledger back onto itself to see which transactions turned
// out to be tombstones.
func TestTheAuditLogSaysWhetherAWriteCorrectedOrDeleted(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	source := spend(t, "audit-original", "2026-03-01", b.groceries, b.cash, 60_000_00)
	_, err := s.SaveTransaction(ctx, b.write("audit-original"), source)
	require.NoError(t, err)

	tombstone := reversalOf(t, source, "audit-tombstone", ledger.Deletion)
	_, err = s.SaveTransaction(ctx, b.write("audit-tombstone"), tombstone)
	require.NoError(t, err)

	created, err := s.AuditEntriesFor(ctx, "transaction", string(source.ID()))
	require.NoError(t, err)
	require.Len(t, created, 1)
	require.Equal(t, "create", created[0].Action)

	deleted, err := s.AuditEntriesFor(ctx, "transaction", string(tombstone.ID()))
	require.NoError(t, err)
	require.Len(t, deleted, 1)
	require.Equal(t, "delete", deleted[0].Action,
		"an append that removes an entry from the books is not a 'create'")

	// The diff carries the link as well, so the log stands alone.
	var diff struct {
		Reverses     string `json:"reverses"`
		ReversalKind string `json:"reversal_kind"`
		Postings     []struct {
			Reverses string `json:"reverses"`
		} `json:"postings"`
	}
	require.NoError(t, json.Unmarshal(deleted[0].Diff, &diff))
	require.Equal(t, string(source.ID()), diff.Reverses)
	require.Equal(t, "deletion", diff.ReversalKind)
	require.Len(t, diff.Postings, 2)
	for _, p := range diff.Postings {
		require.NotEmpty(t, p.Reverses, "the diff lost which line was undone")
	}

	// A correction is its own action, distinct from both.
	other := spend(t, "audit-other", "2026-03-02", b.groceries, b.cash, 70_000_00)
	_, err = s.SaveTransaction(ctx, b.write("audit-other"), other)
	require.NoError(t, err)
	correction := reversalOf(t, other, "audit-correction", ledger.Correction)
	_, err = s.SaveTransaction(ctx, b.write("audit-correction"), correction)
	require.NoError(t, err)

	corrected, err := s.AuditEntriesFor(ctx, "transaction", string(correction.ID()))
	require.NoError(t, err)
	require.Len(t, corrected, 1)
	require.Equal(t, "correct", corrected[0].Action)
}

// The database carries the same rules the domain does, for a writer that never
// passed through it.
func TestTheDatabaseRefusesAReversalItCannotMakeSenseOf(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	source := spend(t, "direct-original", "2026-03-01", b.groceries, b.cash, 80_000_00)
	_, err := s.SaveTransaction(ctx, b.write("direct-original"), source)
	require.NoError(t, err)

	for _, tc := range []struct {
		name, kind string
		reverses   any
		wants      string
	}{
		{
			name: "a kind the schema does not know",
			kind: "removal", reverses: string(source.ID()),
			wants: "transactions_reversal_kind_is_known",
		},
		{
			name: "a link with no kind",
			kind: "", reverses: string(source.ID()),
			wants: "transactions_reversal_is_complete",
		},
		{
			name: "a kind with no link",
			kind: "deletion", reverses: nil,
			wants: "transactions_reversal_is_complete",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var kind any
			if tc.kind != "" {
				kind = tc.kind
			}
			_, err := s.Pool().Exec(ctx,
				`INSERT INTO transactions (id, txn_date, reverses_id, reversal_kind)
				 VALUES ($1, $2, $3, $4)`,
				testID("txn:direct:"+tc.name), "2026-04-02", tc.reverses, kind)
			require.Error(t, err, "the schema must refuse this without any help from Go")
			require.Contains(t, err.Error(), tc.wants)
		})
	}
}

// A transaction cannot reverse itself, checked by the schema as well as by the
// domain. It is the degenerate case that would otherwise net to zero on its
// own and look perfectly legitimate.
func TestTheDatabaseRefusesATransactionThatReversesItself(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_ = seedBooks(t, s)

	id := testID("txn:self-reversing")
	_, err := s.Pool().Exec(ctx,
		`INSERT INTO transactions (id, txn_date, reverses_id, reversal_kind)
		 VALUES ($1, $2, $1, 'correction')`,
		id, "2026-04-02")
	require.Error(t, err)
	require.Contains(t, err.Error(), "transactions_do_not_reverse_themselves")
}
