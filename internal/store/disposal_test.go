// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
	"github.com/GaffaQ/Nusa/internal/store"
)

// A disposal reduces a holding and has to say what it reduced.
//
// Before this existed, selling reduced lots.remaining_amount and left nothing
// behind: which acquisitions a sale drew on, from which dates, at what price
// were simply gone. That is unreconstructible rather than inconvenient — FIFO
// depends on which lots were open at the moment of the disposal, and later
// activity changes that, so re-running the selection over today's lots does
// not reproduce last year's answer.

// The asset the disposal tests trade. BTC is a seeded commodity with scale 8,
// which is deep enough that a fractional cost basis is unmistakable.
const satoshi = 1 // one minor unit of BTC

type holding struct {
	books
	crypto ledger.AccountID
}

func seedHolding(t *testing.T, s *store.Store) holding {
	t.Helper()

	b := seedBooks(t, s)
	h := holding{books: b, crypto: ledger.AccountID(testID("account:crypto"))}

	account, err := ledger.NewAccount(ledger.AccountSpec{
		ID: h.crypto, Kind: ledger.AccountAsset, Name: "Coins", Commodity: "BTC",
	})
	require.NoError(t, err)
	require.NoError(t, s.SaveAccount(context.Background(), account))
	return h
}

// trade builds a balanced four-line transaction moving `units` of BTC against
// `rupiah`, routed through the equity trading account so that each commodity
// sums to zero on its own (§5.1). Positive units acquire, negative dispose.
//
// It returns the transaction and the identity of the line that touches the
// holding, which is the line a disposal names.
func trade(
	t *testing.T, h holding, label, date string, units, rupiah int64,
) (ledger.Transaction, ledger.PostingID) {
	t.Helper()

	assetLine := ledger.PostingID(testID("posting:" + label + ":asset"))
	postings := make([]ledger.Posting, 0, 4)
	for _, line := range []struct {
		id      ledger.PostingID
		account ledger.AccountID
		code    ledger.CommodityCode
		amount  int64
	}{
		{assetLine, h.crypto, "BTC", units},
		{ledger.PostingID(testID("posting:" + label + ":asset-equity")), h.trading, "BTC", -units},
		{ledger.PostingID(testID("posting:" + label + ":cash-equity")), h.trading, "IDR", -rupiah},
		{ledger.PostingID(testID("posting:" + label + ":cash")), h.cash, "IDR", rupiah},
	} {
		posting, err := ledger.NewPosting(ledger.PostingSpec{
			ID: line.id, Account: line.account, Amount: mustMoney(t, line.code, line.amount),
		})
		require.NoError(t, err)
		postings = append(postings, posting)
	}

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:       ledger.TransactionID(testID("txn:" + label)),
		Date:     mustDate(t, date),
		Payee:    "Exchange",
		Postings: postings,
	})
	require.NoError(t, err)
	return txn, assetLine
}

// buy writes an acquisition and the lot it opens.
func buy(
	t *testing.T, s *store.Store, h holding, label, date string, units, rupiah int64,
) ledger.LotID {
	t.Helper()

	txn, assetLine := trade(t, h, label, date, units, rupiah)
	id := ledger.LotID(testID("lot:" + label))
	lot, err := ledger.NewLot(ledger.LotSpec{
		ID: id, Account: h.crypto, OpenedBy: assetLine, OpenedOn: mustDate(t, date),
		Quantity: mustMoney(t, "BTC", units), Cost: mustMoney(t, "IDR", rupiah),
	})
	require.NoError(t, err)

	_, err = s.SaveTransaction(context.Background(), h.write("buy-"+label), txn, lot)
	require.NoError(t, err)
	return id
}

