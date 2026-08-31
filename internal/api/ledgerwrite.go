// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GaffaQ/Nusa/internal/ledger"
	"github.com/GaffaQ/Nusa/internal/store"
)

// The write side of the ledger API.
//
// The verbs come from the accounting model rather than from REST habit,
// because §5.3 makes the journal append-only. A transaction is created and
// never edited; a correction and a deletion are both new transactions that
// answer an old one, built by ledger.Reverse. There is no PUT and no PATCH on
// a transaction or a posting anywhere, and no DELETE at all — a reversal needs
// an identity, a date and a map of posting identities, so it could never have
// been bodiless, and a body on DELETE is undefined rather than merely unusual.
//
// Every route here requires an Idempotency-Key and answers through
// writeIdempotentResponse, so a retry repeats the first attempt's status and
// bytes rather than writing again.

// maxLedgerBodyBytes bounds a write.
//
// Larger than the auth bodies because a transaction carries its lines, and
// small enough that a single request cannot be used to spend memory. A
// transaction may hold at most 32.768 postings by the schema's own ceiling, so
// this is the practical limit long before that one is reached.
const maxLedgerBodyBytes = 1 << 20

// JournalWriter is the write side of the repository, as this package needs it.
type JournalWriter interface {
	SaveAccount(ctx context.Context, account ledger.Account) error
	UpdateAccount(ctx context.Context, id ledger.AccountID, name string, closed bool) (ledger.Account, error)
	SaveTransaction(ctx context.Context, w store.Write, txn ledger.Transaction, lots ...ledger.Lot) (store.Result, error)
	SaveDisposal(ctx context.Context, w store.Write, txn ledger.Transaction, disposing ...ledger.PostingID) (store.DisposalResult, error)
}

// ---------------------------------------------------------------- requests

// createAccountRequest is the body of POST /accounts.
type createAccountRequest struct {
	// ID is supplied by the caller. Unusual for REST, and §5.7 rather than a
	// preference: nothing here mints an identity for something that can be
	// pointed at, so a retry carries the same one and is recognisable as the
	// same write.
	ID        string  `json:"id"`
	ParentID  *string `json:"parent_id"`
	Kind      string  `json:"kind"`
	Name      string  `json:"name"`
	Commodity *string `json:"commodity"`
	Closed    bool    `json:"closed"`
}

// patchAccountRequest is the body of PATCH /accounts/{id}.
//
// Every field is a pointer so that absent and zero are distinguishable: a
// caller clearing a name and a caller not mentioning it are different
// requests, and a bool that is false by default would make "reopen this
// account" indistinguishable from silence.
//
// Kind, ParentID and Commodity are declared here for one reason: so that
// sending them can be refused by name. Leaving them out of the struct would
// make DisallowUnknownFields reject them as unknown, which is true of a
// misspelling and false of these — they are real fields of an account that
// this endpoint will not change.
type patchAccountRequest struct {
	Name   *string `json:"name"`
	Closed *bool   `json:"closed"`

	Kind      *string `json:"kind"`
	ParentID  *string `json:"parent_id"`
	Commodity *string `json:"commodity"`
}

// postingRequest is one line of a transaction on the way in.
type postingRequest struct {
	ID        string       `json:"id"`
	AccountID string       `json:"account_id"`
	Amount    ledger.Money `json:"amount"`
	Rate      *ledger.Rate `json:"rate"`
	Memo      string       `json:"memo"`
}

