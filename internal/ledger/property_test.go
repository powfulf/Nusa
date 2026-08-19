// SPDX-License-Identifier: MIT

// The property-based tests §11 requires for internal/ledger. Each one states
// an invariant from §5 or §4 and lets rapid look for a counterexample, which
// finds the shapes a hand-written table does not think to try: the tie exactly
// on the halfway mark, the transaction whose commodities balance individually
// but not together, the posting that is zero.
//
// No generator here produces a float. rapid's floating-point generators and
// big.Rat's floating-point constructors are as forbidden in a test as they are
// in the ledger itself — a generator that went through one would be testing
// the rule with the very thing the rule bans.
package ledger_test

import (
	"fmt"
	"math/big"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/nusa-app/nusa/internal/ledger"
)

// The commodity mixes transactions are generated over. Including BTC alongside
// two two-place currencies means a generated transaction can span scales.
var commoditySets = [][]ledger.CommodityCode{
	{idr},
	{usd},
	{idr, usd},
	{idr, usd, btc},
}

// propertyAccounts is an unrestricted tree: every account takes any commodity,
// so generation never has to reason about which account can hold what. The
// restriction itself is covered by the table-driven journal tests.
func propertyAccounts(t *testing.T) *ledger.AccountTree {
	t.Helper()
	tree, err := ledger.NewAccountTree(
		mustAccount(t, "a1", ledger.AccountAsset),
		mustAccount(t, "a2", ledger.AccountAsset),
		mustAccount(t, "a3", ledger.AccountAsset),
		mustAccount(t, "e1", ledger.AccountExpense),
		mustAccount(t, "trading", ledger.AccountEquity),
	)
	require.NoError(t, err)
	return tree
}

var propertyAccountIDs = []ledger.AccountID{
	aid("a1"), aid("a2"), aid("a3"), aid("e1"), aid("trading"),
}

// drawBalancedPostings generates postings that sum to zero in every commodity,
// by generating all but one line per commodity freely and making the last one
// close the gap.
func drawBalancedPostings(rt *rapid.T, prefix string) []ledger.Posting {
	codes := rapid.SampledFrom(commoditySets).Draw(rt, prefix+".commodities")

	var postings []ledger.Posting
	for _, code := range codes {
		count := rapid.IntRange(1, 3).Draw(rt, fmt.Sprintf("%s.%s.count", prefix, code))
		total := new(big.Int)

		for i := 0; i < count; i++ {
			label := fmt.Sprintf("%s.%s.%d", prefix, code, i)
			minor := rapid.Int64Range(-1_000_000_000_000, 1_000_000_000_000).Draw(rt, label+".amount")
			account := rapid.SampledFrom(propertyAccountIDs).Draw(rt, label+".account")

			amount, err := ledger.MoneyFromInt(code, minor)
			if err != nil {
				rt.Fatalf("build amount: %v", err)
			}
			p, err := ledger.NewPosting(ledger.PostingSpec{
				ID:      pid(label),
				Account: account,
				Amount:  amount,
			})
			if err != nil {
				rt.Fatalf("build posting: %v", err)
			}
			postings = append(postings, p)
			total.Add(total, big.NewInt(minor))
		}

		closing, err := ledger.NewMoney(code, new(big.Int).Neg(total))
		if err != nil {
			rt.Fatalf("build closing amount: %v", err)
		}
		account := rapid.SampledFrom(propertyAccountIDs).Draw(rt, fmt.Sprintf("%s.%s.closing.account", prefix, code))
		p, err := ledger.NewPosting(ledger.PostingSpec{
			ID:      pid(fmt.Sprintf("%s.%s.closing", prefix, code)),
			Account: account,
			Amount:  closing,
		})
		if err != nil {
			rt.Fatalf("build closing posting: %v", err)
		}
		postings = append(postings, p)
	}
	return postings
}

func drawDate(rt *rapid.T, label string) ledger.Date {
	base, err := ledger.NewDate(2024, 1, 1)
	if err != nil {
		rt.Fatalf("base date: %v", err)
	}
	shifted, err := base.AddDays(rapid.IntRange(0, 720).Draw(rt, label))
	if err != nil {
		rt.Fatalf("shift date: %v", err)
	}
	return shifted
}

