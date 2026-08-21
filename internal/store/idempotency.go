// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// Idempotency is enforced here, in the repository, rather than in HTTP
// middleware (section 5.6). The importer, the rule engine and the scheduler all
// replay writes with no request anywhere in sight, and a guarantee that only
// holds for callers who happen to arrive over HTTP is not the guarantee the
// rule asks for. M2b's middleware will use this; it will not replace it.

type claimOutcome struct {
	// replayed is true when a live claim for the same request already exists.
	replayed bool

	// entityID is what the first attempt wrote. Only meaningful when replayed.
	entityID string
}

// claimKey takes the idempotency key for this write, or reports that an
// earlier identical write already holds it.
//
// It runs inside the caller's database transaction, which is what makes the
// claim and the write it protects succeed or fail together. Claiming in one
// transaction and writing in another would leave a key pointing at a write
// that never happened.
func claimKey(
	ctx context.Context, q *Queries, w Write,
	entityKind, entityID string, fingerprint [32]byte,
) (claimOutcome, error) {
	actor, err := uuidFrom(w.ActorID)
	if err != nil {
		return claimOutcome{}, err
	}
	entity, err := uuidFrom(entityID)
	if err != nil {
		return claimOutcome{}, err
	}

	now := w.at()
	_, err = q.ClaimIdempotencyKey(ctx, ClaimIdempotencyKeyParams{
		ActorID:     actor,
		Key:         w.IdempotencyKey,
		Fingerprint: fingerprint[:],
		EntityKind:  entityKind,
		EntityID:    entity,
		CreatedAt:   timestampFrom(now),
		ExpiresAt:   timestampFrom(w.expiry()),
	})
	switch {
	case err == nil:
		return claimOutcome{}, nil // the key is ours; the write may proceed
	case !errors.Is(err, pgx.ErrNoRows):
		return claimOutcome{}, fmt.Errorf("claim idempotency key: %w", err)
	}

	// No row came back, so a live claim exists. Whether this is a replay or a
	// client bug depends entirely on whether it was the same request.
	existing, err := q.GetIdempotencyKey(ctx, GetIdempotencyKeyParams{
		ActorID: actor,
		Key:     w.IdempotencyKey,
	})
	if err != nil {
		return claimOutcome{}, fmt.Errorf("read existing idempotency key: %w", err)
	}

	if !bytes.Equal(existing.Fingerprint, fingerprint[:]) {
		return claimOutcome{}, fmt.Errorf(
			"%w: key %q was claimed for a different %s",
			ErrIdempotencyConflict, w.IdempotencyKey, existing.EntityKind)
	}
	return claimOutcome{replayed: true, entityID: uuidTo(existing.EntityID)}, nil
}

// SweepIdempotencyKeys removes claims that have expired, and reports how many
// went. Nothing calls it on a timer yet; M2b schedules it.
func (s *Store) SweepIdempotencyKeys(ctx context.Context, asOf time.Time) (int64, error) {
	removed, err := s.DeleteExpiredIdempotencyKeys(ctx, timestampFrom(asOf))
	if err != nil {
		return 0, fmt.Errorf("sweep idempotency keys: %w", err)
	}
	return removed, nil
}

// SaveUser writes a user row. Identity only until M2b adds credentials.
func (s *Store) SaveUser(ctx context.Context, id string) error {
	if err := ledger.ValidateID(id); err != nil {
		return fmt.Errorf("%w: user id: %w", ErrInvalidWrite, err)
	}
	key, err := uuidFrom(id)
	if err != nil {
		return err
	}
	if err := s.InsertUser(ctx, key); err != nil {
		return fmt.Errorf("insert user %s: %w", id, err)
	}
	return nil
}