// createTransactionRequest is the body of POST /transactions.
type createTransactionRequest struct {
	ID         string           `json:"id"`
	Date       ledger.Date      `json:"date"`
	OccurredAt *string          `json:"occurred_at"`
	Timezone   *string          `json:"timezone"`
	Payee      string           `json:"payee"`
	Memo       string           `json:"memo"`
	Postings   []postingRequest `json:"postings"`

	// Disposing names the lines that reduce a holding, so the lots they draw
	// on are recorded.
	//
	// PROVISIONAL — see the note on the response below. The field itself is
	// not a design choice: SaveDisposal derives the account and the quantity
	// from the line, deliberately, and the one thing it cannot derive is which
	// lines are disposals rather than ordinary reductions. Inferring that from
	// "the account happens to hold lots" is exactly the inference the store
	// declines to make. So this is the minimum restatement of the one fact the
	// store must be told, and M7 cannot make it smaller.
	//
	// What is provisional is the *response*: this endpoint reports the
	// transaction and nothing about the lots consumed, because encoding a
	// ledger.Consumption means deciding how an unrounded ledger.Rat crosses
	// the wire, and that is an investment-API question with no UI to test it
	// against yet (§4.7). M7 revisits it, and the change it may make is
	// additive.
	Disposing []string `json:"disposing"`

	// ReversesID and ReversalKind are declared so that sending them can be
	// refused rather than ignored. ledger.Reverse is the only thing that fills
	// them in; a client that could hand-craft a reversal could write a link
	// between two transactions that answer nothing about each other, and the
	// audit trail would record it as an ordinary create.
	ReversesID   *string `json:"reverses_id"`
	ReversalKind *string `json:"reversal_kind"`
}

// reversalRequest is the body of both /corrections and /deletions.
//
// One shape for both, because they are one mechanism — ledger.Reverse with a
// Kind — and two shapes would be two places for §5.3 to drift apart.
type reversalRequest struct {
	ID         string      `json:"id"`
	Date       ledger.Date `json:"date"`
	OccurredAt *string     `json:"occurred_at"`
	Timezone   *string     `json:"timezone"`
	Memo       string      `json:"memo"`

	// Postings maps each posting of the original to the identity its answering
	// line will carry. A map keyed by the original's identity rather than a
	// list in posting order, because §5.7 forbids pointing at a posting by its
	// position: a reordered list would attach a reversing line to the wrong
	// original with no error anywhere.
	//
	// What counts as a valid map is ledger.Reverse's decision and is not
	// second-guessed here. It requires exactly one entry per original line, no
	// two entries sharing an identity, and every original answered — so a key
	// naming a posting that belongs to another transaction is refused by the
	// same rule that refuses a missing one, and the handler adds nothing.
	Postings map[string]string `json:"postings"`
}

// ---------------------------------------------------------------- handlers

func handleCreateAccount(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createAccountRequest
		if !decodeLedgerBody(w, d, r, &req) {
			return
		}

		kind, err := parseAccountKind(req.Kind)
		if err != nil {
			writeValidationFailed(w, d, "kind", err)
			return
		}

		account, err := ledger.NewAccount(ledger.AccountSpec{
			ID:        ledger.AccountID(req.ID),
			Parent:    ledger.AccountID(deref(req.ParentID)),
			Kind:      kind,
			Name:      req.Name,
			Commodity: ledger.CommodityCode(deref(req.Commodity)),
			Closed:    req.Closed,
		})
		if err != nil {
			writeValidationFailed(w, d, fieldForDomainError(err), err)
			return
		}

		if err := d.Ledger.Writer.SaveAccount(r.Context(), account); err != nil {
			writeWriteError(w, d, "save account", err)
			return
		}

		// Accounts are not written through the idempotency machinery: they
		// carry no store.Write, because SaveAccount is a single insert whose
		// own primary key already makes a replay a conflict rather than a
		// duplicate. The key is still required on the way in, so a client
		// retrying gets a conflict it can recognise instead of a second row.
		writeJSON(w, d.Logger, http.StatusCreated, accountToDTO(account))
	}
}

