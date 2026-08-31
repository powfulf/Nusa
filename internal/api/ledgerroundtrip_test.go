// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/GaffaQ/Nusa/internal/api"
	"github.com/GaffaQ/Nusa/internal/ledger"
	"github.com/GaffaQ/Nusa/internal/store"
)

// The read endpoints against a real PostgreSQL, end to end over HTTP.
//
// Every other test in this package uses fakes, and they are the right tool for
// what they cover — a fake stores what it is given and hands it back, which is
// exactly what makes it useless here. This is the first time a domain value is
// built from a database row rather than by a test, and the conversion in
// between is where a scale, an exact fraction or an absent commodity either
// survives or quietly does not. A fake would agree with whatever the DTO
// claimed (§11).
//
// So the values are chosen to be awkward rather than realistic:
//
//   - an ETH amount at scale 18, larger than int64 holds, so the numeric(40,0)
//     column and big.Int are both exercised rather than assumed;
//   - a rate that is an exact fraction with no finite decimal form, which is
//     the whole reason rates are stored as two integers;
//   - an account with no commodity restriction, which is "" in the domain and
//     must be null on the wire;
//   - text carrying characters outside the basic plane.
//
// A well-behaved value would pass whatever the conversion did.

var (
	pgOnce      sync.Once
	pgURL       string
	pgErr       error
	pgTerminate func()
)

func TestMain(m *testing.M) {
	code := m.Run()
	if pgTerminate != nil {
		pgTerminate()
	}
	os.Exit(code)
}

// realStore starts one PostgreSQL for this package, on first use.
//
// Lazily, so the fake-backed tests in this package stay as fast as they were:
// only the round-trip tests pay for a container. There is no build tag, for
// the reason internal/store gives — a tagged suite is one that a green
// `go test ./...` says nothing about.
func realStore(t *testing.T) *store.Store {
	t.Helper()

	pgOnce.Do(func() {
		ctx := context.Background()
		container, err := tcpostgres.Run(ctx, "postgres:16",
			tcpostgres.WithDatabase("nusa"),
			tcpostgres.WithUsername("nusa"),
			tcpostgres.WithPassword("test-only-password"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(2*time.Minute),
			),
		)
		if err != nil {
			pgErr = fmt.Errorf("start postgres: %w", err)
			return
		}
		pgTerminate = func() { _ = testcontainers.TerminateContainer(container) }

		pgURL, pgErr = container.ConnectionString(ctx, "sslmode=disable")
		if pgErr != nil {
			return
		}
		pgErr = store.MigrateUp(pgURL)
	})
	require.NoError(t, pgErr, "these round-trip tests need a running Docker daemon")

	ctx := context.Background()
	s, err := store.Open(ctx, pgURL)
	require.NoError(t, err)
	t.Cleanup(s.Close)

	_, err = s.Pool().Exec(ctx,
		`TRUNCATE audit_log, idempotency_keys, lot_consumptions, lots, postings, transactions,
		          accounts, sessions, user_totp, user_backup_codes, users`)
	require.NoError(t, err)
	return s
}

// awkwardBook writes the fixture and returns the identities it used.
type awkwardBook struct {
	actor       string
	wallet      ledger.AccountID // ETH, scale 18
	brokerage   ledger.AccountID // no commodity restriction
	expenses    ledger.AccountID
	transaction ledger.TransactionID
}

// hugeETH is more than int64 holds: 1,2345678901234567890123456789 ETH counted
// in wei. int64 stops just past 9,2e18.
const hugeETH = "12345678901234567890123456789"

