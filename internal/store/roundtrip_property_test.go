// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/GaffaQ/Nusa/internal/ledger"
	"github.com/GaffaQ/Nusa/internal/store"
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

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:       ledger.TransactionID(testID(fmt.Sprintf("prop:txn:%d", index))),
		Date:     drawDate(rt, label+":date"),
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

// drawText draws user text the store will accept.
//
// NUL is excluded because the store refuses it, by name, before writing —
// PostgreSQL text cannot hold it. That refusal is itself a finding of this
// property test: the first run stopped on a generated payee containing U+0000,
// which is a value no one would have thought to write by hand. TestTextWith-
// ANulByteIsRefusedByName covers the rejection; this generator explores
// everything the store does accept, which is every other awkward string there
// is.
func drawText(rt *rapid.T, label string, n int) string {
	return strings.Map(dropNul, rapid.StringN(0, n, n).Draw(rt, label))
}

// dropNul removes the one code point PostgreSQL text cannot hold.
func dropNul(r rune) rune {
	if r == 0 {
		return -1
	}
	return r
}