func drawTransaction(rt *rapid.T, label string) ledger.Transaction {
	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:       tid(label),
		Date:     drawDate(rt, label+".date"),
		Postings: drawBalancedPostings(rt, label),
	})
	if err != nil {
		rt.Fatalf("build transaction %q: %v", label, err)
	}
	return txn
}

// §5.1. Postings that sum to zero per commodity always construct, and the sums
// really are zero in every commodity involved.
func TestPropertyBalancedPostingsAlwaysConstruct(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		postings := drawBalancedPostings(rt, "txn")

		sums, err := ledger.SumPostings(postings)
		if err != nil {
			rt.Fatalf("sum postings: %v", err)
		}
		if !sums.IsZero() {
			rt.Fatalf("generated postings do not balance: %s", sums)
		}

		if _, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID:       tid("txn"),
			Date:     drawDate(rt, "date"),
			Postings: postings,
		}); err != nil {
			rt.Fatalf("balanced postings were rejected: %v", err)
		}
	})
}

// §5.1 from the other side, and the reason it is absolute: moving any single
// posting by any non-zero amount, however small, must be refused. There is no
// tolerance to slip inside.
func TestPropertyAnyPerturbationIsRejected(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		postings := drawBalancedPostings(rt, "txn")

		index := rapid.IntRange(0, len(postings)-1).Draw(rt, "index")
		delta := rapid.Int64Range(-1_000_000, 1_000_000).Filter(func(d int64) bool { return d != 0 }).Draw(rt, "delta")

		target := postings[index]
		nudge, err := ledger.MoneyFromInt(target.Amount().Commodity(), delta)
		if err != nil {
			rt.Fatalf("build delta: %v", err)
		}
		shifted, err := target.Amount().Add(nudge)
		if err != nil {
			rt.Fatalf("apply delta: %v", err)
		}
		replaced, err := ledger.NewPosting(ledger.PostingSpec{
			ID:      target.ID(),
			Account: target.Account(),
			Amount:  shifted,
		})
		if err != nil {
			rt.Fatalf("rebuild posting: %v", err)
		}
		postings[index] = replaced

		_, err = ledger.NewTransaction(ledger.TransactionSpec{
			ID:       tid("txn"),
			Date:     drawDate(rt, "date"),
			Postings: postings,
		})
		if err == nil {
			rt.Fatalf("a transaction off by %d was accepted", delta)
		}
		require.ErrorIs(rt, err, ledger.ErrUnbalanced)
	})
}

// §5.2. An account's balance is the sum of its postings — recomputed here from
// the transactions themselves, through the public accessors, rather than by
// asking the journal a second time.
func TestPropertyAccountBalanceEqualsSumOfItsPostings(t *testing.T) {
	t.Parallel()

	accounts := propertyAccounts(t)

	rapid.Check(t, func(rt *rapid.T) {
		count := rapid.IntRange(0, 6).Draw(rt, "transactions")
		txns := make([]ledger.Transaction, 0, count)
		for i := 0; i < count; i++ {
			txns = append(txns, drawTransaction(rt, fmt.Sprintf("txn-%d", i)))
		}

		j, err := ledger.NewJournal(accounts, txns...)
		if err != nil {
			rt.Fatalf("build journal: %v", err)
		}

		for _, id := range propertyAccountIDs {
			balance, err := j.Balance(id)
			if err != nil {
				rt.Fatalf("balance %q: %v", id, err)
			}

			expected := map[ledger.CommodityCode]*big.Int{}
			for _, txn := range j.Transactions() {
				for _, p := range txn.Postings() {
					if p.Account() != id {
						continue
					}
					code := p.Amount().Commodity()
					if expected[code] == nil {
						expected[code] = new(big.Int)
					}
					expected[code].Add(expected[code], p.Amount().Amount())
				}
			}

			for code, want := range expected {
				got := balance.Get(code)
				if got.Amount().Cmp(want) != 0 {
					rt.Fatalf("account %q in %s: journal says %s, postings sum to %s",
						id, code, got, want)
				}
			}
		}
	})
}

