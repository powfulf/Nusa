// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/powfulf/Nusa/internal/ledger"
	"github.com/powfulf/Nusa/internal/store"
)

// What a round trip has to survive.
//
// Everything else in this package tests a shape that was chosen because it was
// easy to read. This tests the shapes nobody thought of: amounts far beyond
// what an int64 holds, rates that are exact fractions and not decimals,
// several commodities inside one transaction, lots already partly consumed,
// and postings in whatever order they were written.
//
// No generator here reaches a monetary value through an IEEE 754 double.
// Section 4.1 binds test code too, and a fixture that gets to an amount
// through a float is not verifying the rule — it is demonstrating the one way
// the rule gets broken. Amounts are drawn as bytes and read as big.Int.

// The commodities the property suite uses. ETH is not decoration: its scale is
// 18, which is where a value that survives every other test starts losing
// digits.
var propertyCommodities = []ledger.CommodityCode{"IDR", "USD", "JPY", "BTC", "ETH"}

// amountBytes bounds a drawn amount to 15 bytes, under 2^120, which is about
// 37 decimal digits. Four of those still sum to fewer than the 40 digits
// numeric(40,0) holds, so a balanced transaction can never overflow the column
// the rule in section 4.4 requires.
const amountBytes = 15

type propertyBooks struct {
	actor    string
	accounts []ledger.Account
}

// accountsFor returns the accounts that may hold a commodity: the
// unrestricted ones plus any restricted to exactly this commodity.
func (b propertyBooks) accountsFor(code ledger.CommodityCode) []ledger.AccountID {
	var out []ledger.AccountID
	for _, a := range b.accounts {
		if a.Accepts(code) {
			out = append(out, a.ID())
		}
	}
	return out
}

// WHAT THIS GENERATOR VARIES, FIELD BY FIELD.
//
// The list is here because a property test proves nothing about a field its
// generator never fills: a value that is always zero is compared perfectly and
// says nothing (§11). Green is not the check — this list is, read against the
// domain types.
//
// A field added to ledger.Transaction, Posting, Account or Lot will not add
// itself here, and nothing will go red when it does not. Whoever adds one
// updates this list and the generator together, or the suite quietly starts
// promising more than it tests.
//
//	Transaction  ID          varied, one per case
//	             Date        varied — drawDate
//	             OccurredAt  varied, with a nanosecond component
//	             Timezone    varied, including empty
//	             Payee       varied — drawText
//	             Memo        varied — drawText
//	             Postings    varied in count, commodity and account
//	             Reverses    NOT VARIED — see below
//	             ReversalKind NOT VARIED — see below
//
//	Posting      ID          varied, one per line
//	             Account     varied — sampled from the seeded accounts
//	             Amount      varied — sign and magnitude, up to amountBytes
//	             Rate        varied, and absent about half the time
//	             Memo        varied, and absent about half the time
//	             Reverses    NOT VARIED — see below
//
//	Account      ID          fixed set, seeded once per case
//	             Parent      one child, so a subtree has something beneath it
//	             Kind        all five, so every stored name is read back
//	             Name        fixed strings
//	             Commodity   both restricted and unrestricted
//	             Closed      both, so the column is not write-only
//
//	Lot          ID          varied, one per case
//	             Account     follows the opening posting
//	             OpenedBy    follows the opening posting
//	             OpenedOn    follows the transaction's date
//	             Quantity    varied
//	             Cost        varied
//	             Remaining   varied, usually partly consumed
//
// The two NOT VARIED entries are deliberate and are not a gap left open by
// accident: this generator produces no reversals, so requireSameTransaction
// makes no assertion about reversal links — an assertion over data that cannot
// contain the thing is the trap §11 describes. Reversals are covered by their
// own store test, against values built for the purpose.
//
// Account.Closed and the liability and income kinds were absent until phase 2,
// and their absence was found by audit rather than by a red test: writing
// every account as open, and dropping liability from the kind mapping, both
// left the whole suite green.