// §4.7 in one number. 0,1 of a 0,3 lot that cost Rp 1.000.000 is exactly one
// third of a million rupiah, which is not a whole number of rupiah and never
// will be. Storing it as minor units would round it here — in the middle of a
// calculation, invisibly — and §4.6 says rounding happens once, at the end.
func TestACostBasisThatIsNotAWholeNumberIsStoredExactly(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)

	lot := buy(t, s, h, "third", "2026-01-10", 30_000_000*satoshi, 1_000_000_00)

	sale, disposing := trade(t, h, "sell-third", "2026-05-01", -10_000_000*satoshi, 400_000_00)
	result, err := s.SaveDisposal(ctx, h.write("sell-third"), sale, disposing)
	require.NoError(t, err)

	consumed := result.Consumed[disposing]
	require.Len(t, consumed, 1)
	require.Equal(t, lot, consumed[0].Lot)

	// A third of Rp 1.000.000, in minor units: 100000000/3.
	want := big.NewRat(1_000_000_00, 3)
	require.Equal(t, want.RatString(), consumed[0].Basis.Value().RatString())
	require.False(t, consumed[0].Basis.IsExact(),
		"this basis is deliberately not a whole number of minor units")

	// And it survives the round trip as the same fraction, not as a decimal
	// that happens to compare equal.
	back, err := s.Consumptions(ctx, disposing)
	require.NoError(t, err)
	require.Len(t, back, 1)
	require.Equal(t, want.RatString(), back[0].Basis.Value().RatString(),
		"the stored basis is a rounded figure, which is the one thing it must never be")
	require.Equal(t, ledger.CommodityCode("IDR"), back[0].Basis.Commodity())
	require.True(t, back[0].Quantity.Equal(mustMoney(t, "BTC", 10_000_000*satoshi)))
}

// FIFO, and the record of it. The oldest lot is drained before the next is
// touched, and both consumptions are written.
func TestADisposalDrawsOnTheOldestLotsFirstAndRecordsEachOne(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)

	oldest := buy(t, s, h, "jan", "2026-01-10", 10_000_000*satoshi, 300_000_00)
	middle := buy(t, s, h, "feb", "2026-02-10", 10_000_000*satoshi, 500_000_00)
	newest := buy(t, s, h, "mar", "2026-03-10", 10_000_000*satoshi, 900_000_00)

	// 0,15 BTC: all of January and half of February. March is untouched.
	sale, disposing := trade(t, h, "sell-fifo", "2026-06-01", -15_000_000*satoshi, 1_000_000_00)
	result, err := s.SaveDisposal(ctx, h.write("sell-fifo"), sale, disposing)
	require.NoError(t, err)

	consumed := result.Consumed[disposing]
	require.Len(t, consumed, 2, "March must not be touched while February is open")
	require.Equal(t, oldest, consumed[0].Lot, "the oldest lot goes first")
	require.Equal(t, middle, consumed[1].Lot)

	require.True(t, consumed[0].Quantity.Equal(mustMoney(t, "BTC", 10_000_000*satoshi)))
	require.True(t, consumed[1].Quantity.Equal(mustMoney(t, "BTC", 5_000_000*satoshi)))

	// Basis follows the lot it came from, not an average: all of a Rp 300.000
	// lot, then half of a Rp 500.000 one.
	require.Equal(t, big.NewRat(300_000_00, 1).RatString(), consumed[0].Basis.Value().RatString())
	require.Equal(t, big.NewRat(250_000_00, 1).RatString(), consumed[1].Basis.Value().RatString())

	// The running figures moved to match.
	for _, want := range []struct {
		lot       ledger.LotID
		remaining int64
	}{
		{oldest, 0},
		{middle, 5_000_000 * satoshi},
		{newest, 10_000_000 * satoshi},
	} {
		lot, err := s.LoadLot(ctx, want.lot)
		require.NoError(t, err)
		require.True(t, lot.Remaining().Equal(mustMoney(t, "BTC", want.remaining)),
			"lot %s has %s remaining", want.lot, lot.Remaining())
	}
}

