// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/powfulf/Nusa/internal/ledger"
)

// Origin says what caused a mutation. Section 10 requires it on every audit
// entry, and M11 is what reads it: an AI-proposed change has to be
// distinguishable from one a person made, forever, not just while anyone
// remembers which was which.
type Origin string

// The recognised origins. The database checks this list too.
const (
	OriginHuman  Origin = "human"
	OriginRule   Origin = "rule"
	OriginImport Origin = "import"
	OriginAI     Origin = "ai"
)

// IsValid reports whether the origin is one the database will accept.
func (o Origin) IsValid() bool {
	switch o {
	case OriginHuman, OriginRule, OriginImport, OriginAI:
		return true
	default:
		return false
	}
}

// DefaultIdempotencyTTL is how long a claimed key is honoured when the caller
// names no lifetime of its own. Long enough to cover any retry a client or a
// queue will attempt, short enough that the table does not grow without end.
const DefaultIdempotencyTTL = 24 * time.Hour

// Write carries everything a mutation needs that is not the mutation itself:
// who is doing it, what caused it, and how a replay of it is recognised.
//
// Identities arrive from outside, exactly as they do in the domain. This
// package mints nothing, so a retried write carries the same identities as the
// first attempt and is recognisable as the same write rather than looking like
// a new one.
type Write struct {
	// ActorID is the user responsible. Required — an idempotency key is scoped
	// per actor, because two people may pick the same key and neither may
	// receive the other's result.
	ActorID string

	// Origin is what caused the write. Required.
	Origin Origin

	// IdempotencyKey recognises a replay. Required: section 5.6 says every
	// write is idempotent, and a write with no key cannot be.
	IdempotencyKey string

	// AuditID identifies the audit entry this write records. Required, and
	// supplied by the caller for the same reason every other identity is.
	AuditID string

	// OccurredAt timestamps the audit entry. Zero means the wall clock is read
	// here, at the edge, which is allowed — this is not the domain.
	OccurredAt time.Time

	// TTL is how long the idempotency key is honoured. Zero means
	// DefaultIdempotencyTTL.
	TTL time.Duration
}

func (w Write) validate() error {
	switch {
	case w.ActorID == "":
		return fmt.Errorf("%w: no actor", ErrInvalidWrite)
	case !w.Origin.IsValid():
		return fmt.Errorf("%w: origin %q is not one of human, rule, import, ai",
			ErrInvalidWrite, w.Origin)
	case w.IdempotencyKey == "":
		return fmt.Errorf("%w: no idempotency key", ErrInvalidWrite)
	case w.AuditID == "":
		return fmt.Errorf("%w: no audit entry id", ErrInvalidWrite)
	}
	if err := ledger.ValidateID(w.ActorID); err != nil {
		return fmt.Errorf("%w: actor id: %w", ErrInvalidWrite, err)
	}
	if err := ledger.ValidateID(w.AuditID); err != nil {
		return fmt.Errorf("%w: audit id: %w", ErrInvalidWrite, err)
	}
	return nil
}

func (w Write) at() time.Time {
	if w.OccurredAt.IsZero() {
		return time.Now().UTC()
	}
	return w.OccurredAt
}

func (w Write) expiry() time.Time {
	ttl := w.TTL
	if ttl <= 0 {
		ttl = DefaultIdempotencyTTL
	}
	return w.at().Add(ttl)
}

// Result reports what a write did.
type Result struct {
	// EntityID is what was written, or what the first attempt wrote.
	EntityID string

	// Replayed is true when an earlier write with the same key and the same
	// request had already done this. Nothing was written a second time.
	Replayed bool
}