func writeAwkwardBook(t *testing.T, s *store.Store) awkwardBook {
	t.Helper()
	ctx := context.Background()

	b := awkwardBook{
		actor:       apiTestID("user"),
		wallet:      ledger.AccountID(apiTestID("account:wallet")),
		brokerage:   ledger.AccountID(apiTestID("account:brokerage")),
		expenses:    ledger.AccountID(apiTestID("account:expenses")),
		transaction: ledger.TransactionID(apiTestID("txn")),
	}
	require.NoError(t, s.SaveUser(ctx, b.actor))

	for _, spec := range []ledger.AccountSpec{
		{ID: b.wallet, Kind: ledger.AccountAsset, Name: "Dompet ETH ⛓️", Commodity: "ETH"},
		// No commodity: the account may hold several, which is what a
		// brokerage does. "" in the domain, null on the wire.
		{ID: b.brokerage, Kind: ledger.AccountAsset, Name: "Broker (multi)"},
		{ID: b.expenses, Kind: ledger.AccountExpense, Name: "Biaya jaringan", Commodity: "ETH"},
	} {
		account, err := ledger.NewAccount(spec)
		require.NoError(t, err)
		require.NoError(t, s.SaveAccount(ctx, account))
	}

	debit, err := ledger.ParseMoney("ETH", hugeETH)
	require.NoError(t, err)
	credit, err := ledger.ParseMoney("ETH", "-"+hugeETH)
	require.NoError(t, err)

	// 48001/3 has no finite decimal form. Storing a rate as a decimal would
	// lose it; two integer columns do not (§4.7).
	rate, err := ledger.NewRate("ETH", "IDR", big.NewRat(48001, 3))
	require.NoError(t, err)

	out, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(apiTestID("posting:out")),
		Account: b.wallet,
		Amount:  credit,
		Rate:    rate,
		Memo:    "keluar — dengan tanda hubung panjang",
	})
	require.NoError(t, err)
	in, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(apiTestID("posting:in")),
		Account: b.expenses,
		Amount:  debit,
		Rate:    rate,
	})
	require.NoError(t, err)

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:         b.transaction,
		Date:       mustParseDate(t, "2026-03-07"),
		OccurredAt: time.Date(2026, 3, 7, 4, 30, 15, 123456789, time.UTC),
		Timezone:   "Asia/Jakarta",
		Payee:      "Jaringan Ethereum",
		Memo:       "biaya gas",
		Postings:   []ledger.Posting{out, in},
	})
	require.NoError(t, err)

	_, err = s.SaveTransaction(ctx, store.Write{
		ActorID:        b.actor,
		Origin:         store.OriginHuman,
		IdempotencyKey: "roundtrip",
		AuditID:        apiTestID("audit"),
		OccurredAt:     time.Date(2026, 3, 7, 9, 0, 0, 0, time.UTC),
	}, txn)
	require.NoError(t, err)
	return b
}

// signedIn builds a router whose ledger side is the real store and whose auth
// side is the package's fakes, then registers and signs in.
//
// Mixing them is deliberate: this test is about what comes out of the
// database, and authentication is covered against its own store elsewhere.
// What it does prove incidentally is that the ledger routes really do sit
// behind requireAuthenticated.
func signedIn(t *testing.T, s *store.Store) (*harness, string) {
	t.Helper()

	h := newHarnessWithLedger(t, &api.LedgerDeps{Idempotency: s, Journal: s})
	h.registered()
	res := h.login(testEmail, testPassword)
	require.Equal(t, http.StatusOK, res.code, res.body)
	cookie := res.sessionCookie()
	require.NotEmpty(t, cookie)
	return h, cookie
}

func TestTheLedgerRoutesRefuseAnUnauthenticatedRequest(t *testing.T) {
	s := realStore(t)
	writeAwkwardBook(t, s)
	h := newHarnessWithLedger(t, &api.LedgerDeps{Idempotency: s, Journal: s})

	for _, path := range []string{
		"/api/v1/commodities", "/api/v1/accounts", "/api/v1/transactions",
	} {
		res := h.do(http.MethodGet, path, nil, "")
		require.Equal(t, http.StatusUnauthorized, res.code, "path %s", path)
		require.Equal(t, "unauthenticated", res.errorCode(t))
	}
}

