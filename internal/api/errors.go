// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"errors"
	"log/slog"
	"net/http"
)

// Structured errors, as §5 of the M2 prompt requires: a stable code, a
// developer-facing message, an optional field, and optional details.
//
// The code is the part that matters and the part that must not churn. It is
// what a client translates, so it is a stable identifier rather than English:
// the message beside it is for a developer reading a log, never for a person
// reading a screen. §7 is explicit that no user-facing string is produced
// here — the frontend renders `code` through its own catalogue.

// ErrorCode identifies a failure in a way a client can act on.
type ErrorCode string

// The codes this layer produces. Every one is stable; adding is cheap and
// renaming is a breaking change.
const (
	// CodeInvalidRequest is a malformed or unreadable request body.
	CodeInvalidRequest ErrorCode = "invalid_request"

	// CodeInvalidCredentials covers every way a sign-in can fail on the
	// credential itself.
	//
	// It is deliberately one code for several causes: no such account, wrong
	// password, wrong one-time code, spent backup code. Distinguishing them
	// would tell an anonymous caller which addresses are registered, which is
	// exactly the enumeration the whole login path is arranged to prevent.
	CodeInvalidCredentials ErrorCode = "invalid_credentials" //nolint:gosec // G101: an error code, not a credential

	// CodeSecondFactorRequired reports a session that has passed the password
	// step and has not yet passed the second one. It is not an error in the
	// ordinary sense: it names the next step.
	CodeSecondFactorRequired ErrorCode = "second_factor_required"

	// CodeUnauthenticated reports a request with no usable session.
	CodeUnauthenticated ErrorCode = "unauthenticated"

	// CodeRateLimited reports too many failed sign-in attempts from one
	// address.
	CodeRateLimited ErrorCode = "rate_limited"

	// CodeRegistrationUnavailable reports a registration that will not be
	// accepted.
	//
	// One code for two causes, for the same reason as CodeInvalidCredentials:
	// "registration is closed" tells an anonymous caller that somebody is
	// already using this instance, and "that address is taken" tells them who.
	// Both are the same shape of leak as account enumeration, and both answer
	// with this.
	CodeRegistrationUnavailable ErrorCode = "registration_unavailable"

	// CodeIdempotencyKeyRequired reports a mutation sent without the header.
	//
	// It is separate from CodeInvalidRequest because the caller acts
	// differently: a malformed body is fixed by changing what was sent, and
	// this is fixed by adding something that was never there.
	CodeIdempotencyKeyRequired ErrorCode = "idempotency_key_required"

	// CodeIdempotencyKeyReused reports a key already claimed for a different
	// request. The caller must mint a new key, not retry this one.
	CodeIdempotencyKeyReused ErrorCode = "idempotency_key_reused"

	// CodeNotFound reports a resource that is not there. One code for every
	// kind: a client acts the same way whichever it was, and the kind travels
	// in details for a person reading a log.
	CodeNotFound ErrorCode = "not_found"

	// CodeInvalidCursor reports a cursor this server cannot read — corrupt,
	// truncated, or from an encoding it no longer uses. The caller restarts
	// the listing from the beginning.
	CodeInvalidCursor ErrorCode = "invalid_cursor"

	// CodeCursorFilterChanged reports a cursor presented with filters other
	// than the ones it was issued under.
	//
	// It is separate from CodeInvalidCursor because the person reading the
	// screen must be told something different: their filter change did take
	// effect, and the listing restarted because of it rather than because
	// anything went wrong.
	CodeCursorFilterChanged ErrorCode = "cursor_filter_changed"

	// CodeValidationFailed reports a refusal the ledger made.
	//
	// One code for every domain rule, per the rule in §13: a client acts the
	// same way for all of them, which is to show the message against the field
	// and let the person fix it. What separates them is `field` and the
	// machine-readable `reason` in `details` — a client that wants to say
	// something more specific switches on that rather than on a code per way
	// of being wrong, which is how an error vocabulary becomes a second copy
	// of the domain's own taxonomy.
	CodeValidationFailed ErrorCode = "validation_failed"

	// CodeFieldImmutable reports a field that cannot be changed once the thing
	// exists — an account's kind, or a transaction's reversal link.
	//
	// Separate from CodeValidationFailed because the caller acts differently:
	// nothing they can put in that field will work, so the fix is to stop
	// sending it rather than to correct it.
	CodeFieldImmutable ErrorCode = "field_immutable"

	// CodeAlreadyReversed reports a second attempt to reverse one entry. The
	// caller must read the book again before deciding what to do.
	CodeAlreadyReversed ErrorCode = "already_reversed"

	// CodeSecondFactorAlreadySet reports an enrolment where one is already
	// confirmed. Separate from a validation failure because the caller acts
	// differently: they must remove the existing factor first, and nothing
	// they change about this request will help.
	CodeSecondFactorAlreadySet ErrorCode = "second_factor_already_set"

	// CodeInternal reports a fault on this side. The response carries nothing
	// about it; the log carries everything.
	CodeInternal ErrorCode = "internal"
)