func handlePatchAccount(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := ledger.ValidateID(id); err != nil {
			writeNotFound(w, d, "account")
			return
		}

		var req patchAccountRequest
		if !decodeLedgerBody(w, d, r, &req) {
			return
		}

		// Refused by name rather than ignored. A client that sends a kind and
		// receives 200 will believe the kind changed, and the next thing it
		// does is built on that belief.
		for field, present := range map[string]bool{
			"kind":      req.Kind != nil,
			"parent_id": req.ParentID != nil,
			"commodity": req.Commodity != nil,
		} {
			if present {
				writeErrorDetail(w, d.Logger, http.StatusUnprocessableEntity, errorDetail{
					Code:    CodeFieldImmutable,
					Message: field + " cannot be changed once an account exists",
					Field:   field,
					Details: map[string]string{
						"field":  field,
						"reason": "postings already written depend on it",
					},
				})
				return
			}
		}

		current, err := d.Ledger.Journal.LoadAccount(r.Context(), ledger.AccountID(id))
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeNotFound(w, d, "account")
			return
		case err != nil:
			writeInternalError(w, d.Logger, "load account", err)
			return
		}

		name, closed := current.Name(), current.IsClosed()
		if req.Name != nil {
			name = *req.Name
		}
		if req.Closed != nil {
			closed = *req.Closed
		}

		updated, err := d.Ledger.Writer.UpdateAccount(r.Context(), ledger.AccountID(id), name, closed)
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeNotFound(w, d, "account")
			return
		case err != nil:
			writeWriteError(w, d, "update account", err)
			return
		}
		writeJSON(w, d.Logger, http.StatusOK, accountToDTO(updated))
	}
}

func handleCreateTransaction(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createTransactionRequest
		if !decodeLedgerBody(w, d, r, &req) {
			return
		}

		// Refused, not ignored: see the field's own note.
		for field, present := range map[string]bool{
			"reverses_id":   req.ReversesID != nil,
			"reversal_kind": req.ReversalKind != nil,
		} {
			if present {
				writeErrorDetail(w, d.Logger, http.StatusUnprocessableEntity, errorDetail{
					Code:    CodeFieldImmutable,
					Message: field + " is set by a correction or a deletion, never by a create",
					Field:   field,
					Details: map[string]string{
						"field": field,
						"use":   "POST /api/v1/transactions/{id}/corrections or /deletions",
					},
				})
				return
			}
		}

		postings, err := postingsFromRequest(req.Postings)
		if err != nil {
			writeValidationFailed(w, d, fieldForDomainError(err), err)
			return
		}

		occurred, err := parseInstant(req.OccurredAt)
		if err != nil {
			writeValidationFailed(w, d, "occurred_at", err)
			return
		}

		txn, err := ledger.NewTransaction(ledger.TransactionSpec{
			ID:         ledger.TransactionID(req.ID),
			Date:       req.Date,
			OccurredAt: occurred,
			Timezone:   deref(req.Timezone),
			Payee:      req.Payee,
			Memo:       req.Memo,
			Postings:   postings,
		})
		if err != nil {
			writeValidationFailed(w, d, fieldForDomainError(err), err)
			return
		}

		writeTransaction(w, r, d, txn, req.Disposing)
	}
}

func handleReversal(d Deps, kind ledger.ReversalKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := ledger.ValidateID(id); err != nil {
			writeNotFound(w, d, "transaction")
			return
		}

		var req reversalRequest
		if !decodeLedgerBody(w, d, r, &req) {
			return
		}

		original, err := d.Ledger.Journal.LoadTransaction(r.Context(), ledger.TransactionID(id))
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeNotFound(w, d, "transaction")
			return
		case err != nil:
			writeInternalError(w, d.Logger, "load transaction", err)
			return
		}

		occurred, err := parseInstant(req.OccurredAt)
		if err != nil {
			writeValidationFailed(w, d, "occurred_at", err)
			return
		}

		lines := make(map[ledger.PostingID]ledger.PostingID, len(req.Postings))
		for from, to := range req.Postings {
			lines[ledger.PostingID(from)] = ledger.PostingID(to)
		}

		// The date is required and is never defaulted. Booking a reversal
		// today leaves last year's report intact; booking it on the original's
		// date rewrites that period. Which is right is an accounting decision
		// (§5.4), and ledger.Reverse refuses to guess it — so a missing date
		// arrives here as the zero Date and is refused there, by name.
		reversal, err := ledger.Reverse(original, ledger.ReversalSpec{
			ID:         ledger.TransactionID(req.ID),
			Kind:       kind,
			Date:       req.Date,
			OccurredAt: occurred,
			Timezone:   deref(req.Timezone),
			Memo:       req.Memo,
			Postings:   lines,
		})
		if err != nil {
			writeValidationFailed(w, d, fieldForDomainError(err), err)
			return
		}

		writeTransaction(w, r, d, reversal, nil)
	}
}

