// SPDX-License-Identifier: MIT

package ledger_test

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// original returns a two-line transaction and the map naming the identities
// its reversal's lines will carry.
func original(t *testing.T) (ledger.Transaction, map[ledger.PostingID]ledger.PostingID) {
	t.Helper()
	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:    tid("original"),
		Date:  mustDate(t, "2026-03-01"),
		Payee: "Warung Bu Eka",
		Memo:  "weekly shop",
		Postings: []ledger.Posting{
			mustPosting(t, "original-a", "bank", mustMoney(t, idr, -150_000_00)),
			mustPosting(t, "original-b", "groceries", mustMoney(t, idr, 150_000_00)),
		},
	})
	require.NoError(t, err)
	return txn, map[ledger.PostingID]ledger.PostingID{
		pid("original-a"): pid("reversing-a"),
		pid("original-b"): pid("reversing-b"),
	}
}

func TestReverseNegatesEveryLineAndStillBalances(t *testing.T) {
	t.Parallel()
	source, lines := original(t)

	reversal, err := ledger.Reverse(source, ledger.ReversalSpec{
		ID:       tid("reversal"),
		Kind:     ledger.Correction,
		Date:     mustDate(t, "2026-04-02"),
		Memo:     "wrong amount, see receipt",
		Postings: lines,
	})
	require.NoError(t, err)

	require.True(t, reversal.IsReversal())
	require.Equal(t, source.ID(), reversal.Reverses())
	require.Equal(t, ledger.Correction, reversal.ReversalKind())
	require.Equal(t, mustDate(t, "2026-04-02"), reversal.Date(),
		"a reversal is booked on the date the caller named, never the original's")
	require.Equal(t, source.Payee(), reversal.Payee(),
		"the counterparty did not change because the entry was wrong")
	require.Equal(t, "wrong amount, see receipt", reversal.Memo())

	// Every line answers exactly one of the original's, by identity rather
	// than by position.
	byReversed := reversedBy(reversal)
	require.Len(t, byReversed, len(source.Postings()))

	for _, line := range source.Postings() {
		answer, ok := byReversed[line.ID()]
		require.True(t, ok, "no line answers posting %s", line.ID())
		require.Equal(t, lines[line.ID()], answer.ID())
		require.Equal(t, line.Account(), answer.Account())
		require.Equal(t, line.Memo(), answer.Memo())

		negated, err := line.Amount().Neg()
		require.NoError(t, err)
		requireMoney(t, negated, answer.Amount(), "the reversing line is the original negated")
	}

	// The pair together moves nothing, which is what makes it a reversal.
	sums, err := ledger.SumPostings(append(source.Postings(), reversal.Postings()...))
	require.NoError(t, err)
	require.True(t, sums.IsZero(), "an entry and its reversal must cancel exactly, got %s", sums)
}

// §5.4. The rate is evidence of what a conversion was worth at the moment it
// happened. A reversal that priced itself at today's rate would leave the pair
// failing to cancel by however far the rate had moved — a gain nobody booked.
func TestReversalCarriesTheOriginalRateRatherThanTodays(t *testing.T) {
	t.Parallel()

	// 1 USD = 15.000 IDR on the day, against 17.000 IDR two years later.
	then := mustRate(t, usd, idr, 15_000, 1)

	sold, err := ledger.NewPosting(ledger.PostingSpec{
		ID: pid("fx-sold"), Account: aid("wallet-usd"),
		Amount: mustMoney(t, usd, -100_00), Rate: then,
	})
	require.NoError(t, err)
	bought, err := ledger.NewPosting(ledger.PostingSpec{
		ID: pid("fx-bought"), Account: aid("wallet-usd"),
		Amount: mustMoney(t, usd, 100_00), Rate: then,
	})
	require.NoError(t, err)

	source, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID: tid("fx"), Date: mustDate(t, "2024-06-01"),
		Postings: []ledger.Posting{sold, bought},
	})
	require.NoError(t, err)

	reversal, err := ledger.Reverse(source, ledger.ReversalSpec{
		ID: tid("fx-reversal"), Kind: ledger.Correction,
		Date: mustDate(t, "2026-08-21"),
		Postings: map[ledger.PostingID]ledger.PostingID{
			pid("fx-sold"):   pid("fx-sold-reversed"),
			pid("fx-bought"): pid("fx-bought-reversed"),
		},
	})
	require.NoError(t, err)

	today := mustRate(t, usd, idr, 17_000, 1)
	for _, p := range reversal.Postings() {
		require.True(t, p.Rate().Equal(then),
			"the reversing line must carry the rate recorded on the original, got %s", p.Rate())
		require.False(t, p.Rate().Equal(today),
			"nothing in a reversal may consult a rate from the day it was written")
	}
}

