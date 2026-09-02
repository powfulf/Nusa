// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/powfulf/Nusa/internal/ledger"
	"github.com/powfulf/Nusa/internal/store"
)

// The read side of the ledger API.
//
// Every one of these is authorised by holding a session and by nothing
// narrower: there is no user_id on accounts or transactions, because an
// instance holds at most one credentialed account today. That is recorded as a
// decision with an expiry in §13 — M9 adds households, drops the index the
// decision rests on, and has to reopen this along with per-account rate
// limiting.

// JournalReader is the read side of the repository, as this package needs it.
//
// It is declared here rather than taken as a *store.Store so that a handler
// test can stand something else in its place — but every guard about what the
// database actually returns lives in internal/store against PostgreSQL, and
// the HTTP round trip is proved against a real one too. A fake returns
// whatever it was handed, which is exactly what a row-to-domain conversion
// does not (§11).
type JournalReader interface {
	LoadCommodities(ctx context.Context) ([]ledger.Commodity, error)
	LoadAccounts(ctx context.Context) ([]ledger.Account, error)
	LoadAccount(ctx context.Context, id ledger.AccountID) (ledger.Account, error)
	LoadTransaction(ctx context.Context, id ledger.TransactionID) (ledger.Transaction, error)
	TransactionsPage(ctx context.Context, q store.TransactionQuery) (store.TransactionPage, error)
}

// handleListCommodities returns every commodity this instance knows.
//
// Unpaginated, and next_cursor is always null. Commodities are reference data:
// core seeds a few dozen and a Country Pack adds a country's worth, so the
// whole set is the honest unit to read. It uses the same envelope as the
// paginated endpoints so that a client needs no special case, and so that
// paginating it later is not a breaking change.
func handleListCommodities(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		commodities, err := d.Ledger.Journal.LoadCommodities(r.Context())
		if err != nil {
			writeInternalError(w, d.Logger, "load commodities", err)
			return
		}

		items := make([]commodityDTO, 0, len(commodities))
		for _, c := range commodities {
			items = append(items, commodityToDTO(c))
		}
		writeJSON(w, d.Logger, http.StatusOK, listEnvelope{Items: items})
	}
}

// handleListAccounts returns the whole account tree.
//
// Unpaginated for a reason the store states in its own query: building a tree
// needs every ancestor of every account in it, and an account tree is tens to
// low hundreds of rows. A cursor here would be an endpoint that never produces
// a second page, and it would contradict the reasoning the read is built on.
func handleListAccounts(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accounts, err := d.Ledger.Journal.LoadAccounts(r.Context())
		if err != nil {
			writeInternalError(w, d.Logger, "load accounts", err)
			return
		}

		items := make([]accountDTO, 0, len(accounts))
		for _, a := range accounts {
			items = append(items, accountToDTO(a))
		}
		writeJSON(w, d.Logger, http.StatusOK, listEnvelope{Items: items})
	}
}

// handleGetAccount returns one account.
func handleGetAccount(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := ledger.ValidateID(id); err != nil {
			writeNotFound(w, d, "account")
			return
		}

		account, err := d.Ledger.Journal.LoadAccount(r.Context(), ledger.AccountID(id))
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeNotFound(w, d, "account")
			return
		case err != nil:
			writeInternalError(w, d.Logger, "load account", err)
			return
		}
		writeJSON(w, d.Logger, http.StatusOK, accountToDTO(account))
	}
}

// handleGetTransaction returns one transaction with its lines.
func handleGetTransaction(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := ledger.ValidateID(id); err != nil {
			writeNotFound(w, d, "transaction")
			return
		}

		txn, err := d.Ledger.Journal.LoadTransaction(r.Context(), ledger.TransactionID(id))
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeNotFound(w, d, "transaction")
			return
		case err != nil:
			writeInternalError(w, d.Logger, "load transaction", err)
			return
		}
		writeJSON(w, d.Logger, http.StatusOK, transactionToDTO(txn))
	}
}