func TestCommoditiesSurviveTheRoundTrip(t *testing.T) {
	s := realStore(t)
	h, cookie := signedIn(t, s)

	res := h.do(http.MethodGet, "/api/v1/commodities", nil, cookie)
	require.Equal(t, http.StatusOK, res.code, res.body)

	var body struct {
		Items []struct {
			Code  string `json:"code"`
			Kind  string `json:"kind"`
			Scale uint8  `json:"scale"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.body), &body))
	require.Nil(t, body.NextCursor, "an unpaginated listing must say so rather than imply more")

	// Against the domain's own registry, which is the definition the seed is
	// checked against in internal/store. Here the question is narrower: does
	// the scale survive the row, the conversion and the JSON?
	byCode := map[string]uint8{}
	kinds := map[string]string{}
	for _, item := range body.Items {
		byCode[item.Code] = item.Scale
		kinds[item.Code] = item.Kind
	}
	require.Equal(t, uint8(18), byCode["ETH"], "scale 18 did not survive the round trip")
	require.Equal(t, uint8(8), byCode["BTC"])
	require.Equal(t, uint8(2), byCode["IDR"])
	require.Equal(t, "crypto", kinds["ETH"])
	require.Equal(t, "currency", kinds["IDR"])
}

func TestAccountsSurviveTheRoundTrip(t *testing.T) {
	s := realStore(t)
	b := writeAwkwardBook(t, s)
	h, cookie := signedIn(t, s)

	res := h.do(http.MethodGet, "/api/v1/accounts", nil, cookie)
	require.Equal(t, http.StatusOK, res.code, res.body)

	var body struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.body), &body))
	require.Nil(t, body.NextCursor)
	require.Len(t, body.Items, 3)

	byID := map[string]map[string]any{}
	for _, item := range body.Items {
		byID[item["id"].(string)] = item
	}

	brokerage := byID[string(b.brokerage)]
	require.NotNil(t, brokerage)
	// The case this fixture exists for. "" in the domain means "may hold
	// several", and a client checking for an empty string would be reading a
	// Go convention that has no reason to travel.
	require.Nil(t, brokerage["commodity"],
		"an unrestricted account must report null, not an empty string")
	require.Nil(t, brokerage["parent_id"], "a root account has no parent")
	require.Equal(t, "asset", brokerage["kind"])
	require.Equal(t, false, brokerage["closed"])

	wallet := byID[string(b.wallet)]
	require.Equal(t, "ETH", wallet["commodity"])
	require.Equal(t, "Dompet ETH ⛓️", wallet["name"], "text outside the basic plane was altered")

	// One account read on its own must say the same thing as the same account
	// inside the listing. Two paths to one row is two chances to lose a field.
	single := h.do(http.MethodGet, "/api/v1/accounts/"+string(b.brokerage), nil, cookie)
	require.Equal(t, http.StatusOK, single.code, single.body)
	var alone map[string]any
	require.NoError(t, json.Unmarshal([]byte(single.body), &alone))
	require.Equal(t, brokerage, alone)
}

func TestATransactionSurvivesTheRoundTrip(t *testing.T) {
	s := realStore(t)
	b := writeAwkwardBook(t, s)
	h, cookie := signedIn(t, s)

	res := h.do(http.MethodGet, "/api/v1/transactions/"+string(b.transaction), nil, cookie)
	require.Equal(t, http.StatusOK, res.code, res.body)

	var txn struct {
		ID           string  `json:"id"`
		Date         string  `json:"date"`
		OccurredAt   *string `json:"occurred_at"`
		Timezone     *string `json:"timezone"`
		Payee        string  `json:"payee"`
		Memo         string  `json:"memo"`
		ReversesID   *string `json:"reverses_id"`
		ReversalKind *string `json:"reversal_kind"`
		Postings     []struct {
			ID        string `json:"id"`
			AccountID string `json:"account_id"`
			Amount    struct {
				Amount    string `json:"amount"`
				Commodity string `json:"commodity"`
			} `json:"amount"`
			Rate *struct {
				Base  string `json:"base"`
				Quote string `json:"quote"`
				Value string `json:"value"`
			} `json:"rate"`
			Memo       string  `json:"memo"`
			ReversesID *string `json:"reverses_id"`
		} `json:"postings"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.body), &txn))

	require.Equal(t, string(b.transaction), txn.ID)
	require.Equal(t, "2026-03-07", txn.Date)
	require.Equal(t, "Jaringan Ethereum", txn.Payee)
	require.Equal(t, "biaya gas", txn.Memo)
	require.Nil(t, txn.ReversesID)
	require.Nil(t, txn.ReversalKind, "a transaction that undoes nothing says null, not \"\"")
	require.NotNil(t, txn.Timezone)
	require.Equal(t, "Asia/Jakarta", *txn.Timezone)
	require.NotNil(t, txn.OccurredAt)
	// Microseconds, not nanoseconds, and this test is what found that.
	// timestamptz holds microsecond resolution; internal/store truncates on
	// the way in so the loss happens at one named place rather than inside the
	// driver, and the round-trip property test now asserts the same thing at
	// its own level. The three digits that go missing are below anything a
	// reader of a display-only timestamp can use.
	require.Equal(t, "2026-03-07T04:30:15.123456Z", *txn.OccurredAt,
		"the instant did not survive at the resolution the column offers")

	require.Len(t, txn.Postings, 2)

	var debit, credit int
	for _, p := range txn.Postings {
		require.Equal(t, "ETH", p.Amount.Commodity)
		// The amount is a JSON *string*, and it is larger than int64. A client
		// parsing it as a number would already be wrong; so would a server
		// that had narrowed it on the way out.
		require.NotContains(t, res.body, `"amount":`+hugeETH,
			"the amount crossed the wire as a JSON number")
		switch p.Amount.Amount {
		case hugeETH:
			debit++
		case "-" + hugeETH:
			credit++
		default:
			t.Fatalf("amount %q is neither side of the transaction", p.Amount.Amount)
		}

		require.NotNil(t, p.Rate, "the rate recorded on the posting was dropped")
		require.Equal(t, "ETH", p.Rate.Base)
		require.Equal(t, "IDR", p.Rate.Quote)
		// 48001/3 exactly. A decimal column would have rounded it, and a
		// float would have rounded it differently on every machine.
		require.Equal(t, "48001/3", p.Rate.Value)
	}
	require.Equal(t, 1, debit)
	require.Equal(t, 1, credit)

	// The line order is the order the author wrote, which is what the ordinal
	// column exists for.
	require.Equal(t, apiTestID("posting:out"), txn.Postings[0].ID)
	require.Equal(t, "keluar — dengan tanda hubung panjang", txn.Postings[0].Memo)
	require.Equal(t, apiTestID("posting:in"), txn.Postings[1].ID)

	// And the listing must say the same thing as the single read.
	list := h.do(http.MethodGet, "/api/v1/transactions", nil, cookie)
	require.Equal(t, http.StatusOK, list.code, list.body)
	var page struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal([]byte(list.body), &page))
	require.Len(t, page.Items, 1)
	require.Nil(t, page.NextCursor)
	require.JSONEq(t, res.body, string(page.Items[0]),
		"the listing and the single read disagree about the same row")
}