func seedPropertyBooks(t *testing.T, s *store.Store) propertyBooks {
	t.Helper()
	ctx := context.Background()

	b := propertyBooks{actor: testID("user:property")}
	require.NoError(t, s.SaveUser(ctx, b.actor))

	specs := []ledger.AccountSpec{
		// Unrestricted, which is what a brokerage account holding cash and
		// shares at once actually looks like.
		{ID: ledger.AccountID(testID("prop:any:1")), Kind: ledger.AccountAsset, Name: "Mixed one"},
		{ID: ledger.AccountID(testID("prop:any:2")), Kind: ledger.AccountAsset, Name: "Mixed two"},
		{ID: ledger.AccountID(testID("prop:any:3")), Kind: ledger.AccountExpense, Name: "Mixed spend"},
		{ID: ledger.AccountID(testID("prop:equity")), Kind: ledger.AccountEquity, Name: "Trading"},
		// Liability and income exist so that every name accountKind maps is
		// written and read back at least once. Until phase 2 only asset,
		// expense and equity ever were, so dropping liability from that
		// mapping left the suite green.
		{ID: ledger.AccountID(testID("prop:liability")), Kind: ledger.AccountLiability, Name: "Owed"},
		{ID: ledger.AccountID(testID("prop:income")), Kind: ledger.AccountIncome, Name: "Earned"},
		// A closed account, because closed is a column like any other and
		// nothing was checking that it survived the round trip. It keeps its
		// history and its balance; it is only hidden from pickers, so a
		// posting may still land in it here.
		{
			ID: ledger.AccountID(testID("prop:closed")), Kind: ledger.AccountAsset,
			Name: "Closed one", Closed: true,
		},
	}
	// One account restricted to each commodity, so the commodity-acceptance
	// path is exercised too and not only the permissive one.
	for _, code := range propertyCommodities {
		specs = append(specs, ledger.AccountSpec{
			ID:        ledger.AccountID(testID("prop:only:" + string(code))),
			Kind:      ledger.AccountAsset,
			Name:      "Only " + string(code),
			Commodity: code,
		})
	}
	// A child, so a subtree balance has something beneath it to find.
	specs = append(specs, ledger.AccountSpec{
		ID:     ledger.AccountID(testID("prop:any:1:child")),
		Parent: ledger.AccountID(testID("prop:any:1")),
		Kind:   ledger.AccountAsset,
		Name:   "Under mixed one",
	})

	for _, spec := range specs {
		account, err := ledger.NewAccount(spec)
		require.NoError(t, err)
		require.NoError(t, s.SaveAccount(ctx, account), "seed %s", spec.Name)
		b.accounts = append(b.accounts, account)
	}
	return b
}

// drawBigInt draws a non-negative integer of up to amountBytes bytes, without
// passing through a float at any point.
func drawBigInt(rt *rapid.T, label string) *big.Int {
	raw := rapid.SliceOfN(rapid.Byte(), 1, amountBytes).Draw(rt, label)
	return new(big.Int).SetBytes(raw)
}

// drawSignedBigInt draws an integer that may be negative or zero.
func drawSignedBigInt(rt *rapid.T, label string) *big.Int {
	v := drawBigInt(rt, label)
	if rapid.Bool().Draw(rt, label+":negative") {
		v.Neg(v)
	}
	return v
}

// drawRate draws an exact positive fraction. big.Rat reduces on construction,
// so what comes out is already in lowest terms — which is what the database
// insists on storing.
func drawRate(rt *rapid.T, label string, base ledger.CommodityCode) ledger.Rate {
	var quote ledger.CommodityCode
	for {
		quote = rapid.SampledFrom(propertyCommodities).Draw(rt, label+":quote")
		if quote != base {
			break
		}
	}

	num := drawBigInt(rt, label+":num")
	den := drawBigInt(rt, label+":den")
	// A rate must be positive, so neither term may be zero.
	if num.Sign() == 0 {
		num = big.NewInt(1)
	}
	if den.Sign() == 0 {
		den = big.NewInt(1)
	}

	rate, err := ledger.NewRate(base, quote, new(big.Rat).SetFrac(num, den))
	if err != nil {
		panic(fmt.Sprintf("drawRate built an invalid rate: %v", err))
	}
	return rate
}

func drawDate(rt *rapid.T, label string) ledger.Date {
	d, err := ledger.NewDate(
		rapid.IntRange(2020, 2030).Draw(rt, label+":year"),
		time.Month(rapid.IntRange(1, 12).Draw(rt, label+":month")),
		rapid.IntRange(1, 28).Draw(rt, label+":day"),
	)
	if err != nil {
		panic(fmt.Sprintf("drawDate built an invalid date: %v", err))
	}
	return d
}