// §5.2's shape, one level down: the running figure is derivable from the
// appended record, so the two can be checked against each other.
func TestALotsRemainingQuantityIsReconstructibleFromWhatConsumedIt(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)

	lot := buy(t, s, h, "rebuild", "2026-01-10", 10_000_000*satoshi, 400_000_00)

	for i, units := range []int64{2_000_000, 3_000_000, 1_000_000} {
		label := fmt.Sprintf("sell-part-%d", i)
		sale, disposing := trade(t, h, label, "2026-06-0"+fmt.Sprint(i+1), -units*satoshi, 100_000_00)
		_, err := s.SaveDisposal(ctx, h.write(label), sale, disposing)
		require.NoError(t, err)
	}

	drawn, err := s.ConsumedFromLot(ctx, lot)
	require.NoError(t, err)
	require.Len(t, drawn, 3, "every disposal appended a row; none overwrote another")

	total := mustMoney(t, "BTC", 0)
	for _, c := range drawn {
		total, err = total.Add(c.Quantity)
		require.NoError(t, err)
	}

	stored, err := s.LoadLot(ctx, lot)
	require.NoError(t, err)

	rebuilt, err := stored.Quantity().Sub(total)
	require.NoError(t, err)
	require.True(t, rebuilt.Equal(stored.Remaining()),
		"quantity %s minus %s consumed is %s, but the lot says %s remains",
		stored.Quantity(), total, rebuilt, stored.Remaining())
}

func TestDisposingOfMoreThanIsHeldWritesNothingAtAll(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)

	lot := buy(t, s, h, "small", "2026-01-10", 5_000_000*satoshi, 200_000_00)

	sale, disposing := trade(t, h, "sell-too-much", "2026-06-01", -9_000_000*satoshi, 500_000_00)
	_, err := s.SaveDisposal(ctx, h.write("sell-too-much"), sale, disposing)
	require.ErrorIs(t, err, ledger.ErrInsufficientLots)

	// The whole write is one database transaction, so a refusal leaves no
	// half-finished sale and no partly consumed lot.
	_, err = s.LoadTransaction(ctx, sale.ID())
	require.ErrorIs(t, err, store.ErrNotFound, "the sale must not exist")

	stored, err := s.LoadLot(ctx, lot)
	require.NoError(t, err)
	require.True(t, stored.Remaining().Equal(mustMoney(t, "BTC", 5_000_000*satoshi)),
		"the lot was touched by a disposal that failed")

	drawn, err := s.ConsumedFromLot(ctx, lot)
	require.NoError(t, err)
	require.Empty(t, drawn)
}

func TestADisposalNamingSomethingThatIsNotOneOfItsLinesIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)
	buy(t, s, h, "named", "2026-01-10", 10_000_000*satoshi, 300_000_00)

	sale, disposing := trade(t, h, "sell-named", "2026-06-01", -1_000_000*satoshi, 50_000_00)

	t.Run("a line of another transaction", func(t *testing.T) {
		_, err := s.SaveDisposal(ctx, h.write("stranger"), sale,
			ledger.PostingID(testID("posting:stranger")))
		require.ErrorIs(t, err, store.ErrInvalidWrite)
		require.Contains(t, err.Error(), "is not a line of")
	})

	t.Run("a line that acquires rather than disposes", func(t *testing.T) {
		// The cash line is positive, so consuming lots against it would reduce
		// a holding that just went up.
		_, err := s.SaveDisposal(ctx, h.write("wrong-sign"), sale,
			ledger.PostingID(testID("posting:sell-named:cash")))
		require.ErrorIs(t, err, store.ErrInvalidWrite)
		require.Contains(t, err.Error(), "does not dispose of anything")
	})

	t.Run("no line at all", func(t *testing.T) {
		_, err := s.SaveDisposal(ctx, h.write("none"), sale)
		require.ErrorIs(t, err, store.ErrInvalidWrite)
	})

	t.Run("the same line twice", func(t *testing.T) {
		_, err := s.SaveDisposal(ctx, h.write("twice"), sale, disposing, disposing)
		require.ErrorIs(t, err, store.ErrInvalidWrite)
		require.Contains(t, err.Error(), "twice")
	})

	count, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "only the purchase was ever written")
}