// fingerprint is a hash of the request a key was claimed for. Section 5.6 says
// a replay must not duplicate; it says nothing about honouring a key attached
// to a different request, and doing so would answer a question nobody asked.
//
// It is built from the domain values through their accessors rather than from
// whatever the caller happened to send, so two requests that mean the same
// thing hash the same however they were spelled on the way in.
func fingerprintTransaction(t ledger.Transaction) [32]byte {
	h := sha256.New()
	field := func(parts ...string) {
		for _, p := range parts {
			// The length prefix is what stops "ab"+"c" hashing as "a"+"bc".
			_, _ = io.WriteString(h, strconv.Itoa(len(p)))
			_, _ = io.WriteString(h, ":")
			_, _ = io.WriteString(h, p)
		}
	}

	field("transaction", string(t.ID()), t.Date().String(), t.Timezone(), t.Payee(), t.Memo())
	if occurred := t.OccurredAt(); !occurred.IsZero() {
		field("occurred", occurred.UTC().Format(time.RFC3339Nano))
	}
	// What a transaction undoes is part of what it is. Two corrections of the
	// same original differ only here and in their identities, and a
	// fingerprint blind to this would let the second one be answered with the
	// first one's result — a correction silently not applied.
	if t.IsReversal() {
		field("reverses", string(t.Reverses()), t.ReversalKind().String())
	}
	// In the order the author wrote them, because that order is preserved and
	// a reordered transaction is a different request.
	for _, p := range t.Postings() {
		field("posting", string(p.ID()), string(p.Account()),
			p.Amount().Amount().String(), string(p.Amount().Commodity()), p.Memo())
		if rate := p.Rate(); !rate.IsZero() {
			field("rate", string(rate.Base()), string(rate.Quote()), rate.Value().RatString())
		}
		if reversed := p.Reverses(); reversed != "" {
			field("reverses-posting", string(reversed))
		}
	}

	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// fingerprintDisposal hashes a disposal: the transaction, plus which of its
// lines are consuming lots.
//
// Which lines dispose is part of the request rather than a detail of how it is
// carried out. The same transaction written once as a plain entry and once as
// a disposal produces the same rows in transactions and postings and a
// completely different set of consumed lots, and answering the second with the
// first one's result would report a sale that consumed nothing.
func fingerprintDisposal(t ledger.Transaction, disposing []ledger.PostingID) [32]byte {
	h := sha256.New()

	base := fingerprintTransaction(t)
	_, _ = h.Write(base[:])

	// In the order given: a caller naming its lines in a different order is
	// asking for the same thing, but proving that requires sorting, and a
	// fingerprint that sorts its input is one more place for two callers to
	// disagree about what "the same request" means. The order a caller sends
	// twice is the order it sends twice.
	for _, id := range disposing {
		_, _ = io.WriteString(h, strconv.Itoa(len(id)))
		_, _ = io.WriteString(h, ":")
		_, _ = io.WriteString(h, string(id))
	}

	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// unbalancedError reports whether an error is the database refusing a
// transaction that does not sum to zero.
//
// The trigger raises with a named constraint precisely so this can be
// recognised without matching on message text, which changes.
func unbalancedError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.ConstraintName == "transaction_is_balanced"
}

// missingReversalTarget reports a reversal naming something that is not in the
// book.
//
// The foreign key catches it, but a bare foreign_key_violation names a
// constraint rather than the mistake. The caller supplied an identity for
// something it believed it was undoing, and what it needs to hear is that no
// such entry exists.
func missingReversalTarget(err error) bool {
	return constraintNamed(err, "23503",
		"transactions_reverses_id_fkey", "postings_reverses_posting_id_fkey")
}

// alreadyReversed reports a second reversal of something already reversed.
//
// reverses_id and reverses_posting_id are both UNIQUE, which is how §5.3 stops
// a history where one entry was undone twice and the book is short by its
// amount. Without this the caller would see a unique_violation naming an index.
func alreadyReversed(err error) bool {
	return constraintNamed(err, "23505",
		"transactions_reverses_id_key", "postings_reverses_posting_id_key")
}

func constraintNamed(err error, code string, names ...string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		return false
	}
	for _, name := range names {
		if pgErr.ConstraintName == name {
			return true
		}
	}
	return false
}

// nulNotAllowed reports text that PostgreSQL cannot store.
//
// A Go string may contain any byte; a PostgreSQL text value may contain any
// byte except NUL, and a write carrying one fails with SQLSTATE 22021, an
// error naming neither the field nor the fix. Every other awkward case
// survives intact — control characters, newlines, emoji, combining marks, bidi
// overrides — which the round-trip property test checks.
//
// THIS DUPLICATES ledger.validateText, AND THE DUPLICATION IS DELIBERATE.
// KEEP IT — but keep it for the right reason, which is narrower than it looks.
//
// The rule lives in the domain, where it belongs: "text a ledger can hold" is a
// fact about the ledger rather than about PostgreSQL, which merely noticed it
// first. NewTransaction, NewPosting and NewAccount all refuse NUL.
//
// THIS IS A REGRESSION GUARD, NOT A BYPASS GUARD, and the distinction matters
// enough to spell out. The deferred balance trigger is a bypass guard: it lives
// in the database, so it still fires for an importer, a rule engine or a psql
// session that never goes near Go. This check is not that. It takes a
// ledger.Transaction and a ledger.Account, and neither of those can carry a NUL
// — the constructors refuse to build one. So nothing that reaches this function
// today can fail it: it is unreachable through its own signature, and no test
// can drive it without first weakening the domain.
//
// What it does catch is the domain rule being loosened or lost in a later
// refactor. That was proved by deleting the check in ledger.validateText: this
// is what then surfaced, naming the field, instead of a bare SQLSTATE from
// PostgreSQL hundreds of lines away. That is worth keeping, and it is a smaller
// claim than the trigger's.
//
// The bypass layer for NUL is PostgreSQL itself: a text column cannot hold one,
// whoever is writing. TestPostgresRefusesNulWhenTheDomainIsBypassedEntirely
// covers that, by writing straight to the table.
//
// Refused, never stripped. Quietly mutating what someone typed is how a payee
// stops matching the bank statement it was copied from, with no error anywhere.
func nulNotAllowed(field, value string) error {
	if i := strings.IndexByte(value, 0); i >= 0 {
		return fmt.Errorf("%w: %s contains a NUL byte at offset %d, and text columns cannot hold one",
			ErrInvalidWrite, field, i)
	}
	return nil
}

// validateTransactionText checks every user-supplied string on a transaction
// before any of it reaches the database.
func validateTransactionText(txn ledger.Transaction) error {
	for _, check := range []struct{ field, value string }{
		{"payee", txn.Payee()},
		{"memo", txn.Memo()},
		{"timezone", txn.Timezone()},
	} {
		if err := nulNotAllowed(fmt.Sprintf("transaction %s %s", txn.ID(), check.field), check.value); err != nil {
			return err
		}
	}
	for _, p := range txn.Postings() {
		if err := nulNotAllowed(fmt.Sprintf("posting %s memo", p.ID()), p.Memo()); err != nil {
			return err
		}
	}
	return nil
}