// writeTransaction is the tail every transaction write shares.
func writeTransaction(
	w http.ResponseWriter, r *http.Request, d Deps,
	txn ledger.Transaction, disposing []string,
) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		writeInternalError(w, d.Logger, "ledger write outside requireAuthenticated",
			errNoIdempotencyKey)
		return
	}
	key, ok := idempotencyKeyFrom(r.Context())
	if !ok {
		writeInternalError(w, d.Logger, "ledger write outside requireIdempotencyKey",
			errNoIdempotencyKey)
		return
	}

	auditID, err := d.Auth.NewID()
	if err != nil {
		writeInternalError(w, d.Logger, "mint audit id", err)
		return
	}

	write := store.Write{
		ActorID:        session.UserID,
		Origin:         store.OriginHuman,
		IdempotencyKey: key,
		AuditID:        auditID,
		OccurredAt:     d.Auth.Now().UTC(),
	}

	var result store.Result
	if len(disposing) == 0 {
		result, err = d.Ledger.Writer.SaveTransaction(r.Context(), write, txn)
	} else {
		lines := make([]ledger.PostingID, 0, len(disposing))
		for _, id := range disposing {
			lines = append(lines, ledger.PostingID(id))
		}
		var disposal store.DisposalResult
		disposal, err = d.Ledger.Writer.SaveDisposal(r.Context(), write, txn, lines...)
		// Deliberately only the embedded Result. disposal.Consumed says which
		// lots paid for the sale, and reporting it means encoding a
		// ledger.Rat; that is M7's decision, so nothing about it leaves here.
		result = disposal.Result
	}
	if err != nil {
		writeWriteError(w, d, "save transaction", err)
		return
	}

	writeIdempotentResponse(w, r, d, session.UserID, result.Replayed,
		http.StatusCreated, transactionToDTO(txn))
}

// ---------------------------------------------------------------- helpers

// decodeLedgerBody reads a request body, refusing anything it cannot read
// exactly.
//
// Unknown fields are refused rather than ignored: a client that misspells
// "commodity" and receives 201 has created something other than what it asked
// for, and nothing in the response says so.
func decodeLedgerBody(w http.ResponseWriter, d Deps, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLedgerBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		writeErrorDetail(w, d.Logger, http.StatusBadRequest, errorDetail{
			Code:    CodeInvalidRequest,
			Message: "the request body could not be read: " + err.Error(),
		})
		return false
	}
	return true
}

func postingsFromRequest(lines []postingRequest) ([]ledger.Posting, error) {
	postings := make([]ledger.Posting, 0, len(lines))
	for i, line := range lines {
		spec := ledger.PostingSpec{
			ID:      ledger.PostingID(line.ID),
			Account: ledger.AccountID(line.AccountID),
			Amount:  line.Amount,
			Memo:    line.Memo,
		}
		if line.Rate != nil {
			spec.Rate = *line.Rate
		}
		posting, err := ledger.NewPosting(spec)
		if err != nil {
			return nil, fmt.Errorf("postings.%d: %w", i, err)
		}
		postings = append(postings, posting)
	}
	return postings, nil
}

// parseInstant reads the optional display-only timestamp.
func parseInstant(raw *string) (time.Time, error) {
	if raw == nil || *raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(rfc3339Nano, *raw)
}

