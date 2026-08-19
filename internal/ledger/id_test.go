// SPDX-License-Identifier: MIT

package ledger_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

func TestValidateIDAcceptsAUUIDv7(t *testing.T) {
	t.Parallel()

	require.NoError(t, ledger.ValidateID("018f4c7a-9b21-7c3e-8f0a-1d2e3f4a5b6c"))
	require.NoError(t, ledger.ValidateID(testUUID("anything")))
}

func TestValidateIDRejectsEverythingElse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"a readable slug", "bank"},
		{"too short", "018f4c7a-9b21-7c3e-8f0a-1d2e3f4a5b6"},
		{"too long", "018f4c7a-9b21-7c3e-8f0a-1d2e3f4a5b6cd"},
		{"no hyphens", "018f4c7a9b217c3e8f0a1d2e3f4a5b6cabcd"},
		{"hyphens in the wrong places", "018f4c7a9-b21-7c3e-8f0a-1d2e3f4a5b6c"},
		{"a non-hex character", "018f4c7a-9b21-7c3e-8f0a-1d2e3f4a5bZc"},

		// A version 4 UUID is perfectly valid as a UUID and still refused:
		// §5.6 asks for v7 so that identities sort by creation time.
		{"a version 4 uuid", "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
		{"the nil uuid", "00000000-0000-0000-0000-000000000000"},
		{"the right version but the wrong variant", "018f4c7a-9b21-7c3e-0f0a-1d2e3f4a5b6c"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, ledger.ValidateID(tc.id), ledger.ErrInvalidID, "should have rejected %q", tc.id)
		})
	}
}

// Uniqueness here is by exact string comparison, so one identity arriving in
// two spellings would look like two identities. Uppercase is refused rather
// than folded, because folding would mean every comparison in the package had
// to remember to fold too.
func TestValidateIDRejectsUppercase(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, ledger.ValidateID("018F4C7A-9B21-7C3E-8F0A-1D2E3F4A5B6C"), ledger.ErrInvalidID)
}

// Every constructor that takes an identity applies the same rule, and reports
// both the entity that failed and why, so a caller can match on either.
func TestConstructorsRejectMalformedIdentities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		build      func() error
		wantEntity error
	}{
		{
			"account",
			func() error {
				_, err := ledger.NewAccount(ledger.AccountSpec{
					ID: "bank", Kind: ledger.AccountAsset, Name: "Bank",
				})
				return err
			},
			ledger.ErrInvalidAccount,
		},
		{
			"account parent",
			func() error {
				_, err := ledger.NewAccount(ledger.AccountSpec{
					ID: aid("bank"), Parent: "assets", Kind: ledger.AccountAsset, Name: "Bank",
				})
				return err
			},
			ledger.ErrInvalidAccount,
		},
		{
			"posting",
			func() error {
				_, err := ledger.NewPosting(ledger.PostingSpec{
					ID: "p1", Account: aid("bank"), Amount: mustMoney(t, idr, 1),
				})
				return err
			},
			ledger.ErrInvalidTransaction,
		},
		{
			"posting account",
			func() error {
				_, err := ledger.NewPosting(ledger.PostingSpec{
					ID: pid("p1"), Account: "bank", Amount: mustMoney(t, idr, 1),
				})
				return err
			},
			ledger.ErrInvalidTransaction,
		},
		{
			"transaction",
			func() error {
				spec := groceriesSpec(t)
				spec.ID = "txn-1"
				_, err := ledger.NewTransaction(spec)
				return err
			},
			ledger.ErrInvalidTransaction,
		},
		{
			"lot",
			func() error {
				_, err := ledger.NewLot(ledger.LotSpec{
					ID:       "lot-1",
					Account:  aid("shares"),
					OpenedBy: pid("opened"),
					OpenedOn: mustDate(t, "2024-03-17"),
					Quantity: mustMoney(t, bbca, 100),
					Cost:     mustMoney(t, idr, 1_000),
				})
				return err
			},
			ledger.ErrInvalidLot,
		},
		{
			"lot opening posting",
			func() error {
				_, err := ledger.NewLot(ledger.LotSpec{
					ID:       lid("lot-1"),
					Account:  aid("shares"),
					OpenedBy: "txn-1",
					OpenedOn: mustDate(t, "2024-03-17"),
					Quantity: mustMoney(t, bbca, 100),
					Cost:     mustMoney(t, idr, 1_000),
				})
				return err
			},
			ledger.ErrInvalidLot,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.build()
			require.ErrorIs(t, err, tc.wantEntity, "should say which entity was being built")
			require.ErrorIs(t, err, ledger.ErrInvalidID, "should say the identity was the problem")
		})
	}
}

// A journal resolves a posting by identity wherever it sits, which is what a
// lot, an audit entry or a reversing entry needs in order to point at one.
func TestJournalResolvesPostingsByIdentity(t *testing.T) {
	t.Parallel()

	j := marchJournal(t)

	posting, ok := j.Posting(pid("txn-2-a"))
	require.True(t, ok)
	require.Equal(t, aid("bank"), posting.Account())
	requireMoney(t, mustMoney(t, idr, -15_000_000), posting.Amount())

	owner, ok := j.PostingOwner(pid("txn-2-a"))
	require.True(t, ok)
	require.Equal(t, tid("txn-2"), owner)

	_, ok = j.Posting(pid("not-in-the-journal"))
	require.False(t, ok)

	_, ok = j.PostingOwner(pid("not-in-the-journal"))
	require.False(t, ok)
}