func TestDeletionIsAReversalAndChangesNothingAboutTheOriginal(t *testing.T) {
	t.Parallel()
	source, lines := original(t)

	before := source.String()
	tombstone, err := ledger.Reverse(source, ledger.ReversalSpec{
		ID: tid("tombstone"), Kind: ledger.Deletion,
		Date: mustDate(t, "2026-04-02"), Postings: lines,
	})
	require.NoError(t, err)

	require.Equal(t, ledger.Deletion, tombstone.ReversalKind())
	require.Equal(t, "deletion", tombstone.ReversalKind().String())
	require.Equal(t, before, source.String(),
		"a deletion is an append; the transaction it tombstones is untouched")
	require.False(t, source.IsReversal())
	require.Empty(t, source.Reverses())
}

func TestReverseRefusesWhatItCannotDescribe(t *testing.T) {
	t.Parallel()
	source, lines := original(t)

	t.Run("no kind", func(t *testing.T) {
		t.Parallel()
		_, err := ledger.Reverse(source, ledger.ReversalSpec{
			ID: tid("r1"), Date: mustDate(t, "2026-04-02"), Postings: lines,
		})
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})

	t.Run("a line is missing", func(t *testing.T) {
		t.Parallel()
		short := map[ledger.PostingID]ledger.PostingID{pid("original-a"): pid("reversing-a")}
		_, err := ledger.Reverse(source, ledger.ReversalSpec{
			ID: tid("r2"), Kind: ledger.Correction,
			Date: mustDate(t, "2026-04-02"), Postings: short,
		})
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})

	t.Run("a line is spare", func(t *testing.T) {
		t.Parallel()
		spare := map[ledger.PostingID]ledger.PostingID{
			pid("original-a"): pid("reversing-a"),
			pid("original-b"): pid("reversing-b"),
			pid("stranger"):   pid("reversing-c"),
		}
		_, err := ledger.Reverse(source, ledger.ReversalSpec{
			ID: tid("r3"), Kind: ledger.Correction,
			Date: mustDate(t, "2026-04-02"), Postings: spare,
		})
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})

	t.Run("a line names a posting the original does not have", func(t *testing.T) {
		t.Parallel()
		wrong := map[ledger.PostingID]ledger.PostingID{
			pid("original-a"): pid("reversing-a"),
			pid("stranger"):   pid("reversing-b"),
		}
		_, err := ledger.Reverse(source, ledger.ReversalSpec{
			ID: tid("r4"), Kind: ledger.Correction,
			Date: mustDate(t, "2026-04-02"), Postings: wrong,
		})
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})

	t.Run("two lines share an identity", func(t *testing.T) {
		t.Parallel()
		clashing := map[ledger.PostingID]ledger.PostingID{
			pid("original-a"): pid("reversing-a"),
			pid("original-b"): pid("reversing-a"),
		}
		_, err := ledger.Reverse(source, ledger.ReversalSpec{
			ID: tid("r5"), Kind: ledger.Correction,
			Date: mustDate(t, "2026-04-02"), Postings: clashing,
		})
		require.ErrorIs(t, err, ledger.ErrDuplicateID)
	})

	t.Run("nothing to reverse", func(t *testing.T) {
		t.Parallel()
		_, err := ledger.Reverse(ledger.Transaction{}, ledger.ReversalSpec{
			ID: tid("r6"), Kind: ledger.Correction, Date: mustDate(t, "2026-04-02"),
		})
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})

	t.Run("no date", func(t *testing.T) {
		t.Parallel()
		_, err := ledger.Reverse(source, ledger.ReversalSpec{
			ID: tid("r7"), Kind: ledger.Correction, Postings: lines,
		})
		require.ErrorIs(t, err, ledger.ErrInvalidTransaction,
			"the date is the caller's decision and the domain will not guess it")
	})
}

