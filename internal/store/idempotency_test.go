// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/ledger"
	"github.com/powfulf/Nusa/internal/store"
)

// The requirement in one test: the same request, with the same key, sent
// twice, produces exactly one transaction.
func TestReplayingTheSameWriteProducesOneTransaction(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	txn := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_000)
	w := b.write("client-retry-1")

	first, err := s.SaveTransaction(ctx, w, txn)
	require.NoError(t, err)
	require.False(t, first.Replayed, "the first write is not a replay")
	require.Equal(t, string(txn.ID()), first.EntityID)

	second, err := s.SaveTransaction(ctx, w, txn)
	require.NoError(t, err, "a replay is not an error")
	require.True(t, second.Replayed, "the second write is recognised as a replay")
	require.Equal(t, first.EntityID, second.EntityID, "a replay answers with what the first write produced")

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "two identical writes left one transaction")

	// The balance is the real test of "no double effect": a duplicated write
	// that somehow wrote its postings twice would show up here even if the
	// transaction count did not.
	balance, err := s.Balance(ctx, b.cash)
	require.NoError(t, err)
	require.Len(t, balance, 1)
	require.True(t, balance[0].Equal(mustMoney(t, "IDR", -150_000)),
		"cash moved once, not twice: got %s", balance[0])

	// And the audit log records one mutation, not two. A second entry would
	// mean the replay was invisible to the count but visible to history.
	entries, err := s.CountAuditEntries(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, entries, "a replay writes no second audit entry")
}

func TestTheSameKeyWithADifferentRequestIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	first := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_000)
	_, err := s.SaveTransaction(ctx, b.write("reused"), first)
	require.NoError(t, err)

	// Same key, different transaction. The caller believes it is retrying
	// something it is not, and replaying the first result would answer a
	// question nobody asked.
	other := spend(t, "fuel", "2026-03-02", b.groceries, b.cash, 90_000)
	_, err = s.SaveTransaction(ctx, b.write("reused"), other)
	require.ErrorIs(t, err, store.ErrIdempotencyConflict)

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "the refused write left nothing behind")
}

// A key that differs only in an amount must not be mistaken for the same
// request. This is the case a fingerprint over the identity alone would miss.
func TestAChangedAmountUnderTheSameKeyIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	original := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_000)
	_, err := s.SaveTransaction(ctx, b.write("same-txn-id"), original)
	require.NoError(t, err)

	// Same transaction identity, same key, one digit different.
	amended := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_001)
	require.Equal(t, original.ID(), amended.ID(), "the fixture keeps the identity")

	_, err = s.SaveTransaction(ctx, b.write("same-txn-id"), amended)
	require.ErrorIs(t, err, store.ErrIdempotencyConflict)

	stored, err := s.LoadTransaction(ctx, original.ID())
	require.NoError(t, err)
	require.True(t, stored.Postings()[0].Amount().Equal(mustMoney(t, "IDR", 150_000)),
		"the stored amount is the one that was written first")
}

func TestDifferentKeysWriteDifferentTransactions(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	one := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_000)
	two := spend(t, "fuel", "2026-03-02", b.groceries, b.cash, 90_000)

	_, err := s.SaveTransaction(ctx, b.write("key-one"), one)
	require.NoError(t, err)
	_, err = s.SaveTransaction(ctx, b.write("key-two"), two)
	require.NoError(t, err)

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
}

// Two people may pick the same key, and neither may receive the other's
// result. This is why the key is scoped per actor rather than globally.
func TestKeysAreScopedPerActor(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	other := testID("user:housemate")
	require.NoError(t, s.SaveUser(ctx, other))

	mine := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_000)
	theirs := spend(t, "fuel", "2026-03-02", b.groceries, b.cash, 90_000)

	_, err := s.SaveTransaction(ctx, b.write("shopping"), mine)
	require.NoError(t, err)

	theirWrite := b.write("shopping")
	theirWrite.ActorID = other
	theirWrite.AuditID = testID("audit:housemate-shopping")

	result, err := s.SaveTransaction(ctx, theirWrite, theirs)
	require.NoError(t, err, "another actor's identical key is not this actor's key")
	require.False(t, result.Replayed)

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
}

func TestAnExpiredKeyIsReclaimable(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	txn := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_000)

	expiring := b.write("short-lived")
	expiring.TTL = time.Minute
	_, err := s.SaveTransaction(ctx, expiring, txn)
	require.NoError(t, err)

	// A different request under the same key, an hour later. The first claim
	// has lapsed, so this is a fresh claim rather than a conflict.
	later := spend(t, "fuel", "2026-03-02", b.groceries, b.cash, 90_000)
	reclaim := b.write("short-lived")
	reclaim.OccurredAt = expiring.OccurredAt.Add(time.Hour)
	reclaim.AuditID = testID("audit:reclaimed")

	result, err := s.SaveTransaction(ctx, reclaim, later)
	require.NoError(t, err, "an expired claim does not block forever")
	require.False(t, result.Replayed)

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
}

func TestSweepRemovesOnlyExpiredKeys(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	short := b.write("short")
	short.TTL = time.Minute
	_, err := s.SaveTransaction(ctx, short, spend(t, "a", "2026-03-01", b.groceries, b.cash, 1000))
	require.NoError(t, err)

	long := b.write("long")
	long.TTL = 30 * 24 * time.Hour
	_, err = s.SaveTransaction(ctx, long, spend(t, "b", "2026-03-02", b.groceries, b.cash, 2000))
	require.NoError(t, err)

	removed, err := s.SweepIdempotencyKeys(ctx, short.OccurredAt.Add(time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 1, removed, "only the lapsed key went")
}

func TestAWriteWithoutTheThingsItNeedsIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)
	txn := spend(t, "groceries", "2026-03-01", b.groceries, b.cash, 150_000)

	for _, tc := range []struct {
		name string
		w    store.Write
	}{
		{"no actor", store.Write{Origin: store.OriginHuman, IdempotencyKey: "k", AuditID: testID("a")}},
		{"no origin", store.Write{ActorID: b.actor, IdempotencyKey: "k", AuditID: testID("a")}},
		{"unknown origin", store.Write{ActorID: b.actor, Origin: "robot", IdempotencyKey: "k", AuditID: testID("a")}},
		{"no key", store.Write{ActorID: b.actor, Origin: store.OriginHuman, AuditID: testID("a")}},
		{"no audit id", store.Write{ActorID: b.actor, Origin: store.OriginHuman, IdempotencyKey: "k"}},
		{"actor is not a uuidv7", store.Write{ActorID: "actor-1", Origin: store.OriginHuman, IdempotencyKey: "k", AuditID: testID("a")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.SaveTransaction(ctx, tc.w, txn)
			require.ErrorIs(t, err, store.ErrInvalidWrite)
		})
	}

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, count, "no refused write wrote anything")
}

func TestAPostingToAnUnknownAccountIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	missing := ledger.AccountID(testID("account:never-created"))
	txn := spend(t, "groceries", "2026-03-01", missing, b.cash, 150_000)

	_, err := s.SaveTransaction(ctx, b.write("bad-account"), txn)
	require.ErrorIs(t, err, ledger.ErrUnknownAccount,
		"the domain refuses it, and says so in the domain's own words")

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, count)
}

func TestAPostingInACommodityTheAccountDoesNotHoldIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	// Cash holds IDR; this tries to put IDR into the USD account.
	txn := spend(t, "groceries", "2026-03-01", b.dollars, b.cash, 150_000)

	_, err := s.SaveTransaction(ctx, b.write("bad-commodity"), txn)
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, count)
}
