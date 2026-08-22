// SPDX-License-Identifier: MIT

package ledger

import (
	"fmt"
	"time"
)

// ReversalKind says why a transaction was undone.
//
// The two kinds are not interchangeable and the distinction is not cosmetic. A
// correction says the event happened but was recorded wrongly; a deletion says
// the event never happened at all. A report that excludes deleted transactions
// while keeping corrected ones needs to tell them apart, and so does a person
// reading their own history a year later.
type ReversalKind uint8

// The kinds. The database checks this same list, in the reversal_kind check
// constraint, so a value that cannot be spelled here cannot be stored either.
const (
	// NotReversal is the zero value: a transaction that undoes nothing.
	NotReversal ReversalKind = iota

	// Correction undoes figures that were wrong. The event happened.
	Correction

	// Deletion is the tombstone. The event should never have been recorded.
	//
	// It is a new transaction rather than a flag on the old one, because a
	// flag can be turned off and a posting is immutable (§5.3). Nothing is
	// mutated and no row is removed; the book only ever grows.
	Deletion
)

// String returns the wire and database spelling of the kind.
func (k ReversalKind) String() string {
	switch k {
	case NotReversal:
		return ""
	case Correction:
		return "correction"
	case Deletion:
		return "deletion"
	default:
		return fmt.Sprintf("ReversalKind(%d)", uint8(k))
	}
}

// IsValid reports whether the kind is one that actually reverses something.
func (k ReversalKind) IsValid() bool { return k == Correction || k == Deletion }

// ParseReversalKind reads the database spelling back.
//
// It exists because a value read out of storage has to become a domain value
// again, and the only alternative is for the store to compare strings — which
// is where the two lists quietly drift apart.
func ParseReversalKind(s string) (ReversalKind, error) {
	switch s {
	case "":
		return NotReversal, nil
	case "correction":
		return Correction, nil
	case "deletion":
		return Deletion, nil
	default:
		return NotReversal, fmt.Errorf("%w: %q is not correction or deletion", ErrInvalidReversal, s)
	}
}

// ReversalSpec is the input to Reverse: everything about the reversing
// transaction that cannot be derived from the one being reversed.
type ReversalSpec struct {
	// ID identifies the reversing transaction. Required, and supplied by the
	// caller like every other identity here.
	ID TransactionID

	// Kind says whether this is a correction or a deletion. Required.
	Kind ReversalKind

	// Date is the civil date the reversal is booked on. Required, and
	// deliberately not defaulted to the original's date.
	//
	// Which date to use is an accounting decision, not a mechanical one.
	// Booking the reversal on today's date leaves last year's report saying
	// what it always said, which is what §5.4 protects; booking it on the
	// original's date rewrites that period, which is sometimes what a
	// correction made an hour later actually means. The domain will not guess
	// between those, because guessing silently moves money between periods.
	Date Date

	// OccurredAt, Timezone and Memo describe the reversal itself. All
	// optional; the memo is where "wrong amount, see receipt" belongs.
	OccurredAt time.Time
	Timezone   string
	Memo       string

	// Postings maps each posting of the original to the identity its
	// answering line will carry. Required, with exactly one entry per original
	// posting and no two entries sharing a value.
	//
	// It is a map keyed by the original posting's identity rather than a slice
	// in posting order, because §5.7 forbids pointing at a posting by its
	// position: a reordered slice would silently attach a reversing line to
	// the wrong original, with no error anywhere.
	Postings map[PostingID]PostingID
}

// Reverse builds the transaction that undoes another one.
//
// One builder serves both kinds. Corrections and deletions are the same
// mechanism — a new transaction whose lines answer the original's one for one,
// with every amount negated — and writing them as two builders would create
// two places for §5.3 to drift apart.
//
// The original is not touched, read for anything other than its own values, or
// marked in any way. It cannot be: it is immutable, and that is the point. The
// link runs from the reversal to the original, never the other way, so the
// only thing that changes when a transaction is reversed is that another
// transaction now exists.
//
// Every posting's Rate is carried across exactly as it was recorded. It is not
// looked up, not recomputed, and not converted. §5.4 stores a rate on the
// posting so that a report about last year says the same thing today as it did
// last year, and a reversal that priced itself at today's rate would leave the
// pair failing to cancel — the original and its reversal would sum to whatever
// the rate had moved by, which is a fabricated gain nobody booked.
func Reverse(original Transaction, spec ReversalSpec) (Transaction, error) {
	if !original.IsValid() {
		return Transaction{}, fmt.Errorf("%w: nothing to reverse", ErrInvalidReversal)
	}
	if !spec.Kind.IsValid() {
		return Transaction{}, fmt.Errorf("%w: reversal %q must be a correction or a deletion",
			ErrInvalidReversal, spec.ID)
	}

	originals := original.Postings()
	if len(spec.Postings) != len(originals) {
		return Transaction{}, fmt.Errorf(
			"%w: reversal %q supplies %d posting identities for a transaction with %d lines",
			ErrInvalidReversal, spec.ID, len(spec.Postings), len(originals))
	}

	taken := make(map[PostingID]struct{}, len(spec.Postings))
	postings := make([]Posting, 0, len(originals))
	for _, source := range originals {
		id, ok := spec.Postings[source.ID()]
		if !ok {
			return Transaction{}, fmt.Errorf("%w: reversal %q has no line answering posting %q",
				ErrInvalidReversal, spec.ID, source.ID())
		}
		if _, exists := taken[id]; exists {
			return Transaction{}, fmt.Errorf("%w: reversal %q uses posting identity %q twice",
				ErrDuplicateID, spec.ID, id)
		}
		taken[id] = struct{}{}

		amount, err := source.Amount().Neg()
		if err != nil {
			return Transaction{}, fmt.Errorf("reversal %q: posting %q: %w", spec.ID, source.ID(), err)
		}

		posting, err := NewPosting(PostingSpec{
			ID:      id,
			Account: source.Account(),
			Amount:  amount,
			// Verbatim. See the note above: recomputing this is how a reversal
			// stops cancelling the thing it reverses.
			Rate: source.Rate(),
			// The answering line describes the same line, so it carries the
			// same note. What is new about this transaction belongs in
			// spec.Memo, on the transaction.
			Memo:     source.Memo(),
			Reverses: source.ID(),
		})
		if err != nil {
			return Transaction{}, fmt.Errorf("reversal %q: %w", spec.ID, err)
		}
		postings = append(postings, posting)
	}

	return NewTransaction(TransactionSpec{
		ID:         spec.ID,
		Date:       spec.Date,
		OccurredAt: spec.OccurredAt,
		Timezone:   spec.Timezone,
		// The counterparty did not change because the entry was wrong, and a
		// reversal that lost the payee would be unreadable in a list beside
		// the entry it undoes.
		Payee:        original.Payee(),
		Memo:         spec.Memo,
		Postings:     postings,
		Reverses:     original.ID(),
		ReversalKind: spec.Kind,
	})
}