func TestPagingOverHTTPWalksTheWholeJournal(t *testing.T) {
	ctx := context.Background()
	s := realStore(t)
	b := writeAwkwardBook(t, s)
	h, cookie := signedIn(t, s)

	// Five more transactions, all on one civil date, so the walk crosses a
	// page boundary inside a day — the case the identity tie-break exists for.
	for i := 0; i < 5; i++ {
		label := fmt.Sprintf("extra:%d", i)
		out, err := ledger.NewPosting(ledger.PostingSpec{
			ID:      ledger.PostingID(apiTestID("posting:" + label + ":out")),
			Account: b.wallet,
			Amount:  mustParseMoney(t, "ETH", "-1000"),
		})
		require.NoError(t, err)
		in, err := ledger.NewPosting(ledger.PostingSpec{
			ID:      ledger.PostingID(apiTestID("posting:" + label + ":in")),
			Account: b.expenses,
			Amount:  mustParseMoney(t, "ETH", "1000"),
		})
		require.NoError(t, err)
		txn, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID:       ledger.TransactionID(apiTestID("txn:" + label)),
			Date:     mustParseDate(t, "2026-03-07"),
			Payee:    label,
			Postings: []ledger.Posting{out, in},
		})
		require.NoError(t, err)
		_, err = s.SaveTransaction(ctx, store.Write{
			ActorID: b.actor, Origin: store.OriginHuman,
			IdempotencyKey: label, AuditID: apiTestID("audit:" + label),
		}, txn)
		require.NoError(t, err)
	}

	seen := map[string]int{}
	path := "/api/v1/transactions?limit=2"
	pages := 0
	for {
		res := h.do(http.MethodGet, path, nil, cookie)
		require.Equal(t, http.StatusOK, res.code, res.body)
		pages++

		var page struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			NextCursor *string `json:"next_cursor"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.body), &page))
		require.LessOrEqual(t, len(page.Items), 2)
		for _, item := range page.Items {
			seen[item.ID]++
		}
		if page.NextCursor == nil {
			break
		}
		path = "/api/v1/transactions?limit=2&cursor=" + *page.NextCursor
		require.Less(t, pages, 20, "the walk is not terminating")
	}

	require.Len(t, seen, 6, "the walk lost or invented a transaction")
	for id, count := range seen {
		require.Equal(t, 1, count, "transaction %s came back %d times", id, count)
	}
	require.Equal(t, 3, pages, "6 rows at 2 a page")
}

func TestACursorIsRefusedWhenTheFiltersChange(t *testing.T) {
	s := realStore(t)
	b := writeAwkwardBook(t, s)
	h, cookie := signedIn(t, s)
	_ = b

	// Two rows so that a first page hands back a cursor.
	writeSecondTransaction(t, s, b)

	first := h.do(http.MethodGet,
		"/api/v1/transactions?limit=1&from=2026-01-01&to=2026-12-31", nil, cookie)
	require.Equal(t, http.StatusOK, first.code, first.body)
	var page struct {
		NextCursor *string `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal([]byte(first.body), &page))
	require.NotNil(t, page.NextCursor)

	// The same cursor under a wider range. Answering it would hand back a page
	// that looks ordinary and silently omits what the two ranges disagree on.
	res := h.do(http.MethodGet,
		"/api/v1/transactions?limit=1&from=2020-01-01&to=2026-12-31&cursor="+*page.NextCursor,
		nil, cookie)
	require.Equal(t, http.StatusBadRequest, res.code)
	require.Equal(t, "cursor_filter_changed", res.errorCode(t))

	// A corrupt token is a different message, because the caller must be told
	// something different: nothing they did caused it.
	res = h.do(http.MethodGet, "/api/v1/transactions?cursor=not-a-cursor", nil, cookie)
	require.Equal(t, http.StatusBadRequest, res.code)
	require.Equal(t, "invalid_cursor", res.errorCode(t))
}

