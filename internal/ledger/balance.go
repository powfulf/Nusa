// SPDX-License-Identifier: MIT

package ledger

import (
	"math/big"
	"slices"
	"strings"
)

// Balances is a total per commodity.
//
// A single number is not enough to answer "what is in this account". A
// brokerage account holds rupiah and shares at once; a household holds rupiah
// and dollars. Those are different things and this type refuses to add them —
// which is the same refusal Money makes, carried up to the level of a whole
// account.
//
// Nothing about it is user-facing. Turning a set of totals into one figure a
// person can read means valuing every commodity at some rate on some date, and
// that is a reporting decision made above this package, not a property of the
// ledger.
//
// The map is unexported and every exported method reads, so a Balances handed
// out by a query cannot be altered by whoever received it.
type Balances struct {
	byCode map[CommodityCode]*big.Int
}

func newBalances() *Balances {
	return &Balances{byCode: make(map[CommodityCode]*big.Int)}
}

// add accumulates one amount. It is unexported: a Balances is either being
// built by this package or being read by someone else, never both.
func (b *Balances) add(m Money) error {
	if err := m.validate(); err != nil {
		return err
	}
	total, ok := b.byCode[m.commodity]
	if !ok {
		total = new(big.Int)
		b.byCode[m.commodity] = total
	}
	total.Add(total, m.amount)
	return nil
}

// Get returns the total held in one commodity. A commodity the balances have
// never seen reads as a genuine zero of that commodity rather than as an
// absence, because "no dollars" and "zero dollars" are the same fact.
func (b *Balances) Get(commodity CommodityCode) Money {
	if total, ok := b.byCode[commodity]; ok {
		return Money{amount: new(big.Int).Set(total), commodity: commodity}
	}
	return Money{amount: new(big.Int), commodity: commodity}
}

// Codes returns every commodity present, in sorted order.
func (b *Balances) Codes() []CommodityCode {
	codes := make([]CommodityCode, 0, len(b.byCode))
	for code := range b.byCode {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	return codes
}

// All returns every total, including zero ones, ordered by commodity.
func (b *Balances) All() []Money {
	codes := b.Codes()
	out := make([]Money, 0, len(codes))
	for _, code := range codes {
		out = append(out, b.Get(code))
	}
	return out
}

// Nonzero returns only the totals that are not zero, ordered by commodity.
// This is what an unbalanced transaction's residual looks like, and what a
// conversion has to offset.
func (b *Balances) Nonzero() []Money {
	out := make([]Money, 0, len(b.byCode))
	for _, m := range b.All() {
		if !m.IsZero() {
			out = append(out, m)
		}
	}
	return out
}

// IsZero reports whether every commodity totals exactly zero. For a
// transaction's postings this is §5.1, and it holds exactly or not at all.
func (b *Balances) IsZero() bool {
	for _, total := range b.byCode {
		if total.Sign() != 0 {
			return false
		}
	}
	return true
}

// Len reports how many commodities appear, counting those that total zero.
func (b *Balances) Len() int { return len(b.byCode) }

// String renders the totals for logs and test failures.
func (b *Balances) String() string {
	if len(b.byCode) == 0 {
		return "<empty>"
	}
	parts := make([]string, 0, len(b.byCode))
	for _, m := range b.All() {
		parts = append(parts, m.String())
	}
	return strings.Join(parts, ", ")
}

// SumPostings totals a set of postings by commodity.
//
// It is the whole of §5.1's arithmetic: a transaction balances when every
// total this returns is zero. Nothing here rounds, allows a tolerance, or
// invents a posting to close a gap — a residual is reported, never absorbed.
func SumPostings(postings []Posting) (*Balances, error) {
	sums := newBalances()
	for i, p := range postings {
		if !p.IsValid() {
			return nil, wrapPostingIndex(i, ErrInvalidTransaction, "unconstructed posting")
		}
		if err := sums.add(p.amount); err != nil {
			return nil, wrapPostingIndex(i, err, "")
		}
	}
	return sums, nil
}