// allErrorCodes is every code this package produces.
//
// Go cannot enumerate the constants above, and the OpenAPI document has to
// list them: a spec naming a code the server never sends is a catalogue entry
// a client translates for nothing, and a code the server sends that the spec
// omits is a string a client has no rendering for. So the list exists, and a
// guard checks it against the constant block by reading this file — otherwise
// the list itself becomes the third place to forget.
var allErrorCodes = []ErrorCode{
	CodeInvalidRequest,
	CodeInvalidCredentials,
	CodeSecondFactorRequired,
	CodeSecondFactorAlreadySet,
	CodeUnauthenticated,
	CodeRateLimited,
	CodeRegistrationUnavailable,
	CodeNotFound,
	CodeInvalidCursor,
	CodeCursorFilterChanged,
	CodeIdempotencyKeyRequired,
	CodeIdempotencyKeyReused,
	CodeValidationFailed,
	CodeFieldImmutable,
	CodeAlreadyReversed,
	CodeInternal,
}

// errorBody is the shape every failure crosses the wire in.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Field   string    `json:"field,omitempty"`

	// Details carries whatever a client needs to act, such as how long to wait
	// before retrying. Never anything that distinguishes one failure cause
	// from another where the code deliberately does not.
	Details map[string]string `json:"details,omitempty"`
}

// writeError sends a structured failure.
func writeError(w http.ResponseWriter, logger *slog.Logger, status int, code ErrorCode, message string) {
	writeErrorDetail(w, logger, status, errorDetail{Code: code, Message: message})
}

func writeErrorDetail(w http.ResponseWriter, logger *slog.Logger, status int, detail errorDetail) {
	writeJSON(w, logger, status, errorBody{Error: detail})
}

// writeInternalError logs the cause and tells the caller nothing about it.
//
// The split is the point. An error from the database routinely carries a
// hostname, a username, a query, or a column name, and any of those in a
// response body is a gift to whoever is probing. The caller learns that
// something broke on this side; the operator learns what.
func writeInternalError(w http.ResponseWriter, logger *slog.Logger, what string, err error) {
	logger.Error(what, slog.String("error", err.Error()))
	writeError(w, logger, http.StatusInternalServerError, CodeInternal, "internal error")
}

// errNoIdempotencyKey marks a handler reached without requireIdempotencyKey in
// front of it. It never travels to a caller — it exists so the log line says
// which mistake was made rather than reporting a bare internal error.
var errNoIdempotencyKey = errors.New("no idempotency key in request context")

// errNoSession marks a handler reached without requireAuthenticated in front
// of it. Like errNoIdempotencyKey it never travels to a caller; it exists so
// the log names the mistake.
var errNoSession = errors.New("no session in request context")
