// SPDX-License-Identifier: MIT

package ledger

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
)

// LotID identifies a lot. A distinct type, and never generated here.
type LotID string

// LotSpec is the input to NewLot.
type LotSpec struct {
	// ID identifies the lot. Required, and supplied by the caller.
	ID LotID

	// Account is the account holding the asset. Required.
	Account AccountID

	// OpenedBy is the posting that acquired it — the line that debited the
	// asset account. Required.
	//
	// It names a posting rather than a transaction because a transaction can
	// acquire two things at once, and "which line was this lot" then has no
	// answer. The owning transaction is recoverable from the journal with
	// Journal.PostingOwner, so nothing is lost by storing the narrower fact.
	OpenedBy PostingID

	// OpenedOn is the civil date of that acquisition. Required, and the only
	// thing that orders lots against each other.
	OpenedOn Date

	// Quantity is how much was acquired, positive, in the asset's own
	// commodity — 300 shares, 0,05 BTC. Required.
	Quantity Money

	// Cost is what the whole of Quantity cost, positive or zero, in the
	// currency it was paid in. Required.
	//
	// §7 calls this "what you paid" on screen. It is the total for the lot,
	// not a price per unit, because a total is exact and a per-unit price
	// generally is not.
	Cost Money

	// Remaining is how much of Quantity is left. Optional: an unset Remaining
	// means the lot is untouched, which is what a newly acquired one is.
	Remaining *Money
}

// Lot is one acquisition of an asset, with what it cost, tracked separately so
// that a later disposal can say which units it disposed of.
//
// Lots exist because "what did those shares cost" has no single answer once
// you have bought the same share twice at different prices. Averaging is one
// policy among several and most tax regimes do not accept it, so Nusa keeps
// the acquisitions apart and lets the disposal choose.
//
// Like everything else here a Lot is immutable. Consume returns a new lot
// rather than reducing this one.
type Lot struct {
	id        LotID
	account   AccountID
	openedBy  PostingID
	openedOn  Date
	quantity  Money
	remaining Money
	cost      Money
}

// NewLot validates a spec and returns the lot it describes.
func NewLot(spec LotSpec) (Lot, error) {
	if err := validateIDAs(ErrInvalidLot, "lot id", string(spec.ID)); err != nil {
		return Lot{}, err
	}
	if err := validateIDAs(ErrInvalidLot, "lot account id", string(spec.Account)); err != nil {
		return Lot{}, err
	}
	if err := validateIDAs(ErrInvalidLot, "lot opening posting id", string(spec.OpenedBy)); err != nil {
		return Lot{}, err
	}
	if spec.OpenedOn.IsZero() {
		return Lot{}, fmt.Errorf("%w: lot %q has no date", ErrInvalidLot, spec.ID)
	}
	if err := spec.Quantity.validate(); err != nil {
		return Lot{}, fmt.Errorf("lot %q quantity: %w", spec.ID, err)
	}
	if err := spec.Cost.validate(); err != nil {
		return Lot{}, fmt.Errorf("lot %q cost: %w", spec.ID, err)
	}
	if spec.Quantity.Sign() <= 0 {
		return Lot{}, fmt.Errorf("%w: lot %q holds %s, a lot is an acquisition and must be positive",
			ErrInvalidLot, spec.ID, spec.Quantity)
	}
	if spec.Cost.Sign() < 0 {
		return Lot{}, fmt.Errorf("%w: lot %q cost %s is negative", ErrInvalidLot, spec.ID, spec.Cost)
	}
	if spec.Quantity.Commodity() == spec.Cost.Commodity() {
		return Lot{}, fmt.Errorf("%w: lot %q is %s bought with %s — an asset and its cost are different things",
			ErrInvalidLot, spec.ID, spec.Quantity.Commodity(), spec.Cost.Commodity())
	}

	remaining := spec.Quantity
	if spec.Remaining != nil {
		remaining = *spec.Remaining
		if err := remaining.validate(); err != nil {
			return Lot{}, fmt.Errorf("lot %q remaining: %w", spec.ID, err)
		}
		if remaining.Commodity() != spec.Quantity.Commodity() {
			return Lot{}, fmt.Errorf("%w: lot %q holds %s but %s remains",
				ErrCommodityMismatch, spec.ID, spec.Quantity.Commodity(), remaining.Commodity())
		}
		if remaining.Sign() < 0 {
			return Lot{}, fmt.Errorf("%w: lot %q has %s remaining", ErrInvalidLot, spec.ID, remaining)
		}
		if cmp, _ := remaining.Cmp(spec.Quantity); cmp > 0 {
			return Lot{}, fmt.Errorf("%w: lot %q has %s remaining of %s acquired",
				ErrInvalidLot, spec.ID, remaining, spec.Quantity)
		}
	}

	return Lot{
		id:        spec.ID,
		account:   spec.Account,
		openedBy:  spec.OpenedBy,
		openedOn:  spec.OpenedOn,
		quantity:  spec.Quantity,
		remaining: remaining,
		cost:      spec.Cost,
	}, nil
}

