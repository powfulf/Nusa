// SPDX-License-Identifier: MIT

package ledger_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/ledger"
)

// The rule this file guards arrived from the other direction. The store's
// round-trip property test generated a payee containing U+0000 and PostgreSQL
// refused the write with SQLSTATE 22021, an error naming neither the field nor
// the fix. That was a domain rule the database noticed first: a ledger entry
// has to survive being written down, transported and read back, and NUL
// survives none of that. The store still refuses it too, deliberately.

func TestNulIsRefusedInEveryUserSuppliedString(t *testing.T) {
	t.Parallel()

	postings := []ledger.Posting{
		mustPosting(t, "nul-a", "bank", mustMoney(t, idr, 100_00)),
		mustPosting(t, "nul-b", "groceries", mustMoney(t, idr, -100_00)),
	}

	t.Run("transaction payee", func(t *testing.T) {
		t.Parallel()
		_, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID: tid("nul-payee"), Date: mustDate(t, "2026-03-01"),
			Payee: "Waru\x00ng", Postings: postings,
		})
		requireNulRefused(t, err, "payee")
	})

	t.Run("transaction memo", func(t *testing.T) {
		t.Parallel()
		_, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID: tid("nul-memo"), Date: mustDate(t, "2026-03-01"),
			Memo: "weekly\x00shop", Postings: postings,
		})
		requireNulRefused(t, err, "memo")
	})

	t.Run("transaction timezone", func(t *testing.T) {
		t.Parallel()
		_, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID: tid("nul-zone"), Date: mustDate(t, "2026-03-01"),
			Timezone: "Asia/\x00Jakarta", Postings: postings,
		})
		requireNulRefused(t, err, "timezone")
	})

	t.Run("posting memo", func(t *testing.T) {
		t.Parallel()
		_, err := ledger.NewPosting(ledger.PostingSpec{
			ID: pid("nul-line"), Account: aid("bank"),
			Amount: mustMoney(t, idr, 100_00), Memo: "line\x00note",
		})
		requireNulRefused(t, err, "memo")
	})

	t.Run("account name", func(t *testing.T) {
		t.Parallel()
		_, err := ledger.NewAccount(ledger.AccountSpec{
			ID: aid("nul-account"), Kind: ledger.AccountAsset, Name: "Ca\x00sh",
		})
		requireNulRefused(t, err, "name")
	})
}

func requireNulRefused(t *testing.T, err error, field string) {
	t.Helper()
	require.ErrorIs(t, err, ledger.ErrInvalidText)
	require.Contains(t, err.Error(), "NUL byte",
		"the error must name what is wrong; %q does not", err)
	require.Contains(t, err.Error(), field,
		"the error must name the field; %q does not", err)
}

// Every other awkward string a person can type is user data and is stored
// exactly as typed. The rule is about one code point, not about tidiness: an
// account called "Ca$h 💰" or a memo written right-to-left is not a problem to
// be normalised away.
func TestEveryOtherAwkwardStringSurvivesUntouched(t *testing.T) {
	t.Parallel()

	// Written from code points rather than pasted, so the source file itself
	// does not carry invisible direction-flipping characters that reorder the
	// rest of this function for whoever reads it next.
	bidi := string(rune(0x202E)) + "evil" + string(rune(0x202C))

	awkward := []string{
		"a\x01b",         // a control character that is not NUL
		"line\nbreak",    // a newline
		"tab\tseparated", // a tab
		"Warung Bu Éka",  // combining marks
		"nasi goreng 🍛",  // astral-plane emoji
		bidi,             // right-to-left override and pop
		"  leading and trailing  ",
		"",
	}

	for _, s := range awkward {
		s := s
		t.Run(s, func(t *testing.T) {
			t.Parallel()

			posting, err := ledger.NewPosting(ledger.PostingSpec{
				ID: pid("awkward-a"), Account: aid("bank"),
				Amount: mustMoney(t, idr, 100_00), Memo: s,
			})
			require.NoError(t, err)
			require.Equal(t, s, posting.Memo(), "memo must be stored exactly as typed")

			txn, err := ledger.NewTransaction(ledger.TransactionSpec{
				ID: tid("awkward"), Date: mustDate(t, "2026-03-01"),
				Payee: s, Memo: s, Timezone: s,
				Postings: []ledger.Posting{
					posting,
					mustPosting(t, "awkward-b", "groceries", mustMoney(t, idr, -100_00)),
				},
			})
			require.NoError(t, err)
			require.Equal(t, s, txn.Payee())
			require.Equal(t, s, txn.Memo())
			require.Equal(t, s, txn.Timezone())

			if s != "" {
				account, err := ledger.NewAccount(ledger.AccountSpec{
					ID: aid("awkward-account"), Kind: ledger.AccountAsset, Name: s,
				})
				require.NoError(t, err)
				require.Equal(t, s, account.Name())
			}
		})
	}
}
