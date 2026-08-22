// SPDX-License-Identifier: MIT

package ledger

import (
	"fmt"
	"slices"
	"strings"
)

// AccountID identifies an account. It is its own type so that an account can
// never be passed where a transaction, a posting or a lot was meant — the
// compiler catches the swap that would otherwise post money to the wrong place
// and still balance.
//
// This package never invents one. Identities come from outside, as UUIDv7
// values the client generates, which is what makes a replayed write idempotent
// (§5.6) rather than a duplicate.
type AccountID string

// AccountKind is the accounting classification of an account. It decides which
// side of the books an account sits on, and therefore what a positive balance
// means.
//
// The user never sees these words. §7 is explicit that the technical model
// stays rigorous internally and the vocabulary is translated at the boundary;
// "liability" reaches the screen as something a person would say.
type AccountKind uint8

// The recognised account kinds.
const (
	// AccountUnknown is the zero value and is never valid.
	AccountUnknown AccountKind = iota

	// AccountAsset is something owned: a bank account, cash, a holding.
	AccountAsset

	// AccountLiability is something owed: a card balance, a loan.
	AccountLiability

	// AccountEquity is the household's own stake, and the counterweight for
	// anything that is neither income nor expense — opening balances, and the
	// trading accounts that let one commodity become another (§5.1).
	AccountEquity

	// AccountIncome is value arriving from outside the household.
	AccountIncome

	// AccountExpense is value leaving it.
	AccountExpense
)