// handleListTransactions returns one page of the journal.
func handleListTransactions(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		filters, ok := parseTransactionFilters(w, d, query)
		if !ok {
			return
		}

		limit, ok := parsePageSize(w, d, query.Get("limit"))
		if !ok {
			return
		}

		q := store.TransactionQuery{From: filters.From, To: filters.To, Limit: limit}
		if token := query.Get("cursor"); token != "" {
			date, id, err := decodeCursor(filters, token)
			if err != nil {
				writeCursorError(w, d, err)
				return
			}
			q.After = store.TransactionCursor{Date: date, ID: id}
		}

		page, err := d.Ledger.Journal.TransactionsPage(r.Context(), q)
		if err != nil {
			writeInternalError(w, d.Logger, "list transactions", err)
			return
		}

		items := make([]transactionDTO, 0, len(page.Transactions))
		for _, txn := range page.Transactions {
			items = append(items, transactionToDTO(txn))
		}

		envelope := listEnvelope{Items: items}
		if !page.Next.IsZero() {
			// The cursor is bound to the filters this page was produced under,
			// so presenting it with different ones is refused rather than
			// answered with a page that quietly means something else.
			token := encodeCursor(filters, page.Next.Date, page.Next.ID)
			envelope.NextCursor = &token
		}
		writeJSON(w, d.Logger, http.StatusOK, envelope)
	}
}

// parseTransactionFilters reads the date bounds, refusing what it cannot read.
//
// An unparseable date is refused rather than ignored. Dropping a filter the
// caller asked for returns more rows than they asked about, which is the kind
// of wrong answer that looks like a correct one.
func parseTransactionFilters(
	w http.ResponseWriter, d Deps, query map[string][]string,
) (transactionFilters, bool) {
	var f transactionFilters
	for field, into := range map[string]*ledger.Date{"from": &f.From, "to": &f.To} {
		values := query[field]
		if len(values) == 0 || values[0] == "" {
			continue
		}
		date, err := ledger.ParseDate(values[0])
		if err != nil {
			writeErrorDetail(w, d.Logger, http.StatusBadRequest, errorDetail{
				Code:    CodeInvalidRequest,
				Message: "the " + field + " parameter must be a date as YYYY-MM-DD",
				Field:   field,
			})
			return transactionFilters{}, false
		}
		*into = date
	}

	// A range that cannot contain anything is a mistake rather than an empty
	// answer: nobody means "everything between March and February".
	if !f.From.IsZero() && !f.To.IsZero() && f.From.After(f.To) {
		writeErrorDetail(w, d.Logger, http.StatusBadRequest, errorDetail{
			Code:    CodeInvalidRequest,
			Message: "from is after to, so the range is empty",
			Field:   "from",
			Details: map[string]string{"from": f.From.String(), "to": f.To.String()},
		})
		return transactionFilters{}, false
	}
	return f, true
}

// parsePageSize reads the limit, refusing a size it will not serve rather than
// quietly reducing it.
//
// A caller that asked for a thousand rows and received two hundred, with
// nothing in the response saying so, will believe it holds the whole set.
func parsePageSize(w http.ResponseWriter, d Deps, raw string) (int, bool) {
	if raw == "" {
		return 0, true // the store applies its own default
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > store.MaxPageSize {
		writeErrorDetail(w, d.Logger, http.StatusBadRequest, errorDetail{
			Code:    CodeInvalidRequest,
			Message: "limit must be a whole number between 1 and " + strconv.Itoa(store.MaxPageSize),
			Field:   "limit",
			Details: map[string]string{"maximum": strconv.Itoa(store.MaxPageSize)},
		})
		return 0, false
	}
	return limit, true
}

// writeCursorError separates a cursor that no longer matches its filters from
// one that is simply unreadable.
//
// The distinction earns its code: a client whose filters changed must restart
// the walk with the filters it now wants, and a client with a corrupt token
// must restart from the beginning. Both are "start again", but only the first
// tells the user their filter change took effect.
func writeCursorError(w http.ResponseWriter, d Deps, err error) {
	code := CodeInvalidCursor
	message := "the cursor could not be read; start the listing again"
	if errors.Is(err, errCursorFilterChanged) {
		code = CodeCursorFilterChanged
		message = "the cursor belongs to a different filter set; start the listing again"
	}
	writeErrorDetail(w, d.Logger, http.StatusBadRequest, errorDetail{
		Code:    code,
		Message: message,
		Field:   "cursor",
	})
}

// writeNotFound answers for a resource that is not there.
//
// The kind travels in details rather than in the code, because a client acts
// the same way for all of them and §13's rule is that a code exists only when
// a caller would do something different.
func writeNotFound(w http.ResponseWriter, d Deps, kind string) {
	writeErrorDetail(w, d.Logger, http.StatusNotFound, errorDetail{
		Code:    CodeNotFound,
		Message: "no such " + kind,
		Details: map[string]string{"kind": kind},
	})
}
