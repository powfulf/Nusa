// SPDX-License-Identifier: MIT

package ledger_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nusa-app/nusa/internal/ledger"
)

// 300 BBCA shares bought for Rp 2.700.000,00 — 270.000.000 sen for shares that
// trade whole, so the asset has scale 0 and its cost has scale 2.
func sharesLot(t *testing.T, label, on string, shares, cost int64) ledger.Lot {
	t.Helper()
	l, err := ledger.NewLot(ledger.LotSpec{
		ID:       lid(label),
		Account:  aid("shares"),
		OpenedBy: pid("opened-" + label),
		OpenedOn: mustDate(t, on),
		Quantity: mustMoney(t, bbca, shares),
		Cost:     mustMoney(t, idr, cost),
	})
	require.NoError(t, err)
	return l
}

func TestNewLotValidatesItsInput(t *testing.T) {
	t.Parallel()

	base := func() ledger.LotSpec {
		return ledger.LotSpec{
			ID:       lid("lot-1"),
			Account:  aid("shares"),
			OpenedBy: pid("opened-lot-1"),
			OpenedOn: mustDate(t, "2024-03-17"),
			Quantity: mustMoney(t, bbca, 300),
			Cost:     mustMoney(t, idr, 270_000_000),
		}
	}

	tests := []struct {
		name    string
		mutate  func(*ledger.LotSpec)
		wantErr error
	}{
		{"no id", func(s *ledger.LotSpec) { s.ID = "" }, ledger.ErrInvalidLot},
		{"an id that is not a UUIDv7", func(s *ledger.LotSpec) { s.ID = "lot-1" }, ledger.ErrInvalidID},
		{"no account", func(s *ledger.LotSpec) { s.Account = "" }, ledger.ErrInvalidLot},
		{"no opening posting", func(s *ledger.LotSpec) { s.OpenedBy = "" }, ledger.ErrInvalidLot},
		{"no date", func(s *ledger.LotSpec) { s.OpenedOn = ledger.Date{} }, ledger.ErrInvalidLot},
		{
			"a zero quantity",
			func(s *ledger.LotSpec) { s.Quantity = mustMoney(t, bbca, 0) },
			ledger.ErrInvalidLot,
		},
		{
			"a negative quantity",
			func(s *ledger.LotSpec) { s.Quantity = mustMoney(t, bbca, -300) },
			ledger.ErrInvalidLot,
		},
		{
			"a negative cost",
			func(s *ledger.LotSpec) { s.Cost = mustMoney(t, idr, -1) },
			ledger.ErrInvalidLot,
		},
		{
			"an asset priced in itself",
			func(s *ledger.LotSpec) { s.Cost = mustMoney(t, bbca, 300) },
			ledger.ErrInvalidLot,
		},
		{
			"more remaining than was acquired",
			func(s *ledger.LotSpec) {
				remaining := mustMoney(t, bbca, 400)
				s.Remaining = &remaining
			},
			ledger.ErrInvalidLot,
		},
		{
			"remaining in another commodity",
			func(s *ledger.LotSpec) {
				remaining := mustMoney(t, idr, 300)
				s.Remaining = &remaining
			},
			ledger.ErrCommodityMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := base()
			tc.mutate(&spec)

			_, err := ledger.NewLot(spec)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// A gift or a bonus issue costs nothing, which is not the same as being
// invalid.
func TestNewLotAcceptsAZeroCost(t *testing.T) {
	t.Parallel()

	l := sharesLot(t, "lot-bonus", "2024-03-17", 100, 0)
	require.True(t, l.IsOpen())

	basis, err := l.CostOf(mustMoney(t, bbca, 50))
	require.NoError(t, err)
	require.True(t, basis.IsZero())
}

// 100 of 300 shares that cost Rp 2.700.000,00 is exactly a third, which
// happens to be whole. 100 of 300 that cost one sen is not, and that fraction
// has to survive rather than be rounded here.
func TestLotCostOfIsProportionalAndExact(t *testing.T) {
	t.Parallel()

	even := sharesLot(t, "lot-1", "2024-03-17", 300, 270_000_000)

	third, err := even.CostOf(mustMoney(t, bbca, 100))
	require.NoError(t, err)
	require.True(t, third.IsExact())
	rounded, err := third.Round()
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 90_000_000), rounded)

	uneven := sharesLot(t, "lot-2", "2024-03-17", 300, 1)
	fraction, err := uneven.CostOf(mustMoney(t, bbca, 100))
	require.NoError(t, err)
	require.False(t, fraction.IsExact())
	require.Equal(t, "IDR 1/3", fraction.String())
}

func TestLotCostOfRejectsBadQuantities(t *testing.T) {
	t.Parallel()

	l := sharesLot(t, "lot-1", "2024-03-17", 300, 270_000_000)

	_, err := l.CostOf(mustMoney(t, bbca, 400))
	require.ErrorIs(t, err, ledger.ErrInsufficientLots)

	_, err = l.CostOf(mustMoney(t, bbca, -1))
	require.ErrorIs(t, err, ledger.ErrInvalidLot)

	_, err = l.CostOf(mustMoney(t, idr, 1))
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)
}