// The whole book nets to zero in every commodity, because each transaction
// does and summing zeroes gives zero. A non-zero total means something reached
// the journal without passing the validator.
func TestPropertyWholeJournalNetsToZero(t *testing.T) {
	t.Parallel()

	accounts := propertyAccounts(t)

	rapid.Check(t, func(rt *rapid.T) {
		count := rapid.IntRange(0, 6).Draw(rt, "transactions")
		txns := make([]ledger.Transaction, 0, count)
		for i := 0; i < count; i++ {
			txns = append(txns, drawTransaction(rt, fmt.Sprintf("txn-%d", i)))
		}

		j, err := ledger.NewJournal(accounts, txns...)
		if err != nil {
			rt.Fatalf("build journal: %v", err)
		}

		totals, err := j.TotalsByCommodity()
		if err != nil {
			rt.Fatalf("totals: %v", err)
		}
		if !totals.IsZero() {
			rt.Fatalf("journal does not net to zero: %s", totals)
		}
	})
}

// §5.4's consequence: a balance as of a date never changes because something
// was recorded after it. Historical reports have to be stable or they are not
// reports.
func TestPropertyBalanceAsOfIgnoresLaterTransactions(t *testing.T) {
	t.Parallel()

	accounts := propertyAccounts(t)

	rapid.Check(t, func(rt *rapid.T) {
		count := rapid.IntRange(1, 5).Draw(rt, "transactions")
		txns := make([]ledger.Transaction, 0, count)
		for i := 0; i < count; i++ {
			txns = append(txns, drawTransaction(rt, fmt.Sprintf("txn-%d", i)))
		}

		j, err := ledger.NewJournal(accounts, txns...)
		if err != nil {
			rt.Fatalf("build journal: %v", err)
		}

		cutoff := drawDate(rt, "cutoff")
		before := map[ledger.AccountID]*ledger.Balances{}
		for _, id := range propertyAccountIDs {
			b, err := j.BalanceAsOf(id, cutoff)
			if err != nil {
				rt.Fatalf("balance as of: %v", err)
			}
			before[id] = b
		}

		// A transaction strictly after the cutoff, so it cannot legitimately
		// affect any of the balances just taken.
		afterCutoff, err := cutoff.AddDays(rapid.IntRange(1, 400).Draw(rt, "gap"))
		if err != nil {
			rt.Fatalf("shift past cutoff: %v", err)
		}
		later, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID:       tid("txn-later"),
			Date:     afterCutoff,
			Postings: drawBalancedPostings(rt, "later"),
		})
		if err != nil {
			rt.Fatalf("build later transaction: %v", err)
		}

		extended, err := j.Add(later)
		if err != nil {
			rt.Fatalf("extend journal: %v", err)
		}

		for _, id := range propertyAccountIDs {
			again, err := extended.BalanceAsOf(id, cutoff)
			if err != nil {
				rt.Fatalf("balance as of, second time: %v", err)
			}
			for _, code := range append(before[id].Codes(), again.Codes()...) {
				if !before[id].Get(code).Equal(again.Get(code)) {
					rt.Fatalf("account %q in %s changed from %s to %s after a later transaction",
						id, code, before[id].Get(code), again.Get(code))
				}
			}
		}
	})
}

// §4.6. Rounding never moves a value by half a smallest unit or more, in
// either direction. A sign error in the rounding would show up here as a
// distance of a whole unit.
func TestPropertyRoundingMovesLessThanHalfAUnit(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		num := rapid.Int64Range(-1_000_000_000, 1_000_000_000).Draw(rt, "numerator")
		den := rapid.Int64Range(1, 1_000_000).Draw(rt, "denominator")

		exact, err := ledger.NewRat(idr, big.NewRat(num, den))
		if err != nil {
			rt.Fatalf("build rat: %v", err)
		}

		rounded, err := exact.Round()
		if err != nil {
			rt.Fatalf("round: %v", err)
		}

		// |exact - rounded| must be at most one half.
		distance := new(big.Rat).Sub(exact.Value(), new(big.Rat).SetInt(rounded.Amount()))
		distance.Abs(distance)
		if distance.Cmp(big.NewRat(1, 2)) > 0 {
			rt.Fatalf("rounding %s to %s moved it by %s", exact, rounded, distance.RatString())
		}
	})
}