func writeSecondTransaction(t *testing.T, s *store.Store, b awkwardBook) {
	t.Helper()
	out, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(apiTestID("posting:second:out")),
		Account: b.wallet,
		Amount:  mustParseMoney(t, "ETH", "-2000"),
	})
	require.NoError(t, err)
	in, err := ledger.NewPosting(ledger.PostingSpec{
		ID:      ledger.PostingID(apiTestID("posting:second:in")),
		Account: b.expenses,
		Amount:  mustParseMoney(t, "ETH", "2000"),
	})
	require.NoError(t, err)
	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:       ledger.TransactionID(apiTestID("txn:second")),
		Date:     mustParseDate(t, "2026-04-01"),
		Payee:    "second",
		Postings: []ledger.Posting{out, in},
	})
	require.NoError(t, err)
	_, err = s.SaveTransaction(context.Background(), store.Write{
		ActorID: b.actor, Origin: store.OriginHuman,
		IdempotencyKey: "second", AuditID: apiTestID("audit:second"),
	}, txn)
	require.NoError(t, err)
}

func TestAMissingResourceIsNotFound(t *testing.T) {
	s := realStore(t)
	writeAwkwardBook(t, s)
	h, cookie := signedIn(t, s)

	for _, path := range []string{
		"/api/v1/accounts/" + apiTestID("nobody"),
		"/api/v1/transactions/" + apiTestID("nobody"),
		// A malformed identity is not found rather than a validation failure:
		// the caller asked for something that cannot exist, and telling them
		// which of the two it was distinguishes nothing they can act on.
		"/api/v1/accounts/not-a-uuid",
		"/api/v1/transactions/not-a-uuid",
	} {
		res := h.do(http.MethodGet, path, nil, cookie)
		require.Equal(t, http.StatusNotFound, res.code, "path %s: %s", path, res.body)
		require.Equal(t, "not_found", res.errorCode(t))
	}
}

