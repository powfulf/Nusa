// SPDX-License-Identifier: AGPL-3.0-only

package store

import "errors"

// Errors leaving this package wrap one of these sentinels, so callers match on
// behaviour with errors.Is rather than on message text. Domain errors from
// internal/ledger pass through unwrapped: a transaction that does not balance
// is the domain saying so, and restating it here would give the same fact two
// names.
var (
	// ErrNotFound reports that a row the caller named does not exist.
	ErrNotFound = errors.New("not found")

	// ErrIdempotencyConflict reports the same idempotency key arriving with a
	// different request. It is a client bug rather than a race: the caller
	// believes it is retrying something it is not, and replaying the first
	// result would answer a question nobody asked.
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")

	// ErrUnbalanced reports that the database refused a transaction whose
	// postings do not sum to zero.
	//
	// The domain refuses this first, so reaching it means something wrote
	// without passing NewTransaction. That is exactly the case the constraint
	// trigger exists for, and seeing this error is a sign that a caller is
	// bypassing the repository rather than that the repository is broken.
	ErrUnbalanced = errors.New("transaction does not balance")

	// ErrInvalidWrite reports a write request that is missing something the
	// repository cannot supply for the caller — an actor, an identity, or a
	// transaction that never passed through the domain.
	ErrInvalidWrite = errors.New("invalid write request")

	// ErrCorrupt reports a stored row that cannot be turned back into a domain
	// value. It means the database holds something the domain would never have
	// produced, so it is reported loudly rather than repaired quietly.
	ErrCorrupt = errors.New("stored row is not a valid domain value")
)