// String returns the kind's stable machine name. It is not user-facing text.
func (k AccountKind) String() string {
	switch k {
	case AccountAsset:
		return "asset"
	case AccountLiability:
		return "liability"
	case AccountEquity:
		return "equity"
	case AccountIncome:
		return "income"
	case AccountExpense:
		return "expense"
	case AccountUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// NormalSign returns +1 for kinds that grow with a debit and -1 for kinds that
// grow with a credit.
//
// Postings are stored signed, with a debit positive, so a liability that is
// owed carries a negative raw balance. Multiplying by this turns a raw balance
// into the number a person expects to see: a card you owe Rp 500.000 on reads
// as 500.000 of debt, not as minus 500.000 of something.
func (k AccountKind) NormalSign() int {
	switch k {
	case AccountAsset, AccountExpense:
		return 1
	case AccountLiability, AccountEquity, AccountIncome:
		return -1
	case AccountUnknown:
		return 0
	default:
		return 0
	}
}

// IsValid reports whether the kind is one of the five real classifications.
func (k AccountKind) IsValid() bool {
	return k >= AccountAsset && k <= AccountExpense
}

// AccountSpec is the input to NewAccount. It is a struct rather than a
// parameter list because six positional arguments, two of which are strings,
// is a swap waiting to happen.
type AccountSpec struct {
	// ID identifies the account. Required, and supplied by the caller.
	ID AccountID

	// Parent is the enclosing account, or empty for a root.
	Parent AccountID

	// Kind is the accounting classification. Required.
	Kind AccountKind

	// Name is what the user called this account. It is user data, not a UI
	// string, so it is stored verbatim and never translated.
	Name string

	// Commodity restricts the account to one commodity. Empty means the
	// account may hold several, which is normal for a brokerage account
	// holding cash and shares at once.
	Commodity CommodityCode

	// Closed marks an account the user has finished with. A closed account
	// keeps its history and its balance; it is only hidden from pickers.
	Closed bool
}

// Account is one place value can sit or pass through.
//
// Its fields are unexported: an account's kind and parent decide how every
// balance built from it is read, so changing them behind the tree that
// validated them would invalidate answers already given.
type Account struct {
	id        AccountID
	parent    AccountID
	kind      AccountKind
	name      string
	commodity CommodityCode
	closed    bool
}

// NewAccount validates a spec and returns the account it describes.
func NewAccount(spec AccountSpec) (Account, error) {
	if err := validateIDAs(ErrInvalidAccount, "account id", string(spec.ID)); err != nil {
		return Account{}, err
	}
	if spec.Parent != "" {
		if err := validateIDAs(ErrInvalidAccount, "parent id", string(spec.Parent)); err != nil {
			return Account{}, err
		}
	}
	if !spec.Kind.IsValid() {
		return Account{}, fmt.Errorf("%w: account %q has no kind", ErrInvalidAccount, spec.ID)
	}
	if strings.TrimSpace(spec.Name) == "" {
		return Account{}, fmt.Errorf("%w: account %q has no name", ErrInvalidAccount, spec.ID)
	}
	if err := validateText(fmt.Sprintf("account %s name", spec.ID), spec.Name); err != nil {
		return Account{}, err
	}
	if spec.Parent == spec.ID {
		return Account{}, fmt.Errorf("%w: account %q is its own parent", ErrAccountCycle, spec.ID)
	}
	if spec.Commodity != "" {
		if err := validateCommodityCode(spec.Commodity); err != nil {
			return Account{}, fmt.Errorf("account %q: %w", spec.ID, err)
		}
	}
	return Account{
		id:        spec.ID,
		parent:    spec.Parent,
		kind:      spec.Kind,
		name:      spec.Name,
		commodity: spec.Commodity,
		closed:    spec.Closed,
	}, nil
}

// ID returns the account's identity.
func (a Account) ID() AccountID { return a.id }

// Parent returns the enclosing account, or the empty ID for a root.
func (a Account) Parent() AccountID { return a.parent }

// Kind returns the accounting classification.
func (a Account) Kind() AccountKind { return a.kind }

// Name returns the user's name for the account.
func (a Account) Name() string { return a.name }

// Commodity returns the commodity the account is restricted to, or the empty
// code if it may hold several.
func (a Account) Commodity() CommodityCode { return a.commodity }

// IsClosed reports whether the user has finished with the account.
func (a Account) IsClosed() bool { return a.closed }

// IsRoot reports whether the account sits at the top of the tree.
func (a Account) IsRoot() bool { return a.parent == "" }

// Accepts reports whether the account may hold the given commodity.
func (a Account) Accepts(commodity CommodityCode) bool {
	return a.commodity == "" || a.commodity == commodity
}

// AccountTree is a validated set of accounts and the parent links between
// them. Building one is the only way to know that a parent exists, that no
// chain loops, and that a sub-account has not changed kind halfway down.
//
// It holds no state that changes: every method reads.
type AccountTree struct {
	byID     map[AccountID]Account
	children map[AccountID][]AccountID
	roots    []AccountID
}

// NewAccountTree validates a set of accounts and indexes them.
//
// It rejects duplicate identities, parents that do not exist, chains that loop
// back on themselves, and a child whose kind differs from its parent's. That
// last rule is what keeps a rolled-up balance meaningful: a subtree summed
// from accounts of mixed kinds adds things that grow in opposite directions.
func NewAccountTree(accounts ...Account) (*AccountTree, error) {
	t := &AccountTree{
		byID:     make(map[AccountID]Account, len(accounts)),
		children: make(map[AccountID][]AccountID),
	}

	for _, a := range accounts {
		if a.id == "" {
			return nil, fmt.Errorf("%w: unconstructed account", ErrInvalidAccount)
		}
		if _, exists := t.byID[a.id]; exists {
			return nil, fmt.Errorf("%w: two accounts claim %q", ErrDuplicateID, a.id)
		}
		t.byID[a.id] = a
	}

	for _, a := range accounts {
		if a.parent == "" {
			t.roots = append(t.roots, a.id)
			continue
		}
		parent, ok := t.byID[a.parent]
		if !ok {
			return nil, fmt.Errorf("%w: account %q has parent %q", ErrUnknownAccount, a.id, a.parent)
		}
		if parent.kind != a.kind {
			return nil, fmt.Errorf("%w: account %q is %s under a %s parent %q",
				ErrInvalidAccount, a.id, a.kind, parent.kind, a.parent)
		}
		t.children[a.parent] = append(t.children[a.parent], a.id)
	}

	if err := t.checkForCycles(); err != nil {
		return nil, err
	}

	// Deterministic order everywhere, so anything built from a tree — a
	// report, a picker, a test assertion — comes out the same every run.
	slices.Sort(t.roots)
	for parent := range t.children {
		slices.Sort(t.children[parent])
	}
	return t, nil
}

// checkForCycles walks each account's parent chain. A chain longer than the
// tree itself has revisited something, which is the cheapest sound test here
// and needs no extra bookkeeping.
func (t *AccountTree) checkForCycles() error {
	for id := range t.byID {
		steps := 0
		for cursor := t.byID[id]; cursor.parent != ""; cursor = t.byID[cursor.parent] {
			steps++
			if steps > len(t.byID) {
				return fmt.Errorf("%w: the chain above %q loops", ErrAccountCycle, id)
			}
		}
	}
	return nil
}

// Get returns an account by identity.
func (t *AccountTree) Get(id AccountID) (Account, bool) {
	a, ok := t.byID[id]
	return a, ok
}

// Require returns an account by identity, or an error naming the one that is
// missing.
func (t *AccountTree) Require(id AccountID) (Account, error) {
	a, ok := t.byID[id]
	if !ok {
		return Account{}, fmt.Errorf("%w: %q", ErrUnknownAccount, id)
	}
	return a, nil
}

// Len reports how many accounts the tree holds.
func (t *AccountTree) Len() int { return len(t.byID) }

// IDs returns every account identity in sorted order.
func (t *AccountTree) IDs() []AccountID {
	ids := make([]AccountID, 0, len(t.byID))
	for id := range t.byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Roots returns the identities of accounts with no parent, in sorted order.
func (t *AccountTree) Roots() []AccountID {
	return slices.Clone(t.roots)
}

// Children returns the immediate children of an account, in sorted order.
func (t *AccountTree) Children(id AccountID) []AccountID {
	return slices.Clone(t.children[id])
}

// Subtree returns an account and everything beneath it, in a stable order with
// the account itself first. It is what a rolled-up balance sums over.
func (t *AccountTree) Subtree(id AccountID) []AccountID {
	if _, ok := t.byID[id]; !ok {
		return nil
	}
	out := []AccountID{id}
	for i := 0; i < len(out); i++ {
		out = append(out, t.children[out[i]]...)
	}
	return out
}