// IsValid reports whether the lot came from NewLot.
func (l Lot) IsValid() bool { return l.id != "" && l.quantity.IsValid() && l.cost.IsValid() }

// ID returns the lot's identity.
func (l Lot) ID() LotID { return l.id }

// Account returns the account holding the asset.
func (l Lot) Account() AccountID { return l.account }

// OpenedBy returns the posting that acquired the lot.
func (l Lot) OpenedBy() PostingID { return l.openedBy }

// OpenedOn returns the civil date of the acquisition.
func (l Lot) OpenedOn() Date { return l.openedOn }

// Quantity returns how much was originally acquired.
func (l Lot) Quantity() Money { return l.quantity }

// Remaining returns how much of the acquisition is left.
func (l Lot) Remaining() Money { return l.remaining }

// Cost returns what the whole original quantity cost.
func (l Lot) Cost() Money { return l.cost }

// IsOpen reports whether any of the lot is left to dispose of.
func (l Lot) IsOpen() bool { return l.remaining.Sign() > 0 }

// CostOf returns what the given part of the lot cost, in proportion to the
// whole.
//
// The result is a Rat, not a Money: 100 of 300 shares that cost Rp 1.000.000
// is a third of a million rupiah, which is not a whole number of rupiah. It
// stays exact until the caller rounds it once, at the end of whatever it is
// computing — a gain, a disposal, a report line.
func (l Lot) CostOf(quantity Money) (Rat, error) {
	if err := quantity.validate(); err != nil {
		return Rat{}, err
	}
	if quantity.Commodity() != l.quantity.Commodity() {
		return Rat{}, fmt.Errorf("%w: lot %q holds %s, asked for %s",
			ErrCommodityMismatch, l.id, l.quantity.Commodity(), quantity.Commodity())
	}
	if quantity.Sign() < 0 {
		return Rat{}, fmt.Errorf("%w: asked lot %q for %s", ErrInvalidLot, l.id, quantity)
	}
	if cmp, _ := quantity.Cmp(l.quantity); cmp > 0 {
		return Rat{}, fmt.Errorf("%w: asked lot %q for %s of %s acquired",
			ErrInsufficientLots, l.id, quantity, l.quantity)
	}
	share := new(big.Rat).SetFrac(quantity.amount, l.quantity.amount)
	return l.cost.Mul(share)
}

// Consume disposes of part of the lot, returning the reduced lot and what the
// disposed part cost.
//
// The receiver is unchanged. A caller that ignores the returned lot has not
// consumed anything, which is the behaviour immutability should have: no
// disposal happens as a side effect of asking about one.
func (l Lot) Consume(quantity Money) (Lot, Rat, error) {
	if err := quantity.validate(); err != nil {
		return Lot{}, Rat{}, err
	}
	if quantity.Sign() <= 0 {
		return Lot{}, Rat{}, fmt.Errorf("%w: cannot consume %s from lot %q", ErrInvalidLot, quantity, l.id)
	}
	if quantity.Commodity() != l.remaining.Commodity() {
		return Lot{}, Rat{}, fmt.Errorf("%w: lot %q holds %s, asked to consume %s",
			ErrCommodityMismatch, l.id, l.remaining.Commodity(), quantity.Commodity())
	}
	if cmp, _ := quantity.Cmp(l.remaining); cmp > 0 {
		return Lot{}, Rat{}, fmt.Errorf("%w: lot %q has %s left, asked for %s",
			ErrInsufficientLots, l.id, l.remaining, quantity)
	}

	basis, err := l.CostOf(quantity)
	if err != nil {
		return Lot{}, Rat{}, err
	}
	left, err := l.remaining.Sub(quantity)
	if err != nil {
		return Lot{}, Rat{}, err
	}

	reduced := l
	reduced.remaining = left
	return reduced, basis, nil
}

