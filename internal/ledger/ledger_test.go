// SPDX-License-Identifier: MIT

// The tests live in package ledger_test rather than in ledger itself. Every
// invariant in §5 is a promise made to callers, so proving it through the same
// door a caller uses proves the promise rather than the implementation.
//
// No test in this package may use a binary floating-point type, including in a
// generator. A verification that permits one in its own fixtures is not
// verifying §4.1 — it is demonstrating the one way the rule gets broken.
package ledger_test

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nusa-app/nusa/internal/ledger"
)

// Identities are UUIDv7 (§5.6), which is unreadable in a test. These helpers
// map a readable label to one deterministically, so a test still says "bank"
// while the ledger still receives the identity shape it demands. The same
// label always yields the same identity, within a run and between runs.
//
// The bytes come from a hash rather than a clock, so the timestamp prefix a
// real v7 carries is meaningless here. That is fine: nothing in this package
// reads it. Only the structure is checked, and only the structure matters.
func testUUID(label string) string {
	sum := sha256.Sum256([]byte(label))

	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 9562 variant

	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func aid(label string) ledger.AccountID     { return ledger.AccountID(testUUID("account:" + label)) }
func tid(label string) ledger.TransactionID { return ledger.TransactionID(testUUID("txn:" + label)) }
func pid(label string) ledger.PostingID     { return ledger.PostingID(testUUID("posting:" + label)) }
func lid(label string) ledger.LotID         { return ledger.LotID(testUUID("lot:" + label)) }

// Commodity codes used throughout the tests. IDR and USD are currencies with
// two decimal places; BBCA.JK is an Indonesian equity trading in whole shares,
// which is what makes scale-crossing conversions worth testing.
const (
	idr  = ledger.CommodityCode("IDR")
	usd  = ledger.CommodityCode("USD")
	jpy  = ledger.CommodityCode("JPY")
	btc  = ledger.CommodityCode("BTC")
	bbca = ledger.CommodityCode("BBCA.JK")
)

func mustMoney(t *testing.T, code ledger.CommodityCode, minorUnits int64) ledger.Money {
	t.Helper()
	m, err := ledger.MoneyFromInt(code, minorUnits)
	require.NoError(t, err)
	return m
}

func mustRat(t *testing.T, code ledger.CommodityCode, num, den int64) ledger.Rat {
	t.Helper()
	r, err := ledger.NewRat(code, big.NewRat(num, den))
	require.NoError(t, err)
	return r
}

func mustDate(t *testing.T, s string) ledger.Date {
	t.Helper()
	d, err := ledger.ParseDate(s)
	require.NoError(t, err)
	return d
}

func mustRate(t *testing.T, base, quote ledger.CommodityCode, num, den int64) ledger.Rate {
	t.Helper()
	r, err := ledger.NewRate(base, quote, big.NewRat(num, den))
	require.NoError(t, err)
	return r
}

func mustCommodity(t *testing.T, code ledger.CommodityCode, kind ledger.CommodityKind, scale uint8) ledger.Commodity {
	t.Helper()
	c, err := ledger.NewCommodity(code, kind, scale)
	require.NoError(t, err)
	return c
}

// mustAccount and mustPosting take readable labels and convert them, so test
// bodies stay legible while the ledger still sees real identities.
func mustAccount(t *testing.T, label string, kind ledger.AccountKind) ledger.Account {
	t.Helper()
	return mustAccountSpec(t, ledger.AccountSpec{ID: aid(label), Kind: kind, Name: label})
}

func mustAccountSpec(t *testing.T, spec ledger.AccountSpec) ledger.Account {
	t.Helper()
	a, err := ledger.NewAccount(spec)
	require.NoError(t, err)
	return a
}

func mustPosting(t *testing.T, label, accountLabel string, amount ledger.Money) ledger.Posting {
	t.Helper()
	p, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      pid(label),
		Account: aid(accountLabel),
		Amount:  amount,
	})
	require.NoError(t, err)
	return p
}

// requireMoney compares by value rather than by struct equality. Money holds a
// *big.Int, and two zero big.Ints can differ in their internal representation
// while being the same number, so reflect-based equality is the wrong tool.
func requireMoney(t *testing.T, want, got ledger.Money, because ...string) {
	t.Helper()
	context := ""
	if len(because) > 0 {
		context = " (" + strings.Join(because, "; ") + ")"
	}
	require.True(t, want.Equal(got), "want %s, got %s%s", want, got, context)
}

// testRegistry describes the commodities the tests use. BBCA.JK is registered
// here rather than in StandardRegistry because an exchange's share scale is a
// country-specific fact and §6 keeps those in Country Packs.
func testRegistry(t *testing.T) *ledger.Registry {
	t.Helper()
	reg, err := ledger.StandardRegistry().With(
		mustCommodity(t, bbca, ledger.KindEquity, 0),
	)
	require.NoError(t, err)
	return reg
}

// simpleBooks returns a small but realistic set of accounts: a bank account, a
// dollar wallet, a share holding, somewhere to spend, somewhere to earn, and
// the equity account conversions route through.
func simpleBooks(t *testing.T) *ledger.AccountTree {
	t.Helper()
	tree, err := ledger.NewAccountTree(
		mustAccountSpec(t, ledger.AccountSpec{ID: aid("assets"), Kind: ledger.AccountAsset, Name: "Assets"}),
		mustAccountSpec(t, ledger.AccountSpec{ID: aid("bank"), Parent: aid("assets"), Kind: ledger.AccountAsset, Name: "Bank", Commodity: idr}),
		mustAccountSpec(t, ledger.AccountSpec{ID: aid("wallet-usd"), Parent: aid("assets"), Kind: ledger.AccountAsset, Name: "Dollars", Commodity: usd}),
		mustAccountSpec(t, ledger.AccountSpec{ID: aid("shares"), Parent: aid("assets"), Kind: ledger.AccountAsset, Name: "Shares"}),
		mustAccount(t, "groceries", ledger.AccountExpense),
		mustAccount(t, "salary", ledger.AccountIncome),
		mustAccount(t, "trading", ledger.AccountEquity),
	)
	require.NoError(t, err)
	return tree
}

// jakarta is used only to prove that a timezone reaches OccurredAt and never
// reaches a balance. It is a fixed offset rather than a loaded location so the
// tests do not depend on a zone database being present.
var jakarta = time.FixedZone("WIB", 7*60*60)