// drawTransaction builds a balanced transaction over one to three
// commodities. Each commodity balances on its own, which is what section 5.1
// requires and what NewTransaction refuses to build without.
func drawTransaction(rt *rapid.T, b propertyBooks, index int) ledger.Transaction {
	label := fmt.Sprintf("txn%d", index)

	commodityCount := rapid.IntRange(1, 3).Draw(rt, label+":commodities")
	chosen := make([]ledger.CommodityCode, 0, commodityCount)
	for i := 0; i < commodityCount; i++ {
		code := rapid.SampledFrom(propertyCommodities).Draw(rt, fmt.Sprintf("%s:commodity%d", label, i))
		if !containsCommodity(chosen, code) {
			chosen = append(chosen, code)
		}
	}

	var postings []ledger.Posting
	line := 0
	for _, code := range chosen {
		candidates := b.accountsFor(code)
		lines := rapid.IntRange(2, 4).Draw(rt, fmt.Sprintf("%s:%s:lines", label, code))

		running := new(big.Int)
		for i := 0; i < lines; i++ {
			amount := new(big.Int)
			if i == lines-1 {
				// The closing line is whatever makes this commodity sum to
				// zero. Nothing is synthesised beyond that: the balance is
				// constructed, never repaired after the fact.
				amount.Neg(running)
			} else {
				amount = drawSignedBigInt(rt, fmt.Sprintf("%s:%s:amount%d", label, code, i))
				running.Add(running, amount)
			}

			money, err := ledger.NewMoney(code, amount)
			if err != nil {
				panic(fmt.Sprintf("drawTransaction built invalid money: %v", err))
			}

			spec := ledger.PostingSpec{
				ID:      ledger.PostingID(testID(fmt.Sprintf("prop:posting:%d:%s:%d", index, code, i))),
				Account: rapid.SampledFrom(candidates).Draw(rt, fmt.Sprintf("%s:%s:account%d", label, code, i)),
				Amount:  money,
			}
			if rapid.Bool().Draw(rt, fmt.Sprintf("%s:%s:hasRate%d", label, code, i)) {
				spec.Rate = drawRate(rt, fmt.Sprintf("%s:%s:rate%d", label, code, i), code)
			}
			if rapid.Bool().Draw(rt, fmt.Sprintf("%s:%s:hasMemo%d", label, code, i)) {
				spec.Memo = drawText(rt, fmt.Sprintf("%s:%s:memo%d", label, code, i), 40)
			}

			posting, err := ledger.NewPosting(spec)
			if err != nil {
				panic(fmt.Sprintf("drawTransaction built an invalid posting: %v", err))
			}
			postings = append(postings, posting)
			line++
		}
	}

	// OccurredAt and Timezone were absent from this generator until phase 2,
	// so requireSameTransaction compared two zero values and the suite's
	// promise that everything written comes back exactly did not cover them.
	// The nanosecond component is drawn deliberately: it is the part the
	// column cannot hold, and a generator that only produced whole
	// microseconds would assert nothing about the truncation (§11).
	occurred := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC).
		Add(time.Duration(rapid.Int64Range(0, 86_400_000_000_000).Draw(rt, label+":occurred")))

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:         ledger.TransactionID(testID(fmt.Sprintf("prop:txn:%d", index))),
		Date:       drawDate(rt, label+":date"),
		OccurredAt: occurred,
		Timezone: rapid.SampledFrom([]string{
			"", "UTC", "Asia/Jakarta", "America/Sao_Paulo",
		}).Draw(rt, label+":timezone"),
		Payee:    drawText(rt, label+":payee", 30),
		Memo:     drawText(rt, label+":memo", 30),
		Postings: postings,
	})
	if err != nil {
		panic(fmt.Sprintf("drawTransaction built an unbalanced transaction: %v", err))
	}
	return txn
}

