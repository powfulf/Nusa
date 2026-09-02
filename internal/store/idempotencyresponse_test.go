// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/store"
)

// These tests are deliberately not parallel. open(t) truncates the shared
// container, so two of them running at once delete each other's fixtures —
// which is why no other test in this package is parallel either.
//
// What these guards must cover, decided before any of them was written.
//
//  1. A recorded response reads back with the same status.
//  2. What comes back out of the column, byte for byte — asserted rather than
//     assumed. It was jsonb until migration 10 and did not hold: this guard is
//     what found that, and it is what stops the column drifting back.
//  3. A claim with no recorded response is distinguishable from one with an
//     empty response.
//  4. A key nobody claimed reads back as absent rather than as an error.
//  5. A status that is not an HTTP status is refused before the database sees
//     it, so the caller learns which value was wrong.
//  6. A body that is not JSON is refused before the database sees it.
//  7. Recording against a key with no claim is reported, not silently lost.

// claimed writes a real transaction so that an idempotency claim exists to
// attach a response to. Going through SaveTransaction rather than inserting a
// row directly is the point: the claim under test is the one the write path
// actually produces.
func claimed(t *testing.T, s *store.Store, b books, key string) {
	t.Helper()
	_, err := s.SaveTransaction(context.Background(), b.write(key),
		spend(t, "claim:"+key, "2026-03-01", b.groceries, b.cash, 25_000))
	require.NoError(t, err)
}

func TestARecordedResponseReadsBackWithItsStatus(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)
	claimed(t, s, b, "key-status")

	require.NoError(t, s.SaveIdempotentResponse(ctx, b.actor, "key-status",
		201, []byte(`{"id":"abc"}`)))

	status, body, ok, err := s.IdempotentResponse(ctx, b.actor, "key-status")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 201, status)
	require.JSONEq(t, `{"id":"abc"}`, string(body))
}

// TestTheResponseComesBackVerbatim is the guard the replay path rests on.
//
// writeIdempotentResponse hands the stored bytes straight to the client and
// says so in its own comment. That claim is only as true as this column, and
// when the column was jsonb it was false — keys reordered, whitespace dropped,
// a duplicate key discarded. Migration 10 changed the type; this is what
// proves it and what stops it drifting back.
//
// It is a characterisation test: it asserts what PostgreSQL does rather than
// what any specification promises. That is the point, because what it does is
// what the replay path inherits.
func TestTheResponseComesBackVerbatim(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)
	claimed(t, s, b, "key-bytes")

	// Deliberately awkward: keys out of alphabetical order, insignificant
	// whitespace, and a duplicated key. All three are legal JSON, and all three
	// are what jsonb changed. A well-behaved document would pass under either
	// type and prove nothing.
	original := []byte(`{"z": 1,  "a"  :  "two", "a": "three"}`)
	require.NoError(t, s.SaveIdempotentResponse(ctx, b.actor, "key-bytes", 200, original))

	_, body, ok, err := s.IdempotentResponse(ctx, b.actor, "key-bytes")
	require.NoError(t, err)
	require.True(t, ok)

	t.Logf("stored:    %s", original)
	t.Logf("read back: %s", body)
	require.Equal(t, string(original), string(body),
		"the column altered the document; the replay path cannot claim byte equality")
}

func TestAClaimWithNoRecordedResponseIsNotAResponse(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)
	claimed(t, s, b, "key-unanswered")

	// The claim exists — the write committed — and no response was recorded
	// against it. That is the crash window the HTTP layer has to recover from,
	// and it must be distinguishable from a recorded empty document.
	status, body, ok, err := s.IdempotentResponse(ctx, b.actor, "key-unanswered")
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, status)
	require.Nil(t, body)
}

func TestAKeyNobodyClaimedReadsBackAsAbsent(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	_, _, ok, err := s.IdempotentResponse(ctx, b.actor, "never-claimed")
	require.NoError(t, err, "an absent key is an answer, not a failure")
	require.False(t, ok)
}

func TestAnImpossibleStatusIsRefusedBeforeTheDatabaseSeesIt(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)
	claimed(t, s, b, "key-status-range")

	for _, status := range []int{0, 99, 600, -1, 1000} {
		err := s.SaveIdempotentResponse(ctx, b.actor, "key-status-range", status, []byte(`{}`))
		require.Error(t, err, "status %d", status)
		require.ErrorIs(t, err, store.ErrInvalidWrite)
		// The message must name the value. A constraint name would leave the
		// caller reading the schema to find out which argument was wrong.
		require.Contains(t, err.Error(), "is not an HTTP status")
	}
}

func TestABodyThatIsNotJSONIsRefusedBeforeTheDatabaseSeesIt(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)
	claimed(t, s, b, "key-not-json")

	for name, body := range map[string][]byte{
		"empty":     nil,
		"truncated": []byte(`{"id":`),
		"bare text": []byte(`not json at all`),
		"trailing":  []byte(`{} {}`),
	} {
		t.Run(name, func(t *testing.T) {
			err := s.SaveIdempotentResponse(ctx, b.actor, "key-not-json", 200, body)
			require.Error(t, err)
			require.ErrorIs(t, err, store.ErrInvalidWrite)
		})
	}
}

func TestRecordingAgainstAnUnclaimedKeyIsReported(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	// UPDATE ... WHERE matching nothing is not an error to PostgreSQL. Left
	// unchecked it would look exactly like a successful record, and the replay
	// it was supposed to enable would find nothing.
	err := s.SaveIdempotentResponse(ctx, b.actor, "no-such-key", 200, []byte(`{}`))
	require.Error(t, err)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestAMalformedActorIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	err := s.SaveIdempotentResponse(ctx, "not-a-uuid", "k", 200, []byte(`{}`))
	require.ErrorIs(t, err, store.ErrInvalidWrite)
	require.Contains(t, err.Error(), "is not a uuid")

	_, _, _, err = s.IdempotentResponse(ctx, "not-a-uuid", "k")
	require.ErrorIs(t, err, store.ErrInvalidWrite)
	require.Contains(t, err.Error(), "is not a uuid")

	// Asserted on the error itself. The first draft of this line read
	//
	//     require.True(t, errors.Is(err, store.ErrInvalidWrite) ||
	//         ledger.ValidateID("not-a-uuid") != nil)
	//
	// whose second disjunct is true for every run, so the assertion could not
	// fail whatever the code did — the test arranged its own answer (§11).
}
