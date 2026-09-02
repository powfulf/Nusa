// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/powfulf/Nusa/internal/ledger"
	"github.com/powfulf/Nusa/internal/store"
)

// resetLedger empties everything a check writes, leaving the accounts and the
// actor in place. Accounts are the fixture; transactions are the subject.
func resetLedger(rt *rapid.T, s *store.Store) {
	_, err := s.Pool().Exec(context.Background(),
		`TRUNCATE audit_log, idempotency_keys, lot_consumptions, lots, postings, transactions`)
	if err != nil {
		rt.Fatalf("reset ledger tables: %v", err)
	}
}

// TestPropertyEverythingWrittenComesBackExactly is the milestone's central
// claim: whatever the domain can build, the database can hold and return
// unchanged.
//
// Round-tripping is where a persistence layer actually fails. A transaction
// written and read back that differs by one smallest unit is a wrong balance,
// and a wrong balance discovered a year later cannot be told apart from theft.
// Reading the value back is the only way to know the mapping is exact; nothing
// about the schema says so on its own.
func TestPropertyEverythingWrittenComesBackExactly(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedPropertyBooks(t, s)

	// The accounts are seeded once and read back once, outside the property
	// loop: they do not vary per case, so drawing them again would be a
	// hundred repetitions of one comparison. Reading them at all is the new
	// part — until phase 2 nothing anywhere read an account back and compared
	// it, so Closed was a write-only column and two of the five account kinds
	// were never stored. Both gaps were found by audit, not by a red test.
	requireAccountsRoundTrip(t, s, b.accounts)

	rapid.Check(t, func(rt *rapid.T) {
		resetLedger(rt, s)

		count := rapid.IntRange(1, 3).Draw(rt, "transactions")
		written := make([]ledger.Transaction, 0, count)
		lots := make([]ledger.Lot, 0, count)

		for i := 0; i < count; i++ {
			txn := drawTransaction(rt, b, i)

			var opened []ledger.Lot
			if rapid.Bool().Draw(rt, fmt.Sprintf("txn%d:opensLot", i)) {
				if lot, ok := drawLot(rt, txn, i); ok {
					opened = append(opened, lot)
				}
			}

			w := store.Write{
				ActorID:        b.actor,
				Origin:         store.OriginHuman,
				IdempotencyKey: fmt.Sprintf("property-%d", i),
				AuditID:        testID(fmt.Sprintf("prop:audit:%d", i)),
				OccurredAt:     time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
			}
			if _, err := s.SaveTransaction(ctx, w, txn, opened...); err != nil {
				rt.Fatalf("save transaction %s: %v", txn.ID(), err)
			}

			written = append(written, txn)
			lots = append(lots, opened...)
		}

		for _, txn := range written {
			back, err := s.LoadTransaction(ctx, txn.ID())
			if err != nil {
				rt.Fatalf("load transaction %s: %v", txn.ID(), err)
			}
			requireSameTransaction(rt, txn, back)
		}

		for _, lot := range lots {
			back, err := s.LoadLot(ctx, lot.ID())
			if err != nil {
				rt.Fatalf("load lot %s: %v", lot.ID(), err)
			}
			requireSameLot(rt, lot, back)

			// The lot names a posting, and that posting has to be findable and
			// has to belong to the transaction that opened the lot. A lot
			// pointing at the wrong acquisition is a wrong cost basis, found
			// out a tax year later.
			owner, err := s.PostingOwner(ctx, back.OpenedBy())
			if err != nil {
				rt.Fatalf("posting owner of %s: %v", back.OpenedBy(), err)
			}
			if !postingBelongsTo(written, owner, back.OpenedBy()) {
				rt.Fatalf("lot %s says it was opened by posting %s, which %s does not contain",
					lot.ID(), back.OpenedBy(), owner)
			}
		}

		requireSQLAndDomainAgree(rt, ctx, s, b, written)
	})
}

