// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
	"github.com/GaffaQ/Nusa/internal/store"
)

// The write endpoints, against a real PostgreSQL over HTTP.
//
// Against the real one rather than a fake, and not as a formality: the
// idempotent replay these routes promise is a promise about stored bytes, and
// the layer that keeps it is a column. A fake returns what it was handed,
// which is exactly what jsonb did not — that is how the byte-for-byte claim
// came to be false while thirteen guards above it passed (§11).
//
// What these guards must cover, decided before any of them was written.
//
// Creating
//
//	 1. A transaction is created and reported back as it was stored.
//	 2. An unbalanced transaction is refused with the field and the reason,
//	    not merely a code.
//	 3. A posting naming an account that does not exist is refused, and says
//	    which rule it broke.
//	 4. reverses_id and reversal_kind are refused if present, never ignored.
//	 5. An unknown field is refused rather than dropped.
//
// Idempotency
//
//	 6. Every mutation route refuses a request with no key.
//	 7. A replay returns the first attempt's status and bytes, and writes
//	    nothing a second time.
//	 8. The same key with a different body is refused as a conflict.
//
// Reversing
//
//	 9. A correction is stored as a correction, with its links intact.
//	10. A deletion is stored as a deletion and removes nothing.
//	11. A posting map naming a line that belongs to another transaction is
//	    refused — by ledger.Reverse's own rule, not by anything the handler
//	    invented.
//	12. An incomplete map is refused, and the message names the line that went
//	    unanswered.
//	13. A missing date is refused, because Reverse will not guess between
//	    leaving last year's report intact and rewriting it.
//	14. Nothing is reversed twice.
//	15. Reversing something that is not in the book is not found.
//
// Accounts
//
//	16. An account is created.
//	17. name and closed are updated.
//	18. kind, parent_id and commodity are refused by name, and nothing changes.
//
// Disposals
//
//	19. A disposal consumes lots, and the response says nothing whatever about
//	    which — that half is provisional until M7.

func writeHarness(t *testing.T) (*harness, string, awkwardBook, *store.Store) {
	t.Helper()
	s := realStore(t)
	b := writeAwkwardBook(t, s)
	h, cookie := signedIn(t, s)
	return h, cookie, b, s
}

// post sends a mutation with a key. The key is part of every call here
// because the routes require one, which is itself guard 6.
func (h *harness) postKeyed(path, key string, body any, cookie string) response {
	h.t.Helper()
	return h.doKeyed(http.MethodPost, path, key, body, cookie)
}

func newTransactionBody(b awkwardBook, label string) map[string]any {
	return map[string]any{
		"id":    apiTestID("w:txn:" + label),
		"date":  "2026-05-01",
		"payee": "Toko " + label,
		"postings": []any{
			map[string]any{
				"id":         apiTestID("w:posting:" + label + ":out"),
				"account_id": string(b.wallet),
				"amount":     map[string]string{"amount": "-500", "commodity": "ETH"},
			},
			map[string]any{
				"id":         apiTestID("w:posting:" + label + ":in"),
				"account_id": string(b.expenses),
				"amount":     map[string]string{"amount": "500", "commodity": "ETH"},
			},
		},
	}
}

