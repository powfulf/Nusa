// SPDX-License-Identifier: MIT

package ledger_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

func TestNewAccountValidatesItsInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spec    ledger.AccountSpec
		wantErr error
	}{
		{
			"no id",
			ledger.AccountSpec{Kind: ledger.AccountAsset, Name: "Bank"},
			ledger.ErrInvalidAccount,
		},
		{
			"no kind",
			ledger.AccountSpec{ID: aid("bank"), Name: "Bank"},
			ledger.ErrInvalidAccount,
		},
		{
			"no name",
			ledger.AccountSpec{ID: aid("bank"), Kind: ledger.AccountAsset, Name: "   "},
			ledger.ErrInvalidAccount,
		},
		{
			"its own parent",
			ledger.AccountSpec{ID: aid("bank"), Parent: aid("bank"), Kind: ledger.AccountAsset, Name: "Bank"},
			ledger.ErrAccountCycle,
		},
		{
			"malformed commodity",
			ledger.AccountSpec{ID: aid("bank"), Kind: ledger.AccountAsset, Name: "Bank", Commodity: "idr"},
			ledger.ErrInvalidCommodity,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ledger.NewAccount(tc.spec)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// Postings are stored signed with a debit positive, so a liability the
// household owes carries a negative raw balance. NormalSign is what turns that
// into the figure a person expects to read.
func TestAccountKindNormalSign(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, ledger.AccountAsset.NormalSign())
	require.Equal(t, 1, ledger.AccountExpense.NormalSign())
	require.Equal(t, -1, ledger.AccountLiability.NormalSign())
	require.Equal(t, -1, ledger.AccountEquity.NormalSign())
	require.Equal(t, -1, ledger.AccountIncome.NormalSign())
	require.Equal(t, 0, ledger.AccountUnknown.NormalSign())

	require.True(t, ledger.AccountAsset.IsValid())
	require.False(t, ledger.AccountUnknown.IsValid())
	require.Equal(t, "liability", ledger.AccountLiability.String())
}

func TestAccountAcceptsCommodity(t *testing.T) {
	t.Parallel()

	restricted := mustAccountSpec(t, ledger.AccountSpec{
		ID: aid("bank"), Kind: ledger.AccountAsset, Name: "Bank", Commodity: idr,
	})
	require.True(t, restricted.Accepts(idr))
	require.False(t, restricted.Accepts(usd))

	// A brokerage account normally holds cash and shares at once, so an
	// unrestricted account takes anything.
	open := mustAccount(t, "brokerage", ledger.AccountAsset)
	require.True(t, open.Accepts(idr))
	require.True(t, open.Accepts(bbca))
}

func TestNewAccountTreeRejectsAMissingParent(t *testing.T) {
	t.Parallel()

	_, err := ledger.NewAccountTree(
		mustAccountSpec(t, ledger.AccountSpec{ID: aid("bank"), Parent: aid("assets"), Kind: ledger.AccountAsset, Name: "Bank"}),
	)
	require.ErrorIs(t, err, ledger.ErrUnknownAccount)
}

func TestNewAccountTreeRejectsDuplicateIdentities(t *testing.T) {
	t.Parallel()

	_, err := ledger.NewAccountTree(
		mustAccount(t, "bank", ledger.AccountAsset),
		mustAccountSpec(t, ledger.AccountSpec{ID: aid("bank"), Kind: ledger.AccountAsset, Name: "Other bank"}),
	)
	require.ErrorIs(t, err, ledger.ErrDuplicateID)
}

// A subtree summed from accounts of mixed kinds adds things that grow in
// opposite directions, so a rolled-up figure would be meaningless.
func TestNewAccountTreeRejectsAChildOfAnotherKind(t *testing.T) {
	t.Parallel()

	_, err := ledger.NewAccountTree(
		mustAccount(t, "assets", ledger.AccountAsset),
		mustAccountSpec(t, ledger.AccountSpec{ID: aid("card"), Parent: aid("assets"), Kind: ledger.AccountLiability, Name: "Card"}),
	)
	require.ErrorIs(t, err, ledger.ErrInvalidAccount)
}

func TestNewAccountTreeRejectsACycle(t *testing.T) {
	t.Parallel()

	// Two accounts naming each other as parent: neither is a root, so the
	// chain above either one never terminates.
	a := mustAccountSpec(t, ledger.AccountSpec{ID: aid("a"), Parent: aid("b"), Kind: ledger.AccountAsset, Name: "A"})
	b := mustAccountSpec(t, ledger.AccountSpec{ID: aid("b"), Parent: aid("a"), Kind: ledger.AccountAsset, Name: "B"})

	_, err := ledger.NewAccountTree(a, b)
	require.ErrorIs(t, err, ledger.ErrAccountCycle)
}

func TestAccountTreeNavigation(t *testing.T) {
	t.Parallel()

	tree := simpleBooks(t)

	require.Equal(t, 7, tree.Len())

	// Membership is asserted rather than order. Identities are UUIDs, so the
	// sorted order is not the order of the labels a reader sees here; that the
	// order is stable at all is what TestAccountTreeOrderingIsDeterministic
	// covers.
	require.ElementsMatch(t,
		[]ledger.AccountID{aid("assets"), aid("groceries"), aid("salary"), aid("trading")},
		tree.Roots())
	require.ElementsMatch(t,
		[]ledger.AccountID{aid("bank"), aid("shares"), aid("wallet-usd")},
		tree.Children(aid("assets")))
	require.Empty(t, tree.Children(aid("bank")))

	subtree := tree.Subtree(aid("assets"))
	require.Equal(t, aid("assets"), subtree[0], "a subtree always starts with its own root")
	require.ElementsMatch(t,
		[]ledger.AccountID{aid("assets"), aid("bank"), aid("shares"), aid("wallet-usd")},
		subtree)
	require.Equal(t, []ledger.AccountID{aid("bank")}, tree.Subtree(aid("bank")))
	require.Nil(t, tree.Subtree(aid("nowhere")))

	bank, ok := tree.Get(aid("bank"))
	require.True(t, ok)
	require.Equal(t, aid("assets"), bank.Parent())
	require.False(t, bank.IsRoot())
	require.False(t, bank.IsClosed())
	require.Equal(t, "Bank", bank.Name())

	_, ok = tree.Get(aid("nowhere"))
	require.False(t, ok)

	_, err := tree.Require(aid("nowhere"))
	require.ErrorIs(t, err, ledger.ErrUnknownAccount)
}

// Anything built from a tree — a report, a picker, a test — has to come out
// the same on every run, so the orderings are sorted rather than whatever a
// map produced.
func TestAccountTreeOrderingIsDeterministic(t *testing.T) {
	t.Parallel()

	forwards, err := ledger.NewAccountTree(
		mustAccount(t, "a", ledger.AccountAsset),
		mustAccount(t, "b", ledger.AccountAsset),
		mustAccount(t, "c", ledger.AccountAsset),
	)
	require.NoError(t, err)

	backwards, err := ledger.NewAccountTree(
		mustAccount(t, "c", ledger.AccountAsset),
		mustAccount(t, "b", ledger.AccountAsset),
		mustAccount(t, "a", ledger.AccountAsset),
	)
	require.NoError(t, err)

	require.Equal(t, forwards.Roots(), backwards.Roots())
	require.Equal(t, forwards.IDs(), backwards.IDs())
}

// The slices a tree hands out are copies, so a caller sorting or truncating
// one cannot change what the next caller sees.
func TestAccountTreeHandsOutCopies(t *testing.T) {
	t.Parallel()

	tree := simpleBooks(t)

	wantChildren := tree.Children(aid("assets"))
	wantRoots := tree.Roots()

	children := tree.Children(aid("assets"))
	children[0] = "tampered"
	require.Equal(t, wantChildren, tree.Children(aid("assets")))

	roots := tree.Roots()
	roots[0] = "tampered"
	require.Equal(t, wantRoots, tree.Roots())
}