// §5.6. A retried disposal consumes nothing a second time, and still answers
// the question it was asked: a caller whose first attempt timed out needs the
// consumed lots, not an empty map.
func TestReplayingADisposalConsumesNothingTwiceAndStillAnswers(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)

	lot := buy(t, s, h, "replay", "2026-01-10", 10_000_000*satoshi, 600_000_00)

	sale, disposing := trade(t, h, "sell-replay", "2026-06-01", -4_000_000*satoshi, 300_000_00)

	first, err := s.SaveDisposal(ctx, h.write("sell-replay"), sale, disposing)
	require.NoError(t, err)
	require.False(t, first.Replayed)
	require.Len(t, first.Consumed[disposing], 1)

	second, err := s.SaveDisposal(ctx, h.write("sell-replay"), sale, disposing)
	require.NoError(t, err)
	require.True(t, second.Replayed)
	require.Len(t, second.Consumed[disposing], 1,
		"a replay must answer with what the first attempt consumed")
	require.Equal(t, first.Consumed[disposing][0].Lot, second.Consumed[disposing][0].Lot)
	require.Equal(t,
		first.Consumed[disposing][0].Basis.Value().RatString(),
		second.Consumed[disposing][0].Basis.Value().RatString())

	stored, err := s.LoadLot(ctx, lot)
	require.NoError(t, err)
	require.True(t, stored.Remaining().Equal(mustMoney(t, "BTC", 6_000_000*satoshi)),
		"the replay consumed the lot a second time")

	drawn, err := s.ConsumedFromLot(ctx, lot)
	require.NoError(t, err)
	require.Len(t, drawn, 1, "one disposal, one row")
}

// The same transaction written once as a plain entry and once as a disposal
// produces identical rows in transactions and postings, and a completely
// different set of consumed lots. A fingerprint blind to which lines dispose
// would answer the second with the first one's result.
func TestADisposalAndAPlainEntryAreNotTheSameRequest(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)
	buy(t, s, h, "fp", "2026-01-10", 10_000_000*satoshi, 600_000_00)

	sale, disposing := trade(t, h, "sell-fp", "2026-06-01", -4_000_000*satoshi, 300_000_00)

	_, err := s.SaveTransaction(ctx, h.write("shared"), sale)
	require.NoError(t, err)

	_, err = s.SaveDisposal(ctx, h.write("shared"), sale, disposing)
	require.ErrorIs(t, err, store.ErrIdempotencyConflict,
		"one consumes lots and the other does not, so they are different requests")
}

// §10. The audit log records what a disposal consumed, independently of
// lot_consumptions, and says the entry was a disposal rather than a create.
func TestTheAuditLogRecordsWhatADisposalConsumed(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)

	lot := buy(t, s, h, "audit", "2026-01-10", 30_000_000*satoshi, 1_000_000_00)

	sale, disposing := trade(t, h, "sell-audit", "2026-06-01", -10_000_000*satoshi, 500_000_00)
	_, err := s.SaveDisposal(ctx, h.write("sell-audit"), sale, disposing)
	require.NoError(t, err)

	entries, err := s.AuditEntriesFor(ctx, "transaction", string(sale.ID()))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "dispose", entries[0].Action,
		"a sale is not a 'create'; the point of the entry is that a holding went down")

	var diff struct {
		Consumed map[string][]struct {
			Lot       string `json:"lot"`
			Basis     string `json:"basis"`
			Commodity string `json:"basis_commodity"`
		} `json:"consumed"`
	}
	require.NoError(t, json.Unmarshal(entries[0].Diff, &diff))

	drawn := diff.Consumed[string(disposing)]
	require.Len(t, drawn, 1)
	require.Equal(t, string(lot), drawn[0].Lot)
	require.Equal(t, big.NewRat(1_000_000_00, 3).RatString(), drawn[0].Basis,
		"the log must not hold a rounded copy of an unrounded figure")
	require.Equal(t, "IDR", drawn[0].Commodity)
}

