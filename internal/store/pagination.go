// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// Keyset pagination over the journal.
//
// A page is described by where the last one stopped rather than by how many
// rows to count past. The difference is not only cost. With OFFSET, a
// transaction written while a caller is part-way through shifts every later
// page by one, so the caller is handed a row it has already seen and never
// shown the one that took its place — silently, with nothing anywhere
// reporting it. A position expressed as a key cannot do that, because it names
// a row rather than a distance.
//
// What that buys, stated exactly, because it is easy to over-claim: **every
// transaction that existed when the walk began is returned exactly once**, and
// no transaction is returned twice. It is not a snapshot. A transaction
// written mid-walk is included if it sorts after the current position and
// omitted if it sorts before it, and both outcomes are correct — the caller
// asked for the rows after a position, not for the book as it stood at a
// moment.
//
// The guarantee rests on the sort key never changing for a row that has one,
// and here that is structural rather than hopeful: transactions are immutable
// (§5.3), and the composite foreign key from postings pins txn_date with
// ON UPDATE RESTRICT. Nothing can move a row from one side of a cursor to the
// other. Applying this pattern to a table whose sort key can be updated would
// need that argument made again, and it would not hold.

// Page size bounds, defined here because the store is what has to serve them.
//
// The maximum is a limit on the work one request can ask for, not a
// suggestion: a page carries every posting of every transaction in it, so the
// row count behind a page is several times its size.
const (
	// DefaultPageSize is used when a caller names no size.
	DefaultPageSize = 50

	// MaxPageSize is the largest page that will be served.
	MaxPageSize = 200
)

// TransactionCursor is a position in the transaction ordering.
//
// Both fields are needed and neither is redundant. The date orders the book
// the way a person reads it; the identity breaks ties, and without it two
// transactions sharing a civil date have no defined order between them — which
// is a skipped row and a duplicated row in one, appearing only when a page
// boundary happens to fall inside a day.
type TransactionCursor struct {
	Date ledger.Date
	ID   ledger.TransactionID
}

// IsZero reports the position before the first row.
//
// The identity alone decides it. A zero Date is a legitimate thing to find in
// a malformed cursor, and treating "no identity" as the only way to mean "the
// beginning" keeps one answer to that question.
func (c TransactionCursor) IsZero() bool { return c.ID == "" }

// TransactionQuery describes one page.
type TransactionQuery struct {
	// From and To bound the civil dates, both counted. A zero Date means
	// unbounded on that side.
	From, To ledger.Date

	// After is where the previous page stopped. Zero starts at the beginning.
	After TransactionCursor

	// Limit is how many transactions to return. Zero means DefaultPageSize;
	// anything above MaxPageSize is refused rather than quietly reduced, so a
	// caller asking for a thousand rows finds out rather than believing it
	// received them.
	Limit int
}

// TransactionPage is one page of the journal.
type TransactionPage struct {
	// Transactions are the rows, in (date, identity) order.
	Transactions []ledger.Transaction

	// Next is where the following page starts. Zero when this page is the
	// last: the store fetches one row beyond the page to tell "there is no
	// more" from "the next page happens to be empty", which a caller cannot
	// distinguish from a full page alone.
	Next TransactionCursor
}