func parseAccountKind(name string) (ledger.AccountKind, error) {
	for _, k := range []ledger.AccountKind{
		ledger.AccountAsset, ledger.AccountLiability, ledger.AccountEquity,
		ledger.AccountIncome, ledger.AccountExpense,
	} {
		if k.String() == name {
			return k, nil
		}
	}
	return ledger.AccountUnknown, fmt.Errorf(
		"%w: %q is not one of asset, liability, equity, income, expense",
		ledger.ErrInvalidAccount, name)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// fieldForDomainError names the field a domain refusal is about, where the
// domain's own error is specific enough to say.
//
// It is a best effort and is documented as one: the value of `field` is that a
// form can highlight something, and a wrong guess would highlight the wrong
// box. Where nothing can be said honestly it says nothing, and the caller
// reads `details` instead.
func fieldForDomainError(err error) string {
	switch {
	case errors.Is(err, ledger.ErrUnbalanced):
		return "postings"
	case errors.Is(err, ledger.ErrInvalidDate):
		return "date"
	case errors.Is(err, ledger.ErrInvalidReversal):
		return "postings"
	case errors.Is(err, ledger.ErrInvalidText):
		return "memo"
	case errors.Is(err, ledger.ErrCommodityMismatch), errors.Is(err, ledger.ErrUnknownCommodity):
		return "postings"
	case errors.Is(err, ledger.ErrUnknownAccount):
		return "postings"
	}
	// The prefix postingsFromRequest attaches, so a line-level failure names
	// its line even when the domain error itself does not.
	if i := strings.Index(err.Error(), "postings."); i == 0 {
		if j := strings.Index(err.Error(), ":"); j > 0 {
			return err.Error()[:j]
		}
	}
	return ""
}

// writeValidationFailed reports a refusal the ledger made.
//
// The code is one code, per §13's rule: a client acts the same way for all of
// them — show the message against the field and let the person fix it. What
// distinguishes them is `field` and `details`, which are machine-readable and
// carry no prose, because prose here is a user-facing string that went around
// the catalogue.
func writeValidationFailed(w http.ResponseWriter, d Deps, field string, err error) {
	details := map[string]string{"reason": domainReason(err)}
	writeErrorDetail(w, d.Logger, http.StatusUnprocessableEntity, errorDetail{
		Code:    CodeValidationFailed,
		Message: err.Error(),
		Field:   field,
		Details: details,
	})
}

// domainReason names which rule was broken, as a stable token rather than as
// English. It is what a client switches on when it wants to say something more
// specific than "that did not work".
func domainReason(err error) string {
	for reason, sentinel := range map[string]error{
		"unbalanced":         ledger.ErrUnbalanced,
		"unknown_account":    ledger.ErrUnknownAccount,
		"unknown_commodity":  ledger.ErrUnknownCommodity,
		"commodity_mismatch": ledger.ErrCommodityMismatch,
		"invalid_reversal":   ledger.ErrInvalidReversal,
		"duplicate_id":       ledger.ErrDuplicateID,
		"invalid_id":         ledger.ErrInvalidID,
		"invalid_date":       ledger.ErrInvalidDate,
		"invalid_money":      ledger.ErrInvalidMoney,
		"invalid_rate":       ledger.ErrInvalidRate,
		"invalid_text":       ledger.ErrInvalidText,
		"invalid_account":    ledger.ErrInvalidAccount,
		"insufficient_lots":  ledger.ErrInsufficientLots,
	} {
		if errors.Is(err, sentinel) {
			return reason
		}
	}
	return "invalid"
}

// writeWriteError maps a repository refusal onto a response.
func writeWriteError(w http.ResponseWriter, d Deps, what string, err error) {
	switch {
	case errors.Is(err, store.ErrIdempotencyConflict):
		writeIdempotencyConflict(w, d)
	case errors.Is(err, store.ErrAlreadyReversed):
		writeErrorDetail(w, d.Logger, http.StatusConflict, errorDetail{
			Code:    CodeAlreadyReversed,
			Message: "that transaction has already been reversed",
			Field:   "id",
		})
	case errors.Is(err, store.ErrNotFound):
		writeNotFound(w, d, "transaction")
	case errors.Is(err, store.ErrInvalidWrite), isDomainError(err):
		writeValidationFailed(w, d, fieldForDomainError(err), err)
	default:
		writeInternalError(w, d.Logger, what, err)
	}
}

// isDomainError reports an error the domain raised, which the store passes
// through unwrapped: a transaction that does not balance is the domain saying
// so, and restating it in the store would give one fact two names.
func isDomainError(err error) bool {
	for _, sentinel := range []error{
		ledger.ErrUnbalanced, ledger.ErrUnknownAccount, ledger.ErrUnknownCommodity,
		ledger.ErrCommodityMismatch, ledger.ErrInvalidReversal, ledger.ErrDuplicateID,
		ledger.ErrInvalidID, ledger.ErrInvalidText, ledger.ErrInvalidAccount,
		ledger.ErrInsufficientLots, ledger.ErrInvalidTransaction, ledger.ErrAccountCycle,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}