// Half a link is worse than none, and NewTransaction is the door every caller
// comes through — including a store reading a row back.
func TestATransactionEitherReversesSomethingCompletelyOrNotAtAll(t *testing.T) {
	t.Parallel()

	postings := []ledger.Posting{
		mustPosting(t, "half-a", "bank", mustMoney(t, idr, 100_00)),
		mustPosting(t, "half-b", "groceries", mustMoney(t, idr, -100_00)),
	}
	base := ledger.TransactionSpec{
		ID: tid("half"), Date: mustDate(t, "2026-03-01"), Postings: postings,
	}

	t.Run("a kind with nothing to reverse", func(t *testing.T) {
		t.Parallel()
		spec := base
		spec.ReversalKind = ledger.Correction
		_, err := ledger.NewTransaction(spec)
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})

	t.Run("a link with no kind", func(t *testing.T) {
		t.Parallel()
		spec := base
		spec.Reverses = tid("original")
		_, err := ledger.NewTransaction(spec)
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})

	t.Run("a transaction reversing itself", func(t *testing.T) {
		t.Parallel()
		spec := base
		spec.Reverses = spec.ID
		spec.ReversalKind = ledger.Deletion
		_, err := ledger.NewTransaction(spec)
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})

	t.Run("a link that is not an identity", func(t *testing.T) {
		t.Parallel()
		spec := base
		spec.Reverses = "txn-1"
		spec.ReversalKind = ledger.Correction
		_, err := ledger.NewTransaction(spec)
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})

	t.Run("neither, which is every ordinary transaction", func(t *testing.T) {
		t.Parallel()
		txn, err := ledger.NewTransaction(base)
		require.NoError(t, err)
		require.False(t, txn.IsReversal())
		require.Equal(t, ledger.NotReversal, txn.ReversalKind())
	})
}

func TestAPostingEitherReversesSomethingOrNotAtAll(t *testing.T) {
	t.Parallel()

	t.Run("a posting reversing itself", func(t *testing.T) {
		t.Parallel()
		_, err := ledger.NewPosting(ledger.PostingSpec{
			ID: pid("self"), Account: aid("bank"),
			Amount: mustMoney(t, idr, 100_00), Reverses: pid("self"),
		})
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})

	t.Run("a link that is not an identity", func(t *testing.T) {
		t.Parallel()
		_, err := ledger.NewPosting(ledger.PostingSpec{
			ID: pid("bad-link"), Account: aid("bank"),
			Amount: mustMoney(t, idr, 100_00), Reverses: "posting-1",
		})
		require.ErrorIs(t, err, ledger.ErrInvalidReversal)
	})
}

// A reversal can itself be reversed. Undoing a deletion is a real thing a
// person does, and refusing it would be a policy the domain has no business
// inventing — the chain stays append-only either way. The database's UNIQUE on
// reverses_id still stops the same transaction being reversed twice.
func TestAReversalCanItselfBeReversed(t *testing.T) {
	t.Parallel()
	source, lines := original(t)

	tombstone, err := ledger.Reverse(source, ledger.ReversalSpec{
		ID: tid("tomb"), Kind: ledger.Deletion,
		Date: mustDate(t, "2026-04-02"), Postings: lines,
	})
	require.NoError(t, err)

	undo := make(map[ledger.PostingID]ledger.PostingID, len(tombstone.Postings()))
	for i, p := range tombstone.Postings() {
		undo[p.ID()] = pid(fmt.Sprintf("undo-%d", i))
	}
	restored, err := ledger.Reverse(tombstone, ledger.ReversalSpec{
		ID: tid("undo"), Kind: ledger.Correction,
		Date: mustDate(t, "2026-04-03"), Postings: undo,
	})
	require.NoError(t, err)
	require.Equal(t, tombstone.ID(), restored.Reverses())

	// Undoing the undo leaves the original's effect standing.
	sums, err := ledger.SumPostings(append(tombstone.Postings(), restored.Postings()...))
	require.NoError(t, err)
	require.True(t, sums.IsZero())
}