func mustParseDate(t *testing.T, s string) ledger.Date {
	t.Helper()
	d, err := ledger.ParseDate(s)
	require.NoError(t, err)
	return d
}

func mustParseMoney(t *testing.T, code ledger.CommodityCode, minorUnits string) ledger.Money {
	t.Helper()
	m, err := ledger.ParseMoney(code, minorUnits)
	require.NoError(t, err)
	return m
}

// Parameter validation. These need no database of their own, but they are here
// rather than behind a fake because the handler they exercise is the same one
// the tests above drive, and one fixture is easier to keep honest than two.
func TestListParametersAreRefusedRatherThanIgnored(t *testing.T) {
	s := realStore(t)
	writeAwkwardBook(t, s)
	h, cookie := signedIn(t, s)

	for name, tc := range map[string]struct {
		query string
		field string
	}{
		// Ignoring an unreadable filter returns more rows than the caller
		// asked about, which is a wrong answer wearing the shape of a right
		// one.
		"unparseable from": {"?from=yesterday", "from"},
		"unparseable to":   {"?to=next+week", "to"},
		"impossible date":  {"?from=2026-02-30", "from"},
		// Nobody means "everything between March and February".
		"reversed range": {"?from=2026-03-31&to=2026-03-01", "from"},
		// Refused rather than reduced: a caller that asked for a thousand rows
		// and received two hundred, with nothing saying so, believes it holds
		// the whole set.
		"limit above the maximum": {"?limit=1000", "limit"},
		"limit of zero":           {"?limit=0", "limit"},
		"negative limit":          {"?limit=-1", "limit"},
		"limit is not a number":   {"?limit=lots", "limit"},
	} {
		t.Run(name, func(t *testing.T) {
			res := h.do(http.MethodGet, "/api/v1/transactions"+tc.query, nil, cookie)
			require.Equal(t, http.StatusBadRequest, res.code, res.body)
			require.Equal(t, "invalid_request", res.errorCode(t))
			require.Equal(t, tc.field, res.errorField(t),
				"the response must name which parameter was wrong")
		})
	}

	// The boundary is served rather than refused, so the message about a
	// maximum is true rather than one off.
	res := h.do(http.MethodGet,
		fmt.Sprintf("/api/v1/transactions?limit=%d", store.MaxPageSize), nil, cookie)
	require.Equal(t, http.StatusOK, res.code, res.body)
}

// An empty book is an empty page rather than an error or a null items array. A
// client rendering a list must be able to range over items without checking.
func TestAnEmptyJournalIsAnEmptyPage(t *testing.T) {
	s := realStore(t)
	h, cookie := signedIn(t, s)

	res := h.do(http.MethodGet, "/api/v1/transactions", nil, cookie)
	require.Equal(t, http.StatusOK, res.code, res.body)
	require.JSONEq(t, `{"items":[],"next_cursor":null}`, res.body)
}