// String renders the lot for logs and test failures.
func (l Lot) String() string {
	if !l.IsValid() {
		return "<invalid lot>"
	}
	return fmt.Sprintf("%s %s: %s of %s for %s", l.openedOn, l.id, l.remaining, l.quantity, l.cost)
}

// Consumption records one lot's part in a disposal.
type Consumption struct {
	// Lot is which lot was drawn on.
	Lot LotID

	// Quantity is how much came out of it.
	Quantity Money

	// Basis is what that quantity cost, exact and unrounded. Sum the basis
	// across every consumption first, then round once (§4.6).
	Basis Rat
}

// SortLots orders lots the way disposals consume them: oldest first by civil
// date, then by identity so that two lots opened on the same day always come
// out in the same order.
//
// The tie-break matters more than it looks. Without it, two lots from the same
// day could be consumed in whatever order a map iteration produced, and the
// same disposal would compute a different gain on different runs.
func SortLots(lots []Lot) []Lot {
	sorted := slices.Clone(lots)
	slices.SortStableFunc(sorted, func(a, b Lot) int {
		if c := a.openedOn.Compare(b.openedOn); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})
	return sorted
}

// ConsumeFIFO disposes of a quantity across lots, oldest first.
//
// It returns every lot — reduced where it was drawn on, untouched where it was
// not — in the order it consumed them, alongside a record of what came out of
// each. It changes nothing it was given.
//
// First-in-first-out is the only policy here. Others exist, and which one is
// permitted is a country-specific question, so the rest belong in Country
// Packs rather than in core (§1).
func ConsumeFIFO(lots []Lot, quantity Money) ([]Lot, []Consumption, error) {
	if err := quantity.validate(); err != nil {
		return nil, nil, err
	}
	if quantity.Sign() <= 0 {
		return nil, nil, fmt.Errorf("%w: cannot dispose of %s", ErrInvalidLot, quantity)
	}

	ordered := SortLots(lots)
	for _, l := range ordered {
		if !l.IsValid() {
			return nil, nil, fmt.Errorf("%w: unconstructed lot", ErrInvalidLot)
		}
		if l.quantity.Commodity() != quantity.Commodity() {
			return nil, nil, fmt.Errorf("%w: disposing of %s among lots of %s",
				ErrCommodityMismatch, quantity.Commodity(), l.quantity.Commodity())
		}
	}

	left := quantity
	consumptions := make([]Consumption, 0, len(ordered))

	for i, l := range ordered {
		if left.IsZero() {
			break
		}
		if !l.IsOpen() {
			continue
		}

		take := left
		if cmp, _ := l.remaining.Cmp(left); cmp < 0 {
			take = l.remaining
		}

		reduced, basis, err := l.Consume(take)
		if err != nil {
			return nil, nil, err
		}
		ordered[i] = reduced
		consumptions = append(consumptions, Consumption{Lot: l.id, Quantity: take, Basis: basis})

		if left, err = left.Sub(take); err != nil {
			return nil, nil, err
		}
	}

	if !left.IsZero() {
		return nil, nil, fmt.Errorf("%w: %s short disposing of %s", ErrInsufficientLots, left, quantity)
	}
	return ordered, consumptions, nil
}

// TotalRemaining adds up what is left across lots, which must all hold the
// same commodity.
func TotalRemaining(lots []Lot) (Money, error) {
	if len(lots) == 0 {
		return Money{}, fmt.Errorf("%w: no lots to total", ErrInvalidLot)
	}
	total := lots[0].remaining
	for _, l := range lots[1:] {
		var err error
		if total, err = total.Add(l.remaining); err != nil {
			return Money{}, err
		}
	}
	return total, nil
}