// requireSameTransaction proves the whole value came back, line order
// included. Order is checked positionally on purpose: the ordinal column
// exists to reproduce what the author wrote, and comparing the two slices
// element by element is the only assertion that would notice it failing.
func requireSameTransaction(rt *rapid.T, want, got ledger.Transaction) {
	if want.ID() != got.ID() {
		rt.Fatalf("identity: wrote %s, read %s", want.ID(), got.ID())
	}
	if want.Date() != got.Date() {
		rt.Fatalf("%s date: wrote %s, read %s", want.ID(), want.Date(), got.Date())
	}
	if want.Payee() != got.Payee() {
		rt.Fatalf("%s payee: wrote %q, read %q", want.ID(), want.Payee(), got.Payee())
	}
	if want.Memo() != got.Memo() {
		rt.Fatalf("%s memo: wrote %q, read %q", want.ID(), want.Memo(), got.Memo())
	}
	if want.Timezone() != got.Timezone() {
		rt.Fatalf("%s timezone: wrote %q, read %q", want.ID(), want.Timezone(), got.Timezone())
	}
	// The one field this round trip does not preserve exactly, stated as
	// precisely as it is true rather than skipped. timestamptz holds
	// microseconds; the store truncates on the way in so that the loss happens
	// at one named place, and this is the assertion that pins it there. If it
	// ever starts failing, either the column changed or something stopped
	// truncating — and either is worth knowing.
	if wantAt := want.OccurredAt().Truncate(time.Microsecond); !wantAt.Equal(got.OccurredAt()) {
		rt.Fatalf("%s occurred_at: wrote %s, read %s (expected microsecond truncation)",
			want.ID(), wantAt.Format(time.RFC3339Nano), got.OccurredAt().Format(time.RFC3339Nano))
	}

	wantPostings, gotPostings := want.Postings(), got.Postings()
	if len(wantPostings) != len(gotPostings) {
		rt.Fatalf("%s: wrote %d postings, read %d", want.ID(), len(wantPostings), len(gotPostings))
	}

	for i := range wantPostings {
		w, g := wantPostings[i], gotPostings[i]
		if w.ID() != g.ID() {
			rt.Fatalf("%s line %d: wrote posting %s, read %s in that position",
				want.ID(), i, w.ID(), g.ID())
		}
		if w.Account() != g.Account() {
			rt.Fatalf("%s posting %s account: wrote %s, read %s", want.ID(), w.ID(), w.Account(), g.Account())
		}
		if !w.Amount().Equal(g.Amount()) {
			rt.Fatalf("%s posting %s amount: wrote %s, read %s",
				want.ID(), w.ID(), w.Amount(), g.Amount())
		}
		// Exactness stated twice on purpose: Equal compares the value, and the
		// digit string catches a value that compares equal after having been
		// normalised on the way through.
		if w.Amount().Amount().String() != g.Amount().Amount().String() {
			rt.Fatalf("%s posting %s digits: wrote %s, read %s",
				want.ID(), w.ID(), w.Amount().Amount(), g.Amount().Amount())
		}
		if !w.Rate().Equal(g.Rate()) {
			rt.Fatalf("%s posting %s rate: wrote %s, read %s", want.ID(), w.ID(), w.Rate(), g.Rate())
		}
		if !w.Rate().IsZero() {
			// The database stores rates in lowest terms, and big.Rat reduces
			// on construction, so the two spellings must be identical.
			if w.Rate().Value().RatString() != g.Rate().Value().RatString() {
				rt.Fatalf("%s posting %s rate is not the same fraction: wrote %s, read %s",
					want.ID(), w.ID(), w.Rate().Value().RatString(), g.Rate().Value().RatString())
			}
		}
		if w.Memo() != g.Memo() {
			rt.Fatalf("%s posting %s memo: wrote %q, read %q", want.ID(), w.ID(), w.Memo(), g.Memo())
		}
	}
}

func requireSameLot(rt *rapid.T, want, got ledger.Lot) {
	switch {
	case want.ID() != got.ID():
		rt.Fatalf("lot identity: wrote %s, read %s", want.ID(), got.ID())
	case want.Account() != got.Account():
		rt.Fatalf("lot %s account: wrote %s, read %s", want.ID(), want.Account(), got.Account())
	case want.OpenedBy() != got.OpenedBy():
		rt.Fatalf("lot %s opened by: wrote %s, read %s", want.ID(), want.OpenedBy(), got.OpenedBy())
	case want.OpenedOn() != got.OpenedOn():
		rt.Fatalf("lot %s opened on: wrote %s, read %s", want.ID(), want.OpenedOn(), got.OpenedOn())
	case !want.Quantity().Equal(got.Quantity()):
		rt.Fatalf("lot %s quantity: wrote %s, read %s", want.ID(), want.Quantity(), got.Quantity())
	case !want.Remaining().Equal(got.Remaining()):
		rt.Fatalf("lot %s remaining: wrote %s, read %s", want.ID(), want.Remaining(), got.Remaining())
	case !want.Cost().Equal(got.Cost()):
		rt.Fatalf("lot %s cost: wrote %s, read %s", want.ID(), want.Cost(), got.Cost())
	}
}