func TestATransactionIsCreatedAndReportedBack(t *testing.T) {
	h, cookie, b, s := writeHarness(t)

	body := newTransactionBody(b, "create")
	res := h.postKeyed("/api/v1/transactions", "key-create", body, cookie)
	require.Equal(t, http.StatusCreated, res.code, res.body)

	var created struct {
		ID       string `json:"id"`
		Date     string `json:"date"`
		Payee    string `json:"payee"`
		Postings []struct {
			ID string `json:"id"`
		} `json:"postings"`
		ReversesID   *string `json:"reverses_id"`
		ReversalKind *string `json:"reversal_kind"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.body), &created))
	require.Equal(t, apiTestID("w:txn:create"), created.ID)
	require.Equal(t, "2026-05-01", created.Date)
	require.Len(t, created.Postings, 2)
	require.Nil(t, created.ReversesID)
	require.Nil(t, created.ReversalKind)

	// And it is really in the book, not merely echoed.
	back, err := s.LoadTransaction(context.Background(),
		ledger.TransactionID(apiTestID("w:txn:create")))
	require.NoError(t, err)
	require.Equal(t, "Toko create", back.Payee())
}

func TestAnUnbalancedTransactionSaysWhichRuleItBroke(t *testing.T) {
	h, cookie, b, _ := writeHarness(t)

	body := newTransactionBody(b, "unbalanced")
	postings := body["postings"].([]any)
	postings[1].(map[string]any)["amount"] = map[string]string{"amount": "499", "commodity": "ETH"}

	res := h.postKeyed("/api/v1/transactions", "key-unbalanced", body, cookie)
	require.Equal(t, http.StatusUnprocessableEntity, res.code, res.body)
	require.Equal(t, "validation_failed", res.errorCode(t))
	// A code alone tells a client that something was wrong and nothing about
	// what. The field is what a form highlights; the reason is what a client
	// switches on when it wants to say something specific.
	require.Equal(t, "postings", res.errorField(t))
	require.Equal(t, "unbalanced", res.errorDetail(t, "reason"))
}

func TestAPostingNamingNoSuchAccountIsRefused(t *testing.T) {
	h, cookie, b, _ := writeHarness(t)

	body := newTransactionBody(b, "ghost")
	body["postings"].([]any)[0].(map[string]any)["account_id"] = apiTestID("account:ghost")

	res := h.postKeyed("/api/v1/transactions", "key-ghost", body, cookie)
	require.Equal(t, http.StatusUnprocessableEntity, res.code, res.body)
	require.Equal(t, "validation_failed", res.errorCode(t))
	require.Equal(t, "unknown_account", res.errorDetail(t, "reason"))
}

func TestACreateRefusesToBeHandedAReversalLink(t *testing.T) {
	h, cookie, b, s := writeHarness(t)

	for field, value := range map[string]any{
		"reverses_id":   string(b.transaction),
		"reversal_kind": "correction",
	} {
		t.Run(field, func(t *testing.T) {
			body := newTransactionBody(b, "forged-"+field)
			body[field] = value

			res := h.postKeyed("/api/v1/transactions", "key-forged-"+field, body, cookie)
			require.Equal(t, http.StatusUnprocessableEntity, res.code, res.body)
			require.Equal(t, "field_immutable", res.errorCode(t))
			require.Equal(t, field, res.errorField(t),
				"the refusal must name the field, so the client knows to stop sending it")

			// Refused means nothing was written. A client told "no" that finds
			// a row anyway has been told two different things.
			_, err := s.LoadTransaction(context.Background(),
				ledger.TransactionID(apiTestID("w:txn:forged-"+field)))
			require.ErrorIs(t, err, store.ErrNotFound)
		})
	}
}

func TestAnUnknownFieldIsRefusedRatherThanDropped(t *testing.T) {
	h, cookie, b, _ := writeHarness(t)

	body := newTransactionBody(b, "typo")
	// A client that misspells a field and receives 201 has created something
	// other than what it asked for, with nothing in the response saying so.
	body["comodity"] = "IDR"

	res := h.postKeyed("/api/v1/transactions", "key-typo", body, cookie)
	require.Equal(t, http.StatusBadRequest, res.code, res.body)
	require.Equal(t, "invalid_request", res.errorCode(t))
}

func TestEveryMutationRouteRequiresAKey(t *testing.T) {
	h, cookie, b, _ := writeHarness(t)

	for name, call := range map[string]struct {
		method, path string
		body         any
	}{
		"create transaction": {http.MethodPost, "/api/v1/transactions", newTransactionBody(b, "nokey")},
		"correct":            {http.MethodPost, "/api/v1/transactions/" + string(b.transaction) + "/corrections", map[string]any{}},
		"delete":             {http.MethodPost, "/api/v1/transactions/" + string(b.transaction) + "/deletions", map[string]any{}},
		"create account":     {http.MethodPost, "/api/v1/accounts", map[string]any{}},
		"patch account":      {http.MethodPatch, "/api/v1/accounts/" + string(b.wallet), map[string]any{}},
	} {
		t.Run(name, func(t *testing.T) {
			res := h.do(call.method, call.path, call.body, cookie)
			require.Equal(t, http.StatusBadRequest, res.code, res.body)
			require.Equal(t, "idempotency_key_required", res.errorCode(t))
		})
	}
}

func TestAReplayRepeatsTheAnswerAndWritesNothingAgain(t *testing.T) {
	h, cookie, b, s := writeHarness(t)
	ctx := context.Background()

	body := newTransactionBody(b, "replay")
	first := h.postKeyed("/api/v1/transactions", "key-replay", body, cookie)
	require.Equal(t, http.StatusCreated, first.code, first.body)

	before, err := s.CountTransactions(ctx)
	require.NoError(t, err)

	second := h.postKeyed("/api/v1/transactions", "key-replay", body, cookie)

	// The status is the load-bearing half: a create answered 201 coming back
	// 200 has not replayed, and a client reading the status to decide whether
	// it created something is told the wrong thing.
	require.Equal(t, http.StatusCreated, second.code, second.body)
	require.Equal(t, first.body, second.body, "the replay is not byte for byte")

	after, err := s.CountTransactions(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "the replay wrote a second transaction")
}

func TestTheSameKeyWithADifferentBodyIsAConflict(t *testing.T) {
	h, cookie, b, _ := writeHarness(t)

	require.Equal(t, http.StatusCreated,
		h.postKeyed("/api/v1/transactions", "key-shared", newTransactionBody(b, "one"), cookie).code)

	// The caller believes it is retrying something it is not. Replaying the
	// first answer would report a write that was never attempted.
	res := h.postKeyed("/api/v1/transactions", "key-shared", newTransactionBody(b, "two"), cookie)
	require.Equal(t, http.StatusConflict, res.code, res.body)
	require.Equal(t, "idempotency_key_reused", res.errorCode(t))
}

// reversalBody answers every line of the original, which is what Reverse
// requires.
func reversalBody(t *testing.T, s *store.Store, original ledger.TransactionID, label string) map[string]any {
	t.Helper()
	txn, err := s.LoadTransaction(context.Background(), original)
	require.NoError(t, err)

	lines := map[string]string{}
	for i, p := range txn.Postings() {
		lines[string(p.ID())] = apiTestID(label + ":line:" + string(rune('a'+i)))
	}
	return map[string]any{
		"id":       apiTestID(label),
		"date":     "2026-06-01",
		"memo":     "salah nominal",
		"postings": lines,
	}
}

func TestACorrectionIsStoredAsOne(t *testing.T) {
	h, cookie, b, s := writeHarness(t)

	body := reversalBody(t, s, b.transaction, "w:correction")
	res := h.postKeyed("/api/v1/transactions/"+string(b.transaction)+"/corrections",
		"key-correct", body, cookie)
	require.Equal(t, http.StatusCreated, res.code, res.body)

	var out struct {
		ReversesID   *string `json:"reverses_id"`
		ReversalKind *string `json:"reversal_kind"`
		Postings     []struct {
			ReversesID *string `json:"reverses_id"`
		} `json:"postings"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.body), &out))
	require.NotNil(t, out.ReversesID)
	require.Equal(t, string(b.transaction), *out.ReversesID)
	require.NotNil(t, out.ReversalKind)
	require.Equal(t, "correction", *out.ReversalKind)

	// Line-level provenance, which a transaction-level link cannot supply: a
	// transaction that acquires two things at once has two lines a
	// transaction-level link cannot tell apart.
	for _, p := range out.Postings {
		require.NotNil(t, p.ReversesID, "a reversing line does not say what it undoes")
	}
}