func TestReversalKindRoundTripsThroughItsDatabaseSpelling(t *testing.T) {
	t.Parallel()

	for _, kind := range []ledger.ReversalKind{ledger.NotReversal, ledger.Correction, ledger.Deletion} {
		parsed, err := ledger.ParseReversalKind(kind.String())
		require.NoError(t, err)
		require.Equal(t, kind, parsed)
	}

	_, err := ledger.ParseReversalKind("removal")
	require.ErrorIs(t, err, ledger.ErrInvalidReversal)

	require.False(t, ledger.NotReversal.IsValid())
	require.True(t, ledger.Correction.IsValid())
	require.True(t, ledger.Deletion.IsValid())
}

// §5.1 and §5.3 together: whatever the original was, its reversal balances and
// the two of them cancel exactly. Negation is exact on a big.Int, so this holds
// for every amount rather than for the ones a test author thought of.
func TestPropertyAReversalAlwaysCancelsWhatItReverses(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		source := withDrawnRates(rt, drawTransaction(rt, "source"))

		lines := make(map[ledger.PostingID]ledger.PostingID, len(source.Postings()))
		for i, p := range source.Postings() {
			lines[p.ID()] = pid(fmt.Sprintf("reversing-%d", i))
		}

		kind := rapid.SampledFrom([]ledger.ReversalKind{ledger.Correction, ledger.Deletion}).Draw(rt, "kind")
		reversal, err := ledger.Reverse(source, ledger.ReversalSpec{
			ID:       tid("reversal"),
			Kind:     kind,
			Date:     drawDate(rt, "reversal.date"),
			Postings: lines,
		})
		if err != nil {
			rt.Fatalf("reverse: %v", err)
		}

		// It is a transaction in its own right, so it balances on its own.
		own, err := ledger.SumPostings(reversal.Postings())
		if err != nil {
			rt.Fatalf("sum reversal: %v", err)
		}
		if !own.IsZero() {
			rt.Fatalf("a reversal must balance on its own, got %s", own)
		}

		// And together the pair moves nothing, in every commodity involved.
		both, err := ledger.SumPostings(append(source.Postings(), reversal.Postings()...))
		if err != nil {
			rt.Fatalf("sum pair: %v", err)
		}
		if !both.IsZero() {
			rt.Fatalf("an entry and its reversal must cancel, got %s", both)
		}

		// Each account is left exactly where it started.
		for _, id := range propertyAccountIDs {
			lines := append(source.PostingsFor(id), reversal.PostingsFor(id)...)
			after, err := ledger.SumPostings(lines)
			if err != nil {
				rt.Fatalf("sum account: %v", err)
			}
			if !after.IsZero() {
				rt.Fatalf("account %s was left holding %s after its entry was reversed", id, after)
			}
		}

		// The rate on every line is the one that was recorded, untouched.
		byReversed := reversedBy(reversal)
		for _, p := range source.Postings() {
			answer, ok := byReversed[p.ID()]
			if !ok {
				rt.Fatalf("no line answers posting %s", p.ID())
			}
			if !answer.Rate().Equal(p.Rate()) {
				rt.Fatalf("posting %s carried rate %s, its reversal carries %s",
					p.ID(), p.Rate(), answer.Rate())
			}
		}
	})
}