// drawLot builds a lot opened by one of a transaction's own debit lines,
// usually already partly consumed.
func drawLot(rt *rapid.T, txn ledger.Transaction, index int) (ledger.Lot, bool) {
	var opener ledger.Posting
	found := false
	for _, p := range txn.Postings() {
		if p.Amount().Sign() > 0 {
			opener = p
			found = true
			break
		}
	}
	if !found {
		return ledger.Lot{}, false
	}

	label := fmt.Sprintf("lot%d", index)
	quantityCode := opener.Amount().Commodity()

	var costCode ledger.CommodityCode
	for {
		costCode = rapid.SampledFrom(propertyCommodities).Draw(rt, label+":costCommodity")
		if costCode != quantityCode {
			break
		}
	}

	quantity := drawBigInt(rt, label+":quantity")
	if quantity.Sign() == 0 {
		quantity = big.NewInt(1) // a lot is an acquisition, so it is positive
	}
	quantityMoney, err := ledger.NewMoney(quantityCode, quantity)
	if err != nil {
		panic(err)
	}

	costMoney, err := ledger.NewMoney(costCode, drawBigInt(rt, label+":cost"))
	if err != nil {
		panic(err)
	}

	// Partial consumption: anything from nothing left to untouched. This is
	// the state a lot spends most of its life in, and the one a naive
	// round trip forgets to write back.
	consumed := new(big.Int).Mod(drawBigInt(rt, label+":consumed"), new(big.Int).Add(quantity, big.NewInt(1)))
	remaining, err := ledger.NewMoney(quantityCode, new(big.Int).Sub(quantity, consumed))
	if err != nil {
		panic(err)
	}

	lot, err := ledger.NewLot(ledger.LotSpec{
		ID:        ledger.LotID(testID(fmt.Sprintf("prop:lot:%d", index))),
		Account:   opener.Account(),
		OpenedBy:  opener.ID(),
		OpenedOn:  txn.Date(),
		Quantity:  quantityMoney,
		Cost:      costMoney,
		Remaining: &remaining,
	})
	if err != nil {
		panic(fmt.Sprintf("drawLot built an invalid lot: %v", err))
	}
	return lot, true
}

func containsCommodity(haystack []ledger.CommodityCode, needle ledger.CommodityCode) bool {
	for _, c := range haystack {
		if c == needle {
			return true
		}
	}
	return false
}

// drawText draws arbitrary user text, NUL included, and returns text the
// domain accepts.
//
// The bound used to be in the draw itself. That exclusion was the finding
// rather than the fix: the first run of this property test stopped on a
// generated payee containing U+0000 — a value no reviewer would ever have
// written by hand — and PostgreSQL refused it with SQLSTATE 22021, an error
// naming neither the field nor what to do about it. The store was taught to
// refuse it by name and the generator was told to stop producing it, which
// left a bound in the generator standing in for a rule nobody had decided yet.
//
// The rule is decided now: internal/ledger refuses NUL, because "text a ledger
// can hold" is a fact about the ledger and not about whichever database is
// underneath. So the draw produces it again and the refusal is asserted here,
// against the domain — and then the value is stripped so the case can go on to
// do what this test is actually for.
//
// Ending the case at the refusal instead was tried, and was wrong. Over 30
// characters of rapid's default rune set, NUL is common: most cases stopped
// before writing anything, and the whole round-trip suite ran in eight seconds
// instead of a hundred while proving a fraction as much. The runtime is what
// gave it away — the suite still passed, and passed faster. A cheaper test
// that still goes green is the hardest kind of regression to notice.
func drawText(rt *rapid.T, label string, n int) string {
	raw := rapid.StringN(0, n, n).Draw(rt, label)
	if !strings.ContainsRune(raw, 0) {
		return raw
	}
	requireTheDomainRefusesNul(rt, raw)
	return strings.Map(dropNul, raw)
}

// requireTheDomainRefusesNul checks that the value the generator just produced
// is refused by internal/ledger rather than by anything further down.
//
// Which layer refuses it is the whole point. Before the rule moved into the
// domain this arrived as a SQLSTATE from PostgreSQL, hundreds of lines away
// from the field that caused it.
func requireTheDomainRefusesNul(rt *rapid.T, value string) {
	_, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(testID("prop:nul-probe")),
		Account: ledger.AccountID(testID("prop:any:1")),
		Amount:  mustPropertyMoney("IDR", big.NewInt(1)),
		Memo:    value,
	})
	if !errors.Is(err, ledger.ErrInvalidText) {
		rt.Fatalf("the domain accepted text containing a NUL: %q (error: %v)", value, err)
	}
}

func mustPropertyMoney(code ledger.CommodityCode, amount *big.Int) ledger.Money {
	m, err := ledger.NewMoney(code, amount)
	if err != nil {
		panic(fmt.Sprintf("build probe money: %v", err))
	}
	return m
}

// dropNul removes the one code point a ledger entry cannot survive.
func dropNul(r rune) rune {
	if r == 0 {
		return -1
	}
	return r
}