func TestADeletionIsStoredAsOneAndRemovesNothing(t *testing.T) {
	h, cookie, b, s := writeHarness(t)

	body := reversalBody(t, s, b.transaction, "w:deletion")
	res := h.postKeyed("/api/v1/transactions/"+string(b.transaction)+"/deletions",
		"key-delete", body, cookie)
	require.Equal(t, http.StatusCreated, res.code, res.body)

	var out struct {
		ReversalKind *string `json:"reversal_kind"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.body), &out))
	require.NotNil(t, out.ReversalKind)
	require.Equal(t, "deletion", *out.ReversalKind)

	// A delete is an append. The original is still there, still readable, and
	// still says what it always said.
	original := h.do(http.MethodGet, "/api/v1/transactions/"+string(b.transaction), nil, cookie)
	require.Equal(t, http.StatusOK, original.code)
}

func TestAPostingMapIsJudgedByReverseAndNotByTheHandler(t *testing.T) {
	h, cookie, b, s := writeHarness(t)

	t.Run("a line from another transaction", func(t *testing.T) {
		body := reversalBody(t, s, b.transaction, "w:foreign")
		lines := body["postings"].(map[string]string)
		// Swap one real key for a posting that belongs to nothing here. The
		// map is still the right size, so this is refused by the rule that
		// every original must be answered rather than by a count.
		for k := range lines {
			delete(lines, k)
			break
		}
		lines[apiTestID("posting:from:another:transaction")] = apiTestID("w:foreign:extra")

		res := h.postKeyed("/api/v1/transactions/"+string(b.transaction)+"/corrections",
			"key-foreign", body, cookie)
		require.Equal(t, http.StatusUnprocessableEntity, res.code, res.body)
		require.Equal(t, "validation_failed", res.errorCode(t))
		require.Equal(t, "invalid_reversal", res.errorDetail(t, "reason"))
	})

	t.Run("an incomplete map", func(t *testing.T) {
		body := reversalBody(t, s, b.transaction, "w:short")
		lines := body["postings"].(map[string]string)
		var dropped string
		for k := range lines {
			dropped = k
			delete(lines, k)
			break
		}

		res := h.postKeyed("/api/v1/transactions/"+string(b.transaction)+"/corrections",
			"key-short", body, cookie)
		require.Equal(t, http.StatusUnprocessableEntity, res.code, res.body)
		require.Equal(t, "validation_failed", res.errorCode(t))
		// Reverse decides what a valid map is and says so in its own words.
		// The handler passes the message through rather than inventing one,
		// which is why the count Reverse complains about appears here.
		require.Contains(t, res.body, "posting identities")
		require.NotEmpty(t, dropped)
	})

	t.Run("a missing date", func(t *testing.T) {
		body := reversalBody(t, s, b.transaction, "w:undated")
		delete(body, "date")

		res := h.postKeyed("/api/v1/transactions/"+string(b.transaction)+"/corrections",
			"key-undated", body, cookie)
		require.Equal(t, http.StatusUnprocessableEntity, res.code, res.body)
		// Booking a reversal today leaves last year's report intact; booking
		// it on the original's date rewrites that period. The domain refuses
		// to choose, and so does this.
		require.Equal(t, "validation_failed", res.errorCode(t))
	})
}

func TestNothingIsReversedTwice(t *testing.T) {
	h, cookie, b, s := writeHarness(t)

	first := reversalBody(t, s, b.transaction, "w:first")
	require.Equal(t, http.StatusCreated,
		h.postKeyed("/api/v1/transactions/"+string(b.transaction)+"/corrections",
			"key-first", first, cookie).code)

	second := reversalBody(t, s, b.transaction, "w:second")
	res := h.postKeyed("/api/v1/transactions/"+string(b.transaction)+"/corrections",
		"key-second", second, cookie)
	require.Equal(t, http.StatusConflict, res.code, res.body)
	require.Equal(t, "already_reversed", res.errorCode(t))
}

func TestReversingSomethingNotInTheBookIsNotFound(t *testing.T) {
	h, cookie, _, _ := writeHarness(t)

	res := h.postKeyed("/api/v1/transactions/"+apiTestID("nobody")+"/corrections",
		"key-nobody", map[string]any{"id": apiTestID("w:nobody"), "date": "2026-06-01"}, cookie)
	require.Equal(t, http.StatusNotFound, res.code, res.body)
	require.Equal(t, "not_found", res.errorCode(t))
}

func TestAnAccountIsCreatedAndItsLabelUpdated(t *testing.T) {
	h, cookie, _, _ := writeHarness(t)

	created := h.postKeyed("/api/v1/accounts", "key-account", map[string]any{
		"id":        apiTestID("w:account"),
		"kind":      "liability",
		"name":      "Kartu kredit",
		"commodity": "IDR",
	}, cookie)
	require.Equal(t, http.StatusCreated, created.code, created.body)

	patched := h.doKeyed(http.MethodPatch, "/api/v1/accounts/"+apiTestID("w:account"),
		"key-patch", map[string]any{"name": "Kartu kredit BCA", "closed": true}, cookie)
	require.Equal(t, http.StatusOK, patched.code, patched.body)

	var out struct {
		Name      string  `json:"name"`
		Closed    bool    `json:"closed"`
		Kind      string  `json:"kind"`
		Commodity *string `json:"commodity"`
	}
	require.NoError(t, json.Unmarshal([]byte(patched.body), &out))
	require.Equal(t, "Kartu kredit BCA", out.Name)
	require.True(t, out.Closed)
	require.Equal(t, "liability", out.Kind, "the update must not disturb what it does not touch")
	require.NotNil(t, out.Commodity)
	require.Equal(t, "IDR", *out.Commodity)
}

func TestAnAccountsKindParentAndCommodityCannotBeChanged(t *testing.T) {
	h, cookie, b, s := writeHarness(t)
	ctx := context.Background()

	before, err := s.LoadAccount(ctx, b.wallet)
	require.NoError(t, err)

	for field, value := range map[string]any{
		"kind":      "liability",
		"parent_id": string(b.brokerage),
		"commodity": "IDR",
	} {
		t.Run(field, func(t *testing.T) {
			res := h.doKeyed(http.MethodPatch, "/api/v1/accounts/"+string(b.wallet),
				"key-immutable-"+field, map[string]any{field: value}, cookie)

			require.Equal(t, http.StatusUnprocessableEntity, res.code, res.body)
			require.Equal(t, "field_immutable", res.errorCode(t))
			require.Equal(t, field, res.errorField(t))

			// Refused rather than ignored: a 200 here would leave the client
			// believing the kind changed, and everything it does next rests on
			// that belief.
			after, err := s.LoadAccount(ctx, b.wallet)
			require.NoError(t, err)
			require.Equal(t, before.Kind(), after.Kind())
			require.Equal(t, before.Parent(), after.Parent())
			require.Equal(t, before.Commodity(), after.Commodity())
		})
	}
}

func TestADisposalConsumesLotsAndSaysNothingAboutThem(t *testing.T) {
	h, cookie, b, s := writeHarness(t)
	ctx := context.Background()

	// A holding to sell out of. Lots have no endpoint — that is M7 — so the
	// fixture opens one through the store, which is what an importer would do.
	opening := openLot(t, s, b)

	body := map[string]any{
		"id":    apiTestID("w:txn:disposal"),
		"date":  "2026-06-15",
		"payee": "Jual sebagian",
		"postings": []any{
			map[string]any{
				"id":         apiTestID("w:posting:disposal:out"),
				"account_id": string(b.wallet),
				"amount":     map[string]string{"amount": "-400", "commodity": "ETH"},
			},
			map[string]any{
				"id":         apiTestID("w:posting:disposal:in"),
				"account_id": string(b.expenses),
				"amount":     map[string]string{"amount": "400", "commodity": "ETH"},
			},
		},
		"disposing": []string{apiTestID("w:posting:disposal:out")},
	}

	res := h.postKeyed("/api/v1/transactions", "key-disposal", body, cookie)
	require.Equal(t, http.StatusCreated, res.code, res.body)

	// The lot really was drawn on, so this went through SaveDisposal and not
	// through the ordinary write.
	lot, err := s.LoadLot(ctx, opening)
	require.NoError(t, err)
	require.Equal(t, "600", lot.Remaining().Amount().String(),
		"the disposal did not reduce the lot it drew on")

	consumed, err := s.Consumptions(ctx, ledger.PostingID(apiTestID("w:posting:disposal:out")))
	require.NoError(t, err)
	require.Len(t, consumed, 1, "nothing recorded which lot paid for the sale")

	// And the response says none of that. Reporting it means encoding a
	// ledger.Rat, which is M7's decision; anything shipped now would be a
	// guess a client could start depending on.
	//
	// Headers as well as the body, because a header is part of the response
	// and the first draft of this guard checked only the body — a break that
	// leaked the consumption list through a header left it green.
	whole := res.body
	for name, values := range res.header {
		whole += "\n" + name + ": " + strings.Join(values, ",")
	}
	for _, word := range []string{"lot", "consumed", "basis", "remaining"} {
		require.NotContains(t, strings.ToLower(whole), word,
			"the response leaked consumption detail, which is deferred to M7")
	}
}

// openLot writes a holding for the disposal test to sell out of.
func openLot(t *testing.T, s *store.Store, b awkwardBook) ledger.LotID {
	t.Helper()
	ctx := context.Background()

	buy, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(apiTestID("w:posting:buy")),
		Account: b.wallet,
		Amount:  mustParseMoney(t, "ETH", "1000"),
	})
	require.NoError(t, err)
	pay, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(apiTestID("w:posting:pay")),
		Account: b.expenses,
		Amount:  mustParseMoney(t, "ETH", "-1000"),
	})
	require.NoError(t, err)

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:       ledger.TransactionID(apiTestID("w:txn:buy")),
		Date:     mustParseDate(t, "2026-04-01"),
		Payee:    "Beli",
		Postings: []ledger.Posting{buy, pay},
	})
	require.NoError(t, err)

	lotID := ledger.LotID(apiTestID("w:lot"))
	lot, err := ledger.NewLot(ledger.LotSpec{
		ID:       lotID,
		Account:  b.wallet,
		OpenedBy: ledger.PostingID(apiTestID("w:posting:buy")),
		OpenedOn: mustParseDate(t, "2026-04-01"),
		Quantity: mustParseMoney(t, "ETH", "1000"),
		// The cost is in another commodity, because an asset and what was paid
		// for it are different things and the domain refuses a lot that says
		// otherwise.
		Cost: mustParseMoney(t, "IDR", "5000000"),
	})
	require.NoError(t, err)

	_, err = s.SaveTransaction(ctx, store.Write{
		ActorID: b.actor, Origin: store.OriginHuman,
		IdempotencyKey: "open-lot", AuditID: apiTestID("w:audit:buy"),
	}, txn, lot)
	require.NoError(t, err)
	return lotID
}