// Two disposals of the same holding at the same time must not both succeed
// against the same units.
//
// Getting the two to actually overlap is the whole difficulty. Simply starting
// two goroutines proves nothing: they usually do not collide, and the test then
// passes with the row lock removed — which is what happened the first time this
// guard was broken on purpose. A third transaction holds the lots first, so
// both writers are stopped at the same point before either can commit, and the
// collision is arranged rather than hoped for.
//
// Under a row lock taken at read time, one writer reads, consumes and commits;
// the other then re-reads what is actually left and correctly runs out. Without
// it, MVCC lets both read the original figure — a plain read is never blocked
// by a row lock — and both go on to consume the same units.
func TestTwoDisposalsAtOnceCannotConsumeTheSameUnits(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)

	lot := buy(t, s, h, "race", "2026-01-10", 10_000_000*satoshi, 500_000_00)

	// Each wants 0,06 BTC. Together that is more than the 0,1 held.
	first, firstLine := trade(t, h, "race-a", "2026-06-01", -6_000_000*satoshi, 300_000_00)
	second, secondLine := trade(t, h, "race-b", "2026-06-02", -6_000_000*satoshi, 300_000_00)

	gate, err := s.Pool().Begin(ctx)
	require.NoError(t, err)
	_, err = gate.Exec(ctx, `SELECT id FROM lots WHERE account_id = $1 FOR UPDATE`, string(h.crypto))
	require.NoError(t, err)

	started := make(chan struct{}, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		started <- struct{}{}
		_, errs[0] = s.SaveDisposal(ctx, h.write("race-a"), first, firstLine)
	}()
	go func() {
		defer wg.Done()
		started <- struct{}{}
		_, errs[1] = s.SaveDisposal(ctx, h.write("race-b"), second, secondLine)
	}()

	<-started
	<-started
	// Both are inside SaveDisposal and heading for the same rows. The pause is
	// only to let them reach the database; the gate, not the timing, is what
	// guarantees neither has finished.
	time.Sleep(300 * time.Millisecond)
	require.NoError(t, gate.Rollback(ctx))
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, ledger.ErrInsufficientLots,
			"the loser must lose for the right reason")
	}
	require.Equal(t, 1, succeeded, "exactly one of the two may consume the units")

	// The invariant that actually matters, whatever the two writers did: the
	// appended record and the running figure still describe the same lot.
	stored, err := s.LoadLot(ctx, lot)
	require.NoError(t, err)

	drawn, err := s.ConsumedFromLot(ctx, lot)
	require.NoError(t, err)

	consumed := mustMoney(t, "BTC", 0)
	for _, c := range drawn {
		consumed, err = consumed.Add(c.Quantity)
		require.NoError(t, err)
	}
	require.LessOrEqual(t, consumed.Amount().Cmp(stored.Quantity().Amount()), 0,
		"%s was consumed from a lot that only ever held %s", consumed, stored.Quantity())

	rebuilt, err := stored.Quantity().Sub(consumed)
	require.NoError(t, err)
	require.True(t, rebuilt.Equal(stored.Remaining()),
		"the lot says %s remains, but its history accounts for %s",
		stored.Remaining(), rebuilt)
}