// Guards against a reversal being built from a positional read of the
// original's lines. Reordering is routine and silent, so a builder that keyed
// on position would attach the wrong line to the wrong original with no error
// anywhere — which is exactly the failure §5.7 exists to prevent.
func TestPropertyReversalIsUnaffectedByThePostingOrderItWasGiven(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		source := drawTransaction(rt, "source")
		postings := source.Postings()

		lines := make(map[ledger.PostingID]ledger.PostingID, len(postings))
		for i, p := range postings {
			lines[p.ID()] = pid(fmt.Sprintf("reversing-%d", i))
		}

		order := rapid.Permutation(postings).Draw(rt, "order")
		shuffled, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID: source.ID(), Date: source.Date(), Postings: order,
		})
		if err != nil {
			rt.Fatalf("rebuild in a different order: %v", err)
		}

		spec := ledger.ReversalSpec{
			ID: tid("reversal"), Kind: ledger.Correction,
			Date: source.Date(), Postings: lines,
		}
		fromOriginal, err := ledger.Reverse(source, spec)
		if err != nil {
			rt.Fatalf("reverse original: %v", err)
		}
		fromShuffled, err := ledger.Reverse(shuffled, spec)
		if err != nil {
			rt.Fatalf("reverse shuffled: %v", err)
		}

		// Compare by what each line answers, not by position: the two
		// reversals legitimately list their lines in different orders.
		want := reversedBy(fromOriginal)
		got := reversedBy(fromShuffled)
		if len(want) != len(got) {
			rt.Fatalf("reversals have %d and %d lines", len(want), len(got))
		}
		for id, a := range want {
			b, ok := got[id]
			if !ok {
				rt.Fatalf("no line answers posting %s after reordering", id)
			}
			if a.ID() != b.ID() || a.Account() != b.Account() || !a.Amount().Equal(b.Amount()) {
				rt.Fatalf("posting %s is answered by %s in one order and %s in another", id, a, b)
			}
		}
	})
}

// withDrawnRates rebuilds a transaction with an exchange rate on every line.
//
// The shared generator produces no rates, which left the rate assertion in the
// property test above passing over nothing at all: deleting the line that
// carries the rate through Reverse failed only the single hand-written test.
// A property that never sees the value it asserts about is not checking it.
//
// The rates are exact fractions with awkward denominators on purpose. §5.4
// says a rate is recorded, not recomputed, and a rate that survives a round
// trip only because it happened to be a whole number proves nothing.
func withDrawnRates(rt *rapid.T, txn ledger.Transaction) ledger.Transaction {
	postings := txn.Postings()
	rated := make([]ledger.Posting, 0, len(postings))

	for i, p := range postings {
		label := fmt.Sprintf("rate.%d", i)
		num := rapid.Int64Range(1, 1_000_000).Draw(rt, label+".num")
		den := rapid.Int64Range(1, 999).Draw(rt, label+".den")

		// A rate prices its own posting's commodity against another one.
		quote := idr
		if p.Amount().Commodity() == idr {
			quote = usd
		}
		rate, err := ledger.NewRate(p.Amount().Commodity(), quote, big.NewRat(num, den))
		if err != nil {
			rt.Fatalf("build rate: %v", err)
		}

		next, err := ledger.NewPosting(ledger.PostingSpec{
			ID: p.ID(), Account: p.Account(), Amount: p.Amount(),
			Rate: rate, Memo: p.Memo(),
		})
		if err != nil {
			rt.Fatalf("rebuild posting with a rate: %v", err)
		}
		rated = append(rated, next)
	}

	out, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID: txn.ID(), Date: txn.Date(), Payee: txn.Payee(), Memo: txn.Memo(),
		Postings: rated,
	})
	if err != nil {
		rt.Fatalf("rebuild transaction with rates: %v", err)
	}
	return out
}

// reversedBy indexes a reversal's lines by the line each one undoes.
func reversedBy(txn ledger.Transaction) map[ledger.PostingID]ledger.Posting {
	out := make(map[ledger.PostingID]ledger.Posting, len(txn.Postings()))
	for _, p := range txn.Postings() {
		out[p.Reverses()] = p
	}
	return out
}