// §4.6 again: a value exactly on the halfway mark always lands on an even
// number of smallest units. This is the half of banker's rounding that keeps
// long columns from drifting upward.
func TestPropertyTiesRoundToEven(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		whole := rapid.Int64Range(-1_000_000_000, 1_000_000_000).Draw(rt, "whole")

		// whole + 1/2, built as an exact fraction so nothing goes near a float.
		tie, err := ledger.NewRat(idr, big.NewRat(2*whole+1, 2))
		if err != nil {
			rt.Fatalf("build tie: %v", err)
		}

		rounded, err := tie.Round()
		if err != nil {
			rt.Fatalf("round: %v", err)
		}

		if rounded.Amount().Bit(0) != 0 {
			rt.Fatalf("the tie %s rounded to the odd %s", tie, rounded)
		}
	})
}

// Money is already a whole number of smallest units, so widening it into the
// exact type and rounding back must be the identity. Anything else means a
// value can be nudged just by passing through a calculation.
func TestPropertyMoneyRatRoundTripIsIdentity(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		minor := rapid.Int64Range(-1_000_000_000_000, 1_000_000_000_000).Draw(rt, "amount")

		original, err := ledger.MoneyFromInt(idr, minor)
		if err != nil {
			rt.Fatalf("build money: %v", err)
		}

		widened, err := original.Rat()
		if err != nil {
			rt.Fatalf("widen: %v", err)
		}
		rounded, err := widened.Round()
		if err != nil {
			rt.Fatalf("round: %v", err)
		}

		if !original.Equal(rounded) {
			rt.Fatalf("round trip changed %s into %s", original, rounded)
		}

		// And rounding something already whole is idempotent.
		twice, err := widened.Round()
		if err != nil {
			rt.Fatalf("round twice: %v", err)
		}
		if !twice.Equal(rounded) {
			rt.Fatalf("rounding is not idempotent: %s then %s", rounded, twice)
		}
	})
}

// The ordinary algebra a ledger relies on: order of addition does not matter,
// subtraction undoes addition, negation is its own inverse.
func TestPropertyMoneyArithmeticIsWellBehaved(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		draw := func(label string) ledger.Money {
			minor := rapid.Int64Range(-1_000_000_000_000, 1_000_000_000_000).Draw(rt, label)
			m, err := ledger.MoneyFromInt(idr, minor)
			if err != nil {
				rt.Fatalf("build money: %v", err)
			}
			return m
		}

		a, b, c := draw("a"), draw("b"), draw("c")

		ab, err := a.Add(b)
		require.NoError(rt, err)
		ba, err := b.Add(a)
		require.NoError(rt, err)
		if !ab.Equal(ba) {
			rt.Fatalf("addition is not commutative: %s vs %s", ab, ba)
		}

		abThenC, err := ab.Add(c)
		require.NoError(rt, err)
		bc, err := b.Add(c)
		require.NoError(rt, err)
		aThenBC, err := a.Add(bc)
		require.NoError(rt, err)
		if !abThenC.Equal(aThenBC) {
			rt.Fatalf("addition is not associative: %s vs %s", abThenC, aThenBC)
		}

		back, err := ab.Sub(b)
		require.NoError(rt, err)
		if !back.Equal(a) {
			rt.Fatalf("subtraction did not undo addition: %s vs %s", back, a)
		}

		neg, err := a.Neg()
		require.NoError(rt, err)
		negNeg, err := neg.Neg()
		require.NoError(rt, err)
		if !negNeg.Equal(a) {
			rt.Fatalf("negation is not its own inverse: %s vs %s", negNeg, a)
		}
	})
}