// Consume returns a reduced lot rather than reducing this one. A caller that
// ignores the result has not disposed of anything, which is the behaviour
// immutability should have.
func TestConsumeLeavesTheOriginalAlone(t *testing.T) {
	t.Parallel()

	original := sharesLot(t, "lot-1", "2024-03-17", 300, 270_000_000)

	reduced, basis, err := original.Consume(mustMoney(t, bbca, 100))
	require.NoError(t, err)

	requireMoney(t, mustMoney(t, bbca, 300), original.Remaining())
	requireMoney(t, mustMoney(t, bbca, 200), reduced.Remaining())
	requireMoney(t, mustMoney(t, bbca, 300), reduced.Quantity(), "the original acquisition never changes")

	roundedBasis, err := basis.Round()
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 90_000_000), roundedBasis)
}

func TestConsumeRejectsMoreThanRemains(t *testing.T) {
	t.Parallel()

	l := sharesLot(t, "lot-1", "2024-03-17", 300, 270_000_000)

	_, _, err := l.Consume(mustMoney(t, bbca, 301))
	require.ErrorIs(t, err, ledger.ErrInsufficientLots)

	_, _, err = l.Consume(mustMoney(t, bbca, 0))
	require.ErrorIs(t, err, ledger.ErrInvalidLot)

	_, _, err = l.Consume(mustMoney(t, idr, 1))
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)
}

// Lots are ordered by civil date, and by identity when two fall on the same
// day. Without the tie-break the same disposal could compute a different gain
// on different runs.
func TestSortLotsIsDeterministic(t *testing.T) {
	t.Parallel()

	lots := []ledger.Lot{
		sharesLot(t, "lot-c", "2024-03-17", 100, 1_000),
		sharesLot(t, "lot-a", "2024-01-05", 100, 1_000),
		sharesLot(t, "lot-b", "2024-03-17", 100, 1_000),
	}

	sorted := ledger.SortLots(lots)

	// The January lot is unambiguously first: the date decides.
	require.Equal(t, lid("lot-a"), sorted[0].ID())

	// The other two share a day, so identity breaks the tie. The expected
	// order is derived from the identities rather than written down, because
	// identities are UUIDs and their order is not the order of the labels.
	earlier, later := lid("lot-b"), lid("lot-c")
	if later < earlier {
		earlier, later = later, earlier
	}
	require.Equal(t, earlier, sorted[1].ID())
	require.Equal(t, later, sorted[2].ID())

	// Sorting twice gives the same answer, which is the point of the
	// tie-break: without it these two could come out either way round.
	require.Equal(t, sorted, ledger.SortLots(lots))

	// The input is untouched.
	require.Equal(t, lid("lot-c"), lots[0].ID())
}

