// SPDX-License-Identifier: MIT

package ledger

import (
	"cmp"
	"fmt"
	"slices"
)

// Journal is a set of accounts and the transactions posted against them, with
// the queries that read a balance out of them.
//
// It holds nothing but memory. There is no persistence here, no loading, no
// saving and no cache — a Journal is built from transactions someone else
// fetched, answers questions about them, and is thrown away. When M2 adds
// stored balances, this is the definition they have to agree with: §5.2 says a
// cached balance is an optimisation that must be reconstructible from scratch,
// and reconstructing from scratch is exactly what these methods do.
//
// Balances come out raw and signed, with a debit positive. A liability the
// household owes therefore reads negative. Turning that into the figure a
// person expects is AccountKind.NormalSign's job, one layer up, because how a
// number is presented is not a property of the ledger.
type Journal struct {
	accounts     *AccountTree
	transactions []Transaction
	byID         map[TransactionID]int
	postingOwner map[PostingID]TransactionID
}

// NewJournal validates a set of transactions against a set of accounts.
//
// Every posting must land in an account the tree knows, in a commodity that
// account accepts. Identities must be unique across the whole journal, for
// postings as well as transactions, because a lot or an audit entry pointing
// at a posting id has to find exactly one posting.
//
// The transactions are held in civil date order, with the identity breaking
// ties, so that two journals built from the same set in different orders
// answer every question identically.
func NewJournal(accounts *AccountTree, transactions ...Transaction) (*Journal, error) {
	if accounts == nil {
		return nil, fmt.Errorf("%w: journal needs an account tree", ErrInvalidAccount)
	}

	j := &Journal{
		accounts:     accounts,
		transactions: make([]Transaction, 0, len(transactions)),
		byID:         make(map[TransactionID]int, len(transactions)),
		postingOwner: make(map[PostingID]TransactionID, len(transactions)*2),
	}

	postingIDs := j.postingOwner
	for _, t := range transactions {
		if !t.IsValid() {
			return nil, fmt.Errorf("%w: unconstructed transaction", ErrInvalidTransaction)
		}
		if _, exists := j.byID[t.id]; exists {
			return nil, fmt.Errorf("%w: two transactions claim %q", ErrDuplicateID, t.id)
		}
		j.byID[t.id] = 0 // real index assigned after sorting

		for _, p := range t.postings {
			if owner, exists := postingIDs[p.id]; exists {
				return nil, fmt.Errorf("%w: posting %q appears in both %q and %q",
					ErrDuplicateID, p.id, owner, t.id)
			}
			postingIDs[p.id] = t.id

			account, err := accounts.Require(p.account)
			if err != nil {
				return nil, fmt.Errorf("transaction %q posting %q: %w", t.id, p.id, err)
			}
			if !account.Accepts(p.amount.commodity) {
				return nil, fmt.Errorf("%w: transaction %q posts %s to %q, which only holds %s",
					ErrCommodityMismatch, t.id, p.amount.commodity, account.ID(), account.Commodity())
			}
		}
		j.transactions = append(j.transactions, t)
	}

	slices.SortStableFunc(j.transactions, func(a, b Transaction) int {
		if c := a.date.Compare(b.date); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})
	for i, t := range j.transactions {
		j.byID[t.id] = i
	}
	return j, nil
}

// Accounts returns the tree the journal was validated against.
func (j *Journal) Accounts() *AccountTree { return j.accounts }

// Len reports how many transactions the journal holds.
func (j *Journal) Len() int { return len(j.transactions) }

// Transactions returns every transaction in civil date order. The slice is a
// copy.
func (j *Journal) Transactions() []Transaction { return slices.Clone(j.transactions) }

// Transaction returns one transaction by identity.
func (j *Journal) Transaction(id TransactionID) (Transaction, bool) {
	i, ok := j.byID[id]
	if !ok {
		return Transaction{}, false
	}
	return j.transactions[i], true
}

// PostingOwner returns which transaction a posting belongs to.
//
// It is how a Lot recovers its transaction: a lot names the posting that
// opened it, because a transaction can acquire two things at once and "which
// line was this lot" would then have no answer. Storing the narrower fact and
// widening it here means the two can never disagree.
func (j *Journal) PostingOwner(id PostingID) (TransactionID, bool) {
	owner, ok := j.postingOwner[id]
	return owner, ok
}

// Posting returns one posting by identity, wherever in the journal it sits.
//
// Identity is what makes this possible, and it is why postings have one.
// Finding a posting by its position inside a transaction would give a
// different answer the moment anything reordered them — silently, with no
// error and only a wrong number to show for it.
func (j *Journal) Posting(id PostingID) (Posting, bool) {
	owner, ok := j.postingOwner[id]
	if !ok {
		return Posting{}, false
	}
	for _, p := range j.transactions[j.byID[owner]].postings {
		if p.id == id {
			return p, true
		}
	}
	return Posting{}, false
}