func postingBelongsTo(written []ledger.Transaction, owner ledger.TransactionID, posting ledger.PostingID) bool {
	for _, txn := range written {
		if txn.ID() != owner {
			continue
		}
		for _, p := range txn.Postings() {
			if p.ID() == posting {
				return true
			}
		}
	}
	return false
}

// requireSQLAndDomainAgree is the assertion the whole balance decision rests
// on.
//
// Balances are computed in SQL with nothing cached, and the domain computes
// them by summing postings in memory. Those are two independent
// implementations of the same definition, and the only way to know they agree
// is to run both over the same data and compare. If they ever diverge, the SQL
// is wrong: ledger.Journal is the definition, by section 5.2.
func requireSQLAndDomainAgree(
	rt *rapid.T, ctx context.Context, s *store.Store, b propertyBooks, written []ledger.Transaction,
) {
	tree, err := s.LoadAccountTree(ctx)
	if err != nil {
		rt.Fatalf("load account tree: %v", err)
	}
	journal, err := ledger.NewJournal(tree, written...)
	if err != nil {
		rt.Fatalf("build journal from what was written: %v", err)
	}

	// A date in the middle of what was written, so an as-of balance has both
	// something to include and something to leave out.
	cutoff := written[0].Date()
	for _, txn := range written {
		if txn.Date().Before(cutoff) {
			cutoff = txn.Date()
		}
	}

	for _, account := range b.accounts {
		id := account.ID()

		wantAll, err := journal.Balance(id)
		if err != nil {
			rt.Fatalf("domain balance of %s: %v", id, err)
		}
		gotAll, err := s.Balance(ctx, id)
		if err != nil {
			rt.Fatalf("sql balance of %s: %v", id, err)
		}
		requireSameBalances(rt, wantAll, gotAll, fmt.Sprintf("balance of %s over all time", id))

		wantAsOf, err := journal.BalanceAsOf(id, cutoff)
		if err != nil {
			rt.Fatalf("domain balance of %s as of %s: %v", id, cutoff, err)
		}
		gotAsOf, err := s.BalanceAsOf(ctx, id, cutoff)
		if err != nil {
			rt.Fatalf("sql balance of %s as of %s: %v", id, cutoff, err)
		}
		requireSameBalances(rt, wantAsOf, gotAsOf, fmt.Sprintf("balance of %s as of %s", id, cutoff))

		wantSubtree, err := journal.SubtreeBalanceAsOf(id, ledger.Date{})
		if err != nil {
			rt.Fatalf("domain subtree balance of %s: %v", id, err)
		}
		gotSubtree, err := s.SubtreeBalance(ctx, id, ledger.Date{})
		if err != nil {
			rt.Fatalf("sql subtree balance of %s: %v", id, err)
		}
		requireSameBalances(rt, wantSubtree, gotSubtree, fmt.Sprintf("subtree balance of %s", id))

		// Named explicitly because it is the primitive the other two are built
		// from: SumPostings over the same lines must reach the same figure as
		// the aggregate SQL query.
		wantSum, err := ledger.SumPostings(journal.Postings(id))
		if err != nil {
			rt.Fatalf("SumPostings for %s: %v", id, err)
		}
		requireSameBalances(rt, wantSum, gotAll, fmt.Sprintf("SumPostings for %s", id))
	}

	wantTotals, err := journal.TotalsByCommodity()
	if err != nil {
		rt.Fatalf("domain totals: %v", err)
	}
	gotTotals, err := s.BookTotals(ctx)
	if err != nil {
		rt.Fatalf("sql totals: %v", err)
	}
	requireSameBalances(rt, wantTotals, gotTotals, "totals for the whole book")

	for _, total := range gotTotals {
		if !total.IsZero() {
			rt.Fatalf("the book does not net to zero: %s", total)
		}
	}
}

