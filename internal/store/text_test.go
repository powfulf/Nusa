// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
	"github.com/GaffaQ/Nusa/internal/store"
)

// The one place where what the domain accepts and what the database can hold
// come apart. Found by the round-trip property test, which is the only reason
// anyone knew: a payee containing NUL is not a value anyone would think to
// write by hand.

func TestTextWithANulByteIsRefusedByName(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	build := func(t *testing.T, label, payee, memo, postingMemo string) ledger.Transaction {
		t.Helper()
		debit, err := ledger.NewPosting(ledger.PostingSpec{
			ID:      ledger.PostingID(testID("nul:debit:" + label)),
			Account: b.groceries,
			Amount:  mustMoney(t, "IDR", 1000),
			Memo:    postingMemo,
		})
		require.NoError(t, err)
		credit, err := ledger.NewPosting(ledger.PostingSpec{
			ID:      ledger.PostingID(testID("nul:credit:" + label)),
			Account: b.cash,
			Amount:  mustMoney(t, "IDR", -1000),
		})
		require.NoError(t, err)
		txn, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID:       ledger.TransactionID(testID("nul:txn:" + label)),
			Date:     mustDate(t, "2026-03-01"),
			Payee:    payee,
			Memo:     memo,
			Postings: []ledger.Posting{debit, credit},
		})
		require.NoError(t, err, "the domain builds it happily; that is the point")
		return txn
	}

	for _, tc := range []struct {
		name, wants              string
		payee, memo, postingMemo string
	}{
		{name: "payee", wants: "payee", payee: "Waru\x00ng"},
		{name: "transaction memo", wants: "memo", memo: "weekly\x00shop"},
		{name: "posting memo", wants: "posting", postingMemo: "line\x00note"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			txn := build(t, tc.name, tc.payee, tc.memo, tc.postingMemo)

			_, err := s.SaveTransaction(ctx, b.write("nul-"+tc.name), txn)
			require.ErrorIs(t, err, store.ErrInvalidWrite)
			require.Contains(t, err.Error(), "NUL byte",
				"the error names the problem, not SQLSTATE 22021")
			require.Contains(t, err.Error(), tc.wants,
				"the error names the field")

			count, err := s.CountTransactions(ctx)
			require.NoError(t, err)
			require.EqualValues(t, 0, count, "nothing was written")
		})
	}
}

func TestAnAccountNameWithANulByteIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	account, err := ledger.NewAccount(ledger.AccountSpec{
		ID:   ledger.AccountID(testID("nul:account")),
		Kind: ledger.AccountAsset,
		Name: "Ca\x00sh",
	})
	require.NoError(t, err)

	err = s.SaveAccount(ctx, account)
	require.ErrorIs(t, err, store.ErrInvalidWrite)
	require.Contains(t, err.Error(), "NUL byte")
}

// Everything else awkward survives. This is the other half of the claim: only
// one code point is refused, and the refusal is not a licence to sanitise
// anything else a user typed.
func TestAwkwardTextThatIsNotNulSurvivesUntouched(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	for i, payee := range []string{
		"a\x01b",      // a control character that is not NUL
		"line\nbreak", // a newline
		"Warung 🙂",    // outside the basic plane
		"áb",         // a combining acute
		"\u202eabc",   // a right-to-left override
		"  padded  ",  // leading and trailing space, which is not ours to trim
		"'; DROP TABLE postings; --",
	} {
		label := payee
		t.Run(label, func(t *testing.T) {
			debit, err := ledger.NewPosting(ledger.PostingSpec{
				ID:      ledger.PostingID(testID("ok:debit:" + label)),
				Account: b.groceries,
				Amount:  mustMoney(t, "IDR", 1000),
			})
			require.NoError(t, err)
			credit, err := ledger.NewPosting(ledger.PostingSpec{
				ID:      ledger.PostingID(testID("ok:credit:" + label)),
				Account: b.cash,
				Amount:  mustMoney(t, "IDR", -1000),
			})
			require.NoError(t, err)
			txn, err := ledger.NewTransaction(ledger.TransactionSpec{
				ID:       ledger.TransactionID(testID("ok:txn:" + label)),
				Date:     mustDate(t, "2026-03-01"),
				Payee:    payee,
				Postings: []ledger.Posting{debit, credit},
			})
			require.NoError(t, err)

			_, err = s.SaveTransaction(ctx, b.write("ok-"+label), txn)
			require.NoError(t, err)

			back, err := s.LoadTransaction(ctx, txn.ID())
			require.NoError(t, err)
			require.Equal(t, payee, back.Payee(),
				"case %d came back changed", i)
		})
	}
}
