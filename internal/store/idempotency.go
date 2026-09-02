// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/powfulf/Nusa/internal/ledger"
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

// The two halves of an HTTP replay. A key is claimed inside the write's own
// database transaction (see claimKey), so a stored claim always describes a
// write that committed; the response is recorded afterwards, by the edge,
// because rendering it is not this package's job.
//
// The pair is deliberately narrow. Nothing here knows what a status code means
// or what the body contains — it is two opaque values keyed by a claim, and
// the HTTP layer decides everything else.

// SaveIdempotentResponse records what the first attempt answered, so that a
// replay of it can answer the same way.
//
// Status and body travel together because the schema refuses half a response:
// a replay that had to invent a missing status would be answering a question
// the original never asked. The status is checked here as well as by the
// constraint, so a caller passing zero learns which value was wrong rather
// than reading a constraint name.
func (s *Store) SaveIdempotentResponse(
	ctx context.Context, actorID, key string, status int, body []byte,
) error {
	actor, err := uuidFrom(actorID)
	if err != nil {
		return err
	}
	if status < 100 || status > 599 {
		return fmt.Errorf("%w: %d is not an HTTP status", ErrInvalidWrite, status)
	}
	if !json.Valid(body) {
		// The column is jsonb. Postgres would refuse this too, with an error
		// naming a syntax position in a document the caller never sees.
		return fmt.Errorf("%w: response body is not JSON", ErrInvalidWrite)
	}

	narrowed := int16(status) //nolint:gosec // G115: bounded to 100..599 above
	rows, err := s.RecordIdempotentResponse(ctx, RecordIdempotentResponseParams{
		ActorID:        actor,
		Key:            key,
		ResponseStatus: &narrowed,
		ResponseBody:   body,
	})
	if err != nil {
		return fmt.Errorf("record idempotent response: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: no claim on key %q to attach a response to", ErrNotFound, key)
	}
	return nil
}

// IdempotentResponse reads what an earlier attempt answered.
//
// The second return value separates "no claim, or a claim with no response
// yet" from "a response that happens to be empty". They are not the same
// thing: the first means the caller must produce an answer of its own, and a
// zero status silently standing in for it would replay a response nobody ever
// sent.
func (s *Store) IdempotentResponse(
	ctx context.Context, actorID, key string,
) (status int, body []byte, ok bool, err error) {
	actor, err := uuidFrom(actorID)
	if err != nil {
		return 0, nil, false, err
	}

	row, err := s.GetIdempotencyKey(ctx, GetIdempotencyKeyParams{ActorID: actor, Key: key})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, nil, false, nil
	case err != nil:
		return 0, nil, false, fmt.Errorf("read idempotency key: %w", err)
	}

	// Both or neither, enforced by idempotency_keys_response_is_whole_or_absent.
	// Reading only one of them would make a schema guarantee into an
	// assumption held in this function.
	if row.ResponseStatus == nil || row.ResponseBody == nil {
		return 0, nil, false, nil
	}
	return int(*row.ResponseStatus), row.ResponseBody, true, nil
}
