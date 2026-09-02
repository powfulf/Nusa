// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/powfulf/Nusa/internal/ledger"
)

// Balances are computed by summing postings in SQL, with nothing cached
// anywhere. That is section 5.2 taken literally rather than approximated: an
// account balance is the sum of its postings, so there is no second figure
// that can drift from the first and no reconstruction to get right.
//
// A cached balance stays available as a later optimisation, and the schema is
// already the shape it would need. It is not here because there is no
// measurement asking for it, and because this query is the oracle any cache
// would have to be checked against — building the definition first means the
// checker exists before there is anything to check.
//
// Balances come out raw and signed, debit positive, exactly as the domain
// defines them. Turning that into the figure a person expects is
// AccountKind.NormalSign's job, further out.

// Balance returns what one account holds, per commodity, over all time.
func (s *Store) Balance(ctx context.Context, account ledger.AccountID) ([]ledger.Money, error) {
	id, err := uuidFrom(string(account))
	if err != nil {
		return nil, err
	}
	rows, err := s.AccountBalance(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("balance of %s: %w", account, err)
	}

	out := make([]ledger.Money, 0, len(rows))
	for _, row := range rows {
		m, err := moneyTo(row.Total, row.CommodityCode)
		if err != nil {
			return nil, fmt.Errorf("balance of %s: %w", account, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// BalanceAsOf returns what one account held at the end of a civil date,
// counting that date.
//
// Only the transaction's civil date decides what is included. OccurredAt and
// Timezone are never consulted, which is what makes this answer the same for
// every reader in every zone.
func (s *Store) BalanceAsOf(
	ctx context.Context, account ledger.AccountID, on ledger.Date,
) ([]ledger.Money, error) {
	if on.IsZero() {
		return s.Balance(ctx, account)
	}
	id, err := uuidFrom(string(account))
	if err != nil {
		return nil, err
	}
	rows, err := s.AccountBalanceAsOf(ctx, AccountBalanceAsOfParams{
		AccountID: id,
		TxnDate:   dateFrom(on),
	})
	if err != nil {
		return nil, fmt.Errorf("balance of %s as of %s: %w", account, on, err)
	}
	return moneyRows(rows, func(r AccountBalanceAsOfRow) (pgtype.Numeric, string) {
		return r.Total, r.CommodityCode
	})
}

// BalanceBetween returns what moved through one account between two civil
// dates, both counted.
func (s *Store) BalanceBetween(
	ctx context.Context, account ledger.AccountID, from, to ledger.Date,
) ([]ledger.Money, error) {
	if from.IsZero() || to.IsZero() {
		return nil, fmt.Errorf("%w: a period needs both ends", ledger.ErrInvalidDate)
	}
	if from.After(to) {
		return nil, fmt.Errorf("%w: %s is after %s", ledger.ErrInvalidDate, from, to)
	}
	id, err := uuidFrom(string(account))
	if err != nil {
		return nil, err
	}
	rows, err := s.AccountBalanceBetween(ctx, AccountBalanceBetweenParams{
		AccountID: id,
		TxnDate:   dateFrom(from),
		TxnDate_2: dateFrom(to),
	})
	if err != nil {
		return nil, fmt.Errorf("balance of %s between %s and %s: %w", account, from, to, err)
	}
	return moneyRows(rows, func(r AccountBalanceBetweenRow) (pgtype.Numeric, string) {
		return r.Total, r.CommodityCode
	})
}

// SubtreeBalance returns an account and everything beneath it, summed, as of a
// civil date. The subtree is walked in SQL by following parent links.
func (s *Store) SubtreeBalance(
	ctx context.Context, account ledger.AccountID, on ledger.Date,
) ([]ledger.Money, error) {
	id, err := uuidFrom(string(account))
	if err != nil {
		return nil, err
	}
	// A zero date means all time. The date column has no representable
	// infinity here, so an absent bound becomes the largest date PostgreSQL
	// will hold, which no transaction can exceed.
	bound := dateFrom(on)
	if on.IsZero() {
		bound = pgtype.Date{InfinityModifier: pgtype.Infinity, Valid: true}
	}

	rows, err := s.SubtreeBalanceAsOf(ctx, SubtreeBalanceAsOfParams{
		ID:      id,
		TxnDate: bound,
	})
	if err != nil {
		return nil, fmt.Errorf("subtree balance of %s: %w", account, err)
	}
	return moneyRows(rows, func(r SubtreeBalanceAsOfRow) (pgtype.Numeric, string) {
		return r.Total, r.CommodityCode
	})
}

// BookTotals returns every posting in the book summed by commodity.
//
// It is the whole-book form of section 5.1 and must always be zero: each
// transaction sums to zero on its own, so any number of them still do. A
// non-zero total means something reached the tables without passing
// NewTransaction — which the constraint trigger should have caught, making
// this the check on the check.
func (s *Store) BookTotals(ctx context.Context) ([]ledger.Money, error) {
	rows, err := s.TotalsByCommodity(ctx)
	if err != nil {
		return nil, fmt.Errorf("book totals: %w", err)
	}
	return moneyRows(rows, func(r TotalsByCommodityRow) (pgtype.Numeric, string) {
		return r.Total, r.CommodityCode
	})
}

// moneyRows turns a set of aggregate rows into Money values, in the order the
// query returned them — which is by commodity code, matching how the domain
// orders a Balances.
func moneyRows[T any](rows []T, split func(T) (pgtype.Numeric, string)) ([]ledger.Money, error) {
	out := make([]ledger.Money, 0, len(rows))
	for _, row := range rows {
		amount, commodity := split(row)
		m, err := moneyTo(amount, commodity)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