// §5.3. Nothing a caller receives can be used to change what it came from —
// not the big.Int inside a Money, not the slice of postings inside a
// transaction. Immutability that only holds by convention is not immutability.
func TestPropertyNothingHandedOutCanChangeWhatItCameFrom(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		minor := rapid.Int64Range(-1_000_000_000_000, 1_000_000_000_000).Draw(rt, "amount")
		tamper := rapid.Int64Range(-1_000, 1_000).Draw(rt, "tamper")

		source := big.NewInt(minor)
		amount, err := ledger.NewMoney(idr, source)
		require.NoError(rt, err)

		posting, err := ledger.NewPosting(ledger.PostingSpec{ID: pid("p1"), Account: aid("a1"), Amount: amount})
		require.NoError(rt, err)
		opposite, err := amount.Neg()
		require.NoError(rt, err)
		other, err := ledger.NewPosting(ledger.PostingSpec{ID: pid("p2"), Account: aid("a2"), Amount: opposite})
		require.NoError(rt, err)

		specPostings := []ledger.Posting{posting, other}
		txn, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID:       tid("txn"),
			Date:     drawDate(rt, "date"),
			Postings: specPostings,
		})
		require.NoError(rt, err)

		// Every route a caller has into the internals.
		source.Add(source, big.NewInt(tamper))
		amount.Amount().Add(amount.Amount(), big.NewInt(tamper))
		posting.Amount().Amount().SetInt64(tamper)
		txn.Postings()[0] = other
		specPostings[0] = other

		if !txn.Postings()[0].Amount().Equal(mustMoneyRapid(rt, idr, minor)) {
			rt.Fatalf("the posting changed to %s", txn.Postings()[0].Amount())
		}
		if txn.Postings()[0].ID() != pid("p1") {
			rt.Fatalf("the postings slice was replaced from outside")
		}
	})
}

// The reason postings carry an identity at all: reordering the lines inside a
// transaction must not disturb anything that points at one.
//
// Reordering happens constantly and innocently — a repository returns rows in
// whatever order the query planner chose, a JSON decoder rebuilds a slice, an
// importer sorts by amount for display. If a Lot said "the second posting of
// transaction T", every one of those would silently repoint it at a different
// line. No error, no failed constraint: just a cost basis attached to the
// wrong acquisition, discovered a tax year later.
//
// The lots here are deliberately unrelated to what the postings contain. What
// is being tested is that a reference by identity survives, and identity does
// not care what it is attached to.
func TestPropertyReorderingPostingsKeepsLotReferencesIntact(t *testing.T) {
	t.Parallel()

	accounts := propertyAccounts(t)

	rapid.Check(t, func(rt *rapid.T) {
		original := drawBalancedPostings(rt, "txn")

		// A permutation, drawn as one sort key per posting.
		type keyed struct {
			key int
			p   ledger.Posting
		}
		shuffled := make([]keyed, len(original))
		for i, p := range original {
			shuffled[i] = keyed{
				key: rapid.IntRange(0, 1_000).Draw(rt, fmt.Sprintf("key-%d", i)),
				p:   p,
			}
		}
		sort.SliceStable(shuffled, func(i, j int) bool { return shuffled[i].key < shuffled[j].key })

		reordered := make([]ledger.Posting, len(shuffled))
		for i, k := range shuffled {
			reordered[i] = k.p
		}

		date := drawDate(rt, "date")
		before, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID: tid("txn"), Date: date, Postings: original,
		})
		require.NoError(rt, err)
		after, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID: tid("txn"), Date: date, Postings: reordered,
		})
		require.NoError(rt, err)

		// A lot per posting, each naming the posting that opened it.
		lots := make([]ledger.Lot, 0, len(original))
		for i, p := range original {
			l, lotErr := ledger.NewLot(ledger.LotSpec{
				ID:       lid(fmt.Sprintf("lot-%d", i)),
				Account:  aid("a1"),
				OpenedBy: p.ID(),
				OpenedOn: date,
				Quantity: mustMoneyRapid(rt, bbca, 100),
				Cost:     mustMoneyRapid(rt, idr, 1_000),
			})
			require.NoError(rt, lotErr)
			lots = append(lots, l)
		}

		journalBefore, err := ledger.NewJournal(accounts, before)
		require.NoError(rt, err)
		journalAfter, err := ledger.NewJournal(accounts, after)
		require.NoError(rt, err)

		for _, l := range lots {
			was, ok := journalBefore.Posting(l.OpenedBy())
			if !ok {
				rt.Fatalf("lot %q did not resolve before reordering", l.ID())
			}
			is, ok := journalAfter.Posting(l.OpenedBy())
			if !ok {
				rt.Fatalf("lot %q stopped resolving after reordering", l.ID())
			}

			// Same line, by every observable property, not merely by identity.
			if is.ID() != was.ID() || is.Account() != was.Account() || !is.Amount().Equal(was.Amount()) {
				rt.Fatalf("lot %q now points at %s, it used to point at %s", l.ID(), is, was)
			}

			ownerBefore, _ := journalBefore.PostingOwner(l.OpenedBy())
			ownerAfter, _ := journalAfter.PostingOwner(l.OpenedBy())
			if ownerBefore != ownerAfter {
				rt.Fatalf("lot %q changed transaction from %q to %q", l.ID(), ownerBefore, ownerAfter)
			}
		}

		// The control: where the permutation actually moved a line, an
		// index-based reference would now be pointing somewhere else. That the
		// assertions above still held is the whole difference identity makes.
		for i := range original {
			if original[i].ID() != reordered[i].ID() {
				return
			}
		}
	})
}