// requireSameBalances compares the domain's Balances against the rows SQL
// returned. Both are ordered by commodity code, so a mismatch in either the
// set or the figures shows up here.
func requireSameBalances(rt *rapid.T, want *ledger.Balances, got []ledger.Money, because string) {
	wantCodes := want.Codes()
	if len(wantCodes) != len(got) {
		rt.Fatalf("%s: domain reports %d commodities %v, sql reports %d %v",
			because, len(wantCodes), want, len(got), got)
	}
	for i, code := range wantCodes {
		expected := want.Get(code)
		actual := got[i]
		if actual.Commodity() != code {
			rt.Fatalf("%s: position %d is %s in the domain and %s in sql",
				because, i, code, actual.Commodity())
		}
		if !expected.Equal(actual) {
			rt.Fatalf("%s: %s is %s in the domain and %s in sql",
				because, code, expected, actual)
		}
		if expected.Amount().String() != actual.Amount().String() {
			rt.Fatalf("%s: %s digits differ: domain %s, sql %s",
				because, code, expected.Amount(), actual.Amount())
		}
	}
}

// A value at the top of what numeric(40,0) holds, written and read as an exact
// decimal string. The property test reaches similar magnitudes at random; this
// one pins the boundary down so a regression names itself.
func TestTheWidestAmountTheColumnHoldsSurvives(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	// Thirty-nine digits, one below the column's limit, in ETH's smallest unit
	// where the scale is 18. That is 10^21 ether expressed exactly in wei.
	const huge = "999999999999999999999999999999999999999"

	amount, err := ledger.ParseMoney("ETH", huge)
	require.NoError(t, err)
	negated, err := amount.Neg()
	require.NoError(t, err)

	debit, err := ledger.NewPosting(ledger.PostingSpec{
		ID: ledger.PostingID(testID("wide:debit")), Account: b.trading, Amount: amount,
	})
	require.NoError(t, err)
	credit, err := ledger.NewPosting(ledger.PostingSpec{
		ID: ledger.PostingID(testID("wide:credit")), Account: b.trading, Amount: negated,
	})
	require.NoError(t, err)

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:       ledger.TransactionID(testID("wide:txn")),
		Date:     mustDate(t, "2026-03-01"),
		Postings: []ledger.Posting{debit, credit},
	})
	require.NoError(t, err)

	_, err = s.SaveTransaction(ctx, b.write("widest"), txn)
	require.NoError(t, err)

	back, err := s.LoadTransaction(ctx, txn.ID())
	require.NoError(t, err)
	require.Equal(t, huge, back.Postings()[0].Amount().Amount().String(),
		"39 digits came back with every one of them intact")
	require.Equal(t, "-"+huge, back.Postings()[1].Amount().Amount().String())
}

// requireAccountsRoundTrip reads every seeded account back and compares it
// field by field.
//
// Field by field rather than with a single equality, because ledger.Account
// has unexported fields and a struct comparison would silently start passing
// if one were added — which is the same failure this whole audit was about.
func requireAccountsRoundTrip(t *testing.T, s *store.Store, written []ledger.Account) {
	t.Helper()

	back, err := s.LoadAccounts(context.Background())
	require.NoError(t, err)
	require.Len(t, back, len(written), "an account was lost between writing and reading")

	byID := map[ledger.AccountID]ledger.Account{}
	for _, a := range back {
		byID[a.ID()] = a
	}

	var closed, kinds int
	seenKinds := map[ledger.AccountKind]bool{}
	for _, want := range written {
		got, ok := byID[want.ID()]
		require.True(t, ok, "account %s did not come back", want.ID())

		require.Equal(t, want.Parent(), got.Parent(), "account %s parent", want.ID())
		require.Equal(t, want.Kind(), got.Kind(), "account %s kind", want.ID())
		require.Equal(t, want.Name(), got.Name(), "account %s name", want.ID())
		require.Equal(t, want.Commodity(), got.Commodity(), "account %s commodity", want.ID())
		require.Equal(t, want.IsClosed(), got.IsClosed(), "account %s closed", want.ID())

		if want.IsClosed() {
			closed++
		}
		if !seenKinds[want.Kind()] {
			seenKinds[want.Kind()] = true
			kinds++
		}
	}

	// The fixture is asserted, not assumed. A comparison whose input lost its
	// awkward cases passes for the wrong reason, and nothing about a green run
	// distinguishes the two (§11).
	require.Positive(t, closed, "the fixture no longer contains a closed account")
	require.Equal(t, 5, kinds, "the fixture no longer covers every account kind")
}