// TransactionsPage reads one page of transactions with their postings.
func (s *Store) TransactionsPage(ctx context.Context, q TransactionQuery) (TransactionPage, error) {
	limit := q.Limit
	switch {
	case limit == 0:
		limit = DefaultPageSize
	case limit < 0:
		return TransactionPage{}, fmt.Errorf("%w: page size %d is negative", ErrInvalidWrite, limit)
	case limit > MaxPageSize:
		return TransactionPage{}, fmt.Errorf("%w: page size %d is above the maximum of %d",
			ErrInvalidWrite, limit, MaxPageSize)
	}

	if !q.After.IsZero() {
		if err := ledger.ValidateID(string(q.After.ID)); err != nil {
			return TransactionPage{}, fmt.Errorf("%w: cursor id: %w", ErrInvalidWrite, err)
		}
		if q.After.Date.IsZero() {
			return TransactionPage{}, fmt.Errorf(
				"%w: cursor names a transaction but no date, so it has no position",
				ErrInvalidWrite)
		}
	}

	afterID, err := uuidFrom(string(q.After.ID))
	if err != nil {
		return TransactionPage{}, err
	}

	// One row beyond the page. Reading exactly `limit` rows leaves the caller
	// unable to tell a full last page from a full page with more behind it,
	// and the usual repair — issuing the next request and getting nothing —
	// costs a round trip on every walk.
	rows, err := s.ListTransactionsPage(ctx, ListTransactionsPageParams{
		FromDate:  dateFrom(q.From),
		ToDate:    dateFrom(q.To),
		AfterDate: dateFrom(q.After.Date),
		AfterID:   afterID,
		RowLimit:  int32(limit) + 1, //nolint:gosec // G115: bounded by MaxPageSize above
	})
	if err != nil {
		return TransactionPage{}, fmt.Errorf("list transactions page: %w", err)
	}

	var next TransactionCursor
	if len(rows) > limit {
		last := rows[limit-1]
		date, err := dateTo(last.TxnDate)
		if err != nil {
			return TransactionPage{}, fmt.Errorf("transaction %s: %w", uuidTo(last.ID), err)
		}
		next = TransactionCursor{Date: date, ID: ledger.TransactionID(uuidTo(last.ID))}
		rows = rows[:limit]
	}

	transactions, err := s.transactionsFrom(ctx, rows)
	if err != nil {
		return TransactionPage{}, err
	}
	return TransactionPage{Transactions: transactions, Next: next}, nil
}

// transactionsFrom attaches postings to a page of headers, in two queries
// rather than one per row.
func (s *Store) transactionsFrom(ctx context.Context, rows []Transaction) ([]ledger.Transaction, error) {
	if len(rows) == 0 {
		return []ledger.Transaction{}, nil
	}

	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}

	postingRows, err := s.ListPostingsByTransactions(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list postings for a page of transactions: %w", err)
	}

	byTransaction := make(map[string][]Posting, len(rows))
	for _, pr := range postingRows {
		owner := uuidTo(pr.TransactionID)
		byTransaction[owner] = append(byTransaction[owner], pr)
	}

	out := make([]ledger.Transaction, 0, len(rows))
	for _, row := range rows {
		id := uuidTo(row.ID)

		// A header with no lines is not a transaction the domain can build,
		// and NewTransaction would refuse it with a message about needing two
		// postings — true, and unhelpful about why none arrived. The schema
		// makes this unreachable through the repository; reaching it means a
		// row was written around it.
		lines, ok := byTransaction[id]
		if !ok {
			return nil, fmt.Errorf("%w: transaction %s has no postings", ErrCorrupt, id)
		}

		postings, err := postingsFrom(lines)
		if err != nil {
			return nil, err
		}

		date, err := dateTo(row.TxnDate)
		if err != nil {
			return nil, fmt.Errorf("transaction %s: %w", id, err)
		}
		kind, err := ledger.ParseReversalKind(textTo(row.ReversalKind))
		if err != nil {
			return nil, fmt.Errorf("%w: transaction %s: %w", ErrCorrupt, id, err)
		}

		txn, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID:           ledger.TransactionID(id),
			Date:         date,
			OccurredAt:   timestampTo(row.OccurredAt),
			Timezone:     row.Timezone,
			Payee:        row.Payee,
			Memo:         row.Memo,
			Postings:     postings,
			Reverses:     ledger.TransactionID(uuidTo(row.ReversesID)),
			ReversalKind: kind,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: transaction %s: %w", ErrCorrupt, id, err)
		}
		out = append(out, txn)
	}
	return out, nil
}
