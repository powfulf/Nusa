// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/ledger"
)

// NUL, and the three layers that refuse it.
//
// It was found here, by the round-trip property test, which is the only reason
// anyone knew: a payee containing U+0000 is not a value a reviewer would ever
// have written by hand. PostgreSQL refused it with SQLSTATE 22021, an error
// naming neither the field nor the fix.
//
// It was treated as a storage limitation at the time. It is not one. A ledger
// entry has to survive being written down, sent over a wire, exported and read
// back, and NUL survives none of those — it terminates C strings, is invalid in
// JSON, and is refused in filenames and header values. So the rule now lives in
// internal/ledger, and these tests describe what each layer below it still does.

// The domain refuses it, so nothing carrying a NUL is ever built, let alone
// written. This is the layer that actually fires in practice.
func TestNulIsRefusedBeforeAnythingCanBeBuilt(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	t.Run("payee", func(t *testing.T) {
		debit, err := ledger.NewPosting(ledger.PostingSpec{
			ID:      ledger.PostingID(testID("nul:debit")),
			Account: b.groceries,
			Amount:  mustMoney(t, "IDR", 1000),
		})
		require.NoError(t, err)
		credit, err := ledger.NewPosting(ledger.PostingSpec{
			ID:      ledger.PostingID(testID("nul:credit")),
			Account: b.cash,
			Amount:  mustMoney(t, "IDR", -1000),
		})
		require.NoError(t, err)

		_, err = ledger.NewTransaction(ledger.TransactionSpec{
			ID:       ledger.TransactionID(testID("nul:txn")),
			Date:     mustDate(t, "2026-03-01"),
			Payee:    "Waru\x00ng",
			Postings: []ledger.Posting{debit, credit},
		})
		require.ErrorIs(t, err, ledger.ErrInvalidText,
			"the transaction cannot be built at all, so the store never sees it")
		require.Contains(t, err.Error(), "NUL byte")
		require.Contains(t, err.Error(), "payee", "the error names the field")
	})

	t.Run("posting memo", func(t *testing.T) {
		_, err := ledger.NewPosting(ledger.PostingSpec{
			ID:      ledger.PostingID(testID("nul:line")),
			Account: b.groceries,
			Amount:  mustMoney(t, "IDR", 1000),
			Memo:    "line\x00note",
		})
		require.ErrorIs(t, err, ledger.ErrInvalidText)
		require.Contains(t, err.Error(), "NUL byte")
	})

	t.Run("account name", func(t *testing.T) {
		_, err := ledger.NewAccount(ledger.AccountSpec{
			ID:   ledger.AccountID(testID("nul:account")),
			Kind: ledger.AccountAsset,
			Name: "Ca\x00sh",
		})
		require.ErrorIs(t, err, ledger.ErrInvalidText)
		require.Contains(t, err.Error(), "NUL byte")
	})

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, count, "nothing was written")
}

// And the database refuses it independently, for a writer that never goes near
// Go at all — the importer, the rule engine, psql.
//
// This is the layer that corresponds to the deferred balance trigger: it holds
// when the domain is bypassed entirely. The Go-level check in the store sits
// *behind* the domain constructors and therefore cannot be reached today; it is
// kept as a guard against the domain rule being weakened, which is a different
// and smaller claim. This is the one that cannot be bypassed.
func TestPostgresRefusesNulWhenTheDomainIsBypassedEntirely(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_ = seedBooks(t, s)

	_, err := s.Pool().Exec(ctx,
		`INSERT INTO transactions (id, txn_date, payee) VALUES ($1, $2, $3)`,
		testID("txn:nul:direct"), "2026-03-01", "Waru\x00ng")
	require.Error(t, err, "a text column cannot hold NUL, whoever is writing")

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "22021", pgErr.Code,
		"character_not_in_repertoire, which names neither the field nor the fix — "+
			"which is exactly why the domain refuses it first")

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, count)
}

// Everything else awkward survives. This is the other half of the claim: only
// one code point is refused, and the refusal is not a licence to sanitise
// anything else a user typed.
func TestAwkwardTextThatIsNotNulSurvivesUntouched(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := seedBooks(t, s)

	// Built from its code point rather than pasted, so this source file does
	// not itself carry a character that reorders the lines below it.
	rtlOverride := string(rune(0x202E)) + "abc"

	for i, payee := range []string{
		"a\x01b",      // a control character that is not NUL
		"line\nbreak", // a newline
		"Warung 🙂",    // outside the basic plane
		"áb",          // a combining acute
		rtlOverride,   // a right-to-left override
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