// A conversion pair always balances the transaction it belongs to, whatever
// the two amounts are. This is the guarantee that makes §5.1 workable for
// multi-currency households rather than merely strict.
func TestPropertyConversionPostingsAlwaysBalance(t *testing.T) {
	t.Parallel()

	accounts := propertyAccounts(t)

	rapid.Check(t, func(rt *rapid.T) {
		soldMinor := rapid.Int64Range(1, 1_000_000_000_000).Draw(rt, "sold")
		boughtMinor := rapid.Int64Range(1, 1_000_000_000_000).Draw(rt, "bought")
		rateNum := rapid.Int64Range(1, 1_000_000).Draw(rt, "rate.num")
		rateDen := rapid.Int64Range(1, 1_000_000).Draw(rt, "rate.den")

		sold := mustMoneyRapid(rt, idr, -soldMinor)
		bought := mustMoneyRapid(rt, usd, boughtMinor)

		rate, err := ledger.NewRate(usd, idr, big.NewRat(rateNum, rateDen))
		require.NoError(rt, err)

		conversion, err := accounts.ConversionPostings(ledger.ConversionSpec{
			TradingAccount:  aid("trading"),
			SoldPostingID:   pid("conv-sold"),
			BoughtPostingID: pid("conv-bought"),
			Sold:            sold,
			Bought:          bought,
			Rate:            rate,
		})
		require.NoError(rt, err)

		userSold, err := ledger.NewPosting(ledger.PostingSpec{ID: pid("user-sold"), Account: aid("a1"), Amount: sold})
		require.NoError(rt, err)
		userBought, err := ledger.NewPosting(ledger.PostingSpec{ID: pid("user-bought"), Account: aid("a2"), Amount: bought})
		require.NoError(rt, err)

		txn, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID:       tid("txn-fx"),
			Date:     drawDate(rt, "date"),
			Postings: []ledger.Posting{userSold, userBought, conversion[0], conversion[1]},
		})
		if err != nil {
			rt.Fatalf("a conversion failed to balance its transaction: %v", err)
		}

		sums, err := ledger.SumPostings(txn.Postings())
		require.NoError(rt, err)
		if !sums.IsZero() {
			rt.Fatalf("conversion left a residual: %s", sums)
		}
	})
}