// Three acquisitions at different prices, then a disposal that crosses two of
// them. The basis is what the oldest shares actually cost, not an average.
func TestConsumeFIFOTakesTheOldestFirst(t *testing.T) {
	t.Parallel()

	lots := []ledger.Lot{
		sharesLot(t, "lot-mar", "2024-03-17", 100, 100_000_000), // Rp 10.000 a share
		sharesLot(t, "lot-jan", "2024-01-05", 100, 80_000_000),  // Rp 8.000 a share
		sharesLot(t, "lot-feb", "2024-02-10", 100, 90_000_000),  // Rp 9.000 a share
	}

	remaining, consumed, err := ledger.ConsumeFIFO(lots, mustMoney(t, bbca, 150))
	require.NoError(t, err)

	require.Len(t, consumed, 2)
	require.Equal(t, lid("lot-jan"), consumed[0].Lot)
	requireMoney(t, mustMoney(t, bbca, 100), consumed[0].Quantity)
	require.Equal(t, lid("lot-feb"), consumed[1].Lot)
	requireMoney(t, mustMoney(t, bbca, 50), consumed[1].Quantity)

	// Sum every basis first, then round once (§4.6).
	total := consumed[0].Basis
	total, err = total.Add(consumed[1].Basis)
	require.NoError(t, err)

	rounded, err := total.Round()
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, idr, 125_000_000), rounded, "80.000.000 plus half of 90.000.000")

	left, err := ledger.TotalRemaining(remaining)
	require.NoError(t, err)
	requireMoney(t, mustMoney(t, bbca, 150), left)
}

func TestConsumeFIFOSkipsExhaustedLots(t *testing.T) {
	t.Parallel()

	empty := mustMoney(t, bbca, 0)
	exhausted, err := ledger.NewLot(ledger.LotSpec{
		ID:        lid("lot-old"),
		Account:   aid("shares"),
		OpenedBy:  pid("opened-lot-old"),
		OpenedOn:  mustDate(t, "2023-01-01"),
		Quantity:  mustMoney(t, bbca, 100),
		Cost:      mustMoney(t, idr, 50_000_000),
		Remaining: &empty,
	})
	require.NoError(t, err)
	require.False(t, exhausted.IsOpen())

	lots := []ledger.Lot{exhausted, sharesLot(t, "lot-new", "2024-01-05", 100, 80_000_000)}

	_, consumed, err := ledger.ConsumeFIFO(lots, mustMoney(t, bbca, 40))
	require.NoError(t, err)

	require.Len(t, consumed, 1)
	require.Equal(t, lid("lot-new"), consumed[0].Lot)
}

func TestConsumeFIFOValidatesTheDisposal(t *testing.T) {
	t.Parallel()

	lots := []ledger.Lot{sharesLot(t, "lot-1", "2024-01-05", 100, 80_000_000)}

	_, _, err := ledger.ConsumeFIFO(lots, mustMoney(t, bbca, 200))
	require.ErrorIs(t, err, ledger.ErrInsufficientLots)

	_, _, err = ledger.ConsumeFIFO(lots, mustMoney(t, bbca, 0))
	require.ErrorIs(t, err, ledger.ErrInvalidLot)

	_, _, err = ledger.ConsumeFIFO(lots, mustMoney(t, idr, 100))
	require.ErrorIs(t, err, ledger.ErrCommodityMismatch)

	_, _, err = ledger.ConsumeFIFO([]ledger.Lot{{}}, mustMoney(t, bbca, 1))
	require.ErrorIs(t, err, ledger.ErrInvalidLot)
}

func TestConsumeFIFOLeavesTheInputAlone(t *testing.T) {
	t.Parallel()

	lots := []ledger.Lot{
		sharesLot(t, "lot-jan", "2024-01-05", 100, 80_000_000),
		sharesLot(t, "lot-feb", "2024-02-10", 100, 90_000_000),
	}

	_, _, err := ledger.ConsumeFIFO(lots, mustMoney(t, bbca, 150))
	require.NoError(t, err)

	for _, l := range lots {
		requireMoney(t, mustMoney(t, bbca, 100), l.Remaining())
	}
}

func TestTotalRemainingNeedsLots(t *testing.T) {
	t.Parallel()

	_, err := ledger.TotalRemaining(nil)
	require.ErrorIs(t, err, ledger.ErrInvalidLot)
}

func TestZeroLot(t *testing.T) {
	t.Parallel()

	var unset ledger.Lot
	require.False(t, unset.IsValid())
	require.Equal(t, "<invalid lot>", unset.String())
}