// Add returns a new journal with more transactions, leaving this one alone.
// The same validation runs, so a duplicate identity is caught here rather than
// discovered later in a balance that is quietly twice what it should be.
func (j *Journal) Add(transactions ...Transaction) (*Journal, error) {
	combined := make([]Transaction, 0, len(j.transactions)+len(transactions))
	combined = append(combined, j.transactions...)
	combined = append(combined, transactions...)
	return NewJournal(j.accounts, combined...)
}

// Postings returns every posting landing in one account, in civil date order.
func (j *Journal) Postings(account AccountID) []Posting {
	var out []Posting
	for _, t := range j.transactions {
		for _, p := range t.postings {
			if p.account == account {
				out = append(out, p)
			}
		}
	}
	return out
}

// Balance returns what one account holds, per commodity, over all time.
//
// This is §5.2 stated as code: an account's balance is the sum of its
// postings, and nothing else. There is no stored figure to drift from it.
func (j *Journal) Balance(account AccountID) (*Balances, error) {
	return j.BalanceAsOf(account, Date{})
}

// BalanceAsOf returns what one account held at the end of a given civil date,
// counting that date. A zero date means all time.
//
// Only Transaction.Date decides what is included. A transaction's OccurredAt
// and Timezone are never consulted, which is what makes this answer the same
// for every reader in every zone (§5.4's sibling: a report must not change
// because of who is looking).
func (j *Journal) BalanceAsOf(account AccountID, on Date) (*Balances, error) {
	if _, err := j.accounts.Require(account); err != nil {
		return nil, err
	}
	sums := newBalances()
	for _, t := range j.transactions {
		if !on.IsZero() && t.date.After(on) {
			break // sorted by date, so nothing later qualifies either
		}
		for _, p := range t.postings {
			if p.account != account {
				continue
			}
			if err := sums.add(p.amount); err != nil {
				return nil, fmt.Errorf("transaction %q posting %q: %w", t.id, p.id, err)
			}
		}
	}
	return sums, nil
}

// BalanceBetween returns what moved through one account between two civil
// dates, both counted. It is what a period figure is made of: income earned in
// March, spending against an envelope this month.
func (j *Journal) BalanceBetween(account AccountID, from, to Date) (*Balances, error) {
	if _, err := j.accounts.Require(account); err != nil {
		return nil, err
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return nil, fmt.Errorf("%w: %s is after %s", ErrInvalidDate, from, to)
	}
	sums := newBalances()
	for _, t := range j.transactions {
		if !from.IsZero() && t.date.Before(from) {
			continue
		}
		if !to.IsZero() && t.date.After(to) {
			break
		}
		for _, p := range t.postings {
			if p.account != account {
				continue
			}
			if err := sums.add(p.amount); err != nil {
				return nil, fmt.Errorf("transaction %q posting %q: %w", t.id, p.id, err)
			}
		}
	}
	return sums, nil
}

// SubtreeBalanceAsOf returns an account and everything beneath it, summed. A
// zero date means all time.
//
// The account tree already refuses a child of a different kind from its
// parent, which is what makes this sum mean something: every account under one
// root grows in the same direction.
func (j *Journal) SubtreeBalanceAsOf(account AccountID, on Date) (*Balances, error) {
	if _, err := j.accounts.Require(account); err != nil {
		return nil, err
	}
	members := j.accounts.Subtree(account)
	wanted := make(map[AccountID]struct{}, len(members))
	for _, id := range members {
		wanted[id] = struct{}{}
	}

	sums := newBalances()
	for _, t := range j.transactions {
		if !on.IsZero() && t.date.After(on) {
			break
		}
		for _, p := range t.postings {
			if _, ok := wanted[p.account]; !ok {
				continue
			}
			if err := sums.add(p.amount); err != nil {
				return nil, fmt.Errorf("transaction %q posting %q: %w", t.id, p.id, err)
			}
		}
	}
	return sums, nil
}

// TotalsByCommodity returns every posting in the journal summed by commodity.
//
// It is the whole-book form of §5.1, and it must always be zero: each
// transaction sums to zero on its own, so any number of them still do. A
// non-zero total means something got in without passing NewTransaction.
func (j *Journal) TotalsByCommodity() (*Balances, error) {
	sums := newBalances()
	for _, t := range j.transactions {
		for _, p := range t.postings {
			if err := sums.add(p.amount); err != nil {
				return nil, fmt.Errorf("transaction %q posting %q: %w", t.id, p.id, err)
			}
		}
	}
	return sums, nil
}