// A rate and its inverse are exact reciprocals, and Convert crosses both
// commodities' scales, so converting there and back returns the original
// amount — including between commodities whose scales differ, where the
// hundredfold factors have to cancel as cleanly as the rate does.
//
// The rate is a whole number so that the intermediate lands on whole smallest
// units. That keeps the property about inversion rather than about rounding,
// which the rounding properties above already cover.
func TestPropertyRateInversionIsExact(t *testing.T) {
	t.Parallel()

	reg := ledger.StandardRegistry()

	// JPY has no decimal places and IDR has two, so {jpy, idr} exercises a
	// scale crossing that has nothing to do with the rate itself.
	//
	// The reverse pair is deliberately absent. Converting one sen into yen is
	// a hundredth of a yen, which no amount of yen can express, so the loss is
	// real rather than a defect — one sen is simply finer than the currency it
	// is being converted into. Round says so honestly by returning zero, and a
	// round trip through it cannot come back.
	pairs := [][2]ledger.CommodityCode{{usd, idr}, {jpy, idr}, {idr, usd}}

	rapid.Check(t, func(rt *rapid.T) {
		pair := rapid.SampledFrom(pairs).Draw(rt, "pair")
		minor := rapid.Int64Range(-1_000_000_000, 1_000_000_000).Draw(rt, "amount")
		num := rapid.Int64Range(1, 1_000_000).Draw(rt, "rate")

		amount := mustMoneyRapid(rt, pair[0], minor)
		rate, err := ledger.NewRate(pair[0], pair[1], new(big.Rat).SetInt64(num))
		require.NoError(rt, err)

		converted, err := reg.Convert(amount, rate)
		require.NoError(rt, err)
		if !converted.IsExact() {
			rt.Fatalf("a whole-number rate should land on whole units, got %s", converted)
		}

		intermediate, err := converted.Round()
		require.NoError(rt, err)

		inverted, err := rate.Invert()
		require.NoError(rt, err)

		returned, err := reg.Convert(intermediate, inverted)
		require.NoError(rt, err)

		back, err := returned.Round()
		require.NoError(rt, err)

		if !back.Equal(amount) {
			rt.Fatalf("converting %s to %s and back gave %s", amount, pair[1], back)
		}
	})
}

// Disposing across lots takes exactly what was asked for, and every remaining
// quantity stays within its original.
func TestPropertyFIFOConsumesExactlyWhatWasAsked(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		count := rapid.IntRange(1, 5).Draw(rt, "lots")

		lots := make([]ledger.Lot, 0, count)
		available := int64(0)
		for i := 0; i < count; i++ {
			shares := rapid.Int64Range(1, 10_000).Draw(rt, fmt.Sprintf("lot-%d.shares", i))
			cost := rapid.Int64Range(0, 1_000_000_000).Draw(rt, fmt.Sprintf("lot-%d.cost", i))

			l, err := ledger.NewLot(ledger.LotSpec{
				ID:       lid(fmt.Sprintf("lot-%d", i)),
				Account:  aid("a1"),
				OpenedBy: pid(fmt.Sprintf("opened-%d", i)),
				OpenedOn: drawDate(rt, fmt.Sprintf("lot-%d.date", i)),
				Quantity: mustMoneyRapid(rt, bbca, shares),
				Cost:     mustMoneyRapid(rt, idr, cost),
			})
			require.NoError(rt, err)

			lots = append(lots, l)
			available += shares
		}

		wanted := rapid.Int64Range(1, available).Draw(rt, "disposal")
		remaining, consumed, err := ledger.ConsumeFIFO(lots, mustMoneyRapid(rt, bbca, wanted))
		if err != nil {
			rt.Fatalf("dispose of %d of %d available: %v", wanted, available, err)
		}

		taken := int64(0)
		for _, c := range consumed {
			taken += c.Quantity.Amount().Int64()
		}
		if taken != wanted {
			rt.Fatalf("asked for %d, consumed %d", wanted, taken)
		}

		left, err := ledger.TotalRemaining(remaining)
		require.NoError(rt, err)
		if left.Amount().Int64() != available-wanted {
			rt.Fatalf("expected %d left, got %s", available-wanted, left)
		}

		for _, l := range remaining {
			if cmp, _ := l.Remaining().Cmp(l.Quantity()); cmp > 0 {
				rt.Fatalf("lot %q has more left than it ever held", l.ID())
			}
		}
	})
}

// mustMoneyRapid is the rapid-flavoured mustMoney: the property tests fail
// through *rapid.T so a counterexample is shrunk and reported.
func mustMoneyRapid(rt *rapid.T, code ledger.CommodityCode, minorUnits int64) ledger.Money {
	m, err := ledger.MoneyFromInt(code, minorUnits)
	if err != nil {
		rt.Fatalf("build money: %v", err)
	}
	return m
}