// The schema carries the exactness rule itself, for a writer that never went
// through ledger.Rat.
func TestTheDatabaseRefusesACostBasisThatIsNotInNormalForm(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)

	lot := buy(t, s, h, "direct", "2026-01-10", 10_000_000*satoshi, 400_000_00)
	sale, disposing := trade(t, h, "sell-direct", "2026-06-01", -1_000_000*satoshi, 50_000_00)
	_, err := s.SaveTransaction(ctx, h.write("sell-direct"), sale)
	require.NoError(t, err)

	for _, tc := range []struct {
		name      string
		num, den  string
		commodity string
		// Any one of these naming the refusal is enough. A row can violate
		// more than one of these constraints at once, and PostgreSQL does not
		// promise which it reports — a non-whole numerator is also not in
		// lowest terms, and insisting on one name would make this test depend
		// on evaluation order rather than on the rule.
		wants []string
	}{
		{"a zero denominator", "1", "0", "IDR", []string{"basis_denominator_is_positive"}},
		{"a negative denominator", "1", "-3", "IDR", []string{"basis_denominator_is_positive"}},
		{"a negative basis", "-1", "3", "IDR", []string{"basis_is_not_negative"}},
		{"terms that are not whole", "1.5", "3", "IDR", []string{"basis_terms_are_whole", "basis_is_reduced"}},
		{"a fraction not in lowest terms", "2", "4", "IDR", []string{"basis_is_reduced"}},
		{"a commodity the lot did not cost", "1", "3", "USD", []string{"basis_matches_its_lots_cost"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Pool().Exec(ctx,
				`INSERT INTO lot_consumptions (
				     posting_id, lot_id, quantity_amount, quantity_commodity,
				     basis_num, basis_den, basis_commodity)
				 VALUES ($1, $2, 1, 'BTC', $3::numeric, $4::numeric, $5)`,
				string(disposing), string(lot), tc.num, tc.den, tc.commodity)
			require.Error(t, err, "the schema must refuse this without any help from Go")

			named := false
			for _, want := range tc.wants {
				if strings.Contains(err.Error(), want) {
					named = true
					break
				}
			}
			require.True(t, named, "refused, but by none of %v: %v", tc.wants, err)
		})
	}
}

// A consumption cannot describe a quantity in a commodity its lot does not
// hold, nor point at a lot or a line that is not there.
func TestTheDatabaseRefusesAConsumptionThatDoesNotMatchItsLot(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	h := seedHolding(t, s)

	lot := buy(t, s, h, "mismatch", "2026-01-10", 10_000_000*satoshi, 400_000_00)
	sale, disposing := trade(t, h, "sell-mismatch", "2026-06-01", -1_000_000*satoshi, 50_000_00)
	_, err := s.SaveTransaction(ctx, h.write("sell-mismatch"), sale)
	require.NoError(t, err)

	t.Run("a quantity in the wrong commodity", func(t *testing.T) {
		_, err := s.Pool().Exec(ctx,
			`INSERT INTO lot_consumptions (
			     posting_id, lot_id, quantity_amount, quantity_commodity,
			     basis_num, basis_den, basis_commodity)
			 VALUES ($1, $2, 1, 'USD', 1, 3, 'IDR')`,
			string(disposing), string(lot))
		require.Error(t, err)
		require.Contains(t, err.Error(), "quantity_matches_its_lot")
	})

	t.Run("a lot that is not there", func(t *testing.T) {
		_, err := s.Pool().Exec(ctx,
			`INSERT INTO lot_consumptions (
			     posting_id, lot_id, quantity_amount, quantity_commodity,
			     basis_num, basis_den, basis_commodity)
			 VALUES ($1, $2, 1, 'BTC', 1, 3, 'IDR')`,
			string(disposing), testID("lot:absent"))
		require.Error(t, err)
	})

	t.Run("a zero quantity", func(t *testing.T) {
		_, err := s.Pool().Exec(ctx,
			`INSERT INTO lot_consumptions (
			     posting_id, lot_id, quantity_amount, quantity_commodity,
			     basis_num, basis_den, basis_commodity)
			 VALUES ($1, $2, 0, 'BTC', 1, 3, 'IDR')`,
			string(disposing), string(lot))
		require.Error(t, err)
		require.Contains(t, err.Error(), "quantity_is_positive")
	})
}
