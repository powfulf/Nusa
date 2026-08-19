// SPDX-License-Identifier: MIT

package ledger

import "fmt"

// ConversionSpec describes one change of commodity inside a transaction.
//
// Sold and Bought are the amounts exactly as they appear on the user's own
// accounts: Sold is negative because value left one of them, Bought is
// positive because value arrived in another. The postings this produces are
// their mirror images, landing on the trading account.
type ConversionSpec struct {
	// TradingAccount absorbs both sides. It must be an equity account: a
	// conversion moves value between commodities without the household
	// gaining or spending anything, and equity is where "neither income nor
	// expense" lives (§5.1).
	TradingAccount AccountID

	// SoldPostingID and BoughtPostingID identify the two postings this
	// builds. They come from the caller, like every other identity here.
	SoldPostingID   PostingID
	BoughtPostingID PostingID

	// Sold is the negative amount that left a user account.
	Sold Money

	// Bought is the positive amount that arrived in one.
	Bought Money

	// Rate is the exchange rate at the moment of the transaction, quoted
	// either way round. It is recorded on both postings, inverted for the
	// side it does not directly price, so a historical report can read the
	// rate off either line (§5.4). Optional but strongly preferred; without
	// it the conversion is a fact with no explanation.
	Rate Rate

	// Memo is the user's note, copied to both postings.
	Memo string
}

// ConversionPostings builds the pair of postings that balances a transaction
// whose value changes commodity.
//
// Buying 100 dollars for 1.600.000 rupiah gives four postings, not two: the
// rupiah leaving the bank, the dollars arriving in the wallet, and the two
// this returns, which put 1.600.000 rupiah and minus 100 dollars on the
// trading account. Each commodity then sums to zero on its own, which is what
// §5.1 asks for, and the trading account carries the difference the way any
// double-entry system carries it.
//
// Building the pair is a separate, explicit step. NewTransaction never invents
// it: a validator that closes its own gaps cannot tell a genuine conversion
// from an importer dropping a line.
//
// It does not check that Sold times Rate equals Bought. Testing that would
// need a tolerance, because a rate almost never lands on a whole number of
// smallest units, and a tolerance is exactly what §5.1 refuses. A caller who
// wants the two to agree derives Bought with Registry.Convert and Rat.Round
// rather than asking this function to grade its arithmetic.
func (t *AccountTree) ConversionPostings(spec ConversionSpec) ([2]Posting, error) {
	var none [2]Posting

	trading, err := t.Require(spec.TradingAccount)
	if err != nil {
		return none, fmt.Errorf("conversion: %w", err)
	}
	if trading.Kind() != AccountEquity {
		return none, fmt.Errorf("%w: %q is %s", ErrNotEquityAccount, trading.ID(), trading.Kind())
	}

	if err := spec.Sold.validate(); err != nil {
		return none, fmt.Errorf("conversion sold: %w", err)
	}
	if err := spec.Bought.validate(); err != nil {
		return none, fmt.Errorf("conversion bought: %w", err)
	}
	if spec.Sold.Commodity() == spec.Bought.Commodity() {
		return none, fmt.Errorf("%w: %s to itself is not a conversion",
			ErrInvalidTransaction, spec.Sold.Commodity())
	}
	if spec.Sold.Sign() >= 0 {
		return none, fmt.Errorf("%w: sold %s must be negative, it is value leaving an account",
			ErrInvalidTransaction, spec.Sold)
	}
	if spec.Bought.Sign() <= 0 {
		return none, fmt.Errorf("%w: bought %s must be positive, it is value arriving in an account",
			ErrInvalidTransaction, spec.Bought)
	}
	if !trading.Accepts(spec.Sold.Commodity()) || !trading.Accepts(spec.Bought.Commodity()) {
		return none, fmt.Errorf("%w: trading account %q only holds %s",
			ErrInvalidAccount, trading.ID(), trading.Commodity())
	}
	if spec.SoldPostingID == "" || spec.BoughtPostingID == "" {
		return none, fmt.Errorf("%w: conversion postings need ids", ErrInvalidTransaction)
	}
	if spec.SoldPostingID == spec.BoughtPostingID {
		return none, fmt.Errorf("%w: both conversion postings are called %q",
			ErrDuplicateID, spec.SoldPostingID)
	}

	soldRate, boughtRate, err := spec.ratesForSides()
	if err != nil {
		return none, err
	}

	// The trading account takes the opposite of each user-facing amount, which
	// is what makes both commodities sum to zero.
	soldOffset, err := spec.Sold.Neg()
	if err != nil {
		return none, err
	}
	boughtOffset, err := spec.Bought.Neg()
	if err != nil {
		return none, err
	}

	sold, err := NewPosting(PostingSpec{
		ID:      spec.SoldPostingID,
		Account: trading.ID(),
		Amount:  soldOffset,
		Rate:    soldRate,
		Memo:    spec.Memo,
	})
	if err != nil {
		return none, err
	}
	bought, err := NewPosting(PostingSpec{
		ID:      spec.BoughtPostingID,
		Account: trading.ID(),
		Amount:  boughtOffset,
		Rate:    boughtRate,
		Memo:    spec.Memo,
	})
	if err != nil {
		return none, err
	}
	return [2]Posting{sold, bought}, nil
}

// ratesForSides works out which way round the caller quoted the rate and
// returns it oriented for each posting, since a posting's rate must price its
// own commodity. Inverting a ratio is exact, so neither side loses anything.
func (spec ConversionSpec) ratesForSides() (soldSide, boughtSide Rate, err error) {
	if spec.Rate.IsZero() {
		return Rate{}, Rate{}, nil
	}

	soldCode, boughtCode := spec.Sold.Commodity(), spec.Bought.Commodity()
	switch {
	case spec.Rate.Base() == soldCode && spec.Rate.Quote() == boughtCode:
		inverted, invErr := spec.Rate.Invert()
		if invErr != nil {
			return Rate{}, Rate{}, invErr
		}
		return spec.Rate, inverted, nil

	case spec.Rate.Base() == boughtCode && spec.Rate.Quote() == soldCode:
		inverted, invErr := spec.Rate.Invert()
		if invErr != nil {
			return Rate{}, Rate{}, invErr
		}
		return inverted, spec.Rate, nil

	default:
		return Rate{}, Rate{}, fmt.Errorf("%w: rate %s does not price %s against %s",
			ErrInvalidRate, spec.Rate, soldCode, boughtCode)
	}
}
